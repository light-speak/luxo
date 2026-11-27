package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/codegen"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/lockfile"
	"github.com/light-speak/luxo/pkg/lux"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
)

func enterCacheGenerationScratch(t testing.TB) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(root, "cache-module-generation-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Error(err)
		}
	})
	t.Chdir(directory)
}

func TestAddPreservesExistingModuleFiles(t *testing.T) {
	for _, path := range []string{"service/user/resolver/resolver.go", "origin/user/model.luxo", "origin/user.luxo"} {
		t.Run(path, func(t *testing.T) {
			prepareAddProject(t)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("keep existing source\n"), 0600); err != nil {
				t.Fatal(err)
			}
			before := addProjectSnapshot(t)
			output, err := captureCLIResponse(t, func() error { return runAdd(nil, []string{"user"}) })
			if err == nil || output != "" {
				t.Errorf("existing module was overwritten or reported success: output=%q error=%v", output, err)
			}
			assertAddProjectSnapshot(t, before)
		})
	}
}

func TestAddRejectsModulePathBeforeWriting(t *testing.T) {
	for _, name := range []string{
		"../victim", "nested/name", `nested\name`, "", ".", "..", "line\nbreak", "line\rbreak", "nul\x00name", string([]byte{0xff}),
		"billing-v2", "billing.v2", "2billing", "sales report", "订单", "_",
	} {
		t.Run(strconv.Quote(name), func(t *testing.T) {
			prepareAddProject(t)
			before := addProjectSnapshot(t)
			output, err := captureCLIResponse(t, func() error { return runAdd(nil, []string{name}) })
			if err == nil || !strings.Contains(err.Error(), "module name") || output != "" {
				t.Errorf("invalid module path reached scaffold output: name=%q output=%q error=%v", name, output, err)
			}
			assertAddProjectSnapshot(t, before)
		})
	}
}

func TestAddRejectsReservedScaffoldNamesBeforeWriting(t *testing.T) {
	tests := []struct {
		names      []string
		diagnostic string
	}{
		{[]string{
			"int", "string", "float", "boolean", "dateTime", "duration", "uUID",
			"decimal", "bytes", "jSON", "result", "channel", "page", "cursor", "identity",
			"app", "new", "newFromDB", "registerSchema", "queue", "close", "tx", "dB",
		}, "model name"},
		{[]string{"internal", "vendor"}, "Go import path"},
		{[]string{strings.Repeat("a", 63)}, "database table name"},
	}
	for _, test := range tests {
		for _, name := range test.names {
			t.Run(name, func(t *testing.T) {
				prepareAddProject(t)
				before := addProjectSnapshot(t)
				output, err := captureCLIResponse(t, func() error { return runAdd(nil, []string{name}) })
				if err == nil || !strings.Contains(err.Error(), "module name") || !strings.Contains(err.Error(), test.diagnostic) || output != "" {
					t.Fatalf("reserved name %q lost its diagnostic or published success: output=%q error=%v", name, output, err)
				}
				assertAddProjectSnapshot(t, before)
			})
		}
	}
}

func TestAddPreservesDanglingResolverLink(t *testing.T) {
	prepareAddProject(t)
	path := filepath.Join("service", "user", "resolver", "resolver.go")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing.go", path); err != nil {
		t.Fatal(err)
	}
	before := addProjectSnapshot(t)
	output, err := captureCLIResponse(t, func() error { return runAdd(nil, []string{"user"}) })
	if err == nil || output != "" {
		t.Errorf("dangling resolver link was followed: output=%q error=%v", output, err)
	}
	assertAddProjectSnapshot(t, before)
}

func TestAddRejectsSymlinkOutsideProject(t *testing.T) {
	enterCacheGenerationScratch(t)
	for _, path := range []string{"outside/user/resolver", "project/origin"} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	outside, err := filepath.Abs("outside/user/resolver/resolver.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("keep outside resolver\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside", "project/service"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("project/go.mod", []byte("module example.com/add\n\ngo 1.27.1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir("project")
	before := addProjectSnapshot(t)
	output, err := captureCLIResponse(t, func() error { return runAdd(nil, []string{"user"}) })
	if err == nil || output != "" {
		t.Errorf("add followed a link outside the project: output=%q error=%v", output, err)
	}
	assertAddProjectSnapshot(t, before)
	if data, err := os.ReadFile(outside); err != nil || string(data) != "keep outside resolver\n" {
		t.Fatal("outside resolver was overwritten", string(data), err)
	}
}

func TestAddWriteFailureRemovesNewFiles(t *testing.T) {
	prepareAddProject(t)
	path := filepath.Join("service", "user")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(path, 0700); err != nil {
			t.Error(err)
		}
	})
	probe := filepath.Join(path, "probe")
	file, err := os.OpenFile(probe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err == nil {
		file.Close()
		os.Remove(probe)
		t.Skip("filesystem credentials permit writes to a read-only directory")
	} else if !errors.Is(err, os.ErrPermission) {
		t.Fatal(err)
	}
	before := addProjectSnapshot(t)
	output, err := captureCLIResponse(t, func() error { return runAdd(nil, []string{"user"}) })
	if !errors.Is(err, os.ErrPermission) || output != "" {
		t.Errorf("failed write lost its error or published partial success: output=%q error=%v", output, err)
	}
	assertAddProjectSnapshot(t, before)
}

func TestAddModuleGeneratesBuildableProject(t *testing.T) {
	for _, name := range []string{"user_v2", "testdata", "_sample"} {
		t.Run(name, func(t *testing.T) {
			writeGlobalGenerationProject(t, nil)
			output, err := captureCLIResponse(t, func() error { return runAdd(nil, []string{name}) })
			if err != nil || !strings.Contains(output, "Module "+name+" added") {
				t.Fatal("valid add failed", output, err)
			}
			modulePath, err := readModulePath()
			if err != nil {
				t.Fatal(err)
			}
			for path, want := range codegen.AddModule(modulePath, name).Files {
				if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, want) {
					t.Fatal("scaffold bytes changed", path, err)
				}
			}
			if output, err := captureCLIResponse(t, func() error { return runGen(nil, nil) }); err != nil {
				t.Fatal("added module did not generate", output, err)
			}
			command := exec.Command("go", "build", "-o", "entry.test", "./lucis/"+name)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("added module did not build: %v\n%s", err, output)
			}
		})
	}
}

func TestAddPreservesMaximumLengthTableName(t *testing.T) {
	writeGlobalGenerationProject(t, nil)
	name := strings.Repeat("a", 62)
	if output, err := captureCLIResponse(t, func() error { return runAdd(nil, []string{name}) }); err != nil {
		t.Fatal("valid table name rejected", output, err)
	}
	if output, err := captureCLIResponse(t, func() error { return runGen(nil, nil) }); err != nil {
		t.Fatal("valid table name did not generate", output, err)
	}
}

