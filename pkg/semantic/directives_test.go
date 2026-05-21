package semantic

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

// ========== Directive Registry Tests ==========

func TestLookupDirective(t *testing.T) {
	d := LookupDirective("unique")
	if d == nil {
		t.Fatal("expected to find @unique")
	}
	if d.Name != "unique" {
		t.Errorf("expected name 'unique', got %q", d.Name)
	}
	if d.Contexts&OnField == 0 {
		t.Error("@unique should be allowed on field")
	}
}

func TestLookupDirectiveUnknown(t *testing.T) {
	d := LookupDirective("nonexistent")
	if d != nil {
		t.Errorf("expected nil for unknown directive, got %v", d)
	}
}

func TestAllDirectiveNames(t *testing.T) {
	names := AllDirectiveNames()
	if len(names) == 0 {
		t.Fatal("expected non-empty directive names list")
	}
	found := false
	for _, n := range names {
		if n == "auth" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'auth' in directive names")
	}
}

func TestContextNames(t *testing.T) {
	s := contextNames(OnField | OnModel)
	if s != "field, model" {
		t.Errorf("expected 'field, model', got %q", s)
	}

	s = contextNames(OnApi)
	if s != "api" {
		t.Errorf("expected 'api', got %q", s)
	}

	s = contextNames(0)
	if s != "unknown" {
		t.Errorf("expected 'unknown', got %q", s)
	}
}

func TestContextNameSingle(t *testing.T) {
	tests := []struct {
		ctx  DirectiveContext
		want string
	}{
		{OnField, "field"},
		{OnModel, "model"},
		{OnApi, "api"},
		{OnFn, "fn"},
		{OnMiddleware, "middleware"},
		{OnEvent, "event"},
		{OnComputed, "computed"},
		{DirectiveContext(0), "unknown"},
	}
	for _, tt := range tests {
		got := contextName(tt.ctx)
		if got != tt.want {
			t.Errorf("contextName(%d) = %q, want %q", tt.ctx, got, tt.want)
		}
	}
}

// ========== Directive Context Validation ==========

func TestDirectiveWrongContext(t *testing.T) {
	// @auth on model (should be on api/fn)
	result := analyze(t, `model User @auth { name: String }`)
	expectError(t, result, "@auth cannot be used on model")
}

func TestDirectiveWrongContextField(t *testing.T) {
	// @native on field (should be on api/fn/middleware)
	result := analyze(t, `model User { name: String @native }`)
	expectError(t, result, "@native cannot be used on field")
}

func TestDirectiveNativeOnApi(t *testing.T) {
	result := analyze(t, `api oauthLogin(): Int @native`)
	expectNoErrors(t, result)
}

func TestDirectiveNativeOnApiNoReturnType(t *testing.T) {
	// Construct AST directly since the parser enforces return type syntax.
	// This covers override/extend scenarios where ReturnType might be nil.
	file := &ast.File{
		Name: "test.luxo",
		APIs: []*ast.ApiDecl{{
			Name:       "oauthLogin",
			Pos:        token.Position{File: "test.luxo", Line: 1, Col: 1},
			Directives: []*ast.Directive{{Name: "native", Pos: token.Position{File: "test.luxo", Line: 1, Col: 20}}},
			ReturnType: nil,
		}},
	}
	a := New()
	result := a.Analyze([]*ast.File{file})
	expectError(t, result, "@native API must declare a return type")
}

func TestDirectiveCorrectContextApi(t *testing.T) {
	// @auth on api (correct)
	result := analyze(t, `api getUser(): Int @auth`)
	expectNoErrors(t, result)
}

func TestDirectiveCorrectContextFn(t *testing.T) {
	// @native on fn (correct)
	result := analyze(t, `fn encrypt(value: String): String @native`)
	expectNoErrors(t, result)
}

func TestDirectiveCorrectContextMiddleware(t *testing.T) {
	// @native on middleware (correct)
	result := analyze(t, `middleware customAuth @native`)
	expectNoErrors(t, result)
}

func TestDirectiveFnAuth(t *testing.T) {
	// @auth on fn (correct — allowed on fn)
	result := analyze(t, `fn secureOp(): Int @auth @native`)
	expectNoErrors(t, result)
}

