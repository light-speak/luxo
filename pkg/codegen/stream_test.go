package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestStreamEnumModulesTrackTypedIdentityAndSkipLocalOwner(t *testing.T) {
	member := &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "role"}
	member.SetTypeTag("Role")
	body := &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: &ast.BinaryExpr{Left: member, Op: "==", Right: member}}}}
	for _, sample := range []struct {
		name, source, want string
		modules            map[string]string
		body               *ast.Block
	}{
		{"remote", "feed", "auth", map[string]string{"Role": "auth"}, body},
		{"local", "feed", "", map[string]string{"Role": "feed"}, body},
		{"same_owner", "auth", "", map[string]string{"Role": "auth"}, body},
		{"empty_catalog", "feed", "", nil, body},
		{"unknown_type", "feed", "", map[string]string{"Other": "auth"}, body},
		{"empty_owner", "feed", "", map[string]string{"Role": ""}, body},
		{"empty_body", "feed", "", map[string]string{"Role": "auth"}, nil},
	} {
		t.Run(sample.name, func(t *testing.T) {
			modules := make(map[string]bool)
			collectStreamEnumModules(sample.body, sample.modules, sample.source, modules)
			if sample.want == "" && len(modules) != 0 || sample.want != "" && (len(modules) != 1 || !modules[sample.want]) {
				t.Fatalf("enum dependencies=%v want=%q", modules, sample.want)
			}
		})
	}
}

func TestStreamOutputIdentityDependency(t *testing.T) {
	visible := &ast.Directive{Name: "visible", Args: []*ast.NamedArg{{Value: &ast.BinaryExpr{Left: &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "id"}, Op: "==", Right: &ast.Literal{Kind: token.Int, Value: "42"}}}}}
	for _, sample := range []struct {
		name   string
		fields map[string][]*ast.FieldDecl
		want   bool
	}{
		{"plain", map[string][]*ast.FieldDecl{"Root": {testField("name", "String")}}, false},
		{"direct", map[string][]*ast.FieldDecl{"Root": {{Name: "secret", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{visible}}}}, true},
		{"nested", map[string][]*ast.FieldDecl{"Root": {{Name: "children", Type: &ast.TypeRef{Name: "Child", IsList: true}}}, "Child": {{Name: "secret", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{visible}}}}, true},
		{"recursive", map[string][]*ast.FieldDecl{"Root": {testField("next", "Root")}}, false},
		{"hidden", map[string][]*ast.FieldDecl{"Root": {{Name: "secret", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{visible, {Name: "hidden"}}}}}, false},
		{"remote", nil, true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			declarations := streamTypes{types: map[string]bool{"Root": true}, fields: sample.fields}
			if got := streamOutputUsesIdentity("Root", declarations); got != sample.want {
				t.Fatalf("identity dependency: %v, want %v", got, sample.want)
			}
			generator := generatorWithModelFieldIDs(map[string]map[string]int{"Root": {"name": 1, "secret": 2, "children": 3, "next": 4}})
			ids := generator.streamIdentityFields("Root", declarations)
			want := 0
			if sample.name == "direct" {
				want = 2
			}
			if sample.name == "nested" {
				want = 3
			}
			if want == 0 && len(ids) != 0 || want != 0 && (len(ids) != 1 || ids[0] != want) {
				t.Fatalf("unsafe identity projection: %v, want field %d", ids, want)
			}
		})
	}
}

