package cachepolicy

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	base, err := filepath.Abs("../../.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(base, "cache-catalog-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if filepath.Dir(dir) != base {
			t.Fatal("refusing cleanup outside project scratch directory")
		}
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	path := filepath.Join(dir, Filename)
	if catalog, err := Load(path); err != nil || catalog != nil {
		t.Fatalf("absent catalog: %v %v", catalog, err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("directory accepted as a catalog")
	}
	for _, raw := range []string{`{}`, `{"version":1,"policies":{"preview":{"mode":"ttl","ttl":"1s"}}}`} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		catalog, err := Load(path)
		if raw == "{}" {
			if err == nil || catalog != nil {
				t.Fatal("invalid catalog accepted")
			}
		} else if policy, ok := catalog.Lookup("preview"); err != nil || !ok || policy.TTL != time.Second {
			t.Fatalf("valid catalog: %+v %v", policy, err)
		}
	}
}
