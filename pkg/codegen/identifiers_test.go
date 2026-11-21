package codegen

import (
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lockfile"
)

func TestReceiverName(t *testing.T) {
	for name, want := range map[string]string{
		"": "", "_Record": "_luxoReceiver", "_": "_luxoReceiver", "User": "u", "value": "v", "Équipe": "é", "用户": "用", "𐐀Model": "𐐨",
	} {
		if got := receiverName(name); got != want {
			t.Errorf("receiverName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestGeneratorRejectsAmbiguousIdentifiers(t *testing.T) {
	for name, ids := range map[string]StableIDs{
		"duplicate APIs":         {APIs: map[string]int{"a": 1, "b": 1}},
		"zero API":               {APIs: map[string]int{"a": 0}},
		"negative API":           {APIs: map[string]int{"a": -1}},
		"empty API":              {APIs: map[string]int{"": 1}},
		"duplicate fields":       {ModelFields: map[string]map[string]int{"M": {"a": 1, "b": 1}}},
		"empty model":            {ModelFields: map[string]map[string]int{"": {"a": 1}}},
		"empty parameter owner":  {APIParams: map[string]map[string]int{"": {"x": 1}}},
		"duplicate event fields": {EventFields: map[string]map[string]int{"E": {"a": 1, "b": 1}}},
		"empty event field":      {EventFields: map[string]map[string]int{"E": {"": 1}}},
		"duplicate params":       {APIParams: map[string]map[string]int{"a": {"x": 1, "y": 1}}},
		"reserved param":         {APIParams: map[string]map[string]int{"a": {"x": (1 << 31) - 2}}},
		"oversized model field":  {ModelFields: map[string]map[string]int{"M": {"x": 65536}}},
		"oversized event field":  {EventFields: map[string]map[string]int{"E": {"x": 1 << 31}}},
		"oversized API":          {APIs: map[string]int{"a": 1 << 31}},
	} {
		t.Run(name, func(t *testing.T) {
			g, err := NewGenerator(GeneratorConfig{IDs: ids})
			if err == nil || g != nil {
				t.Fatalf("invalid identifiers accepted: %v %v", g, err)
			}
		})
	}
}

func TestRegisteredAPIRequiresEveryActiveParameterID(t *testing.T) {
	input := result(&ast.File{APIs: []*ast.ApiDecl{{
		Name: "echo", ReturnType: &ast.TypeRef{Name: "Int"},
		Directives: []*ast.Directive{{Name: "native"}},
		Params:     []*ast.ParamDecl{{Name: "value", Type: &ast.TypeRef{Name: "Int"}}},
	}}})
	for _, params := range []map[string]map[string]int{nil, {"echo": {}}, {"echo": {"retired": 1}}} {
		g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{
			APIs: map[string]int{"echo": 1}, APIParams: params,
		}})
		generated, err := g.Generate(input, "generated", nil)
		if err == nil || generated != nil || !strings.Contains(err.Error(), "echo.value") {
			t.Errorf("handler silently omitted active parameter: %v", err)
		}
		data, err := g.BuildSchemaJSON(input, nil)
		if err == nil || data != nil || !strings.Contains(err.Error(), "echo.value") {
			t.Errorf("SDK schema accepted incomplete parameter IDs: %v", err)
		}
	}
}

func TestGeneratorIdentifierSpacesRemainIndependent(t *testing.T) {
	g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{
		APIs:        map[string]int{"a": 1, "b": 2},
		APIParams:   map[string]map[string]int{"a": {"x": 1}, "b": {"x": 1, "last": lockfile.MaxParameterID}},
		ModelFields: map[string]map[string]int{"M": {"x": 1}, "T": {"x": 1}},
		EventFields: map[string]map[string]int{"E": {"x": 1}, "Empty": {}, "EmptyNil": nil},
	}})
	if g.apiParamID("a", "x") != g.modelFieldID("M", "x") {
		t.Fatal("independent spaces were renumbered")
	}
}

func TestGeneratorAcceptsClassifiedIdentifierMaxima(t *testing.T) {
	g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{
		APIs:        map[string]int{"last": 2147483647},
		APIParams:   map[string]map[string]int{"last": {"value": 2147483645}},
		ModelFields: map[string]map[string]int{"M": {"value": 65535}},
		EventFields: map[string]map[string]int{"E": {"value": 2147483647}},
	}})
	if g.apiID("last") != 2147483647 || g.modelFieldID("M", "value") != 65535 || g.eventFieldID("E", "value") != 2147483647 || g.apiParamID("last", "value") != 2147483645 {
		t.Fatal("valid maximum identifiers were changed")
	}
}

