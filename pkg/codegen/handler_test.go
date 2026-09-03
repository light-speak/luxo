package codegen

import (
	"go/format"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestGenerateCRUDHandlersUseDeclaredPrimaryKey(t *testing.T) {
	model := &ast.ModelDecl{
		Name: "Product",
		Fields: []*ast.FieldDecl{
			{Name: "sku", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "id"}}},
			{Name: "name", Type: &ast.TypeRef{Name: "String"}},
		},
		Directives: []*ast.Directive{{Name: "crud"}},
	}
	var b strings.Builder
	generateCRUDHandlers(&b, []*ast.ModelDecl{model}, map[string]bool{}, nil)
	code := b.String()
	if strings.Contains(code, "ProductWhere.Id") {
		t.Fatalf("custom primary key must not use synthetic Id field:\n%s", code)
	}
	for _, want := range []string{"ProductWhere.Sku.Eq(id)", "ProductWhere.Sku.In(ids...)"} {
		if !strings.Contains(code, want) {
			t.Fatalf("custom primary key output missing %q:\n%s", want, code)
		}
	}
	if !strings.Contains(code, "cols := selectProductSQLColumns(req.Select)") {
		t.Fatalf("custom primary key selection must use the generated model selector:\n%s", code)
	}
	if strings.Contains(code, "selection.SQLColumns(req.Select)") {
		t.Fatalf("generic selection hardcodes id and must not be used for CRUD:\n%s", code)
	}
}

func TestGenerateSQLColumnSelectorUsesDeclaredDatabaseFields(t *testing.T) {
	model := &ast.ModelDecl{
		Name: "Product",
		Fields: []*ast.FieldDecl{
			{Name: "sku", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "id"}}},
			{Name: "displayName", Type: &ast.TypeRef{Name: "String"}},
			{Name: "secret", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hidden"}}},
			{Name: "reviewCount", Type: &ast.TypeRef{Name: "Int"}, Computed: &ast.ComputedField{}},
			{Name: "reviews", Type: &ast.TypeRef{Name: "Review", IsList: true}},
		},
	}

	var b strings.Builder
	generateSQLColumnSelector(&b, model, nil)
	code := b.String()
	for _, want := range []string{
		"func selectProductSQLColumns(fields []*selection.Field) []string",
		`case "sku":`,
		`cols = ensureSelectedColumn(cols, "sku")`,
		`case "displayName":`,
		`cols = ensureSelectedColumn(cols, "display_name")`,
		`if !hasPrimaryKey { cols = append(cols, "sku") }`,
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("model SQL selector missing %q:\n%s", want, code)
		}
	}
	for _, excluded := range []string{`case "secret":`, `case "reviewCount":`, `case "reviews":`, `"id"`} {
		if strings.Contains(code, excluded) {
			t.Fatalf("model SQL selector contains non-database field %q:\n%s", excluded, code)
		}
	}
}

func TestCollectSelectionModelsIncludesExternalExtensionOnce(t *testing.T) {
	user := &ast.ModelDecl{Name: "User"}
	result := &semantic.Result{Files: []*ast.File{{Extends: []*ast.ExtendDecl{
		{Name: "User"},
		{Name: "Post", Fields: []*ast.FieldDecl{{Name: "title", Type: &ast.TypeRef{Name: "String"}}}},
	}}}}
	models := collectSelectionModels(result, []*ast.ModelDecl{user})
	if len(models) != 2 || models[0] != user || models[1].Name != "Post" {
		t.Fatalf("selection models = %#v", models)
	}
}

func crudDirective(args ...*ast.NamedArg) *ast.Directive {
	return &ast.Directive{
		Pos:  token.Position{File: "test.luxo", Line: 1, Col: 1},
		Name: "crud",
		Args: args,
	}
}

func TestGenerateBatchLoadHandlersUsesCanonicalListAndSelection(t *testing.T) {
	oldAPIs := apiIDs
	oldFields := modelFieldIDs
	defer func() {
		apiIDs = oldAPIs
		modelFieldIDs = oldFields
	}()
	apiIDs = map[string]int{"svc:batchLoad:User": 42}
	modelFieldIDs = map[string]map[string]int{"User": {"id": 1, "name": 2, "posts": 3}}

	model := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "name", Type: &ast.TypeRef{Name: "String"}},
			{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
			{Name: "displayName", Type: &ast.TypeRef{Name: "String"}, Computed: &ast.ComputedField{}},
		},
	}
	var b strings.Builder
	generateBatchLoadHandlers(&b, []*ast.ModelDecl{model})
	code := b.String()

	if !strings.Contains(code, `ParamIntArray("keys")`) {
		t.Errorf("batch handler must decode the canonical list param:\n%s", code)
	}
	if !strings.Contains(code, `Name: "keys", Type: "Int", IsList: true`) {
		t.Errorf("batch handler metadata must describe a list param:\n%s", code)
	}
	if !strings.Contains(code, `codec.FieldMaskHas(fieldMask, 2)`) || !strings.Contains(code, `fields = append(fields, "name")`) {
		t.Errorf("batch handler must push the wire selection into SQL:\n%s", code)
	}
	if strings.Contains(code, `fields = append(fields, "posts")`) {
		t.Errorf("batch handler must not select relation names as SQL columns:\n%s", code)
	}
}

func TestGenerateBatchLoadHandlersSupportsUUID(t *testing.T) {
	oldAPIs := apiIDs
	defer func() { apiIDs = oldAPIs }()
	apiIDs = map[string]int{"svc:batchLoad:Account": 45}
	model := &ast.ModelDecl{
		Name: "Account",
		Fields: []*ast.FieldDecl{{
			Name: "id",
			Type: &ast.TypeRef{Name: "UUID"},
		}},
	}

	var b strings.Builder
	generateBatchLoadHandlers(&b, []*ast.ModelDecl{model})
	code := b.String()
	for _, check := range []string{
		`ParamUUIDArray("keys")`,
		`lux.NewUUIDField("id").In(keys...)`,
		`Name: "keys", Type: "UUID", IsList: true`,
	} {
		if !strings.Contains(code, check) {
			t.Errorf("UUID batch handler missing %q:\n%s", check, code)
		}
	}
}

func TestGenerateRemoteNamedLoadHandlers(t *testing.T) {
	oldContext := globalEventCtx
	oldAPIs := apiIDs
	oldFields := modelFieldIDs
	defer func() {
		globalEventCtx = oldContext
		apiIDs = oldAPIs
		modelFieldIDs = oldFields
	}()
	globalEventCtx = &EventContext{remoteLoadCalls: map[string][]loadCallInfo{
		"user": {{
			modelName:    "User",
			argNames:     []string{"tenantId", "email"},
			argTypes:     []string{"int64", "string"},
			argTypeNames: []string{"Int", "String"},
		}},
	}}
	apiIDs = map[string]int{"svc:load:User:tenantId:email": 72}
	apiParamIDs = map[string]map[string]int{
		"svc:load:User:tenantId:email": {"tenantId": 4, "email": 7},
	}
	modelFieldIDs = map[string]map[string]int{"User": {"id": 1, "tenantId": 2, "email": 3, "name": 4}}

	user := &ast.ModelDecl{
		Name: "User",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "tenantId", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "email", Type: &ast.TypeRef{Name: "String"}},
			{Name: "name", Type: &ast.TypeRef{Name: "String"}},
		},
	}
	result := &semantic.Result{Files: []*ast.File{{Name: "origin/user.luxo", Models: []*ast.ModelDecl{user}}}}
	code := string(generateHandlerFile(result, "luxo", nil))
	if _, err := format.Source([]byte(code)); err != nil {
		t.Fatalf("remote load handler generated invalid Go: %v\n%s", err, code)
	}
	checks := []string{
		"handleLoadUserByTenantIdAndEmail",
		`ParamIntArray("tenantId")`,
		`ParamStringArray("email")`,
		`fields = append(fields, "tenant_id")`,
		`fields = append(fields, "email")`,
		"groups[i] = lux.AllOf(",
		"lux.AnyOf(groups...)",
		`router.Handle("svc:load:User:tenantId:email"`,
		`FieldID: 4, Name: "tenantId", Type: "Int", IsList: true`,
		`FieldID: 7, Name: "email", Type: "String", IsList: true`,
	}
	for _, check := range checks {
		if !strings.Contains(code, check) {
			t.Errorf("remote load handler missing %q:\n%s", check, code)
		}
	}
}

func TestGenerateRemoteNamedLoadHandlersSkipsUnknownModel(t *testing.T) {
	var b strings.Builder
	generateRemoteNamedLoadHandlers(&b, &semantic.Result{}, nil, []loadCallInfo{{
		modelName:    "Missing",
		argNames:     []string{"id"},
		argTypes:     []string{"int64"},
		argTypeNames: []string{"Int"},
	}})
	if b.Len() != 0 {
		t.Fatalf("unknown model generated a remote loader:\n%s", b.String())
	}
}

func TestGenerateRemoteNamedLoadHandlerSingleSoftKey(t *testing.T) {
	model := &ast.ModelDecl{
		Name:       "User",
		Directives: []*ast.Directive{{Name: "soft"}},
		Fields: []*ast.FieldDecl{
			{Name: "email", Type: &ast.TypeRef{Name: "String"}},
			computedAggregateField("postCount", "Int", "count", &ast.Ident{Name: "posts"}),
		},
	}
	call := loadCallInfo{
		modelName:    "User",
		argNames:     []string{"email"},
		argTypes:     []string{"string"},
		argTypeNames: []string{"String"},
	}
	var b strings.Builder
	generateRemoteNamedLoadHandler(&b, model, call)
	out := b.String()
	for _, want := range []string{
		`lux.NewStringField("email").In(emailKeys...)`,
		`lux.NewTimeField("deleted_at").IsNull()`,
		"resolveUserComputed(ctx, app, rows, req.FieldMask)",
		"key := row.Email",
		"key := emailKeys[i]",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("single-key remote handler missing %q:\n%s", want, out)
		}
	}
}

func TestGenerateFederationResolversUsesCanonicalListAndSelection(t *testing.T) {
	oldAPIs := apiIDs
	oldFields := modelFieldIDs
	defer func() {
		apiIDs = oldAPIs
		modelFieldIDs = oldFields
	}()
	apiIDs = map[string]int{"svc:resolve:Post:userId": 43}
	modelFieldIDs = map[string]map[string]int{"Post": {"id": 1, "userId": 2, "title": 3}}

	post := &ast.ModelDecl{
		Name: "Post",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "title", Type: &ast.TypeRef{Name: "String"}},
		},
	}
	result := &semantic.Result{Files: []*ast.File{{
		Name:   "origin/post.luxo",
		Models: []*ast.ModelDecl{post},
		Extends: []*ast.ExtendDecl{{
			Name: "User",
			Fields: []*ast.FieldDecl{
				{Name: "invalid"},
				{Name: "remote", Type: &ast.TypeRef{Name: "Remote"}},
				{
					Name: "posts",
					Type: &ast.TypeRef{Name: "Post", IsList: true},
				},
			},
		}},
	}}}

	var b strings.Builder
	generateFederationResolvers(&b, result, []*ast.ModelDecl{post}, nil)
	code := b.String()
	if !strings.Contains(code, `Name: "keys", Type: "Int", IsList: true`) {
		t.Errorf("resolver metadata must describe one canonical list param:\n%s", code)
	}
	if !strings.Contains(code, `codec.FieldMaskHas(fieldMask, 3)`) || !strings.Contains(code, `fields = append(fields, "title")`) {
		t.Errorf("resolver must push the field selection into SQL:\n%s", code)
	}
	if !strings.Contains(code, `fields = append(fields, "user_id")`) {
		t.Errorf("resolver must select its grouping key:\n%s", code)
	}
}

func TestGenerateFederationResolversUsesExtendedModelPrimaryKeyType(t *testing.T) {
	oldContext := globalEventCtx
	oldAPIs := apiIDs
	defer func() {
		globalEventCtx = oldContext
		apiIDs = oldAPIs
	}()
	apiIDs = map[string]int{"svc:resolve:Review:productSku": 44}
	globalEventCtx = &EventContext{
		ModelIDType:  map[string]string{"Product": "String"},
		ModelIDField: map[string]string{"Product": "sku"},
	}

	review := &ast.ModelDecl{
		Name: "Review",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "productSku", Type: &ast.TypeRef{Name: "String"}},
		},
	}
	result := &semantic.Result{Files: []*ast.File{{
		Name:   "origin/review.luxo",
		Models: []*ast.ModelDecl{review},
		Extends: []*ast.ExtendDecl{{
			Name: "Product",
			Fields: []*ast.FieldDecl{{
				Name: "reviews",
				Type: &ast.TypeRef{Name: "Review", IsList: true},
			}},
		}},
	}}}

	var b strings.Builder
	generateFederationResolvers(&b, result, []*ast.ModelDecl{review}, nil)
	code := b.String()
	for _, want := range []string{
		`ParamStringArray("keys")`,
		`lux.NewStringField("product_sku").In(keys...)`,
		`make(map[string][]*Review, len(keys))`,
		`Name: "keys", Type: "String", IsList: true`,
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("custom primary-key resolver missing %q:\n%s", want, code)
		}
	}
}

func TestGenerateFederationResolversSkipsNullForeignKeys(t *testing.T) {
	oldContext := globalEventCtx
	defer func() { globalEventCtx = oldContext }()
	globalEventCtx = &EventContext{
		ModelIDType:  map[string]string{"Product": "String"},
		ModelIDField: map[string]string{"Product": "sku"},
	}

	review := &ast.ModelDecl{
		Name: "Review",
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "productSku", Type: &ast.TypeRef{Name: "String", Nullable: true}},
		},
	}
	result := &semantic.Result{Files: []*ast.File{{
		Name:   "origin/review.luxo",
		Models: []*ast.ModelDecl{review},
		Extends: []*ast.ExtendDecl{{
			Name: "Product",
			Fields: []*ast.FieldDecl{{
				Name: "reviews",
				Type: &ast.TypeRef{Name: "Review", IsList: true},
			}},
		}},
	}}}

	var b strings.Builder
	generateFederationResolvers(&b, result, []*ast.ModelDecl{review}, nil)
	code := b.String()
	for _, want := range []string{
		"fk := row.ProductSku",
		"if fk == nil { continue }",
		"grouped[*fk] = append(grouped[*fk], row)",
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("nullable federation key handling missing %q:\n%s", want, code)
		}
	}
	if _, err := format.Source([]byte(code)); err != nil {
		t.Fatalf("nullable federation resolver generated invalid Go: %v\n%s", err, code)
	}
}

func testModel(name string, directives []*ast.Directive, fields []*ast.FieldDecl) *ast.ModelDecl {
	return &ast.ModelDecl{
		Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
		Name:       name,
		Directives: directives,
		Fields:     fields,
	}
}

func testField(name, typeName string, directives ...*ast.Directive) *ast.FieldDecl {
	return &ast.FieldDecl{
		Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
		Name:       name,
		Type:       &ast.TypeRef{Name: typeName},
		Directives: directives,
	}
}

