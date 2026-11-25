package codegen

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
	"gopkg.in/yaml.v3"
)

func TestComposeInheritsJWTEnvironmentForApplicationServices(t *testing.T) {
	files := GenerateDeployFiles(makeHelmResult("user", "post"), "myapp")
	var compose struct {
		Services map[string]struct {
			Environment map[string]*string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(files.DockerCompose, &compose); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gateway", "user", "post"} {
		for _, key := range []string{"JWT_SECRET", "JWT_EXPIRES", "JWT_REFRESH", "JWT_REFRESH_EXPIRES"} {
			value, present := compose.Services[name].Environment[key]
			if !present || value != nil {
				t.Errorf("service %s must inherit %s without a default value", name, key)
			}
		}
	}
	for _, name := range []string{"postgres", "redis"} {
		if _, present := compose.Services[name].Environment["JWT_SECRET"]; present {
			t.Errorf("infrastructure service %s received the JWT secret", name)
		}
	}
}

func TestGeneratedToolchainMatchesCoreModule(t *testing.T) {
	data, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	var version string
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "go" {
			version = fields[1]
			break
		}
	}
	if version == "" {
		t.Fatal("core module must declare its Go version")
	}
	project := InitProject("example.com/toolchain")
	if !strings.Contains(string(project.Files["go.mod"]), "\ngo "+version+"\n") {
		t.Fatalf("new projects must use the core Go version %s", version)
	}
	want := "FROM golang:" + version + "-alpine AS builder\n"
	if !strings.Contains(string(generateDockerfile([]moduleInfo{{name: "user"}})), want) {
		t.Fatalf("generated Dockerfile must use the core Go version %s", version)
	}
}

func TestGenerateDockerfileStagesOptionalMigrations(t *testing.T) {
	dockerfile := string(generateDockerfile([]moduleInfo{{name: "user"}, {name: "post"}}))
	mkdir := strings.Index(dockerfile, "mkdir -p /app/migrations")
	projectCopy := strings.Index(dockerfile, "COPY . .\n")
	migrationCopy := "COPY --from=builder /app/migrations/ /app/migrations/\n"
	if mkdir < 0 || projectCopy < mkdir || strings.Count(dockerfile, migrationCopy) != 2 {
		t.Fatalf("builder must stage an empty migrations directory before copying project files:\n%s", dockerfile)
	}
}

func TestGenerateDeployFiles_Basic(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{
			{
				Name: "origin/user.luxo",
				Models: []*ast.ModelDecl{{
					Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
					Name:       "User",
					Directives: []*ast.Directive{{Name: "crud"}},
				}},
			},
			{
				Name: "origin/post.luxo",
				Models: []*ast.ModelDecl{{
					Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
					Name:       "Post",
					Directives: []*ast.Directive{{Name: "crud"}},
				}},
			},
		},
	}

	files := GenerateDeployFiles(result, "myapp")
	if files == nil {
		t.Fatal("should generate deploy files")
	}

	// Dockerfile checks
	df := string(files.Dockerfile)
	checks := []string{
		"FROM golang:1.27.1-alpine AS builder",
		"FROM builder AS user",
		"FROM builder AS post",
		"FROM builder AS gateway",
		"FROM alpine:3.20 AS user-run",
		"FROM alpine:3.20 AS post-run",
		"CGO_ENABLED=0 go build",
		"./lucis/user/",
		"./lucis/post/",
		"./lucis/gateway/",
		"ENTRYPOINT",
		"migrations",
	}
	for _, check := range checks {
		if !strings.Contains(df, check) {
			t.Errorf("Dockerfile missing %q", check)
		}
	}

	// docker-compose checks
	dc := string(files.DockerCompose)
	dcChecks := []string{
		"services:",
		"postgres:",
		"image: postgres:17-alpine",
		"redis:",
		"user:",
		"post:",
		"gateway:",
		"DATABASE_DRIVER: pg",
		"POSTGRES_USER: ${DATABASE_USER:-postgres}",
		"POSTGRES_PASSWORD: ${DATABASE_PASSWORD:-postgres}",
		"DATABASE_PREFIX: myapp",
		"APP_PORT:",
		"LUXO_PORT:",
		"GATEWAY_PORT",
		"CORS_ORIGIN",
		"volumes:",
		"pgdata:",
		"depends_on:",
		"service_healthy",
		"service_started",
	}
	for _, check := range dcChecks {
		if !strings.Contains(dc, check) {
			t.Errorf("docker-compose missing %q", check)
		}
	}
	if strings.Contains(dc, ":-luxo}") {
		t.Fatal("generated compose must not use the removed luxo database credentials")
	}
}