func TestStreamIdentityProjectionRejectsPartialCatalog(t *testing.T) {
	input := analyzePermissionFixture(t, `
model Viewer @noTime @withAuth(stores: [id], default: true) { id: Int @id }
type Profile {
 first: String? @visible(my.id == 7)
 second: String? @visible(my.id == 9)
}
api watch(): Profile @native @stream
`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	for _, sample := range []struct {
		name   string
		fields map[string]int
	}{
		{"complete", map[string]int{"first": 1, "second": 2}},
		{"partial", map[string]int{"first": 1}},
	} {
		t.Run(sample.name, func(t *testing.T) {
			ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Viewer": {"id": 1}, "Profile": sample.fields}})
			generator := mustNewGenerator(t, GeneratorConfig{IDs: ids})
			streams := generator.collectStreams(input)
			if len(streams) != 1 || !streams[0].payloadIdentity {
				t.Fatal("identity-dependent stream was not collected")
			}
			projection := streams[0].identityFields
			generated, err := generator.Generate(input, "generated", nil)
			if sample.name == "partial" {
				if projection != nil || err == nil || generated != nil || !strings.Contains(err.Error(), "Profile.second") {
					t.Fatalf("partial catalog escaped the conservative boundary: projection=%v, generated=%v, error=%v", projection, generated != nil, err)
				}
			} else if len(projection) != 2 || projection[0] != 1 || projection[1] != 2 || err != nil || generated == nil {
				t.Fatalf("complete catalog rejected: projection=%v, error=%v", projection, err)
			}
		})
	}
}

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
	if !strings.Contains(code, "switch value := rawIdentity.(type)") || !strings.Contains(code, "default: return errors.Unauthorized") {
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
	if !strings.Contains(code, `event.OnValueDecode[DanmakuSentEvent](bus, "DanmakuSent"`) {
		t.Error("should bind event with its generated decoder")
	}

	// Native event matchers receive the already-decoded typed event once.
	if !strings.Contains(code, "MatchWatchDanmaku(event DanmakuSentEvent, params *WatchDanmakuStreamParams, identity any) bool") {
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
	if !strings.Contains(code, `router.HandleStreamPrepared("watchLiveScore"`) {
		t.Error("should register HandleStreamPrepared")
	}

	// Should generate StreamResolver interface
	if !strings.Contains(code, "type StreamResolver interface") {
		t.Error("should generate StreamResolver interface")
	}
	if !strings.Contains(code, "HandleWatchLiveScore(ctx context.Context, params *WatchLiveScoreStreamParams, identity any, stream *api.TypedStream[ScoreEvent]) error") {
		t.Error("should generate HandleWatchLiveScore method in StreamResolver")
	}

	// Should generate handler wrapper delegating to resolver
	if !strings.Contains(code, "return resolver.HandleWatchLiveScore(ctx, params, identity, typedStream)") {
		t.Error("should delegate to resolver.HandleWatchLiveScore")
	}
}

func TestGenerateStreamFileLuxoMatcher(t *testing.T) {
	// @stream with body `it.roomId == roomId` — should compile to decoder + comparison
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{EventFields: map[string]map[string]int{
		"DanmakuSent": {"roomId": 1, "content": 2},
	}}})

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

	src := generator.generateStreamFile(result, "luxo")
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
	if !strings.Contains(code, "streamWatchDanmaku.Dispatch") || !strings.Contains(code, "matchWatchDanmaku(payload.RoomId, params, identity)") {
		t.Error("typed event field should be captured by the prepared matcher")
	}

	// Should validate and decode the subscription parameter once.
	if !strings.Contains(code, `param_roomId := params.RoomId`) {
		t.Error("should validate the subscription parameter")
	}

	// Should return comparison
	if !strings.Contains(code, "ev_roomId == param_roomId") {
		t.Error("should compare decoded field with param")
	}

	// Should have event binding
	if !strings.Contains(code, `event.OnValueDecode[DanmakuSentEvent](bus, "DanmakuSent"`) {
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
	if !strings.Contains(code, `streamLiveAlerts.Dispatch(`) {
		t.Fatalf("typed stream registration missing:\n%s", code)
	}
	if !strings.Contains(code, `streamLiveAlerts.Dispatch(func(params *LiveAlertsStreamParams, identity any) (bool, error)`) ||
		!strings.Contains(code, `return matchLiveAlerts(params, identity), nil`) {
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
	if code := b.String(); !strings.Contains(code, "WriteColumnarAlert(buf, payload.Alerts, mask)") {
		t.Fatalf("model list stream encoding must be columnar:\n%s", code)
	}
}

func TestSameStreamTypeIsExact(t *testing.T) {
	base := &ast.TypeRef{Name: "Int"}
	if !sameTypeRef(base, &ast.TypeRef{Name: "Int"}) {
		t.Fatal("identical stream types should match")
	}
	if sameTypeRef(base, &ast.TypeRef{Name: "Int", Nullable: true}) || sameTypeRef(base, &ast.TypeRef{Name: "Int", IsList: true}) {
		t.Fatal("nullable and list stream payloads must not match scalar returns")
	}
}

func TestSameStreamTypeNestedShapes(t *testing.T) {
	if sameTypeRef(nil, nil) {
		t.Fatal("nil stream types must not match")
	}
	typeArg := &ast.TypeRef{Name: "Result", TypeArgs: []*ast.TypeRef{{Name: "Int"}}}
	if !sameTypeRef(typeArg, &ast.TypeRef{Name: "Result", TypeArgs: []*ast.TypeRef{{Name: "Int"}}}) {
		t.Fatal("matching type arguments must compare equal")
	}
	if sameTypeRef(typeArg, &ast.TypeRef{Name: "Result", TypeArgs: []*ast.TypeRef{{Name: "String"}}}) {
		t.Fatal("different type arguments must not compare equal")
	}
	tuple := &ast.TypeRef{Name: "Tuple", Tuple: []*ast.TypeRef{{Name: "Int"}, {Name: "String"}}}
	if !sameTypeRef(tuple, &ast.TypeRef{Name: "Tuple", Tuple: []*ast.TypeRef{{Name: "Int"}, {Name: "String"}}}) {
		t.Fatal("matching tuples must compare equal")
	}
	if sameTypeRef(tuple, &ast.TypeRef{Name: "Tuple", Tuple: []*ast.TypeRef{{Name: "Int"}, {Name: "Boolean"}}}) {
		t.Fatal("different tuples must not compare equal")
	}
}

func TestMergeGlobalStreamTypes(t *testing.T) {
	local := &ast.EventDecl{Name: "Local"}
	remote := &ast.EventDecl{Name: "Remote"}
	generator := mustNewGenerator(t, GeneratorConfig{Events: &EventContext{
		Events:      map[string]*ast.EventDecl{"Local": remote, "Remote": remote},
		ModelModule: map[string]string{"User": "accounts"},
		TypeModule:  map[string]string{"Payload": "common"},
		EnumModule:  map[string]string{"Role": "auth"},
	}})
	declarations := streamTypes{
		events: map[string]*ast.EventDecl{"Local": local},
		models: map[string]bool{}, types: map[string]bool{}, enums: map[string]bool{},
	}
	generator.mergeGlobalStreamTypes(&declarations)
	if declarations.events["Local"] != local || declarations.events["Remote"] != remote {
		t.Fatal("global events were not merged without replacing local declarations")
	}
	if !declarations.models["User"] || !declarations.types["Payload"] || !declarations.enums["Role"] {
		t.Fatalf("global stream types were not merged: %+v", declarations)
	}
}

func TestClassifyStreamPayloadKinds(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{Events: &EventContext{
		ModelModule: map[string]string{"User": "accounts"},
		TypeModule:  map[string]string{"Payload": "common"},
		EnumModule:  map[string]string{"Role": "auth"},
	}})
	models := map[string]bool{"User": true}
	types := map[string]bool{"Payload": true}
	enums := map[string]bool{"Role": true}

	var model, payload, enum streamInfo
	generator.classifyStreamPayload(&model, &ast.TypeRef{Name: "User"}, models, types, enums)
	generator.classifyStreamPayload(&payload, &ast.TypeRef{Name: "Payload"}, models, types, enums)
	generator.classifyStreamPayload(&enum, &ast.TypeRef{Name: "Role"}, models, types, enums)
	generator.classifyStreamPayload(&streamInfo{}, nil, models, types, enums)
	if model.payloadKind != streamPayloadModel || model.payloadModule != "accounts" {
		t.Fatalf("model payload = %+v", model)
	}
	if payload.payloadKind != streamPayloadType || payload.payloadModule != "common" {
		t.Fatalf("type payload = %+v", payload)
	}
	if !enum.payloadEnum || enum.payloadKind != streamPayloadScalar || enum.payloadModule != "auth" {
		t.Fatalf("enum payload = %+v", enum)
	}
}

func TestCrossModuleEnumStreamEncoding(t *testing.T) {
	g := mustNewGenerator(t, GeneratorConfig{Events: &EventContext{ModulePath: "example/service", EnumModule: map[string]string{"Role": "auth"}}})
	for _, list := range []bool{false, true} {
		si := streamInfo{apiName: "watchRole", sourceModule: "feed", isNative: true, returnType: &ast.TypeRef{Name: "Role", IsList: list}}
		g.classifyStreamPayload(&si, si.returnType, nil, nil, map[string]bool{"Role": true})
		var body, imports strings.Builder
		writeStreamResolverInterface(&body, []streamInfo{si})
		g.writeStreamImports(&imports, []streamInfo{si}, body.String())
		if !strings.Contains(imports.String(), `auth_luxo "example/service/auth/luxo"`) || !strings.Contains(body.String(), "auth_luxo.Role(") || !strings.Contains(body.String(), ".AppendLuxo(buf.B)") {
			t.Fatalf("remote enum writer missing: %s\n%s", imports.String(), body.String())
		}
		want := "auth_luxo.Role"
		if list {
			want = "[]" + want
		}
		if got := streamReturnGoType(si); got != want {
			t.Fatalf("native type = %s, want %s", got, want)
		}
		if strings.Contains(imports.String(), `"github.com/light-speak/luxo/pkg/lux/codec"`) != list {
			t.Fatal("codec import does not match generated body")
		}
	}
}

func TestGenerateCrossModuleStreamSource(t *testing.T) {
	eventDecl := &ast.EventDecl{
		Name:   "CountChanged",
		Params: []*ast.ParamDecl{{Name: "count", Type: &ast.TypeRef{Name: "Int"}}},
	}
	generator := mustNewGenerator(t, GeneratorConfig{Events: &EventContext{
		EventModule: map[string]string{"CountChanged": "common"},
		Events:      map[string]*ast.EventDecl{"CountChanged": eventDecl},
		ModelModule: map[string]string{}, TypeModule: map[string]string{}, EnumModule: map[string]string{},
		ModulePath: "github.com/test/service",
	}})
	result := &semantic.Result{Files: []*ast.File{{
		Name: "origin/consumer/watch.luxo",
		APIs: []*ast.ApiDecl{{
			Name:       "watchCount",
			ReturnType: &ast.TypeRef{Name: "Int"},
			Directives: []*ast.Directive{{Name: "stream", Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "CountChanged"}}}}},
		}},
	}}}
	code := string(generator.generateStreamFile(result, "luxo"))
	for _, want := range []string{
		`common_luxo "github.com/test/service/common/luxo"`,
		`event.OnValueDecode[common_luxo.CountChangedEvent]`,
		`func(data []byte) (common_luxo.CountChangedEvent, error)`,
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
			want: `(ev_roomId == param_roomId) && (ev_userId == api.IdentityID(identity))`,
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

	if got := streamFieldGoType(&ast.TypeRef{Name: "Unknown"}, false); got != "" {
		t.Errorf("unknown stream field type = %q", got)
	}
}

