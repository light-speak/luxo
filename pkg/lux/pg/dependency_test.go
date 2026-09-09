package pg

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/light-speak/luxo/pkg/lux"
)

func TestRuntimeDependenciesReportsLivePool(t *testing.T) {
	db := testDB(t)
	dependencies := db.RuntimeDependencies(context.Background())
	if len(dependencies) != 1 || dependencies[0].Status != lux.DependencyOnline || dependencies[0].LatencyMs == nil {
		t.Fatalf("live dependencies = %+v", dependencies)
	}
}

type fakePoolStatistics struct {
	acquireCount      int64
	acquireDuration   time.Duration
	acquiredConns     int32
	canceledAcquire   int64
	constructingConns int32
	emptyAcquireCount int64
	idleConns         int32
	maxConns          int32
	totalConns        int32
}

func (stats fakePoolStatistics) AcquireCount() int64            { return stats.acquireCount }
func (stats fakePoolStatistics) AcquireDuration() time.Duration { return stats.acquireDuration }
func (stats fakePoolStatistics) AcquiredConns() int32           { return stats.acquiredConns }
func (stats fakePoolStatistics) CanceledAcquireCount() int64    { return stats.canceledAcquire }
func (stats fakePoolStatistics) ConstructingConns() int32       { return stats.constructingConns }
func (stats fakePoolStatistics) EmptyAcquireCount() int64       { return stats.emptyAcquireCount }
func (stats fakePoolStatistics) IdleConns() int32               { return stats.idleConns }
func (stats fakePoolStatistics) MaxConns() int32                { return stats.maxConns }
func (stats fakePoolStatistics) TotalConns() int32              { return stats.totalConns }

func TestPostgresDependencyStats(t *testing.T) {
	config, err := pgxpool.ParseConfig("postgres://postgres:secret@db.internal:5432/taskflow?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	stats := fakePoolStatistics{
		acquireCount: 4, acquireDuration: 8 * time.Millisecond, acquiredConns: 2,
		constructingConns: 1, emptyAcquireCount: 3, idleConns: 4, maxConns: 20, totalConns: 7,
	}
	latency := 1250 * time.Microsecond
	dependency := postgresDependencyStats(config, stats, latency, nil)
	if dependency.Kind != lux.DependencyDatabase || dependency.Status != lux.DependencyOnline {
		t.Fatalf("dependency status = %+v", dependency)
	}
	if dependency.Target != "db.internal:5432" || dependency.Name != "taskflow" || dependency.Target == "secret" {
		t.Fatalf("dependency target is not safely normalized: %+v", dependency)
	}
	if dependency.LatencyMs == nil || *dependency.LatencyMs != 1.25 || dependency.PoolAcquireMs != 2 {
		t.Fatalf("dependency latency = %+v", dependency)
	}
	if dependency.PoolMax != 20 || dependency.PoolTotal != 7 || dependency.PoolInUse != 2 || dependency.PoolIdle != 4 || dependency.PoolConstructing != 1 || dependency.PoolEmptyAcquireCount != 3 {
		t.Fatalf("dependency pool stats = %+v", dependency)
	}
}

func TestPostgresDependencyStatsReportsProbeFailure(t *testing.T) {
	config, err := pgxpool.ParseConfig("postgres://postgres@127.0.0.1:5432/taskflow")
	if err != nil {
		t.Fatal(err)
	}
	dependency := postgresDependencyStats(config, fakePoolStatistics{}, time.Second, errors.New("offline"))
	if dependency.Status != lux.DependencyError || dependency.LatencyMs != nil || dependency.PoolAcquireMs != 0 {
		t.Fatalf("failed dependency = %+v", dependency)
	}
}