// ========== Directive Type Constraint ==========

func TestDirectiveTypeConstraintHash(t *testing.T) {
	// @hash on Int (should be on String)
	result := analyze(t, `model User { age: Int @hash }`)
	expectError(t, result, "@hash can only be used on String")
}

func TestDirectiveTypeConstraintHashOk(t *testing.T) {
	result := analyze(t, `model User { password: String @hash }`)
	expectNoErrors(t, result)
}

func TestDirectiveTypeConstraintEmail(t *testing.T) {
	result := analyze(t, `model User { age: Int @email }`)
	expectError(t, result, "@email can only be used on String")
}

func TestDirectiveTypeConstraintSearch(t *testing.T) {
	result := analyze(t, `model User { age: Int @search }`)
	expectError(t, result, "@search can only be used on String")
}

func TestDirectiveTypeConstraintMask(t *testing.T) {
	result := analyze(t, `model User { age: Int @mask }`)
	expectError(t, result, "@mask can only be used on String")
}

func TestDirectiveTypeConstraintEncrypt(t *testing.T) {
	result := analyze(t, `model User { age: Int @encrypt }`)
	expectError(t, result, "@encrypt can only be used on String")
}

func TestDirectiveRangeOnString(t *testing.T) {
	result := analyze(t, `model User { name: String @range(0, 150) }`)
	expectError(t, result, "@range can only be used on numeric")
}

// ========== Directive Argument Count ==========

func TestDirectiveTooManyArgs(t *testing.T) {
	result := analyze(t, `model User { name: String @varchar(100, 200, 300) }`)
	expectError(t, result, "@varchar expects at most 1 argument(s), got 3")
}

func TestDirectiveArgCountOk(t *testing.T) {
	result := analyze(t, `model User { name: String @varchar(100) }`)
	expectNoErrors(t, result)
}

func TestDirectiveNoArgsWhenNoneExpected(t *testing.T) {
	// @hidden takes no args
	result := analyze(t, `model User { name: String @hidden }`)
	expectNoErrors(t, result)
}

func TestDirectiveTooManyArgsHidden(t *testing.T) {
	result := analyze(t, `model User { name: String @hidden(true) }`)
	expectError(t, result, "@hidden expects at most 0 argument(s), got 1")
}

func TestDirectiveScopeUnlimitedArgs(t *testing.T) {
	// @scope has MaxArgs = -1 (unlimited positional args)
	result := analyze(t, `api getUsers(): Int @scope(published, active, mine)`)
	expectNoErrors(t, result)
}

// ========== Typo Suggestion ==========

func TestDirectiveTypoSuggestion(t *testing.T) {
	result := analyze(t, `model User { name: String @uniuqe }`)
	expectError(t, result, "did you mean '@unique'")
}

func TestDirectiveTypoSuggestionApi(t *testing.T) {
	result := analyze(t, `api test(): Int @autth`)
	expectError(t, result, "did you mean '@auth'")
}

func TestDirectiveTypoSuggestionModel(t *testing.T) {
	result := analyze(t, `model User @sof { name: String }`)
	expectError(t, result, "did you mean '@soft'")
}

// ========== Computed Field Directives ==========

func TestComputedDirectiveCount(t *testing.T) {
	result := analyze(t, `
model Post {
  title: String
  val totalCount: Int get @count
}
`)
	expectNoErrors(t, result)
}

func TestComputedDirectiveAvg(t *testing.T) {
	result := analyze(t, `
model Post {
  title: String
  val avgLikes: Float get @avg(field: likes)
}
`)
	expectNoErrors(t, result)
}

func TestComputedDirectiveWrongContext(t *testing.T) {
	// @hidden on computed field — should fail (OnField only, not OnComputed)
	result := analyze(t, `
model Post {
  title: String
  val totalCount: Int get @hidden
}
`)
	expectError(t, result, "@hidden cannot be used on computed")
}

// ========== Multiple Directives ==========

func TestMultipleDirectivesOnField(t *testing.T) {
	result := analyze(t, `model User {
  name: String @varchar(100) @filterable @sortable
  email: String @unique @hidden
}`)
	expectNoErrors(t, result)
}