func TestStreamGenerationTypeHelpers(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		ref    *ast.TypeRef
		isEnum bool
		want   string
	}{
		{name: "enum", value: "payload.Role", ref: &ast.TypeRef{Name: "Role"}, isEnum: true, want: "string(payload.Role)"},
		{name: "duration", value: "payload.TTL", ref: &ast.TypeRef{Name: "Duration"}, want: "int64(payload.TTL)"},
		{name: "uuid", value: "payload.ID", ref: &ast.TypeRef{Name: "UUID"}, want: "[16]byte(payload.ID)"},
		{name: "plain", value: "payload.Name", ref: &ast.TypeRef{Name: "String"}, want: "payload.Name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := streamEventValue(tt.value, tt.ref, tt.isEnum); got != tt.want {
				t.Fatalf("streamEventValue() = %q, want %q", got, tt.want)
			}
		})
	}

	if got := streamReturnGoType(streamInfo{}); got != "struct{}" {
		t.Fatalf("nil return type = %q", got)
	}
	remote := streamInfo{returnType: &ast.TypeRef{Name: "User", IsList: true}, payloadKind: streamPayloadModel, payloadModule: "accounts", sourceModule: "feed"}
	if got := streamReturnGoType(remote); got != "[]*accounts_luxo.User" {
		t.Fatalf("remote list return type = %q", got)
	}
}

