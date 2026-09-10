// Package lux provides shared types used by all Luxo backends.
// Backend-specific runtime packages (pg, mysql, mongo) import this package
// for common types like Condition, SetField, and QueryBase.
package lux

import (
	"context"
	"net"
	"net/url"
	"os"
	"strconv"
	"time"
)

// RuntimeDependencyKind identifies an infrastructure dependency class.
type RuntimeDependencyKind string

const (
	DependencyDatabase RuntimeDependencyKind = "DATABASE"
	DependencyCache    RuntimeDependencyKind = "CACHE"
	DependencyQueue    RuntimeDependencyKind = "QUEUE"
	DependencyStorage  RuntimeDependencyKind = "STORAGE"
	DependencySearch   RuntimeDependencyKind = "SEARCH"
)

// RuntimeDependencyStatus is the latest probe result for a dependency.
type RuntimeDependencyStatus string

const (
	DependencyOnline RuntimeDependencyStatus = "ONLINE"
	DependencyError  RuntimeDependencyStatus = "ERROR"
)

// RuntimeDependencyStats is a credential-free dependency snapshot exported to Studio.
type RuntimeDependencyStats struct {
	Key                   string                  `json:"key"`
	Kind                  RuntimeDependencyKind   `json:"kind"`
	Provider              string                  `json:"provider"`
	Target                string                  `json:"target"`
	Name                  string                  `json:"name"`
	Status                RuntimeDependencyStatus `json:"status"`
	LatencyMs             *float64                `json:"latencyMs"`
	PoolMax               int64                   `json:"poolMax"`
	PoolTotal             int64                   `json:"poolTotal"`
	PoolInUse             int64                   `json:"poolInUse"`
	PoolIdle              int64                   `json:"poolIdle"`
	PoolConstructing      int64                   `json:"poolConstructing"`
	PoolAcquireCount      int64                   `json:"poolAcquireCount"`
	PoolAcquireMs         float64                 `json:"poolAcquireMs"`
	PoolEmptyAcquireCount int64                   `json:"poolEmptyAcquireCount"`
	PoolCanceledCount     int64                   `json:"poolCanceledCount"`
}

// RuntimeDependencyStatsProvider captures dependency metrics outside request hot paths.
type RuntimeDependencyStatsProvider interface {
	RuntimeDependencies(context.Context) []RuntimeDependencyStats
}

// DBConfig holds database connection configuration shared by all backends.
type DBConfig struct {
	Host     string        // DATABASE_HOST (default: localhost)
	Port     string        // DATABASE_PORT (default: 5432)
	User     string        // DATABASE_USER (default: postgres)
	Password string        // DATABASE_PASSWORD
	DBName   string        // DATABASE_PREFIX (default: luxo)
	SSL      string        // DATABASE_SSL (default: disable)
	Pool     int           // DATABASE_POOL — max connections (default: 20)
	Idle     int           // DATABASE_IDLE — min idle connections (default: 2)
	Timeout  time.Duration // DATABASE_TIMEOUT (default: 5s)
	DebugSQL bool          // DEBUG_SQL — print all queries with timing
}

// DefaultDBConfig returns a DBConfig with sensible defaults.
func DefaultDBConfig() DBConfig {
	return DBConfig{
		Host:    "localhost",
		Port:    "5432",
		User:    "postgres",
		DBName:  "luxo",
		SSL:     "disable",
		Pool:    20,
		Idle:    2,
		Timeout: 5 * time.Second,
	}
}

// DBConfigFromEnv reads database configuration from environment variables.
// Uses os.Getenv — call env.Load(".env") before this if using a .env file.
func DBConfigFromEnv() DBConfig {
	cfg := DefaultDBConfig()
	if v := os.Getenv("DATABASE_HOST"); v != "" {
		cfg.Host = v
	}
	if v := os.Getenv("DATABASE_PORT"); v != "" {
		cfg.Port = v
	}
	if v := os.Getenv("DATABASE_USER"); v != "" {
		cfg.User = v
	}
	if v := os.Getenv("DATABASE_PASSWORD"); v != "" {
		cfg.Password = v
	}
	if v := os.Getenv("DATABASE_PREFIX"); v != "" {
		cfg.DBName = v
	}
	if v := os.Getenv("DATABASE_SSL"); v != "" {
		cfg.SSL = v
	}
	if v := os.Getenv("DATABASE_POOL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Pool = n
		}
	}
	if v := os.Getenv("DATABASE_IDLE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Idle = n
		}
	}
	if v := os.Getenv("DATABASE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Timeout = d
		}
	}
	cfg.DebugSQL = os.Getenv("DEBUG_SQL") == "true"
	return cfg
}

// ConnectionString builds a PostgreSQL-style connection URL.
func (c DBConfig) ConnectionString() string {
	u := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(c.User, c.Password),
		Host:     net.JoinHostPort(c.Host, c.Port),
		Path:     c.DBName,
		RawQuery: "sslmode=" + c.SSL,
	}
	return u.String()
}

// DatabaseTraceMeta describes one credential-free database operation. Statement
// must already be normalized, have literals redacted, and be size bounded by
// the backend before it crosses the trace boundary.
type DatabaseTraceMeta struct {
	Backend       string
	Database      string
	Operation     string
	Resource      string
	Statement     string
	Fingerprint   string
	ArgumentCount int
	Truncated     bool
}

// DatabaseTraceResult contains non-sensitive outcome metadata.
type DatabaseTraceResult struct {
	Command      string
	ErrorCode    string
	RowsAffected int64
	RowsKnown    bool
}

// DatabasePoolTraceMeta identifies the pool involved in one acquire operation.
type DatabasePoolTraceMeta struct {
	Backend  string
	Database string
}

// DatabaseTraceSink receives request-scoped database spans. Backends check for
// a sink before reading clocks or allocating trace metadata, keeping ordinary
// requests allocation free.
type DatabaseTraceSink interface {
	DatabaseTraceDetails() bool
	StartDatabaseQuery(context.Context, DatabaseTraceMeta) context.Context
	FinishDatabaseQuery(context.Context, DatabaseTraceResult)
	StartDatabaseAcquire(context.Context, DatabasePoolTraceMeta) context.Context
	FinishDatabaseAcquire(context.Context, error)
}

type databaseTraceSinkKey struct{}

// WithDatabaseTraceSink attaches request-scoped database tracing to ctx.
func WithDatabaseTraceSink(ctx context.Context, sink DatabaseTraceSink) context.Context {
	return context.WithValue(ctx, databaseTraceSinkKey{}, sink)
}

// DatabaseTraceSinkFromContext returns the active database sink, if any.
func DatabaseTraceSinkFromContext(ctx context.Context) DatabaseTraceSink {
	sink, _ := ctx.Value(databaseTraceSinkKey{}).(DatabaseTraceSink)
	return sink
}
