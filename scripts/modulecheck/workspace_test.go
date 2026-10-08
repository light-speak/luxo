package modulecheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRevisionWorkspace(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := consumerFixture(t, root, "", "")
	files := map[string]string{
		"VERSION":            "1.0.0-beta.1\n",
		"go.mod":             "module github.com/light-speak/luxo\ngo 1.27.1\n",
		"pkg/model/model.go": "package model\nconst Value = 1\n",
		"pkg/lux/pg/go.mod":  "module github.com/light-speak/luxo/pkg/lux/pg\ngo 1.27.1\nrequire github.com/light-speak/luxo v1.0.0-beta.1\n",
		"pkg/lux/pg/pg.go":   "package pg\nimport \"github.com/light-speak/luxo/pkg/model\"\nconst Value = model.Value\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	setup := func() *exec.Cmd { return exec.Command("sh", filepath.Join(root, "scripts/setup-modules.sh"), dir) }
	if output, err := setup().CombinedOutput(); err != nil {
		t.Fatalf("setup: %v\n%s", err, output)
	}
	cmd := exec.Command("go", "list", "-deps", "./pkg/lux/pg/...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK="+filepath.Join(dir, "go.work"), "GOPROXY=off")
	if output, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(output), "github.com/light-speak/luxo/pkg/model") {
		t.Fatalf("isolated graph: %v\n%s", err, output)
	}
	if output, err := setup().CombinedOutput(); err == nil || !strings.Contains(string(output), "Refusing to overwrite") {
		t.Fatalf("existing workspace was not protected: %v\n%s", err, output)
	}
}