func directive(name string) *ast.Directive {
	return &ast.Directive{Name: name}
}

func TestGenerateHandlerFileNoCrud(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", nil, []*ast.FieldDecl{
					testField("id", "Int"),
					testField("name", "String"),
				}),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	if src != nil {
		t.Fatal("should return nil when no @crud models")
	}
}

func TestGenerateHandlerFileAllCrud(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
					testField("id", "Int", directive("id"), directive("auto")),
					testField("name", "String"),
					testField("email", "String"),
				}),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	if src == nil {
		t.Fatal("should generate handler file")
	}
	code := string(src)

	// Should have all 5 CRUD handlers
	for _, name := range []string{"handleGetUser", "handleListUsers", "handleCreateUser", "handleUpdateUser", "handleDeleteUser"} {
		if !strings.Contains(code, name) {
			t.Errorf("missing handler: %s", name)
		}
	}

	// Should have RegisterHandlers
	if !strings.Contains(code, "func RegisterHandlers") {
		t.Error("missing RegisterHandlers function")
	}

	// Should register all 5 APIs
	for _, api := range []string{"getUser", "listUsers", "createUser", "updateUser", "deleteUser"} {
		if !strings.Contains(code, `"`+api+`"`) {
			t.Errorf("missing API registration: %s", api)
		}
	}
}

func TestGenerateHandlerCrudOnlyGetList(t *testing.T) {
	onlyArg := &ast.NamedArg{
		Name: "only",
		Value: &ast.ListExpr{
			Items: []ast.Expr{
				&ast.Ident{Name: "get"},
				&ast.Ident{Name: "list"},
			},
		},
	}
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("Post", []*ast.Directive{crudDirective(onlyArg)}, []*ast.FieldDecl{
					testField("id", "Int", directive("id")),
					testField("title", "String"),
				}),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	if !strings.Contains(code, "handleGetPost") {
		t.Error("should have getPost")
	}
	if !strings.Contains(code, "handleListPosts") {
		t.Error("should have listPosts")
	}
	if strings.Contains(code, "handleCreatePost") {
		t.Error("should NOT have createPost")
	}
	if strings.Contains(code, "handleDeletePost") {
		t.Error("should NOT have deletePost")
	}
}

func TestGenerateHandlerCrudExceptDelete(t *testing.T) {
	exceptArg := &ast.NamedArg{
		Name: "except",
		Value: &ast.ListExpr{
			Items: []ast.Expr{
				&ast.Ident{Name: "delete"},
			},
		},
	}
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective(exceptArg)}, []*ast.FieldDecl{
					testField("id", "Int", directive("id")),
					testField("name", "String"),
				}),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	if !strings.Contains(code, "handleGetUser") {
		t.Error("should have getUser")
	}
	if strings.Contains(code, "handleDeleteUser(") {
		t.Error("should NOT have deleteUser")
	}
}

func TestGenerateHandlerSoftDelete(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User",
					[]*ast.Directive{crudDirective(), {Name: "soft"}},
					[]*ast.FieldDecl{
						testField("id", "Int", directive("id")),
						testField("name", "String"),
					},
				),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	if !strings.Contains(code, "SoftDelete") {
		t.Error("@soft model should use SoftDelete")
	}
}

func TestGenerateHandlerNullableField(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
					testField("id", "Int", directive("id"), directive("auto")),
					testField("name", "String"),
					{
						Name:       "avatar",
						Type:       &ast.TypeRef{Name: "String", Nullable: true},
						Directives: nil,
					},
				}),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	// Nullable field should have HasParam check in create handler
	if !strings.Contains(code, `req.HasParam("avatar")`) {
		t.Error("nullable field should check HasParam in create handler")
	}
}

func TestGenerateHandlerImmutableField(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
					testField("id", "Int", directive("id"), directive("auto")),
					testField("email", "String", directive("immutable")),
					testField("name", "String"),
				}),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	// Update handler should NOT include immutable fields
	updatePart := code[strings.Index(code, "handleUpdateUser"):]
	if strings.Contains(updatePart, `"email"`) {
		t.Error("immutable field should be excluded from update handler")
	}
}

func TestPluralize(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"User", "Users"},
		{"Post", "Posts"},
		{"Address", "Addresses"},
		{"Box", "Boxes"},
		{"Quiz", "Quizes"},
		{"Category", "Categories"},
	}
	for _, tt := range tests {
		got := pluralize(tt.input)
		if got != tt.want {
			t.Errorf("pluralize(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCrudOperationsNoArgs(t *testing.T) {
	m := testModel("User", []*ast.Directive{crudDirective()}, nil)
	ops := crudOperations(m)
	if len(ops) != 6 {
		t.Errorf("got %d ops, want 6", len(ops))
	}
}

func TestCrudOperationsNoCrud(t *testing.T) {
	m := testModel("User", nil, nil)
	ops := crudOperations(m)
	if ops != nil {
		t.Error("should return nil for non-crud model")
	}
}

func TestExtractListArgNonList(t *testing.T) {
	result := extractListArg(&ast.Ident{Name: "get"})
	if result != nil {
		t.Error("should return nil for non-list arg")
	}
}

func TestCrudOperationsMultipleDirectives(t *testing.T) {
	// Model with multiple directives including @crud — tests the continue branch
	m := testModel("User", []*ast.Directive{
		{Name: "soft"},
		crudDirective(),
		{Name: "noTime"},
	}, nil)
	ops := crudOperations(m)
	if len(ops) != 6 {
		t.Errorf("got %d ops, want 6 (should skip non-crud directives)", len(ops))
	}
}

func TestCrudOperationsUnknownArg(t *testing.T) {
	// @crud with unknown arg (not only/except) falls through to default crudOps
	unknownArg := &ast.NamedArg{
		Name:  "unknown",
		Value: &ast.Ident{Name: "something"},
	}
	m := testModel("User", []*ast.Directive{crudDirective(unknownArg)}, nil)
	ops := crudOperations(m)
	if len(ops) != 6 {
		t.Errorf("got %d ops, want 6 (should fall through to default)", len(ops))
	}
}

func TestSkipHandlerFieldComputed(t *testing.T) {
	f := &ast.FieldDecl{
		Name:     "commentCount",
		Type:     &ast.TypeRef{Name: "Int"},
		Computed: &ast.ComputedField{},
	}
	if !skipHandlerField(f, nil) {
		t.Error("computed field should be skipped")
	}
}

func TestSkipHandlerFieldIDNoAuto(t *testing.T) {
	// id field with @id but without @auto should NOT be skipped
	f := &ast.FieldDecl{
		Name:       "id",
		Type:       &ast.TypeRef{Name: "Int"},
		Directives: []*ast.Directive{{Name: "id"}},
	}
	if skipHandlerField(f, nil) {
		t.Error("id without @auto should not be skipped")
	}
}

func TestSkipHandlerFieldIDWithAuto(t *testing.T) {
	// id field with @auto should be skipped
	f := &ast.FieldDecl{
		Name:       "id",
		Type:       &ast.TypeRef{Name: "Int"},
		Directives: []*ast.Directive{{Name: "auto"}},
	}
	if !skipHandlerField(f, nil) {
		t.Error("id with @auto should be skipped")
	}
}

func TestSkipHandlerFieldRelation(t *testing.T) {
	// Non-builtin, non-enum type is a relation → skipped
	f := &ast.FieldDecl{
		Name: "user",
		Type: &ast.TypeRef{Name: "User"},
	}
	if !skipHandlerField(f, nil) {
		t.Error("relation field should be skipped")
	}
}

func TestJSONFieldIsNotRelation(t *testing.T) {
	f := &ast.FieldDecl{Name: "metadata", Type: &ast.TypeRef{Name: "JSON"}}
	if isRelationField(f, nil) || skipHandlerField(f, nil) {
		t.Fatal("JSON is a built-in scalar field")
	}
}

func TestSkipHandlerFieldInternal(t *testing.T) {
	f := &ast.FieldDecl{
		Name:       "secret",
		Type:       &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{{Name: "internal"}},
	}
	if !skipHandlerField(f, nil) {
		t.Error("@internal field should be skipped")
	}
}

func TestSkipHandlerFieldNormal(t *testing.T) {
	// A normal field with no special directives should NOT be skipped
	f := &ast.FieldDecl{
		Name: "name",
		Type: &ast.TypeRef{Name: "String"},
	}
	if skipHandlerField(f, nil) {
		t.Error("normal field should not be skipped")
	}
}

func TestSkipHandlerFieldEnumNotSkipped(t *testing.T) {
	// Enum fields are NOT relation fields, so should NOT be skipped
	enums := map[string]bool{"Role": true}
	f := &ast.FieldDecl{
		Name: "role",
		Type: &ast.TypeRef{Name: "Role"},
	}
	if skipHandlerField(f, enums) {
		t.Error("enum field should not be skipped")
	}
}

func TestIdGoTypeNoIdField(t *testing.T) {
	m := testModel("User", nil, []*ast.FieldDecl{
		testField("name", "String"),
	})
	if got := idGoType(m); got != "int64" {
		t.Errorf("idGoType = %q, want int64 (default)", got)
	}
}

func TestIdGoTypeUUID(t *testing.T) {
	m := testModel("User", nil, []*ast.FieldDecl{
		testField("id", "UUID"),
		testField("name", "String"),
	})
	if got := idGoType(m); got != "uuid.UUID" {
		t.Errorf("idGoType = %q, want uuid.UUID", got)
	}
}

func TestIdGoTypeString(t *testing.T) {
	m := testModel("User", nil, []*ast.FieldDecl{
		testField("id", "String"),
		testField("name", "String"),
	})
	if got := idGoType(m); got != "string" {
		t.Errorf("idGoType = %q, want string", got)
	}
}

func TestParamMethodBool(t *testing.T) {
	if got := paramMethod("bool"); got != "Bool" {
		t.Errorf("paramMethod(bool) = %q, want Bool", got)
	}
}

func TestParamMethodFloat(t *testing.T) {
	if got := paramMethod("float64"); got != "Float" {
		t.Errorf("paramMethod(float64) = %q, want Float", got)
	}
}

func TestParamMethodDateTime(t *testing.T) {
	if got := paramMethod("time.Time"); got != "DateTime" {
		t.Errorf("paramMethod(time.Time) = %q, want DateTime", got)
	}
}

func TestParamMethodCustomType(t *testing.T) {
	// Custom types return empty for ParamJSON dispatch
	if got := paramMethod("uuid.UUID"); got != "" {
		t.Errorf("paramMethod(uuid.UUID) = %q, want empty", got)
	}
}

func TestParamMethodArrayTypes(t *testing.T) {
	if got := paramMethod("[]int64"); got != "IntArray" {
		t.Errorf("paramMethod([]int64) = %q, want IntArray", got)
	}
	if got := paramMethod("[]string"); got != "StringArray" {
		t.Errorf("paramMethod([]string) = %q, want StringArray", got)
	}
}

func TestGenerateHandlerEnumField(t *testing.T) {
	enums := map[string]bool{"Role": true}
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
					testField("id", "Int", directive("id"), directive("auto")),
					testField("role", "Role"),
				}),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", enums)
	code := string(src)

	// Enum field should have cast: Role(roleValVal)
	if !strings.Contains(code, "Role(") {
		t.Errorf("enum field should be cast to enum type:\n%s", code)
	}
}

func TestGenerateHandlerUUIDId(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("Token", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
					testField("id", "UUID", directive("id"), directive("auto")),
					testField("value", "String"),
				}),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	// UUID id should use ParamString
	if !strings.Contains(code, "ParamString") {
		t.Errorf("UUID id should use ParamString:\n%s", code)
	}
}

func TestGenerateHandlerBooleanField(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
					testField("id", "Int", directive("id"), directive("auto")),
					testField("active", "Boolean"),
				}),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	// Boolean field should use ParamBool
	if !strings.Contains(code, "ParamBool") {
		t.Errorf("Boolean field should use ParamBool:\n%s", code)
	}
}

func TestGenerateHandlerHardDelete(t *testing.T) {
	// Test delete without @soft (hard delete path)
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User",
					[]*ast.Directive{crudDirective()},
					[]*ast.FieldDecl{
						testField("id", "Int", directive("id")),
						testField("name", "String"),
					},
				),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	// Hard delete should use .Where().Delete()
	if strings.Contains(code, "SoftDelete") {
		t.Error("non-@soft model should NOT use SoftDelete")
	}
	if !strings.Contains(code, ".Delete(ctx)") {
		t.Error("should use .Delete(ctx)")
	}
}

func TestSkipHandlerFieldNilType(t *testing.T) {
	// Field with nil type should not panic in isRelationField
	f := &ast.FieldDecl{
		Name: "unknown",
		Type: nil,
	}
	// Should not be skipped (nil type → isRelationField returns false)
	if skipHandlerField(f, nil) {
		t.Error("nil-type field should not be skipped")
	}
}

func TestGenerateHandlerWithRelations(t *testing.T) {
	// Test get/list handlers with relation resolvers
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User",
					[]*ast.Directive{crudDirective()},
					[]*ast.FieldDecl{
						testField("id", "Int", directive("id"), directive("auto")),
						testField("name", "String"),
						{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
					},
				),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	// Should have relation resolver function
	if !strings.Contains(code, "resolveUserRelations") {
		t.Errorf("should have relation resolver:\n%s", code)
	}
	// Get handler should call resolve
	if !strings.Contains(code, "resolveUserRelations(ctx, app, result, req.Select)") {
		t.Errorf("get handler should call resolveRelations:\n%s", code)
	}
}

func TestGenerateHandlerNullableRelation(t *testing.T) {
	// Nullable FK should generate nil check before Load
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("Post",
					[]*ast.Directive{crudDirective()},
					[]*ast.FieldDecl{
						testField("id", "Int", directive("id"), directive("auto")),
						testField("title", "String"),
						{Name: "userId", Type: &ast.TypeRef{Name: "Int", Nullable: true}},
						{Name: "user", Type: &ast.TypeRef{Name: "User", Nullable: true}},
					},
				),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	// Nullable FK should have nil check: if post.UserId != nil {
	if !strings.Contains(code, "if post.UserId != nil") {
		t.Errorf("nullable FK should have nil check before Load:\n%s", code)
	}
	// Should dereference the pointer: *post.UserId
	if !strings.Contains(code, "*post.UserId") {
		t.Errorf("nullable FK should dereference pointer in Load call:\n%s", code)
	}
}

func TestGenerateHandlerNonNullableRelation(t *testing.T) {
	// Non-nullable FK should NOT have nil check
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("Post",
					[]*ast.Directive{crudDirective()},
					[]*ast.FieldDecl{
						testField("id", "Int", directive("id"), directive("auto")),
						testField("title", "String"),
						testField("userId", "Int"),
						{Name: "user", Type: &ast.TypeRef{Name: "User"}},
					},
				),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	// Non-nullable FK should NOT have nil check
	if strings.Contains(code, "if post.UserId != nil") {
		t.Errorf("non-nullable FK should NOT have nil check:\n%s", code)
	}
	// Should NOT dereference pointer
	if strings.Contains(code, "*post.UserId") {
		t.Errorf("non-nullable FK should NOT dereference:\n%s", code)
	}
	// Should directly pass post.UserId
	if !strings.Contains(code, "post.UserId, childCols") {
		t.Errorf("should directly pass FK value:\n%s", code)
	}
}

