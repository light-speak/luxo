package pg

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/light-speak/luxo/pkg/lux"
)

type transactionReadConn struct {
	pgx.Tx
	active             atomic.Bool
	queryErr, countErr error
}

func (c *transactionReadConn) Query(context.Context, string, ...any) (pgx.Rows, error) {
	if !c.active.CompareAndSwap(false, true) {
		return nil, errors.New("connection already busy")
	}
	time.Sleep(time.Second)
	if c.queryErr != nil {
		c.active.Store(false)
		return nil, c.queryErr
	}
	return &transactionReadRows{methodTestRows: methodTestRows{left: 1}, owner: c}, nil
}
func (c *transactionReadConn) QueryRow(context.Context, string, ...any) pgx.Row {
	return transactionCountRow{owner: c}
}

type transactionReadRows struct {
	methodTestRows
	owner *transactionReadConn
}

func (r *transactionReadRows) Close() { r.methodTestRows.Close(); r.owner.active.Store(false) }

type transactionCountRow struct{ owner *transactionReadConn }

func (r transactionCountRow) Scan(values ...any) error {
	if !r.owner.active.CompareAndSwap(false, true) {
		return errors.New("connection already busy")
	}
	defer r.owner.active.Store(false)
	time.Sleep(time.Second)
	if r.owner.countErr != nil {
		return r.owner.countErr
	}
	*values[0].(*int64) = 42
	return nil
}

func TestTransactionAllWithCountDoesNotShareConnectionConcurrently(t *testing.T) {
	for _, kind := range []string{"transaction", "opaque"} {
		synctest.Test(t, func(t *testing.T) {
			connection := &transactionReadConn{}
			db := &DB{conn: connection}
			switch kind {
			case "opaque":
				db.conn = struct{ conn }{connection}
			}
			rows, count, err := NewQuery(db, "values", scanMethodValue, nil).AllWithCount(t.Context())
			if err != nil || len(rows) != 1 || count != 42 || *rows[0] != 42 {
				t.Fatalf("connection=%s rows=%v count=%d err=%v", kind, rows, count, err)
			}
			if connection.active.Load() {
				t.Fatal("query still active after return")
			}
		})
	}
}

func TestTransactionAllWithCountPropagatesFailures(t *testing.T) {
	want := errors.New("database failure")
	for _, countFailure := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			connection := &transactionReadConn{}
			if countFailure {
				connection.countErr = want
			} else {
				connection.queryErr = want
			}
			rows, count, err := NewQuery(&DB{conn: connection}, "values", scanMethodValue, nil).AllWithCount(t.Context())
			if !errors.Is(err, want) || rows != nil || count != 0 {
				t.Fatalf("rows=%v count=%d err=%v", rows, count, err)
			}
			if connection.active.Load() {
				t.Fatal("failed query still active")
			}
		})
	}
}

