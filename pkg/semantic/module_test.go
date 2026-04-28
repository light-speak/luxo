package semantic

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
)

// parseFile parses input as a .luxo file with the given filename.
func parseFileWithName(t *testing.T, input, filename string) *ast.File {
	t.Helper()
	l := lexer.New(input, filename)
	tokens, lexErrors := l.Tokenize()
	if len(lexErrors) > 0 {
		t.Fatalf("lexer errors in %s: %v", filename, lexErrors)
	}
	p := parser.New(tokens)
	file, parseErrors := p.Parse(filename)
	if len(parseErrors) > 0 {
		t.Fatalf("parser errors in %s: %v", filename, parseErrors)
	}
	return file
}

// analyzeModules runs AnalyzeWithModules on multiple files.
func analyzeModules(t *testing.T, files []*ast.File) *Result {
	t.Helper()
	a := New()
	return a.AnalyzeWithModules(files)
}

// ========== FileToModule Tests ==========

func TestFileToModule_FileBasedModule(t *testing.T) {
	mi := FileToModule("/path/to/origin/user.luxo")
	if mi.Name != "user" {
		t.Errorf("expected module 'user', got %q", mi.Name)
	}
	if mi.IsCommon {
		t.Error("expected IsCommon=false for user module")
	}
}

func TestFileToModule_FolderBasedModule(t *testing.T) {
	mi := FileToModule("/path/to/origin/post/model.luxo")
	if mi.Name != "post" {
		t.Errorf("expected module 'post', got %q", mi.Name)
	}
	if mi.IsCommon {
		t.Error("expected IsCommon=false for post module")
	}
}

func TestFileToModule_FolderBasedModuleDeep(t *testing.T) {
	mi := FileToModule("/path/to/origin/post/sub/deep.luxo")
	if mi.Name != "post" {
		t.Errorf("expected module 'post', got %q", mi.Name)
	}
}

func TestFileToModule_CommonModule(t *testing.T) {
	mi := FileToModule("/path/to/origin/common/base.luxo")
	if mi.Name != "common" {
		t.Errorf("expected module 'common', got %q", mi.Name)
	}
	if !mi.IsCommon {
		t.Error("expected IsCommon=true for common module")
	}
}

func TestFileToModule_CommonFile(t *testing.T) {
	mi := FileToModule("/path/to/origin/common.luxo")
	if mi.Name != "common" {
		t.Errorf("expected module 'common', got %q", mi.Name)
	}
	if !mi.IsCommon {
		t.Error("expected IsCommon=true for common.luxo")
	}
}

func TestFileToModule_NoOrigin(t *testing.T) {
	mi := FileToModule("/some/path/test.luxo")
	if mi.Name != "test" {
		t.Errorf("expected module 'test', got %q", mi.Name)
	}
}

// ========== Cross-Module Visibility Tests ==========

func TestModuleCrossModuleTypeWithoutExtend(t *testing.T) {
	// user.luxo defines model User and enum Role
	userFile := parseFileWithName(t, `
model User {
  name: String
  role: Role
}

enum Role {
  USER
  ADMIN
}
`, "/project/origin/user.luxo")

	// post.luxo uses Role directly without extend — should ERROR
	postFile := parseFileWithName(t, `
model Post {
  title: String
  authorRole: Role
}
`, "/project/origin/post.luxo")

	result := analyzeModules(t, []*ast.File{userFile, postFile})

	// should have error about Role being from user module
	found := false
	for _, err := range result.Errors {
		if strings.Contains(err.Message, "Role") && strings.Contains(err.Message, "module") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about cross-module type 'Role', got errors: %v", result.Errors)
	}
}