func TestMultipleDirectivesOnModel(t *testing.T) {
	result := analyze(t, `model User @soft @noTime { name: String }`)
	expectNoErrors(t, result)
}

func TestMultipleDirectivesOnApi(t *testing.T) {
	result := analyze(t, `api getUser(): Int @auth @cache(ttl: 60)`)
	expectNoErrors(t, result)
}

// ========== Directive with body ==========

func TestDirectiveWithBody(t *testing.T) {
	result := analyze(t, `model User {
  name: String @transform { it }
}`)
	expectNoErrors(t, result)
}

func TestDirectiveBeforeSave(t *testing.T) {
	result := analyze(t, `model User {
  name: String @beforeSave { it }
}`)
	expectNoErrors(t, result)
}

// ========== Complex directive args ==========

func TestDirectiveCrudOnly(t *testing.T) {
	result := analyze(t, `model Config @crud(only: [get, list]) { key: String }`)
	expectNoErrors(t, result)
}

func TestDirectiveCrudExcept(t *testing.T) {
	result := analyze(t, `model Log @crud(except: [update, delete]) { action: String }`)
	expectNoErrors(t, result)
}

func TestDirectiveAuthRole(t *testing.T) {
	result := analyze(t, `api admin(): Int @auth(role: "admin")`)
	expectNoErrors(t, result)
}

func TestDirectiveCacheTtl(t *testing.T) {
	result := analyze(t, `api cached(): Int @cache(ttl: 5m)`)
	expectNoErrors(t, result)
}

func TestDirectiveRateLimit(t *testing.T) {
	result := analyze(t, `api limited(): Int @rateLimit(max: 100, window: 1m)`)
	expectError(t, result, "unknown directive '@rateLimit'")
}

func TestDirectiveIndexFields(t *testing.T) {
	result := analyze(t, `model User @index(fields: ["name", "email"]) { name: String }`)
	expectNoErrors(t, result)
}

func TestDirectiveDecimalParams(t *testing.T) {
	result := analyze(t, `model Product { price: Float @decimal(precision: 10, scale: 2) }`)
	expectNoErrors(t, result)
}

func TestDirectiveVectorDim(t *testing.T) {
	result := analyze(t, `model Doc { embedding: String @vector(dim: 1536) }`)
	expectNoErrors(t, result)
}

func TestDirectiveDeprecatedWithReason(t *testing.T) {
	result := analyze(t, `model User { old: String @deprecated(reason: "use newField") }`)
	expectNoErrors(t, result)
}

func TestDirectiveDeprecatedOnApi(t *testing.T) {
	result := analyze(t, `api oldEndpoint(): Int @deprecated(reason: "use newEndpoint")`)
	expectNoErrors(t, result)
}

// ========== Directive Registry Completeness ==========

func TestAllBuiltinDirectivesRegistered(t *testing.T) {
	// verify all builtinDirectives are accessible via LookupDirective
	for _, d := range builtinDirectives {
		if LookupDirective(d.Name) == nil {
			t.Errorf("directive @%s not found in registry", d.Name)
		}
	}
}

func TestDirectiveDefMaxArgsDefault(t *testing.T) {
	// @hidden has no Params and MaxArgs=0, meaning 0 args allowed
	d := LookupDirective("hidden")
	if d == nil {
		t.Fatal("expected @hidden")
	}
	if d.MaxArgs != 0 && len(d.Params) != 0 {
		t.Error("@hidden should accept 0 args")
	}
}

func TestDirectiveContextNamesMultiple(t *testing.T) {
	s := contextNames(OnApi | OnFn | OnMiddleware)
	if s != "api, fn, middleware" {
		t.Errorf("expected 'api, fn, middleware', got %q", s)
	}
}

// ========== @scope Validation Tests ==========

func TestScopeDirectiveOnModelType(t *testing.T) {
	// @scope on API returning a model — valid, no errors/warnings
	result := analyze(t, `
model Post {
  title: String
  scope published = where(status == "PUBLISHED")
  scope recent = where(createdAt > now())
}
api listPosts(): [Post] @scope(published, recent)
`)
	expectNoErrors(t, result)
	// should have no warnings about @scope
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "@scope") {
			t.Errorf("unexpected @scope warning: %s", w.Message)
		}
	}
}

