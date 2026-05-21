package semantic

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// ========== Field Type Resolution Tests ==========

func TestFieldTypeResolution(t *testing.T) {
	result := analyze(t, `
enum Role { USER ADMIN }
model User {
  name: String
  role: Role
  age: Int
}
`)
	expectNoErrors(t, result)

	user := result.Types["User"]
	nameField := user.Fields["name"]
	if nameField.Type.Kind != TypeString {
		t.Errorf("expected String type for name, got %v", nameField.Type.Kind)
	}
	roleField := user.Fields["role"]
	if roleField.Type.Kind != TypeEnum {
		t.Errorf("expected Enum type for role, got %v", roleField.Type.Kind)
	}
}

func TestUnknownFieldType(t *testing.T) {
	result := analyze(t, `model User { name: Stirng }`)
	expectError(t, result, "unknown type 'Stirng'")
}

func TestUnknownFieldTypeSuggestion(t *testing.T) {
	result := analyze(t, `model User { name: Stirng }`)
	for _, err := range result.Errors {
		if strings.Contains(err.Message, "Stirng") {
			if err.Suggestion == "" || !strings.Contains(err.Suggestion, "String") {
				t.Errorf("expected suggestion 'String', got %q", err.Suggestion)
			}
			return
		}
	}
	t.Error("expected error for 'Stirng'")
}

func TestNullableField(t *testing.T) {
	result := analyze(t, `model User { avatar: String? }`)
	expectNoErrors(t, result)

	user := result.Types["User"]
	if !user.Fields["avatar"].Nullable {
		t.Error("expected avatar to be nullable")
	}
}

func TestListField(t *testing.T) {
	result := analyze(t, `
model Post { title: String }
model User { posts: [Post] }
`)
	expectNoErrors(t, result)

	user := result.Types["User"]
	postsField := user.Fields["posts"]
	if !postsField.Type.IsList {
		t.Error("expected posts to be list type")
	}
}

func TestDuplicateField(t *testing.T) {
	result := analyze(t, `model User {
  name: String
  name: String
}`)
	expectError(t, result, "duplicate field")
}

// ========== Computed Field Tests ==========

func TestComputedField(t *testing.T) {
	result := analyze(t, `model Post {
  title: String
  val totalCount: Int get @count
  val avgLikes: Float get @avg(field: likes)
}`)
	expectNoErrors(t, result)

	post := result.Types["Post"]
	tc := post.Fields["totalCount"]
	if tc == nil || !tc.Computed {
		t.Error("expected computed field totalCount")
	}
}

// ========== Inheritance Tests ==========

func TestInheritance(t *testing.T) {
	result := analyze(t, `
model Base {
  id: Int
  createdAt: DateTime
}
model User : Base {
  name: String
}
`)
	expectNoErrors(t, result)

	user := result.Types["User"]
	if len(user.Parents) != 1 {
		t.Fatalf("expected 1 parent, got %d", len(user.Parents))
	}
	if user.Parents[0].Name != "Base" {
		t.Errorf("expected parent 'Base', got '%s'", user.Parents[0].Name)
	}
	// should be able to lookup inherited field
	field := user.LookupField("id")
	if field == nil {
		t.Error("expected inherited field 'id'")
	}
}

func TestInheritanceNotFound(t *testing.T) {
	result := analyze(t, `model User : NonExistent { name: String }`)
	expectError(t, result, "not found")
}

func TestMultipleParentsInheritance(t *testing.T) {
	result := analyze(t, `
model Base {
  id: Int
  createdAt: DateTime
}
model Auditable {
  updatedAt: DateTime
  updatedBy: String
}
model User : Base, Auditable {
  name: String
}
`)
	expectNoErrors(t, result)

	user := result.Types["User"]
	if user == nil {
		t.Fatal("expected User type")
	}
	if len(user.Parents) != 2 {
		t.Fatalf("expected 2 parents, got %d", len(user.Parents))
	}

	// Should inherit fields from both parents
	idField := user.LookupField("id")
	if idField == nil {
		t.Error("expected inherited field 'id' from Base")
	}
	updatedByField := user.LookupField("updatedBy")
	if updatedByField == nil {
		t.Error("expected inherited field 'updatedBy' from Auditable")
	}
}