func TestGenerateDeployFiles_Empty(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{}}
	files := GenerateDeployFiles(result, "myapp")
	if files != nil {
		t.Error("should return nil for empty project")
	}
}

func TestGenerateDeployFiles_WithEvents(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{
			{
				Name: "origin/order.luxo",
				Models: []*ast.ModelDecl{{
					Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
					Name:       "Order",
					Directives: []*ast.Directive{{Name: "crud"}},
				}},
				Events: []*ast.EventDecl{
					{Name: "OrderCreated"},
				},
			},
		},
	}

	files := GenerateDeployFiles(result, "shop")
	dc := string(files.DockerCompose)

	// Should include NATS for event bus
	if !strings.Contains(dc, "nats:") {
		t.Error("should include NATS when events exist")
	}
	if !strings.Contains(dc, "NATS_URL") {
		t.Error("should set NATS_URL for event-aware modules")
	}
}

func TestGenerateDeployFiles_NoEvents(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{
			{
				Name: "origin/user.luxo",
				Models: []*ast.ModelDecl{{
					Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
					Name:       "User",
					Directives: []*ast.Directive{{Name: "crud"}},
				}},
			},
		},
	}

	files := GenerateDeployFiles(result, "myapp")
	dc := string(files.DockerCompose)

	if strings.Contains(dc, "nats:") {
		t.Error("should NOT include NATS when no events")
	}
}

func TestGenerateDeployFilesCheckedBackends(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{
		{Name: "origin/watch.luxo", APIs: []*ast.ApiDecl{{Name: "watchValue", Directives: []*ast.Directive{{Name: "stream"}}}}},
		{Name: "origin/order.luxo", Events: []*ast.EventDecl{{Name: "OrderCreated"}}},
		{Name: "origin/read.luxo", APIs: []*ast.ApiDecl{{Name: "readValue", Directives: []*ast.Directive{{Name: "cache"}}}}},
	}}
	for _, sample := range []struct {
		name             string
		selection        EntryBackends
		natsConsumers    []string
		jetstream, redis bool
	}{
		{"memory", EntryBackends{Events: "memory", Queue: "memory", Cache: "memory"}, nil, false, false},
		{"event", EntryBackends{Events: "nats", Cache: "memory"}, []string{"watch", "order", "gateway"}, false, false},
		{"queue", EntryBackends{Events: "memory", Queue: "nats", Cache: "memory"}, []string{"watch", "order", "read"}, true, false},
		{"remote", EntryBackends{Events: "nats", Queue: "nats", Cache: "redis"}, []string{"watch", "order", "read", "gateway"}, true, true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			files, err := GenerateDeployFilesChecked(result, "shop", sample.selection)
			if err != nil {
				t.Fatal(err)
			}
			checkComposeBackends(t, files.DockerCompose, sample.natsConsumers, sample.jetstream, sample.redis)
		})
	}
}

func checkComposeBackends(t *testing.T, manifest []byte, natsConsumers []string, jetstream, redis bool) {
	t.Helper()
	type service struct {
		Environment map[string]string
		DependsOn   map[string]struct{ Condition string } `yaml:"depends_on"`
		Command     []string
		Volumes     []string
		Healthcheck struct{ Test []string }
	}
	var config struct {
		Services map[string]service
		Volumes  map[string]struct{}
	}
	if err := yaml.Unmarshal(manifest, &config); err != nil {
		t.Fatal(err)
	}
	nats, hasNATS := config.Services["nats"]
	redisService, hasRedis := config.Services["redis"]
	if hasNATS != (len(natsConsumers) > 0) || hasRedis != redis {
		t.Fatalf("infrastructure: NATS = %v, Redis = %v", hasNATS, hasRedis)
	}
	_, hasNATSVolume := config.Volumes["natsdata"]
	if hasNATSVolume != jetstream || (len(nats.Volumes) > 0) != jetstream || (len(nats.Command) > 0 && nats.Command[0] == "-js") != jetstream {
		t.Fatalf("JetStream command or storage mismatch: %+v, %v", nats, config.Volumes)
	}
	if hasNATS && len(nats.Healthcheck.Test) == 0 || redis && len(redisService.Healthcheck.Test) == 0 {
		t.Fatal("infrastructure needs a health check")
	}
	checkComposeCacheSettings(t, config.Services["read"].Environment)
	for _, name := range []string{"watch", "order", "read", "gateway"} {
		service := config.Services[name]
		checkComposeServiceConnection(t, name, "NATS_URL", "nats://nats:4222", service.Environment, service.DependsOn["nats"].Condition, slices.Contains(natsConsumers, name))
		checkComposeServiceConnection(t, name, "REDIS_URL", "redis://redis:6379", service.Environment, service.DependsOn["redis"].Condition, redis && name == "read")
		if name != "read" && service.Environment["LUXO_CACHE_PROJECT"] != "" {
			t.Fatalf("service %s got cache settings", name)
		}
	}
}

