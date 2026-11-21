package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/codegen"
	"github.com/light-speak/luxo/pkg/lockfile"
	"github.com/light-speak/luxo/pkg/semantic"
)

var schemaDiscoveryCount int
var schemaDiscoveryErrorText string

var addModuleErrorText string

var initProjectErrorText string

func BenchmarkCLIInitProject(b *testing.B) {
	for _, sample := range []string{"new", "empty", "nonempty", "unreadable", "readonly", "file"} {
		b.Run(sample, func(b *testing.B) {
			enterCacheGenerationScratch(b)
			quietModuleGeneration(b)
			baseline := os.Getenv("LUXO_INIT_COST_BASELINE") == "1"
			wantError := sample != "new" && sample != "empty" && !(baseline && sample == "unreadable")
			setupInitProjectBenchmark(b, sample)
			b.ReportAllocs()
			for b.Loop() {
				err := runInit(nil, []string{"example"})
				if (err != nil) != wantError {
					b.Fatal("unexpected init result", sample, err)
				}
				initProjectErrorText = ""
				if err != nil {
					initProjectErrorText = err.Error()
				}
				cleanupInitProjectBenchmark(b, sample)
			}
		})
	}
}

func setupInitProjectBenchmark(b *testing.B, sample string) {
	b.Helper()
	if sample == "new" {
		return
	}
	if sample == "file" {
		if err := os.WriteFile("example", []byte("keep existing file\n"), 0600); err != nil {
			b.Fatal(err)
		}
		return
	}
	if err := os.Mkdir("example", 0700); err != nil {
		b.Fatal(err)
	}
	if sample == "nonempty" || sample == "unreadable" {
		for _, name := range []string{"go.mod", ".gitignore", ".env.example"} {
			if err := os.WriteFile(filepath.Join("example", name), []byte("keep existing content\n"), 0600); err != nil {
				b.Fatal(err)
			}
		}
	}
	mode := os.FileMode(0700)
	if sample == "unreadable" {
		mode = 0333
	} else if sample == "readonly" {
		mode = 0555
	}
	if err := os.Chmod("example", mode); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := os.Chmod("example", 0700); err != nil {
			b.Error(err)
		}
	})
}

