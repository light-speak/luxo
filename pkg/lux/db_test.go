package lux

import (
	"os"
	"testing"
	"time"
)

// ===== DefaultDBConfig =====

func TestDefaultDBConfig(t *testing.T) {
	cfg := DefaultDBConfig()

	if cfg.Host != "localhost" {
		t.Errorf("Host = %q, want localhost", cfg.Host)
	}
	if cfg.Port != "5432" {
		t.Errorf("Port = %q, want 5432", cfg.Port)
	}
	if cfg.User != "postgres" {
		t.Errorf("User = %q, want postgres", cfg.User)
	}
	if cfg.Password != "" {
		t.Errorf("Password = %q, want empty", cfg.Password)
	}
	if cfg.DBName != "luxo" {
		t.Errorf("DBName = %q, want luxo", cfg.DBName)
	}
	if cfg.SSL != "disable" {
		t.Errorf("SSL = %q, want disable", cfg.SSL)
	}
	if cfg.Pool != 20 {
		t.Errorf("Pool = %d, want 20", cfg.Pool)
	}
	if cfg.Idle != 2 {
		t.Errorf("Idle = %d, want 2", cfg.Idle)
	}
	if cfg.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", cfg.Timeout)
	}
	if cfg.DebugSQL {
		t.Errorf("DebugSQL should default to false")
	}
}

// ===== ConnectionString =====

func TestConnectionStringNormal(t *testing.T) {
	cfg := DBConfig{
		Host:     "localhost",
		Port:     "5432",
		User:     "postgres",
		Password: "secret",
		DBName:   "mydb",
		SSL:      "disable",
	}
	got := cfg.ConnectionString()
	want := "postgres://postgres:secret@localhost:5432/mydb?sslmode=disable"
	if got != want {
		t.Errorf("ConnectionString() = %q, want %q", got, want)
	}
}

func TestConnectionStringSpecialCharsInPassword(t *testing.T) {
	cfg := DBConfig{
		Host:     "db.example.com",
		Port:     "5432",
		User:     "admin",
		Password: "p@ss/word=test&more",
		DBName:   "prod",
		SSL:      "require",
	}
	got := cfg.ConnectionString()
	// The URL should be parseable and contain percent-encoded special chars.
	// Verify it starts with the correct scheme and host.
	if len(got) == 0 {
		t.Fatal("ConnectionString() is empty")
	}
	if got[:len("postgres://")] != "postgres://" {
		t.Errorf("missing postgres:// scheme: %q", got)
	}
	// Password with special chars must be percent-encoded (no raw @, /, =, &).
	// The user info section ends at the '@' before the host — there should be
	// exactly one '@' separating userinfo from host.
	atCount := 0
	for _, ch := range got {
		if ch == '@' {
			atCount++
		}
	}
	if atCount != 1 {
		t.Errorf("expected exactly one '@' in URL (raw special chars would add more), got %d in %q", atCount, got)
	}
}

func TestConnectionStringNoPassword(t *testing.T) {
	cfg := DBConfig{
		Host:   "localhost",
		Port:   "5433",
		User:   "postgres",
		DBName: "testdb",
		SSL:    "disable",
	}
	got := cfg.ConnectionString()
	// With empty password, url.UserPassword still encodes a ':' separator.
	// Just verify the output is a valid postgres URL.
	if got == "" {
		t.Fatal("ConnectionString() is empty")
	}
	if got[:len("postgres://")] != "postgres://" {
		t.Errorf("missing scheme: %q", got)
	}
}

// ===== DBConfigFromEnv =====

func TestDBConfigFromEnvDefaults(t *testing.T) {
	// Ensure no relevant env vars are set.
	for _, key := range []string{
		"DATABASE_HOST", "DATABASE_PORT", "DATABASE_USER",
		"DATABASE_PASSWORD", "DATABASE_PREFIX", "DATABASE_SSL",
		"DATABASE_POOL", "DATABASE_IDLE", "DATABASE_TIMEOUT", "DEBUG_SQL",
	} {
		t.Setenv(key, "")
	}

	cfg := DBConfigFromEnv()
	def := DefaultDBConfig()

	if cfg.Host != def.Host {
		t.Errorf("Host = %q, want %q", cfg.Host, def.Host)
	}
	if cfg.Port != def.Port {
		t.Errorf("Port = %q, want %q", cfg.Port, def.Port)
	}
	if cfg.User != def.User {
		t.Errorf("User = %q, want %q", cfg.User, def.User)
	}
	if cfg.Pool != def.Pool {
		t.Errorf("Pool = %d, want %d", cfg.Pool, def.Pool)
	}
	if cfg.Idle != def.Idle {
		t.Errorf("Idle = %d, want %d", cfg.Idle, def.Idle)
	}
	if cfg.Timeout != def.Timeout {
		t.Errorf("Timeout = %v, want %v", cfg.Timeout, def.Timeout)
	}
	if cfg.DebugSQL {
		t.Error("DebugSQL should be false when DEBUG_SQL is empty")
	}
}