func TestModuleExtendMakesTypeVisible(t *testing.T) {
	// user.luxo defines model User
	userFile := parseFileWithName(t, `
model User {
  name: String
}
`, "/project/origin/user.luxo")

	// post.luxo extends User — should be OK to reference User
	postFile := parseFileWithName(t, `
model Post {
  title: String
  author: User
}

extend User {
  posts: [Post]
}
`, "/project/origin/post.luxo")

	result := analyzeModules(t, []*ast.File{userFile, postFile})

	// should have NO error about User visibility (extend makes it visible)
	for _, err := range result.Errors {
		if strings.Contains(err.Message, "module") && strings.Contains(err.Message, "User") {
			t.Errorf("unexpected module visibility error: %v", err)
		}
	}
}

func TestModuleCommonTypesVisibleEverywhere(t *testing.T) {
	// common/base.luxo defines interface Base
	commonFile := parseFileWithName(t, `
interface Base {
  id: Int
  createdAt: DateTime
}
`, "/project/origin/common/base.luxo")

	// user.luxo uses Base — should be OK without extend
	userFile := parseFileWithName(t, `
model User : Base {
  name: String
}
`, "/project/origin/user.luxo")

	result := analyzeModules(t, []*ast.File{commonFile, userFile})

	// should have no error about Base visibility
	for _, err := range result.Errors {
		if strings.Contains(err.Message, "module") && strings.Contains(err.Message, "Base") {
			t.Errorf("unexpected module visibility error: %v", err)
		}
	}
}

func TestModuleSameNameTypeDifferentModules(t *testing.T) {
	// user.luxo defines enum Role
	userFile := parseFileWithName(t, `
enum Role {
  USER
  ADMIN
}
`, "/project/origin/user.luxo")

	// post.luxo also defines enum Role — should ERROR (cross-module duplicate)
	postFile := parseFileWithName(t, `
enum Role {
  AUTHOR
  EDITOR
}
`, "/project/origin/post.luxo")

	result := analyzeModules(t, []*ast.File{userFile, postFile})

	// should have error about duplicate type across modules
	found := false
	for _, err := range result.Errors {
		if strings.Contains(err.Message, "Role") && strings.Contains(err.Message, "already declared") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about same-named type 'Role' in different modules, got errors: %v", result.Errors)
	}
}

func TestModuleSameModuleTypesVisible(t *testing.T) {
	// post/model.luxo defines model Post and enum PostStatus
	postModelFile := parseFileWithName(t, `
model Post {
  title: String
  status: PostStatus
}
`, "/project/origin/post/model.luxo")

	// post/enums.luxo defines PostStatus — same module "post"
	postEnumsFile := parseFileWithName(t, `
enum PostStatus {
  DRAFT
  PUBLISHED
}
`, "/project/origin/post/enums.luxo")

	result := analyzeModules(t, []*ast.File{postModelFile, postEnumsFile})

	// should have NO module visibility errors
	for _, err := range result.Errors {
		if strings.Contains(err.Message, "module") {
			t.Errorf("unexpected module visibility error: %v", err)
		}
	}
}

func TestModuleNoModuleIsolationWithAnalyze(t *testing.T) {
	// When using Analyze() (not AnalyzeWithModules), no module isolation is enforced.
	userFile := parseFileWithName(t, `
model User {
  name: String
}

enum Role {
  USER
  ADMIN
}
`, "/project/origin/user.luxo")

	postFile := parseFileWithName(t, `
model Post {
  title: String
  authorRole: Role
}
`, "/project/origin/post.luxo")

	a := New()
	result := a.Analyze([]*ast.File{userFile, postFile})

	// no module errors because Analyze doesn't enforce modules
	for _, err := range result.Errors {
		if strings.Contains(err.Message, "module") {
			t.Errorf("unexpected module error with Analyze(): %v", err)
		}
	}
}