func TestGenerateHandlerDefaultFieldValue(t *testing.T) {
	// Field with a default value should use HasParam in create handler
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
					testField("id", "Int", directive("id"), directive("auto")),
					{
						Name:    "role",
						Type:    &ast.TypeRef{Name: "String"},
						Default: &ast.Literal{Value: "user"},
					},
				}),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	// Field with default should have HasParam check
	if !strings.Contains(code, `req.HasParam("role")`) {
		t.Errorf("field with default should use HasParam:\n%s", code)
	}
}

func TestGenerateHandlerHiddenField(t *testing.T) {
	// @hidden fields should generate defaultCols excluding hidden fields
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User",
					[]*ast.Directive{crudDirective()},
					[]*ast.FieldDecl{
						testField("id", "Int", directive("id"), directive("auto")),
						testField("name", "String"),
						{Name: "password", Type: &ast.TypeRef{Name: "String"},
							Directives: []*ast.Directive{{Name: "hidden"}}},
					},
				),
			},
		}},
	}
	src := generateHandlerFile(result, "app", nil)
	code := string(src)

	// Should generate defaultUserCols
	if !strings.Contains(code, "defaultUserCols") {
		t.Errorf("should generate defaultCols for model with @hidden:\n%s", code)
	}
	// defaultCols should not contain password
	if strings.Contains(code, `"password"`) && strings.Contains(code, "defaultUserCols") {
		// check that password is not in the defaultCols var
		idx := strings.Index(code, "defaultUserCols")
		line := code[idx : idx+200]
		if strings.Contains(line, "password") {
			t.Errorf("defaultCols should not contain @hidden field password:\n%s", line)
		}
	}
}

func TestGenerateHandlerHashField(t *testing.T) {
	// @hash field should auto-hash in create handler
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User",
					[]*ast.Directive{crudDirective()},
					[]*ast.FieldDecl{
						testField("id", "Int", directive("id"), directive("auto")),
						testField("name", "String"),
						{Name: "password", Type: &ast.TypeRef{Name: "String"},
							Directives: []*ast.Directive{{Name: "hash"}}},
					},
				),
			},
		}},
	}
	src := generateHandlerFile(result, "app", nil)
	code := string(src)

	if !strings.Contains(code, "luxocrypto.HashPassword") {
		t.Errorf("@hash field should generate HashPassword call:\n%s", code)
	}
}

func TestGenerateHandlerNullableParam(t *testing.T) {
	// Nullable param should use & prefix in setter
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("Post",
					[]*ast.Directive{crudDirective()},
					[]*ast.FieldDecl{
						testField("id", "Int", directive("id"), directive("auto")),
						testField("title", "String"),
						{Name: "subtitle", Type: &ast.TypeRef{Name: "String", Nullable: true}},
					},
				),
			},
		}},
	}
	src := generateHandlerFile(result, "app", nil)
	code := string(src)

	if !strings.Contains(code, "&subtitleVal") {
		t.Errorf("nullable param should use & prefix:\n%s", code)
	}
}

func TestNativeHandlersDecodeOptionalNullableJSON(t *testing.T) {
	param := &ast.ParamDecl{
		Name:    "payload",
		Type:    &ast.TypeRef{Name: "Payload", Nullable: true},
		Default: &ast.Literal{Kind: token.Null, Value: "null"},
	}
	var apiBuilder strings.Builder
	generateNativeAPIHandler(&apiBuilder, &ast.ApiDecl{
		Name:       "submit",
		Params:     []*ast.ParamDecl{param},
		Directives: []*ast.Directive{{Name: "native"}},
	}, nil, nil)
	if out := apiBuilder.String(); !strings.Contains(out, `req.ParamJSONOptionalNullable("payload", &payload)`) {
		t.Fatalf("native API nullable parameter decoding missing:\n%s", out)
	}

	var serviceBuilder strings.Builder
	generateNativeServiceHandler(&serviceBuilder, &ast.FnDecl{
		Name:       "submit",
		Params:     []*ast.ParamDecl{param},
		Directives: []*ast.Directive{{Name: "native"}, {Name: "service"}},
	}, nil, nil)
	if out := serviceBuilder.String(); !strings.Contains(out, `req.ParamJSONOptionalNullable("payload", &payload)`) {
		t.Fatalf("native service nullable parameter decoding missing:\n%s", out)
	}
}

func TestGenerateHandlerEnumParam(t *testing.T) {
	// Enum param should cast to enum type
	result := &semantic.Result{
		Files: []*ast.File{{
			Enums: []*ast.EnumDecl{{Name: "Role", Values: []string{"ADMIN", "USER"}}},
			Models: []*ast.ModelDecl{
				testModel("User",
					[]*ast.Directive{crudDirective()},
					[]*ast.FieldDecl{
						testField("id", "Int", directive("id"), directive("auto")),
						testField("name", "String"),
						{Name: "role", Type: &ast.TypeRef{Name: "Role"}},
					},
				),
			},
		}},
	}
	enums := collectEnums(result)
	src := generateHandlerFile(result, "app", enums)
	code := string(src)

	if !strings.Contains(code, "Role(roleVal)") {
		t.Errorf("enum param should cast to enum type:\n%s", code)
	}
}

// TestGenerateFilterParserAllTypes covers Float, DateTime, and Boolean
// branches of generateFilterParser.
func TestGenerateFilterParserAllTypes(t *testing.T) {
	filterDirective := &ast.Directive{Name: "filterable"}

	m := testModel("Event", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
		testField("id", "Int", directive("id"), directive("auto")),
		{Name: "score", Type: &ast.TypeRef{Name: "Float"}, Directives: []*ast.Directive{filterDirective}},
		{Name: "active", Type: &ast.TypeRef{Name: "Boolean"}, Directives: []*ast.Directive{filterDirective}},
		{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}, Directives: []*ast.Directive{filterDirective}},
	})
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{m},
		}},
	}

	src := generateHandlerFile(result, "app", nil)
	code := string(src)

	if !strings.Contains(code, "NewFloatField") {
		t.Errorf("missing NewFloatField in filter parser:\n%s", code)
	}
	if !strings.Contains(code, "NewBoolField") {
		t.Errorf("missing NewBoolField in filter parser:\n%s", code)
	}
	if !strings.Contains(code, "NewTimeField") {
		t.Errorf("missing NewTimeField in filter parser:\n%s", code)
	}
}

// TestGenerateFilterParserSkipsRelation ensures relation fields with @filterable
// are skipped (isRelationField returns true → continue).
func TestGenerateFilterParserSkipsRelation(t *testing.T) {
	filterDirective := &ast.Directive{Name: "filterable"}

	m := testModel("Post", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
		testField("id", "Int", directive("id"), directive("auto")),
		testField("title", "String", filterDirective),
		// relation field with @filterable — should be skipped
		{Name: "user", Type: &ast.TypeRef{Name: "User"}, Directives: []*ast.Directive{filterDirective}},
	})
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{m},
		}},
	}

	src := generateHandlerFile(result, "app", nil)
	code := string(src)

	// "user" relation should not appear as a filter case
	if strings.Contains(code, `case "user"`) {
		t.Errorf("relation field should be skipped in filter parser:\n%s", code)
	}
}

// TestGenerateSorterParserHasSortableField ensures generateSorterParser emits
// a case for @sortable fields (covering the per-field emit branch at line 732).
func TestGenerateSorterParserHasSortableField(t *testing.T) {
	sortDirective := &ast.Directive{Name: "sortable"}

	m := testModel("Post", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
		testField("id", "Int", directive("id"), directive("auto")),
		{Name: "createdAt", Type: &ast.TypeRef{Name: "DateTime"}, Directives: []*ast.Directive{sortDirective}},
	})
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{m},
		}},
	}

	src := generateHandlerFile(result, "app", nil)
	code := string(src)

	if !strings.Contains(code, `case "createdAt"`) {
		t.Errorf("missing sortable field case in sorter parser:\n%s", code)
	}
	if !strings.Contains(code, "created_at") {
		t.Errorf("missing snake_case column in sorter parser:\n%s", code)
	}
}

// TestSkipHandlerFieldNilTypeFalse ensures a field with nil type and no
// other skip conditions is NOT skipped (the unexercised branch in skipHandlerField
// where isAutoManaged, @internal, computed are all false but type is nil).
func TestSkipHandlerFieldNilTypeFalse(t *testing.T) {
	// A field with no directives and nil type — isAutoManaged=false, @internal=false,
	// computed=nil, and isRelationField returns false for nil Type.
	f := &ast.FieldDecl{
		Name: "freeField",
		Type: nil,
	}
	// Should NOT be skipped (nil type is not a relation).
	if skipHandlerField(f, nil) {
		t.Error("field with nil type and no skip conditions should not be skipped")
	}
}

// TestCollectInferredAPIsWithBody ensures APIs with a Body are excluded from
// the inferred list (covers the body-check branch at line 145).
func TestCollectInferredAPIsWithBody(t *testing.T) {
	body := &ast.Block{} // non-nil body
	result := &semantic.Result{
		Files: []*ast.File{{
			APIs: []*ast.ApiDecl{
				{Name: "withBody", Body: body},
				{Name: "noBody", Body: nil},
			},
		}},
	}
	_, apis := collectInferredAPIs(result)
	for _, a := range apis {
		if a.Body != nil {
			t.Errorf("API with Body should be excluded from inferred list: %s", a.Name)
		}
	}
	found := false
	for _, a := range apis {
		if a.Name == "noBody" {
			found = true
		}
	}
	if !found {
		t.Error("no-body API should be in inferred list")
	}
}

// TestGenerateHandlerWithInferredAPI exercises generateInferredHandlers
// (covering the non-nil inferAPI + ValidateInferredReturnType paths).
func TestGenerateHandlerWithInferredAPI(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
					testField("id", "Int", directive("id"), directive("auto")),
					testField("email", "String"),
				}),
			},
			APIs: []*ast.ApiDecl{
				{
					Name:   "getUserByEmail",
					Body:   nil,
					Params: []*ast.ParamDecl{{Name: "email", Type: &ast.TypeRef{Name: "String"}}},
				},
			},
		}},
	}
	src := generateHandlerFile(result, "app", nil)
	if src == nil {
		t.Fatal("should generate handler file")
	}
	code := string(src)
	if !strings.Contains(code, "handleGetUserByEmail") {
		t.Errorf("missing inferred handler:\n%s", code)
	}
}

// TestGenerateRegisterFuncWithInferredNames covers the inferred-names loop
// in generateRegisterFuncWithInferred (line 619).
func TestGenerateRegisterFuncWithInferredNames(t *testing.T) {
	var b strings.Builder
	models := []*ast.ModelDecl{
		testModel("User", []*ast.Directive{crudDirective()}, nil),
	}
	inferredNames := []string{"getUserByEmail", "listUsersByRole"}
	generateRegisterFuncWithInferred(&b, models, inferredNames, nil)
	out := b.String()

	if !strings.Contains(out, `"getUserByEmail"`) {
		t.Errorf("missing inferred handler registration for getUserByEmail:\n%s", out)
	}
	if !strings.Contains(out, `"listUsersByRole"`) {
		t.Errorf("missing inferred handler registration for listUsersByRole:\n%s", out)
	}
}

// TestGenerateFilterParserIntAndStringTypes covers the "Int" and "String"
// branches of generateFilterParser (lines 694-695 and 698-699).
func TestGenerateFilterParserIntAndStringTypes(t *testing.T) {
	filterDirective := &ast.Directive{Name: "filterable"}

	m := testModel("Article", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
		testField("id", "Int", directive("id"), directive("auto")),
		{Name: "views", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{filterDirective}},
		{Name: "title", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{filterDirective}},
	})
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{m},
		}},
	}

	src := generateHandlerFile(result, "app", nil)
	code := string(src)

	// Int @filterable → NewIntField
	if !strings.Contains(code, "NewIntField") {
		t.Errorf("missing NewIntField for Int @filterable field:\n%s", code)
	}
	// String @filterable → NewStringField
	if !strings.Contains(code, "NewStringField") {
		t.Errorf("missing NewStringField for String @filterable field:\n%s", code)
	}
}

// TestGenerateFilterParserDefaultBranch covers the default branch of the
// filter type switch (enum type → NewStringField). The enum must be passed via
// the enums map so isRelationField returns false.
func TestGenerateFilterParserDefaultBranch(t *testing.T) {
	filterDirective := &ast.Directive{Name: "filterable"}
	// "Status" is an enum — not a relation when enums map contains it.
	enums := map[string]bool{"Status": true}

	m := testModel("Post", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
		testField("id", "Int", directive("id"), directive("auto")),
		{Name: "status", Type: &ast.TypeRef{Name: "Status"}, Directives: []*ast.Directive{filterDirective}},
	})
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{m},
		}},
	}

	src := generateHandlerFile(result, "app", enums)
	code := string(src)

	// default branch → NewStringField
	if !strings.Contains(code, "NewStringField") {
		t.Errorf("default branch should use NewStringField for enum type:\n%s", code)
	}
}

// TestGenerateInferredHandlerWithReturnTypeWarning covers the warning branch in
// generateInferredHandlers when ValidateInferredReturnType returns a non-empty message.
func TestGenerateInferredHandlerWithReturnTypeWarning(t *testing.T) {
	// getUserByEmail but declared return type is [User] (wrong — get returns single, not list)
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", nil, []*ast.FieldDecl{
					testField("id", "Int"),
					testField("email", "String"),
				}),
			},
			APIs: []*ast.ApiDecl{
				{
					Name:       "getUserByEmail",
					ReturnType: &ast.TypeRef{Name: "User", IsList: true}, // wrong: list for "get"
					Params:     []*ast.ParamDecl{{Name: "email", Type: &ast.TypeRef{Name: "String"}}},
				},
			},
		}},
	}
	// Should not panic — warning goes to stderr, handler still generated
	src := generateHandlerFile(result, "app", nil)
	if src == nil {
		t.Fatal("should generate handler file even with return type mismatch warning")
	}
	code := string(src)
	if !strings.Contains(code, "handleGetUserByEmail") {
		t.Errorf("handler should be generated despite warning:\n%s", code)
	}
}