func TestAddExclusiveCreatePreservesLateFile(t *testing.T) {
	prepareAddProject(t)
	path := filepath.Join("origin", "user.luxo")
	if err := os.WriteFile(path, []byte("created by another writer\n"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	before := addProjectSnapshot(t)
	if err := writeNewModuleFile(root, path, []byte("replacement\n")); !errors.Is(err, os.ErrExist) {
		t.Errorf("late file creation was not rejected: %v", err)
	}
	assertAddProjectSnapshot(t, before)
}

func TestAddCleanupPreservesRemovalError(t *testing.T) {
	prepareAddProject(t)
	root, err := os.OpenRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	before := addProjectSnapshot(t)
	err = removeModuleFiles(root, []string{"already_removed.go"})
	if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "remove incomplete scaffold already_removed.go") {
		t.Errorf("cleanup lost the original removal error: %v", err)
	}
	assertAddProjectSnapshot(t, before)
}

func prepareAddProject(t *testing.T) {
	t.Helper()
	enterCacheGenerationScratch(t)
	for _, name := range []string{"origin", "service"} {
		if err := os.Mkdir(name, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile("go.mod", []byte("module example.com/add\n\ngo 1.27.1\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func addProjectSnapshot(t *testing.T) map[string]string {
	t.Helper()
	files := make(map[string]string)
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			files[path] = "directory"
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			files[path] = "symlink:" + target
			return err
		}
		data, err := os.ReadFile(path)
		files[path] = "file:" + string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func assertAddProjectSnapshot(t *testing.T, before map[string]string) {
	t.Helper()
	after := addProjectSnapshot(t)
	if len(after) != len(before) {
		t.Errorf("rejected add changed file/directory count: before=%v after=%v", before, after)
	}
	for path, want := range before {
		if after[path] != want {
			t.Errorf("rejected add changed %s: got=%q want=%q", path, after[path], want)
		}
	}
}

func TestInitReadFailurePreservesExistingProject(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.Mkdir("example", 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"go.mod", ".gitignore", ".env.example"} {
		if err := os.WriteFile(filepath.Join("example", name), []byte("keep existing "+name+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	before := addProjectSnapshot(t)
	if err := os.Chmod("example", 0333); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod("example", 0700); err != nil {
			t.Error(err)
		}
	})
	if _, err := os.ReadDir("example"); err == nil {
		t.Skip("filesystem credentials permit reading a write-only directory")
	} else if !errors.Is(err, os.ErrPermission) {
		t.Fatal(err)
	}
	output, err := captureCLIResponse(t, func() error { return runInit(nil, []string{"example"}) })
	if !errors.Is(err, os.ErrPermission) || output != "" {
		t.Errorf("unreadable project was changed or reported success: output=%q error=%v", output, err)
	}
	if err := os.Chmod("example", 0700); err != nil {
		t.Fatal(err)
	}
	assertAddProjectSnapshot(t, before)
}

func TestInitPreservesExistingNonDirectory(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.WriteFile("example", []byte("keep existing file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before := addProjectSnapshot(t)
	output, err := captureCLIResponse(t, func() error { return runInit(nil, []string{"example"}) })
	if err == nil || output != "" {
		t.Errorf("existing file reported success: output=%q error=%v", output, err)
	}
	assertAddProjectSnapshot(t, before)
}

func TestInitPreservesScaffoldBytesAndEmptyRoot(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(strconv.FormatBool(existing), func(t *testing.T) {
			enterCacheGenerationScratch(t)
			if existing {
				if err := os.Mkdir("example", 0700); err != nil {
					t.Fatal(err)
				}
			}
			output, err := captureCLIResponse(t, func() error { return runInit(nil, []string{"example"}) })
			if err != nil || !strings.Contains(output, "Project example created") {
				t.Fatal("valid init failed", output, err)
			}
			for path, want := range codegen.InitProject("example").Files {
				if data, err := os.ReadFile(filepath.Join("example", path)); err != nil || !bytes.Equal(data, want) {
					t.Fatal("init scaffold changed", path, err)
				}
			}
			if entries, err := os.ReadDir("example/origin"); err != nil || len(entries) != 0 {
				t.Fatal("initial origin was not empty", entries, err)
			}
			before := addProjectSnapshot(t)
			output, err = captureCLIResponse(t, func() error { return runInit(nil, []string{"example"}) })
			if err == nil || output != "" {
				t.Errorf("repeated init reported success: output=%q error=%v", output, err)
			}
			assertAddProjectSnapshot(t, before)
		})
	}
}

func TestInitPinsCoreAndDefaultBackend(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := runInit(nil, []string{"example"}); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile("example/go.mod")
	if err != nil {
		t.Fatal(err)
	}
	for _, module := range []string{"github.com/light-speak/luxo", "github.com/light-speak/luxo/pkg/lux/pg"} {
		if !strings.Contains(string(manifest), module+" v"+Version+"\n") {
			t.Fatalf("CLI generated mismatched dependency: %s", manifest)
		}
	}
	settings, err := os.ReadFile("example/.env.example")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(settings), "LUXO_EVENT_BACKEND=none") {
		t.Fatal("missing explicit build selection")
	}
	if err := runInit(nil, []string{"example"}); err == nil {
		t.Fatal("existing project overwritten")
	}
}

func TestInitScaffoldFailurePreservesConcurrentFiles(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.MkdirAll("project/locked", 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir("project")
	if err := os.Chmod("locked", 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod("locked", 0700); err != nil {
			t.Error(err)
		}
	})
	before := addProjectSnapshot(t)
	root, err := os.OpenRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	files := map[string][]byte{"a.txt": []byte("new first file"), "locked/b.txt": []byte("blocked second file")}
	err = writeProjectScaffold(root, files)
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("write failure lost its original error: %v", err)
	}
	assertAddProjectSnapshot(t, before)
	if err := os.Chmod("locked", 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeProjectScaffold(root, files); err != nil {
		t.Fatal("cleaned project could not retry", err)
	}
	for path, want := range files {
		if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, want) {
			t.Fatal("retry lost scaffold data", path, err)
		}
	}
}

func TestInitPreservesLateProjectDirectory(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.Mkdir("example", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("example/keep.txt", []byte("concurrent user content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	project, err := filepath.Abs("example")
	if err != nil {
		t.Fatal(err)
	}
	before := addProjectSnapshot(t)
	err = createProjectScaffold(project, codegen.InitProject("example").Files, true)
	if !errors.Is(err, os.ErrExist) {
		t.Errorf("late project directory was not rejected: %v", err)
	}
	assertAddProjectSnapshot(t, before)
}

func TestInitPreservesLateOutputFile(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.WriteFile("go.mod", []byte("concurrent manifest\n"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	before := addProjectSnapshot(t)
	err = writeProjectScaffold(root, codegen.InitProject("example").Files)
	if !errors.Is(err, os.ErrExist) {
		t.Errorf("late output file was not rejected: %v", err)
	}
	assertAddProjectSnapshot(t, before)
}

func TestInitParentChangedAfterDiscovery(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.WriteFile("parent", []byte("concurrent parent file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	project, err := filepath.Abs("parent/example")
	if err != nil {
		t.Fatal(err)
	}
	before := addProjectSnapshot(t)
	err = createProjectScaffold(project, codegen.InitProject("example").Files, true)
	if err == nil || !strings.Contains(err.Error(), "create project parent directory") {
		t.Errorf("changed parent was not rejected: %v", err)
	}
	assertAddProjectSnapshot(t, before)
}

func TestInitRootRemovedAfterDiscovery(t *testing.T) {
	enterCacheGenerationScratch(t)
	project, err := filepath.Abs("removed")
	if err != nil {
		t.Fatal(err)
	}
	before := addProjectSnapshot(t)
	err = createProjectScaffold(project, codegen.InitProject("example").Files, false)
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing existing root lost its error: %v", err)
	}
	assertAddProjectSnapshot(t, before)
}

func TestInitSearchPermissionFailurePreservesRoot(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.Mkdir("example", 0700); err != nil {
		t.Fatal(err)
	}
	before := addProjectSnapshot(t)
	if err := os.Chmod("example", 0666); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod("example", 0700); err != nil {
			t.Error(err)
		}
	})
	output, err := captureCLIResponse(t, func() error { return runInit(nil, []string{"example"}) })
	if !errors.Is(err, os.ErrPermission) || output != "" {
		t.Errorf("unsearchable project lost its error or reported success: %q %v", output, err)
	}
	if err := os.Chmod("example", 0700); err != nil {
		t.Fatal(err)
	}
	assertAddProjectSnapshot(t, before)
}

func TestInitInvalidScaffoldRemovesOwnedEmptyRoot(t *testing.T) {
	enterCacheGenerationScratch(t)
	project, err := filepath.Abs("example")
	if err != nil {
		t.Fatal(err)
	}
	before := addProjectSnapshot(t)
	err = createProjectScaffold(project, map[string][]byte{"../escape.txt": []byte("invalid scaffold")}, true)
	if err == nil {
		t.Error("invalid scaffold escaped its project")
	}
	assertAddProjectSnapshot(t, before)
}

func TestInitProjectCreatesBuildableModuleWithoutChangingManifest(t *testing.T) {
	core, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	enterCacheGenerationScratch(t)
	if output, err := captureCLIResponse(t, func() error { return runInit(nil, []string{"example"}) }); err != nil {
		t.Fatal(output, err)
	}
	t.Chdir("example")
	manifest, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	workspace := "go 1.27.4\n\nuse (\n .\n " + strconv.Quote(core) + "\n " + strconv.Quote(filepath.Join(core, "pkg/lux/pg")) + "\n)\nreplace github.com/light-speak/luxo v1.0.0-beta.1 => " + strconv.Quote(core) + "\nreplace github.com/light-speak/luxo/pkg/lux/pg v1.0.0-beta.1 => " + strconv.Quote(filepath.Join(core, "pkg/lux/pg")) + "\n"
	if err := os.WriteFile("go.work", []byte(workspace), 0600); err != nil {
		t.Fatal(err)
	}
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOWORK", filepath.Join(directory, "go.work"))
	for _, key := range []string{"LUXO_EVENT_BACKEND", "LUXO_QUEUE_BACKEND", "LUXO_CACHE_BACKEND"} {
		t.Setenv(key, "none")
	}
	t.Setenv("DATABASE_DRIVER", "pg")
	if output, err := captureCLIResponse(t, func() error { return runAdd(nil, []string{"user"}) }); err != nil {
		t.Fatal(output, err)
	}
	if output, err := captureCLIResponse(t, func() error { return runGen(nil, nil) }); err != nil {
		t.Fatal(output, err)
	}
	command := exec.CommandContext(t.Context(), "go", "build", "-mod=readonly", "./...")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("initialized project failed to build: %v\n%s", err, output)
	}
	if after, err := os.ReadFile("go.mod"); err != nil || !bytes.Equal(after, manifest) {
		t.Fatal("local consumer changed initialized go.mod", err)
	}
}

func TestCRUDLockAndGeneratedSchemaUseTheSameNames(t *testing.T) {
	enterCacheGenerationScratch(t)
	file := &ast.File{Name: "origin/main/model.luxo"}
	for _, name := range []string{"TimeEntry", "Address", "Box", "Quiz", "Gateway"} {
		file.Models = append(file.Models, &ast.ModelDecl{
			Name: name, Directives: []*ast.Directive{{Name: "crud"}},
			Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}}}},
		})
	}
	files := []*ast.File{file}
	result, err := analyzeFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	lf := lockfile.New()
	if err := lf.Update(files); err != nil {
		t.Fatal(err)
	}
	generator, err := buildProjectGenerator(lf, files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generateModules(result, generator); err != nil {
		t.Fatal(err)
	}
	raw, err := generator.BuildSchemaJSON(result, nil)
	if err != nil {
		t.Fatal(err)
	}
	var published schema.Schema
	if err := json.Unmarshal(raw, &published); err != nil {
		t.Fatal(err)
	}
	if err := published.Prepare(); err != nil {
		t.Fatal(err)
	}
	for name, definition := range published.APIs {
		if locked := lf.APIs[name]; locked == nil || locked.ID != definition.ID {
			t.Fatalf("exported API %s differs from its locked ID", name)
		}
	}
}

func TestCachePolicyReadFailure(t *testing.T) {
	if _, err := loadCachePolicies("."); err == nil {
		t.Fatal("unreadable policy file accepted")
	}
}

func parseGenerationSource(t testing.TB, source, path string) *ast.File {
	t.Helper()
	tokens, lexErrors := lexer.New(source, path).Tokenize()
	if len(lexErrors) != 0 {
		t.Fatal(lexErrors)
	}
	file, parseErrors := parser.New(tokens).Parse(path)
	if len(parseErrors) != 0 {
		t.Fatal(parseErrors)
	}
	return file
}

func TestModuleGenerationPreservesGenericWhenTypes(t *testing.T) {
	enterCacheGenerationScratch(t)
	configureGeneratedModuleWorkspace(t)
	var files []*ast.File
	for _, name := range []string{"Alpha", "Beta"} {
		source := strings.ReplaceAll(moduleGenericWhenSource, "NAME", name)
		files = append(files, parseGenerationSource(t, source, "origin/"+strings.ToLower(name)+"/model.luxo"))
	}
	analyzed, err := analyzeFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	locked := lockfile.New()
	if err := locked.Update(analyzed.Files); err != nil {
		t.Fatal(err)
	}
	generator, err := buildProjectGenerator(locked, analyzed.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generateModules(analyzed, generator); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Alpha", "Beta"} {
		assertions := strings.ReplaceAll(moduleGenericWhenAssertions, "NAME", name)
		if err := os.WriteFile(filepath.Join("service", strings.ToLower(name), "luxo", "generic_test.go"), []byte(assertions), 0600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command("go", "test", "-race", "./service/...")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated generic modules failed: %v\n%s", err, output)
	}
}

const moduleGenericWhenSource = `
model RecordNAME @noTime { id: Int @id }
fn chooseNAME(flag: Boolean, value: Int?): Int {
 val left: Channel<Int?> = Channel(1)
 val right: Channel<Int?> = Channel(1)
 val channel = when { flag -> { left } else -> { right } }
 channel <- value
 return when {
  <-left -> { item -> if !flag { throw error.BadRequest } item ?: 42 }
  <-right -> { item -> if flag { throw error.BadRequest } item ?: 42 }
 }
}
api probeNAME(flag: Boolean,value: Int?): Int { return chooseNAME(flag,value) }
`

const moduleGenericWhenAssertions = `package luxo
import "testing"
func TestGenericWhen(t *testing.T){
 app:=&App{};input:=int64(7)
 for _,flag:=range []bool{false,true}{
  for _,value:=range []*int64{nil,&input}{
   want:=int64(42);if value!=nil{want=*value}
   got,err:=app.chooseNAME(t.Context(),flag,value)
   if err!=nil||got!=want{t.Fatal(got,err,want)}
  }
 }
}
`

func TestModuleGenerationPreservesInheritedFields(t *testing.T) {
	enterCacheGenerationScratch(t)
	configureGeneratedModuleWorkspace(t)
	var files []*ast.File
	for name, source := range map[string]string{
		"common/base": "model Base @noTime { id: Int @id }",
		"item/model":  "model Item : Base @noTime { name: String } api getItemById(key: Int): Item",
	} {
		path := "origin/" + name + ".luxo"
		files = append(files, parseGenerationSource(t, source, path))
	}
	analyzed, err := analyzeFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	locked := lockfile.New()
	if err := locked.Update(analyzed.Files); err != nil {
		t.Fatal(err)
	}
	generator, err := buildProjectGenerator(locked, analyzed.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generateModules(analyzed, generator); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("service/item/luxo/db.gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `case "id":`) {
		t.Fatal("module generation lost the inherited primary key")
	}
	command := exec.Command("go", "test", "./service/...")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated inherited modules do not compile: %v\n%s", err, output)
	}
}

func TestModuleGenerationPreservesExternalStoredFields(t *testing.T) {
	enterCacheGenerationScratch(t)
	configureGeneratedModuleWorkspace(t)
	var files []*ast.File
	for name, source := range map[string]string{
		"common/types": "enum Role { USER ADMIN }",
		"user/model":   "model User @noTime { id: Int @id role: Role optionalRole: Role? tags: [String]? }",
		"post/model":   "extend User { role: Role optionalRole: Role? tags: [String]? } model Post @noTime { id: Int @id }",
		"user/stream":  "api watchRole(): Role @stream @native",
		"post/events":  "event RoleChanged(role: Role) api roleUpdates(): Role @stream(RoleChanged) { it.role == Role.ADMIN }",
	} {
		path := "origin/" + name + ".luxo"
		files = append(files, parseGenerationSource(t, source, path))
	}
	analyzed, err := analyzeFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	locked := lockfile.New()
	if err := locked.Update(analyzed.Files); err != nil {
		t.Fatal(err)
	}
	generator, err := buildProjectGenerator(locked, analyzed.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generateModules(analyzed, generator); err != nil {
		t.Fatal(err)
	}
	shared, err := os.ReadFile(filepath.Join("internal", "luxotypes", "enum.gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(shared), "type Role string") {
		t.Fatal("missing canonical enum definition")
	}
	common, err := os.ReadFile(filepath.Join("service", "common", "luxo", "model.gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(common), "type Role = luxotypes.Role") {
		t.Fatal("declaring module must use the canonical enum alias")
	}
	raw, err := os.ReadFile(filepath.Join("service", "post", "luxo", "db.gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"role", "optional_role", "tags"} {
		if !strings.Contains(string(raw), `case "`+field+`":`) {
			t.Fatalf("formal module generation dropped stored field %s", field)
		}
	}
	writeSharedEnumAssertions(t)
	command := exec.Command("go", "test", "-race", "./service/...", "./internal/...")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated modules do not compile: %v\n%s", err, output)
	}
}

func TestSharedEnumCleanupPreservesHandwrittenFiles(t *testing.T) {
	enterCacheGenerationScratch(t)
	directory := filepath.Join("internal", "luxotypes")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	generated := filepath.Join(directory, "enum.gen.go")
	helper := filepath.Join(directory, "helper.go")
	for path, source := range map[string]string{
		generated: "// Code generated by luxo. DO NOT EDIT.\npackage luxotypes\n",
		helper:    "package luxotypes\n",
	} {
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	generator, err := codegen.NewGenerator(codegen.GeneratorConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := generateSharedEnums(generator); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(generated); !os.IsNotExist(err) {
		t.Fatalf("stale shared enums remain: %v", err)
	}
	if _, err := os.Stat(helper); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(generated, []byte("package luxotypes\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := generateSharedEnums(generator); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(generated); err != nil {
		t.Fatal("unmarked file was removed", err)
	}
}

func TestSharedEnumCleanupRejectsLinkedDirectory(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.Mkdir("target", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", "internal"); err != nil {
		t.Fatal(err)
	}
	generator, err := codegen.NewGenerator(codegen.GeneratorConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := generateSharedEnums(generator); err == nil {
		t.Fatal("shared enum cleanup accepted a linked directory")
	}
}

func TestSharedEnumGenerationRefusesUnownedTargets(t *testing.T) {
	for _, kind := range []string{"handwritten", "symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			enterCacheGenerationScratch(t)
			directory := filepath.Join("internal", "luxotypes")
			if err := os.MkdirAll(directory, 0755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "enum.gen.go")
			original := []byte("package luxotypes\n// handwritten\n")
			if err := os.WriteFile("original.go", original, 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "handwritten":
				if err := os.WriteFile(path, original, 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Join("..", "..", "original.go"), path); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0755); err != nil {
					t.Fatal(err)
				}
			}
			generator, err := codegen.NewGenerator(codegen.GeneratorConfig{Events: &codegen.EventContext{
				SharedEnumPackage: "example.com/project/internal/luxotypes",
				Enums:             map[string]*ast.EnumDecl{"Role": {Name: "Role", Values: []string{"USER"}}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			if err := generateSharedEnums(generator); err == nil {
				t.Fatal("unowned output was accepted")
			}
			readPath := "original.go"
			if kind == "handwritten" {
				readPath = path
			}
			got, err := os.ReadFile(readPath)
			if err != nil || string(got) != string(original) {
				t.Fatalf("original changed: %q, %v", got, err)
			}
		})
	}
}

func writeSharedEnumAssertions(t *testing.T) {
	t.Helper()
	source := `package luxo_test
import (
 "testing"
 "slices"
 "example.com/generated/internal/luxotypes"
 common "example.com/generated/service/common/luxo"
 user "example.com/generated/service/user/luxo"
 post "example.com/generated/service/post/luxo"
 "github.com/light-speak/luxo/pkg/lux/schema"
)
func TestSharedEnums(t *testing.T) {
 var canonical luxotypes.Role = user.RoleUSER
 var projected post.Role = canonical
 var declared common.Role = projected
 if declared != common.RoleUSER {t.Fatal("alias identity changed")}
 for _, register:=range []func(*schema.Schema)error{common.RegisterSchema,user.RegisterSchema,post.RegisterSchema} {
  s:=schema.New();if err:=register(s);err!=nil {t.Fatal(err)}
  role:=s.Enums["Role"]
  if role==nil||role.Module!="common"||!slices.Equal(role.Values,[]string{"USER","ADMIN"}) {t.Fatalf("enum metadata: %#v",role)}
 }
}
`
	if err := os.WriteFile(filepath.Join("service", "post", "luxo", "shared_enum_test.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}

func configureGeneratedModuleWorkspace(t testing.TB) {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	core := filepath.Dir(filepath.Dir(directory))
	for name, content := range map[string]string{
		"go.mod":  "module example.com/generated\n\ngo 1.27.4\n",
		"go.work": "go 1.27.4\n\nuse (\n .\n " + strconv.Quote(core) + "\n " + strconv.Quote(filepath.Join(core, "pkg/lux/pg")) + "\n)\nreplace github.com/light-speak/luxo v1.0.0-beta.1 => " + strconv.Quote(core) + "\n",
	} {
		if err := os.WriteFile(name, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GOWORK", filepath.Join(directory, "go.work"))
}

func TestModuleGenerationInternalOnlyAndDataOnlyPackages(t *testing.T) {
	enterCacheGenerationScratch(t)
	configureGeneratedModuleWorkspace(t)
	id := &ast.FieldDecl{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}}}
	name := &ast.FieldDecl{Name: "name", Type: &ast.TypeRef{Name: "String"}}
	files := []*ast.File{
		{Name: "origin/common/types.luxo", Enums: []*ast.EnumDecl{{Name: "Role", Values: []string{"USER", "ADMIN"}}}},
		{Name: "origin/user/model.luxo", Models: []*ast.ModelDecl{{Name: "User", Fields: []*ast.FieldDecl{id, name}}}},
		{Name: "origin/post/model.luxo", Models: []*ast.ModelDecl{{Name: "Post", Fields: []*ast.FieldDecl{id}}}, Extends: []*ast.ExtendDecl{{Name: "User", Fields: []*ast.FieldDecl{name}}}},
	}
	analyzed, err := analyzeFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	locked := lockfile.New()
	if err := locked.Update(analyzed.Files); err != nil {
		t.Fatal(err)
	}
	generator, err := buildProjectGenerator(locked, analyzed.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generateModules(analyzed, generator); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("service/common/resolver/resolver.go"); !os.IsNotExist(err) {
		t.Fatalf("data-only module generated a resolver: %v", err)
	}
	writeInternalSelectorFixture(t)
	writeInternalRouteFixture(t)
	command := exec.Command("go", "test", "-race", "./service/...")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated internal/data modules do not compile: %v\n%s", err, output)
	}
}

func TestFullGenerationInternalOnlyModules(t *testing.T) {
	enterCacheGenerationScratch(t)
	configureGeneratedModuleWorkspace(t)
	if err := os.MkdirAll("lucis/common", 0755); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{
		"main.gen.go": "// Code generated by luxo. DO NOT EDIT.\npackage main\nfunc main() { removedService() }\n",
		"helper.go":   "package main\n// Handwritten code must survive regeneration.\n",
	} {
		if err := os.WriteFile(filepath.Join("lucis/common", name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for name, source := range map[string]string{
		"common/types": "enum Role { USER ADMIN }",
		"user/model":   "model User @noTime { id: Int @id name: String }",
		"post/model":   "extend User { name: String } model Post @noTime { id: Int @id }",
	} {
		path := filepath.Join("origin", name+".luxo")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := runGen(nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("lucis/common/main.gen.go"); !os.IsNotExist(err) {
		t.Fatalf("stale generated process remains: %v", err)
	}
	if _, err := os.Stat("lucis/common/helper.go"); err != nil {
		t.Fatalf("handwritten helper removed: %v", err)
	}
	writeInternalSelectorFixture(t)
	writeInternalRouteFixture(t)
	command := exec.Command("go", "test", "-race", "./...")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("complete generated project does not compile: %v\n%s", err, output)
	}
}

func writeInternalSelectorFixture(t *testing.T) {
	t.Helper()
	const source = `package luxo
import (
 "testing"
 "github.com/light-speak/luxo/pkg/lux/selection"
)
func TestInternalProjection(t *testing.T) {
 fields := []*selection.Field{{Name:"name"}}
 got := selectUserSQLColumns(fields)
 if len(got)!=2 || got[0]!="\"name\"" || got[1]!="\"id\"" { t.Fatalf("projection = %v",got) }
 if len(fields)!=1 || fields[0].Name!="name" { t.Fatal("mutated selection") }
}
`
	if err := os.WriteFile("service/post/luxo/selection_test.go", []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}

func writeInternalRouteFixture(t *testing.T) {
	t.Helper()
	const source = `package luxo
import (
 "net/http/httptest"
 "strings"
 "testing"
 "github.com/light-speak/luxo/pkg/lux/api"
)
func TestModelOnlyInternalRouting(t *testing.T) {
 router := api.NewRouter()
 if err := RegisterHandlers(router, &App{}); err != nil { t.Fatal(err) }
 if err := RegisterBatchLoaders(router, &App{}); err != nil { t.Fatal(err) }
 routes := router.ExportHandlers()
 if len(routes)!=1 || routes["svc:batchLoad:User"]==nil { t.Fatalf("RPC routes = %v",routes) }
 for _,name := range []string{"svc:batchLoad:User","createUser","getUser","listUsers"} {
  response := httptest.NewRecorder()
  router.ServeHTTP(response, httptest.NewRequest("POST","/luvia",strings.NewReader("{\"$api\":\""+name+"\"}")))
  if response.Code!=404 { t.Fatalf("public route %s: %d %s",name,response.Code,response.Body.String()) }
 }
}
`
	if err := os.WriteFile("service/user/luxo/routing_test.go", []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyzeFilesLoadsExplicitCachePolicies(t *testing.T) {
	enterCacheGenerationScratch(t)
	path := "origin/main/api.luxo"
	tokens, lexErrors := lexer.New(`api cached(): Int @cache(policy: "preview") { return 1 }`, path).Tokenize()
	if len(lexErrors) != 0 {
		t.Fatal(lexErrors)
	}
	file, parseErrors := parser.New(tokens).Parse(path)
	if len(parseErrors) != 0 {
		t.Fatal(parseErrors)
	}
	if _, err := analyzeFiles([]*ast.File{file}); err == nil {
		t.Fatal("missing policy file guessed a contract")
	}
	for _, raw := range []string{`{}`, `{"version":1,"policies":{"preview":{"mode":"ttl","ttl":"1s"}},"backend":"redis"}`} {
		if err := os.WriteFile("luxo.cache.json", []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := analyzeFiles([]*ast.File{file}); err == nil {
			t.Fatal("invalid policy file ignored")
		}
	}
	if err := os.WriteFile("luxo.cache.json", []byte(`{"version":1,"policies":{"preview":{"mode":"ttl","ttl":"1s"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	analyzed, err := analyzeFiles([]*ast.File{file})
	if err != nil {
		t.Fatal(err)
	}
	lf := lockfile.New()
	if err := lf.Update(analyzed.Files); err != nil {
		t.Fatal(err)
	}
	generator, err := buildProjectGenerator(lf, analyzed.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generateModules(analyzed, generator); err != nil {
		t.Fatal(err)
	}
	metadata, err := os.ReadFile("service/main/luxo/schema.gen.go")
	if err != nil || !strings.Contains(string(metadata), `CachePolicy = &schema.CachePolicy{Mode: "ttl", TTL: 1000000000, Scope: "private"}`) {
		t.Fatalf("module lost compiled contract: %s %v", metadata, err)
	}
}

func TestGenerateModulesPreservesCacheDependencyAnalysis(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.WriteFile("luxo.cache.json", []byte(`{"version":1,"policies":{"preview":{"mode":"ttl","ttl":"1m"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, name := range []string{"first", "second"} {
		path := "origin/" + name + "/api.luxo"
		source := "api " + name + `(): Int @native @cache(policy: "preview") @cacheable(dependsOn: ["pricing/rates"])`
		files = append(files, parseGenerationSource(t, source, path))
	}
	analyzed, err := analyzeFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	lf := lockfile.New()
	if err := lf.Update(analyzed.Files); err != nil {
		t.Fatal(err)
	}
	generator, err := buildProjectGenerator(lf, analyzed.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generateModules(analyzed, generator); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first", "second"} {
		source, err := os.ReadFile(filepath.Join("service", name, "luxo", "handler.gen.go"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(source), `router.RequireCache("`+name+`")`) {
			t.Fatalf("module %s lost its cached API", name)
		}
		metadata, err := os.ReadFile(filepath.Join("service", name, "luxo", "schema.gen.go"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(metadata), `s.APIs["`+name+`"].CacheDependencies`) || !strings.Contains(string(metadata), `"pricing/rates"`) {
			t.Fatalf("module %s lost execution sources: %s", name, metadata)
		}
	}
}

func TestCallFieldMaskUsesStableSchemaIDs(t *testing.T) {
	runtimeSchema := schema.New()
	runtimeSchema.RegisterModel(&schema.Model{Name: "Payload", Fields: []schema.Field{
		{ID: 1, Name: "id", Type: schema.FieldInt},
		{ID: 12, Name: "metadata", Type: schema.FieldJSON},
	}})
	apiSchema := &schema.API{Name: "getPayload", ReturnType: "Payload"}

	mask, err := callFieldMask("metadata,id", apiSchema, runtimeSchema)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(codec.SelectionMaskFields(mask), []byte{0x01, 0x08}) {
		t.Fatalf("mask = %x", mask)
	}
	if _, err := callFieldMask("missing", apiSchema, runtimeSchema); err == nil {
		t.Fatal("unknown field should fail")
	}
}

func TestLoadCallSchemaPreparesNestedTypes(t *testing.T) {
	tempRoot := filepath.Join("..", "..", ".tmp")
	if err := os.MkdirAll(tempRoot, 0755); err != nil {
		t.Fatal(err)
	}
	file, err := os.CreateTemp(tempRoot, "call-schema-*.json")
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	t.Cleanup(func() { file.Close(); os.Remove(path) })
	data := `{"models":{},"apis":{"read":{"id":1,"name":"read","returnType":"Parent"}},"types":{
		"Parent":{"name":"Parent","fields":[{"id":1,"name":"child","type":"Model","typeName":"Child","relation":true}]},
		"Child":{"name":"Child","fields":[{"id":3,"name":"value","type":"Int"}]}}}`
	if _, err := file.WriteString(data); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := loadCallSchema(path)
	if err != nil {
		t.Fatal(err)
	}
	mask, err := callFieldMask("child { value }", s.APIs["read"], s)
	if err != nil || len(mask) == 0 {
		t.Fatalf("decoded nested selection: %x / %v", mask, err)
	}
	child := s.LookupModel("Child")
	if child == nil || child != s.LookupModel("Child") || string(child.FieldByID(3).JSONPrefix) != `"value":` {
		t.Fatal("CLI did not prepare type metadata")
	}
	if _, err := loadCallSchema(path + ".missing"); err == nil {
		t.Fatal("missing file accepted")
	}
	if err := os.WriteFile(path, []byte(`{"apis":{"bad":{"id":0,"name":"bad"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCallSchema(path); err == nil {
		t.Fatal("invalid schema IDs accepted")
	}
	if err := os.WriteFile(path, []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCallSchema(path); err == nil {
		t.Fatal("malformed schema accepted")
	}
}

func TestParseBinaryCLIValueUsesSchemaType(t *testing.T) {
	value, err := parseBinaryCLIValue(`["AQI=",""]`, schema.Param{Type: schema.FieldBytes, IsList: true})
	if err != nil {
		t.Fatal(err)
	}
	items := value.([]any)
	if !bytes.Equal(items[0].([]byte), []byte{1, 2}) || len(items[1].([]byte)) != 0 {
		t.Fatalf("bytes = %#v", items)
	}
	jsonValue, err := parseBinaryCLIValue(`{"ok":true}`, schema.Param{Type: schema.FieldJSON})
	if err != nil {
		t.Fatal(err)
	}
	if string(jsonValue.(json.RawMessage)) != `{"ok":true}` {
		t.Fatalf("JSON = %#v", jsonValue)
	}
}

func TestSplitLines(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"a\nb\nc", 3},
		{"a\nb\n", 2},
		{"single", 1},
		{"", 0},
		{"\n\n", 2},
	}
	for _, tt := range tests {
		lines := splitLines([]byte(tt.input))
		if len(lines) != tt.want {
			t.Errorf("splitLines(%q) = %d lines, want %d", tt.input, len(lines), tt.want)
		}
	}
}

func TestSplitLinesContent(t *testing.T) {
	lines := splitLines([]byte("hello\nworld"))
	if string(lines[0]) != "hello" || string(lines[1]) != "world" {
		t.Errorf("got %q, %q", string(lines[0]), string(lines[1]))
	}
}

func TestCleanStaleGenFiles(t *testing.T) {
	dir := t.TempDir()
	// Stale generated file — its declaration was removed from the origin,
	// so this run's Files map no longer contains it.
	stale := filepath.Join(dir, "error.gen.go")
	if err := os.WriteFile(stale, []byte("package x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Freshly written generated file — must be kept.
	kept := filepath.Join(dir, "model.gen.go")
	if err := os.WriteFile(kept, []byte("package x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Handwritten file — never touched, even without .gen. suffix match.
	hand := filepath.Join(dir, "helper.go")
	if err := os.WriteFile(hand, []byte("package x\n"), 0644); err != nil {
		t.Fatal(err)
	}

	removed, err := cleanStaleGenFiles(dir, map[string][]byte{"model.gen.go": nil})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "error.gen.go" {
		t.Errorf("removed = %v, want [error.gen.go]", removed)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale error.gen.go should be deleted")
	}
	if _, err := os.Stat(kept); err != nil {
		t.Error("freshly written model.gen.go must be kept")
	}
	if _, err := os.Stat(hand); err != nil {
		t.Error("handwritten helper.go must never be touched")
	}
}

func TestEntryCleanupPreservesUnownedFiles(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := cleanStaleEntryFiles(nil, false); err != nil {
		t.Fatal(err)
	}
	const marked = "// Code generated by luxo. DO NOT EDIT.\npackage main\n"
	for name, content := range map[string]string{
		"old": marked, "active": marked, "app": marked, "gateway": marked,
		"manual": "package main\n", "other": "// Code generated by another tool. DO NOT EDIT.\npackage main\n",
	} {
		if err := os.MkdirAll(filepath.Join("lucis", name), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join("lucis", name, "main.gen.go"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("active", "lucis/linked"); err != nil {
		t.Fatal(err)
	}
	if err := cleanStaleEntryFiles(map[string][]byte{"active": nil}, true); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"active", "app", "gateway", "manual", "other", "linked"} {
		if _, err := os.Stat(filepath.Join("lucis", name, "main.gen.go")); err != nil {
			t.Fatalf("entry %s was removed: %v", name, err)
		}
	}
	if _, err := os.Stat("lucis/old/main.gen.go"); !os.IsNotExist(err) {
		t.Fatalf("stale entry remains: %v", err)
	}
	if err := cleanStaleEntryFiles(nil, false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"active", "app", "gateway"} {
		if _, err := os.Stat(filepath.Join("lucis", name, "main.gen.go")); !os.IsNotExist(err) {
			t.Fatalf("data-only project kept process %s: %v", name, err)
		}
	}
}

func TestGenKeepsSystemAndBusinessEntriesDistinct(t *testing.T) {
	writeGlobalGenerationProject(t, map[string]string{
		"app":     "api appValue(): Int { return 1 }",
		"modules": "api modulesValue(): Int { return 2 }",
	})
	if _, err := captureCLIResponse(t, func() error { return runAdd(nil, []string{"gateway"}) }); err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{
		"lucis/app/main.gen.go":             "// Source: main.gen.go",
		"lucis/gateway/main.gen.go":         "Luvia Gateway",
		"lucis/modules/app/main.gen.go":     "cluster entry for app module",
		"lucis/modules/gateway/main.gen.go": "cluster entry for gateway module",
		"lucis/modules/main.gen.go":         "cluster entry for modules module",
	}
	for range 2 {
		if _, err := captureCLIResponse(t, func() error { return runGen(nil, nil) }); err != nil {
			t.Fatal(err)
		}
		for path, marker := range paths {
			data, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(data), marker) {
				t.Errorf("entry %s missing marker %q: %v", path, marker, err)
			}
		}
		if t.Failed() {
			return
		}
	}
	if output, err := exec.Command("go", "build", "./lucis/...").CombinedOutput(); err != nil {
		t.Fatalf("generated entries do not build: %v\n%s", err, output)
	}
	writeCoverageFile(t, "origin/app.luxo", "type AppInfo { value: Int }\n")
	const handwritten = "lucis/modules/app/helper.go"
	writeCoverageFile(t, handwritten, "package main\n")
	previous := allowBreaking
	allowBreaking = true
	t.Cleanup(func() { allowBreaking = previous })
	if _, err := captureCLIResponse(t, func() error { return runGen(nil, nil) }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("lucis/modules/app/main.gen.go"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("obsolete business entry remains: %v", err)
	}
	for _, path := range []string{handwritten, "lucis/app/main.gen.go", "lucis/modules/main.gen.go"} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected file %s was removed: %v", path, err)
		}
	}
}

func TestEntryCleanupMatchesFilesystemCase(t *testing.T) {
	enterCacheGenerationScratch(t)
	const marked = "// Code generated by luxo. DO NOT EDIT.\npackage main\n"
	for _, path := range []string{"lucis/App/main.gen.go", "lucis/modules/Gateway/main.gen.go", "lucis/modules/other/main.gen.go"} {
		writeCoverageFile(t, path, marked)
	}
	_, err := os.Stat("lucis/app/main.gen.go")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	caseSensitive := errors.Is(err, os.ErrNotExist)
	if err := cleanStaleEntryFiles(map[string][]byte{"gateway": nil}, true); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"lucis/App/main.gen.go", "lucis/modules/Gateway/main.gen.go"} {
		_, err := os.Stat(path)
		if caseSensitive && !errors.Is(err, os.ErrNotExist) || !caseSensitive && err != nil {
			t.Errorf("unexpected cleanup of %s on filesystem with case sensitivity %v: %v", path, caseSensitive, err)
		}
	}
	if _, err := os.Stat("lucis/modules/other/main.gen.go"); err != nil {
		t.Fatalf("unmanaged nested entry was removed: %v", err)
	}
}

func TestMarkedEntryCleanupIgnoresNonRegularFiles(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.Mkdir("directory", 0755); err != nil {
		t.Fatal(err)
	}
	const marker = "// Code generated by luxo. DO NOT EDIT.\n"
	if err := os.WriteFile("target", []byte(marker), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", "linked"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"missing", "directory", "linked"} {
		if removed, err := removeMarkedEntry(path); removed || err != nil {
			t.Fatalf("cleanup %s = %v, %v", path, removed, err)
		}
	}
	if raw, err := os.ReadFile("target"); err != nil || string(raw) != marker {
		t.Fatalf("link target changed: %q, %v", raw, err)
	}
	if removed, err := removeMarkedEntry("target/child"); removed || err == nil {
		t.Fatalf("invalid parent error lost: %v, %v", removed, err)
	}
}

func TestEntryCleanupRejectsLinkedRoot(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.Mkdir("outside", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("outside", "lucis"); err != nil {
		t.Fatal(err)
	}
	if err := cleanStaleEntryFiles(nil, false); err == nil {
		t.Fatal("cleanup followed a linked root")
	}
}

func TestReadModulePath(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.WriteFile("go.mod", []byte("module github.com/test/myapp\n\ngo 1.27.1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	path, err := readModulePath()
	if err != nil || path != "github.com/test/myapp" {
		t.Fatalf("path=%q error=%v", path, err)
	}
}

func TestReadModulePathNotFound(t *testing.T) {
	enterCacheGenerationScratch(t)
	path, err := readModulePath()
	if path != "" || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing go.mod lost its read error: path=%q error=%v", path, err)
	}
}

func TestReadModulePathNoDirective(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.WriteFile("go.mod", []byte("go 1.27.1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if path, err := readModulePath(); path != "" || err == nil {
		t.Fatalf("missing directive: path=%q error=%v", path, err)
	}
}

func TestLoadDialect(t *testing.T) {
	d, err := loadDialect()
	if err != nil || d == nil {
		t.Error("default dialect should not be nil")
	}
}

func TestLoadDialectMySQL(t *testing.T) {
	t.Setenv("DATABASE_DRIVER", "mysql")
	d, err := loadDialect()
	if err == nil || d != nil {
		t.Fatalf("unsupported mysql dialect = %T, %v; want explicit error", d, err)
	}
}

func setupResolverLookupPath(t testing.TB, module, kind string) {
	t.Helper()
	base := filepath.Join("service", module)
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, "resolver")
	switch kind {
	case "absent":
		return
	case "not_directory":
		if err := os.WriteFile(path, []byte("keep existing project file\n"), 0600); err != nil {
			t.Fatal(err)
		}
		return
	case "loop":
		if err := os.Symlink("resolver", path); err != nil {
			t.Fatal(err)
		}
		return
	case "linked":
		target := "resolver-" + module
		if err := os.Symlink(filepath.Join("..", "..", target), path); err != nil {
			t.Fatal(err)
		}
		path = target
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	source := "package resolver\n\nimport luxo \"example.com/generated/service/" + module + "/luxo\"\n\n// Keep custom resolver.\nfunc Setup(*luxo.App) {}\n"
	if err := os.WriteFile(filepath.Join(path, "resolver.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestGenerationReportsResolverLookupFailure(t *testing.T) {
	for _, kind := range []string{"not_directory", "loop"} {
		t.Run(kind, func(t *testing.T) {
			writeGlobalGenerationProject(t, map[string]string{"project/main": `api value(): Int { return 42 }`})
			setupResolverLookupPath(t, "project", kind)
			before := addProjectSnapshot(t)
			path := filepath.Join("service", "project", "resolver")
			_, lookupErr := os.Stat(filepath.Join(path, "resolver.go"))
			if lookupErr == nil || os.IsNotExist(lookupErr) {
				t.Fatalf("fixture did not produce a non-ENOENT lookup failure: %v", lookupErr)
			}
			output, err := captureCLIResponse(t, func() error { return runGen(nil, nil) })
			var failure *os.PathError
			if !errors.As(lookupErr, &failure) || !errors.Is(err, failure.Err) || strings.Contains(output, "file(s) generated") {
				t.Errorf("resolver lookup failure was ignored: error=%v output=%q lookup=%v", err, output, lookupErr)
			}
			if after := addProjectSnapshot(t); after[path] != before[path] {
				t.Fatalf("existing resolver path changed: before=%q after=%q", before[path], after[path])
			}
			if err == nil {
				command := exec.Command("go", "build", "-mod=readonly", "-o", "broken-entry", "./lucis/app")
				output, buildErr := command.CombinedOutput()
				t.Logf("reported-success generated entry consumption: error=%v output=%s", buildErr, output)
			}
		})
	}
}

func TestGenerationPreservesUsableResolverPaths(t *testing.T) {
	for _, kind := range []string{"absent", "existing", "linked"} {
		t.Run(kind, func(t *testing.T) {
			writeGlobalGenerationProject(t, map[string]string{"project/main": `api value(): Int { return 42 }`})
			setupResolverLookupPath(t, "project", kind)
			before := addProjectSnapshot(t)
			output, err := captureCLIResponse(t, func() error { return runGen(nil, nil) })
			if err != nil || !strings.Contains(output, "file(s) generated") {
				t.Fatalf("valid resolver layout rejected: %v %q", err, output)
			}
			after := addProjectSnapshot(t)
			for path, content := range before {
				if strings.Contains(path, "resolver") && after[path] != content {
					t.Errorf("handwritten resolver changed: %s", path)
				}
			}
			command := exec.Command("go", "build", "-mod=readonly", "-o", "entry", "./lucis/app")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("generated entry failed to build: %v\n%s", err, output)
			}
		})
	}
}

func TestValidateWireCompatibilityRequiresExplicitOverride(t *testing.T) {
	lf := lockfile.New()
	base := []*ast.File{{Models: []*ast.ModelDecl{{
		Name:   "User",
		Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}},
	}}}}
	if err := lf.Update(base); err != nil {
		t.Fatal(err)
	}
	removed := []*ast.File{{}}

	if err := validateWireCompatibility(lf, removed, false); err == nil || !strings.Contains(err.Error(), "--allow-breaking") {
		t.Fatalf("breaking change must require explicit override: %v", err)
	}
	if err := validateWireCompatibility(lf, removed, true); err != nil {
		t.Fatalf("explicit override rejected: %v", err)
	}
}

func TestLockUpdateFailurePreservesPersistedFile(t *testing.T) {
	enterCacheGenerationScratch(t)
	lf := lockfile.New()
	lf.Models["User"] = &lockfile.ModelLock{NextID: 65536, Fields: map[string]int{}}
	if err := lf.Save("luxo.lock"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile("luxo.lock")
	if err != nil {
		t.Fatal(err)
	}
	input := []*ast.File{{Models: []*ast.ModelDecl{{Name: "User", Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}}}}}
	got, err := updateLockFileAndReturn(input)
	if err == nil || got != nil || !strings.Contains(err.Error(), "update luxo.lock") {
		t.Fatalf("exhausted lock update succeeded: %v", err)
	}
	after, err := os.ReadFile("luxo.lock")
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed update replaced file: %v", err)
	}
}

func TestLockReadFailurePreservesPersistedFile(t *testing.T) {
	for _, raw := range []string{
		`{"version":1,"version":2}`,
		`{"apis":{"read":{"id":1,"id":2}}}`,
		`{"apis":{"read":{"id":1,"contrcat":true}}}`,
		`{"models":{"\ud800":{"next_id":1,"fields":{}}}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			enterCacheGenerationScratch(t)
			if err := os.WriteFile("luxo.lock", []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := updateLockFileAndReturn([]*ast.File{{}})
			if err == nil || got != nil {
				t.Fatalf("invalid metadata reached update: %#v %v", got, err)
			}
			after, err := os.ReadFile("luxo.lock")
			if err != nil || string(after) != raw {
				t.Fatalf("failed read replaced file: %v", err)
			}
		})
	}
}

func TestLoadGenerationEnvironment(t *testing.T) {
	tempRoot := filepath.Join("..", "..", ".tmp")
	if err := os.MkdirAll(tempRoot, 0755); err != nil {
		t.Fatal(err)
	}
	file, err := os.CreateTemp(tempRoot, "gen-env-*.env")
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	t.Cleanup(func() { os.Remove(path) })
	if _, err := file.WriteString("LUXO_TEST_GENERATION_ENV=loaded\n"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	previous, existed := os.LookupEnv("LUXO_TEST_GENERATION_ENV")
	os.Unsetenv("LUXO_TEST_GENERATION_ENV")
	t.Cleanup(func() {
		if existed {
			os.Setenv("LUXO_TEST_GENERATION_ENV", previous)
		} else {
			os.Unsetenv("LUXO_TEST_GENERATION_ENV")
		}
	})

	if err := loadGenerationEnvironment(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("LUXO_TEST_GENERATION_ENV"); got != "loaded" {
		t.Fatalf("loaded environment = %q, want loaded", got)
	}
	if err := loadGenerationEnvironment(filepath.Join(tempRoot, "missing.env")); err != nil {
		t.Fatalf("missing optional environment file = %v", err)
	}
}

func TestNextMigrationSeq(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	// No migrations dir
	seq, err := nextMigrationSeq()
	if err != nil {
		t.Fatal(err)
	}
	if seq != 1 {
		t.Errorf("got %d, want 1", seq)
	}

	// Create some migration files
	os.Mkdir("migrations", 0755)
	os.WriteFile(filepath.Join("migrations", "001_init.sql"), []byte("--"), 0644)
	os.WriteFile(filepath.Join("migrations", "002_add_user.sql"), []byte("--"), 0644)

	seq, err = nextMigrationSeq()
	if err != nil {
		t.Fatal(err)
	}
	if seq != 3 {
		t.Errorf("got %d, want 3", seq)
	}
}

func TestPrintDiffOps(t *testing.T) {
	// Just ensure it doesn't panic for all op types
	ops := []codegen.DiffOp{
		{Kind: codegen.CreateTable, Table: "users"},
		{Kind: codegen.DropTable, Table: "old_table"},
		{Kind: codegen.AddColumn, Table: "users", Column: "email"},
		{Kind: codegen.DropColumn, Table: "users", Column: "old_col"},
		{Kind: codegen.RenameColumn, Table: "users", Column: "new_name", OldName: "old_name"},
		{Kind: codegen.AlterColumn, Table: "users", Column: "age"},
		{Kind: codegen.AddIndex, Index: &lux.IndexInfo{Name: "idx_email"}},
		{Kind: codegen.DropIndex, Index: &lux.IndexInfo{Name: "idx_old"}},
		{Kind: codegen.AddColumn, Table: "users", Column: "x", Warning: "breaking change"},
	}
	// Redirect stdout to discard
	old := os.Stdout
	os.Stdout, _ = os.Open(os.DevNull)
	defer func() { os.Stdout = old }()

	printDiffOps(ops)
}

func prepareCoverageGeneration(t *testing.T) {
	t.Helper()
	writeGlobalGenerationProject(t, map[string]string{
		"user/main": "model SoftItem @soft @noTime { id: Int @id } api userValue(): Int { return 1 }",
		"post/main": "api postValue(): Int { return 2 }",
	})
}

func writeCoverageFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func blockCoveragePath(t *testing.T, path string, directory bool) {
	t.Helper()
	if directory {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		return
	}
	writeCoverageFile(t, path, "preserve blocked output\n")
}

func TestGenerationEntryFailurePropagation(t *testing.T) {
	for _, sample := range []struct {
		name, path, context string
		directory           bool
	}{
		{"environment", ".env", "load generation environment", true},
		{"lock-load", "luxo.lock", "load luxo.lock", true},
		{"module-directory", "service/user", "create service/user/luxo", false},
		{"module-output", "service/user/luxo/model.gen.go", "write service/user/luxo/model.gen.go", true},
		{"embedded-directory", "lucis/app", "create lucis/app", false},
		{"embedded-output", "lucis/app/main.gen.go", "write lucis/app/main.gen.go", true},
		{"module-entry-directory", "lucis/user", "create lucis/user", false},
		{"module-entry-output", "lucis/user/main.gen.go", "write lucis/user/main.gen.go", true},
		{"gateway-directory", "lucis/gateway", "create lucis/gateway", false},
		{"gateway-output", "lucis/gateway/main.gen.go", "write lucis/gateway/main.gen.go", true},
		{"schema-output", "luxo.schema.json", "export schema", true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			prepareCoverageGeneration(t)
			blockCoveragePath(t, sample.path, sample.directory)
			output, err := captureCLIResponse(t, func() error { return runGen(nil, nil) })
			if err == nil || !strings.Contains(err.Error(), sample.context) || strings.Contains(output, "file(s) generated") {
				t.Fatalf("failed stage reported success: error=%v output=%q", err, output)
			}
			info, statErr := os.Stat(sample.path)
			if statErr != nil || info.IsDir() != sample.directory {
				t.Fatalf("blocked output was replaced: %v %v", info, statErr)
			}
			if !sample.directory {
				assertDeployArtifactContains(t, sample.path, "preserve blocked output")
			}
		})
	}
}

func TestGenerationInvalidInputsAndWarnings(t *testing.T) {
	for _, sample := range []struct{ name, file, content, context string }{
		{"lexer", "origin/user/main.luxo", `"unterminated`, "lexer error"},
		{"cache-policy", "luxo.cache.json", `{`, "cache"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			prepareCoverageGeneration(t)
			writeCoverageFile(t, sample.file, sample.content)
			output, err := captureCLIResponse(t, func() error { return runGen(nil, nil) })
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), sample.context) || strings.Contains(output, "file(s) generated") {
				t.Fatalf("invalid input was ignored: error=%v output=%q", err, output)
			}
		})
	}
	t.Run("backend", func(t *testing.T) {
		prepareCoverageGeneration(t)
		t.Setenv("LUXO_CACHE_BACKEND", "unsupported")
		if err := runGen(nil, nil); err == nil || !strings.Contains(err.Error(), "unsupported cache backend") {
			t.Fatal(err)
		}
	})
	t.Run("driver", func(t *testing.T) {
		prepareCoverageGeneration(t)
		t.Setenv("DATABASE_DRIVER", "mysql")
		if err := runGen(nil, nil); err == nil || !strings.Contains(err.Error(), "mysql") {
			t.Fatal(err)
		}
	})
	t.Run("warning", func(t *testing.T) {
		enterCacheGenerationScratch(t)
		file := parseGenerationSource(t, "api value(): Int { val unused = 1 return 42 }", "origin/main.luxo")
		result, err := analyzeFiles([]*ast.File{file})
		if err != nil || len(result.Warnings) == 0 {
			t.Fatalf("warning lost or rejected: result=%+v error=%v", result, err)
		}
	})
}

func TestGenerationEmptyOriginAndMissingInputs(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := runAdd(nil, []string{"user"}); err == nil || !strings.Contains(err.Error(), "not a Luxo project") {
		t.Fatal(err)
	}
	if err := os.Mkdir("origin", 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := findSchemaFiles(); err == nil || !strings.Contains(err.Error(), "no .luxo files") {
		t.Fatal(err)
	}
	if _, err := parseFile("origin/missing.luxo"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err := runGen(nil, nil); err == nil || !strings.Contains(err.Error(), "no .luxo files") {
		t.Fatal(err)
	}
}

func TestMigrationFieldIDMapsUseStoredColumns(t *testing.T) {
	enterCacheGenerationScratch(t)
	writeCoverageFile(t, "luxo.lock", "invalid")
	if got := buildFieldIDMaps(); got != nil {
		t.Fatalf("invalid lock yielded maps: %v", got)
	}
	lf := lockfile.New()
	lf.Models["Person"] = &lockfile.ModelLock{NextID: 9, Fields: map[string]int{"givenName": 3, "newName": 7}}
	lf.Models["Absent"] = &lockfile.ModelLock{NextID: 2, Fields: map[string]int{"id": 1}}
	if err := lf.Save("luxo.lock"); err != nil {
		t.Fatal(err)
	}
	state := &codegen.SchemaState{Models: map[string]*codegen.ModelState{
		"Person": {Columns: map[string]*codegen.ColumnState{"given_name": {Type: "text"}, "removed": {Type: "text"}}},
	}}
	if err := os.Mkdir("migrations", 0700); err != nil {
		t.Fatal(err)
	}
	if err := codegen.SaveState("migrations/.state.json", state); err != nil {
		t.Fatal(err)
	}
	got := buildFieldIDMaps()
	if len(got["Person"].OldByID) != 1 || got["Person"].OldByID[3] != "given_name" || len(got["Person"].NewByID) != 2 || got["Person"].NewByID[7] != "new_name" || len(got["Absent"].OldByID) != 0 {
		t.Fatalf("field identity does not match persisted columns: %+v", got)
	}
}

func restrictCoveragePath(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(absolute, info.Mode().Perm()); err != nil {
			t.Error(err)
		}
	})
	if err := os.Chmod(absolute, mode); err != nil {
		t.Fatal(err)
	}
}

func TestGenerationPermissionFailureStages(t *testing.T) {
	for _, kind := range []string{"lock-save", "resolver-directory", "resolver-output", "stale-model", "stale-entry"} {
		t.Run(kind, func(t *testing.T) {
			prepareCoverageGeneration(t)
			switch kind {
			case "lock-save":
				restrictCoveragePath(t, ".", 0555)
			case "resolver-directory":
				if err := os.MkdirAll("service/user/luxo", 0700); err != nil {
					t.Fatal(err)
				}
				restrictCoveragePath(t, "service/user", 0555)
			case "resolver-output":
				if err := os.MkdirAll("service/user/resolver", 0700); err != nil {
					t.Fatal(err)
				}
				restrictCoveragePath(t, "service/user/resolver", 0555)
			case "stale-model":
				if err := runGen(nil, nil); err != nil {
					t.Fatal(err)
				}
				writeCoverageFile(t, "service/user/luxo/stale.gen.go", "// Code generated by luxo. DO NOT EDIT.\npackage luxo\n")
				restrictCoveragePath(t, "service/user/luxo", 0555)
			case "stale-entry":
				writeCoverageFile(t, "lucis/obsolete/main.gen.go", "// Code generated by luxo. DO NOT EDIT.\npackage main\n")
				restrictCoveragePath(t, "lucis/obsolete", 0555)
			}
			output, err := captureCLIResponse(t, func() error { return runGen(nil, nil) })
			if !errors.Is(err, os.ErrPermission) || strings.Contains(output, "file(s) generated") {
				t.Fatalf("generation swallowed permission failure: error=%v output=%q", err, output)
			}
		})
	}
}

func TestGeneratedCleanupAccessFailures(t *testing.T) {
	t.Run("missing-model-directory", func(t *testing.T) {
		enterCacheGenerationScratch(t)
		if _, err := cleanStaleGenFiles("missing", nil); !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	})
	t.Run("unreadable-entry-root", func(t *testing.T) {
		enterCacheGenerationScratch(t)
		if err := os.Mkdir("lucis", 0700); err != nil {
			t.Fatal(err)
		}
		restrictCoveragePath(t, "lucis", 0000)
		if err := cleanStaleEntryFiles(nil, false); !errors.Is(err, os.ErrPermission) {
			t.Fatal(err)
		}
	})
	t.Run("unsearchable-project", func(t *testing.T) {
		enterCacheGenerationScratch(t)
		restrictCoveragePath(t, ".", 0000)
		if err := cleanStaleEntryFiles(nil, false); !errors.Is(err, os.ErrPermission) {
			t.Fatal(err)
		}
		if _, err := writeModuleScaffold("example.com/project", "user"); !errors.Is(err, os.ErrPermission) {
			t.Fatal(err)
		}
	})
}

func TestSharedEnumPermissionFailures(t *testing.T) {
	for _, kind := range []string{"parent-search", "target-search", "target-read", "directory-create"} {
		t.Run(kind, func(t *testing.T) {
			enterCacheGenerationScratch(t)
			if err := os.MkdirAll("internal/luxotypes", 0700); err != nil {
				t.Fatal(err)
			}
			generator, err := codegen.NewGenerator(codegen.GeneratorConfig{Events: &codegen.EventContext{
				SharedEnumPackage: "example.com/project/internal/luxotypes",
				Enums:             map[string]*ast.EnumDecl{"Role": {Name: "Role", Values: []string{"USER"}}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "parent-search":
				restrictCoveragePath(t, "internal", 0000)
			case "target-search":
				restrictCoveragePath(t, "internal/luxotypes", 0000)
			case "target-read":
				writeCoverageFile(t, "internal/luxotypes/enum.gen.go", "// Code generated by luxo. DO NOT EDIT.\npackage luxotypes\n")
				restrictCoveragePath(t, "internal/luxotypes/enum.gen.go", 0000)
			case "directory-create":
				if err := os.Remove("internal/luxotypes"); err != nil {
					t.Fatal(err)
				}
				restrictCoveragePath(t, "internal", 0555)
			}
			if err := generateSharedEnums(generator); !errors.Is(err, os.ErrPermission) {
				t.Fatalf("shared enum stage ignored permission failure: %v", err)
			}
		})
	}
}

func TestGenerationHelpersPropagateRejectedAnalysis(t *testing.T) {
	enterCacheGenerationScratch(t)
	generator, err := codegen.NewGenerator(codegen.GeneratorConfig{})
	if err != nil {
		t.Fatal(err)
	}
	invalid := &semantic.Result{Files: []*ast.File{{Name: "origin/user.luxo"}}, Errors: []semantic.Error{{Message: "invalid source"}}}
	if _, err := generateModules(invalid, generator); err == nil || !strings.Contains(err.Error(), "generate module user") {
		t.Fatal(err)
	}
	if err := generateEntry(invalid, "example.com/project"); err == nil || !strings.Contains(err.Error(), "generate embedded entry") {
		t.Fatal(err)
	}
	if err := exportSchemaJSON(invalid, generator); err == nil {
		t.Fatal("rejected analysis exported schema")
	}
	if err := generateEntry(&semantic.Result{}, "example.com/project"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("lucis"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("empty analysis created an entry", err)
	}
}

func TestMigrationPureFilesystemBoundaries(t *testing.T) {
	t.Run("missing-state-parent", func(t *testing.T) {
		enterCacheGenerationScratch(t)
		if _, _, err := resolveMigrationStatePath("missing/state.json"); !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	})
	t.Run("unsearchable-state-parent", func(t *testing.T) {
		enterCacheGenerationScratch(t)
		if err := os.Mkdir("blocked", 0700); err != nil {
			t.Fatal(err)
		}
		restrictCoveragePath(t, "blocked", 0000)
		if _, _, err := resolveMigrationStatePath("blocked/state.json"); !errors.Is(err, os.ErrPermission) {
			t.Fatal(err)
		}
		if err := checkMigrationOutputAvailable("blocked/output.sql"); !errors.Is(err, os.ErrPermission) {
			t.Fatal(err)
		}
	})
	for _, invalid := range []bool{false, true} {
		t.Run(strconv.FormatBool(invalid), func(t *testing.T) {
			writeGlobalGenerationProject(t, map[string]string{"main": "model Probe @noTime { id: Int @id }"})
			if err := os.Mkdir("migrations", 0700); err != nil {
				t.Fatal(err)
			}
			result, err := parseAndAnalyze()
			if err != nil {
				t.Fatal(err)
			}
			dialect, err := loadDialect()
			if err != nil {
				t.Fatal(err)
			}
			state, err := codegen.ComputeState(result, codegen.CollectEnumsFromResult(result), dialect)
			if err != nil {
				t.Fatal(err)
			}
			if err := codegen.SaveState("migrations/.state.json", state); err != nil {
				t.Fatal(err)
			}
			if invalid {
				writeCoverageFile(t, "migrations/.state.json", "invalid state")
			}
			output, err := captureCLIResponse(t, func() error { return runMigrateDiff(nil, nil) })
			if invalid && (err == nil || !strings.Contains(err.Error(), "load state")) || !invalid && (err != nil || !strings.Contains(output, "No changes detected")) {
				t.Fatalf("migration state boundary not preserved: error=%v output=%q", err, output)
			}
		})
	}
}

func TestGenerationRejectsBreakingLockAndUnownedEnums(t *testing.T) {
	t.Run("breaking-lock", func(t *testing.T) {
		prepareCoverageGeneration(t)
		if err := runGen(nil, nil); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile("luxo.lock")
		if err != nil {
			t.Fatal(err)
		}
		writeCoverageFile(t, "origin/user/main.luxo", "api userValue(): Int { return 1 }")
		output, err := captureCLIResponse(t, func() error { return runGen(nil, nil) })
		if err == nil || !strings.Contains(err.Error(), "breaking binary contract") || strings.Contains(output, "file(s) generated") {
			t.Fatalf("breaking lock update was accepted: %v %q", err, output)
		}
		after, err := os.ReadFile("luxo.lock")
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("rejected lock was changed", err)
		}
	})
	t.Run("unowned-enum", func(t *testing.T) {
		prepareCoverageGeneration(t)
		writeCoverageFile(t, "origin/user/main.luxo", "enum Role { USER } api userValue(): Int { return 1 }")
		path := "internal/luxotypes/enum.gen.go"
		writeCoverageFile(t, path, "package luxotypes\n// preserve handwritten enum\n")
		output, err := captureCLIResponse(t, func() error { return runGen(nil, nil) })
		if err == nil || !strings.Contains(err.Error(), "refusing to overwrite unowned shared enum") || strings.Contains(output, "file(s) generated") {
			t.Fatalf("enum generation failure was ignored: %v %q", err, output)
		}
		assertDeployArtifactContains(t, path, "preserve handwritten enum")
	})
}

func TestInitExistingReadOnlyRootCannotCreateOrigin(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.Mkdir("locked", 0700); err != nil {
		t.Fatal(err)
	}
	restrictCoveragePath(t, "locked", 0555)
	output, err := captureCLIResponse(t, func() error { return runInit(nil, []string{"locked"}) })
	if !errors.Is(err, os.ErrPermission) || !strings.Contains(err.Error(), "create origin directory") || output != "" {
		t.Fatalf("inaccessible origin reported success: %v %q", err, output)
	}
	if _, err := os.Stat("locked/origin"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed init left origin", err)
	}
}

func TestSharedEnumsRequireExplicitPackageBeforeWriting(t *testing.T) {
	enterCacheGenerationScratch(t)
	generator, err := codegen.NewGenerator(codegen.GeneratorConfig{Events: &codegen.EventContext{
		Enums: map[string]*ast.EnumDecl{"Role": {Name: "Role", Values: []string{"USER"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := generateSharedEnums(generator); err == nil || !strings.Contains(err.Error(), "explicit shared package path") {
		t.Fatal("incomplete shared enum configuration was accepted", err)
	}
	if _, err := os.Stat("internal"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rejected shared enum configuration created output", err)
	}
}

func TestInitPathResolutionFromUnreadableDeepWorkingDirectory(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Darwin path limit and directory permission precondition")
	}
	enterCacheGenerationScratch(t)
	base, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("owned remaining-nine scratch: %s", base)
	home, err := os.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer home.Close()
	defer func() {
		if err := home.Chdir(); err != nil {
			t.Error(err)
		}
	}()
	for index := 0; index < 16; index++ {
		component := strings.Repeat("d", 72) + strconv.Itoa(index)
		if err := os.Mkdir(component, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(component); err != nil {
			t.Fatal(err)
		}
	}
	parent, err := os.Open("..")
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	defer func() {
		if err := parent.Chmod(0700); err != nil {
			t.Error(err)
		}
	}()
	if err := parent.Chmod(0100); err != nil {
		t.Fatal(err)
	}
	output, err := captureCLIResponse(t, func() error { return runInit(nil, []string{"new-project"}) })
	t.Logf("deep unreadable cwd result: error=%v output=%q", err, output)
	if !errors.Is(err, os.ErrPermission) || !strings.Contains(err.Error(), "resolve path:") || output != "" {
		t.Fatalf("path resolution failure was not preserved: error=%v output=%q", err, output)
	}
}

func clearOwnedInitACL(t *testing.T, scratch, directory string) {
	t.Helper()
	temporary := filepath.Dir(scratch)
	info, err := os.Lstat(temporary)
	if err != nil || filepath.Base(temporary) != ".tmp" || !info.IsDir() {
		t.Fatal("refuse ACL cleanup outside project scratch", temporary, err)
	}
	info, err = os.Lstat(scratch)
	if err != nil || !info.IsDir() {
		t.Fatal("refuse linked or missing owned scratch", scratch, err)
	}
	resolvedTemporary, err := filepath.EvalSymlinks(temporary)
	if err != nil {
		t.Fatal(err)
	}
	resolvedScratch, err := filepath.EvalSymlinks(scratch)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(resolvedScratch) != resolvedTemporary || filepath.Base(resolvedScratch) != filepath.Base(scratch) || !strings.HasPrefix(filepath.Base(scratch), "cache-module-generation-") {
		t.Fatal("owned scratch moved outside its project temporary directory", scratch)
	}
	resolvedDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(resolvedScratch, resolvedDirectory)
	if err != nil || relative != "acl-parent" {
		t.Fatal("refuse ACL cleanup outside owned scratch", directory, err)
	}
	if err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("refuse linked ACL fixture: " + path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("/bin/chmod", "-RN", directory).CombinedOutput()
	if err != nil {
		t.Fatalf("clear owned ACL: %v %s", err, output)
	}
	t.Logf("owned remaining-nine ACL cleared: %s", directory)
}

func TestInitPreservesAllPermissionFailuresWithInheritedACL(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS inherited directory ACL fault fixture")
	}
	if os.Geteuid() == 0 {
		t.Skip("permission failure requires an unprivileged user")
	}
	enterCacheGenerationScratch(t)
	scratch, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(scratch, "acl-parent")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	acl, err := exec.Command("/bin/ls", "-lde", parent).CombinedOutput()
	if err != nil {
		t.Fatalf("read original owned ACL: %v %s", err, acl)
	}
	t.Logf("owned remaining-nine ACL before: %s", acl)
	if strings.Count(strings.TrimSpace(string(acl)), "\n") != 0 {
		t.Skip("new fixture inherited an existing ACL; do not replace it")
	}
	t.Cleanup(func() { clearOwnedInitACL(t, scratch, parent) })
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	permission := "user:" + account.Username + " deny add_file,delete_child,file_inherit,directory_inherit"
	output, err := exec.Command("/bin/chmod", "+a", permission, parent).CombinedOutput()
	if err != nil {
		t.Skipf("owned filesystem cannot apply inherited ACL: %v %s", err, output)
	}
	acl, err = exec.Command("/bin/ls", "-lde", parent).CombinedOutput()
	if err != nil {
		t.Fatalf("read applied ACL: %v %s", err, acl)
	}
	t.Logf("owned remaining-nine ACL applied: %s", acl)
	outputText, err := captureCLIResponse(t, func() error { return runInit(nil, []string{filepath.Join("acl-parent", "example")}) })
	t.Logf("owned remaining-nine joined init error: %v", err)
	if !errors.Is(err, os.ErrPermission) || outputText != "" || !strings.Contains(err.Error(), "create .env.example") || !strings.Contains(err.Error(), "remove incomplete origin directory") || !strings.Contains(err.Error(), "remove incomplete project directory") {
		t.Fatalf("init lost write/cleanup errors or reported success: error=%v output=%q", err, outputText)
	}
	if info, err := os.Stat(filepath.Join(parent, "example", "origin")); err != nil || !info.IsDir() {
		t.Fatal("ACL fixture did not reach both owned cleanup attempts", err)
	}
}

func modulePathDeclarationFixtures() []struct{ name, source string } {
	return []struct{ name, source string }{
		{"plain", "module example.com/project\n\ngo 1.27.1\n"},
		{"quoted", "module \"example.com/project\"\n\ngo 1.27.1\n"},
		{"whitespace-crlf", " \tmodule\t\"example.com/project\"\t\r\n\r\ngo 1.27.1\r\n"},
		{"inline-comment", "// module example.com/decoy\nmodule example.com/project // retained comment\n\ngo 1.27.1\n"},
		{"escaped", "module \"example.com/\\x70roject\"\n\ngo 1.27.1\n"},
		{"require-block-token", "require (\nmodule v1.2.3\n)\nmodule \"example.com/project\"\n\ngo 1.27.1\n"},
		{"replace-block-token", "replace (\nmodule => example.com/dependency v1.2.3\n)\nmodule example.com/project\n\ngo 1.27.1\n"},
		{"plain-eof", "module example.com/project"},
		{"quoted-comment-eof", "module \"example.com/project\" // final comment"},
	}
}

func TestReadModulePathGoDeclarations(t *testing.T) {
	for _, sample := range modulePathDeclarationFixtures() {
		t.Run(sample.name, func(t *testing.T) {
			enterCacheGenerationScratch(t)
			if err := os.WriteFile("go.mod", []byte(sample.source), 0600); err != nil {
				t.Fatal(err)
			}
			path, err := readModulePath()
			if err != nil || path != "example.com/project" {
				t.Fatalf("Go-valid declaration read as %q: %v", path, err)
			}
		})
	}
}

func TestReadModulePathRejectsInvalidDeclarations(t *testing.T) {
	for _, sample := range []struct{ name, source string }{
		{"unterminated", "module \"example.com/project\n"},
		{"escape", "module \"example.com/\\qproject\"\n"},
		{"extra-bare", "module example.com/project extra\n"},
		{"extra-quoted", "module \"example.com/project\" extra\n"},
		{"adjacent-quoted", "module \"example.com/project\"\"extra\"\n"},
		{"interior-quote", "module example.com/\"project\"\n"},
		{"missing-path", "module // missing path\n"},
		{"require-only", "require (\nmodule v1.2.3\n)\n"},
		{"replace-only", "replace (\nmodule => example.com/dependency v1.2.3\n)\n"},
		{"replace-line-only", "replace module => ./dependency\n"},
		{"empty-path", "module \"\"\n"},
		{"module-block", "module (\nexample.com/project\n)\n"},
		{"block-comment-before", "/* not a go.mod line comment */\nmodule example.com/project\n"},
		{"block-comment-after", "module example.com/project /* not a line comment */\n"},
		{"unmatched-close", ")\nmodule example.com/project\n"},
		{"single-quote", "module 'example.com/project'\n"},
		{"raw-quote", "module `example.com/project`\n"},
		{"missing-eof", "module"},
		{"earlier-bad-string", "require \"broken\nmodule example.com/project\n"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			enterCacheGenerationScratch(t)
			if err := os.WriteFile("go.mod", []byte(sample.source), 0600); err != nil {
				t.Fatal(err)
			}
			if path, err := readModulePath(); path != "" || err == nil {
				t.Fatalf("invalid declaration accepted as %q: %v", path, err)
			}
		})
	}
}

func TestReadModulePathPreservesReadFailure(t *testing.T) {
	enterCacheGenerationScratch(t)
	if err := os.Mkdir("go.mod", 0700); err != nil {
		t.Fatal(err)
	}
	path, err := readModulePath()
	var failure *os.PathError
	if path != "" || !errors.As(err, &failure) || failure.Path != "go.mod" {
		t.Fatalf("read error was replaced: path=%q error=%v", path, err)
	}
}

func TestGenQuotedModulePathMatchesPlain(t *testing.T) {
	var plain map[string]string
	for _, sample := range []struct{ name, directive string }{
		{"plain", "module example.com/project"},
		{"quoted", "module \"example.com/project\""},
	} {
		t.Run(sample.name, func(t *testing.T) {
			writeGlobalGenerationProject(t, map[string]string{"main": "api probe(): Int { return 7 }"})
			if err := os.WriteFile("go.mod", []byte(sample.directive+"\n\ngo 1.27.1\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if output, err := captureCLIResponse(t, func() error { return runGen(nil, nil) }); err != nil {
				t.Fatalf("public gen failed: %v\n%s", err, output)
			}
			files := make(map[string]string)
			for path, contents := range addProjectSnapshot(t) {
				if strings.HasSuffix(path, ".go") {
					files[path] = contents
				}
			}
			if len(files) != 8 {
				t.Fatalf("expected full embedded/module/gateway output, got %d Go files", len(files))
			}
			if sample.name == "plain" {
				plain = files
				return
			}
			for path, want := range plain {
				if files[path] != want {
					t.Errorf("quoted module changed generated Go file %s", path)
				}
			}
		})
	}
}

func writeInvalidModulePathFixture(t testing.TB, kind string) {
	t.Helper()
	if kind == "read-error" {
		if err := os.Remove("go.mod"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir("go.mod", 0700); err != nil {
			t.Fatal(err)
		}
		return
	}
	source := "module \"example.com/project\n"
	if kind == "extra-token" {
		source = "module example.com/project extra\n"
	}
	if err := os.WriteFile("go.mod", []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestGenInvalidModulePathPreservesProject(t *testing.T) {
	for _, kind := range []string{"bad-quote", "extra-token", "read-error"} {
		for _, state := range []string{"new", "existing"} {
			t.Run(kind+"/"+state, func(t *testing.T) {
				writeGlobalGenerationProject(t, map[string]string{"main": "api probe(): Int { return 7 }"})
				if state == "existing" {
					if err := lockfile.New().Save("luxo.lock"); err != nil {
						t.Fatal(err)
					}
					for _, path := range []string{"service/main/luxo/app.gen.go", "service/main/resolver/resolver.go", "lucis/app/main.gen.go", "luxo.schema.json"} {
						if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(path, []byte("keep existing "+path), 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
				writeInvalidModulePathFixture(t, kind)
				before := addProjectSnapshot(t)
				output, err := captureCLIResponse(t, func() error { return runGen(nil, nil) })
				if err == nil || !strings.Contains(err.Error(), "go.mod") || output != "" {
					t.Errorf("invalid go.mod reported gen success or wrote output: %v %q", err, output)
				}
				assertAddProjectSnapshot(t, before)
			})
		}
	}
}

func TestGenerationHelpersRejectInvalidModulePathBeforeWriting(t *testing.T) {
	for _, operation := range []string{"generator", "modules"} {
		t.Run(operation, func(t *testing.T) {
			writeGlobalGenerationProject(t, map[string]string{"main": "api probe(): Int { return 7 }"})
			files, err := parseAllFiles([]string{"origin/main.luxo"})
			if err != nil {
				t.Fatal(err)
			}
			input, err := analyzeFiles(files)
			if err != nil {
				t.Fatal(err)
			}
			locked, err := updateLockFileAndReturn(files)
			if err != nil {
				t.Fatal(err)
			}
			generator, err := buildProjectGenerator(locked, files)
			if err != nil {
				t.Fatal(err)
			}
			writeInvalidModulePathFixture(t, "bad-quote")
			before := addProjectSnapshot(t)
			var constructed *codegen.GeneratorContext
			output, err := captureCLIResponse(t, func() error {
				if operation == "generator" {
					constructed, err = buildProjectGenerator(locked, files)
					return err
				}
				_, err := generateModules(input, generator)
				return err
			})
			if err == nil || !strings.Contains(err.Error(), "go.mod") || output != "" || constructed != nil {
				t.Errorf("helper ignored invalid go.mod: constructed=%v error=%v output=%q", constructed != nil, err, output)
			}
			assertAddProjectSnapshot(t, before)
		})
	}
}

func TestGenWithoutModuleFilePreservesServiceOnlyMode(t *testing.T) {
	writeGlobalGenerationProject(t, map[string]string{"main": "api probe(): Int { return 7 }"})
	if err := os.Remove("go.mod"); err != nil {
		t.Fatal(err)
	}
	if output, err := captureCLIResponse(t, func() error { return runGen(nil, nil) }); err != nil {
		t.Fatal(output, err)
	}
	for _, path := range []string{"luxo.lock", "service/main/luxo/handler.gen.go", "luxo.schema.json"} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(path, err)
		}
	}
	if _, err := os.Stat("lucis"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing go.mod unexpectedly generated entrypoints", err)
	}
}

func TestProjectGeneratorQuotedModuleWithTypeAndEventIDs(t *testing.T) {
	writeGlobalGenerationProject(t, map[string]string{"main": `type Payload { value: Int }
event Changed(value: Int)
model Item @noTime { id: Int @id }
api probe(): Int { return 7 }`})
	if err := os.WriteFile("go.mod", []byte("module \"example.com/project\"\n\ngo 1.27.1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := parseAllFiles([]string{"origin/main.luxo"})
	if err != nil {
		t.Fatal(err)
	}
	input, err := analyzeFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	locked, err := updateLockFileAndReturn(files)
	if err != nil {
		t.Fatal(err)
	}
	generator, err := buildProjectGenerator(locked, files)
	if err != nil {
		t.Fatal(err)
	}
	generated, err := generator.Generate(input, "luxo", nil)
	if err != nil || generated == nil {
		t.Fatalf("quoted module type/event generation: %v", err)
	}
	if len(generated.Files["event.gen.go"]) == 0 || !strings.Contains(string(generated.Files["model.gen.go"]), "type Payload struct") {
		t.Fatal("type/event declarations were lost")
	}
}

func TestGenQuotedModulePathRegenerationRemovesStaleFiles(t *testing.T) {
	writeGlobalGenerationProject(t, map[string]string{"main": "api probe(): Int { return 7 }"})
	if err := os.WriteFile("go.mod", []byte("module \"example.com/project\"\n\ngo 1.27.1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureCLIResponse(t, func() error { return runGen(nil, nil) }); err != nil {
		t.Fatal(err)
	}
	stale := "service/main/luxo/removed.gen.go"
	if err := os.WriteFile(stale, []byte("// Code generated by luxo. DO NOT EDIT.\npackage luxo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := captureCLIResponse(t, func() error { return runGen(nil, nil) })
	if err != nil || !strings.Contains(output, stale) || !strings.Contains(output, "file(s) generated") {
		t.Fatalf("quoted module regeneration did not finish cleanup: %v %q", err, output)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stale generated source survived regeneration", err)
	}
}

func assertModulePathMatchesScanner(t testing.TB, data []byte) {
	t.Helper()
	got, gotErr := parseModulePath(data)
	want, wantErr := scanModulePath(data)
	gotText, wantText := "", ""
	if gotErr != nil {
		gotText = gotErr.Error()
	}
	if wantErr != nil {
		wantText = wantErr.Error()
	}
	if got != want || (gotErr == nil) != (wantErr == nil) || gotText != wantText {
		t.Fatalf("module source %q: fast=(%q,%q), scanner=(%q,%q)", data, got, gotText, want, wantText)
	}
}

func modulePathFastSubsetSeeds() [][]byte {
	seeds := [][]byte{
		[]byte("module example.com/project"),
		[]byte("module example.com/project\n"),
		[]byte("module EXAMPLE.com/Project_2-v1.3\ngo 1.27.1\n"),
		[]byte("module example.com/project\n\x00"),
		[]byte("module example.com/project\n\xff"),
		[]byte("module example.com/project\n\xef\xbb\xbf"),
		[]byte("module example.com/project\ng\x00"),
		[]byte("module example.com/project\n\n\xff"),
		[]byte("module example.com/project\n\r\x00"),
		[]byte("module example.com/project\n\xe4\xb8\xad"),
		[]byte("module example.com/project// comment\n"),
		[]byte("module example.com/project/* comment */\n"),
		[]byte("module example.com/project \n"),
		[]byte("module example.com/project\r\n"),
		[]byte("module example.com/\xff\n"),
		[]byte("module example.com/project\n\xc2"),
		[]byte("module \n"), []byte("module "), []byte(""),
	}
	for _, size := range []int{1015, 1016, 1017, 8192} {
		seeds = append(seeds, []byte("module "+strings.Repeat("a", size)+"\n\x00"))
	}
	for _, sample := range modulePathDeclarationFixtures() {
		seeds = append(seeds, []byte(sample.source))
	}
	return seeds
}

func TestModulePathFastSubsetMatchesScanner(t *testing.T) {
	for _, seed := range modulePathFastSubsetSeeds() {
		assertModulePathMatchesScanner(t, seed)
	}
	for _, tail := range []struct {
		bytes     string
		wantError bool
	}{
		{"", false}, {"go 1.27.1\n", false}, {"\x00", true}, {"\xff", true},
		{"\xef\xbb\xbf", false}, {"g\x00", false}, {"\n\xff", false}, {"\r\x00", false},
	} {
		data := []byte("module example.com/project\n" + tail.bytes)
		got, err := parseModulePath(data)
		if (err != nil) != tail.wantError || !tail.wantError && got != "example.com/project" {
			t.Fatalf("newline lookahead %q: path=%q error=%v", tail.bytes, got, err)
		}
	}
}

func TestModulePathFastSubsetAllocations(t *testing.T) {
	for _, source := range []string{"module example.com/project", "module example.com/project\n\ngo 1.27.1\n"} {
		data := []byte(source)
		allocations := testing.AllocsPerRun(100, func() { modulePathReadValue, modulePathReadError = parseModulePath(data) })
		if modulePathReadValue != "example.com/project" || modulePathReadError != nil {
			t.Fatal(modulePathReadValue, modulePathReadError)
		}
		if allocations > 1 {
			t.Errorf("plain module reader allocated %.0f times; want at most its returned string", allocations)
		}
	}
}

func FuzzModulePathFastSubsetMatchesScanner(f *testing.F) {
	for _, seed := range modulePathFastSubsetSeeds() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 16384 {
			return
		}
		assertModulePathMatchesScanner(t, data)
	})
}

func TestGenEmbeddedFormatterRejectsDecodedQuoteModulePath(t *testing.T) {
	prepareCoverageGeneration(t)
	writeCoverageFile(t, "go.mod", "module "+strconv.Quote("\"")+"\n\ngo 1.27.1\n")
	path, err := readModulePath()
	if err != nil || path != "\"" {
		t.Fatalf("quoted directive was not decoded: path=%q error=%v", path, err)
	}
	output, err := captureCLIResponse(t, func() error { return runGen(nil, nil) })
	if err == nil || !strings.Contains(err.Error(), "generate embedded entry") || strings.Contains(output, "file(s) generated") {
		t.Fatalf("embedded formatter failure was hidden: error=%v output=%q", err, output)
	}
	if _, err := os.Stat("lucis/app/main.gen.go"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("formatter failure published an embedded entry", err)
	}
	// Generation before the entry stage retains the existing non-atomic boundary.
	for _, name := range []string{"luxo.lock", "service/user/luxo/app.gen.go", "service/post/luxo/app.gen.go"} {
		if _, err := os.Stat(name); err != nil {
			t.Fatal(name, err)
		}
	}
}

func TestGenGatewayFormatterRejectsUnclosedImportComment(t *testing.T) {
	enterCacheGenerationScratch(t)
	for _, name := range []string{"LUXO_EVENT_BACKEND", "LUXO_QUEUE_BACKEND", "LUXO_CACHE_BACKEND"} {
		t.Setenv(name, "")
	}
	writeCoverageFile(t, "go.mod", "module "+strconv.Quote(`x*/" /*`)+"\n\ngo 1.27.1\n")
	writeCoverageFile(t, "origin/demo.luxo", "api probe(): Int { return 7 }\n")
	output, err := captureCLIResponse(t, func() error { return runGen(nil, nil) })
	if err == nil || !strings.Contains(err.Error(), "comment not terminated") || strings.Contains(output, "file(s) generated") {
		t.Fatalf("gateway formatter failure was hidden: error=%v output=%q", err, output)
	}
	for _, name := range []string{"lucis/app/main.gen.go", "lucis/demo/main.gen.go"} {
		if _, err := os.Stat(name); err != nil {
			t.Fatal("earlier entry did not complete", name, err)
		}
	}
	if _, err := os.Stat("lucis/gateway/main.gen.go"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("formatter failure published a gateway entry", err)
	}
}

func TestGenModuleFormatterRejectsUnclosedImportComment(t *testing.T) {
	enterCacheGenerationScratch(t)
	for _, name := range []string{"LUXO_EVENT_BACKEND", "LUXO_QUEUE_BACKEND", "LUXO_CACHE_BACKEND"} {
		t.Setenv(name, "")
	}
	writeCoverageFile(t, "go.mod", "module "+strconv.Quote("*/\"; ` /*")+"\n\ngo 1.27.1\n")
	for index, name := range []string{"alpha", "beta", "gamma"} {
		writeCoverageFile(t, "origin/"+name+".luxo", "api "+name+"(): Int { return "+strconv.Itoa(index+1)+" }\n")
	}
	output, err := captureCLIResponse(t, func() error { return runGen(nil, nil) })
	if err == nil || !strings.Contains(err.Error(), "format module entry ") || !strings.Contains(err.Error(), "comment not terminated") || strings.Contains(output, "file(s) generated") {
		t.Fatalf("module formatter failure was hidden: error=%v output=%q", err, output)
	}
	if _, err := os.Stat("lucis/app/main.gen.go"); err != nil {
		t.Fatal("earlier embedded entry did not complete", err)
	}
	for _, name := range []string{"alpha", "beta", "gamma", "gateway"} {
		if _, err := os.Stat(filepath.Join("lucis", name, "main.gen.go")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("formatter failure published a later entry", name, err)
		}
	}
}