func TestSynthesizedAPIParametersRequireStableIDs(t *testing.T) {
	model := &ast.ModelDecl{Name: "User", Directives: []*ast.Directive{{Name: "crud"}}, Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}}},
	}}
	input := result(&ast.File{Name: "origin/user.luxo", Models: []*ast.ModelDecl{model}, Functions: []*ast.FnDecl{{
		Name: "score", Directives: []*ast.Directive{{Name: "service"}, {Name: "native"}},
		Params: []*ast.ParamDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}, ReturnType: &ast.TypeRef{Name: "Int"},
	}}})
	for _, test := range []struct {
		api     string
		params  map[string]int
		missing string
	}{
		{"getUser", nil, "id"},
		{"listUsers", map[string]int{"page": 1}, "pageSize"},
		{"svc:batchLoad:User", nil, "keys"},
		{"svc:score", nil, "id"},
	} {
		t.Run(test.api, func(t *testing.T) {
			ids := completeFixtureAPIIDs(input, StableIDs{})
			ids.APIParams[test.api] = test.params
			g := mustNewGenerator(t, GeneratorConfig{IDs: ids})
			want := test.api + "." + test.missing
			if generated, err := g.Generate(input, "generated", nil); err == nil || generated != nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("missing generated parameter accepted: %v", err)
			}
			if data, err := g.BuildSchemaJSON(input, nil); err == nil || data != nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("missing SDK parameter accepted: %v", err)
			}
			g.ids.APIParams[test.api] = map[string]int{test.missing: 2}
			for name, id := range test.params {
				g.ids.APIParams[test.api][name] = id
			}
			if err := g.validateRegisteredParameters(input, nil); err != nil {
				t.Fatalf("complete registration rejected: %v", err)
			}
		})
	}
}

func TestGeneratorRejectsCollidingGoFields(t *testing.T) {
	for _, kind := range []string{"model", "type", "extend"} {
		t.Run(kind, func(t *testing.T) {
			fields := []*ast.FieldDecl{{Name: "name", Type: &ast.TypeRef{Name: "String"}}, {Name: "Name", Type: &ast.TypeRef{Name: "String"}}}
			file := &ast.File{}
			switch kind {
			case "model":
				file.Models = []*ast.ModelDecl{{Name: "Entry", Fields: fields}}
			case "type":
				file.Types = []*ast.TypeDecl{{Name: "Entry", Fields: fields}}
			case "extend":
				file.Extends = []*ast.ExtendDecl{{Name: "Entry", Fields: fields}}
			}
			g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"Entry": {"id": 1, "name": 2, "Name": 3}}}})
			generated, err := g.Generate(result(file), "generated", nil)
			if err == nil || generated != nil {
				t.Fatalf("colliding Go fields accepted: error=%v", err)
			}
			if data, schemaErr := g.BuildSchemaJSON(result(file), nil); schemaErr == nil || data != nil {
				t.Fatalf("SDK schema export accepted colliding Go fields: %v", schemaErr)
			}
			for _, want := range []string{"Entry", "name", "Name", "Go field"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("missing %q in %v", want, err)
				}
			}
		})
	}
}

func TestDeclaredGoFieldNamesRemainDistinct(t *testing.T) {
	g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"Entry": {"userID": 1, "userId": 2}}}})
	fields := []*ast.FieldDecl{{Name: "userID", Type: &ast.TypeRef{Name: "Int"}}, {Name: "userId", Type: &ast.TypeRef{Name: "Int"}}}
	if err := g.validateDeclaredFields("Entry", fields, &fieldNameValidation{}); err != nil {
		t.Fatalf("distinct Go names rejected: %v", err)
	}
}

