// Package lux provides shared types used by all Luxo backends.
// Backend-specific runtime packages (pg, mysql, mongo) import this package
// for common types like Condition, SetField, and QueryBase.
package lux

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// DBConfig holds database connection configuration shared by all backends.
type DBConfig struct {
	Host     string        // DATABASE_HOST (default: localhost)
	Port     string        // DATABASE_PORT (default: 5432)
	User     string        // DATABASE_USER (default: postgres)
	Password string        // DATABASE_PASSWORD
	DBName   string        // DATABASE_PREFIX (default: luxo)
	SSL      string        // DATABASE_SSL (default: disable)
	Pool     int           // DATABASE_POOL — max connections (default: 50)
	Idle     int           // DATABASE_IDLE — idle connections (default: 10)
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
		Pool:    50,
		Idle:    10,
		Timeout: 5 * time.Second,
	}
}

// DBConfigFromEnv reads database configuration from environment variables.
// Uses os.LookupEnv — call env.Load(".env") before this if using a .env file.
func DBConfigFromEnv() DBConfig {
	getEnv := func(key string) string { return os.Getenv(key) }
	cfg := DefaultDBConfig()
	if v := getEnv("DATABASE_HOST"); v != "" {
		cfg.Host = v
	}
	if v := getEnv("DATABASE_PORT"); v != "" {
		cfg.Port = v
	}
	if v := getEnv("DATABASE_USER"); v != "" {
		cfg.User = v
	}
	if v := getEnv("DATABASE_PASSWORD"); v != "" {
		cfg.Password = v
	}
	if v := getEnv("DATABASE_PREFIX"); v != "" {
		cfg.DBName = v
	}
	if v := getEnv("DATABASE_SSL"); v != "" {
		cfg.SSL = v
	}
	if v := getEnv("DATABASE_POOL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Pool = n
		}
	}
	if v := getEnv("DATABASE_IDLE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Idle = n
		}
	}
	if v := getEnv("DATABASE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Timeout = d
		}
	}
	cfg.DebugSQL = getEnv("DEBUG_SQL") == "true"
	return cfg
}

// ConnectionString builds a PostgreSQL-style connection URL.
func (c DBConfig) ConnectionString() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
		c.User, c.Password, c.Host, c.Port, c.DBName, c.SSL)
}

// QueryTracer is called before and after each query for debugging.
// Implemented by each backend (pg, mysql, etc.).
type QueryTracer interface {
	BeforeQuery(sql string, args []any)
	AfterQuery(sql string, args []any, duration time.Duration, err error)
}
