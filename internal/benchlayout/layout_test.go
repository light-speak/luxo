package benchlayout

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSplitPackagesPreserveLogicalIdentity(t *testing.T) {
	root := layoutFixture(t)
	for _, path := range []string{"pkg/lux/cache", "pkg/lux/cache/redis"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0755); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Packages(root, "pkg/lux/cache")
	if err != nil || !reflect.DeepEqual(got, []string{"pkg/lux/cache", "pkg/lux/cache/redis"}) {
		t.Fatalf("%v: %v", got, err)
	}
	if Canonical("pkg/lux/cache/redis") != "pkg/lux/cache" || Canonical("pkg/parser") != "pkg/parser" {
		t.Fatal("invalid canonical mapping")
	}
	if _, err := Packages(root, "../outside"); err == nil {
		t.Fatal("accepted path escape")
	}
	if _, err := Packages(root, "pkg/missing"); err == nil {
		t.Fatal("accepted absent package")
	}
}

func TestPackagesFilesystemFailures(t *testing.T) {
	root := layoutFixture(t)
	if _, err := Packages(filepath.Join(root, "missing"), "pkg/parser"); !os.IsNotExist(err) {
		t.Fatalf("missing root: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "pkg"), 0755); err != nil {
		t.Fatal(err)
	}
	// A self-referencing link reliably produces a non-ENOENT stat error, even
	// when CI runs as root; chmod-based permission tests would not do that.
	loop := filepath.Join(root, "pkg", "loop")
	if err := os.Symlink("loop", loop); err != nil {
		t.Fatal(err)
	}
	if _, err := Packages(root, "pkg/loop"); err == nil || os.IsNotExist(err) {
		t.Fatalf("stat error was swallowed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "file"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Packages(root, "pkg/file"); err == nil {
		t.Fatal("regular file accepted as package directory")
	}
}

func layoutFixture(t *testing.T) string {
	t.Helper()
	if err := os.MkdirAll("../../.tmp", 0755); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("../../.tmp", "bench-layout-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	return root
}