func TestGeneratorRejectsCollidingDatabaseColumns(t *testing.T) {
	for _, kind := range []string{"model", "extend"} {
		t.Run(kind, func(t *testing.T) {
			fields := []*ast.FieldDecl{{Name: "userID", Type: &ast.TypeRef{Name: "Int"}}, {Name: "userId", Type: &ast.TypeRef{Name: "Int"}}}
			file := &ast.File{}
			if kind == "model" {
				file.Models = []*ast.ModelDecl{{Name: "Entry", Fields: fields}}
			} else {
				file.Extends = []*ast.ExtendDecl{{Name: "Entry", Fields: fields}}
			}
			g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"Entry": {"id": 1, "userID": 2, "userId": 3}}}})
			if output, err := g.Generate(result(file), "generated", nil); err == nil || output != nil || !strings.Contains(err.Error(), "user_id") {
				t.Fatalf("ambiguous database columns accepted: %v", err)
			}
			if output, err := g.BuildSchemaJSON(result(file), nil); err == nil || output != nil {
				t.Fatalf("ambiguous database Schema accepted: %v", err)
			}
		})
	}
}

func TestDatabaseColumnNamesUsePersistedFields(t *testing.T) {
	for _, sample := range []struct {
		name      string
		field     *ast.FieldDecl
		wantError bool
	}{
		{"scalar", &ast.FieldDecl{Name: "userId", Type: &ast.TypeRef{Name: "Int"}}, true},
		{"enum", &ast.FieldDecl{Name: "userId", Type: &ast.TypeRef{Name: "Status"}}, true},
		{"relation", &ast.FieldDecl{Name: "userId", Type: &ast.TypeRef{Name: "Owner"}}, false},
		{"relation list", &ast.FieldDecl{Name: "userId", Type: &ast.TypeRef{Name: "Owner", IsList: true}}, false},
		{"computed", &ast.FieldDecl{Name: "userId", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{}}, false},
		{"missing type", &ast.FieldDecl{Name: "userId"}, false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			fields := []*ast.FieldDecl{{Name: "userID", Type: &ast.TypeRef{Name: "Int"}}, sample.field}
			err := (&fieldNameValidation{}).validateDatabaseColumnNames("Entry", fields, map[string]bool{"Status": true})
			if (err != nil) != sample.wantError {
				t.Fatalf("column collision error=%v, want %v", err, sample.wantError)
			}
		})
	}
}

func TestPlainTypeDoesNotRequireUniqueDatabaseColumns(t *testing.T) {
	fields := []*ast.FieldDecl{{Name: "userID", Type: &ast.TypeRef{Name: "Int"}}, {Name: "userId", Type: &ast.TypeRef{Name: "Int"}}}
	input := result(&ast.File{Types: []*ast.TypeDecl{{Name: "Entry", Fields: fields}}})
	g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"Entry": {"userID": 1, "userId": 2}}}})
	if output, err := g.Generate(input, "generated", nil); err != nil || output == nil {
		t.Fatalf("plain type rejected: %v", err)
	}
	if output, err := g.BuildSchemaJSON(input, nil); err != nil || output == nil {
		t.Fatalf("plain type Schema rejected: %v", err)
	}
}

func TestFieldNameIndexesResetBetweenDeclarations(t *testing.T) {
	for _, pair := range [][2]string{{"name", "Name"}, {"userID", "userId"}, {"étiquette", "Étiquette"}} {
		fields := []*ast.FieldDecl{{Name: pair[0], Type: &ast.TypeRef{Name: "Int"}}}
		first := &ast.ModelDecl{Name: "First", Fields: fields}
		second := &ast.ModelDecl{Name: "Second", Fields: fields}
		input := result(&ast.File{Models: []*ast.ModelDecl{first}}, &ast.File{Models: []*ast.ModelDecl{second}})
		g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{
			"First": {pair[0]: 1}, "Second": {pair[0]: 1, pair[1]: 2},
		}}})
		if err := g.validateFieldIdentifiers(input, nil); err != nil {
			t.Fatal(err)
		}
		second.Fields = append([]*ast.FieldDecl{fields[0]}, &ast.FieldDecl{Name: pair[1], Type: &ast.TypeRef{Name: "Int"}})
		if err := g.validateFieldIdentifiers(input, nil); err == nil || !strings.Contains(err.Error(), "Second") {
			t.Fatalf("second declaration collision lost: %v", err)
		}
	}
}