func cleanupInitProjectBenchmark(b *testing.B, sample string) {
	b.Helper()
	if sample != "new" && sample != "empty" && sample != "unreadable" {
		return
	}
	root, err := os.Getwd()
	if err != nil || !strings.Contains(filepath.ToSlash(root), "/.tmp/cache-module-generation-") {
		b.Fatal("unexpected benchmark scratch root", root, err)
	}
	path := filepath.Join(root, "example")
	if filepath.Dir(path) != root {
		b.Fatal("refusing benchmark cleanup outside owned scratch", path)
	}
	if sample == "unreadable" {
		if err := os.Remove(filepath.Join(path, "origin")); err != nil && !os.IsNotExist(err) {
			b.Fatal(err)
		}
		return
	}
	if err := os.RemoveAll(path); err != nil {
		b.Fatal(err)
	}
	if sample == "empty" {
		if err := os.Mkdir(path, 0700); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCLIAddModule(b *testing.B) {
	for _, sample := range []string{"new", "long_name", "schema_exists", "resolver_exists", "directory_exists", "parent_path"} {
		b.Run(sample, func(b *testing.B) {
			enterCacheGenerationScratch(b)
			quietModuleGeneration(b)
			name := setupAddModuleBenchmark(b, sample)
			baseline := os.Getenv("LUXO_ADD_COST_BASELINE") == "1"
			wantError := sample == "schema_exists" || (!baseline && sample != "new" && sample != "long_name")
			b.ReportAllocs()
			for b.Loop() {
				err := runAdd(nil, []string{name})
				if (err != nil) != wantError {
					b.Fatal("unexpected add result", sample, err)
				}
				addModuleErrorText = ""
				if err != nil {
					addModuleErrorText = err.Error()
				}
				cleanupAddModuleBenchmark(b, sample, name)
			}
		})
	}
}

func setupAddModuleBenchmark(b *testing.B, sample string) string {
	b.Helper()
	for _, name := range []string{"origin", "service"} {
		if err := os.Mkdir(name, 0700); err != nil {
			b.Fatal(err)
		}
	}
	files := map[string]string{"go.mod": "module example.com/add\n\ngo 1.27.1\n"}
	switch sample {
	case "schema_exists":
		files["origin/user.luxo"] = "keep existing schema\n"
	case "resolver_exists":
		files["service/user/resolver/resolver.go"] = "keep existing resolver\n"
	case "directory_exists":
		files["origin/user/model.luxo"] = "keep existing model\n"
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			b.Fatal(err)
		}
	}
	if sample == "parent_path" {
		return "../victim"
	}
	if sample == "long_name" {
		return "user_" + strings.Repeat("a", 24)
	}
	return "user"
}

func cleanupAddModuleBenchmark(b *testing.B, sample, name string) {
	b.Helper()
	if sample == "schema_exists" {
		return
	}
	paths := []string{filepath.Join("origin", name+".luxo")}
	if sample != "resolver_exists" {
		paths = append(paths, filepath.Join("service", name))
	}
	root, err := os.Getwd()
	if err != nil || !strings.Contains(filepath.ToSlash(root), "/.tmp/cache-module-generation-") {
		b.Fatal("unexpected benchmark scratch root", root, err)
	}
	for _, path := range paths {
		absolute, err := filepath.Abs(path)
		if err != nil || !strings.HasPrefix(absolute, root+string(filepath.Separator)) {
			b.Fatal("refusing benchmark cleanup outside owned scratch", path, absolute, err)
		}
		if err := os.RemoveAll(absolute); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCLISchemaDiscovery(b *testing.B) {
	for _, sample := range []string{"flat", "modules", "large", "unreadable"} {
		b.Run(sample, func(b *testing.B) {
			enterCacheGenerationScratch(b)
			setupSchemaDiscoveryBenchmark(b, sample)
			b.ReportAllocs()
			for b.Loop() {
				files, err := findSchemaFiles()
				schemaDiscoveryCount = len(files)
				schemaDiscoveryErrorText = ""
				if err != nil {
					schemaDiscoveryErrorText = err.Error()
				}
			}
		})
	}
}

func setupSchemaDiscoveryBenchmark(b *testing.B, sample string) {
	b.Helper()
	if err := os.Mkdir("origin", 0700); err != nil {
		b.Fatal(err)
	}
	if sample == "flat" || sample == "unreadable" {
		if err := os.WriteFile("origin/a.luxo", []byte("api value(): Int { return 7 }"), 0600); err != nil {
			b.Fatal(err)
		}
	} else {
		modules, files := 2, 5
		if sample == "large" {
			modules, files = 20, 10
		}
		for module := range modules {
			path := filepath.Join("origin", fmt.Sprintf("module%02d", module))
			if err := os.Mkdir(path, 0700); err != nil {
				b.Fatal(err)
			}
			for file := range files {
				if err := os.WriteFile(filepath.Join(path, fmt.Sprintf("part%02d.luxo", file)), []byte("api value(): Int { return 7 }"), 0600); err != nil {
					b.Fatal(err)
				}
			}
		}
	}
	if sample == "unreadable" {
		if err := os.Mkdir("origin/blocked", 0000); err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() {
			if err := os.Chmod("origin/blocked", 0700); err != nil {
				b.Error(err)
			}
		})
		if _, err := os.ReadDir("origin/blocked"); err == nil {
			b.Skip("filesystem credentials permit reading a mode-000 directory")
		}
	}
}

// Measure regeneration in an existing directory; preparation and Go builds are excluded.
func BenchmarkModuleGenericContext(b *testing.B) {
	for _, sample := range []struct{ name, source string }{
		{"scalar-control", moduleCostSource("return value ?: 42")},
		{"channel-control", moduleCostSource("val channel: Channel<Int?> = Channel(1) channel <- value val item = <-channel return item ?: 42")},
		{"generic-when", moduleGenericWhenSource},
	} {
		b.Run(sample.name, func(b *testing.B) {
			enterCacheGenerationScratch(b)
			configureGeneratedModuleWorkspace(b)
			quietModuleGeneration(b)
			result, generator := prepareModuleBenchmark(b, sample.source)
			wantError := sample.name == "generic-when" && os.Getenv("LUXO_CLI_GENERIC_BASELINE") == "1"
			count, err := generateModules(result, generator)
			checkModuleBenchmark(b, count, err, wantError)
			if directory := os.Getenv("LUXO_CLI_MODULE_OUTPUT"); directory != "" {
				if err := os.CopyFS(filepath.Join(directory, sample.name), os.DirFS("service")); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				count, err := generateModules(result, generator)
				checkModuleBenchmark(b, count, err, wantError)
			}
		})
	}
}

func moduleCostSource(body string) string {
	return "model RecordNAME @noTime { id: Int @id }\nfn chooseNAME(flag: Boolean, value: Int?): Int { " + body +
		" }\napi probeNAME(flag: Boolean,value: Int?): Int { return chooseNAME(flag,value) }"
}

func prepareModuleBenchmark(b *testing.B, source string) (*semantic.Result, *codegen.GeneratorContext) {
	b.Helper()
	var files []*ast.File
	for _, name := range []string{"Alpha", "Beta"} {
		files = append(files, parseGenerationSource(b, strings.ReplaceAll(source, "NAME", name), "origin/"+strings.ToLower(name)+"/model.luxo"))
	}
	result, err := analyzeFiles(files)
	if err != nil {
		b.Fatal(err)
	}
	locked := lockfile.New()
	if err := locked.Update(result.Files); err != nil {
		b.Fatal(err)
	}
	generator, err := buildProjectGenerator(locked, result.Files)
	if err != nil {
		b.Fatal(err)
	}
	return result, generator
}

func checkModuleBenchmark(b *testing.B, count int, err error, wantError bool) {
	b.Helper()
	if wantError {
		if err == nil || !strings.Contains(err.Error(), "Channel element metadata is missing") {
			b.Fatalf("expected the prior metadata failure, got count=%d error=%v", count, err)
		}
	} else if err != nil || count == 0 {
		b.Fatalf("module generation failed: count=%d error=%v", count, err)
	}
}

func quietModuleGeneration(b *testing.B) {
	b.Helper()
	output, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		b.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = output
	b.Cleanup(func() {
		os.Stdout = previous
		if err := output.Close(); err != nil {
			b.Error(err)
		}
	})
}

var resolverLookupErrorText string

func BenchmarkCLIResolverLookupGeneration(b *testing.B) {
	for _, sample := range []struct {
		name, kind string
		modules    int
	}{
		{"existing_one", "existing", 1}, {"existing_16", "existing", 16},
		{"missing", "absent", 1}, {"linked", "linked", 1},
		{"not_directory", "not_directory", 1}, {"loop", "loop", 1},
	} {
		b.Run(sample.name, func(b *testing.B) {
			enterCacheGenerationScratch(b)
			configureGeneratedModuleWorkspace(b)
			quietModuleGeneration(b)
			result, generator := prepareResolverLookupBenchmark(b, sample.kind, sample.modules)
			_, lookupErr := os.Stat("service/module0/resolver/resolver.go")
			wantError := (sample.kind == "not_directory" || sample.kind == "loop") && os.Getenv("LUXO_CLI_RESOLVER_BASELINE") != "1"
			check := func() {
				count, err := generateModules(result, generator)
				if (err != nil) != wantError || err == nil && count == 0 || err != nil && !errors.Is(err, lookupErr.(*os.PathError).Err) {
					b.Fatalf("unexpected generation result: %d %v", count, err)
				}
				resolverLookupErrorText = ""
				if err != nil {
					resolverLookupErrorText = err.Error()
				}
			}
			check()
			b.ReportAllocs()
			for b.Loop() {
				if sample.kind == "absent" {
					if err := os.Remove("service/module0/resolver/resolver.go"); err != nil {
						b.Fatal(err)
					}
				}
				check()
			}
		})
	}
}

func prepareResolverLookupBenchmark(b *testing.B, kind string, modules int) (*semantic.Result, *codegen.GeneratorContext) {
	b.Helper()
	b.Setenv("DATABASE_DRIVER", "pg")
	files := make([]*ast.File, modules)
	for index := range modules {
		module := fmt.Sprintf("module%d", index)
		source := fmt.Sprintf("api value%d(): Int { return %d }", index, index)
		files[index] = parseGenerationSource(b, source, "origin/"+module+"/main.luxo")
		setupResolverLookupPath(b, module, kind)
	}
	result, err := analyzeFiles(files)
	if err != nil {
		b.Fatal(err)
	}
	locked := lockfile.New()
	if err := locked.Update(result.Files); err != nil {
		b.Fatal(err)
	}
	generator, err := buildProjectGenerator(locked, result.Files)
	if err != nil {
		b.Fatal(err)
	}
	return result, generator
}

var modulePathReadValue string
var modulePathReadError error

func BenchmarkCLIReadModulePath(b *testing.B) {
	for _, sample := range []struct{ name, source string }{
		{"plain", "module example.com/project\n\ngo 1.27.1\n"},
		{"quoted", "module \"example.com/project\"\n\ngo 1.27.1\n"},
		{"comment", "module example.com/project // comment\n"},
		{"invalid-quote", "module \"example.com/project\n"},
		{"no-directive", "go 1.27.1\n"},
		{"missing-file", ""},
		{"read-error", ""},
	} {
		b.Run(sample.name, func(b *testing.B) {
			enterCacheGenerationScratch(b)
			if sample.name == "read-error" {
				if err := os.Mkdir("go.mod", 0700); err != nil {
					b.Fatal(err)
				}
			} else if sample.name != "missing-file" {
				if err := os.WriteFile("go.mod", []byte(sample.source), 0600); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				modulePathReadValue, modulePathReadError = readModulePath()
			}
		})
	}
}