func TestSharedEnumStreamUsesLocalAliases(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{Events: &EventContext{
		EnumModule:        map[string]string{"Role": "auth"},
		Enums:             map[string]*ast.EnumDecl{"Role": {Name: "Role", Values: []string{"Admin"}}},
		SharedEnumPackage: "example.com/app/internal/luxotypes",
		ModulePath:        "example.com/app/service",
	}})
	expression := &ast.MemberExpr{Object: &ast.Ident{Name: "Role"}, Field: "Admin"}
	got, ok := generator.compileStreamExpr(expression, nil, map[string]bool{"Role": true}, "feed")
	if !ok || got != "string(RoleAdmin)" {
		t.Fatalf("enum expression = %q, %v", got, ok)
	}
	stream := streamInfo{sourceModule: "feed", body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: expression}}}}
	generator.classifyStreamPayload(&stream, &ast.TypeRef{Name: "Role"}, nil, nil, map[string]bool{"Role": true})
	if stream.payloadModule != "" {
		t.Fatal("shared enum payload still imports its business service")
	}
	var imports strings.Builder
	generator.writeStreamImports(&imports, []streamInfo{stream}, "")
	if strings.Contains(imports.String(), "/service/auth/") {
		t.Fatal("enum matcher imported a business service")
	}
}

