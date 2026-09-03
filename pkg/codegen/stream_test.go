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

func TestGenerateAuthenticatedStreamRejectsAnonymousIdentity(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{
		Name: "test.luxo",
		APIs: []*ast.ApiDecl{{
			Name:       "watchPrivate",
			Directives: []*ast.Directive{{Name: "stream"}, {Name: "auth"}},
		}},
	}}}
	code := string(generateStreamFile(result, "luxo"))
	if !strings.Contains(code, "if api.IdentityID(identity) == 0") {
		t.Fatalf("authenticated stream has no identity guard:\n%s", code)
	}
}

func TestGenerateStreamFileWithEventSource(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "test.luxo",
			Events: []*ast.EventDecl{{
				Name: "DanmakuSent",
				Params: []*ast.ParamDecl{
					{Name: "danmaku", Type: &ast.TypeRef{Name: "Danmaku"}},
					{Name: "roomId", Type: &ast.TypeRef{Name: "Int"}},
				},
			}},
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

	// Should have RegisterStreams function with injected native resolver.
	if !strings.Contains(code, "func RegisterStreams(router *api.Router, bus event.Bus, resolver StreamResolver)") {
		t.Error("should generate RegisterStreams function")
	}
	if !strings.Contains(code, `router.RequireStream("watchDanmaku")`) {
		t.Error("generated stream must participate in startup validation")
	}

	// Should bind the typed event decoder.
	if !strings.Contains(code, `event.OnDecode[DanmakuSentEvent](bus, "DanmakuSent"`) {
		t.Error("should bind event with its generated decoder")
	}

	// Native event matchers receive the already-decoded typed event once.
	if !strings.Contains(code, "MatchWatchDanmaku(event DanmakuSentEvent, params *api.StreamParams, identity any) bool") {
		t.Error("should generate a typed native event matcher contract")
	}
	if !strings.Contains(code, "matchWatchDanmaku(payload, params, identity, resolver)") || !strings.Contains(code, "resolver.MatchWatchDanmaku(event, params, identity)") {
		t.Error("should delegate the decoded event to resolver.MatchWatchDanmaku")
	}
	if strings.Contains(code, "payload.MarshalLuxo()") || strings.Contains(code, "DispatchEvent(") {
		t.Error("native matcher must not re-encode and decode its event per subscriber")
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
	if !strings.Contains(code, "HandleWatchLiveScore(ctx context.Context, params *api.StreamParams, identity any, stream *api.TypedStream[ScoreEvent])") {
		t.Error("should generate HandleWatchLiveScore method in StreamResolver")
	}

	// Should generate handler wrapper delegating to resolver
	if !strings.Contains(code, "resolver.HandleWatchLiveScore(ctx, params, identity, typedStream)") {
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
					ReturnType: &ast.TypeRef{Name: "String"},
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

	// Payload encoding still uses the canonical codec.
	if !strings.Contains(code, `"github.com/light-speak/luxo/pkg/lux/codec"`) {
		t.Error("should import codec package")
	}

	if strings.Contains(code, "codec.NewDecoder(data)") {
		t.Error("typed event fields must not be decoded again per subscriber")
	}
	if !strings.Contains(code, "DispatchPreparedEvent") || !strings.Contains(code, "matchWatchDanmaku(payload.RoomId, params, identity)") {
		t.Error("typed event field should be captured by the prepared matcher")
	}

	// Should validate and decode the subscription parameter once.
	if !strings.Contains(code, `param_roomId, ok_roomId := params.LookupInt("roomId")`) {
		t.Error("should validate the subscription parameter")
	}

	// Should return comparison
	if !strings.Contains(code, "ev_roomId == param_roomId") {
		t.Error("should compare decoded field with param")
	}

	// Should have event binding
	if !strings.Contains(code, `event.OnDecode[DanmakuSentEvent](bus, "DanmakuSent"`) {
		t.Error("should bind event to bus")
	}
}

func TestGenerateTypedStreamRegistersMatcher(t *testing.T) {
	result := &semantic.Result{Files: []*ast.File{{
		Name: "test.luxo",
		Events: []*ast.EventDecl{{
			Name:   "AlertFired",
			Params: []*ast.ParamDecl{{Name: "alert", Type: &ast.TypeRef{Name: "Alert"}}},
		}},
		APIs: []*ast.ApiDecl{{
			Name:       "liveAlerts",
			ReturnType: &ast.TypeRef{Name: "Alert"},
			Directives: []*ast.Directive{
				{Name: "stream", Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "AlertFired"}}}},
				{Name: "auth"},
			},
		}},
	}}}

	code := string(generateStreamFile(result, "luxo"))
	if !strings.Contains(code, `router.Streams.DispatchPreparedEvent("liveAlerts"`) {
		t.Fatalf("typed stream registration missing:\n%s", code)
	}
	if !strings.Contains(code, `DispatchPreparedEvent("liveAlerts", matchLiveAlerts`) {
		t.Fatalf("authenticated stream matcher registration missing:\n%s", code)
	}
	if !strings.Contains(code, `return router.StreamPayloadJSON("liveAlerts", data)`) {
		t.Fatalf("JSON streams must use schema conversion:\n%s", code)
	}
	if strings.Contains(code, "json.Marshal") {
		t.Fatalf("stream payload must not bypass Luxo binary encoding:\n%s", code)
	}
}