func checkComposeServiceConnection(t *testing.T, service, key, url string, env map[string]string, condition string, required bool) {
	t.Helper()
	wantURL, wantCondition := "", ""
	if required {
		wantURL, wantCondition = url, "service_healthy"
	}
	if env[key] != wantURL || condition != wantCondition {
		t.Fatalf("service %s: %s = %q, dependency = %q, want %q and %q", service, key, env[key], condition, wantURL, wantCondition)
	}
}

func checkComposeCacheSettings(t *testing.T, env map[string]string) {
	t.Helper()
	for _, name := range []string{
		"LUXO_CACHE_PROJECT", "LUXO_CACHE_ENVIRONMENT", "LUXO_CACHE_REVISION",
		"LUXO_CACHE_ORIGIN_CONCURRENCY", "LUXO_CACHE_ORIGIN_MAX_WAITERS", "LUXO_CACHE_ORIGIN_WAIT_TIMEOUT",
	} {
		if env[name] != "${"+name+":?set "+name+"}" {
			t.Fatalf("cache requires explicit %s, got %q", name, env[name])
		}
	}
}

func TestGenerateDeployFilesCheckedRejectsInvalidResult(t *testing.T) {
	invalid := analyzePermissionFixture(t, "type Detail { level: Int }\nmodel Record { id: Int @id detail: Detail }")
	for _, sample := range []struct {
		result     *semantic.Result
		diagnostic string
	}{
		{nil, "requires a semantic result"},
		{invalid, "has no database mapping"},
	} {
		files, err := GenerateDeployFilesChecked(sample.result, "shop", EntryBackends{})
		if files != nil || err == nil || !strings.Contains(err.Error(), sample.diagnostic) {
			t.Fatalf("invalid result: generated=%v error=%v", files != nil, err)
		}
	}
}

func TestGenerateDeployFilesCheckedRejectsInvalidBackends(t *testing.T) {
	result := analyzePermissionFixture(t, `
event OrderCreated(id: Int)
on OrderCreated @native
`)
	if len(result.Errors) != 0 {
		t.Fatal(result.Errors)
	}
	for _, sample := range []struct {
		name       string
		selection  EntryBackends
		diagnostic string
	}{
		{"missing events backend", EntryBackends{}, "events/streams require an explicit memory or nats build backend"},
		{"unsupported queue backend", EntryBackends{Events: "memory", Queue: "invalid"}, `unsupported queue backend "invalid"`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			files, err := GenerateDeployFilesChecked(result, "shop", sample.selection)
			if files != nil || err == nil || !strings.Contains(err.Error(), sample.diagnostic) {
				t.Fatalf("generated=%v error=%v", files != nil, err)
			}
		})
	}
}

func TestGenerateDeployFiles_ProjectNameInContainerNames(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/user.luxo",
			Models: []*ast.ModelDecl{{
				Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
				Name:       "User",
				Directives: []*ast.Directive{{Name: "crud"}},
			}},
		}},
	}

	files := GenerateDeployFiles(result, "acme")
	dc := string(files.DockerCompose)

	if !strings.Contains(dc, "container_name: acme-pg") {
		t.Error("postgres container should use project name")
	}
	if !strings.Contains(dc, "container_name: acme-redis") {
		t.Error("redis container should use project name")
	}
}

func TestGenerateDockerfile_MultiModuleBuildStages(t *testing.T) {
	modules := []moduleInfo{
		{name: "auth"},
		{name: "payment"},
		{name: "order"},
	}
	df := string(generateDockerfile(modules))

	// Each module should have a build stage + runtime stage
	for _, m := range modules {
		if !strings.Contains(df, "FROM builder AS "+m.name) {
			t.Errorf("missing build stage for %s", m.name)
		}
		if !strings.Contains(df, "FROM alpine:3.20 AS "+m.name+"-run") {
			t.Errorf("missing runtime stage for %s", m.name)
		}
	}

	// Gateway always included
	if !strings.Contains(df, "FROM builder AS gateway") {
		t.Error("missing gateway build stage")
	}
	if !strings.Contains(df, "FROM alpine:3.20 AS gateway-run") {
		t.Error("missing gateway runtime stage")
	}
}
