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
	expectWarning(t, result, "did you mean '@unique'")
}

func TestDirectiveTypoSuggestionApi(t *testing.T) {
	result := analyze(t, `api test(): Int @autth`)
	expectWarning(t, result, "did you mean '@auth'")
}

func TestDirectiveTypoSuggestionModel(t *testing.T) {
	result := analyze(t, `model User @sof { name: String }`)
	expectWarning(t, result, "did you mean '@soft'")
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
	expectNoErrors(t, result)
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

func TestDirectiveRequiredParamProvided(t *testing.T) {
	// @withAuth requires 'secret' — providing it should not error
	result := analyze(t, `
model User @withAuth(secret: "my-secret") {
  name: String
  email: String
}
`)
	for _, err := range result.Errors {
		if strings.Contains(err.Message, "requires parameter 'secret'") {
			t.Errorf("should not error about required param when provided: %s", err.Message)
		}
	}
}

func TestDirectiveRequiredParamMissing(t *testing.T) {
	// @withAuth without 'secret' — should error
	result := analyze(t, `
model User @withAuth(stores: "id") {
  name: String
  email: String
}
`)
	expectError(t, result, "requires parameter 'secret'")
}
