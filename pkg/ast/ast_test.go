package ast

import (
	"testing"
	"unsafe"

	"github.com/light-speak/luxo/pkg/token"
)

func TestNarrowingMetadataDoesNotGrowTypeTags(t *testing.T) {
	type previousTags struct {
		TypeTag      string
		NullableTag  bool
		ListTypeTag  bool
		ModelTypeTag bool
	}
	if got, previous := unsafe.Sizeof(TypeTagged{}), unsafe.Sizeof(previousTags{}); got != previous {
		t.Fatalf("type tag size grew: %d -> %d", previous, got)
	}
}

func TestExprGetPos(t *testing.T) {
	pos := token.Position{File: "test.luxo", Line: 10, Col: 5, Offset: 100}

	exprs := []Expr{
		&Literal{Pos: pos},
		&Ident{Pos: pos},
		&MemberExpr{Pos: pos},
		&CallExpr{Pos: pos},
		&BinaryExpr{Pos: pos},
		&UnaryExpr{Pos: pos},
		&ElvisExpr{Pos: pos},
		&BangElvisExpr{Pos: pos},
		&WhenExpr{Pos: pos},
		&LambdaExpr{Pos: pos},
		&ListExpr{Pos: pos},
		&ObjectExpr{Pos: pos},
		&TemplateString{Pos: pos},
		&RangeExpr{Pos: pos},
		&TransactionExpr{Pos: pos},
		&YieldExpr{Pos: pos},
		&AsyncExpr{Pos: pos},
		&AwaitExpr{Pos: pos},
		&ForStmt{Pos: pos},
	}

	for _, e := range exprs {
		got := e.GetPos()
		if got != pos {
			t.Errorf("%T.GetPos() = %v, want %v", e, got, pos)
		}
	}
}

func TestStmtGetPos(t *testing.T) {
	pos := token.Position{File: "test.luxo", Line: 3, Col: 1, Offset: 20}

	stmts := []Stmt{
		&ValStmt{Pos: pos},
		&IfStmt{Pos: pos},
		&ForStmt{Pos: pos},
		&ReturnStmt{Pos: pos},
		&ThrowStmt{Pos: pos},
		&EmitStmt{Pos: pos},
		&ExprStmt{Pos: pos},
		&AssignStmt{Pos: pos},
		&BreakStmt{Pos: pos},
		&ContinueStmt{Pos: pos},
		&OnDecl{Pos: pos},
	}

	for _, s := range stmts {
		got := s.GetPos()
		if got != pos {
			t.Errorf("%T.GetPos() = %v, want %v", s, got, pos)
		}
	}
}

func TestExprNodeInterface(t *testing.T) {
	// Verify all Expr types implement the interface marker method.
	exprs := []Expr{
		&Literal{}, &Ident{}, &MemberExpr{}, &CallExpr{},
		&BinaryExpr{}, &UnaryExpr{}, &ElvisExpr{}, &WhenExpr{},
		&LambdaExpr{}, &ListExpr{}, &ObjectExpr{}, &TemplateString{},
		&RangeExpr{}, &TransactionExpr{},
		&YieldExpr{}, &AsyncExpr{}, &AwaitExpr{}, &ForStmt{},
	}
	for _, e := range exprs {
		e.exprNode() // just ensure it compiles and doesn't panic
	}
}

func TestStmtNodeInterface(t *testing.T) {
	stmts := []Stmt{
		&ValStmt{}, &IfStmt{}, &ForStmt{},
		&ReturnStmt{}, &ThrowStmt{}, &EmitStmt{}, &ExprStmt{},
		&AssignStmt{}, &BreakStmt{}, &OnDecl{},
	}
	for _, s := range stmts {
		s.stmtNode()
	}
}

func TestEffectiveAPIParamsInjectsPaginationDefaults(t *testing.T) {
	api := &ApiDecl{
		Params: []*ParamDecl{{Name: "status", Type: &TypeRef{Name: "String", Nullable: true}}},
		Directives: []*Directive{{Name: "paginate", Args: []*NamedArg{{
			Name:  "defaultPageSize",
			Value: &Literal{Kind: token.Int, Value: "50"},
		}}}},
	}

	params := api.EffectiveParams()
	if len(params) != 3 || params[0].Name != "status" || params[1].Name != "page" || params[2].Name != "pageSize" {
		t.Fatalf("effective params = %+v", params)
	}
	if len(api.Params) != 1 {
		t.Fatalf("EffectiveParams mutated source params: %+v", api.Params)
	}
	if got := params[1].Default.(*Literal).Value; got != "1" {
		t.Fatalf("page default = %s, want 1", got)
	}
	if got := params[2].Default.(*Literal).Value; got != "50" {
		t.Fatalf("pageSize default = %s, want 50", got)
	}
	if got := api.DefaultPageSize(); got != 50 {
		t.Fatalf("DefaultPageSize() = %d, want 50", got)
	}
}