func TestScopeDirectiveOnNonModelType(t *testing.T) {
	// @scope on API returning a custom type — warning
	result := analyze(t, `
type MyResult { data: String }
api test(): MyResult @scope(published)
`)
	expectWarning(t, result, "@scope can only be used on APIs returning a model type")
}

func TestScopeDirectiveOnEnumType(t *testing.T) {
	// @scope on API returning an enum — warning
	result := analyze(t, `
enum Status { ACTIVE INACTIVE }
api getStatus(): Status @scope(active)
`)
	expectWarning(t, result, "@scope can only be used on APIs returning a model type")
}

func TestScopeDirectiveUnknownScopeName(t *testing.T) {
	// scope name does not exist on the model — error
	result := analyze(t, `
model Post {
  title: String
  scope published = where(status == "PUBLISHED")
}
api listPosts(): [Post] @scope(published, nonexistent)
`)
	expectError(t, result, "scope 'nonexistent' not found on model 'Post'")
}

func TestScopeDirectiveScopeNameTypo(t *testing.T) {
	// scope name is a typo — error with suggestion
	result := analyze(t, `
model Post {
  title: String
  scope published = where(status == "PUBLISHED")
}
api listPosts(): [Post] @scope(publised)
`)
	expectError(t, result, "did you mean 'published'")
}

func TestScopeDirectiveOnBuiltinReturnType(t *testing.T) {
	// API returning a built-in type (Int) — not a model, but also not custom/enum
	// @scope should warn since Int is not a model
	result := analyze(t, `api test(): Int @scope(published)`)
	expectWarning(t, result, "@scope can only be used on APIs returning a model type")
}

func TestScopeDirectiveAllScopesValid(t *testing.T) {
	// all scopes are valid — no errors
	result := analyze(t, `
model AuditablePost {
  title: String
  scope published = where(status == "PUBLISHED")
  scope mine = where(userId == my.id)
  scope hot = where(likes > 100)
}
api listPosts(): [AuditablePost] @scope(published, mine, hot)
`)
	expectNoErrors(t, result)
}

// ========== hasNamedArg found path ==========

func TestHasNamedArgFound(t *testing.T) {
	args := []*ast.NamedArg{
		{Name: "secret", Value: &ast.Literal{Kind: token.String, Value: "mysecret"}},
		{Name: "stores", Value: &ast.Literal{Kind: token.String, Value: "id"}},
	}
	if !hasNamedArg(args, "secret") {
		t.Error("expected hasNamedArg to return true for 'secret'")
	}
}

func TestHasNamedArgNotFound(t *testing.T) {
	args := []*ast.NamedArg{
		{Name: "stores", Value: &ast.Literal{Kind: token.String, Value: "id"}},
	}
	if hasNamedArg(args, "secret") {
		t.Error("expected hasNamedArg to return false for 'secret'")
	}
}

// ========== checkDirectiveArgs required param provided ==========

func TestDirectiveWithAuthStores(t *testing.T) {
	// @withAuth with stores — should work
	result := analyze(t, `
model User @withAuth(stores: [id, role]) {
  name: String
  email: String
}
`)
	expectNoErrors(t, result)
}

func TestDirectiveWithAuthDefault(t *testing.T) {
	// @withAuth(stores: ..., default: true) — marks this model as the default auth subject
	result := analyze(t, `
model User @withAuth(stores: [id], default: true) {
  name: String
  email: String
}
`)
	expectNoErrors(t, result)
}

func TestDirectiveWithAuthMissingStores(t *testing.T) {
	// @withAuth without stores — should error
	result := analyze(t, `
model User @withAuth {
  name: String
  email: String
}
`)
	expectError(t, result, "requires parameter 'stores'")
}

// ========== validateBareAPI / validateFieldSegments / splitByAndOr Tests ==========

func TestBareAPIValidByField(t *testing.T) {
	// getUserByEmail — email exists on User → no error
	result := analyze(t, `
model User {
  email: String
}
api getUserByEmail
`)
	expectNoErrors(t, result)
}

