package codegen

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/str"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestCapitalizedASCIISnakeCaseLenMatchesRenderedName(t *testing.T) {
	for _, name := range []string{
		"", "a", "user", "userID", "uSR", "uSr", "uRLParser", "HTTP", "HTMLParser", "api1HTTPClient",
		"_user_v2", strings.Repeat("a", 59) + "Ba", strings.Repeat("a", 62),
	} {
		length, ok := capitalizedASCIISnakeCaseLen(name)
		want := len(str.ToSnakeCase(str.Capitalize(name)))
		if !ok || length != want {
			t.Errorf("%q: got %d/%v, want %d/true", name, length, ok, want)
		}
	}
	for _, name := range []string{"\x00before", "a\x00b", "éstart", "aé", "a\xff", "\xff"} {
		if _, ok := capitalizedASCIISnakeCaseLen(name); ok {
			t.Errorf("%q should use the general conversion", name)
		}
	}
}

func TestValidateModuleScaffoldModelNameDoesNotAllocate(t *testing.T) {
	for _, name := range []string{
		"user", "user_" + strings.Repeat("a", 24), strings.Repeat("a", 62),
		"userID", "uSR", "userHTTPServer", "_user", "User",
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateModuleScaffoldModelName(name); err != nil {
				t.Fatal(err)
			}
			if allocations := testing.AllocsPerRun(50, func() {
				if err := ValidateModuleScaffoldModelName(name); err != nil {
					t.Fatal(err)
				}
			}); allocations != 0 {
				t.Fatalf("validating %q allocated %.0f times, want zero", name, allocations)
			}
		})
	}
}

func TestValidateModuleScaffoldModelNameMatchesGeneratedNames(t *testing.T) {
	names := []string{
		"", "_", "user", "person2", "User", "_user", "uSR", "uSr", "uRLParser",
		"HTMLParser", "apiHTTPClient", "apiHTTP", "api1HTTPClient", "userID", "User_Id",
		"résumé", "éHTTPParser", "mañana", "δHTTP", "aK", "a\x00b", "\xff",
		"resolver", "EventBus", "EventHandlers", "SetLoaders", "loaders",
	}
	for _, base := range []string{"App", "New", "NewFromDB", "RegisterSchema", "DB", "Queue", "Close", "Tx"} {
		names = append(names, base, str.LowerFirst(base), strings.ToLower(base))
	}
	for builtin := range semantic.BuiltinTypes() {
		names = append(names, builtin, str.LowerFirst(builtin), strings.ToLower(builtin))
	}
	for _, length := range []int{28, 32, 33, 61, 62, 63, 64} {
		names = append(names, strings.Repeat("a", length), strings.Repeat("a", length-2)+"Ba",
			"uS"+strings.Repeat("A", length-2), strings.Repeat("aB", length/2))
	}

	for _, name := range names {
		want := referenceModuleScaffoldNameError(name)
		got := fmt.Sprint(ValidateModuleScaffoldModelName(name))
		if got != want {
			t.Errorf("%q: got %q, want %q", name, got, want)
		}
	}
}

func referenceModuleScaffoldNameError(moduleName string) string {
	modelName := str.Capitalize(moduleName)
	if semantic.IsBuiltinTypeName(modelName) {
		return fmt.Sprintf("generated model name %q is a built-in type / 生成的模型名 %q 是内置类型", modelName, modelName)
	}
	switch modelName {
	case generatedAppTypeName, generatedAppNewName, generatedAppNewFromDBName, generatedSchemaRegisterName:
		return fmt.Sprintf("generated model name %q conflicts with a Go declaration / 生成的模型名 %q 与 Go 声明冲突", modelName, modelName)
	}
	g := defaultGenerator()
	if g.conflictsWithAppMember(modelName, &semantic.Result{}, nil) {
		return fmt.Sprintf("generated model name %q conflicts with an App member / 生成的模型名 %q 与 App 成员冲突", modelName, modelName)
	}
	if err := g.sqlDialect.ValidateIdentifier(modelTableName(modelName)); err != nil {
		return fmt.Sprintf("generated model name %q has an invalid database table name / 生成的模型名 %q 对应的数据库表名无效: %v", modelName, modelName, err)
	}
	return "<nil>"
}

func TestInitProject(t *testing.T) {
	sr := InitProject("github.com/example/myapp")

	expected := []string{".gitignore", ".env.example", "go.mod"}
	for _, name := range expected {
		if _, ok := sr.Files[name]; !ok {
			t.Errorf("missing %s", name)
		}
	}
}

func TestInitProjectGoMod(t *testing.T) {
	sr := InitProject("github.com/example/myapp")
	src := string(sr.Files["go.mod"])

	checks := []string{
		"module github.com/example/myapp",
		"go 1.27.1",
		"github.com/light-speak/luxo v1.0.0-beta.1",
		"github.com/light-speak/luxo/pkg/lux/pg v1.0.0-beta.1",
	}
	for _, check := range checks {
		if !strings.Contains(src, check) {
			t.Errorf("missing %q in go.mod:\n%s", check, src)
		}
	}
}