// TestGenerateHandlerWithAuth verifies that @withAuth on model injects
// luvia.Identity auth guard into all CRUD handlers.
func TestGenerateHandlerWithAuth(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User",
					[]*ast.Directive{crudDirective(), {Name: "withAuth", Args: []*ast.NamedArg{{Name: "stores", Value: &ast.ListExpr{Items: []ast.Expr{&ast.Ident{Name: "id"}}}}}}},
					[]*ast.FieldDecl{
						testField("id", "Int", directive("id"), directive("auto")),
						testField("name", "String"),
					},
				),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	if src == nil {
		t.Fatal("should generate handler file")
	}
	code := string(src)

	// All CRUD handlers should have auth check
	if !strings.Contains(code, "luvia.Identity(ctx)") {
		t.Error("@withAuth model should inject luvia.Identity check")
	}
	if !strings.Contains(code, "errors.Unauthorized") {
		t.Error("@withAuth model should return errors.Unauthorized")
	}
	// Should import luvia
	if !strings.Contains(code, `"github.com/light-speak/luxo/pkg/lux/luvia"`) {
		t.Error("should import luvia when @withAuth is used")
	}
}

func TestGenerateCRUDHandlerWithModelAuth(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("Project",
					[]*ast.Directive{crudDirective(), {Name: "auth"}},
					[]*ast.FieldDecl{
						testField("id", "Int", directive("id"), directive("auto")),
						testField("name", "String"),
					},
				),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	if src == nil {
		t.Fatal("should generate handler file")
	}
	code := string(src)
	if got := strings.Count(code, "luvia.Identity(ctx)"); got != len(crudOps) {
		t.Errorf("@auth model should protect all %d CRUD handlers, got %d guards", len(crudOps), got)
	}
	if !strings.Contains(code, `"github.com/light-speak/luxo/pkg/lux/luvia"`) {
		t.Error("should import luvia when a CRUD model uses @auth")
	}
}

// TestGenerateHandlerWithoutAuth verifies that models without @withAuth
// do NOT inject auth guards.
func TestGenerateHandlerWithoutAuth(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("Post",
					[]*ast.Directive{crudDirective()},
					[]*ast.FieldDecl{
						testField("id", "Int", directive("id"), directive("auto")),
						testField("title", "String"),
					},
				),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	if src == nil {
		t.Fatal("should generate handler file")
	}
	code := string(src)

	if strings.Contains(code, "luvia.Identity") {
		t.Error("model without @withAuth should NOT inject auth check")
	}
}

// TestGenerateCompiledAPIWithAuth verifies @auth on compiled API handlers.
func TestGenerateCompiledAPIWithAuth(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", nil, []*ast.FieldDecl{
					testField("id", "Int", directive("id")),
					testField("name", "String"),
				}),
			},
			APIs: []*ast.ApiDecl{
				{
					Name:       "updateProfile",
					Directives: []*ast.Directive{{Name: "auth"}},
					Params:     []*ast.ParamDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}},
					ReturnType: &ast.TypeRef{Name: "Boolean"},
					Body: &ast.Block{Stmts: []ast.Stmt{
						&ast.ReturnStmt{Value: &ast.Literal{Kind: token.True, Value: "true"}},
					}},
				},
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	if src == nil {
		t.Fatal("should generate handler file")
	}
	code := string(src)

	if !strings.Contains(code, "luvia.Identity(ctx)") {
		t.Error("@auth API should inject luvia.Identity check")
	}
	if !strings.Contains(code, "errors.Unauthorized") {
		t.Error("@auth API should return errors.Unauthorized")
	}
}

// TestGenerateCompiledAPIWithoutAuth verifies compiled APIs without @auth
// do NOT inject auth guards.
func TestGenerateCompiledAPIWithoutAuth(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			APIs: []*ast.ApiDecl{
				{
					Name:       "register",
					ReturnType: &ast.TypeRef{Name: "Boolean"},
					Body: &ast.Block{Stmts: []ast.Stmt{
						&ast.ReturnStmt{Value: &ast.Literal{Kind: token.True, Value: "true"}},
					}},
				},
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	if src == nil {
		t.Fatal("should generate handler file")
	}
	code := string(src)

	if strings.Contains(code, "luvia.Identity") {
		t.Error("API without @auth should NOT inject auth check")
	}
}

// --- writeAuthCheck ---

func TestWriteAuthCheckNilDirective(t *testing.T) {
	var b strings.Builder
	writeAuthCheck(&b, "\t")
	code := b.String()
	if !strings.Contains(code, "luvia.Identity(ctx)") {
		t.Error("should have identity check")
	}
	if !strings.Contains(code, "errors.Unauthorized") {
		t.Error("should have unauthorized check")
	}
	// Without directives, should NOT have _authorized
	if strings.Contains(code, "_authorized") {
		t.Error("nil directives should not add role/own checks")
	}
}

func TestWriteAuthCheckSingleRole(t *testing.T) {
	var b strings.Builder
	d := &ast.Directive{
		Name: "auth",
		Args: []*ast.NamedArg{
			{Value: &ast.Ident{Name: "Admin"}},
		},
	}
	writeAuthCheck(&b, "\t", d)
	code := b.String()
	if !strings.Contains(code, `identity.String("role") == "Admin"`) {
		t.Errorf("single role check missing:\n%s", code)
	}
	if !strings.Contains(code, "errors.Forbidden") {
		t.Error("should have forbidden check")
	}
}

func TestWriteAuthCheckMultipleRoles(t *testing.T) {
	var b strings.Builder
	d := &ast.Directive{
		Name: "auth",
		Args: []*ast.NamedArg{
			{Value: &ast.Ident{Name: "Admin"}},
			{Value: &ast.Ident{Name: "Moderator"}},
		},
	}
	writeAuthCheck(&b, "\t", d)
	code := b.String()
	if !strings.Contains(code, "switch identity.String(\"role\")") {
		t.Errorf("multi-role switch missing:\n%s", code)
	}
	if !strings.Contains(code, `case "Admin", "Moderator":`) {
		t.Errorf("roles must share one case so every role authorizes:\n%s", code)
	}
}

func TestWriteAuthCheckOwn(t *testing.T) {
	var b strings.Builder
	d := &ast.Directive{
		Name: "auth",
		Args: []*ast.NamedArg{
			{Name: "own", Value: &ast.Literal{Value: "userId"}},
		},
	}
	writeAuthCheck(&b, "\t", d)
	code := b.String()
	if !strings.Contains(code, `req.ParamInt("userId")`) {
		t.Errorf("own param check missing:\n%s", code)
	}
	if !strings.Contains(code, "identity.ID()") {
		t.Errorf("own identity check missing:\n%s", code)
	}
}

func TestWriteAuthCheckOwnIdent(t *testing.T) {
	var b strings.Builder
	d := &ast.Directive{
		Name: "auth",
		Args: []*ast.NamedArg{
			{Name: "own", Value: &ast.Ident{Name: "id"}},
		},
	}
	writeAuthCheck(&b, "\t", d)
	code := b.String()
	if !strings.Contains(code, `req.ParamInt("id")`) {
		t.Errorf("own ident check missing:\n%s", code)
	}
}

func TestWriteAuthCheckPermission(t *testing.T) {
	var b strings.Builder
	d := &ast.Directive{
		Name: "auth",
		Args: []*ast.NamedArg{
			{Name: "permission", Value: &ast.LambdaExpr{
				Body: &ast.Block{Stmts: []ast.Stmt{
					&ast.ExprStmt{Expr: &ast.BinaryExpr{
						Left: &ast.MemberExpr{
							Object: &ast.Ident{Name: "my"},
							Field:  "role",
						},
						Op:    "==",
						Right: &ast.Literal{Value: "SUPER"},
					}},
				}},
			}},
		},
	}
	writeAuthCheck(&b, "\t", d)
	code := b.String()
	if !strings.Contains(code, `identity.String("role") == "SUPER"`) {
		t.Errorf("permission expression missing:\n%s", code)
	}
}

func TestWriteAuthCheckRoleAndOwn(t *testing.T) {
	var b strings.Builder
	d := &ast.Directive{
		Name: "auth",
		Args: []*ast.NamedArg{
			{Value: &ast.Ident{Name: "Admin"}},
			{Name: "own", Value: &ast.Literal{Value: "userId"}},
		},
	}
	writeAuthCheck(&b, "\t", d)
	code := b.String()
	if !strings.Contains(code, `identity.String("role") == "Admin"`) {
		t.Errorf("role check missing:\n%s", code)
	}
	if !strings.Contains(code, `req.ParamInt("userId")`) {
		t.Errorf("own check missing:\n%s", code)
	}
}

// --- compilePermissionExpr ---

func TestCompilePermissionExprMemberIdent(t *testing.T) {
	expr := &ast.BinaryExpr{
		Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "role"},
		Op:    "==",
		Right: &ast.Ident{Name: "ADMIN"},
	}
	got := compilePermissionExpr(expr)
	if got != `identity.String("role") == "ADMIN"` {
		t.Fatalf("got %q", got)
	}
}

func TestCompilePermissionExprMemberMember(t *testing.T) {
	// my.role == Enum.VALUE
	expr := &ast.BinaryExpr{
		Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "role"},
		Op:    "!=",
		Right: &ast.MemberExpr{Object: &ast.Ident{Name: "Role"}, Field: "BANNED"},
	}
	got := compilePermissionExpr(expr)
	if got != `identity.String("role") != "BANNED"` {
		t.Fatalf("got %q", got)
	}
}

func TestCompilePermissionExprNonBinary(t *testing.T) {
	got := compilePermissionExpr(&ast.Ident{Name: "foo"})
	if got != "" {
		t.Fatalf("non-binary should return empty, got %q", got)
	}
}

func TestCompilePermissionExprLeftNotMember(t *testing.T) {
	expr := &ast.BinaryExpr{
		Left:  &ast.Ident{Name: "foo"},
		Op:    "==",
		Right: &ast.Literal{Value: "bar"},
	}
	got := compilePermissionExpr(expr)
	if got != "" {
		t.Fatalf("non-member left should return empty, got %q", got)
	}
}

func TestCompilePermissionExprLeftNotMy(t *testing.T) {
	expr := &ast.BinaryExpr{
		Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "other"}, Field: "role"},
		Op:    "==",
		Right: &ast.Literal{Value: "x"},
	}
	got := compilePermissionExpr(expr)
	if got != "" {
		t.Fatalf("non-my left should return empty, got %q", got)
	}
}

func TestCompilePermissionExprRightUnsupported(t *testing.T) {
	expr := &ast.BinaryExpr{
		Left:  &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "role"},
		Op:    "==",
		Right: &ast.CallExpr{Func: &ast.Ident{Name: "foo"}},
	}
	got := compilePermissionExpr(expr)
	if got != "" {
		t.Fatalf("unsupported right should return empty, got %q", got)
	}
}

// --- bodyContains* ---

func TestBodyContainsAwait(t *testing.T) {
	if bodyContainsAwait(nil) {
		t.Error("nil block should return false")
	}
	block := &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.AwaitExpr{Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: &ast.Ident{Name: "x"}}}}}},
	}}
	if !bodyContainsAwait(block) {
		t.Error("should detect await")
	}
	block2 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.Ident{Name: "x"}},
	}}
	if bodyContainsAwait(block2) {
		t.Error("should not detect await in non-await stmt")
	}
	// Non-ExprStmt
	block3 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ReturnStmt{Value: &ast.Ident{Name: "x"}},
	}}
	if bodyContainsAwait(block3) {
		t.Error("non-ExprStmt should return false")
	}
}

func TestBodyContainsTransaction(t *testing.T) {
	if bodyContainsTransaction(nil) {
		t.Error("nil block should return false")
	}
	// ExprStmt with transaction call
	block := &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.CallExpr{
			Func: &ast.Ident{Name: "transaction"},
		}},
	}}
	if !bodyContainsTransaction(block) {
		t.Error("should detect transaction ExprStmt")
	}
	// ValStmt with transaction call
	block2 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ValStmt{Name: "result", Value: &ast.CallExpr{
			Func: &ast.Ident{Name: "transaction"},
		}},
	}}
	if !bodyContainsTransaction(block2) {
		t.Error("should detect transaction ValStmt")
	}
	// ExprStmt with non-transaction call
	block3 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.CallExpr{
			Func: &ast.Ident{Name: "other"},
		}},
	}}
	if bodyContainsTransaction(block3) {
		t.Error("should not detect non-transaction")
	}
	// Non-ident func in call
	block4 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.CallExpr{
			Func: &ast.MemberExpr{Object: &ast.Ident{Name: "x"}, Field: "transaction"},
		}},
	}}
	if bodyContainsTransaction(block4) {
		t.Error("member expr func should not match")
	}
	// ValStmt with non-call value
	block5 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ValStmt{Name: "x", Value: &ast.Ident{Name: "y"}},
	}}
	if bodyContainsTransaction(block5) {
		t.Error("non-call val should not match")
	}
	// ValStmt with non-ident func
	block6 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ValStmt{Name: "x", Value: &ast.CallExpr{
			Func: &ast.MemberExpr{Object: &ast.Ident{Name: "x"}, Field: "y"},
		}},
	}}
	if bodyContainsTransaction(block6) {
		t.Error("val with member func should not match")
	}
}

func TestBodyContainsTemplateString(t *testing.T) {
	if bodyContainsTemplateString(nil) {
		t.Error("nil block should return false")
	}
	// ValStmt with TemplateString
	block := &ast.Block{Stmts: []ast.Stmt{
		&ast.ValStmt{Name: "msg", Value: &ast.TemplateString{Parts: []ast.Expr{}}},
	}}
	if !bodyContainsTemplateString(block) {
		t.Error("should detect template in val")
	}
	// ReturnStmt with TemplateString
	block2 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ReturnStmt{Value: &ast.TemplateString{Parts: []ast.Expr{}}},
	}}
	if !bodyContainsTemplateString(block2) {
		t.Error("should detect template in return")
	}
	// ReturnStmt with nil value
	block3 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ReturnStmt{},
	}}
	if bodyContainsTemplateString(block3) {
		t.Error("nil return value should not match")
	}
	// Non-matching stmt types
	block4 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.Ident{Name: "x"}},
	}}
	if bodyContainsTemplateString(block4) {
		t.Error("non-matching should return false")
	}
}

// --- inferParamType ---