// ========== Directive Validation Tests ==========

func TestHashOnNonString(t *testing.T) {
	result := analyze(t, `model User { age: Int @hash }`)
	expectError(t, result, "@hash can only be used on String")
}

func TestEmailOnNonString(t *testing.T) {
	result := analyze(t, `model User { age: Int @email }`)
	expectError(t, result, "@email can only be used on String")
}

func TestRangeOnNonNumeric(t *testing.T) {
	result := analyze(t, `model User { name: String @range(0, 150) }`)
	expectError(t, result, "@range can only be used on numeric")
}

func TestValidDirectives(t *testing.T) {
	result := analyze(t, `model User {
  name: String @varchar(100) @filterable
  email: String @unique @hidden
  password: String @hash
  age: Int @sortable
}`)
	expectNoErrors(t, result)
}

func TestUnknownDirectiveError(t *testing.T) {
	result := analyze(t, `
model User @foobar {
  name: String
}
`)
	expectError(t, result, "unknown directive '@foobar'")
}

func TestApiUnknownDirectiveError(t *testing.T) {
	result := analyze(t, `
api test(): Int @unknownDir
`)
	expectError(t, result, "unknown directive '@unknownDir'")
}

func TestResolveFieldDeclDefaultValue(t *testing.T) {
	result := analyze(t, `
model User {
  name: String = "unknown"
  active: Boolean
}
`)
	expectNoErrors(t, result)
	user := result.Types["User"]
	if user == nil {
		t.Fatal("expected User type")
	}
	nameField := user.Fields["name"]
	if nameField == nil {
		t.Fatal("expected 'name' field")
	}
	if !nameField.HasDefault {
		t.Error("expected HasDefault to be true for field with default value")
	}
}

func TestFieldNotFoundSuggestion(t *testing.T) {
	// Construct AST directly so we access a field on a known type
	a := New()
	a.declareType("User", TypeModel, token.Position{}, "")
	userType := a.types["User"]
	userType.Fields["name"] = &FieldInfo{Name: "name", Type: &ResolvedType{Kind: TypeString, Name: "String"}}
	userType.Fields["email"] = &FieldInfo{Name: "email", Type: &ResolvedType{Kind: TypeString, Name: "String"}}

	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: &ast.TypeRef{Name: "String"},
				Body: &ast.Block{
					Stmts: []ast.Stmt{
						&ast.ExprStmt{
							Expr: &ast.MemberExpr{
								Object: &ast.Ident{Name: "User"},
								Field:  "nme",
							},
						},
					},
				},
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	found := false
	for _, err := range result.Errors {
		if strings.Contains(err.Message, "has no field 'nme'") {
			found = true
			if err.Suggestion == "" || !strings.Contains(err.Suggestion, "name") {
				t.Errorf("expected suggestion containing 'name', got %q", err.Suggestion)
			}
		}
	}
	if !found {
		t.Errorf("expected field-not-found error for 'nme', got errors: %v", result.Errors)
	}
}

func TestFieldNamesHelper(t *testing.T) {
	typ := &ResolvedType{
		Kind: TypeModel,
		Name: "User",
		Fields: map[string]*FieldInfo{
			"name":  {Name: "name"},
			"email": {Name: "email"},
			"age":   {Name: "age"},
		},
	}
	names := fieldNames(typ)
	if len(names) != 3 {
		t.Errorf("expected 3 field names, got %d", len(names))
	}
}

func TestNilFieldsMapInit(t *testing.T) {
	result := analyze(t, `
interface Searchable {
  query: String
}
`)
	expectNoErrors(t, result)

	typ := result.Types["Searchable"]
	if typ == nil {
		t.Fatal("expected type Searchable")
	}
	if typ.Fields == nil {
		t.Error("expected Fields map to be initialized")
	}
	if _, ok := typ.Fields["query"]; !ok {
		t.Error("expected field 'query'")
	}
}