func TestStreamImportsCollectRemoteTypesAndEnums(t *testing.T) {
	generator := mustNewGenerator(t, GeneratorConfig{Events: &EventContext{
		EnumModule: map[string]string{"Role": "auth"},
		ModulePath: "github.com/example/service",
	}})
	body := &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.MemberExpr{Object: &ast.Ident{Name: "Role"}, Field: "Admin"}},
		&ast.ExprStmt{Expr: &ast.MemberExpr{Object: &ast.Ident{Name: "Role"}, Field: "Member"}},
		&ast.ExprStmt{Expr: &ast.MemberExpr{Object: &ast.Ident{Name: "Unknown"}, Field: "Value"}},
		&ast.ExprStmt{Expr: &ast.MemberExpr{Object: &ast.CallExpr{Func: &ast.Ident{Name: "value"}}, Field: "Name"}},
	}}
	streams := []streamInfo{{
		eventName: "Changed", eventModule: "events", payloadKind: streamPayloadType,
		payloadModule: "common", sourceModule: "feed", body: body,
	}}
	var b strings.Builder
	generator.writeStreamImports(&b, streams, `func use() { var _ events_luxo.ChangedEvent; var _ common_luxo.Payload; _ = auth_luxo.RoleAdmin }`)
	for _, module := range []string{"auth", "common", "events"} {
		if !strings.Contains(b.String(), module+`_luxo "github.com/example/service/`+module+`/luxo"`) {
			t.Fatalf("missing %s import:\n%s", module, b.String())
		}
	}
	modules := make(map[string]bool)
	collectStreamEnumModules(body, generator.events.EnumModule, "feed", modules)
	if len(modules) != 1 || !modules["auth"] {
		t.Fatalf("enum modules = %v", modules)
	}
}

func TestWriteStreamPayloadEncodingTypeList(t *testing.T) {
	si := streamInfo{
		returnType: &ast.TypeRef{Name: "Payload", IsList: true}, payloadKind: streamPayloadType,
		payloadModule: "common", sourceModule: "feed",
	}
	var b strings.Builder
	writeStreamPayloadEncoding(&b, si, "payload.Values", "\t")
	if code := b.String(); !strings.Contains(code, "common_luxo.WriteColumnarPayload") {
		t.Fatalf("type list stream encoding is not columnar:\n%s", code)
	}
}

func TestGenerateLuxoStreamMatcherRejectsUnsupportedExpression(t *testing.T) {
	si := streamInfo{
		apiName: "watch", hasBody: true,
		body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: "unsupported"}}}}},
	}
	defer func() {
		failure, ok := recover().(*InternalCompilerError)
		if !ok || !strings.Contains(failure.Detail, "unsupported stream matcher") {
			t.Fatalf("expected compiler failure, got %v", failure)
		}
	}()
	var b strings.Builder
	generateLuxoStreamMatcher(&b, si, nil)
}

func TestCompileStreamExprRejectsUnsupportedBinaryAndQualifiesEnum(t *testing.T) {
	unsupported := &ast.BinaryExpr{
		Left: &ast.CallExpr{Func: &ast.Ident{Name: "call"}}, Op: "==", Right: &ast.Ident{Name: "value"},
	}
	if got, ok := compileStreamExpr(unsupported, nil, nil, ""); ok || got != "" {
		t.Fatalf("unsupported binary = %q, %v", got, ok)
	}
	inExpr := &ast.BinaryExpr{Left: &ast.Ident{Name: "value"}, Op: "in", Right: &ast.Ident{Name: "values"}}
	if got, ok := compileStreamExpr(inExpr, nil, nil, ""); ok || got != "" {
		t.Fatalf("in expression = %q, %v", got, ok)
	}

	generator := mustNewGenerator(t, GeneratorConfig{Events: &EventContext{EnumModule: map[string]string{"Role": "auth"}}})
	enumExpr := &ast.MemberExpr{Object: &ast.Ident{Name: "Role"}, Field: "Admin"}
	if got, ok := generator.compileStreamExpr(enumExpr, nil, map[string]bool{"Role": true}, "feed"); !ok || got != "string(auth_luxo.RoleAdmin)" {
		t.Fatalf("enum expression = %q, %v", got, ok)
	}
}

