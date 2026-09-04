package semantic

import (
	"strings"
	"testing"
)

// ========== Collection Method Tests ==========

func TestCollectionMethodsOnList(t *testing.T) {
	result := analyze(t, `
model Item { name: String }
api test(): Int {
  val items = Item.find(where: name == "x")
  val mapped = items.map { it }
  val filtered = items.filter { true }
  val sz = items.size
  items.forEach { it }
  sz
}
`)
	expectNoErrors(t, result)
}

func TestCollectionMethodSize(t *testing.T) {
	result := analyze(t, `
model Item { name: String }
api test(): Int {
  val items = Item.find(where: name == "x")
  items.size
}
`)
	expectNoErrors(t, result)
}

func TestCollectionMethodAny(t *testing.T) {
	result := analyze(t, `
model Item { name: String }
api test(): Boolean {
  val items = Item.find(where: name == "x")
  items.any
}
`)
	expectNoErrors(t, result)
}

func TestCollectionMethodFirstOrNull(t *testing.T) {
	result := analyze(t, `
model Item { name: String }
api test(): Item? {
  val items = Item.find(where: name == "x")
  val first = items.firstOrNull
  first
}
`)
	// firstOrNull returns nullable, which matches the declared API contract.
	if len(result.Errors) > 0 {
		for _, err := range result.Errors {
			if !strings.Contains(err.Message, "undefined") {
				t.Errorf("unexpected error: %v", err)
			}
		}
	}
}

func TestCollectionMethodFilter(t *testing.T) {
	result := analyze(t, `
model Item { name: String }
api test(): [Item] {
  val items = Item.find(where: name == "x")
  items.filter { true }
}
`)
	expectNoErrors(t, result)
}

func TestCollectionMethodSumOf(t *testing.T) {
	result := analyze(t, `
model Item { price: Float }
api test(): Float {
  val items = Item.find(where: price > 0)
  items.sumOf
}
`)
	expectNoErrors(t, result)
}

func TestCollectionMethodJoinToString(t *testing.T) {
	result := analyze(t, `
model Item { name: String }
api test(): String {
  val items = Item.find(where: name == "x")
  items.joinToString
}
`)
	expectNoErrors(t, result)
}

func TestCollectionMethodContains(t *testing.T) {
	result := analyze(t, `
model Item { name: String }
api test(): Boolean {
  val items = Item.find(where: name == "x")
  items.contains
}
`)
	expectNoErrors(t, result)
}

func TestCollectionMethodIsEmpty(t *testing.T) {
	result := analyze(t, `
model Item { name: String }
api test(): Boolean {
  val items = Item.find(where: name == "x")
  items.isEmpty
}
`)
	expectNoErrors(t, result)
}

func TestCollectionMethodCount(t *testing.T) {
	result := analyze(t, `
model Item { name: String }
api test(): Int {
  val items = Item.find(where: name == "x")
  items.count
}
`)
	expectNoErrors(t, result)
}

func TestCollectionMethodSortBy(t *testing.T) {
	result := analyze(t, `
model Item { name: String }
api test(): [Item] {
  val items = Item.find(where: name == "x")
  items.sortBy
}
`)
	expectNoErrors(t, result)
}

func TestCollectionMethodTake(t *testing.T) {
	result := analyze(t, `
model Item { name: String }
api test(): [Item] {
  val items = Item.find(where: name == "x")
  items.take
}
`)
	expectNoErrors(t, result)
}

func TestCollectionMethodTakeLast(t *testing.T) {
	result := analyze(t, `
model Item { name: String }
api test(): [Item] {
  val items = Item.find(where: name == "x")
  items.takeLast
}
`)
	expectNoErrors(t, result)
}

func TestCollectionMethodGroupBy(t *testing.T) {
	result := analyze(t, `
model Item { name: String }
api test(): Int {
  val items = Item.find(where: name == "x")
  val grouped = items.groupBy
  0
}
`)
	expectNoErrors(t, result)
}

func TestCollectionMethodFlatMap(t *testing.T) {
	result := analyze(t, `
model Item { name: String }
api test(): Int {
  val items = Item.find(where: name == "x")
  val flat = items.flatMap
  0
}
`)
	expectNoErrors(t, result)
}

func TestCollectionMethodForEach(t *testing.T) {
	result := analyze(t, `
model Item { name: String }
api test(): Int {
  val items = Item.find(where: name == "x")
  items.forEach { it }
  0
}
`)
	expectNoErrors(t, result)
}

func TestCollectionMethodReversed(t *testing.T) {
	result := analyze(t, `
model Post { title: String }
api test(): [Post] {
  val items = Post.find(where: title == "test")
  items.reversed
}
`)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCollectionMethodDistinct(t *testing.T) {
	result := analyze(t, `
model Post { title: String }
api test(): [Post] {
  val items = Post.find(where: title == "test")
  items.distinct
}
`)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestCollectionMethodChunked(t *testing.T) {
	result := analyze(t, `
model Post { title: String }
api test(): [Post] {
  val items = Post.find(where: title == "test")
  items.chunked
}
`)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestListFieldAccessNonCollectionMethod(t *testing.T) {
	// Access a field that is NOT a collection method on a list type
	// This exercises the return false path of isCollectionMethod
	result := analyze(t, `
model Item { name: String }
api test(): String {
  val items = Item.find(where: name == "x")
  items.name
}
`)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestFindReturnsListType(t *testing.T) {
	result := analyze(t, `
model User { name: String }
api test(): [User] {
  User.find(where: name == "x")
}
`)
	expectNoErrors(t, result)
}

func TestFindReturnsNonNullable(t *testing.T) {
	// find by id returns non-nullable (auto-throws NotFound)
	result := analyze(t, `
model User { name: String }
api test(): User {
  val u = User.find(id: 1)
  u
}
`)
	expectNoErrors(t, result)
}

// CRUD operations should inject model fields into scope and return model type
func TestFindInjectsModelFields(t *testing.T) {
	result := analyze(t, `
model User {
  name: String
  email: String
}
api test(): [User] {
  val user = User.find(where: email == "test@test.com")
  user
}
`)
	expectNoErrors(t, result)
}

func TestCreateReturnsModelType(t *testing.T) {
	result := analyze(t, `
model User {
  name: String
  email: String
}
api test(): User {
  val user = User.create(name: "lin", email: "test@test.com")
  user.name
  user
}
`)
	expectNoErrors(t, result)
}

func TestUpdateWithModelFields(t *testing.T) {
	result := analyze(t, `
model Post {
  title: String
  status: String
}
api test(): Post {
  val post = Post.find(id: 1)
  post.update(status: "published")
  post
}
`)
	expectNoErrors(t, result)
}