func TestInferParamType(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"id", "Int"},
		{"userId", "Int"},
		{"page", "Int"},
		{"pageSize", "Int"},
		{"limit", "Int"},
		{"offset", "Int"},
		{"priority", "Int"},
		{"minutes", "Int"},
		{"quantity", "Int"},
		{"count", "Int"},
		{"isActive", "Boolean"},
		{"active", "Boolean"},
		{"published", "Boolean"},
		{"amount", "Float"},
		{"price", "Float"},
		{"balance", "Float"},
		{"score", "Float"},
		{"total", "Float"},
		{"budget", "Float"},
		{"name", "String"},
		{"email", "String"},
		{"unknown", "String"},
	}
	for _, tt := range tests {
		if got := inferParamType(tt.name); got != tt.want {
			t.Errorf("inferParamType(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// --- resolveParamTypeFromAST ---

func TestResolveParamTypeFromAST(t *testing.T) {
	old := apiParamTypes
	defer func() { apiParamTypes = old }()

	// No AST data — falls back to heuristic
	apiParamTypes = nil
	if got := resolveParamTypeFromAST("getUser", "id"); got != "Int" {
		t.Fatalf("fallback should use inferParamType, got %q", got)
	}

	// With AST data
	apiParamTypes = map[string]map[string]string{
		"getUser": {"id": "UUID"},
	}
	if got := resolveParamTypeFromAST("getUser", "id"); got != "UUID" {
		t.Fatalf("should use AST type, got %q", got)
	}
	if got := resolveParamTypeFromAST("svc:getUser", "id"); got != "UUID" {
		t.Fatalf("service API should use the unprefixed AST type, got %q", got)
	}
	// Missing param in AST — fallback
	if got := resolveParamTypeFromAST("getUser", "name"); got != "String" {
		t.Fatalf("missing param should fallback, got %q", got)
	}
}

// --- writeAPIRegistration ---

func TestWriteAPIRegistration(t *testing.T) {
	oldIDs := apiIDs
	oldParamIDs := apiParamIDs
	oldParamTypes := apiParamTypes
	defer func() {
		apiIDs = oldIDs
		apiParamIDs = oldParamIDs
		apiParamTypes = oldParamTypes
	}()

	// ID = 0 → early return
	apiIDs = map[string]int{}
	var b strings.Builder
	writeAPIRegistration(&b, "noApi")
	if b.Len() != 0 {
		t.Error("zero ID should produce no output")
	}

	// With ID and params
	apiIDs = map[string]int{"getUser": 5}
	apiParamIDs = map[string]map[string]int{
		"getUser": {"id": 2, "name": 1},
	}
	apiParamTypes = map[string]map[string]string{
		"getUser": {"id": "Int", "name": "String"},
	}
	b.Reset()
	writeAPIRegistration(&b, "getUser")
	code := b.String()
	if !strings.Contains(code, `Register("getUser", 5)`) {
		t.Errorf("missing Register call:\n%s", code)
	}
	if !strings.Contains(code, `RegisterParams("getUser"`) {
		t.Errorf("missing RegisterParams:\n%s", code)
	}
	if !strings.Contains(code, `"Int"`) {
		t.Errorf("missing type Int:\n%s", code)
	}
	if strings.Index(code, `Name: "name"`) > strings.Index(code, `Name: "id"`) {
		t.Errorf("parameters are not sorted by field ID:\n%s", code)
	}

	apiParamIDs["getUser"] = map[string]int{"ids": 2}
	apiParamTypes["getUser"] = map[string]string{"ids": "[UUID]?"}
	b.Reset()
	writeAPIRegistration(&b, "getUser")
	if code := b.String(); !strings.Contains(code, `Type: "UUID", FieldID: 2, IsList: true, Nullable: true`) {
		t.Errorf("missing list metadata:\n%s", code)
	}

	apiParamIDs["getUser"] = map[string]int{"id": 1, "removed": 9}
	apiParamTypes["getUser"] = map[string]string{"id": "Int"}
	b.Reset()
	writeAPIRegistration(&b, "getUser")
	if code := b.String(); strings.Contains(code, `Name: "removed"`) {
		t.Errorf("stale lock-file params must not be registered:\n%s", code)
	}
}

func TestWriteAPIRegistrationNoParams(t *testing.T) {
	oldIDs := apiIDs
	oldParamIDs := apiParamIDs
	defer func() {
		apiIDs = oldIDs
		apiParamIDs = oldParamIDs
	}()

	apiIDs = map[string]int{"ping": 1}
	apiParamIDs = nil

	var b strings.Builder
	writeAPIRegistration(&b, "ping")
	code := b.String()
	if !strings.Contains(code, `Register("ping", 1)`) {
		t.Errorf("missing Register:\n%s", code)
	}
	if strings.Contains(code, "RegisterParams") {
		t.Error("no params should skip RegisterParams")
	}
}

// --- paramMethod ---

func TestParamMethod(t *testing.T) {
	tests := []struct {
		goType string
		want   string
	}{
		{"int64", "Int"},
		{"float64", "Float"},
		{"string", "String"},
		{"time.Time", "DateTime"},
		{"bool", "Bool"},
		{"[]int64", "IntArray"},
		{"[]string", "StringArray"},
		{"CustomType", ""},
		{"map[string]any", ""},
	}
	for _, tt := range tests {
		if got := paramMethod(tt.goType); got != tt.want {
			t.Errorf("paramMethod(%q) = %q, want %q", tt.goType, got, tt.want)
		}
	}
}

// TestGenerateCompiledHandlers covers the compiled-handler path where api.Body != nil
// and @native is not set, exercising generateCompiledHandlers lines 91-94.
func TestGenerateCompiledHandlers(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
					testField("id", "Int", directive("id"), directive("auto")),
					testField("name", "String"),
				}),
			},
			APIs: []*ast.ApiDecl{
				{
					Name: "myCompiledApi",
					Body: &ast.Block{Stmts: []ast.Stmt{}}, // non-nil body, no @native
				},
			},
		}},
	}

	src := generateHandlerFile(result, "app", nil)
	if src == nil {
		t.Fatal("should generate handler file")
	}
	code := string(src)
	// The compiled handler should register "myCompiledApi"
	if !strings.Contains(code, "myCompiledApi") {
		t.Errorf("compiled API should be registered:\n%s", code)
	}
}

// ─── generateParamSet: nullable enum ────────────────────────────────────────

func TestGenerateParamSetNullableEnum(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "role",
		Type: &ast.TypeRef{Name: "Role", Nullable: true},
	}
	enums := map[string]bool{"Role": true}
	generateParamSet(&b, f, "SetRole", "\t\t", enums)
	out := b.String()
	if !strings.Contains(out, "ParamString") {
		t.Fatalf("nullable enum should use ParamString, got:\n%s", out)
	}
	if !strings.Contains(out, "roleValEnum") {
		t.Fatalf("nullable enum should create enum tmp var, got:\n%s", out)
	}
	if !strings.Contains(out, "&roleValEnum") {
		t.Fatalf("nullable enum should take pointer, got:\n%s", out)
	}
}

// ─── generateParamSet: custom JSON type ─────────────────────────────────────

func TestGenerateParamSetCustomType(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "metadata",
		Type: &ast.TypeRef{Name: "JSON"},
	}
	generateParamSet(&b, f, "SetMetadata", "\t\t", nil)
	out := b.String()
	if !strings.Contains(out, "ParamJSON") {
		t.Fatalf("custom type should use ParamJSON, got:\n%s", out)
	}
}

// ─── writeHandlerImports: all features enabled ──────────────────────────────

func TestWriteHandlerImportsAllFeatures(t *testing.T) {
	var b strings.Builder
	models := []*ast.ModelDecl{
		{
			Name:       "User",
			Directives: []*ast.Directive{{Name: "crud"}},
			Fields: []*ast.FieldDecl{
				{Name: "password", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hash"}}},
			},
		},
	}
	result := &semantic.Result{Files: []*ast.File{{}}}
	writeHandlerImports(&b, result, models, handlerFeatures{hasOrGroups: true, hasSortable: true, hasAwait: true, hasTransaction: true, hasTemplateStr: true, hasAuth: true}, true)
	out := b.String()
	if !strings.Contains(out, `"strconv"`) {
		t.Fatalf("hasOrGroups should add strconv import, got:\n%s", out)
	}
	if !strings.Contains(out, `"strings"`) {
		t.Fatalf("hasSortable should add strings import, got:\n%s", out)
	}
	if !strings.Contains(out, "errgroup") {
		t.Fatalf("hasAwait should add errgroup import, got:\n%s", out)
	}
	if !strings.Contains(out, "luvia") {
		t.Fatalf("hasAuth should add luvia import, got:\n%s", out)
	}
	if !strings.Contains(out, "luxocrypto") {
		t.Fatalf("hash model should add luxocrypto import, got:\n%s", out)
	}
}

func TestWriteHandlerImportsDecimalFromGeneratedBody(t *testing.T) {
	var b strings.Builder
	writeHandlerImports(&b, &semantic.Result{}, nil, handlerFeatures{}, false, "value := decimal.Zero")
	if out := b.String(); !strings.Contains(out, `"github.com/shopspring/decimal"`) {
		t.Fatalf("generated decimal use did not add import:\n%s", out)
	}
}

// ─── writeHandlerImports: hash only when CRUD has write ops ──────

func TestWriteHandlerImportsNoHashWithoutWriteOps(t *testing.T) {
	var b strings.Builder
	result := &semantic.Result{Files: []*ast.File{{}}}
	// Model has @hash but CRUD only has get/list (no create/update) — no luxocrypto needed
	models := []*ast.ModelDecl{{
		Name: "User",
		Directives: []*ast.Directive{{
			Name: "crud",
			Args: []*ast.NamedArg{{
				Name:  "only",
				Value: &ast.ListExpr{Items: []ast.Expr{&ast.Ident{Name: "get"}, &ast.Ident{Name: "list"}}},
			}},
		}},
		Fields: []*ast.FieldDecl{
			{Name: "password", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hash"}}},
		},
	}}
	writeHandlerImports(&b, result, models, handlerFeatures{}, true)
	out := b.String()
	if strings.Contains(out, "luxocrypto") {
		t.Fatalf("read-only CRUD with @hash should not add luxocrypto, got:\n%s", out)
	}
}

func TestWriteHandlerImportsUsesGeneratedBody(t *testing.T) {
	var b strings.Builder
	result := &semantic.Result{Files: []*ast.File{{}}}
	body := "func handle() { _, _ = luxocrypto.HashPassword(password) }"
	writeHandlerImports(&b, result, nil, handlerFeatures{hasEmit: true}, false, body)
	out := b.String()
	if !strings.Contains(out, "luxocrypto") {
		t.Fatalf("generated crypto reference should add import, got:\n%s", out)
	}
	if strings.Contains(out, `"fmt"`) || strings.Contains(out, `"strings"`) {
		t.Fatalf("unused imports should not be inferred from declarations, got:\n%s", out)
	}
}

func TestScanModelsForHash_NoCrud(t *testing.T) {
	// Model without @crud — should not trigger hash import
	models := []*ast.ModelDecl{{
		Name: "Config",
		Fields: []*ast.FieldDecl{
			{Name: "secret", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hash"}}},
		},
	}}
	if scanModelsForHash(models) {
		t.Error("model without @crud should not trigger hash import")
	}
}

func TestScanModelsForHash_CrudNoHash(t *testing.T) {
	// CRUD model without @hash — should not trigger
	models := []*ast.ModelDecl{{
		Name:       "Post",
		Directives: []*ast.Directive{{Name: "crud"}},
		Fields: []*ast.FieldDecl{
			{Name: "title", Type: &ast.TypeRef{Name: "String"}},
		},
	}}
	if scanModelsForHash(models) {
		t.Error("CRUD model without @hash should not trigger hash import")
	}
}

func TestScanForTimeImport_FnDurationParam(t *testing.T) {
	// fn with Duration param — should trigger time import
	result := &semantic.Result{Files: []*ast.File{{
		Functions: []*ast.FnDecl{{
			Name:   "sleep",
			Params: []*ast.ParamDecl{{Name: "d", Type: &ast.TypeRef{Name: "Duration"}}},
		}},
	}}}
	if !scanForTimeImport(result, nil) {
		t.Error("fn with Duration param should trigger time import")
	}
}

func TestScanForTimeImport_ApiDateTimeParam(t *testing.T) {
	// API with DateTime param — does NOT need time import (uses int64 Unix timestamp internally)
	result := &semantic.Result{Files: []*ast.File{{
		APIs: []*ast.ApiDecl{{
			Name:   "listByDate",
			Params: []*ast.ParamDecl{{Name: "since", Type: &ast.TypeRef{Name: "DateTime"}}},
		}},
	}}}
	if scanForTimeImport(result, nil) {
		t.Error("DateTime param should NOT trigger time import (uses ParamDateTime, no explicit time.Time)")
	}
}

func TestScanForTimeImport_ApiDurationParam(t *testing.T) {
	// API with Duration param — needs time import (time.Duration type in generated code)
	result := &semantic.Result{Files: []*ast.File{{
		APIs: []*ast.ApiDecl{{
			Name:   "delay",
			Params: []*ast.ParamDecl{{Name: "d", Type: &ast.TypeRef{Name: "Duration"}}},
		}},
	}}}
	if !scanForTimeImport(result, nil) {
		t.Error("Duration param should trigger time import")
	}
}

// ─── detectHandlerFeatures: sortable field ──────────────────────────────────

func TestDetectHandlerFeaturesSortable(t *testing.T) {
	models := []*ast.ModelDecl{
		{
			Name: "Post",
			Fields: []*ast.FieldDecl{
				{Name: "title", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "sortable"}}},
			},
		},
	}
	f := detectHandlerFeatures(&semantic.Result{}, models, nil, nil)
	if !f.hasSortable {
		t.Fatal("should detect sortable field")
	}
}

// ─── detectHandlerFeatures: withAuth on model ───────────────────────────────

func TestDetectHandlerFeaturesWithAuth(t *testing.T) {
	models := []*ast.ModelDecl{
		{
			Name:       "User",
			Directives: []*ast.Directive{{Name: "withAuth"}},
		},
	}
	f := detectHandlerFeatures(&semantic.Result{}, models, nil, nil)
	if !f.hasAuth {
		t.Fatal("should detect withAuth")
	}
}

// ─── writeFKEnsure: dedup and snake_case ────────────────────────────────────

func TestWriteFKEnsureDedupe(t *testing.T) {
	var b strings.Builder
	rels := []Relation{
		{LocalKey: "userId"},
		{LocalKey: "userId"}, // duplicate
		{LocalKey: "postId"},
	}
	writeFKEnsure(&b, rels)
	out := b.String()
	if strings.Count(out, "user_id") != 1 {
		t.Fatalf("should dedupe user_id, got:\n%s", out)
	}
	if !strings.Contains(out, "post_id") {
		t.Fatalf("should include post_id, got:\n%s", out)
	}
}

// --- fn @service ---

func TestGenerateServiceFnHandler(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/user.luxo",
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
					testField("id", "Int", directive("id"), directive("auto")),
					testField("name", "String"),
					testField("score", "Float"),
				}),
			},
			Functions: []*ast.FnDecl{
				{
					Name: "getUserScore",
					Params: []*ast.ParamDecl{
						{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
					},
					ReturnType: &ast.TypeRef{Name: "Float"},
					Directives: []*ast.Directive{{Name: "service"}},
					Body: &ast.Block{
						Stmts: []ast.Stmt{
							&ast.ReturnStmt{Value: &ast.Literal{Kind: token.Float, Value: "1.5"}},
						},
					},
				},
			},
		}},
	}

	src := generateHandlerFile(result, "luxo", nil)
	if src == nil {
		t.Fatal("should generate handler file")
	}
	code := string(src)

	// Should have fn handler
	if !strings.Contains(code, "handleGetUserScore") {
		t.Error("missing fn @service handler: handleGetUserScore")
	}

	// Should have RegisterServiceFns
	if !strings.Contains(code, "func RegisterServiceFns") {
		t.Error("missing RegisterServiceFns function")
	}

	// Should register with svc: prefix
	if !strings.Contains(code, `"svc:getUserScore"`) {
		t.Error("missing svc: prefix in registration")
	}
}

