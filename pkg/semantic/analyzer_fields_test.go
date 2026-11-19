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
model Post { id: Int title: String userId: Int }
model User { id: Int posts: [Post] }
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

func TestDeclaredFieldsRejectDuplicatesWithoutOverwritingMetadata(t *testing.T) {
	for _, declaration := range []string{"type Packet", "interface Packet", "model Packet @noTime", "type Packet<T>"} {
		t.Run(declaration, func(t *testing.T) {
			result := analyze(t, declaration+` {
 value: Int? = null
 value: String
}`)
			expectError(t, result, "duplicate field 'value'")
			field := result.Types["Packet"].Fields["value"]
			if field.Type.Name != "Int" || !field.Nullable || !field.HasDefault || field.Pos.Line != 2 {
				t.Fatalf("duplicate field replaced the accepted metadata: %+v", field)
			}
			var located bool
			for _, err := range result.Errors {
				if strings.Contains(err.Message, "duplicate field") && err.Pos.Line == 3 {
					located = true
				}
			}
			if !located {
				t.Fatal("duplicate field diagnostic lost the second declaration location")
			}
		})
	}
}

func TestDeclaredFieldUniquenessKeepsInheritedAndDistinctNames(t *testing.T) {
	for _, source := range []string{
		`interface Base { value: Int } model Packet : Base @noTime { id: Int @id value: Int }`,
		`type Packet { lower: Int upper: String }`,
		`interface Packet { lower: Int upper: String }`,
		`type Packet<T> { value: T values: [T] }`,
	} {
		expectNoErrors(t, analyze(t, source))
	}
}

// ========== Computed Field Tests ==========

