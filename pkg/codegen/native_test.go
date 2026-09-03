package codegen

import (
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

func TestGenerateNativeFileImportsTimeForDateTime(t *testing.T) {
	result := &semantic.Result{Files: []*luxoast.File{{
		APIs: []*luxoast.ApiDecl{mkNativeAPI(
			"schedule",
			[]*luxoast.ParamDecl{{Name: "at", Type: &luxoast.TypeRef{Name: "DateTime"}}},
			&luxoast.TypeRef{Name: "DateTime"},
		)},
	}}}
	code := string(GenerateNativeFile(result, "luxo"))
	if !strings.Contains(code, `"time"`) {
		t.Fatalf("DateTime native resolver did not import time:\n%s", code)
	}
}

func TestGenerateNativeFileEmbedsTypedStreamResolver(t *testing.T) {
	result := &semantic.Result{Files: []*luxoast.File{{
		APIs: []*luxoast.ApiDecl{{
			Name:       "watchScores",
			ReturnType: &luxoast.TypeRef{Name: "Score"},
			Directives: []*luxoast.Directive{{Name: "stream"}, {Name: "native"}},
		}},
	}}}
	code := string(GenerateNativeFile(result, "luxo"))
	if !strings.Contains(code, "type NativeResolver interface {\n\tStreamResolver") {
		t.Fatalf("native stream resolver must be embedded:\n%s", code)
	}
	if strings.Contains(code, "WatchScores(ctx context.Context") {
		t.Fatalf("native stream must not also generate a unary resolver method:\n%s", code)
	}
}

func TestGenerateNativeFileNoReturn(t *testing.T) {
	// Native functions may return Void and should expose an error-only Go method.
	result := &semantic.Result{
		Files: []*luxoast.File{{
			Functions: []*luxoast.FnDecl{
				{
					Name:       "doSomething",
					Directives: []*luxoast.Directive{{Name: "native"}},
				},
			},
		}},
	}

	src := GenerateNativeFile(result, "luxo")
	if src == nil {
		t.Fatal("should generate native file")
	}
	code := string(src)
	if !strings.Contains(code, "DoSomething(ctx context.Context) error") {
		t.Errorf("void native function should return only error:\n%s", code)
	}
	if strings.Contains(code, "any") {
		t.Errorf("void native function must not leak any into generated code:\n%s", code)
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