func TestTransactionGroupByReadsOwnWrites(t *testing.T) {
	db := testDB(t)
	abort := errors.New("rollback aggregate transaction")
	err := db.Tx(t.Context(), func(tx *DB) error {
		if err := insertTransactionUser(t.Context(), tx, "uncommitted"); err != nil {
			return err
		}
		if err := tx.Tx(t.Context(), func(inner *DB) error {
			rows, err := NewQuery(inner, "users", scanUser, nil).GroupBy(t.Context(), []string{"name"}, groupCountColumns(), scanGroupCount, lux.NewIntField("COUNT(*)").Eq(1))
			if err != nil {
				return err
			}
			if len(rows) != 1 || rows[0].Name != "uncommitted" || rows[0].Total != 1 {
				t.Errorf("aggregate missed transaction writes: %v", rows)
			}
			return nil
		}); err != nil {
			return err
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatal(err)
	}
	count, err := db.QueryScalar[int64](t.Context(), "SELECT COUNT(*) FROM users")
	if err != nil || count != 0 {
		t.Fatalf("rollback rows=%d err=%v", count, err)
	}
}

// Exercise the pool execution helper without pretending a fake connection is
// a real pool. Database tests separately cover concrete pool dispatch.
func allWithCountPoolFixture[T any](ctx context.Context, q *Query[T]) ([]*T, int64, error) {
	selectSQL, selectArgs := lux.BuildSelectSQL(q.table, q.fields, q.conds, q.orderBy, q.limit, q.offset)
	countSQL, countArgs := lux.BuildCountSQL(q.table, q.conds)
	return q.allWithCountFromPool(ctx, selectSQL, selectArgs, countSQL, countArgs)
}

type parallelReadConn struct {
	conn
	queryErr, countErr error
	countFinished      atomic.Bool
	queryWait          <-chan struct{}
	countStarted       chan struct{}
}

func (c *parallelReadConn) Query(context.Context, string, ...any) (pgx.Rows, error) {
	if c.queryWait != nil {
		<-c.queryWait
	}
	return &methodTestRows{left: 1}, c.queryErr
}
func (c *parallelReadConn) QueryRow(context.Context, string, ...any) pgx.Row {
	return parallelCountRow{owner: c}
}

type parallelCountRow struct{ owner *parallelReadConn }

func (r parallelCountRow) Scan(values ...any) error {
	if r.owner.countStarted != nil {
		close(r.owner.countStarted)
	}
	time.Sleep(time.Second)
	r.owner.countFinished.Store(true)
	*values[0].(*int64) = 42
	return r.owner.countErr
}

func TestParallelAllWithCountJoinsCountOnSelectFailure(t *testing.T) {
	want := errors.New("select failed")
	synctest.Test(t, func(t *testing.T) {
		connection := &parallelReadConn{queryErr: want}
		rows, count, err := allWithCountPoolFixture(t.Context(), NewQuery(&DB{conn: connection}, "values", scanMethodValue, nil))
		if !errors.Is(err, want) || rows != nil || count != 0 {
			t.Fatalf("rows=%v count=%d err=%v", rows, count, err)
		}
		if !connection.countFinished.Load() {
			t.Fatal("count operation survived method return")
		}
	})
}

func TestParallelAllWithCountResults(t *testing.T) {
	for _, failCount := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			started := make(chan struct{})
			connection := &parallelReadConn{queryWait: started, countStarted: started}
			if failCount {
				connection.countErr = errors.New("count failed")
			}
			rows, count, err := allWithCountPoolFixture(t.Context(), NewQuery(&DB{conn: connection}, "values", scanMethodValue, nil))
			if !errors.Is(err, connection.countErr) {
				t.Fatal(err)
			}
			if failCount {
				if rows != nil || count != 0 {
					t.Fatal("partial result returned", rows, count)
				}
			} else if len(rows) != 1 || *rows[0] != 42 || count != 42 {
				t.Fatal(rows, count)
			}
			if !connection.countFinished.Load() {
				t.Fatal("count unfinished")
			}
		})
	}
}

type transactionGroupRows struct {
	methodTestRows
	scanErr error
}

func (r *transactionGroupRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	*dest[0].(*string), *dest[1].(*int64) = "group", 1
	return nil
}
func (*transactionGroupRows) FieldDescriptions() []pgconn.FieldDescription {
	return []pgconn.FieldDescription{{Name: "name"}, {Name: "total"}}
}

type transactionGroupConn struct {
	conn
	rows *transactionGroupRows
	err  error
}

func (c *transactionGroupConn) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return c.rows, c.err
}

func TestGroupByCurrentConnectionFailureCleanup(t *testing.T) {
	want := errors.New("group failure")
	for _, stage := range []string{"none", "query", "scan", "iteration"} {
		rows := &transactionGroupRows{methodTestRows: methodTestRows{left: 1}}
		connection := &transactionGroupConn{rows: rows}
		switch stage {
		case "query":
			connection.err = want
		case "scan":
			rows.scanErr = want
		case "iteration":
			rows.left, rows.err = 0, want
		}
		result, err := NewQuery(&DB{conn: connection}, "groups", scanMethodValue, nil).GroupBy(t.Context(), []string{"name"}, groupCountColumns(), scanGroupCount)
		if stage == "none" {
			if err != nil || len(result) != 1 || result[0].Name != "group" || result[0].Total != 1 {
				t.Fatalf("groups=%v, err=%v", result, err)
			}
		} else if !errors.Is(err, want) {
			t.Fatalf("stage=%s err=%v", stage, err)
		}
		if rows.closed != (stage != "query") {
			t.Fatalf("rows cleanup: stage=%s closed=%t", stage, rows.closed)
		}
	}
}

type groupCount struct {
	Name  string
	Total int64
}

func groupCountColumns() []lux.GroupAgg {
	return []lux.GroupAgg{{Col: "name"}, {Fn: "COUNT", Alias: "total"}}
}

func scanGroupCount(rows Rows) (*groupCount, error) {
	var result groupCount
	if err := rows.Scan(&result.Name, &result.Total); err != nil {
		return nil, err
	}
	return &result, nil
}