func TestGenerateNativeServiceFnHandler(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/user.luxo",
			Functions: []*ast.FnDecl{
				{
					Name: "processPayment",
					Params: []*ast.ParamDecl{
						{Name: "orderId", Type: &ast.TypeRef{Name: "Int"}},
					},
					ReturnType: &ast.TypeRef{Name: "Boolean"},
					Directives: []*ast.Directive{{Name: "native"}, {Name: "service"}},
					// Body is nil for @native
				},
			},
		}},
	}

	src := generateHandlerFile(result, "luxo", nil)
	if src == nil {
		t.Fatal("should generate handler file for @native @service fn")
	}
	code := string(src)

	// Should have native handler delegating to Resolver
	if !strings.Contains(code, "handleProcessPayment") {
		t.Error("missing @native @service handler")
	}
	if !strings.Contains(code, "app.Resolver.ProcessPayment") {
		t.Error("missing NativeResolver delegation")
	}
	if !strings.Contains(code, `"svc:processPayment"`) {
		t.Error("missing svc: prefix")
	}
}

func TestNativeAPIHandler(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/monitoring.luxo",
			Models: []*ast.ModelDecl{
				testModel("MetricBucket", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
					testField("id", "Int", directive("id"), directive("auto")),
				}),
			},
			APIs: []*ast.ApiDecl{
				{
					Name:       "getDashboardOverview",
					Directives: []*ast.Directive{{Name: "native"}, {Name: "auth"}},
					Params: []*ast.ParamDecl{
						{Name: "projectId", Type: &ast.TypeRef{Name: "Int"}},
						{Name: "startTime", Type: &ast.TypeRef{Name: "DateTime"}},
						{Name: "endTime", Type: &ast.TypeRef{Name: "DateTime"}},
					},
					ReturnType: &ast.TypeRef{Name: "DashboardOverview"},
				},
				{
					Name:       "getErrorBreakdown",
					Directives: []*ast.Directive{{Name: "native"}},
					Params: []*ast.ParamDecl{
						{Name: "projectId", Type: &ast.TypeRef{Name: "Int"}},
					},
					ReturnType: &ast.TypeRef{Name: "ErrorBreakdown", IsList: true},
				},
				{
					Name:       "countActiveUsers",
					Directives: []*ast.Directive{{Name: "native"}},
					ReturnType: &ast.TypeRef{Name: "Int"},
				},
			},
		}},
	}

	code := string(generateHandlerFile(result, "luxo", nil))

	// Handler functions
	if !strings.Contains(code, "handleGetDashboardOverview(app *App)") {
		t.Error("missing native API handler for getDashboardOverview")
	}
	if !strings.Contains(code, "handleGetErrorBreakdown(app *App)") {
		t.Error("missing native API handler for getErrorBreakdown")
	}

	// @auth check
	if !strings.Contains(code, "luvia.Identity(ctx)") {
		t.Error("@auth native API should have identity check")
	}

	// Resolver delegation
	if !strings.Contains(code, "app.Resolver.GetDashboardOverview(ctx") {
		t.Error("should delegate to Resolver")
	}

	// Scalar return encoding
	if !strings.Contains(code, "codec.AppendSvarint(req.Buf.B, result)") {
		t.Error("Int return should use AppendSvarint")
	}

	// Struct return encoding
	if !strings.Contains(code, "result.WriteLuxo(req.Buf, req.FieldMask)") {
		t.Error("struct return should use WriteLuxo")
	}

	// List of struct return — columnar (the wire protocol for ALL list
	// responses; Luvia's BinaryListToJSON reads columnar)
	if !strings.Contains(code, "WriteColumnarErrorBreakdown(req.Buf, result, req.FieldMask)") {
		t.Error("list of type should encode columnar")
	}
	if strings.Contains(code, "result[i].WriteLuxo(req.Buf, req.FieldMask)") {
		t.Error("list of struct must NOT be row-wise — Luvia can't decode it")
	}

	// Registration in RegisterHandlers
	if !strings.Contains(code, `router.Handle("getDashboardOverview"`) {
		t.Error("native API should be registered in RegisterHandlers")
	}
	if !strings.Contains(code, `router.Handle("getErrorBreakdown"`) {
		t.Error("native API should be registered in RegisterHandlers")
	}
}

func TestNativeAPIHandlerModelList(t *testing.T) {
	// Native API returning [Model] — resolver returns a VALUE slice []Model,
	// but WriteColumnarModel takes []*Model, so the handler needs an adapter.
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/user.luxo",
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
					testField("id", "Int", directive("id"), directive("auto")),
				}),
			},
			APIs: []*ast.ApiDecl{{
				Name:       "topUsers",
				Directives: []*ast.Directive{{Name: "native"}},
				ReturnType: &ast.TypeRef{Name: "User", IsList: true},
			}},
		}},
	}

	code := string(generateHandlerFile(result, "luxo", nil))
	if !strings.Contains(code, "_ptrs[i] = &result[i]") {
		t.Errorf("model list should build pointer slice adapter:\n%s", code)
	}
	if !strings.Contains(code, "WriteColumnarUser(req.Buf, _ptrs, req.FieldMask)") {
		t.Errorf("model list should encode columnar via pointer slice:\n%s", code)
	}
}

func TestNativeAPIHandlerReturnTypes(t *testing.T) {
	tests := []struct {
		retType  *ast.TypeRef
		contains string
	}{
		{&ast.TypeRef{Name: "Float"}, "codec.AppendFixed64"},
		{&ast.TypeRef{Name: "String"}, "codec.AppendString"},
		{&ast.TypeRef{Name: "Boolean"}, "codec.AppendBool"},
	}
	for _, tt := range tests {
		var b strings.Builder
		generateNativeAPIHandler(&b, &ast.ApiDecl{
			Name:       "test",
			Directives: []*ast.Directive{{Name: "native"}},
			ReturnType: tt.retType,
		}, nil, nil)
		if !strings.Contains(b.String(), tt.contains) {
			t.Errorf("return type %s should contain %q, got:\n%s", tt.retType.Name, tt.contains, b.String())
		}
	}
}

func TestNativeAPIHandlerListPrimitive(t *testing.T) {
	var b strings.Builder
	generateNativeAPIHandler(&b, &ast.ApiDecl{
		Name:       "getIds",
		Directives: []*ast.Directive{{Name: "native"}},
		ReturnType: &ast.TypeRef{Name: "Int", IsList: true},
	}, nil, nil)
	code := b.String()
	if !strings.Contains(code, "codec.AppendVarint(req.Buf.B, uint64(len(result)))") {
		t.Errorf("[Int] should use unsigned count prefix:\n%s", code)
	}
	if !strings.Contains(code, "codec.AppendSvarint(req.Buf.B, v)") {
		t.Errorf("[Int] should encode each element with AppendSvarint:\n%s", code)
	}

	var b2 strings.Builder
	generateNativeAPIHandler(&b2, &ast.ApiDecl{
		Name:       "getNames",
		Directives: []*ast.Directive{{Name: "native"}},
		ReturnType: &ast.TypeRef{Name: "String", IsList: true},
	}, nil, nil)
	if !strings.Contains(b2.String(), "codec.AppendString(req.Buf.B, v)") {
		t.Errorf("[String] should encode each element with AppendString:\n%s", b2.String())
	}

	var b3 strings.Builder
	generateNativeAPIHandler(&b3, &ast.ApiDecl{
		Name:       "getRatios",
		Directives: []*ast.Directive{{Name: "native"}},
		ReturnType: &ast.TypeRef{Name: "Float", IsList: true},
	}, nil, nil)
	if !strings.Contains(b3.String(), "codec.AppendFixed64(req.Buf.B, v)") {
		t.Errorf("[Float] should encode each element with AppendFixed64:\n%s", b3.String())
	}

	var b4 strings.Builder
	generateNativeAPIHandler(&b4, &ast.ApiDecl{
		Name:       "getFlags",
		Directives: []*ast.Directive{{Name: "native"}},
		ReturnType: &ast.TypeRef{Name: "Boolean", IsList: true},
	}, nil, nil)
	if !strings.Contains(b4.String(), "codec.AppendBool(req.Buf.B, v)") {
		t.Errorf("[Boolean] should encode each element with AppendBool:\n%s", b4.String())
	}
}

func TestNativeAPIHandlerAdvancedScalarReturns(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"DateTime", "codec.AppendSvarint(req.Buf.B, result.Unix())"},
		{"Duration", "codec.AppendSvarint(req.Buf.B, int64(result))"},
		{"UUID", "codec.AppendUUID(req.Buf.B, result)"},
		{"Decimal", "codec.AppendString(req.Buf.B, result.String())"},
		{"Bytes", "codec.AppendBytes(req.Buf.B, result)"},
		{"JSON", "codec.AppendBytes(req.Buf.B, result)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			generateNativeAPIHandler(&b, &ast.ApiDecl{
				Name:       "value",
				Directives: []*ast.Directive{{Name: "native"}},
				ReturnType: &ast.TypeRef{Name: tt.name},
			}, nil, nil)
			if got := b.String(); !strings.Contains(got, tt.want) {
				t.Fatalf("%s return missing %q:\n%s", tt.name, tt.want, got)
			}
		})
	}
}

func TestNativeServiceHandlerNoReturn(t *testing.T) {
	var b strings.Builder
	generateNativeServiceHandler(&b, &ast.FnDecl{
		Name:       "purge",
		Directives: []*ast.Directive{{Name: "native"}, {Name: "service"}},
		// no ReturnType
	}, nil, nil)
	code := b.String()
	if !strings.Contains(code, "err := app.Resolver.Purge(ctx)") {
		t.Errorf("void native function should only return an error:\n%s", code)
	}
	if strings.Contains(code, "result") {
		t.Errorf("void native function must not create a result value:\n%s", code)
	}
}

func TestServiceFnNotRegisteredWithoutAnnotation(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/user.luxo",
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective()}, []*ast.FieldDecl{
					testField("id", "Int", directive("id"), directive("auto")),
				}),
			},
			Functions: []*ast.FnDecl{
				{
					Name:       "internalHelper",
					Params:     []*ast.ParamDecl{},
					ReturnType: &ast.TypeRef{Name: "Int"},
					Body: &ast.Block{
						Stmts: []ast.Stmt{
							&ast.ReturnStmt{Value: &ast.Literal{Kind: token.Int, Value: "42"}},
						},
					},
					// No @service directive
				},
			},
		}},
	}

	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	// Internal fn without @service should NOT be registered
	if strings.Contains(code, "svc:internalHelper") {
		t.Error("fn without @service should not be registered as RPC")
	}
	// Should NOT have RegisterServiceFns (no service fns)
	if strings.Contains(code, "RegisterServiceFns") {
		t.Error("no service fns, should not generate RegisterServiceFns")
	}
}

func TestGenerateHandlerDeleteMany_IntID(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("User", []*ast.Directive{crudDirective(
					&ast.NamedArg{Name: "only", Value: &ast.ListExpr{Items: []ast.Expr{
						&ast.Ident{Name: "deleteMany"},
					}}},
				)}, []*ast.FieldDecl{
					testField("id", "Int", directive("id"), directive("auto")),
					testField("name", "String"),
				}),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	// Should use ParamIntArray for int64 ids
	if !strings.Contains(code, "req.ParamIntArray") {
		t.Error("deleteMany with Int ID should use ParamIntArray")
	}
	if strings.Contains(code, "json.Unmarshal") {
		t.Error("deleteMany with Int ID should NOT use json.Unmarshal")
	}
}

func TestGenerateHandlerDeleteMany_StringID(t *testing.T) {
	result := &semantic.Result{
		Files: []*ast.File{{
			Models: []*ast.ModelDecl{
				testModel("Product", []*ast.Directive{crudDirective(
					&ast.NamedArg{Name: "only", Value: &ast.ListExpr{Items: []ast.Expr{
						&ast.Ident{Name: "deleteMany"},
					}}},
				)}, []*ast.FieldDecl{
					testField("id", "String", directive("id")),
					testField("name", "String"),
				}),
			},
		}},
	}
	src := generateHandlerFile(result, "luxo", nil)
	code := string(src)

	// Should use ParamStringArray for string ids
	if !strings.Contains(code, "req.ParamStringArray") {
		t.Error("deleteMany with String ID should use ParamStringArray")
	}
}

// ─── Validation directive tests ──────────────────────────────────────────────

func TestGenerateStringValidation_NotBlank(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name:       "name",
		Type:       &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{{Name: "notBlank"}},
	}
	generateStringValidation(&b, f, "nameVal", "\t")
	if !strings.Contains(b.String(), "TrimSpace") {
		t.Error("@notBlank should generate TrimSpace check")
	}
}

func TestGenerateStringValidation_Email(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name:       "email",
		Type:       &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{{Name: "email"}},
	}
	generateStringValidation(&b, f, "emailVal", "\t")
	if !strings.Contains(b.String(), "@") {
		t.Error("@email should check for @")
	}
}

func TestGenerateStringValidation_MinMaxLength(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "username",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{
			{Name: "minLength", Args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.Int, Value: "3"}}}},
			{Name: "maxLength", Args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.Int, Value: "20"}}}},
		},
	}
	generateStringValidation(&b, f, "usernameVal", "\t")
	out := b.String()
	if !strings.Contains(out, "< 3") {
		t.Error("@minLength should check < 3")
	}
	if !strings.Contains(out, "> 20") {
		t.Error("@maxLength should check > 20")
	}
}

func TestGenerateStringValidation_Pattern(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "code",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{
			{Name: "pattern", Args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.String, Value: "^[A-Z]{3}$"}}}},
		},
	}
	generateStringValidation(&b, f, "codeVal", "\t")
	if !strings.Contains(b.String(), "regexp.MatchString") {
		t.Error("@pattern should use regexp")
	}
}

func TestGenerateNumericValidation_Range(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "age",
		Type: &ast.TypeRef{Name: "Int"},
		Directives: []*ast.Directive{
			{Name: "range", Args: []*ast.NamedArg{
				{Value: &ast.Literal{Kind: token.Int, Value: "0"}},
				{Value: &ast.Literal{Kind: token.Int, Value: "150"}},
			}},
		},
	}
	generateNumericValidation(&b, f, "ageVal", "\t")
	if !strings.Contains(b.String(), "< 0") || !strings.Contains(b.String(), "> 150") {
		t.Error("@range should check bounds")
	}
}

func TestWriteValidationCheck(t *testing.T) {
	var b strings.Builder
	writeValidationCheck(&b, "\t", "name", `nameVal == ""`, "must not be empty")
	out := b.String()
	if !strings.Contains(out, `nameVal == ""`) || !strings.Contains(out, "must not be empty") {
		t.Errorf("unexpected output: %s", out)
	}
}

// ─── Handler registration with @cache/@rateLimit ─────────────────────────────