func TestEffectiveAPIParamsDoesNotInjectWithoutPaginate(t *testing.T) {
	api := &ApiDecl{Params: []*ParamDecl{{Name: "page", Type: &TypeRef{Name: "String"}}}}
	params := api.EffectiveParams()
	if len(params) != 1 || params[0] != api.Params[0] {
		t.Fatalf("effective params = %+v", params)
	}
	if got := api.DefaultPageSize(); got != 20 {
		t.Fatalf("DefaultPageSize() = %d, want 20", got)
	}
}

func TestPaginationHelpersHandleDefensiveInputs(t *testing.T) {
	var missing *ApiDecl
	if missing.EffectiveParams() != nil || missing.DefaultPageSize() != DefaultPaginationPageSize {
		t.Fatal("nil API should use empty params and the protocol default")
	}

	tests := []struct {
		name string
		arg  Expr
	}{
		{"non-literal", &Ident{Name: "size"}},
		{"wrong literal type", &Literal{Kind: token.String, Value: "50"}},
		{"non-positive", &Literal{Kind: token.Int, Value: "0"}},
		{"overflow", &Literal{Kind: token.Int, Value: "999999999999999999999999999999"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &ApiDecl{Directives: []*Directive{{Name: "paginate", Args: []*NamedArg{{Value: tt.arg}}}}}
			if got := api.DefaultPageSize(); got != DefaultPaginationPageSize {
				t.Fatalf("DefaultPageSize() = %d, want %d", got, DefaultPaginationPageSize)
			}
		})
	}

	page := paginationParam("page", "3")
	pageSize := paginationParam("pageSize", "9")
	api := &ApiDecl{
		Params:     []*ParamDecl{page, pageSize},
		Directives: []*Directive{{Name: "paginate"}},
	}
	params := api.EffectiveParams()
	if len(params) != 2 || params[0] != page || params[1] != pageSize {
		t.Fatalf("explicit pagination params were replaced: %+v", params)
	}
}

func TestWalkYieldsExcludesNestedForExpressions(t *testing.T) {
	outer := &YieldExpr{Value: &Ident{Name: "outer"}}
	inner := &YieldExpr{Value: &Ident{Name: "inner"}}
	loop := &ForStmt{Body: &Block{Stmts: []Stmt{&ExprStmt{Expr: inner}}}}
	root := &Block{Stmts: []Stmt{
		&ValStmt{Name: "inner", Value: loop},
		&ForStmt{Body: &Block{Stmts: []Stmt{&ExprStmt{Expr: outer}}}},
	}}
	var seen []*YieldExpr
	visit := func(value *YieldExpr) { seen = append(seen, value) }
	WalkYields(root, visit)
	if len(seen) != 1 || seen[0] != outer {
		t.Fatalf("owned yields = %v, want only outer yield", seen)
	}
	WalkExpressionYields(loop, visit)
	if len(seen) != 1 {
		t.Fatal("nested expression yield escaped its scope")
	}
	WalkExpressionYields(outer, visit)
	if len(seen) != 2 || seen[1] != outer {
		t.Fatal("direct yield was skipped")
	}
}

func TestWalkExprsTraversesAllNestedExpressionBlocks(t *testing.T) {
	blockWith := func(name string) *Block {
		return &Block{Stmts: []Stmt{&ExprStmt{Expr: &Ident{Name: name}}}}
	}
	root := &Block{Stmts: []Stmt{
		&ExprStmt{Expr: &RangeExpr{Start: &Ident{Name: "rangeStart"}, End: &Ident{Name: "rangeEnd"}}},
		&ExprStmt{Expr: &TransactionExpr{Body: blockWith("transaction")}},
		&ExprStmt{Expr: &YieldExpr{Value: &Ident{Name: "yield"}}},
		&ExprStmt{Expr: &AsyncExpr{Body: blockWith("async")}},
		&ExprStmt{Expr: &AwaitExpr{Body: blockWith("await")}},
		&ExprStmt{Expr: &ForStmt{
			Collection: &Ident{Name: "collection"},
			Body:       blockWith("forBody"),
		}},
	}}
	seen := make(map[string]bool)
	WalkExprs(root, func(expr Expr) {
		if ident, ok := expr.(*Ident); ok {
			seen[ident.Name] = true
		}
	})
	for _, name := range []string{"rangeStart", "rangeEnd", "transaction", "yield", "async", "await", "collection", "forBody"} {
		if !seen[name] {
			t.Errorf("WalkExprs did not visit %q", name)
		}
	}
}