func TestDBConfigFromEnvOverrides(t *testing.T) {
	t.Setenv("DATABASE_HOST", "db.prod.internal")
	t.Setenv("DATABASE_PORT", "5433")
	t.Setenv("DATABASE_USER", "appuser")
	t.Setenv("DATABASE_PASSWORD", "hunter2")
	t.Setenv("DATABASE_PREFIX", "myapp_prod")
	t.Setenv("DATABASE_SSL", "require")
	t.Setenv("DATABASE_POOL", "100")
	t.Setenv("DATABASE_IDLE", "20")
	t.Setenv("DATABASE_TIMEOUT", "10s")
	t.Setenv("DEBUG_SQL", "true")

	cfg := DBConfigFromEnv()

	if cfg.Host != "db.prod.internal" {
		t.Errorf("Host = %q", cfg.Host)
	}
	if cfg.Port != "5433" {
		t.Errorf("Port = %q", cfg.Port)
	}
	if cfg.User != "appuser" {
		t.Errorf("User = %q", cfg.User)
	}
	if cfg.Password != "hunter2" {
		t.Errorf("Password = %q", cfg.Password)
	}
	if cfg.DBName != "myapp_prod" {
		t.Errorf("DBName = %q", cfg.DBName)
	}
	if cfg.SSL != "require" {
		t.Errorf("SSL = %q", cfg.SSL)
	}
	if cfg.Pool != 100 {
		t.Errorf("Pool = %d, want 100", cfg.Pool)
	}
	if cfg.Idle != 20 {
		t.Errorf("Idle = %d, want 20", cfg.Idle)
	}
	if cfg.Timeout != 10*time.Second {
		t.Errorf("Timeout = %v, want 10s", cfg.Timeout)
	}
	if !cfg.DebugSQL {
		t.Error("DebugSQL should be true")
	}
}

func TestDBConfigFromEnvInvalidPool(t *testing.T) {
	t.Setenv("DATABASE_POOL", "not-a-number")
	t.Setenv("DATABASE_IDLE", "-5") // negative — should be ignored
	cfg := DBConfigFromEnv()
	// Invalid/negative values must leave defaults in place.
	if cfg.Pool != DefaultDBConfig().Pool {
		t.Errorf("Pool = %d, want default %d on invalid input", cfg.Pool, DefaultDBConfig().Pool)
	}
	if cfg.Idle != DefaultDBConfig().Idle {
		t.Errorf("Idle = %d, want default %d on non-positive input", cfg.Idle, DefaultDBConfig().Idle)
	}
}

func TestDBConfigFromEnvInvalidTimeout(t *testing.T) {
	t.Setenv("DATABASE_TIMEOUT", "not-a-duration")
	cfg := DBConfigFromEnv()
	if cfg.Timeout != DefaultDBConfig().Timeout {
		t.Errorf("Timeout = %v, want default on invalid input", cfg.Timeout)
	}
}

func TestDBConfigFromEnvDebugSQLFalse(t *testing.T) {
	t.Setenv("DEBUG_SQL", "false")
	cfg := DBConfigFromEnv()
	if cfg.DebugSQL {
		t.Error("DebugSQL should be false when DEBUG_SQL=false")
	}
}

func TestDBConfigFromEnvDebugSQLTrue(t *testing.T) {
	os.Setenv("DEBUG_SQL", "true")
	t.Cleanup(func() { os.Unsetenv("DEBUG_SQL") })
	cfg := DBConfigFromEnv()
	if !cfg.DebugSQL {
		t.Error("DebugSQL should be true when DEBUG_SQL=true")
	}
}
