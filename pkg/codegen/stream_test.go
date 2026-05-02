package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestGenerateStreamFileNoStreams(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{Name: "test.luxo"}}}
	src := generateStreamFile(result, "luxo")
	if src != nil {
		t.Error("should return nil when no @stream APIs")
	}
}

func TestGenerateStreamFileWithEventSource(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			APIs: []*ast.ApiDecl{
				{
					Pos:  token.Position{File: "test.luxo", Line: 1, Col: 1},
					Name: "watchDanmaku",
					Params: []*ast.ParamDecl{
						{Name: "roomId", Type: &ast.TypeRef{Name: "Int"}},
					},
					ReturnType: &ast.TypeRef{Name: "Danmaku"},
					Directives: []*ast.Directive{
						{Name: "stream", Args: []*ast.NamedArg{
							{Value: &ast.Ident{Name: "DanmakuSent"}},
						}},
						{Name: "native"},
					},
				},
			},
		}},
	}

	src := generateStreamFile(result, "luxo")
	if src == nil {
		t.Fatal("should generate stream file")
	}

	code := string(src)

	// Should import context (has event source)
	if !strings.Contains(code, `"context"`) {
		t.Error("should import context when event source exists")
	}

	// Should have RegisterStreams function
	if !strings.Contains(code, "func RegisterStreams(") {
		t.Error("should generate RegisterStreams function")
	}

	// Should bind event to bus.On
	if !strings.Contains(code, `bus.On("DanmakuSent"`) {
		t.Error("should bind event to bus.On")
	}

	// Should generate native matcher delegating to resolver
	if !strings.Contains(code, "resolver.MatchWatchDanmaku") {
		t.Error("should delegate to resolver.MatchWatchDanmaku")
	}

	// Should register matcher on router
	if !strings.Contains(code, `router.HandleStream("watchDanmaku"`) {
		t.Error("should register matcher on router")
	}
}

func TestGenerateStreamFileNoEventSource(t *testing.T) {
	// @stream @native without event — should skip in RegisterStreams
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			APIs: []*ast.ApiDecl{
				{
					Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
					Name:       "watchLiveScore",
					Params:     []*ast.ParamDecl{{Name: "matchId", Type: &ast.TypeRef{Name: "Int"}}},
					ReturnType: &ast.TypeRef{Name: "ScoreEvent"},
					Directives: []*ast.Directive{
						{Name: "stream"},
						{Name: "native"},
					},
				},
			},
		}},
	}

	src := generateStreamFile(result, "luxo")
	if src == nil {
		t.Fatal("should generate stream file")
	}

	code := string(src)

	// No event source → should NOT import context
	if strings.Contains(code, `"context"`) {
		t.Error("should NOT import context when no event source")
	}

	// Should NOT have bus.On (no event binding)
	if strings.Contains(code, "bus.On(") {
		t.Error("should not bind to bus when no event source")
	}
}

func TestGenerateStreamFileLuxoMatcher(t *testing.T) {
	// @stream with lambda body (non-native) — should generate compile-error stub
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			APIs: []*ast.ApiDecl{
				{
					Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
					Name:       "watchNotifications",
					ReturnType: &ast.TypeRef{Name: "Notification"},
					Directives: []*ast.Directive{
						{Name: "stream", Args: []*ast.NamedArg{
							{Value: &ast.Ident{Name: "NotificationCreated"}},
						}},
					},
					Body: &ast.Block{
						Stmts: []ast.Stmt{&ast.ExprStmt{Expr: &ast.Ident{Name: "placeholder"}}},
					},
				},
			},
		}},
	}

	src := generateStreamFile(result, "luxo")
	if src == nil {
		t.Fatal("should generate stream file")
	}

	code := string(src)

	// Should generate deliberate compile error
	if !strings.Contains(code, "STREAM_LAMBDA_NOT_IMPLEMENTED") {
		t.Error("luxo stream matcher should generate compile-time error marker")
	}

	// Should have event binding
	if !strings.Contains(code, `bus.On("NotificationCreated"`) {
		t.Error("should bind event to bus")
	}
}

func TestCollectStreams(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			APIs: []*ast.ApiDecl{
				{
					Name:       "normalAPI",
					Directives: []*ast.Directive{},
				},
				{
					Name: "streamWithEvent",
					Directives: []*ast.Directive{
						{Name: "stream", Args: []*ast.NamedArg{
							{Value: &ast.Ident{Name: "MyEvent"}},
						}},
					},
				},
				{
					Name: "streamNative",
					Directives: []*ast.Directive{
						{Name: "stream"},
						{Name: "native"},
					},
				},
			},
		}},
	}

	streams := collectStreams(result)
	if len(streams) != 2 {
		t.Fatalf("expected 2 streams, got %d", len(streams))
	}

	// First stream: has event source
	if streams[0].apiName != "streamWithEvent" {
		t.Errorf("first stream should be streamWithEvent, got %s", streams[0].apiName)
	}
	if streams[0].eventName != "MyEvent" {
		t.Errorf("event name should be MyEvent, got %s", streams[0].eventName)
	}
	if streams[0].isNative {
		t.Error("streamWithEvent should not be native")
	}

	// Second stream: native without event
	if streams[1].apiName != "streamNative" {
		t.Errorf("second stream should be streamNative, got %s", streams[1].apiName)
	}
	if streams[1].eventName != "" {
		t.Errorf("streamNative should have no event name, got %s", streams[1].eventName)
	}
	if !streams[1].isNative {
		t.Error("streamNative should be native")
	}
}
