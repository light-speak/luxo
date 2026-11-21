package codegen

import (
	"bytes"
	goast "go/ast"
	"go/format"
	goparser "go/parser"
	gotoken "go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestEntryBackendSelection(t *testing.T) {
	input := &semantic.Result{Files: []*ast.File{{Name: "origin/service.luxo", Events: []*ast.EventDecl{{Name: "Changed"}}}}}
	if _, err := GenerateEntryFileChecked(input, "example.com/app", EntryBackends{}); err == nil {
		t.Fatal("event use without a build backend accepted")
	}
	for _, backend := range []string{"memory", "nats"} {
		source, err := GenerateEntryFileChecked(input, "example.com/app", EntryBackends{Events: backend})
		if err != nil {
			t.Fatal(err)
		}
		code := string(source)
		if strings.Contains(code, "event.NewFromEnv") || strings.Contains(code, "queue.NewFromEnv") || strings.Contains(code, "taskQueue") {
			t.Fatalf("implicit backend or unused queue: %s", code)
		}
		constructor := "event.NewChanBus(256)"
		if backend == "nats" {
			constructor = "event.NewNATSBus("
		}
		if !strings.Contains(code, constructor) {
			t.Fatalf("missing static %s", constructor)
		}
		if got := strings.Contains(code, `"github.com/light-speak/luxo/pkg/lux/nats/event"`); got != (backend == "nats") {
			t.Fatalf("NATS import does not match build selection %q", backend)
		}
		for _, unused := range []string{"pkg/lux/cache/redis", "pkg/lux/storage/s3", "pkg/lux/nats/queue"} {
			if strings.Contains(code, unused) {
				t.Fatalf("unused adapter imported: %s", unused)
			}
		}
	}
}

func TestInvalidEntryBackendSelection(t *testing.T) {
	for _, selection := range []EntryBackends{{Events: "redis"}, {Queue: "redis"}, {Cache: "nats"}} {
		if err := selection.Validate(&semantic.Result{}); err == nil {
			t.Fatalf("accepted %+v", selection)
		}
	}
}

func TestCheckedEntriesRejectBackendConfiguration(t *testing.T) {
	input := &semantic.Result{}
	for _, options := range [][]EntryBackends{{{Events: "unsupported"}}, {{}, {}}} {
		if source, err := GenerateEntryFileChecked(input, "example.com/app", options...); err == nil || source != nil {
			t.Fatal("embedded entry accepted invalid backend configuration")
		}
		if sources, err := GenerateModuleEntryFilesChecked(input, "example.com/app", options...); err == nil || sources != nil {
			t.Fatal("module entries accepted invalid backend configuration")
		}
		if source, err := GenerateGatewayEntryChecked(input, "example.com/app", options...); err == nil || source != nil {
			t.Fatal("gateway entry accepted invalid backend configuration")
		}
	}
	if err := (EntryBackends{}).Validate(nil); err == nil {
		t.Fatal("backend validation accepted missing semantic result")
	}
}

func TestRemoteCacheAndQueueEntrySelection(t *testing.T) {
	input := analyzeCachedFixture(t, []*ast.File{{Name: "origin/service.luxo", APIs: []*ast.ApiDecl{cachedTestAPI(true)}}}, true)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	if err := (EntryBackends{Cache: "none"}).Validate(input); err == nil {
		t.Fatal("cached API accepted a disabled backend")
	}
	source, err := GenerateEntryFileChecked(input, "example.com/app", EntryBackends{Queue: "nats", Cache: "redis"})
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`"github.com/light-speak/luxo/pkg/lux/cache/redis"`,
		`"github.com/light-speak/luxo/pkg/lux/nats/queue"`,
		`env.CheckBackend("LUXO_CACHE_BACKEND", "redis")`,
		`env.CheckBackend("LUXO_QUEUE_BACKEND", "nats")`,
		`api.NewResponseCacheFromEnv(ctx, "embedded", redis.Open)`,
		`natsqueue.NewNATSQueue(queueURL, queue.DefaultConfig())`,
	} {
		if !strings.Contains(string(source), required) {
			t.Fatalf("selected adapter missing %q", required)
		}
	}
	if strings.Contains(string(source), "pkg/lux/nats/event") || strings.Contains(string(source), "cache.OpenMemory") {
		t.Fatal("remote queue/cache selection imported an unselected adapter")
	}
}