func TestWriteHandlerRegistration_Cache(t *testing.T) {
	var b strings.Builder
	dirs := []*ast.Directive{
		{Name: "cache", Args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.Int, Value: "60"}}}},
	}
	writeHandlerRegistration(&b, "getUser", dirs)
	out := b.String()
	if !strings.Contains(out, "api.WithCache") {
		t.Errorf("@cache should wrap with WithCache: %s", out)
	}
}

func TestWriteHandlerRegistration_Plain(t *testing.T) {
	var b strings.Builder
	writeHandlerRegistration(&b, "getUser", nil)
	out := b.String()
	if strings.Contains(out, "WithCache") || strings.Contains(out, "WithRateLimit") {
		t.Errorf("no directives should not wrap: %s", out)
	}
	if !strings.Contains(out, "handleGetUser(app)") {
		t.Errorf("should register plain handler: %s", out)
	}
}

// ─── @beforeSave tests ──────────────────────────────────────────────────────

func TestGenerateBeforeSave_WithBody(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "name",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{
			{Name: "beforeSave", Body: &ast.Block{Stmts: []ast.Stmt{
				&ast.ExprStmt{Expr: &ast.CallExpr{
					Func: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "trim"},
				}},
			}}},
		},
	}
	generateBeforeSave(&b, f, "nameVal", "\t")
	if !strings.Contains(b.String(), "strings.TrimSpace") {
		t.Errorf("@beforeSave { it.trim() } should generate TrimSpace: %s", b.String())
	}
}

func TestGenerateBeforeSave_NoDirective(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{Name: "name", Type: &ast.TypeRef{Name: "String"}}
	generateBeforeSave(&b, f, "nameVal", "\t")
	if b.Len() > 0 {
		t.Error("no @beforeSave should generate nothing")
	}
}

// ─── Aggregate computed fields ──────────────────────────────────────────────

func TestGenerateComputedResolversBatchByRelation(t *testing.T) {
	oldFields := modelFieldIDs
	defer func() { modelFieldIDs = oldFields }()
	modelFieldIDs = map[string]map[string]int{
		"User": {"id": 1, "postCount": 4, "avgLikes": 5, "totalLikes": 6},
	}
	user := &ast.ModelDecl{
		Name:       "User",
		Directives: []*ast.Directive{{Name: "crud"}},
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
			computedAggregateField("postCount", "Int", "count", &ast.Ident{Name: "posts"}),
			computedAggregateField("avgLikes", "Float", "avg", &ast.MemberExpr{Object: &ast.Ident{Name: "posts"}, Field: "likes"}),
			computedAggregateField("totalLikes", "Int", "sum", &ast.MemberExpr{Object: &ast.Ident{Name: "posts"}, Field: "likes"}),
		},
	}
	post := &ast.ModelDecl{
		Name:       "Post",
		Directives: []*ast.Directive{{Name: "soft"}},
		Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "likes", Type: &ast.TypeRef{Name: "Int"}},
		},
	}
	models := map[string]*ast.ModelDecl{"User": user, "Post": post}

	var b strings.Builder
	generateComputedResolvers(&b, []*ast.ModelDecl{user, post}, models, map[string]bool{})
	out := b.String()

	for _, want := range []string{
		"func resolveUserComputed(",
		"codec.SelectionMaskFields(selectionMask)",
		"codec.FieldMaskHas(fieldMask, 4)",
		"codec.FieldMaskHas(fieldMask, 5)",
		"codec.FieldMaskHas(fieldMask, 6)",
		`SELECT "user_id", COUNT(*)::bigint, COALESCE(AVG("likes"), 0)::double precision, COALESCE(SUM("likes"), 0)::bigint FROM "posts" WHERE "user_id" = ANY($1) AND "deleted_at" IS NULL GROUP BY "user_id"`,
		"pg.QueryRaw(ctx, app.DB, query, keys)",
		"rows.Scan(&key, &value.PostCount, &value.AvgLikes, &value.TotalLikes)",
		"values := make(map[int64]userPostsComputedValue, len(items))",
		"item.PostCount = value.PostCount",
		"item.AvgLikes = value.AvgLikes",
		"item.TotalLikes = value.TotalLikes",
		"if err := rows.Err(); err != nil { return err }",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("computed resolver missing %q:\n%s", want, out)
		}
	}
	if got := strings.Count(out, "pg.QueryRaw("); got != 1 {
		t.Fatalf("same relation must use one batch query, got %d:\n%s", got, out)
	}
}

func TestGenerateComputedResolversCustomRelationKeys(t *testing.T) {
	oldFields := modelFieldIDs
	defer func() { modelFieldIDs = oldFields }()
	modelFieldIDs = map[string]map[string]int{"Product": {"sku": 1, "tenant": 2, "reviewCount": 3}}
	product := &ast.ModelDecl{
		Name: "Product",
		Fields: []*ast.FieldDecl{
			{Name: "sku", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "id"}}},
			{Name: "tenant", Type: &ast.TypeRef{Name: "String"}},
			{Name: "reviews", Type: &ast.TypeRef{Name: "Review", IsList: true}, Directives: []*ast.Directive{{Name: "by", Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "productTenant"}}, {Value: &ast.Ident{Name: "tenant"}}}}}},
			computedAggregateField("reviewCount", "Int", "count", &ast.Ident{Name: "reviews"}),
		},
	}
	review := &ast.ModelDecl{Name: "Review", Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "productTenant", Type: &ast.TypeRef{Name: "String"}},
	}}

	var b strings.Builder
	generateComputedResolvers(&b, []*ast.ModelDecl{product, review}, map[string]*ast.ModelDecl{"Product": product, "Review": review}, map[string]bool{})
	out := b.String()
	for _, want := range []string{
		`SELECT "product_tenant", COUNT(*)::bigint FROM "reviews" WHERE "product_tenant" = ANY($1) GROUP BY "product_tenant"`,
		"keys := make([]string, 0, len(items))",
		"values := make(map[string]productReviewsComputedValue, len(items))",
		"keys = append(keys, item.Tenant)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("custom-key resolver missing %q:\n%s", want, out)
		}
	}
	var selector strings.Builder
	generateSQLColumnSelector(&selector, product, map[string]bool{})
	generateSelectedSQLFields(&selector, product, map[string]bool{})
	for _, want := range []string{
		`case "reviewCount":`,
		`cols = ensureSelectedColumn(cols, "tenant")`,
		`if codec.FieldMaskHas(fieldMask, 3) { fields = ensureSelectedColumn(fields, "tenant") }`,
	} {
		if !strings.Contains(selector.String(), want) {
			t.Errorf("computed selection dependency missing %q:\n%s", want, selector.String())
		}
	}
}

func TestGenerateComputedResolversNoComputed(t *testing.T) {
	var b strings.Builder
	m := &ast.ModelDecl{Name: "User", Fields: []*ast.FieldDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}}
	generateComputedResolvers(&b, []*ast.ModelDecl{m}, map[string]*ast.ModelDecl{"User": m}, map[string]bool{})
	if b.Len() > 0 {
		t.Error("no computed fields should generate nothing")
	}
}

func TestNestedRelationResolvesComputedFieldsInOneBatch(t *testing.T) {
	oldFields := modelFieldIDs
	defer func() { modelFieldIDs = oldFields }()
	modelFieldIDs = map[string]map[string]int{
		"User":    {"id": 1, "posts": 2},
		"Post":    {"id": 1, "userId": 2, "comments": 3, "commentCount": 4},
		"Comment": {"id": 1, "postId": 2},
	}
	user := &ast.ModelDecl{Name: "User", Directives: []*ast.Directive{{Name: "crud"}}, Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
	}}
	post := &ast.ModelDecl{Name: "Post", Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "comments", Type: &ast.TypeRef{Name: "Comment", IsList: true}},
		computedAggregateField("commentCount", "Int", "count", &ast.Ident{Name: "comments"}),
	}}
	comment := &ast.ModelDecl{Name: "Comment", Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "postId", Type: &ast.TypeRef{Name: "Int"}},
	}}
	result := &semantic.Result{Files: []*ast.File{{Models: []*ast.ModelDecl{user, post, comment}}}}
	out := string(generateHandlerFile(result, "example", map[string]bool{}))
	for _, want := range []string{
		"func resolvePostComputedFields(",
		"case \"commentCount\": needComments = true",
		"computedItems := make([]*Post, 0)",
		"computedItems = append(computedItems, item.Posts...)",
		"resolvePostComputedFields(ctx, app, computedItems, f.Children)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("nested computed resolution missing %q:\n%s", want, out)
		}
	}
}

func TestComputedFieldLocalKeySkipsInvalidDirectives(t *testing.T) {
	model := &ast.ModelDecl{Name: "User", Fields: []*ast.FieldDecl{
		{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}},
	}}
	field := &ast.FieldDecl{
		Name: "invalid",
		Type: &ast.TypeRef{Name: "Int"},
		Computed: &ast.ComputedField{Directives: []*ast.Directive{
			{Name: "count"},
			{Name: "count", Args: []*ast.NamedArg{{Value: &ast.Literal{Kind: token.Int, Value: "1"}}}},
		}},
	}
	if key, ok := computedFieldLocalKey(model, field, nil); ok || key != "" {
		t.Fatalf("invalid computed directive resolved key %q", key)
	}
}

func TestNestedComputedResolveSupportsScalarRelations(t *testing.T) {
	relation := Relation{FieldName: "profile", TargetName: "Profile"}
	computed := map[string]bool{"Profile": true}
	var b strings.Builder
	writeNestedComputedResolve(&b, relation, "item.Profile", "\t", computed)
	writeNestedListComputedResolve(&b, relation, computed)
	out := b.String()
	for _, want := range []string{
		"[]*Profile{item.Profile}",
		"if item.Profile != nil { computedItems = append(computedItems, item.Profile) }",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("scalar computed relation missing %q:\n%s", want, out)
		}
	}
}

func TestCollectBatchLoadModelsIncludesRemotePrimaryKeyModel(t *testing.T) {
	oldContext := globalEventCtx
	defer func() { globalEventCtx = oldContext }()
	globalEventCtx = &EventContext{remotePKModels: map[string]bool{"Post": true}}
	user := &ast.ModelDecl{Name: "User"}
	post := &ast.ModelDecl{Name: "Post"}
	models := collectBatchLoadModels([]*ast.ModelDecl{user}, []*ast.ModelDecl{user, post})
	if len(models) != 2 || models[1] != post {
		t.Fatalf("batch load models = %#v", models)
	}
}

func TestWriteAPIRegistrationSkipsOnlyStaleParams(t *testing.T) {
	oldAPIs := apiIDs
	oldParams := apiParamIDs
	oldTypes := apiParamTypes
	defer func() {
		apiIDs = oldAPIs
		apiParamIDs = oldParams
		apiParamTypes = oldTypes
	}()
	apiIDs = map[string]int{"svc:lookup": 9}
	apiParamIDs = map[string]map[string]int{"svc:lookup": {"stale": 1}}
	apiParamTypes = map[string]map[string]string{"lookup": {"active": "String"}}
	var b strings.Builder
	writeAPIRegistration(&b, "svc:lookup")
	out := b.String()
	if !strings.Contains(out, `router.Registry.Register("svc:lookup", 9)`) || strings.Contains(out, "RegisterParams") {
		t.Fatalf("stale parameter registration output:\n%s", out)
	}
}

func TestComputedAggregateValidationBranches(t *testing.T) {
	oldFields := modelFieldIDs
	defer func() { modelFieldIDs = oldFields }()
	modelFieldIDs = map[string]map[string]int{"User": {"badArgs": 2}}

	missingID := computedAggregateField("missing", "Int", "count", &ast.Ident{Name: "posts"})
	if _, _, ok := parseComputedAggregate("User", missingID); ok {
		t.Fatal("computed field without a field ID was accepted")
	}
	badArgs := &ast.FieldDecl{
		Name:     "badArgs",
		Type:     &ast.TypeRef{Name: "Int"},
		Computed: &ast.ComputedField{Directives: []*ast.Directive{{Name: "count"}}},
	}
	if _, _, ok := parseComputedAggregate("User", badArgs); ok {
		t.Fatal("computed field without one directive argument was accepted")
	}

	tests := []struct {
		name string
		expr ast.Expr
	}{
		{name: "count", expr: &ast.Literal{Kind: token.Int, Value: "1"}},
		{name: "median", expr: &ast.Ident{Name: "posts"}},
		{name: "sum", expr: &ast.Ident{Name: "posts"}},
		{name: "sum", expr: &ast.MemberExpr{Object: &ast.Literal{Kind: token.Int, Value: "1"}, Field: "likes"}},
	}
	for _, test := range tests {
		if relation, target, ok := computedAggregateTarget(test.name, test.expr); ok || relation != "" || target != "" {
			t.Fatalf("invalid %s aggregate accepted: relation=%q target=%q", test.name, relation, target)
		}
	}
}

func TestModelFieldByNameHandlesNilAndMissingModel(t *testing.T) {
	if field := modelFieldByName(nil, "id"); field != nil {
		t.Fatalf("nil model returned field %#v", field)
	}
	model := &ast.ModelDecl{Name: "User", Fields: []*ast.FieldDecl{{Name: "id"}}}
	if field := modelFieldByName(model, "missing"); field != nil {
		t.Fatalf("missing field returned %#v", field)
	}
}

func computedAggregateField(name, typeName, directive string, target ast.Expr) *ast.FieldDecl {
	return &ast.FieldDecl{
		Name: name,
		Type: &ast.TypeRef{Name: typeName},
		Computed: &ast.ComputedField{Directives: []*ast.Directive{{
			Name: directive,
			Args: []*ast.NamedArg{{Value: target}},
		}}},
	}
}

func TestScanModelsForValidation_Pattern(t *testing.T) {
	models := []*ast.ModelDecl{{
		Name:       "User",
		Directives: []*ast.Directive{{Name: "crud"}},
		Fields: []*ast.FieldDecl{
			{Name: "code", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "pattern"}}},
		},
	}}
	hasVal, hasPat := scanModelsForValidation(models)
	if !hasVal || !hasPat {
		t.Error("@pattern should set both hasValidation and hasPattern")
	}
}

func TestScanModelsForValidation_NoCrud(t *testing.T) {
	models := []*ast.ModelDecl{{
		Name: "Config",
		Fields: []*ast.FieldDecl{
			{Name: "name", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "notBlank"}}},
		},
	}}
	hasVal, _ := scanModelsForValidation(models)
	if hasVal {
		t.Error("non-CRUD model should not trigger validation")
	}
}

func TestGenerateBeforeSave_EmptyBody(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "name",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{
			{Name: "beforeSave", Body: &ast.Block{}},
		},
	}
	generateBeforeSave(&b, f, "nameVal", "\t")
	if b.Len() > 0 {
		t.Error("empty body should generate nothing")
	}
}

func TestGenerateBeforeSave_NonExprBody(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "name",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{
			{Name: "beforeSave", Body: &ast.Block{Stmts: []ast.Stmt{&ast.ReturnStmt{}}}},
		},
	}
	generateBeforeSave(&b, f, "nameVal", "\t")
	if b.Len() > 0 {
		t.Error("non-ExprStmt body should generate nothing")
	}
}

