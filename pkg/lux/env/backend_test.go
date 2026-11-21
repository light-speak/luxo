package env

import "testing"

func TestCompiledBackendSelectionMatrix(t *testing.T) {
	for _, compiled := range []string{"none", "memory", "redis", "nats", "pg"} {
		for _, configured := range []string{"", "none", "memory", "redis", "nats", "pg", "unknown"} {
			t.Run(compiled+"/"+configured, func(t *testing.T) {
				t.Setenv("LUXO_TEST_BACKEND", configured)
				err := CheckBackend("LUXO_TEST_BACKEND", compiled)
				wantError := configured != "" && configured != compiled
				if (err != nil) != wantError {
					t.Fatalf("compiled=%q configured=%q: %v", compiled, configured, err)
				}
			})
		}
	}
}

func TestCompiledBackend(t *testing.T) {
	t.Setenv("LUXO_TEST_BACKEND", "memory")
	if err := CheckBackend("LUXO_TEST_BACKEND", "memory"); err != nil {
		t.Fatal(err)
	}
	if err := CheckBackend("LUXO_TEST_BACKEND", "nats"); err == nil {
		t.Fatal("runtime backend switch accepted")
	}
	t.Setenv("LUXO_TEST_BACKEND", "")
	if err := CheckBackend("LUXO_TEST_BACKEND", "memory"); err != nil {
		t.Fatal(err)
	}
	if _, err := Required("LUXO_TEST_BACKEND"); err == nil {
		t.Fatal("empty required configuration accepted")
	}
	t.Setenv("LUXO_TEST_BACKEND", "  ")
	if _, err := Required("LUXO_TEST_BACKEND"); err == nil {
		t.Fatal("blank required configuration accepted")
	}
	t.Setenv("LUXO_TEST_BACKEND", "value")
	if got, err := Required("LUXO_TEST_BACKEND"); err != nil || got != "value" {
		t.Fatalf("%q %v", got, err)
	}
}