func TestGeneratedMemoryQueueInitialization(t *testing.T) {
	root, err := filepath.Abs("../../.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "entry-queue-config-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	var generated strings.Builder
	writeSelectedQueue(&generated, "memory")
	body := strings.ReplaceAll(generated.String(), "queue.DefaultConfig()", "testConfig()")
	program := `package main
import ("fmt"; "os"; "github.com/light-speak/luxo/pkg/lux/env"; "github.com/light-speak/luxo/pkg/lux/queue")
func testConfig() queue.Config {
 switch os.Args[1] {
 case "retries": return queue.Config{MaxRetries:-1}
 case "delay": return queue.Config{RetryDelay:-1}
 case "concurrency": return queue.Config{Concurrency:-1}
 }
 return queue.Config{}
}
func main() {
` + body + "\nfmt.Println(taskQueue != nil)\n}\n"
	path, binary := filepath.Join(dir, "main.go"), filepath.Join(dir, "probe")
	if err := os.WriteFile(path, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), "go", "build", "-race", "-o", binary, path).CombinedOutput(); err != nil {
		t.Fatalf("build generated queue initialization: %v\n%s", err, out)
	}
	for _, name := range []string{"defaults", "retries", "delay", "concurrency"} {
		t.Run(name, func(t *testing.T) { checkGeneratedQueueInitialization(t, binary, name) })
	}
}

func checkGeneratedQueueInitialization(t *testing.T, binary, name string) {
	t.Helper()
	command := exec.CommandContext(t.Context(), binary, name)
	command.Env = append(os.Environ(), "LUXO_QUEUE_BACKEND=memory")
	output, err := command.CombinedOutput()
	if name == "defaults" {
		if err != nil || string(output) != "true\n" {
			t.Fatalf("default initialization: %v, %s", err, output)
		}
		return
	}
	if err == nil || !strings.Contains(string(output), "fatal: initialize messaging: queue: configuration values must not be negative") || strings.Contains(string(output), "true") {
		t.Fatalf("invalid initialization was not rejected: %v, %s", err, output)
	}
}

func TestGeneratedQueueCloseReportsFailure(t *testing.T) {
	input := &semantic.Result{Files: []*ast.File{{Name: "origin/service.luxo", Models: []*ast.ModelDecl{{Name: "User"}}}}}
	var cleanup string
	for _, backend := range []string{"memory", "nats"} {
		options := EntryBackends{Queue: backend}
		source, err := GenerateEntryFileChecked(input, "example.com/app", options)
		if err != nil {
			t.Fatal(err)
		}
		entries, err := GenerateModuleEntryFilesChecked(input, "example.com/app", options)
		if err != nil {
			t.Fatal(err)
		}
		entries["embedded"] = source
		for name, source := range entries {
			got := generatedQueueCleanup(t, source)
			if !strings.Contains(got, "if err := taskQueue.Close(); err != nil") {
				t.Errorf("%s/%s discards queue shutdown errors: %s", backend, name, got)
			}
			if cleanup != "" && cleanup != got {
				t.Fatal("queue shutdown differs between entry modes")
			}
			cleanup = got
		}
	}
	if !t.Failed() {
		executeGeneratedQueueCleanup(t, cleanup)
	}
}

func generatedQueueCleanup(t *testing.T, source []byte) string {
	t.Helper()
	fs := gotoken.NewFileSet()
	file, err := goparser.ParseFile(fs, "entry.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	var cleanup string
	goast.Inspect(file, func(node goast.Node) bool {
		statement, ok := node.(*goast.DeferStmt)
		if !ok {
			return true
		}
		var out bytes.Buffer
		if err := format.Node(&out, fs, statement); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "taskQueue.Close()") {
			if cleanup != "" {
				t.Fatal("duplicate queue cleanup")
			}
			cleanup = out.String()
		}
		return false
	})
	if cleanup == "" {
		t.Fatal("missing queue cleanup")
	}
	return cleanup
}

