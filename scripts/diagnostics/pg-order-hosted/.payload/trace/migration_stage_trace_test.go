package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type migrationStageEvent struct {
	Category string `json:"category"`
	Step     string `json:"step"`
	Offset   int64  `json:"offset_ns"`
	Duration int64  `json:"duration_ns"`
	PID      uint32 `json:"pid,omitempty"`
	Error    string `json:"error,omitempty"`
}

type migrationStageSample struct {
	Leaf     string                `json:"leaf"`
	Cycle    int                   `json:"cycle"`
	Phase    string                `json:"phase"`
	Duration int64                 `json:"duration_ns"`
	Events   []migrationStageEvent `json:"events"`
	start    time.Time
}

type migrationSampleKey struct{}
type migrationSpanKey string

type migrationStageTracer struct{}

var (
	_ pgx.QueryTracer       = migrationStageTracer{}
	_ pgx.PrepareTracer     = migrationStageTracer{}
	_ pgxpool.AcquireTracer = migrationStageTracer{}
)

func migrationSQLStage(sql string) string {
	sql = historyReadTraceSQL(sql)
	switch {
	case sql == "SELECT pg_advisory_lock($1)":
		return "lock"
	case sql == "SELECT pg_advisory_unlock($1)":
		return "unlock"
	case strings.HasPrefix(sql, "CREATE TABLE IF NOT EXISTS _migrations"):
		return "init"
	case sql == "SELECT pg_catalog.to_regclass('_migrations') IS NOT NULL":
		return "catalog"
	case sql == historySessionMetadata:
		return "catalog-session"
	case strings.HasPrefix(sql, "SELECT name, checksum FROM _migrations"):
		return "checksum"
	case strings.HasPrefix(sql, "SELECT name, applied_at FROM _migrations"):
		return "timestamp"
	case strings.Contains(sql, "JOIN pg_catalog.pg_index"):
		return "index-guard"
	case strings.TrimSuffix(sql, ";") == strings.TrimSuffix(indexUp, ";"):
		return "index"
	case strings.TrimSuffix(sql, ";") == strings.TrimSuffix(ordinaryUp, ";"):
		return "update"
	case strings.HasPrefix(sql, "INSERT INTO _migrations (name, checksum)"):
		return "history-insert"
	case sql == "begin", sql == "commit", sql == "rollback":
		return sql
	case sql == "":
		return "ping"
	default:
		return "unknown"
	}
}

func migrationStartSpan(ctx context.Context, category migrationSpanKey, step string) context.Context {
	sample, ok := ctx.Value(migrationSampleKey{}).(*migrationStageSample)
	if !ok {
		return ctx
	}
	return context.WithValue(ctx, category, migrationStageEvent{
		Category: string(category), Step: step, Offset: time.Since(sample.start).Nanoseconds(),
	})
}

func migrationEndSpan(ctx context.Context, category migrationSpanKey, conn *pgx.Conn, err error) {
	event, ok := ctx.Value(category).(migrationStageEvent)
	if !ok {
		return
	}
	sample := ctx.Value(migrationSampleKey{}).(*migrationStageSample)
	event.Duration = time.Since(sample.start).Nanoseconds() - event.Offset
	if conn != nil {
		event.PID = conn.PgConn().PID()
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			event.Error = pgErr.Code
		} else {
			event.Error = "non-sql-error"
		}
	}
	sample.Events = append(sample.Events, event)
}

func (migrationStageTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return migrationStartSpan(ctx, "query", migrationSQLStage(data.SQL))
}

func (migrationStageTracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	migrationEndSpan(ctx, "query", conn, data.Err)
}

func (migrationStageTracer) TracePrepareStart(ctx context.Context, _ *pgx.Conn, data pgx.TracePrepareStartData) context.Context {
	return migrationStartSpan(ctx, "prepare", migrationSQLStage(data.SQL))
}

func (migrationStageTracer) TracePrepareEnd(ctx context.Context, conn *pgx.Conn, data pgx.TracePrepareEndData) {
	migrationEndSpan(ctx, "prepare", conn, data.Err)
}

func (migrationStageTracer) TraceAcquireStart(ctx context.Context, _ *pgxpool.Pool, _ pgxpool.TraceAcquireStartData) context.Context {
	return migrationStartSpan(ctx, "pool", "acquire")
}

func (migrationStageTracer) TraceAcquireEnd(ctx context.Context, _ *pgxpool.Pool, data pgxpool.TraceAcquireEndData) {
	migrationEndSpan(ctx, "pool", data.Conn, data.Err)
}

func measureMigrationStage(t *testing.T, ctx context.Context, leaf string, cycle int, phase string, work func(context.Context)) {
	t.Helper()
	sample := migrationStageSample{Leaf: leaf, Cycle: cycle, Phase: phase, Events: make([]migrationStageEvent, 0, 32)}
	scope := context.WithValue(ctx, migrationSampleKey{}, &sample)
	sample.start = time.Now()
	work(scope)
	sample.Duration = time.Since(sample.start).Nanoseconds()
	encoded, err := json.Marshal(sample)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("migration-stage %s\n", encoded)
}

func TestMigrationPairStageTrace(t *testing.T) {
	if os.Getenv("LUXO_MIGPAIR_STAGE_TRACE") != "1" {
		t.Skip("isolated migration stage trace is opt-in")
	}
	for _, scenario := range historyReadScenarios {
		t.Run(scenario.name, func(t *testing.T) {
			traceMigrationScenario(t, scenario)
		})
	}
}

func traceMigrationScenario(t *testing.T, scenario historyReadScenario) {
	t.Helper()
	f := prepareHistoryReadFixture(t, scenario)
	f.reset(scenario)
	var runner *Runner
	defer func() {
		if runner != nil {
			runner.Close()
		}
	}()
	for cycle := 0; cycle <= 12; cycle++ {
		if cycle == 0 || scenario.cold {
			measureMigrationStage(t, f.ctx, scenario.name, cycle, "open", func(ctx context.Context) {
				setup := f
				setup.ctx = ctx
				runner = setup.open(migrationStageTracer{})
			})
		}
		var before historyReadState
		measureMigrationStage(t, f.ctx, scenario.name, cycle, "inspect", func(context.Context) {
			before = f.inspect()
		})
		var names []string
		var err error
		measureMigrationStage(t, f.ctx, scenario.name, cycle, "up", func(ctx context.Context) {
			names, err = runner.Up(ctx)
		})
		measureMigrationStage(t, f.ctx, scenario.name, cycle, "verify-reset", func(context.Context) {
			f.verify(scenario, before, names, err)
			f.reset(scenario)
			if scenario.cold {
				runner.Close()
				runner = nil
			}
		})
	}
}
