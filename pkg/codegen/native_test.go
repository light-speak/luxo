package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	luxoast "github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func mkNativeAPI(name string, params []*luxoast.ParamDecl, ret *luxoast.TypeRef) *luxoast.ApiDecl {
	return &luxoast.ApiDecl{
		Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
		Name:       name,
		Params:     params,
		ReturnType: ret,
		Directives: []*luxoast.Directive{{Name: "native"}},
	}
}

func TestGenerateNativeFileNoNative(t *testing.T) {
	result := &semantic.Result{
		Files: []*luxoast.File{{
			APIs: []*luxoast.ApiDecl{{
				Name:       "getUser",
				Directives: []*luxoast.Directive{{Name: "auth"}},
			}},
		}},
	}
	src := GenerateNativeFile(result, "luxo")
	if src != nil {
		t.Error("should return nil when no @native APIs")
	}
}

func TestGenerateNativeFile(t *testing.T) {
	result := &semantic.Result{
		Files: []*luxoast.File{{
			APIs: []*luxoast.ApiDecl{
				mkNativeAPI("oauthLogin",
					[]*luxoast.ParamDecl{
						{Name: "provider", Type: &luxoast.TypeRef{Name: "String"}},
						{Name: "code", Type: &luxoast.TypeRef{Name: "String"}},
					},
					&luxoast.TypeRef{Name: "String"},
				),
				mkNativeAPI("encrypt",
					[]*luxoast.ParamDecl{
						{Name: "value", Type: &luxoast.TypeRef{Name: "String"}},
					},
					&luxoast.TypeRef{Name: "String"},
				),
			},
		}},
	}

	src := GenerateNativeFile(result, "luxo")
	if src == nil {
		t.Fatal("should generate native file")
	}
	code := string(src)

	checks := []string{
		"NativeResolver interface",
		"OauthLogin(ctx context.Context, provider string, code string) (string, error)",
		"Encrypt(ctx context.Context, value string) (string, error)",
	}
	for _, check := range checks {
		if !strings.Contains(code, check) {
			t.Errorf("missing %q in:\n%s", check, code)
		}
	}
}

func TestGenerateNativeStub(t *testing.T) {
	stub := generateNativeStub(nativeAPI{
		Name:       "oauthLogin",
		Params:     []*luxoast.ParamDecl{{Name: "provider", Type: &luxoast.TypeRef{Name: "String"}}},
		ReturnType: &luxoast.TypeRef{Name: "String"},
	})

	if !strings.Contains(stub, "func OauthLogin(ctx context.Context, provider string) (string, error)") {
		t.Errorf("bad stub:\n%s", stub)
	}
	if !strings.Contains(stub, "panic(\"not implemented\")") {
		t.Error("should have panic placeholder")
	}
}

func TestMergeResolverAppendNew(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resolver.go")

	initial := `package resolver

import "context"

func Setup() {
}
`
	os.WriteFile(path, []byte(initial), 0644)

	apis := []nativeAPI{
		{
			Name:       "oauthLogin",
			Params:     []*luxoast.ParamDecl{{Name: "code", Type: &luxoast.TypeRef{Name: "String"}}},
			ReturnType: &luxoast.TypeRef{Name: "String"},
		},
	}

	err := MergeResolver(path, apis)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	data, _ := os.ReadFile(path)
	code := string(data)

	if !strings.Contains(code, "func Setup()") {
		t.Error("should preserve existing functions")
	}
	if !strings.Contains(code, "func OauthLogin(ctx context.Context") {
		t.Errorf("should append new function:\n%s", code)
	}
}

func TestMergeResolverExistingSkip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resolver.go")

	initial := `package resolver

import "context"

func OauthLogin(ctx context.Context, code string) (string, error) {
	return "my-implementation", nil
}
`
	os.WriteFile(path, []byte(initial), 0644)

	apis := []nativeAPI{
		{
			Name:       "oauthLogin",
			Params:     []*luxoast.ParamDecl{{Name: "code", Type: &luxoast.TypeRef{Name: "String"}}},
			ReturnType: &luxoast.TypeRef{Name: "String"},
		},
	}

	err := MergeResolver(path, apis)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	data, _ := os.ReadFile(path)
	code := string(data)

	// Should preserve user's implementation, not overwrite
	if !strings.Contains(code, "my-implementation") {
		t.Error("should preserve existing implementation")
	}
	// Should NOT have panic placeholder
	if strings.Contains(code, "not implemented") {
		t.Error("should not add stub for existing function")
	}
}

func TestMergeResolverNoFile(t *testing.T) {
	err := MergeResolver("/nonexistent/path/resolver.go", nil)
	if err != nil {
		t.Error("should not error for non-existent file")
	}
}

func TestCollectNativeAPIs(t *testing.T) {
	result := &semantic.Result{
		Files: []*luxoast.File{{
			APIs: []*luxoast.ApiDecl{
				{Name: "getUser", Directives: []*luxoast.Directive{{Name: "cache"}}},
				mkNativeAPI("encrypt", nil, nil),
				mkNativeAPI("decrypt", nil, nil),
			},
		}},
	}
	apis := collectNativeAPIs(result)
	if len(apis) != 2 {
		t.Errorf("expected 2 native APIs, got %d", len(apis))
	}
}