func TestNilFieldsMapInitType(t *testing.T) {
	result := analyze(t, `
type AuthResult {
  token: String
  success: Boolean
}
`)
	expectNoErrors(t, result)

	typ := result.Types["AuthResult"]
	if typ == nil {
		t.Fatal("expected type AuthResult")
	}
	if typ.Fields == nil {
		t.Error("expected Fields map to be initialized")
	}
	if _, ok := typ.Fields["token"]; !ok {
		t.Error("expected field 'token'")
	}
}

func TestDuplicateModelFieldsResolveSkip(t *testing.T) {
	// When a model name conflicts with a builtin, typ will be nil in resolveModelFields
	// since declareType returns nil for duplicates. We need the continue path.
	result := analyze(t, `
model String { name: String }
`)
	// "String" is a builtin type, so this should produce a "already declared" error
	expectError(t, result, "already declared")
}

func TestDuplicateInterfaceFieldsResolveSkip(t *testing.T) {
	result := analyze(t, `
interface Int { value: String }
`)
	expectError(t, result, "already declared")
}

func TestDuplicateTypeFieldsResolveSkip(t *testing.T) {
	result := analyze(t, `
type Boolean { flag: String }
`)
	expectError(t, result, "already declared")
}

func TestResolveFieldsNilModelType(t *testing.T) {
	// Exercise the typ == nil guard in resolveFields for models (line 209).
	a := New()
	file := &ast.File{
		Models: []*ast.ModelDecl{
			{
				Name: "Ghost",
				Fields: []*ast.FieldDecl{
					{Name: "x", Type: &ast.TypeRef{Name: "String"}},
				},
			},
		},
	}
	a.resolveFields(file)
	if len(a.errors) > 0 {
		t.Errorf("expected no errors for skipped nil model, got %v", a.errors)
	}
}

func TestResolveFieldsNilInterfaceType(t *testing.T) {
	// Exercise the typ == nil guard in resolveFields for interfaces (line 227).
	a := New()
	file := &ast.File{
		Interfaces: []*ast.InterfaceDecl{
			{
				Name: "Ghost",
				Fields: []*ast.FieldDecl{
					{Name: "x", Type: &ast.TypeRef{Name: "String"}},
				},
			},
		},
	}
	a.resolveFields(file)
	if len(a.errors) > 0 {
		t.Errorf("expected no errors for skipped nil interface, got %v", a.errors)
	}
}

func TestResolveFieldsNilCustomType(t *testing.T) {
	// Exercise the typ == nil guard in resolveFields for custom types (line 240).
	a := New()
	file := &ast.File{
		Types: []*ast.TypeDecl{
			{
				Name: "Ghost",
				Fields: []*ast.FieldDecl{
					{Name: "x", Type: &ast.TypeRef{Name: "String"}},
				},
			},
		},
	}
	a.resolveFields(file)
	if len(a.errors) > 0 {
		t.Errorf("expected no errors for skipped nil custom type, got %v", a.errors)
	}
}

func TestResolveFieldsNilApiSymbol(t *testing.T) {
	// Exercise the sym == nil guard in resolveFields for apis (line 254).
	a := New()
	// Don't define the api in scope, but include it in the file
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "ghostApi",
				ReturnType: &ast.TypeRef{Name: "String"},
			},
		},
	}
	a.resolveFields(file)
	if len(a.errors) > 0 {
		t.Errorf("expected no errors for skipped nil api symbol, got %v", a.errors)
	}
}

func TestResolveFieldsNilFnSymbol(t *testing.T) {
	// Exercise the sym == nil guard in resolveFields for fns (line 269).
	a := New()
	// Don't define the fn in scope, but include it in the file
	file := &ast.File{
		Functions: []*ast.FnDecl{
			{
				Name:       "ghostFn",
				ReturnType: &ast.TypeRef{Name: "String"},
			},
		},
	}
	a.resolveFields(file)
	if len(a.errors) > 0 {
		t.Errorf("expected no errors for skipped nil fn symbol, got %v", a.errors)
	}
}

