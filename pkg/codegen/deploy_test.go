package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

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
		"FROM golang:1.26-alpine AS builder",
		"FROM builder AS user",
		"FROM builder AS post",
		"FROM builder AS gateway",
		"FROM alpine:3.20 AS user-run",
		"FROM alpine:3.20 AS post-run",
		"CGO_ENABLED=0 go build",
		"./luxis/user/",
		"./luxis/post/",
		"./luxis/gateway/",
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