func TestGenerateBeforeSave_AssignTrim(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "name",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{
			{Name: "beforeSave", Body: &ast.Block{Stmts: []ast.Stmt{
				&ast.AssignStmt{
					Target: &ast.Ident{Name: "it"},
					Op:     "=",
					Value: &ast.CallExpr{
						Func: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "trim"},
					},
				},
			}}},
		},
	}
	generateBeforeSave(&b, f, "nameVal", "\t")
	if !strings.Contains(b.String(), "strings.TrimSpace(nameVal)") {
		t.Errorf("@beforeSave { it = it.trim() } should generate TrimSpace: %s", b.String())
	}
}

func TestGenerateBeforeSave_AssignFreeFunc(t *testing.T) {
	var b strings.Builder
	f := &ast.FieldDecl{
		Name: "slug",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{
			{Name: "beforeSave", Body: &ast.Block{Stmts: []ast.Stmt{
				&ast.AssignStmt{
					Target: &ast.Ident{Name: "it"},
					Op:     "=",
					Value: &ast.CallExpr{
						Func: &ast.Ident{Name: "slugify"},
						Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "it"}}},
					},
				},
			}}},
		},
	}
	generateBeforeSave(&b, f, "slugVal", "\t")
	out := b.String()
	if !strings.Contains(out, "slugify(slugVal)") {
		t.Errorf("@beforeSave { it = slugify(it) } should generate slugify call: %s", out)
	}
}

func TestGenerateBeforeSave_AssignChained(t *testing.T) {
	var b strings.Builder
	// it = it.trim().lowercase()
	f := &ast.FieldDecl{
		Name: "email",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{
			{Name: "beforeSave", Body: &ast.Block{Stmts: []ast.Stmt{
				&ast.AssignStmt{
					Target: &ast.Ident{Name: "it"},
					Op:     "=",
					Value: &ast.CallExpr{
						Func: &ast.MemberExpr{
							Object: &ast.CallExpr{
								Func: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "trim"},
							},
							Field: "lowercase",
						},
					},
				},
			}}},
		},
	}
	generateBeforeSave(&b, f, "emailVal", "\t")
	out := b.String()
	if !strings.Contains(out, "strings.ToLower(strings.TrimSpace(emailVal))") {
		t.Errorf("@beforeSave { it = it.trim().lowercase() } should chain: %s", out)
	}
}

func TestGenerateBeforeSave_MultipleStmts(t *testing.T) {
	var b strings.Builder
	// Two statements: it = it.trim(); it = it.lowercase()
	f := &ast.FieldDecl{
		Name: "name",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{
			{Name: "beforeSave", Body: &ast.Block{Stmts: []ast.Stmt{
				&ast.AssignStmt{
					Target: &ast.Ident{Name: "it"},
					Op:     "=",
					Value: &ast.CallExpr{
						Func: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "trim"},
					},
				},
				&ast.AssignStmt{
					Target: &ast.Ident{Name: "it"},
					Op:     "=",
					Value: &ast.CallExpr{
						Func: &ast.MemberExpr{Object: &ast.Ident{Name: "it"}, Field: "lowercase"},
					},
				},
			}}},
		},
	}
	generateBeforeSave(&b, f, "nameVal", "\t")
	out := b.String()
	if !strings.Contains(out, "TrimSpace") {
		t.Errorf("should contain TrimSpace: %s", out)
	}
	if !strings.Contains(out, "ToLower") {
		t.Errorf("should contain ToLower: %s", out)
	}
}

func TestGenerateBeforeSave_FreeFuncMultiArgs(t *testing.T) {
	var b strings.Builder
	// it = concat(it, "suffix")
	f := &ast.FieldDecl{
		Name: "title",
		Type: &ast.TypeRef{Name: "String"},
		Directives: []*ast.Directive{
			{Name: "beforeSave", Body: &ast.Block{Stmts: []ast.Stmt{
				&ast.AssignStmt{
					Target: &ast.Ident{Name: "it"},
					Op:     "=",
					Value: &ast.CallExpr{
						Func: &ast.Ident{Name: "concat"},
						Args: []*ast.NamedArg{
							{Value: &ast.Ident{Name: "it"}},
							{Value: &ast.Literal{Kind: token.String, Value: "suffix"}},
						},
					},
				},
			}}},
		},
	}
	generateBeforeSave(&b, f, "titleVal", "\t")
	out := b.String()
	if !strings.Contains(out, `concat(titleVal, "suffix")`) {
		t.Errorf("should generate concat with two args: %s", out)
	}
}

func TestCRUDHandlerSkipDuplicate(t *testing.T) {
	models := []*ast.ModelDecl{
		{Name: "Project", Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}, {Name: "auto"}, {Name: "serial"}}},
			{Name: "name", Type: &ast.TypeRef{Name: "String"}},
		}, Directives: []*ast.Directive{{Name: "crud"}}},
	}
	skipNames := map[string]bool{"createProject": true, "deleteProject": true}
	var b strings.Builder
	generateCRUDHandlers(&b, models, nil, skipNames)
	code := b.String()
	if strings.Contains(code, "func handleCreateProject(") {
		t.Error("should skip createProject (overridden by compiled API)")
	}
	if strings.Contains(code, "func handleDeleteProject(") {
		t.Error("should skip deleteProject (overridden by compiled API)")
	}
	if !strings.Contains(code, "func handleGetProject(") {
		t.Error("should still have getProject")
	}
}

func TestCRUDHandlerSkipDebug(t *testing.T) {
	models := []*ast.ModelDecl{
		{Name: "Project", Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}, {Name: "auto"}, {Name: "serial"}}},
		}, Directives: []*ast.Directive{{Name: "crud"}}},
	}
	ops := crudOperations(models[0])
	t.Logf("ops: %v", ops)
	for _, op := range ops {
		t.Logf("crudAPIName(Project, %s) = %s", op, crudAPIName("Project", op))
	}
}

func TestScanBodyForBuiltins(t *testing.T) {
	// crypto.randomHex() → hasCrypto
	body := &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.CallExpr{
			Func: &ast.MemberExpr{Object: &ast.Ident{Name: "crypto"}, Field: "randomHex"},
		}},
	}}
	var f handlerFeatures
	scanBodyForBuiltins(body, &f)
	if !f.hasCrypto {
		t.Error("should detect crypto usage")
	}

	// now() → hasTimeFunc
	body2 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ValStmt{Name: "t", Value: &ast.CallExpr{
			Func: &ast.Ident{Name: "now"},
		}},
	}}
	var f2 handlerFeatures
	scanBodyForBuiltins(body2, &f2)
	if !f2.hasTimeFunc {
		t.Error("should detect now() usage")
	}

	// n.days → hasTimeFunc
	body3 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ValStmt{Name: "d", Value: &ast.MemberExpr{
			Object: &ast.Ident{Name: "n"}, Field: "days",
		}},
	}}
	var f3 handlerFeatures
	scanBodyForBuiltins(body3, &f3)
	if !f3.hasTimeFunc {
		t.Error("should detect duration property usage")
	}

	// nil body
	var f4 handlerFeatures
	scanBodyForBuiltins(nil, &f4)
	if f4.hasCrypto || f4.hasTimeFunc {
		t.Error("nil body should not set flags")
	}
}

func TestScanBodyForBuiltinsEmit(t *testing.T) {
	old := globalEventCtx
	defer func() { globalEventCtx = old }()

	globalEventCtx = &EventContext{
		EventModule: map[string]string{"ProjectDeleted": "common"},
		ModulePath:  "github.com/test/service",
	}

	body := &ast.Block{Stmts: []ast.Stmt{
		&ast.EmitStmt{EventName: "ProjectDeleted"},
	}}
	var f handlerFeatures
	scanBodyForBuiltins(body, &f, "project")
	if !f.hasEmit {
		t.Error("should detect emit")
	}
	if f.crossEventImports["common"] != "common_luxo" {
		t.Errorf("should have cross-module import, got %v", f.crossEventImports)
	}
}

func TestScanBodyForBuiltinsNestedEmit(t *testing.T) {
	old := globalEventCtx
	defer func() { globalEventCtx = old }()

	globalEventCtx = &EventContext{
		EventModule: map[string]string{"ProjectDeleted": "common"},
		ModulePath:  "github.com/test/service",
	}

	// Emit inside IfStmt
	body := &ast.Block{Stmts: []ast.Stmt{
		&ast.IfStmt{
			Condition: &ast.Literal{Kind: token.Int, Value: "1"},
			Then: &ast.Block{Stmts: []ast.Stmt{
				&ast.EmitStmt{EventName: "ProjectDeleted"},
			}},
		},
	}}
	var f handlerFeatures
	scanBodyForBuiltins(body, &f, "project")
	if !f.hasEmit {
		t.Error("should detect emit inside if")
	}
	if f.crossEventImports["common"] != "common_luxo" {
		t.Error("should detect cross-module import from nested emit")
	}

	// Emit inside ForStmt
	body2 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ForStmt{
			VarName:    "item",
			Collection: &ast.Ident{Name: "items"},
			Body: &ast.Block{Stmts: []ast.Stmt{
				&ast.EmitStmt{EventName: "ProjectDeleted"},
			}},
		},
	}}
	var f2 handlerFeatures
	scanBodyForBuiltins(body2, &f2, "project")
	if !f2.hasEmit {
		t.Error("should detect emit inside for")
	}

	// Emit inside TransactionExpr
	body3 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.TransactionExpr{
			Body: &ast.Block{Stmts: []ast.Stmt{
				&ast.EmitStmt{EventName: "ProjectDeleted"},
			}},
		}},
	}}
	var f3 handlerFeatures
	scanBodyForBuiltins(body3, &f3, "project")
	if !f3.hasEmit {
		t.Error("should detect emit inside transaction")
	}

	// Emit inside AsyncExpr
	body4 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.AsyncExpr{
			Body: &ast.Block{Stmts: []ast.Stmt{
				&ast.EmitStmt{EventName: "ProjectDeleted"},
			}},
		}},
	}}
	var f4 handlerFeatures
	scanBodyForBuiltins(body4, &f4, "project")
	if !f4.hasEmit {
		t.Error("should detect emit inside async")
	}

	// Emit inside AwaitExpr
	body5 := &ast.Block{Stmts: []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.AwaitExpr{
			Body: &ast.Block{Stmts: []ast.Stmt{
				&ast.EmitStmt{EventName: "ProjectDeleted"},
			}},
		}},
	}}
	var f5 handlerFeatures
	scanBodyForBuiltins(body5, &f5, "project")
	if !f5.hasEmit {
		t.Error("should detect emit inside await")
	}
}

func TestScanBodyForBuiltinsLocalEmit(t *testing.T) {
	old := globalEventCtx
	defer func() { globalEventCtx = old }()

	globalEventCtx = &EventContext{
		EventModule: map[string]string{"TraceIngested": "monitoring"},
	}

	body := &ast.Block{Stmts: []ast.Stmt{
		&ast.EmitStmt{EventName: "TraceIngested"},
	}}
	var f handlerFeatures
	scanBodyForBuiltins(body, &f, "monitoring")
	if !f.hasEmit {
		t.Error("should detect emit")
	}
	if len(f.crossEventImports) != 0 {
		t.Error("local emit should not generate cross-module import")
	}
}

func TestWriteSortedCrossModuleImports(t *testing.T) {
	old := globalEventCtx
	defer func() { globalEventCtx = old }()

	globalEventCtx = &EventContext{
		ModulePath: "github.com/test/service",
	}

	var b strings.Builder
	imports := map[string]string{
		"common":     "common_luxo",
		"monitoring": "monitoring_luxo",
		"auth":       "auth_luxo",
	}
	writeSortedCrossModuleImports(&b, imports)
	out := b.String()

	// Should be sorted alphabetically: auth, common, monitoring
	authIdx := strings.Index(out, "auth_luxo")
	commonIdx := strings.Index(out, "common_luxo")
	monitorIdx := strings.Index(out, "monitoring_luxo")
	if authIdx < 0 || commonIdx < 0 || monitorIdx < 0 {
		t.Fatalf("missing imports in:\n%s", out)
	}
	if !(authIdx < commonIdx && commonIdx < monitorIdx) {
		t.Errorf("imports not sorted:\n%s", out)
	}

	// No context → no output
	globalEventCtx = nil
	var b2 strings.Builder
	writeSortedCrossModuleImports(&b2, imports)
	if b2.Len() != 0 {
		t.Error("should produce nothing without event context")
	}

	// Empty imports → no output
	globalEventCtx = &EventContext{ModulePath: "github.com/test/service"}
	var b3 strings.Builder
	writeSortedCrossModuleImports(&b3, nil)
	if b3.Len() != 0 {
		t.Error("should produce nothing with empty imports")
	}
}

func TestNeedsFmtImport(t *testing.T) {
	// With emit
	if !needsFmtImport(nil, true) {
		t.Error("emit should need fmt")
	}

	// No emit, no models
	if needsFmtImport(nil, false) {
		t.Error("should not need fmt without emit or create/update")
	}
}

// ─── Native-only module generates handler.gen.go ─────────────────────────────

func TestNativeOnlyModuleGeneratesHandler(t *testing.T) {
	// Module with ONLY native APIs (no models, no CRUD, no compiled APIs)
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/admin.luxo",
			APIs: []*ast.ApiDecl{
				{
					Name:       "getStats",
					Directives: []*ast.Directive{{Name: "native"}},
					ReturnType: &ast.TypeRef{Name: "Stats"},
				},
				{
					Name:       "resetCache",
					Directives: []*ast.Directive{{Name: "native"}},
				},
			},
		}},
	}

	src := generateHandlerFile(result, "luxo", nil)
	if src == nil {
		t.Fatal("native-only module should generate handler file")
	}
	code := string(src)

	if !strings.Contains(code, "handleGetStats") {
		t.Error("missing native API handler for getStats")
	}
	if !strings.Contains(code, "handleResetCache") {
		t.Error("missing native API handler for resetCache")
	}
	if !strings.Contains(code, "RegisterHandlers") {
		t.Error("missing RegisterHandlers")
	}
}

func TestHandlerImportsNoModels(t *testing.T) {
	// Module with no models should NOT import lux/selection or lux
	result := &semantic.Result{
		Files: []*ast.File{{
			Name: "origin/admin.luxo",
			APIs: []*ast.ApiDecl{
				{
					Name:       "ping",
					Directives: []*ast.Directive{{Name: "native"}},
				},
			},
		}},
	}

	src := generateHandlerFile(result, "luxo", nil)
	if src == nil {
		t.Fatal("should generate handler file")
	}
	code := string(src)

	// Should NOT have "lux/selection" import when no models
	if strings.Contains(code, `"github.com/light-speak/luxo/pkg/lux/selection"`) {
		t.Error("should not import lux/selection when no models")
	}
	// Should NOT have standalone lux import when no models
	if strings.Contains(code, `"github.com/light-speak/luxo/pkg/lux"`) && !strings.Contains(code, `"github.com/light-speak/luxo/pkg/lux/`) {
		t.Error("should not import lux when no models")
	}
}