func TestGenerateStreamPayloadEncodings(t *testing.T) {
	list := streamInfo{
		returnType:  &ast.TypeRef{Name: "Int", IsList: true},
		payloadEnum: false,
	}
	var b strings.Builder
	writeStreamPayloadEncoding(&b, list, "payload.Values", "\t")
	if code := b.String(); !strings.Contains(code, "codec.AppendArrayHeader") || !strings.Contains(code, "codec.AppendSvarint") {
		t.Fatalf("scalar list stream encoding is not canonical:\n%s", code)
	}

	b.Reset()
	models := streamInfo{returnType: &ast.TypeRef{Name: "Alert", IsList: true}, payloadKind: streamPayloadModel}
	writeStreamPayloadEncoding(&b, models, "payload.Alerts", "\t")
	if code := b.String(); !strings.Contains(code, "WriteColumnarAlertValues") {
		t.Fatalf("model list stream encoding must be columnar:\n%s", code)
	}
}

func TestSameStreamTypeIsExact(t *testing.T) {
	base := &ast.TypeRef{Name: "Int"}
	if !sameStreamType(base, &ast.TypeRef{Name: "Int"}) {
		t.Fatal("identical stream types should match")
	}
	if sameStreamType(base, &ast.TypeRef{Name: "Int", Nullable: true}) || sameStreamType(base, &ast.TypeRef{Name: "Int", IsList: true}) {
		t.Fatal("nullable and list stream payloads must not match scalar returns")
	}
}

func TestGenerateCrossModuleStreamSource(t *testing.T) {
	old := globalEventCtx
	defer func() { globalEventCtx = old }()
	eventDecl := &ast.EventDecl{
		Name:   "CountChanged",
		Params: []*ast.ParamDecl{{Name: "count", Type: &ast.TypeRef{Name: "Int"}}},
	}
	globalEventCtx = &EventContext{
		EventModule: map[string]string{"CountChanged": "common"},
		Events:      map[string]*ast.EventDecl{"CountChanged": eventDecl},
		ModelModule: map[string]string{}, TypeModule: map[string]string{}, EnumModule: map[string]string{},
		ModulePath: "github.com/test/service",
	}
	result := &semantic.Result{Files: []*ast.File{{
		Name: "origin/consumer/watch.luxo",
		APIs: []*ast.ApiDecl{{
			Name:       "watchCount",
			ReturnType: &ast.TypeRef{Name: "Int"},
			Directives: []*ast.Directive{{Name: "stream", Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "CountChanged"}}}}},
		}},
	}}}
	code := string(generateStreamFile(result, "luxo"))
	for _, want := range []string{
		`common_luxo "github.com/test/service/common/luxo"`,
		`event.OnDecode[common_luxo.CountChangedEvent]`,
		`common_luxo.UnmarshalCountChanged`,
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("cross-module stream missing %q:\n%s", want, code)
		}
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
			want: `param_roomId`,
		},
		{
			name: "string param",
			expr: &ast.Ident{Name: "name"},
			want: `param_name`,
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
			want: `ev_roomId == param_roomId && ev_userId == api.IdentityID(identity)`,
		},
		{
			name: "unary not",
			expr: &ast.UnaryExpr{Op: "!", Value: &ast.Ident{Name: "roomId"}},
			want: `!param_roomId`,
		},
		{
			name: "bare my ident",
			expr: &ast.Ident{Name: "my"},
			want: "identity",
		},
		{
			name: "unknown bare ident",
			expr: &ast.Ident{Name: "unknown"},
			want: "unknown",
		},
		{
			name: "my.role == string literal → IdentityString",
			expr: &ast.BinaryExpr{
				Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "role"},
				Op:    "==",
				Right: &ast.Literal{Kind: token.String, Value: "admin"},
			},
			want: `api.IdentityString(identity, "role") == "admin"`,
		},
		{
			name: "my.level == int → stays IdentityInt",
			expr: &ast.BinaryExpr{
				Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "level"},
				Op:    ">=",
				Right: &ast.Literal{Kind: token.Int, Value: "5"},
			},
			want: `api.IdentityInt(identity, "level") >= 5`,
		},
		{
			name: "unsupported expr",
			expr: &ast.CallExpr{Func: &ast.Ident{Name: "foo"}},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := compileStreamExpr(tt.expr, params, nil, "")
			if got != tt.want {
				t.Errorf("compileStreamExpr = %q, want %q", got, tt.want)
			}
			if ok == (tt.name == "unsupported expr") {
				t.Errorf("compileStreamExpr ok = %v", ok)
			}
		})
	}
}