func TestDatabaseFieldColumnName(t *testing.T) {
	for source, want := range map[string]string{"": "", "user_id": "user_id", "field12": "field12", "userID": "user_id", "HTMLParser": "html_parser", "Étiquette": "étiquette", "ǅName": "ǆname", "字段": "字段"} {
		if got := databaseFieldColumnName(source); got != want {
			t.Errorf("%q => %q, want %q", source, got, want)
		}
	}
}

func TestExternalKeyRejectsGeneratedNameCollision(t *testing.T) {
	for _, sample := range []struct{ key, field, want string }{
		{"id", "Id", "Go field"}, {"userID", "userId", "database column"},
		{"writeLuxo", "title", "generated protocol method"}, {"readLuxoInput", "title", "generated protocol method"},
	} {
		t.Run(sample.field, func(t *testing.T) {
			input := result(&ast.File{Extends: []*ast.ExtendDecl{{Name: "Entry", Fields: []*ast.FieldDecl{{Name: sample.field, Type: &ast.TypeRef{Name: "Int"}}}}}})
			g := mustNewGenerator(t, GeneratorConfig{Events: &EventContext{ModelIDField: map[string]string{"Entry": sample.key}}, IDs: StableIDs{ModelFields: map[string]map[string]int{"Entry": {sample.key: 1, sample.field: 2}}}})
			if output, err := g.Generate(input, "generated", nil); err == nil || output != nil || !strings.Contains(err.Error(), sample.want) {
				t.Fatalf("injected key collision accepted: %v", err)
			}
			if output, err := g.BuildSchemaJSON(input, nil); err == nil || output != nil || !strings.Contains(err.Error(), sample.want) {
				t.Fatalf("injected key collision exported: %v", err)
			}
		})
	}
}

func TestExternalKeyNameControls(t *testing.T) {
	for _, sample := range []struct {
		name      string
		field     *ast.FieldDecl
		wantError bool
	}{
		{"declared key", &ast.FieldDecl{Name: "userID", Type: &ast.TypeRef{Name: "Int"}}, false},
		{"unrelated field", &ast.FieldDecl{Name: "title", Type: &ast.TypeRef{Name: "String"}}, false},
		{"relation", &ast.FieldDecl{Name: "userId", Type: &ast.TypeRef{Name: "Owner"}}, false},
		{"computed", &ast.FieldDecl{Name: "userId", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{}}, false},
		{"missing type", &ast.FieldDecl{Name: "userId"}, false},
		{"enum column", &ast.FieldDecl{Name: "userId", Type: &ast.TypeRef{Name: "Status"}}, true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			extension := &ast.ExtendDecl{Name: "Entry", Fields: []*ast.FieldDecl{sample.field}}
			err := validateExternalKeyNames(extension, "userID", map[string]bool{"Status": true})
			if (err != nil) != sample.wantError {
				t.Fatalf("key collision=%v want=%v", err, sample.wantError)
			}
		})
	}
}

func TestSoftDeleteFieldContract(t *testing.T) {
	for _, sample := range []struct {
		name  string
		field *ast.FieldDecl
		valid bool
	}{
		{"implicit", nil, true},
		{"explicit nullable timestamp", &ast.FieldDecl{Name: "deletedAt", Type: &ast.TypeRef{Name: "DateTime", Nullable: true}}, true},
		{"Go collision", &ast.FieldDecl{Name: "DeletedAt", Type: &ast.TypeRef{Name: "DateTime", Nullable: true}}, false},
		{"column collision", &ast.FieldDecl{Name: "deleted_at", Type: &ast.TypeRef{Name: "DateTime", Nullable: true}}, false},
		{"missing type", &ast.FieldDecl{Name: "deletedAt"}, false},
		{"string", &ast.FieldDecl{Name: "deletedAt", Type: &ast.TypeRef{Name: "String", Nullable: true}}, false},
		{"nonnullable", &ast.FieldDecl{Name: "deletedAt", Type: &ast.TypeRef{Name: "DateTime"}}, false},
		{"list", &ast.FieldDecl{Name: "deletedAt", Type: &ast.TypeRef{Name: "DateTime", Nullable: true, IsList: true}}, false},
		{"computed", &ast.FieldDecl{Name: "deletedAt", Type: &ast.TypeRef{Name: "DateTime", Nullable: true}, Computed: &ast.ComputedField{}}, false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			fields := []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}}}}
			ids := map[string]int{"id": 1}
			if sample.field != nil {
				fields = append(fields, sample.field)
				ids[sample.field.Name] = 2
			}
			input := result(&ast.File{Models: []*ast.ModelDecl{{Name: "Entry", Fields: fields, Directives: []*ast.Directive{{Name: "soft"}}}}})
			g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"Entry": ids}}})
			output, err := g.Generate(input, "generated", nil)
			if sample.valid {
				if err != nil || output == nil {
					t.Fatalf("valid soft model rejected: %v", err)
				}
			} else if err == nil || output != nil {
				t.Fatalf("invalid soft model accepted: %v", err)
			}
			data, err := g.BuildSchemaJSON(input, nil)
			if sample.valid {
				if err != nil || data == nil {
					t.Fatal(err)
				}
			} else if err == nil || data != nil {
				t.Fatalf("invalid soft schema exported: %v", err)
			}
		})
	}
}