func TestBareAPIUnknownField(t *testing.T) {
	// getUserByFoo — foo does NOT exist on User → error "unknown field"
	result := analyze(t, `
model User {
  email: String
}
api getUserByFoo
`)
	expectError(t, result, "unknown field 'foo'")
}

func TestBareAPIIncompleteAnd(t *testing.T) {
	// getUserByEmailAnd — trailing And without following field → error "incomplete"
	result := analyze(t, `
model User {
  email: String
}
api getUserByEmailAnd
`)
	expectError(t, result, "is incomplete")
}

func TestBareAPIIncompleteOr(t *testing.T) {
	// getUserByEmailOr — trailing Or without following field → error "incomplete"
	result := analyze(t, `
model User {
  email: String
}
api getUserByEmailOr
`)
	expectError(t, result, "is incomplete")
}

func TestBareAPIIncompleteJustBy(t *testing.T) {
	// getUserBy — nothing after By → error "incomplete"
	result := analyze(t, `
model User {
  email: String
}
api getUserBy
`)
	expectError(t, result, "is incomplete")
}

func TestBareAPINoKnownModel(t *testing.T) {
	// api get — does not start with a valid prefix+ModelName → error "does not reference a known model"
	result := analyze(t, `
model User {
  email: String
}
api getXyz
`)
	expectError(t, result, "does not reference a known model")
}

func TestBareAPIImplicitCreatedAt(t *testing.T) {
	// getUserByCreatedAtBetween — createdAt is implicit → no error
	result := analyze(t, `
model User {
  email: String
}
api getUserByCreatedAtBetween
`)
	expectNoErrors(t, result)
}

func TestBareAPITwoFieldsAndOr(t *testing.T) {
	// getUserByEmailAndName — both email and name exist → no error
	result := analyze(t, `
model User {
  email: String
  name: String
}
api getUserByEmailAndName
`)
	expectNoErrors(t, result)
}

func TestBareAPIListValid(t *testing.T) {
	// listUsers — valid list without By clause → no error
	result := analyze(t, `
model User {
  email: String
}
api listUsers
`)
	expectNoErrors(t, result)
}

func TestBareAPIOperatorSuffixStripped(t *testing.T) {
	// getUserByEmailContaining — operator suffix "Containing" is stripped, email exists → no error
	result := analyze(t, `
model User {
  email: String
}
api getUserByEmailContaining
`)
	expectNoErrors(t, result)
}

func TestBareAPIUnknownActionPrefix(t *testing.T) {
	// fetchUser — "fetch" is not a valid prefix → error about action prefix
	result := analyze(t, `
model User {
  email: String
}
api fetchUser
`)
	expectError(t, result, "must start with get/list/count/exists/delete")
}

func TestBareAPIExpectedByAfterModel(t *testing.T) {
	// getUserNameSuffix — something after model name that is not By/OrderBy
	result := analyze(t, `
model User {
  email: String
}
api getUserNameSuffix
`)
	expectError(t, result, "expected 'By' after model name")
}

func TestBareAPIOrderByValid(t *testing.T) {
	// listUsersOrderByName — OrderBy is accepted without field validation → no error
	result := analyze(t, `
model User {
  name: String
}
api listUsersOrderByName
`)
	expectNoErrors(t, result)
}

func TestBareAPIWithBody(t *testing.T) {
	// API with body is skipped by validateBareAPI → no error
	result := analyze(t, `
model User {
  email: String
}
api getUser(): Int {
  1
}
`)
	expectNoErrors(t, result)
}

func TestBareAPIWithNativeDirective(t *testing.T) {
	// @native API is skipped by validateBareAPI → no error
	result := analyze(t, `
api oauthCallback(): Int @native
`)
	expectNoErrors(t, result)
}

// ========== splitByAndOr unit tests ==========

func TestSplitByAndOrSimple(t *testing.T) {
	parts := splitByAndOr("EmailAndNameOrAge")
	want := []string{"Email", "Name", "Age"}
	if len(parts) != len(want) {
		t.Fatalf("splitByAndOr(%q) = %v, want %v", "EmailAndNameOrAge", parts, want)
	}
	for i, p := range parts {
		if p != want[i] {
			t.Errorf("parts[%d] = %q, want %q", i, p, want[i])
		}
	}
}