func TestStructFieldAccessDecls(t *testing.T) {
	m := &ModelDecl{Name: "User", Parents: []string{"Base"}}
	if m.Name != "User" || len(m.Parents) != 1 {
		t.Error("ModelDecl fields not accessible")
	}

	f := &FieldDecl{Name: "email", Doc: "email field"}
	if f.Name != "email" || f.Doc != "email field" {
		t.Error("FieldDecl fields not accessible")
	}

	tr := &TypeRef{Name: "String", Nullable: true, IsList: false}
	if !tr.Nullable || tr.IsList {
		t.Error("TypeRef fields not accessible")
	}

	file := &File{Name: "test.luxo"}
	if file.Name != "test.luxo" {
		t.Error("File.Name not accessible")
	}

	d := &Directive{Name: "cache", Args: []*NamedArg{{Name: "ttl", Value: &Literal{Value: "60"}}}}
	if d.Name != "cache" || len(d.Args) != 1 {
		t.Error("Directive fields not accessible")
	}
}

func TestCRUDModelNames(t *testing.T) {
	for name, plural := range map[string]string{
		"User": "Users", "Address": "Addresses", "Box": "Boxes", "Quiz": "Quizes",
		"TimeEntry": "TimeEntries", "Gateway": "Gateways", "Key": "Keys", "y": "ys",
	} {
		if got := PluralModelName(name); got != plural {
			t.Errorf("PluralModelName(%q) = %q, want %q", name, got, plural)
		}
		for operation, want := range map[string]string{
			"get": "get" + name, "list": "list" + plural, "create": "create" + name,
			"update": "update" + name, "delete": "delete" + name, "deleteMany": "delete" + plural,
		} {
			if got := CRUDAPIName(name, operation); got != want {
				t.Errorf("CRUDAPIName(%q, %q) = %q, want %q", name, operation, got, want)
			}
		}
	}
}

func TestModelCRUDOperations(t *testing.T) {
	list := func(names ...string) *ListExpr {
		items := make([]Expr, 0, len(names))
		for _, name := range names {
			items = append(items, &Ident{Name: name})
		}
		return &ListExpr{Items: items}
	}
	crud := func(args ...*NamedArg) *ModelDecl {
		return &ModelDecl{Directives: []*Directive{{Name: "crud", Args: args}}}
	}
	mixedList := &ListExpr{Items: []Expr{&Literal{}, &Ident{Name: "get"}}}
	tests := []struct {
		name       string
		model      *ModelDecl
		operations []string
	}{
		{name: "nil model"},
		{name: "without crud", model: &ModelDecl{}},
		{name: "all", model: crud(), operations: []string{"get", "list", "create", "update", "delete", "deleteMany"}},
		{name: "only", model: crud(&NamedArg{Name: "only", Value: list("get", "list")}), operations: []string{"get", "list"}},
		{name: "except", model: crud(&NamedArg{Name: "except", Value: list("update", "delete")}), operations: []string{"get", "list", "create", "deleteMany"}},
		{name: "invalid only value", model: crud(&NamedArg{Name: "only", Value: &Ident{Name: "get"}})},
		{name: "unknown argument", model: crud(&NamedArg{Name: "other"}), operations: []string{"get", "list", "create", "update", "delete", "deleteMany"}},
		{name: "skip non-ident", model: crud(&NamedArg{Name: "only", Value: mixedList}), operations: []string{"get"}},
		{name: "skip other directive", model: &ModelDecl{Directives: []*Directive{{Name: "auth"}, {Name: "crud"}}}, operations: []string{"get", "list", "create", "update", "delete", "deleteMany"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.model.CRUDOperations()
			if len(got) != len(tt.operations) {
				t.Fatalf("CRUDOperations() = %v, want %v", got, tt.operations)
			}
			for i := range got {
				if got[i] != tt.operations[i] {
					t.Fatalf("CRUDOperations() = %v, want %v", got, tt.operations)
				}
			}
		})
	}
}

func TestStructFieldAccessNodes(t *testing.T) {
	wb := &WhenBranch{IsType: "User", Body: &Ident{Name: "x"}}
	if wb.IsType != "User" {
		t.Error("WhenBranch fields not accessible")
	}

	sv := &SealedVariant{Name: "Success"}
	if sv.Name != "Success" {
		t.Error("SealedVariant fields not accessible")
	}

	cf := &ComputedField{}
	if cf.Body != nil {
		t.Error("ComputedField.Body should be nil")
	}

	pd := &ParamDecl{Name: "id"}
	if pd.Name != "id" {
		t.Error("ParamDecl fields not accessible")
	}

	ev := &EventDecl{Name: "UserCreated", Pos: token.Position{Line: 1, Col: 1}}
	if ev.Name != "UserCreated" || ev.GetPos().Line != 1 {
		t.Error("EventDecl fields not accessible")
	}

	on := &OnDecl{EventName: "UserCreated", Native: true, Pos: token.Position{Line: 2, Col: 1}}
	if on.EventName != "UserCreated" || !on.Native || on.GetPos().Line != 2 {
		t.Error("OnDecl fields not accessible")
	}

	es := &EmitStmt{EventName: "UserCreated"}
	if es.EventName != "UserCreated" {
		t.Error("EmitStmt.EventName not accessible")
	}
}