func TestConditionalModelMethodFieldCollision(t *testing.T) {
	for _, method := range []string{"CreateToken", "CreateTokenWithExpiry", "CreateRefreshToken", "RefreshToken", "AppendClaimsJSON", "VerifySecret"} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/enabled=%t", method, enabled), func(t *testing.T) {
				field := strings.ToLower(method[:1]) + method[1:]
				model := &ast.ModelDecl{Name: "Entry", Fields: []*ast.FieldDecl{
					{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}}},
					{Name: "secret", Type: &ast.TypeRef{Name: "String"}},
					{Name: field, Type: &ast.TypeRef{Name: "String"}},
				}}
				if enabled {
					if method == "VerifySecret" {
						model.Fields[1].Directives = []*ast.Directive{{Name: "hash"}}
					} else {
						model.Directives = []*ast.Directive{{Name: "withAuth", Args: []*ast.NamedArg{{Name: "stores", Value: &ast.ListExpr{Items: []ast.Expr{&ast.Ident{Name: "id"}}}}}}}
					}
				}
				input := result(&ast.File{Models: []*ast.ModelDecl{model}})
				g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{ModelFields: map[string]map[string]int{"Entry": {"id": 1, "secret": 2, field: 3}}}})
				output, err := g.Generate(input, "generated", nil)
				if (err != nil) != enabled || (output == nil) != enabled {
					t.Fatalf("conditional method conflict: %v", err)
				}
				if enabled && !strings.Contains(err.Error(), "generated method") {
					t.Fatal(err)
				}
				schema, err := g.BuildSchemaJSON(input, nil)
				if (err != nil) != enabled || (schema == nil) != enabled {
					t.Fatalf("conditional method schema: %v", err)
				}
			})
		}
	}
}