// ========== Coverage Gap Tests ==========

func TestResolveInheritanceNilType(t *testing.T) {
	// Exercise the typ == nil guard in resolveInheritance (line 190).
	// This happens when a model name appears in the AST but was never
	// registered in a.types (e.g. name collision removed it).
	a := New()
	// Do NOT declare "Ghost" in a.types, but include it in the file's Models
	// with a parent reference. resolveInheritance should skip it via continue.
	file := &ast.File{
		Models: []*ast.ModelDecl{
			{
				Name:    "Ghost",
				Parents: []string{"Base"},
			},
		},
	}
	// Run only pass 2 (resolveInheritance) directly
	a.resolveInheritance(file)
	// No panic and no errors about "Ghost" itself (it was skipped)
	if len(a.errors) > 0 {
		t.Errorf("expected no errors for skipped nil type, got %v", a.errors)
	}
}

func TestResolveTypeRefNil(t *testing.T) {
	// A fn with no return type should resolve to Void
	result := analyze(t, `
fn doSomething(x: Int) @native
`)
	expectNoErrors(t, result)
}

func TestResolveTypeRefNilRef(t *testing.T) {
	// Exercise the nil ref path in resolveTypeRef directly
	a := New()
	file := &ast.File{
		Functions: []*ast.FnDecl{
			{
				Name:       "test",
				ReturnType: nil, // nil return type -> resolves to Void
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	sym := result.Scope.Lookup("test")
	if sym == nil {
		t.Fatal("expected test symbol")
	}
}

func TestResolveTypeRefNilDirect(t *testing.T) {
	// Exercise resolveTypeRef with nil ref via API with nil return type
	a := New()
	file := &ast.File{
		APIs: []*ast.ApiDecl{
			{
				Name:       "test",
				ReturnType: nil, // nil return type
			},
		},
	}
	result := a.Analyze([]*ast.File{file})
	sym := result.Scope.Lookup("test")
	if sym == nil {
		t.Fatal("expected test symbol")
	}
	if sym.Type == nil {
		t.Fatal("expected non-nil type (should be Void)")
	}
	if sym.Type.Kind != TypeVoid {
		t.Errorf("expected TypeVoid, got %v", sym.Type.Kind)
	}
}

func TestDuplicateModelFieldResolution(t *testing.T) {
	// When a type is declared twice, the second declaration fails
	// and resolveFields will encounter typ == nil for the duplicate
	result := analyze(t, `
model Dup { name: String }
model Dup { email: String }
model Other { x: String }
`)
	// Should have "already declared" error
	expectError(t, result, "already declared")
}

func TestExtendDecl(t *testing.T) {
	result := analyze(t, `
model Post { title: String }
model User { name: String }
extend User {
  posts: [Post]
}
`)
	expectNoErrors(t, result)
}

func TestExtendTargetNotFound(t *testing.T) {
	// Exercise the extend target not found path (line 282-285).
	// When extend references a type not in a.types, fields should
	// still be validated.
	result := analyze(t, `
extend NonExistent {
  field: String
}
`)
	// No error for the missing target (it might be in another module),
	// but the field type should be resolved without error.
	for _, err := range result.Errors {
		if strings.Contains(err.Message, "NonExistent") {
			t.Errorf("should not error on extend target not found, got: %v", err)
		}
	}
}

func TestCustomFKField(t *testing.T) {
	result := analyze(t, `
model User { name: String }
model Post {
  author: User(key: authorId)
  authorId: Int
}
`)
	expectNoErrors(t, result)
}

func TestEnumMemberAccess(t *testing.T) {
	result := analyze(t, `
enum Role { USER ADMIN MODERATOR }
api test(): Role {
  Role.ADMIN
}
`)
	expectNoErrors(t, result)
}

func TestEnumMemberAccessInvalid(t *testing.T) {
	result := analyze(t, `
enum Role { USER ADMIN }
api test(): Role {
  Role.SUPERADMIN
}
`)
	expectError(t, result, "has no field 'SUPERADMIN'")
}

func TestNilFieldsModelDirect(t *testing.T) {
	// Cover the typ.Fields == nil init path in resolveModelFields by
	// creating a model whose type is pre-registered with nil Fields.
	a := New()
	// Pre-register the type with nil Fields to cover the defensive init
	a.types["PreModel"] = &ResolvedType{Kind: TypeModel, Name: "PreModel"}

	file := &ast.File{
		Name: "test.luxo",
		Models: []*ast.ModelDecl{
			{
				Name: "PreModel",
				Fields: []*ast.FieldDecl{
					{Name: "name", Type: &ast.TypeRef{Name: "String"}},
				},
			},
		},
	}

	// Skip collectDeclarations (type already registered), just resolveFields
	a.resolveFields(file)
	if a.types["PreModel"].Fields == nil {
		t.Error("expected Fields to be initialized")
	}
	if _, ok := a.types["PreModel"].Fields["name"]; !ok {
		t.Error("expected field 'name'")
	}
}

func TestNilFieldsInterfaceDirect(t *testing.T) {
	a := New()
	a.types["PreIface"] = &ResolvedType{Kind: TypeInterface, Name: "PreIface"}

	file := &ast.File{
		Name: "test.luxo",
		Interfaces: []*ast.InterfaceDecl{
			{
				Name: "PreIface",
				Fields: []*ast.FieldDecl{
					{Name: "query", Type: &ast.TypeRef{Name: "String"}},
				},
			},
		},
	}

	a.resolveFields(file)
	if a.types["PreIface"].Fields == nil {
		t.Error("expected Fields to be initialized")
	}
}

func TestNilFieldsTypeDirect(t *testing.T) {
	a := New()
	a.types["PreType"] = &ResolvedType{Kind: TypeCustom, Name: "PreType"}

	file := &ast.File{
		Name: "test.luxo",
		Types: []*ast.TypeDecl{
			{
				Name: "PreType",
				Fields: []*ast.FieldDecl{
					{Name: "value", Type: &ast.TypeRef{Name: "Int"}},
				},
			},
		},
	}

	a.resolveFields(file)
	if a.types["PreType"].Fields == nil {
		t.Error("expected Fields to be initialized")
	}
}

// Regression: fuzz found panic on nil map when model name conflicts with builtin type
func TestModelNameConflictWithBuiltin(t *testing.T) {
	result := analyze(t, `model Int { name: String }`)
	// should report "already declared" error but not panic
	expectError(t, result, "already declared")
}

func TestModelNameConflictWithBuiltinFields(t *testing.T) {
	// model String shadows builtin, resolveFields should not panic
	result := analyze(t, `model String { value: Int }`)
	expectError(t, result, "already declared")
}

func TestBangElvisExpr(t *testing.T) {
	result := analyze(t, `
model User @crud {
  id: Int @id @auto
  name: String
}
api test: Boolean {
  User.exists() !: throw error.Conflict
  return true
}
`)
	expectNoErrors(t, result)
}

func TestVisibleRequiresNullable(t *testing.T) {
	result := analyze(t, `
model User {
  name: String
  salary: Float @visible { my.role == "admin" }
}
`)
	expectError(t, result, "@visible requires nullable")
}

func TestVisibleNullableOk(t *testing.T) {
	result := analyze(t, `
model User {
  name: String
  salary: Float? @visible { my.role == "admin" }
}
`)
	expectNoErrors(t, result)
}

func TestUsedVarInCreateNamedArg(t *testing.T) {
	result := analyze(t, `
model Team @crud {
  id: Int @id @auto
  name: String
  slug: String
}
api test(teamName: String): Team {
  val slug = teamName
  val team = Team.create(name: teamName, slug: slug)
  return team
}
`)
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "slug") && strings.Contains(w.Message, "never used") {
			t.Errorf("slug used in create() named arg should NOT be flagged as unused: %s", w.Message)
		}
	}
}
