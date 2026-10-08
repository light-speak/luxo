package modulecheck

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Consumers build outside go.work so workspace substitution cannot conceal a
// missing module requirement or a core import of an unused backend driver.
func TestIndependentConsumers(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct{ name, module, imports, driver string }{
		{"core", "", `_ "github.com/light-speak/luxo/pkg/lux/api"; _ "github.com/light-speak/luxo/pkg/lux/storage"; _ "github.com/light-speak/luxo/pkg/lux/cache"; _ "github.com/light-speak/luxo/pkg/lux/event"; _ "github.com/light-speak/luxo/pkg/lux/queue"; _ "github.com/light-speak/luxo/pkg/lux/luvia"`, ""},
		{"redis", "pkg/lux/cache/redis", `_ "github.com/light-speak/luxo/pkg/lux/cache/redis"`, "github.com/redis/"},
		{"nats", "pkg/lux/nats", `_ "github.com/light-speak/luxo/pkg/lux/nats/event"; _ "github.com/light-speak/luxo/pkg/lux/nats/queue"`, "github.com/nats-io/"},
		{"s3", "pkg/lux/storage/s3", `_ "github.com/light-speak/luxo/pkg/lux/storage/s3"`, "github.com/minio/"},
		{"pg", "pkg/lux/pg", `_ "github.com/light-speak/luxo/pkg/lux/pg"; _ "github.com/light-speak/luxo/pkg/lux/pg/migrate"`, "github.com/jackc/"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			dir := consumerFixture(t, root, fixture.module, fixture.imports)
			runConsumer(t, dir, "build", "-mod=mod", "-o", filepath.Join(dir, "consumer"), ".")
			deps := runConsumer(t, dir, "list", "-mod=mod", "-deps", ".")
			for _, testOnly := range []string{"github.com/testcontainers/", "github.com/moby/", "github.com/stretchr/testify/"} {
				if strings.Contains(deps, testOnly) {
					t.Fatalf("consumer imports test infrastructure %s", testOnly)
				}
			}
			for _, driver := range []string{"github.com/redis/", "github.com/nats-io/", "github.com/minio/", "github.com/jackc/"} {
				if strings.Contains(deps, driver) != (driver == fixture.driver) {
					t.Fatalf("unexpected dependency boundary for %s", driver)
				}
			}
		})
	}
}

func consumerFixture(t *testing.T, root, module, imports string) string {
	t.Helper()
	tmp := filepath.Join(root, ".tmp")
	if err := os.MkdirAll(tmp, 0755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(tmp, "module-consumer-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	manifest := fmt.Sprintf("module example.com/consumer\n\ngo 1.27.1\nrequire github.com/light-speak/luxo v1.0.0-beta.1\nreplace github.com/light-speak/luxo => %s\n", filepath.ToSlash(root))
	if module != "" {
		manifest += fmt.Sprintf("require github.com/light-speak/luxo/%s v1.0.0-beta.1\nreplace github.com/light-speak/luxo/%s => %s\n", module, module, filepath.ToSlash(filepath.Join(root, module)))
	}
	for name, content := range map[string]string{"go.mod": manifest, "main.go": "package main\nimport(" + imports + ")\nfunc main(){}\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestIndependentCLI(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := consumerFixture(t, root, "cmd/luxo", `_ "github.com/light-speak/luxo/pkg/lux/api"`)
	runConsumer(t, dir, "mod", "edit", "-replace=github.com/light-speak/luxo/pkg/lux/pg="+filepath.Join(root, "pkg/lux/pg"))
	runConsumer(t, dir, "build", "-mod=mod", "-o", filepath.Join(dir, "luxo"), "github.com/light-speak/luxo/cmd/luxo")
	deps := runConsumer(t, dir, "list", "-mod=mod", "-deps", "github.com/light-speak/luxo/cmd/luxo")
	for _, driver := range []string{"github.com/redis/", "github.com/nats-io/", "github.com/minio/"} {
		if strings.Contains(deps, driver) {
			t.Fatalf("CLI imports unused adapter %s", driver)
		}
	}
}

func runConsumer(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %v: %v\n%s", args, err, output)
	}
	return string(output)
}