func TestScaffoldPinsOnlySelectedDefaultAdapter(t *testing.T) {
	version, err := os.ReadFile("../../VERSION")
	if err != nil {
		t.Fatal(err)
	}
	manifest := string(InitProject("example.com/app").Files["go.mod"])
	if !strings.Contains(manifest, "github.com/light-speak/luxo v"+strings.TrimSpace(string(version))+"\n") {
		t.Fatal("scaffold version drift")
	}
	for _, unwanted := range []string{"/nats", "/cache/redis", "/storage/s3", "replace "} {
		if strings.Contains(manifest, unwanted) {
			t.Fatalf("unselected dependency %q", unwanted)
		}
	}
}

func TestInitProjectGitignore(t *testing.T) {
	sr := InitProject("github.com/example/myapp")
	src := string(sr.Files[".gitignore"])

	checks := []string{".env", "bin/", ".DS_Store", "*.cov"}
	for _, check := range checks {
		if !strings.Contains(src, check) {
			t.Errorf("missing %q in .gitignore:\n%s", check, src)
		}
	}
}

func TestInitProjectEnvExample(t *testing.T) {
	sr := InitProject("github.com/example/myapp")
	src := string(sr.Files[".env.example"])

	checks := []string{
		"DATABASE_DRIVER=pg",
		"DATABASE_USER=postgres",
		"DATABASE_PASSWORD=postgres",
		"# JWT_SECRET=",
		"DEPLOY_MODE=embedded",
		"LUXO_EVENT_BACKEND=none",
		"LUXO_QUEUE_BACKEND=none",
		"LUXO_CACHE_BACKEND=none",
		"CORS_ORIGIN=*",
		"LOG_LEVEL=info",
		"LOG_REQUESTS_FULL=true",
		"sample 1 in 100 successful requests",
		"GRACEFUL_TIMEOUT=30s",
	}
	for _, check := range checks {
		if !strings.Contains(src, check) {
			t.Errorf("missing %q in .env.example:\n%s", check, src)
		}
	}
	for _, line := range strings.Split(src, "\n") {
		key, _, _ := strings.Cut(line, "=")
		if strings.TrimSpace(key) == "JWT_SECRET" {
			t.Error(".env.example must leave JWT_SECRET unset")
		}
	}

	unsupported := []string{
		"APP_DEBUG=",
		"APP_TIMEZONE=",
		"GEN_MODE=",
		"LOG_FORMAT=",
		"TRANSPORT_MODE=",
		"HEALTH_PATH=",
		"REQUEST_ID_HEADER=",
	}
	for _, setting := range unsupported {
		if strings.Contains(src, setting) {
			t.Errorf("unsupported setting %q must not be advertised:\n%s", setting, src)
		}
	}
}

func TestAddModule(t *testing.T) {
	sr := AddModule("github.com/example/myapp", "user")

	expected := []string{
		"origin/user.luxo",
		"service/user/resolver/resolver.go",
	}
	for _, name := range expected {
		if _, ok := sr.Files[name]; !ok {
			t.Errorf("missing %s", name)
		}
	}
}

func TestAddModuleSchema(t *testing.T) {
	sr := AddModule("github.com/example/myapp", "user")
	src := string(sr.Files["origin/user.luxo"])

	checks := []string{
		"User module",
		"model User",
		"id:        UUID      @auto",
		"createdAt: DateTime",
		"updatedAt: DateTime",
	}
	for _, check := range checks {
		if !strings.Contains(src, check) {
			t.Errorf("missing %q in user.luxo:\n%s", check, src)
		}
	}
}

func TestAddModuleResolver(t *testing.T) {
	sr := AddModule("github.com/example/myapp", "user")
	src := string(sr.Files["service/user/resolver/resolver.go"])

	checks := []string{
		"package resolver",
		"type Resolver struct",
		"func Setup(app *luxo.App)",
		"user module",
	}
	for _, check := range checks {
		if !strings.Contains(src, check) {
			t.Errorf("missing %q in resolver.go:\n%s", check, src)
		}
	}
}

// TestAddModuleNoLucisMain guards against re-introducing a hand-written
// lucis/<module>/main.go scaffold: `luxo gen` emits main.gen.go in that same
// directory, so a second main.go would collide (duplicate package main / func main).
func TestAddModuleNoLucisMain(t *testing.T) {
	sr := AddModule("github.com/example/myapp", "user")

	if _, ok := sr.Files["lucis/user/main.go"]; ok {
		t.Error("AddModule must not generate lucis/user/main.go — it collides with luxo gen's main.gen.go")
	}
	for path := range sr.Files {
		if strings.HasPrefix(path, "lucis/") {
			t.Errorf("AddModule must not write under lucis/ (generated by luxo gen), got %q", path)
		}
	}
}

func TestAddModuleDifferentNames(t *testing.T) {
	tests := []struct {
		module    string
		modelName string
	}{
		{"user", "User"},
		{"order", "Order"},
		{"payment", "Payment"},
	}
	for _, tt := range tests {
		t.Run(tt.module, func(t *testing.T) {
			sr := AddModule("github.com/example/myapp", tt.module)
			schema := string(sr.Files["origin/"+tt.module+".luxo"])
			if !strings.Contains(schema, "model "+tt.modelName) {
				t.Errorf("missing model %s in schema:\n%s", tt.modelName, schema)
			}
			resolverSrc := string(sr.Files["service/"+tt.module+"/resolver/resolver.go"])
			if !strings.Contains(resolverSrc, "package resolver") {
				t.Errorf("missing resolver package for module %s:\n%s", tt.module, resolverSrc)
			}
		})
	}
}