func TestSplitByAndOrSingleSegment(t *testing.T) {
	parts := splitByAndOr("Email")
	if len(parts) != 1 || parts[0] != "Email" {
		t.Errorf("splitByAndOr(%q) = %v, want [Email]", "Email", parts)
	}
}

func TestSplitByAndOrOnlyAnd(t *testing.T) {
	parts := splitByAndOr("EmailAndName")
	want := []string{"Email", "Name"}
	if len(parts) != 2 || parts[0] != want[0] || parts[1] != want[1] {
		t.Errorf("splitByAndOr(%q) = %v, want %v", "EmailAndName", parts, want)
	}
}

func TestSplitByAndOrOnlyOr(t *testing.T) {
	parts := splitByAndOr("EmailOrName")
	want := []string{"Email", "Name"}
	if len(parts) != 2 || parts[0] != want[0] || parts[1] != want[1] {
		t.Errorf("splitByAndOr(%q) = %v, want %v", "EmailOrName", parts, want)
	}
}

func TestSplitByAndOrEmpty(t *testing.T) {
	parts := splitByAndOr("")
	if len(parts) != 0 {
		t.Errorf("splitByAndOr(%q) = %v, want []", "", parts)
	}
}

func TestSplitByAndOrLeadingAnd(t *testing.T) {
	// "AndName" — no lowercase char before "And", not a word boundary, kept as one segment
	parts := splitByAndOr("AndName")
	if len(parts) != 1 || parts[0] != "AndName" {
		t.Errorf("splitByAndOr(%q) = %v, want [AndName]", "AndName", parts)
	}
}

func TestSplitByAndOrFieldWithOr(t *testing.T) {
	// "OrderId" — "Or" inside field name, should NOT split
	parts := splitByAndOr("OrderId")
	if len(parts) != 1 || parts[0] != "OrderId" {
		t.Errorf("splitByAndOr(%q) = %v, want [OrderId]", "OrderId", parts)
	}
}

func TestSplitByAndOrFieldWithAnd(t *testing.T) {
	// "SandyEmail" — "And" inside field name, should NOT split
	parts := splitByAndOr("SandyEmail")
	if len(parts) != 1 || parts[0] != "SandyEmail" {
		t.Errorf("splitByAndOr(%q) = %v, want [SandyEmail]", "SandyEmail", parts)
	}
}

// ========== @upload Directive ==========

func TestDirectiveUploadLookup(t *testing.T) {
	d := LookupDirective("upload")
	if d == nil {
		t.Fatal("expected to find @upload")
	}
	if d.Contexts&OnField == 0 {
		t.Error("@upload should be allowed on field")
	}
	if len(d.TypeConstraint) != 1 || d.TypeConstraint[0] != "String" {
		t.Errorf("@upload type constraint = %v, want [String]", d.TypeConstraint)
	}
	if d.MaxArgs != 3 {
		t.Errorf("@upload maxArgs = %d, want 3", d.MaxArgs)
	}
}

func TestDirectiveUploadOnString(t *testing.T) {
	result := analyze(t, `model User { avatar: String @upload }`)
	expectNoErrors(t, result)
}

func TestDirectiveUploadOnInt(t *testing.T) {
	result := analyze(t, `model User { avatar: Int @upload }`)
	expectError(t, result, "@upload can only be used on String")
}

func TestDirectiveUploadWithParams(t *testing.T) {
	result := analyze(t, `model User { avatar: String @upload(bucket: "avatars", maxSize: "5mb", accept: "image/*") }`)
	expectNoErrors(t, result)
}

func TestDirectiveUploadTooManyArgs(t *testing.T) {
	result := analyze(t, `model User { avatar: String @upload(bucket: "a", maxSize: "5mb", accept: "image/*", extra: "bad") }`)
	expectError(t, result, "at most 3")
}

func TestDirectiveUploadOnModel(t *testing.T) {
	result := analyze(t, `model User @upload { name: String }`)
	expectError(t, result, "@upload cannot be used on model")
}
