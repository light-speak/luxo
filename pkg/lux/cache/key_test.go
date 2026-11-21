package cache

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyResponseKeyAPIsAreUnavailable(t *testing.T) {
	root, err := filepath.Abs("../../../.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "legacy-cache-key-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	for _, sample := range []struct{ name, args string }{
		{"BuildKey", `"cached", nil`},
		{"BuildKeyRaw", `"cached", nil`},
		{"InvalidatePrefix", `"User"`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			source := "package consumer\nimport \"github.com/light-speak/luxo/pkg/lux/cache\"\nvar _ = cache." + sample.name + "(" + sample.args + ")\n"
			if err := os.WriteFile(filepath.Join(dir, "consumer.go"), []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), "go", "test", "-run", "^$", ".")
			cmd.Dir = dir
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "undefined: cache."+sample.name) {
				t.Fatalf("legacy cache API remains callable or failed for another reason: %v\n%s", err, output)
			}
		})
	}
}