func TestComputedField(t *testing.T) {
	result := analyze(t, `model User {
  id: Int @id
  posts: [Post]
  val totalCount: Int get @count(posts)
  val avgLikes: Float get @avg(field: posts.likes)
}
model Post {
  id: Int @id
  userId: Int
  likes: Int
}`)
	expectNoErrors(t, result)

	user := result.Types["User"]
	tc := user.Fields["totalCount"]
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

func TestFieldDefaultsMustBeStaticAndTyped(t *testing.T) {
	for _, kind := range []string{"model", "type"} {
		for _, declaration := range []string{
			`value: Int = "42"`, `value: String = 42`, `value: Float = 42`,
			`value: Boolean = "false"`, `value: Duration = 42`, `value: Int = 42ms`,
			`value: Int = null`, `value: String = null`, `value: [Int] = null`,
			`value: State = Other.OPEN`, `value: State = State.MISSING`,
			`value: State = "OPEN"`, `value: State = MISSING`,
			`value: Int = value()`, `value: Int = 1 + 2`, `value: [Int] = [1]`,
		} {
			t.Run(kind+"/"+declaration, func(t *testing.T) {
				result := analyze(t, `enum State { OPEN CLOSED }
enum Other { OPEN }
fn value(): Int { return 42 }
`+kind+` Probe { `+declaration+` }`)
				expectError(t, result, "default value for field 'value'")
			})
		}
	}
}

func TestFieldDefaultTypeControls(t *testing.T) {
	for _, kind := range []string{"model", "type"} {
		result := analyze(t, `enum State { OPEN CLOSED }
`+kind+` Probe {
 integer: Int = -9223372036854775808
 real: Float = -0.0
 text: String = "value"
 flag: Boolean = false
 elapsed: Duration = -1d
 state: State = State.OPEN
 legacyState: State = OPEN
 optional: Int? = null
 present: Int? = 42
 items: [Int]? = null
 json: JSON = null
 optionalJSON: JSON? = null
}`)
		expectNoErrors(t, result)
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
	// Reject the source declaration without modifying the existing builtin metadata.
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

func TestRejectedDeclarationsCannotModifyBuiltins(t *testing.T) {
	for _, sample := range []struct{ source, builtin string }{
		{`model Base @noTime { id: Int @id } model String : Base @noTime { poisoned: Int }`, "String"},
		{`interface Int { poisoned: String }`, "Int"},
		{`type Boolean { poisoned: String }`, "Boolean"},
	} {
		t.Run(sample.builtin, func(t *testing.T) {
			result := analyze(t, sample.source)
			expectError(t, result, "already declared")
			typ := result.Types[sample.builtin]
			if typ.Fields["poisoned"] != nil || len(typ.Parents) != 0 {
				t.Fatalf("rejected declaration modified builtin %s: fields=%v parents=%v", sample.builtin, typ.Fields, typ.Parents)
			}
		})
	}
}

func TestRejectedDuplicateCannotAddFieldsWithSamePosition(t *testing.T) {
	for _, kind := range []string{"model", "interface", "type"} {
		t.Run(kind, func(t *testing.T) {
			first := parseFileWithName(t, kind+` Shared { keep: Int }`, "first.luxo")
			duplicate := parseFileWithName(t, kind+` Shared { poisoned: String }`, "duplicate.luxo")
			switch kind {
			case "model":
				duplicate.Models[0].Pos = first.Models[0].Pos
			case "interface":
				duplicate.Interfaces[0].Pos = first.Interfaces[0].Pos
			case "type":
				duplicate.Types[0].Pos = first.Types[0].Pos
			}
			result := Analyze([]*ast.File{first, duplicate})
			expectError(t, result, "already declared")
			fields := result.Types["Shared"].Fields
			if fields["keep"] == nil || fields["poisoned"] != nil {
				t.Fatalf("rejected %s with matching source position modified accepted fields: %v", kind, fields)
			}
		})
	}
}

func TestRejectedModelCannotAddInheritance(t *testing.T) {
	result := analyze(t, `
model First @noTime { id: Int @id }
model Other @noTime { id: Int @id }
model Shared : First @noTime { keep: Int }
model Shared : Other @noTime { poisoned: String }
`)
	expectError(t, result, "already declared")
	typ := result.Types["Shared"]
	if len(typ.Parents) != 1 || typ.Parents[0].Name != "First" || typ.Fields["poisoned"] != nil {
		t.Fatalf("rejected model modified accepted inheritance or fields: %+v", typ)
	}
}

func TestRejectedTypeCannotReplaceModuleOwnership(t *testing.T) {
	for _, sample := range []struct{ first, duplicate string }{
		{`model Shared @noTime { id: Int @id }`, `model Shared @noTime { id: Int @id }`},
		{`interface Shared { keep: Int }`, `interface Shared { poisoned: String }`},
		{`type Shared { keep: Int }`, `type Shared { poisoned: String }`},
		{`enum Shared { FIRST }`, `enum Shared { OTHER }`},
		{`sealed Shared { First }`, `sealed Shared { Other }`},
	} {
		t.Run(sample.first, func(t *testing.T) {
			first := parseFileWithName(t, sample.first, "origin/first.luxo")
			duplicate := parseFileWithName(t, sample.duplicate, "origin/duplicate.luxo")
			a := New()
			result := a.AnalyzeWithModules([]*ast.File{first, duplicate})
			expectError(t, result, "already declared")
			if owner := a.typeOwners.ownerOf("Shared"); owner != "first" {
				t.Fatalf("rejected declaration changed the accepted module to %q", owner)
			}
		})
	}
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
model User { id: Int name: String }
model Post {
  id: Int
  author: User @by(id, authorId)
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

func TestBangElvisRejectsNonBooleanCondition(t *testing.T) {
	for _, kind := range []string{"Int", "String", "Boolean?", "[Boolean]"} {
		t.Run(kind, func(t *testing.T) {
			result := analyze(t, "fn guard(value: "+kind+"): Boolean { value !: throw error.Conflict return true }")
			expectError(t, result, "Boolean")
		})
	}
}

func TestBangElvisGuardBoundary(t *testing.T) {
	for _, body := range []string{
		"true !: false return true",
		"true !: 42 return true",
		"return true !: throw error.Conflict",
		"val result = true !: throw error.Conflict return result",
		"if true !: throw error.Conflict { return true } return false",
	} {
		t.Run(body, func(t *testing.T) {
			result := analyze(t, "fn guard(): Boolean { "+body+" }")
			expectError(t, result, "!:")
		})
	}
	expectNoErrors(t, analyze(t, "fn guard(value: Boolean): Boolean { value !: throw error.Conflict return true }"))
}

func TestBangElvisFailureDoesNotNarrowContinuingScope(t *testing.T) {
	result := analyze(t, `
error Rejected(detail: String) { code: 409 message: "error.rejected" }
fn guard(value: String?, reject: Boolean): String {
 reject !: throw Rejected(detail: value ?: throw error.NotFound)
 return value
}
`)
	expectError(t, result, "return expects")
}

func TestBangElvisCannotSupplyImplicitReturn(t *testing.T) {
	result := analyze(t, "fn guard(value: Boolean): Boolean { value !: throw error.Conflict }")
	expectError(t, result, "only valid as a guard statement")
}

func TestThrowRejectsNonErrorValues(t *testing.T) {
	for _, value := range []string{"42", "\"failure\"", "null", "true", "[1]"} {
		for _, body := range []string{
			"throw " + value,
			"true !: throw " + value + " return true",
			"return false ?: throw " + value,
		} {
			t.Run(body, func(t *testing.T) {
				result := analyze(t, "fn guard(): Boolean { "+body+" }")
				expectError(t, result, "throw requires")
			})
		}
	}
}

func TestExpressionAssignmentStillRequiresWritableTarget(t *testing.T) {
	for _, body := range []string{"read() = 1", "(1 + 2) = 3", "(true) = false"} {
		t.Run(body, func(t *testing.T) {
			result := analyze(t, "fn read(): Int { return 0 } fn write(): Int { "+body+" return 0 }")
			expectError(t, result, "assignment target is not writable")
		})
	}
}

func TestNumericNegationRejectsInvalidTypes(t *testing.T) {
	for _, kind := range []string{"String", "Boolean", "DateTime", "UUID", "JSON", "Bytes", "Int?", "Float?", "[Int]", "[Decimal]"} {
		t.Run(kind, func(t *testing.T) {
			expectError(t, analyze(t, "fn negative(value: "+kind+"): "+kind+" { return -value }"), "numeric negation requires")
		})
	}
	for _, kind := range []string{"Int", "Float", "Decimal", "Duration"} {
		expectNoErrors(t, analyze(t, "fn negative(value: "+kind+"): "+kind+" { return -value }"))
	}
}

func TestThrowRejectsOrdinaryCallsAndMembers(t *testing.T) {
	for _, source := range []string{
		"fn Failure(): Int { return 42 } fn run(): Int { throw Failure() }",
		"type Item { value: String } fn run(item: Item): Int { throw item.value }",
		"type Item { NotFound: String } fn run(error: Item): Int { throw error.NotFound }",
	} {
		expectError(t, analyze(t, source), "throw requires")
	}
}

func TestVisibleRequiresNullable(t *testing.T) {
	for _, sample := range []struct{ name, source string }{
		{"model", `model User { name: String salary: Float @visible { my.role == "admin" } }`},
		{"type", `type Record { secret: String @visible(true) }`},
		{"interface", `interface Record { secret: String @visible(true) }`},
		{"generic", `type Box<T> { value: T @visible(true) }`},
	} {
		t.Run(sample.name, func(t *testing.T) {
			for _, duplicate := range []bool{false, true} {
				source := sample.source
				if duplicate {
					source = strings.Replace(source, "@visible", "@visible(false) @visible", 1)
				}
				result := analyze(t, source)
				count := 0
				for _, failure := range result.Errors {
					if strings.Contains(failure.Message, "@visible requires nullable") {
						count++
					}
				}
				if count != 1 {
					t.Fatalf("duplicate=%t: want one nullable diagnostic, got %d: %v", duplicate, count, result.Errors)
				}
				if duplicate {
					expectError(t, result, "duplicate @visible")
				}
			}
		})
	}
}

func TestVisibleNullableOk(t *testing.T) {
	for _, sample := range []struct{ name, source string }{
		{"model", `model User { name: String salary: Float? @visible { my.role == "admin" } }`},
		{"type", `type Record { secret: String? @visible(true) }`},
		{"interface", `interface Record { secret: String? @visible(true) }`},
		{"generic", `type Box<T> { value: T? @visible(true) }`},
	} {
		t.Run(sample.name, func(t *testing.T) { expectNoErrors(t, analyze(t, sample.source)) })
	}
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

func TestVisibleRequiresSingleExpression(t *testing.T) {
	for _, directive := range []string{`@visible`, `@visible {}`, `@visible { val allowed = true }`, `@visible { true false }`} {
		t.Run(directive, func(t *testing.T) {
			expectError(t, analyze(t, `type Secret { value: String? `+directive+` }`), "@visible")
		})
	}
}

func TestVisibleRejectsDuplicateConditions(t *testing.T) {
	for _, source := range []string{
		`model Secret { id: Int @id value: String? @visible { true } @visible { false } }`,
		`type Secret { value: String? @visible { true } @visible { false } }`,
		`interface Secret { value: String? @visible { true } @visible { false } }`,
		`model Secret { id: Int @id } extend Secret { value: String? @visible { true } @visible { false } }`,
		`model Secret { id: Int @id value: String? @visible { false } @visible { true } }`,
	} {
		t.Run(source, func(t *testing.T) { expectError(t, analyze(t, source), "duplicate @visible") })
	}
}

func TestVisiblePositionalCondition(t *testing.T) {
	expectNoErrors(t, analyze(t, `type Secret { value: String? @visible(!true) }`))
	for _, directive := range []string{`@visible(true) { false }`, `@visible(condition: true)`} {
		expectError(t, analyze(t, `type Secret { value: String? `+directive+` }`), "@visible")
	}
}

func TestVisibleRequiresBooleanCondition(t *testing.T) {
	for _, condition := range []string{`1`, `"yes"`, `null`, `[true]`, `my.id + 1`, `missing`} {
		t.Run(condition, func(t *testing.T) {
			expectError(t, analyze(t, `type Secret { value: String? @visible(`+condition+`) }`), "@visible")
		})
	}
	expectNoErrors(t, analyze(t, `model User @withAuth(stores: [id, enabled]) { id: Int @id enabled: Boolean } type Secret { value: String? @visible(my.enabled) }`))
	expectError(t, analyze(t, `model User @withAuth(stores: [id, enabled]) { id: Int @id enabled: Boolean? } type Secret { value: String? @visible(my.enabled) }`), "@visible")
}

func TestVisibleConditionDeclarationBoundaries(t *testing.T) {
	for _, source := range []string{
		`model Secret { id: Int @id value: String? @visible(1) }`,
		`type Secret { value: String? @visible(1) }`,
		`interface Secret { value: String? @visible(1) }`,
		`model Secret { id: Int @id } extend Secret { value: String? @visible(1) }`,
	} {
		t.Run(source, func(t *testing.T) { expectError(t, analyze(t, source), "@visible") })
	}
	expectNoErrors(t, analyze(t, `type Secret { value: String? @visible(my.role == "admin") }`))
	expectNoErrors(t, analyze(t, `
enum Role { ADMIN MEMBER }
model User @withAuth(stores: [id, role]) { id: Int @id role: Role }
type Secret { value: String? @visible(my.role == Role.ADMIN) }
`))
}

func TestInheritanceCyclesAreDiagnostics(t *testing.T) {
	for _, source := range []string{
		`model Self : Self { value: Int }`,
		`model A : B { a: Int } model B : A { b: Int }`,
		`model A : B { a: Int } model B : C { b: Int } model C : A { c: Int }`,
	} {
		expectError(t, analyze(t, source), "inheritance cycle")
	}
	expectNoErrors(t, analyze(t, `model Base { id: Int }
model Left : Base { left: String }
model Right : Base { right: String }
model Leaf : Left, Right { own: String }`))
}

func TestAuthMethodsRejectDeclaredFieldCollision(t *testing.T) {
	for _, name := range []string{"createToken", "createRefreshToken", "refreshToken", "verify"} {
		t.Run(name, func(t *testing.T) {
			result := analyze(t, "model User @withAuth(stores: [id]) { id: Int @id\n"+name+": Int }")
			expectError(t, result, "conflicts with @withAuth")
			field := result.Types["User"].Fields[name]
			if field == nil || field.IsMethod || field.Type.Kind != TypeInt {
				t.Fatal("auth method overwrote the declared field")
			}
			ordinary := analyze(t, "model User { "+name+": Int }")
			expectNoErrors(t, ordinary)
		})
	}
}

func TestStoredFieldsRejectUnmappedTypes(t *testing.T) {
	for _, typ := range []string{"Detail", "Detail?", "[Detail]", "[Detail]?"} {
		for _, owner := range []string{"model Record { id: Int @id detail: ", "extend Record { detail: "} {
			expectError(t, analyze(t, "type Detail { level: Int }\n"+owner+typ+" }"), "has no database mapping")
		}
	}
	expectNoErrors(t, analyze(t, `type Detail { level: Int }
 event Changed(detail: Detail)
 fn echo(value: Detail): Detail { return value }
 api read(value: Detail): Detail { return echo(value) }`))
}

func TestComputedFieldsKeepIndependentDiagnostics(t *testing.T) {
	result := analyze(t, `type Detail { level: Int }
 model Record { id: Int @id val detail: Detail get @native }`)
	expectError(t, result, "must declare exactly one aggregate")
	for _, diagnostic := range result.Errors {
		if strings.Contains(diagnostic.Message, "has no database mapping") {
			t.Fatal("computed field incorrectly treated as stored", diagnostic)
		}
	}
}

func TestInheritedStorageRejectsUnmappedFields(t *testing.T) {
	for _, declaration := range []string{"type", "interface"} {
		source := "type Detail { level: Int }\n" + declaration + " Base { detail: Detail }\nmodel Record : Base { id: Int @id }"
		expectError(t, analyze(t, source), "has no database mapping")
	}
	expectNoErrors(t, analyze(t, `type Base { label: String }
 model Record : Base { id: Int @id }`))
	expectNoErrors(t, analyze(t, `type Detail { level: Int }
 type Base { detail: Detail }
 model Record : Base { id: Int @id detail: String }`))
}

func TestStoredFieldsRejectAbstractDeclarations(t *testing.T) {
	for _, declaration := range []string{"interface Detail { level: Int }", "sealed Detail { Value(level: Int) }"} {
		for _, typ := range []string{"Detail", "Detail?", "[Detail]", "[Detail]?"} {
			for _, owner := range []string{"model Record { id: Int @id detail: ", "extend Record { detail: "} {
				expectError(t, analyze(t, declaration+"\n"+owner+typ+" }"), "has no database mapping")
			}
		}
		expectNoErrors(t, analyze(t, declaration))
	}
}

func TestExplicitRelationOverridesImplicitForeignKey(t *testing.T) {
	input := analyze(t, `model User { id: Int name: String } model Post { id: Int userId: Int authorId: Int author: User @by(id, authorId) }`)
	expectNoErrors(t, input)
}