func TestExtendHashMethodFieldCollision(t *testing.T) {
	for _, injected := range []bool{false, true} {
		t.Run(fmt.Sprint(injected), func(t *testing.T) {
			fields := []*ast.FieldDecl{{Name: "secret", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hash"}}}}
			key := "id"
			if injected {
				key = "verifySecret"
			} else {
				fields = append(fields, &ast.FieldDecl{Name: "verifySecret", Type: &ast.TypeRef{Name: "String"}})
			}
			g := mustNewGenerator(t, GeneratorConfig{Events: &EventContext{ModelIDField: map[string]string{"Entry": key}}, IDs: StableIDs{ModelFields: map[string]map[string]int{"Entry": {"id": 1, "secret": 2, "verifySecret": 3}}}})
			input := result(&ast.File{Extends: []*ast.ExtendDecl{{Name: "Entry", Fields: fields}}})
			output, err := g.Generate(input, "generated", nil)
			if err == nil || output != nil || !strings.Contains(err.Error(), "generated method") {
				t.Fatalf("extend hash collision accepted: %v", err)
			}
			schema, err := g.BuildSchemaJSON(input, nil)
			if err == nil || schema != nil || !strings.Contains(err.Error(), "generated method") {
				t.Fatalf("extend hash collision exported: %v", err)
			}
		})
	}
}

func TestEventFieldIndexesReset(t *testing.T) {
	fields := []*ast.ParamDecl{{Name: "value", Type: &ast.TypeRef{Name: "Int"}}}
	input := result(&ast.File{Events: []*ast.EventDecl{{Name: "First", Params: fields}, {Name: "Second", Params: fields}}})
	g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{EventFields: map[string]map[string]int{"First": {"value": 1}, "Second": {"value": 1}}}})
	if _, err := g.Generate(input, "generated", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := g.BuildSchemaJSON(input, nil); err != nil {
		t.Fatal(err)
	}
}

func TestHashMethodNameNormalization(t *testing.T) {
	for _, sample := range []struct {
		field, collision string
		rejected         bool
	}{
		{"école", "verifyÉcole", true}, {"école", "verifyécole", false},
		{strings.Repeat("secret", 20), "verifyS" + strings.Repeat("secret", 20)[1:], true},
	} {
		t.Run(sample.collision, func(t *testing.T) {
			fields := []*ast.FieldDecl{{Name: sample.field, Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hash"}}}}
			names := map[goFieldName]string{normalizedGoFieldName(sample.collision): sample.collision}
			if err := validateHashMethodNames("Entry", fields, names, ""); (err != nil) != sample.rejected {
				t.Fatalf("declared collision: %v", err)
			}
			if err := validateHashMethodNames("Entry", fields, nil, sample.collision); (err != nil) != sample.rejected {
				t.Fatalf("injected collision: %v", err)
			}
		})
	}
}

func TestGeneratorRejectsAppMemberCollisions(t *testing.T) {
	for _, name := range []string{"DB", "Queue", "Close", "Tx", "Resolver", "EventBus", "EventHandlers"} {
		t.Run(name, func(t *testing.T) {
			file := &ast.File{Models: []*ast.ModelDecl{{Name: name, Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}}}}}}}
			switch name {
			case "Resolver":
				file.Functions = []*ast.FnDecl{{Name: "compute", ReturnType: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "native"}}}}
			case "EventBus", "EventHandlers":
				file.Events = []*ast.EventDecl{{Name: "Changed"}}
				file.Listeners = []*ast.OnDecl{{EventName: "Changed", Native: true}}
			}
			input := result(file)
			ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{name: {"id": 1}}})
			g := mustNewGenerator(t, GeneratorConfig{IDs: ids})
			generated, err := g.Generate(input, "generated", nil)
			if err == nil || generated != nil || !strings.Contains(err.Error(), "App") || !strings.Contains(err.Error(), name) {
				t.Fatalf("App member collision accepted: %v", err)
			}
			if data, err := g.BuildSchemaJSON(input, nil); err == nil || data != nil {
				t.Fatalf("invalid application exported: %v", err)
			}
		})
	}
}

func TestConditionalAppMembers(t *testing.T) {
	g := defaultGenerator()
	for _, name := range []string{"Resolver", "EventBus", "EventHandlers", "SetLoaders", "loaders", "Entry"} {
		if g.conflictsWithAppMember(name, result(&ast.File{}), nil) {
			t.Fatalf("absent App member reserves %q", name)
		}
	}
	stream := result(&ast.File{APIs: []*ast.ApiDecl{{Name: "watch", Directives: []*ast.Directive{{Name: "native"}, {Name: "stream"}}}}})
	if !g.conflictsWithAppMember("Resolver", stream, nil) {
		t.Fatal("native stream resolver collision accepted")
	}
	withLoader := result(&ast.File{Models: []*ast.ModelDecl{{Name: "Entry", Fields: []*ast.FieldDecl{{Name: "owner", Type: &ast.TypeRef{Name: "Owner"}}}}, {Name: "Owner"}}})
	for _, name := range []string{"SetLoaders", "loaders"} {
		if !g.conflictsWithAppMember(name, withLoader, nil) {
			t.Fatalf("generated loader member %q not checked", name)
		}
	}
}

func TestGeneratedUnusedAppMemberNames(t *testing.T) {
	runGeneratedFixture(t, `
model Resolver @noTime { id: Int @id }
model EventBus @noTime { id: Int @id }
model EventHandlers @noTime { id: Int @id }
model SetLoaders @noTime { id: Int @id }
`, `package generated
import "testing"
func TestClients(t *testing.T) {
 app := NewFromDB(nil)
 if app.Resolver == nil || app.EventBus == nil || app.EventHandlers == nil || app.SetLoaders == nil { t.Fatal("missing model client") }
}
`, StableIDs{ModelFields: map[string]map[string]int{
		"Resolver": {"id": 1}, "EventBus": {"id": 1}, "EventHandlers": {"id": 1}, "SetLoaders": {"id": 1},
	}})
}
