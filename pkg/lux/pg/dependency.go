package pg

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/light-speak/luxo/pkg/lux"
)

type poolStatistics interface {
	AcquireCount() int64
	AcquireDuration() time.Duration
	AcquiredConns() int32
	CanceledAcquireCount() int64
	ConstructingConns() int32
	EmptyAcquireCount() int64
	IdleConns() int32
	MaxConns() int32
	TotalConns() int32
}

// RuntimeDependencies reports a credential-free PostgreSQL pool snapshot.
func (db *DB) RuntimeDependencies(ctx context.Context) []lux.RuntimeDependencyStats {
	startedAt := time.Now()
	err := db.pool.Ping(ctx)
	return []lux.RuntimeDependencyStats{
		postgresDependencyStats(db.pool.Config(), db.pool.Stat(), time.Since(startedAt), err),
	}
}

func postgresDependencyStats(config *pgxpool.Config, stats poolStatistics, latency time.Duration, probeErr error) lux.RuntimeDependencyStats {
	target := net.JoinHostPort(config.ConnConfig.Host, strconv.Itoa(int(config.ConnConfig.Port)))
	dependency := lux.RuntimeDependencyStats{
		Key:                   fmt.Sprintf("database:postgresql:%s/%s", target, config.ConnConfig.Database),
		Kind:                  lux.DependencyDatabase,
		Provider:              "postgresql",
		Target:                target,
		Name:                  config.ConnConfig.Database,
		Status:                lux.DependencyOnline,
		PoolMax:               int64(stats.MaxConns()),
		PoolTotal:             int64(stats.TotalConns()),
		PoolInUse:             int64(stats.AcquiredConns()),
		PoolIdle:              int64(stats.IdleConns()),
		PoolConstructing:      int64(stats.ConstructingConns()),
		PoolAcquireCount:      stats.AcquireCount(),
		PoolEmptyAcquireCount: stats.EmptyAcquireCount(),
		PoolCanceledCount:     stats.CanceledAcquireCount(),
	}
	if dependency.PoolAcquireCount > 0 {
		dependency.PoolAcquireMs = float64(stats.AcquireDuration().Microseconds()) / 1000 / float64(dependency.PoolAcquireCount)
	}
	if probeErr != nil {
		dependency.Status = lux.DependencyError
		return dependency
	}
	latencyMs := float64(latency.Microseconds()) / 1000
	dependency.LatencyMs = &latencyMs
	return dependency
}