func executeGeneratedQueueCleanup(t *testing.T, cleanup string) {
	t.Helper()
	root, err := filepath.Abs("../../.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "entry-queue-close-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	program := `package main
import ("errors";"fmt";"os")
type taskQueueStub struct{}
func (taskQueueStub) Close() error {
 fmt.Println("close-called")
 if len(os.Args)>1 { return errors.New("shutdown failed") }; return nil
}
func main(){
 defer fmt.Println("remaining-cleanup")
 taskQueue:=taskQueueStub{}
` + cleanup + "\n}\n"
	path, binary := filepath.Join(dir, "main.go"), filepath.Join(dir, "probe")
	if err := os.WriteFile(path, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.CommandContext(t.Context(), "go", "build", "-o", binary, path).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	for _, fail := range []bool{false, true} {
		args := []string{}
		if fail {
			args = append(args, "fail")
		}
		command := exec.CommandContext(t.Context(), binary, args...)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		stdout, err := command.Output()
		if err != nil || string(stdout) != "close-called\nremaining-cleanup\n" {
			t.Fatalf("cleanup did not complete exactly once: %v %s", err, stdout)
		}
		expected := ""
		if fail {
			expected = "error: close task queue: shutdown failed\n"
		}
		if stderr.String() != expected {
			t.Fatalf("shutdown diagnostic=%q want=%q", stderr.String(), expected)
		}
	}
}

func TestGeneratedEventCloseReportsFailure(t *testing.T) {
	input := &semantic.Result{Files: []*ast.File{{Name: "origin/service.luxo", Models: []*ast.ModelDecl{{Name: "User"}}, Events: []*ast.EventDecl{{Name: "Changed"}}}}}
	for _, backend := range []string{"memory", "nats"} {
		options := EntryBackends{Events: backend}
		source, err := GenerateEntryFileChecked(input, "example.com/app", options)
		if err != nil {
			t.Fatal(err)
		}
		entries, err := GenerateModuleEntryFilesChecked(input, "example.com/app", options)
		if err != nil {
			t.Fatal(err)
		}
		entries["embedded"] = source
		for name, source := range entries {
			// Reuse the existing executable cleanup harness with only fixture names changed.
			renamed := strings.ReplaceAll(string(source), "eventBus", "taskQueue")
			cleanup := generatedQueueCleanup(t, []byte(renamed))
			if !strings.Contains(cleanup, "if err := taskQueue.Close(); err != nil") || !strings.Contains(cleanup, "error: close event bus:") {
				t.Fatalf("%s/%s discards event close failure: %s", backend, name, cleanup)
			}
			executeGeneratedQueueCleanup(t, strings.ReplaceAll(cleanup, "error: close event bus:", "error: close task queue:"))
		}
	}
}

func TestGeneratedBackendShutdownDependencyOrder(t *testing.T) {
	input := &semantic.Result{Files: []*ast.File{{Name: "origin/service.luxo", Models: []*ast.ModelDecl{{Name: "User"}}, Events: []*ast.EventDecl{{Name: "Changed"}}}}}
	for _, backend := range []string{"memory", "nats"} {
		selection := EntryBackends{Events: backend, Queue: backend}
		source, err := GenerateEntryFileChecked(input, "example.com/app", selection)
		if err != nil {
			t.Fatal(err)
		}
		entries, err := GenerateModuleEntryFilesChecked(input, "example.com/app", selection)
		if err != nil {
			t.Fatal(err)
		}
		entries["embedded"] = source
		for name, source := range entries {
			fs := gotoken.NewFileSet()
			file, err := goparser.ParseFile(fs, "entry.go", source, 0)
			if err != nil {
				t.Fatal(err)
			}
			order := []string{}
			goast.Inspect(file, func(node goast.Node) bool {
				statement, ok := node.(*goast.DeferStmt)
				if !ok {
					return true
				}
				var out bytes.Buffer
				if err := format.Node(&out, fs, statement); err != nil {
					t.Fatal(err)
				}
				code := out.String()
				switch {
				case strings.Contains(code, "db.Close()") || strings.Contains(code, "app.Close()"):
					order = append(order, "database")
				case strings.Contains(code, "eventBus.Close()"):
					order = append(order, "events")
				case strings.Contains(code, "taskQueue.Close()"):
					order = append(order, "queue")
				}
				return false
			})
			if got := strings.Join(order, ","); got != "database,events,queue" {
				t.Fatalf("%s/%s registers unsafe deferred shutdown order: %s", backend, name, got)
			}
		}
	}
}

func TestCheckedEntriesRejectInvalidSemanticResults(t *testing.T) {
	invalid := analyzePermissionFixture(t, "type Detail { level: Int }\nmodel Record { id: Int @id detail: Detail }")
	for _, entry := range []struct {
		name     string
		generate func(*semantic.Result) (bool, error)
	}{
		{"schema", func(input *semantic.Result) (bool, error) {
			output, err := BuildSchemaJSON(input, nil, GeneratorConfig{})
			return output == nil, err
		}},
		{"handlers", func(input *semantic.Result) (bool, error) {
			output, err := Generate(input, "generated", GeneratorConfig{})
			return output == nil, err
		}},
		{"embedded", func(input *semantic.Result) (bool, error) {
			output, err := GenerateEntryFileChecked(input, "example.com/app")
			return output == nil, err
		}},
		{"modules", func(input *semantic.Result) (bool, error) {
			output, err := GenerateModuleEntryFilesChecked(input, "example.com/app")
			return output == nil, err
		}},
		{"gateway", func(input *semantic.Result) (bool, error) {
			output, err := GenerateGatewayEntryChecked(input, "example.com/app")
			return output == nil, err
		}},
	} {
		for _, sample := range []struct {
			name       string
			input      *semantic.Result
			diagnostic string
		}{
			{"nil", nil, "semantic result"},
			{"invalid", invalid, "has no database mapping"},
		} {
			t.Run(entry.name+"/"+sample.name, func(t *testing.T) {
				defer func() {
					if failure := recover(); failure != nil {
						t.Errorf("checked entry panicked: %v", failure)
					}
				}()
				empty, err := entry.generate(sample.input)
				if !empty || err == nil || !strings.Contains(err.Error(), sample.diagnostic) {
					t.Fatalf("invalid analysis accepted: empty=%v error=%v", empty, err)
				}
			})
		}
	}
}