func TestStreamFieldTypes(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Int", "int64"}, {"Float", "float64"}, {"String", "string"},
		{"Boolean", "bool"}, {"Duration", "int64"}, {"UUID", "[16]byte"},
	}
	for _, c := range cases {
		if got := streamFieldGoType(&ast.TypeRef{Name: c.in}, false); got != c.want {
			t.Errorf("streamFieldGoType(%q) = %q", c.in, got)
		}
	}
	if got := streamFieldGoType(&ast.TypeRef{Name: "Role"}, true); got != "string" {
		t.Errorf("enum stream type = %q", got)
	}

	// streamParamMethod — all branches
	paramCases := []struct{ in, want string }{
		{"Int", "Int"}, {"String", "String"}, {"Boolean", "Boolean"},
	}
	for _, c := range paramCases {
		if got := streamParamMethod(c.in, false); got != c.want {
			t.Errorf("streamParamMethod(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExtractMatcherExpr_EdgeCases(t *testing.T) {
	// nil body
	if extractMatcherExpr(nil) != nil {
		t.Error("nil body should return nil")
	}
	// empty stmts
	if extractMatcherExpr(&ast.Block{}) != nil {
		t.Error("empty stmts should return nil")
	}
	// all nil exprs (non-ExprStmt statements)
	block := &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: nil}}}
	if extractMatcherExpr(block) != nil {
		t.Error("all nil exprs should return nil")
	}
}

func TestGenerateLuxoStreamMatcher_EmptyBody(t *testing.T) {
	var b strings.Builder
	si := streamInfo{
		apiName: "watchEmpty",
		hasBody: true,
		body:    &ast.Block{}, // no stmts
	}
	generateLuxoStreamMatcher(&b, si, nil)
	code := b.String()
	if !strings.Contains(code, "STREAM_MATCHER_EMPTY") {
		t.Error("empty body should generate STREAM_MATCHER_EMPTY error")
	}
}

func TestGenerateLuxoStreamMatcher_UnknownItField(t *testing.T) {
	// it.unknownField — field not in event params
	// Var declaration is skipped, but reference remains → Go compile error (correct behavior)
	SetEventFieldIDs(map[string]map[string]int{
		"TestEvent": {"knownField": 1},
	})
	defer SetEventFieldIDs(nil)

	var b strings.Builder
	si := streamInfo{
		apiName:   "watchUnknown",
		eventName: "TestEvent",
		hasBody:   true,
		body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: &ast.BinaryExpr{
			Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "unknownField"},
			Op:    "==",
			Right: &ast.Literal{Kind: token.Int, Value: "1"},
		}}}},
		eventParams: []*ast.ParamDecl{
			{Name: "knownField", Type: &ast.TypeRef{Name: "Int"}},
		},
	}
	generateLuxoStreamMatcher(&b, si, nil)
	code := b.String()
	if strings.Contains(code, "codec.NewDecoder") {
		t.Error("generated matchers must not decode per subscriber")
	}
	// No var declaration for unknownField (not in event params)
	if strings.Contains(code, "var ev_unknownField") {
		t.Error("unknown field should not have var declaration")
	}
	// Reference still exists (will fail Go compile — semantic should catch this)
	if !strings.Contains(code, "ev_unknownField") {
		t.Error("expression reference should still compile to ev_unknownField")
	}
}

func TestWalkExpr_AllBranches(t *testing.T) {
	// Test that walkExpr visits all node types
	var visited []string
	expr := &ast.BinaryExpr{
		Left: &ast.UnaryExpr{
			Op:    "!",
			Value: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "x"},
		},
		Right: &ast.CallExpr{
			Func: &ast.Ident{Name: "foo"},
			Args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.Int, Value: "1"}}},
		},
	}
	walkExpr(expr, func(e ast.Expr) {
		switch e.(type) {
		case *ast.BinaryExpr:
			visited = append(visited, "binary")
		case *ast.UnaryExpr:
			visited = append(visited, "unary")
		case *ast.MemberExpr:
			visited = append(visited, "member")
		case *ast.Ident:
			visited = append(visited, "ident")
		case *ast.CallExpr:
			visited = append(visited, "call")
		case *ast.Literal:
			visited = append(visited, "literal")
		}
	})
	// Should visit: binary, unary, member, ident(it), call, ident(foo), literal(1)
	if len(visited) != 7 {
		t.Errorf("expected 7 nodes visited, got %d: %v", len(visited), visited)
	}

	// walkExpr with nil — should not panic
	walkExpr(nil, func(e ast.Expr) { t.Error("should not visit nil") })
}

func TestCompileStreamExpr_GenericMember(t *testing.T) {
	// obj.field where obj is not "it" or "my"
	params := map[string]*ast.ParamDecl{}
	expr := &ast.MemberExpr{
		Object: &ast.Ident{Name: "other"},
		Field:  "value",
	}
	got, ok := compileStreamExpr(expr, params, nil, "")
	if ok || got != "" {
		t.Errorf("generic member should be rejected, got %q, ok=%v", got, ok)
	}
}
