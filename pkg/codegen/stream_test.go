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
	// @stream @native without event — should generate HandleStreamNative + StreamResolver
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

	// No event source → should NOT import context (RegisterStreams doesn't use it)
	if strings.Contains(code, "bus.On(") {
		t.Error("should not bind to bus when no event source")
	}

	// Should register native stream handler
	if !strings.Contains(code, `router.HandleStreamNative("watchLiveScore"`) {
		t.Error("should register HandleStreamNative")
	}

	// Should generate StreamResolver interface
	if !strings.Contains(code, "type StreamResolver interface") {
		t.Error("should generate StreamResolver interface")
	}
	if !strings.Contains(code, "HandleWatchLiveScore(ctx context.Context, params *api.StreamParams, identity any, stream *api.Stream)") {
		t.Error("should generate HandleWatchLiveScore method in StreamResolver")
	}

	// Should generate handler wrapper delegating to resolver
	if !strings.Contains(code, "resolver.HandleWatchLiveScore(ctx, params, identity, stream)") {
		t.Error("should delegate to resolver.HandleWatchLiveScore")
	}
}

func TestGenerateStreamFileLuxoMatcher(t *testing.T) {
	// @stream with body `it.roomId == roomId` — should compile to decoder + comparison
	// Set event field IDs for the test
	SetEventFieldIDs(map[string]map[string]int{
		"DanmakuSent": {"roomId": 1, "content": 2},
	})
	defer SetEventFieldIDs(nil)

	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Events: []*ast.EventDecl{
				{
					Name: "DanmakuSent",
					Params: []*ast.ParamDecl{
						{Name: "roomId", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "content", Type: &ast.TypeRef{Name: "String"}},
					},
				},
			},
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
					},
					// Body: it.roomId == roomId
					Body: &ast.Block{
						Stmts: []ast.Stmt{&ast.ExprStmt{Expr: &ast.BinaryExpr{
							Left: &ast.MemberExpr{
								Object: &ast.Ident{Name: "it"},
								Field:  "roomId",
							},
							Op:    "==",
							Right: &ast.Ident{Name: "roomId"},
						}}},
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

	// Should import codec for decoding
	if !strings.Contains(code, `"github.com/light-speak/luxo/pkg/lux/codec"`) {
		t.Error("should import codec package")
	}

	// Should generate decoder
	if !strings.Contains(code, "codec.NewDecoder(data)") {
		t.Error("should generate decoder")
	}

	// Should decode roomId field (field ID 1)
	if !strings.Contains(code, "case 1:") {
		t.Error("should decode field ID 1")
	}
	if !strings.Contains(code, "dec.ReadInt()") {
		t.Error("should use ReadInt for Int field")
	}

	// Should access subscription param
	if !strings.Contains(code, `params.Int("roomId")`) {
		t.Error("should access params.Int for subscription param")
	}

	// Should return comparison
	if !strings.Contains(code, "ev_roomId == params.Int") {
		t.Error("should compare decoded field with param")
	}

	// Should have event binding
	if !strings.Contains(code, `bus.On("DanmakuSent"`) {
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

func TestCompileStreamExpr(t *testing.T) {
	params := map[string]*ast.ParamDecl{
		"roomId": {Name: "roomId", Type: &ast.TypeRef{Name: "Int"}},
		"name":   {Name: "name", Type: &ast.TypeRef{Name: "String"}},
	}

	tests := []struct {
		name string
		expr ast.Expr
		want string
	}{
		{
			name: "it.field access",
			expr: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "roomId"},
			want: "ev_roomId",
		},
		{
			name: "my.id",
			expr: &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "id"},
			want: "api.IdentityID(identity)",
		},
		{
			name: "my.userId",
			expr: &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "userId"},
			want: `api.IdentityInt(identity, "userId")`,
		},
		{
			name: "int param",
			expr: &ast.Ident{Name: "roomId"},
			want: `params.Int("roomId")`,
		},
		{
			name: "string param",
			expr: &ast.Ident{Name: "name"},
			want: `params.String("name")`,
		},
		{
			name: "string literal",
			expr: &ast.Literal{Kind: token.String, Value: "hello"},
			want: `"hello"`,
		},
		{
			name: "int literal",
			expr: &ast.Literal{Kind: token.Int, Value: "42"},
			want: "42",
		},
		{
			name: "bool literal true",
			expr: &ast.Literal{Kind: token.True, Value: "true"},
			want: "true",
		},
		{
			name: "binary ==",
			expr: &ast.BinaryExpr{
				Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "userId"},
				Op:    "==",
				Right: &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "id"},
			},
			want: "ev_userId == api.IdentityID(identity)",
		},
		{
			name: "logical && combination",
			expr: &ast.BinaryExpr{
				Left: &ast.BinaryExpr{
					Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "roomId"},
					Op:    "==",
					Right: &ast.Ident{Name: "roomId"},
				},
				Op: "&&",
				Right: &ast.BinaryExpr{
					Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "userId"},
					Op:    "==",
					Right: &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "id"},
				},
			},
			want: `ev_roomId == params.Int("roomId") && ev_userId == api.IdentityID(identity)`,
		},
		{
			name: "unary not",
			expr: &ast.UnaryExpr{Op: "!", Value: &ast.Ident{Name: "roomId"}},
			want: `!params.Int("roomId")`,
		},
		{
			name: "unsupported expr",
			expr: &ast.CallExpr{Func: &ast.Ident{Name: "foo"}},
			want: "false /* unsupported expr */",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compileStreamExpr(tt.expr, params)
			if got != tt.want {
				t.Errorf("compileStreamExpr = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStreamFieldTypes(t *testing.T) {
	// streamFieldGoType
	if streamFieldGoType("Float") != "float64" {
		t.Error("Float should map to float64")
	}
	if streamFieldGoType("String") != "string" {
		t.Error("String should map to string")
	}
	if streamFieldGoType("Boolean") != "bool" {
		t.Error("Boolean should map to bool")
	}

	// streamReadMethod
	if streamReadMethod("Float") != "ReadFloat" {
		t.Error("Float should map to ReadFloat")
	}
	if streamReadMethod("String") != "ReadString" {
		t.Error("String should map to ReadString")
	}
	if streamReadMethod("Boolean") != "ReadBool" {
		t.Error("Boolean should map to ReadBool")
	}

	// streamParamMethod
	if streamParamMethod("String") != "String" {
		t.Error("String param should use String method")
	}
	if streamParamMethod("Boolean") != "Get" {
		t.Error("Unknown param type should use Get method")
	}
}