func TestModuleExtendVisibilityForApiReturnType(t *testing.T) {
	// user.luxo defines model User
	userFile := parseFileWithName(t, `
model User {
  name: String
}
`, "/project/origin/user.luxo")

	// post.luxo uses User as API return type — needs extend
	postFile := parseFileWithName(t, `
model Post {
  title: String
}

extend User {
  posts: [Post]
}

api getPostAuthor(postId: Int): User {
  return find(User, id: postId)
}
`, "/project/origin/post.luxo")

	result := analyzeModules(t, []*ast.File{userFile, postFile})

	// User is made visible by extend — no module visibility error
	for _, err := range result.Errors {
		if strings.Contains(err.Message, "module") && strings.Contains(err.Message, "User") {
			t.Errorf("unexpected module visibility error: %v", err)
		}
	}
}

func TestModuleCrossModuleWithoutExtendError(t *testing.T) {
	// user.luxo defines model User
	userFile := parseFileWithName(t, `
model User {
  name: String
}
`, "/project/origin/user.luxo")

	// post.luxo references User without extend — ERROR
	postFile := parseFileWithName(t, `
api getAuthor(postId: Int): User {
  return find(User, id: postId)
}
`, "/project/origin/post.luxo")

	result := analyzeModules(t, []*ast.File{userFile, postFile})

	found := false
	for _, err := range result.Errors {
		if strings.Contains(err.Message, "User") && strings.Contains(err.Message, "extend") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about 'User' requiring extend, got errors: %v", result.Errors)
	}
}

func TestBuildFileModuleMap(t *testing.T) {
	files := []*ast.File{
		{Name: "/project/origin/user.luxo"},
		{Name: "/project/origin/post/model.luxo"},
		{Name: "/project/origin/common/base.luxo"},
	}
	m := BuildFileModuleMap(files)

	if m["/project/origin/user.luxo"].Name != "user" {
		t.Errorf("expected module 'user', got %q", m["/project/origin/user.luxo"].Name)
	}
	if m["/project/origin/post/model.luxo"].Name != "post" {
		t.Errorf("expected module 'post', got %q", m["/project/origin/post/model.luxo"].Name)
	}
	if !m["/project/origin/common/base.luxo"].IsCommon {
		t.Error("expected IsCommon=true for common module")
	}
}

func TestModuleFnParamCrossRef(t *testing.T) {
	// fn using a type from another module without extend → error
	fileA := parseFileWithName(t, `model User { id: Int }`, "origin/user.luxo")
	fileB := parseFileWithName(t, `fn doSomething(user: User): String { return "" }`, "origin/post.luxo")

	a := New()
	result := a.AnalyzeWithModules([]*ast.File{fileA, fileB})
	found := false
	for _, e := range result.Errors {
		if strings.Contains(e.Message, "User") {
			found = true
		}
	}
	if !found {
		t.Error("expected error for fn using User without extend")
	}
}

func TestModuleEventParamCrossRef(t *testing.T) {
	fileA := parseFileWithName(t, `model Order { id: Int }`, "origin/order.luxo")
	fileB := parseFileWithName(t, `event OrderCreated(order: Order)`, "origin/user.luxo")

	a := New()
	result := a.AnalyzeWithModules([]*ast.File{fileA, fileB})
	found := false
	for _, e := range result.Errors {
		if strings.Contains(e.Message, "Order") {
			found = true
		}
	}
	if !found {
		t.Error("expected error for event using Order without extend")
	}
}

func TestModuleExtendMakesTypeVisibleForFn(t *testing.T) {
	fileA := parseFileWithName(t, `model User { id: Int }`, "origin/user.luxo")
	fileB := parseFileWithName(t, `
extend User { id: Int }
fn getUser(id: Int): User { return nil }
`, "origin/post.luxo")

	a := New()
	result := a.AnalyzeWithModules([]*ast.File{fileA, fileB})
	for _, e := range result.Errors {
		if strings.Contains(e.Message, "User") && strings.Contains(e.Message, "not visible") {
			t.Errorf("User should be visible via extend: %s", e.Message)
		}
	}
}

