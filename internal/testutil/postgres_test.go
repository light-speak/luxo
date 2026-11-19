package testutil

import "testing"

func TestPostgresImage(t *testing.T) {
	for _, value := range []string{"", "timescale/timescaledb:latest-pg16", "postgres@sha256:fixture"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("LUXO_TEST_POSTGRES_IMAGE", value)
			want := value
			if want == "" {
				want = "postgres:17-alpine"
			}
			if got := PostgresImage(); got != want {
				t.Fatalf("image = %q, want %q", got, want)
			}
		})
	}
}