func TestCompileStreamDurationLiteral(t *testing.T) {
	for literal, want := range map[string]string{
		"1ms": "int64(time.Millisecond)", "5m": "int64(5 * time.Minute)",
		"1s": "int64(time.Second)", "1h": "int64(time.Hour)",
		"1d": "int64(24 * time.Hour)", "2w": "",
	} {
		got, ok := compileStreamExpr(&ast.Literal{Kind: token.Duration, Value: literal}, nil, nil, "")
		if got != want || ok != (want != "") {
			t.Fatalf("duration %q = %q, %v; want %q", literal, got, ok, want)
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
	// A parsed return statement must not bypass the matcher expression boundary.
	input := analyzePermissionFixture(t, `event Changed(value: Int)
api watch: Int @stream(Changed) { return true }`)
	if len(input.Errors) == 0 || !strings.Contains(input.Errors[0].Message, "matcher body must contain exactly one boolean expression") {
		t.Fatalf("return matcher did not retain its semantic diagnostic: %v", input.Errors)
	}
	body := input.Files[0].APIs[0].Body
	if _, ok := body.Stmts[0].(*ast.ReturnStmt); !ok {
		t.Fatal("parser did not produce the intended return statement")
	}
	if extractMatcherExpr(body) != nil {
		t.Fatal("return statement was accepted as a matcher expression")
	}
	if generated, err := defaultGenerator().Generate(input, "generated", nil); err == nil || generated != nil {
		t.Fatalf("invalid matcher emitted sources: generated=%v, error=%v", generated != nil, err)
	}
}

func TestGenerateLuxoStreamMatcher_EmptyBody(t *testing.T) {
	var b strings.Builder
	si := streamInfo{
		apiName: "watchEmpty",
		hasBody: true,
		body:    &ast.Block{}, // no stmts
	}
	defer func() {
		failure, ok := recover().(*InternalCompilerError)
		if !ok || !strings.Contains(failure.Detail, "invalid stream matcher") {
			t.Fatalf("expected compiler failure, got %v", failure)
		}
	}()
	generateLuxoStreamMatcher(&b, si, nil)
}

func TestGenerateLuxoStreamMatcher_UnknownItField(t *testing.T) {
	// The generator must reject an unknown field before emitting invalid Go.
	generator := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{EventFields: map[string]map[string]int{
		"TestEvent": {"knownField": 1},
	}}})

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
	defer func() {
		failure, ok := recover().(*InternalCompilerError)
		if !ok || !strings.Contains(failure.Detail, "unknown stream matcher field") {
			t.Fatalf("expected compiler failure, got %v", failure)
		}
	}()
	generator.generateLuxoStreamMatcher(&b, si, nil)
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

func TestCompiledStreamConditionPreservesGrouping(t *testing.T) {
	compare := func(op, value string) ast.Expr {
		return &ast.BinaryExpr{Left: &ast.Ident{Name: "id"}, Op: op, Right: &ast.Literal{Kind: token.Int, Value: value}}
	}
	grouped := &ast.BinaryExpr{Left: &ast.BinaryExpr{Left: compare("==", "0"), Op: "||", Right: compare("==", "1")}, Op: "&&", Right: compare("!=", "0")}
	params := map[string]*ast.ParamDecl{"id": {Name: "id", Type: &ast.TypeRef{Name: "Int"}}}
	positive, ok := compileStreamExpr(grouped, params, nil, "")
	if !ok {
		t.Fatal("condition rejected")
	}
	negative, ok := compileStreamExpr(&ast.UnaryExpr{Op: "!", Value: grouped}, params, nil, "")
	if !ok {
		t.Fatal("negation rejected")
	}
	runGeneratedProtocolPackage(t, "package generated\nfunc allowed(param_id int64)bool{return "+positive+"}\nfunc denied(param_id int64)bool{return "+negative+"}\n", `package generated
import "testing"
func TestGroupedCondition(t *testing.T){for _,id:=range []int64{0,1,2}{want:=id==1;if allowed(id)!=want||denied(id)==want{t.Fatalf("id=%d allowed=%t denied=%t",id,allowed(id),denied(id))}}}
`)
}