func TestModuleNilTypeRefDoesNotPanic(t *testing.T) {
	// Field with nil type should not cause panic in visibility check
	fileA := parseFileWithName(t, `model User { id: Int }`, "origin/user.luxo")
	a := New()
	result := a.AnalyzeWithModules([]*ast.File{fileA})
	_ = result // just ensure no panic
}

// ========== load + Cross-Module Restriction Tests ==========

func TestCrossModuleLoadAllowed(t *testing.T) {
	fileUser := parseFileWithName(t, `
model User {
	id: Int
	phone: String
}
`, "origin/user.luxo")

	filePost := parseFileWithName(t, `
model Post {
	id: Int
	userId: Int
}

extend User {
	phone: String
}

api getAuthorPhone(postId: Int): String {
	val post = Post.find(postId)
	val author = User.load(post.userId)
	return author.phone
}
`, "origin/post.luxo")

	result := analyzeModules(t, []*ast.File{fileUser, filePost})
	for _, e := range result.Errors {
		// load should be allowed, no errors about "can only use load"
		if strings.Contains(e.Message, "can only use 'load'") {
			t.Errorf("unexpected error: %s", e.Message)
		}
	}
}

func TestCrossModuleFindForbidden(t *testing.T) {
	fileUser := parseFileWithName(t, `
model User {
	id: Int
	phone: String
}
`, "origin/user.luxo")

	filePost := parseFileWithName(t, `
model Post {
	id: Int
	userId: Int
}

extend User {
	phone: String
}

api getAuthorPhone(postId: Int): String {
	val post = Post.find(postId)
	val author = User.find(post.userId)
	return author.phone
}
`, "origin/post.luxo")

	result := analyzeModules(t, []*ast.File{fileUser, filePost})
	found := false
	for _, e := range result.Errors {
		if strings.Contains(e.Message, "can only use 'load'") && strings.Contains(e.Message, "'find'") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected error: cross-module model 'User' can only use 'load', not 'find'")
	}
}

func TestCrossModuleFieldVisibility(t *testing.T) {
	fileUser := parseFileWithName(t, `
model User {
	id: Int
	phone: String
	email: String
}
`, "origin/user.luxo")

	filePost := parseFileWithName(t, `
model Post {
	id: Int
	userId: Int
}

extend User {
	phone: String
}

api getAuthorEmail(postId: Int): String {
	val post = Post.find(postId)
	val author = User.load(post.userId)
	return author.email
}
`, "origin/post.luxo")

	result := analyzeModules(t, []*ast.File{fileUser, filePost})
	found := false
	for _, e := range result.Errors {
		if strings.Contains(e.Message, "not declared in extend") && strings.Contains(e.Message, "email") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected error: field 'email' not declared in extend User")
	}
}

func TestCrossModuleExtendFieldAccessAllowed(t *testing.T) {
	fileUser := parseFileWithName(t, `
model User {
	id: Int
	phone: String
	email: String
}
`, "origin/user.luxo")

	filePost := parseFileWithName(t, `
model Post {
	id: Int
	userId: Int
}

extend User {
	phone: String
	email: String
}

api getAuthorInfo(postId: Int): String {
	val post = Post.find(postId)
	val author = User.load(post.userId)
	return author.phone
}
`, "origin/post.luxo")

	result := analyzeModules(t, []*ast.File{fileUser, filePost})
	for _, e := range result.Errors {
		if strings.Contains(e.Message, "not declared in extend") {
			t.Errorf("unexpected extend field error: %s", e.Message)
		}
	}
}

func TestSameModuleFindAllowed(t *testing.T) {
	// Same-module model operations should not be restricted
	fileUser := parseFileWithName(t, `
model User {
	id: Int
	name: String
}

api getUser(id: Int): User {
	return User.find(id)
}
`, "origin/user.luxo")

	result := analyzeModules(t, []*ast.File{fileUser})
	for _, e := range result.Errors {
		if strings.Contains(e.Message, "can only use 'load'") {
			t.Errorf("same-module find should be allowed: %s", e.Message)
		}
	}
}
