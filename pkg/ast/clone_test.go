package ast_test

import (
	"os"
	"testing"

	"github.com/light-speak/luxo/internal/testutil"
	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
)

func parseCloneFixture(t *testing.T, source string) *ast.File {
	t.Helper()
	tokens, lexErrors := lexer.New(source, "clone.luxo").Tokenize()
	if len(lexErrors) != 0 {
		t.Fatal(lexErrors)
	}
	file, parseErrors := parser.New(tokens).Parse("clone.luxo")
	if len(parseErrors) != 0 {
		t.Fatal(parseErrors)
	}
	return file
}

func cloneFixture(t *testing.T, files []*ast.File) ([]*ast.File, *ast.CloneMap) {
	t.Helper()
	clones, mapping, err := ast.CloneFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	return clones, mapping
}

func analyzedCloneFixture(t *testing.T) *semantic.Result {
	t.Helper()
	file := parseCloneFixture(t, `
model User @noTime @withAuth(stores: [id]) { id: Int @id score: Int = 7 }
enum State { PENDING PAID }
model Order @noTime { id: Int @id status: State amount: Float points: Int }
type Summary { status: State total: Float count: Int }
error Rejected(value: Int) { code: 422 message: "rejected" }
fn must(value: Int = 7): Int { throw Rejected(value: value) }
api current(): Int @auth { return my.id }
api summary(): [Summary] {
 return Order.where(points > 0).groupBy { it.status }.select {
  Summary { status: it.key, total: it.sum { it.amount }, count: it.count() }
 }.having { it.count() > 0 && it.count() < 5 }
}`)
	result := semantic.Analyze([]*ast.File{file})
	if len(result.Errors) != 0 {
		t.Fatal(result.Errors)
	}
	return result
}

func TestCloneFilesAnalyzedMetadataAndIsolation(t *testing.T) {
	result := analyzedCloneFixture(t)
	original := result.Files[0]
	clones, mapping := cloneFixture(t, result.Files)
	clone := clones[0]
	if clone == original || mapping.Files[original] != clone || clone.Name != original.Name {
		t.Fatal("file identity or position changed")
	}
	api := original.APIs[0]
	if mapping.APIs[api] != clone.APIs[0] || mapping.Directives[api.Directives[0]].AuthSubject != "User" {
		t.Fatal("API or semantic authentication identity missing")
	}
	assertCloneExpressionMetadata(t, mapping)
	throw := original.Functions[0].Body.Stmts[0].(*ast.ThrowStmt).Error.(*ast.CallExpr)
	copyThrow := mapping.Exprs[throw].(*ast.CallExpr)
	if throw.ErrorConstructor != original.Errors[0] || copyThrow.ErrorConstructor != clone.Errors[0] || copyThrow.ErrorConstructor == throw.ErrorConstructor {
		t.Fatal("error-constructor identity was not rebound")
	}
	group := original.APIs[1].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	copyGroup := mapping.Exprs[group].(*ast.CallExpr)
	if group.Group == nil || copyGroup.Group == group.Group || copyGroup.Group.Input != mapping.Exprs[group.Group.Input] || len(copyGroup.Group.Having) == 0 {
		t.Fatal("checked group projection was lost or aliases its source")
	}
	copyGroup.Group.Columns[0].Field = "changed"
	copyGroup.Group.Keys[0] = "changed"
	copyGroup.Group.Having[0].Operator = "changed"
	clone.Models[0].Fields[1].Default.(*ast.Literal).Value = "9"
	clone.APIs[0].Directives[0].AuthSubject = "changed"
	mapping.TypeRefs[original.Functions[0].ReturnType].Name = "String"
	if group.Group.Columns[0].Field == "changed" || group.Group.Keys[0] == "changed" || group.Group.Having[0].Operator == "changed" || original.Models[0].Fields[1].Default.(*ast.Literal).Value != "7" || api.Directives[0].AuthSubject != "User" || original.Functions[0].ReturnType.Name != "Int" {
		t.Fatal("editing the compilation tree changed the LSP tree")
	}
}

func TestCloneFilesSharedDefaultsAndCycles(t *testing.T) {
	file := analyzedCloneFixture(t).Files[0]
	shared := file.Models[0].Fields[1].Default
	file.Functions[0].Params[0].Default = shared
	ref := file.Functions[0].ReturnType
	ref.TypeArgs = []*ast.TypeRef{ref}
	group := file.APIs[1].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	group.Group.Input = group
	condition := group.Group.Having[0]
	condition.Left = condition
	clones, mapping := cloneFixture(t, []*ast.File{file, file})
	clone := clones[0]
	if clone != clones[1] || clone.Functions[0].Params[0].Default != clone.Models[0].Fields[1].Default || clone.Functions[0].Params[0].Default == shared {
		t.Fatal("shared defaults or file identity were duplicated")
	}
	copyRef := mapping.TypeRefs[ref]
	copyGroup := mapping.Exprs[group].(*ast.CallExpr)
	if copyRef.TypeArgs[0] != copyRef || copyGroup.Group.Input != copyGroup || mapping.GroupConditions[condition].Left != mapping.GroupConditions[condition] {
		t.Fatal("cycles were not reconstructed")
	}
	other, otherMapping := cloneFixture(t, []*ast.File{file})
	if other[0] == clone || otherMapping.Exprs[group] == copyGroup || otherMapping.TypeRefs[ref] == copyRef {
		t.Fatal("independent clone calls share mutable context")
	}
}

func TestCloneFilesForExpressionStatementIdentity(t *testing.T) {
	file := parseCloneFixture(t, `fn run(): [Int] { val items = for item in [1, 2] { yield item } return items }`)
	loop := file.Functions[0].Body.Stmts[0].(*ast.ValStmt).Value.(*ast.ForStmt)
	file.Functions[0].Body.Stmts = append(file.Functions[0].Body.Stmts, loop)
	_, mapping := cloneFixture(t, []*ast.File{file})
	if mapping.Exprs[loop].(*ast.ForStmt) != mapping.Statements[loop].(*ast.ForStmt) || mapping.Exprs[loop] == loop {
		t.Fatal("for expression and statement clones have different identities")
	}
}

func TestCloneFilesNilAndEmpty(t *testing.T) {
	if files, _ := cloneFixture(t, nil); files != nil {
		t.Fatal("nil files became an empty slice")
	}
	if files, _ := cloneFixture(t, []*ast.File{}); files == nil || len(files) != 0 {
		t.Fatal("non-nil empty files were lost")
	}
	var expression *ast.Ident
	var statement *ast.ValStmt
	file := &ast.File{APIs: []*ast.ApiDecl{nil, {Params: []*ast.ParamDecl{}, Body: &ast.Block{
		Stmts: []ast.Stmt{nil, statement, &ast.ExprStmt{Expr: expression}},
	}}}}
	files, mapping := cloneFixture(t, []*ast.File{nil, file})
	clone := files[1]
	if files[0] != nil || clone.Models != nil || clone.APIs[0] != nil || clone.APIs[1].Params == nil {
		t.Fatal("nil and empty collections changed shape")
	}
	stmts := clone.APIs[1].Body.Stmts
	if stmts[0] != nil || stmts[1] != (*ast.ValStmt)(nil) || stmts[2].(*ast.ExprStmt).Expr != (*ast.Ident)(nil) || len(mapping.Exprs) != 0 {
		t.Fatal("typed nil interfaces changed their dynamic shape")
	}
}

func TestCloneFilesRealParserCorpora(t *testing.T) {
	data, err := os.ReadFile("../../editors/vscode/examples/demo.luxo")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{testutil.CompilerDemoV1(), string(data)} {
		file := parseCloneFixture(t, source)
		clones, mapping := cloneFixture(t, []*ast.File{file})
		if len(mapping.Exprs) < 50 || clones[0].Name != file.Name || clones[0] == file {
			t.Fatal("real source graph was not cloned")
		}
		for _, function := range file.Functions {
			ast.Walk(function.Body, ast.Visitor{
				Expr: func(expression ast.Expr) {
					if mapping.Exprs[expression] == nil || mapping.Exprs[expression] == expression {
						t.Fatalf("source expression missing: %T", expression)
					}
				},
				Stmt: func(statement ast.Stmt) {
					if mapping.Statements[statement] == nil || mapping.Statements[statement] == statement {
						t.Fatalf("source statement missing: %T", statement)
					}
				},
			})
		}
	}
}

type foreignCloneExpr struct{ ast.Expr }
type foreignCloneStmt struct{ ast.Stmt }

func TestCloneFilesRejectsForeignNodes(t *testing.T) {
	for _, statement := range []ast.Stmt{&ast.ExprStmt{Expr: &foreignCloneExpr{}}, &foreignCloneStmt{}} {
		file := &ast.File{APIs: []*ast.ApiDecl{{Body: &ast.Block{Stmts: []ast.Stmt{statement}}}}}
		clones, mapping, err := ast.CloneFiles([]*ast.File{file})
		if err == nil || clones != nil || mapping != nil {
			t.Fatal("unknown node exposed a partial clone", err)
		}
	}
}

func TestCloneFilesParserBangElvisAndLegacyTransaction(t *testing.T) {
	file := parseCloneFixture(t, `fn recoverValue(): Int { return true !: 7 }`)
	block := file.Functions[0].Body
	// TransactionExpr is a retained fragment node; current source parses transaction as a call.
	legacy := &ast.TransactionExpr{Pos: block.Pos, Body: block}
	file.Globals = []*ast.ValStmt{{Name: "legacy", Value: legacy}}
	clones, mapping := cloneFixture(t, []*ast.File{file})
	if mapping.Exprs[legacy].(*ast.TransactionExpr).Body != clones[0].Functions[0].Body {
		t.Fatal("legacy fragment lost the shared parsed body")
	}
	bang := block.Stmts[0].(*ast.ReturnStmt).Value.(*ast.BangElvisExpr)
	if mapping.Exprs[bang].(*ast.BangElvisExpr).Left != mapping.Exprs[bang.Left] {
		t.Fatal("parsed bang-Elvis child was not rebound")
	}
}

func TestCloneFilesKeepsFirstUnsupportedNodeError(t *testing.T) {
	body := &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: &foreignCloneExpr{}}, &foreignCloneStmt{}}}
	files, mapping, err := ast.CloneFiles([]*ast.File{{Functions: []*ast.FnDecl{{Body: body}}}})
	if files != nil || mapping != nil || err == nil || err.Error() != "cannot clone unsupported AST expr implementation" {
		t.Fatal("first error was replaced", err)
	}
}

func TestCloneFilesSharedSourceOwners(t *testing.T) {
	files := []*ast.File{parseCloneFixture(t, testutil.CompilerDemoV1()), analyzedCloneFixture(t).Files[0]}
	parsed := parseCloneFixture(t, `fn recoverValue(): Int { return true !: 7 }`)
	parsed.Globals = []*ast.ValStmt{{Value: &ast.TransactionExpr{Body: parsed.Functions[0].Body}}}
	files = append(files, parsed)
	_, sourceNodes := cloneFixture(t, files)
	extra := sharedCloneOwners(sourceNodes)
	files = append(files, extra)
	clones, mapping := cloneFixture(t, files)
	extraClone := clones[len(clones)-1]
	for i, statement := range extra.APIs[0].Body.Stmts {
		if original, ok := statement.(*ast.ExprStmt); ok {
			if copied, ok := extraClone.APIs[0].Body.Stmts[i].(*ast.ExprStmt); ok && sourceNodes.Exprs[original.Expr] != nil && copied.Expr != mapping.Exprs[original.Expr] {
				t.Fatalf("shared expression owner disagreed: %T", original.Expr)
			}
		}
	}
}

func sharedCloneOwners(nodes *ast.CloneMap) *ast.File {
	body := &ast.Block{}
	extra := &ast.File{APIs: []*ast.ApiDecl{{Body: body}}}
	for node := range nodes.Exprs {
		body.Stmts = append(body.Stmts, &ast.ExprStmt{Expr: node})
	}
	for node := range nodes.Statements {
		body.Stmts = append(body.Stmts, node)
	}
	sharedCloneDeclarations(nodes, extra)
	sharedCloneAnnotations(nodes, extra, body)
	return extra
}

func sharedCloneDeclarations(nodes *ast.CloneMap, extra *ast.File) {
	for node := range nodes.Imports {
		extra.Imports = append(extra.Imports, node)
	}
	for node := range nodes.Models {
		extra.Models = append(extra.Models, node)
	}
	for node := range nodes.Interfaces {
		extra.Interfaces = append(extra.Interfaces, node)
	}
	for node := range nodes.Enums {
		extra.Enums = append(extra.Enums, node)
	}
	for node := range nodes.Sealeds {
		extra.Sealeds = append(extra.Sealeds, node)
	}
	for node := range nodes.Variants {
		extra.Sealeds = append(extra.Sealeds, &ast.SealedDecl{Variants: []*ast.SealedVariant{node}})
	}
	for node := range nodes.Types {
		extra.Types = append(extra.Types, node)
	}
	for node := range nodes.APIs {
		extra.APIs = append(extra.APIs, node)
	}
	for node := range nodes.Functions {
		extra.Functions = append(extra.Functions, node)
	}
	for node := range nodes.Errors {
		extra.Errors = append(extra.Errors, node)
	}
	for node := range nodes.Extends {
		extra.Extends = append(extra.Extends, node)
	}
	for node := range nodes.Uses {
		extra.Uses = append(extra.Uses, node)
	}
	for node := range nodes.Middlewares {
		extra.Middlewares = append(extra.Middlewares, node)
	}
	for node := range nodes.Events {
		extra.Events = append(extra.Events, node)
	}
}

func sharedCloneAnnotations(nodes *ast.CloneMap, extra *ast.File, body *ast.Block) {
	for node := range nodes.Scopes {
		extra.Models = append(extra.Models, &ast.ModelDecl{Scopes: []*ast.ScopeDecl{node}})
	}
	for node := range nodes.Fields {
		extra.Types = append(extra.Types, &ast.TypeDecl{Fields: []*ast.FieldDecl{node}})
	}
	for node := range nodes.ComputedFields {
		extra.Types = append(extra.Types, &ast.TypeDecl{Fields: []*ast.FieldDecl{{Computed: node}}})
	}
	for node := range nodes.Params {
		extra.Functions = append(extra.Functions, &ast.FnDecl{Params: []*ast.ParamDecl{node}})
	}
	for node := range nodes.TypeRefs {
		extra.Types = append(extra.Types, &ast.TypeDecl{Fields: []*ast.FieldDecl{{Type: node}}})
	}
	for node := range nodes.Directives {
		extra.APIs = append(extra.APIs, &ast.ApiDecl{Directives: []*ast.Directive{node}})
	}
	for node := range nodes.Groups {
		body.Stmts = append(body.Stmts, &ast.ExprStmt{Expr: &ast.CallExpr{Group: node}})
	}
	for node := range nodes.GroupConditions {
		body.Stmts = append(body.Stmts, &ast.ExprStmt{Expr: &ast.CallExpr{Group: &ast.GroupProjection{Having: []*ast.GroupCondition{node}}}})
	}
	for node := range nodes.Arguments {
		body.Stmts = append(body.Stmts, &ast.EmitStmt{Args: []*ast.NamedArg{node}})
	}
	for node := range nodes.WhenBranches {
		body.Stmts = append(body.Stmts, &ast.ExprStmt{Expr: &ast.WhenExpr{Branches: []*ast.WhenBranch{node}}})
	}
	for node := range nodes.Blocks {
		extra.Functions = append(extra.Functions, &ast.FnDecl{Body: node})
	}
}

func TestCloneFilesNilNodeKinds(t *testing.T) {
	expressionKinds := []ast.Expr{(*ast.Literal)(nil), (*ast.Ident)(nil), (*ast.MemberExpr)(nil), (*ast.CallExpr)(nil), (*ast.BinaryExpr)(nil), (*ast.UnaryExpr)(nil), (*ast.ElvisExpr)(nil), (*ast.BangElvisExpr)(nil), (*ast.WhenExpr)(nil), (*ast.LambdaExpr)(nil), (*ast.ListExpr)(nil), (*ast.ObjectExpr)(nil), (*ast.TemplateString)(nil), (*ast.RangeExpr)(nil), (*ast.TransactionExpr)(nil), (*ast.YieldExpr)(nil), (*ast.AsyncExpr)(nil), (*ast.AwaitExpr)(nil), (*ast.ForStmt)(nil)}
	statementKinds := []ast.Stmt{(*ast.OnDecl)(nil), (*ast.ValStmt)(nil), (*ast.IfStmt)(nil), (*ast.ForStmt)(nil), (*ast.ReturnStmt)(nil), (*ast.ThrowStmt)(nil), (*ast.EmitStmt)(nil), (*ast.AssignStmt)(nil), (*ast.BreakStmt)(nil), (*ast.ContinueStmt)(nil), (*ast.ExprStmt)(nil)}
	body := &ast.Block{Stmts: append([]ast.Stmt{}, statementKinds...)}
	for _, node := range expressionKinds {
		body.Stmts = append(body.Stmts, &ast.ExprStmt{Expr: node})
	}
	_, mapping := cloneFixture(t, []*ast.File{{APIs: []*ast.ApiDecl{{Body: body}}}})
	for node := range mapping.Exprs {
		if node == nil {
			t.Fatal("nil expression entered identity catalog")
		}
	}
	for i, node := range statementKinds {
		if mapping.Blocks[body].Stmts[i] != node {
			t.Fatal("typed nil statement changed shape")
		}
	}
	file := &ast.File{
		Imports: []*ast.ImportDecl{nil}, Models: []*ast.ModelDecl{nil, {Fields: []*ast.FieldDecl{nil}, Scopes: []*ast.ScopeDecl{nil}, Directives: []*ast.Directive{nil}}},
		Interfaces: []*ast.InterfaceDecl{nil}, Enums: []*ast.EnumDecl{nil}, Sealeds: []*ast.SealedDecl{nil, {Variants: []*ast.SealedVariant{nil}}},
		Types: []*ast.TypeDecl{nil}, Functions: []*ast.FnDecl{nil, {Params: []*ast.ParamDecl{nil}}}, Errors: []*ast.ErrorDecl{nil},
		Extends: []*ast.ExtendDecl{nil}, Uses: []*ast.UseDecl{nil}, Middlewares: []*ast.MiddlewareDecl{nil}, Events: []*ast.EventDecl{nil},
		APIs: []*ast.ApiDecl{{Directives: []*ast.Directive{{Args: []*ast.NamedArg{nil}}}, Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: &ast.WhenExpr{Branches: []*ast.WhenBranch{nil}}}}}}},
	}
	cloneFixture(t, []*ast.File{file})
}

func assertCloneExpressionMetadata(t *testing.T, mapping *ast.CloneMap) {
	t.Helper()
	for expression, copied := range mapping.Exprs {
		if copied == expression || copied.GetPos() != expression.GetPos() || copied.GetTypeTag() != expression.GetTypeTag() || copied.IsNullable() != expression.IsNullable() || copied.IsListType() != expression.IsListType() || copied.IsModelType() != expression.IsModelType() || copied.IsEnumType() != expression.IsEnumType() {
			t.Fatalf("expression metadata changed: %T", expression)
		}
	}
}

func cloneIdentityFixtures(t *testing.T) []*ast.File {
	parsed := parseCloneFixture(t, `fn recoverValue(): Int { return true !: 7 }`)
	parsed.Globals = []*ast.ValStmt{{Value: &ast.TransactionExpr{Body: parsed.Functions[0].Body}}}
	return []*ast.File{parseCloneFixture(t, testutil.CompilerDemoV1()), analyzedCloneFixture(t).Files[0], parsed}
}

func TestCloneFilesEveryNodeCanOwnTheMemo(t *testing.T) {
	_, sourceNodes := cloneFixture(t, cloneIdentityFixtures(t))
	for expression := range sourceNodes.Exprs {
		file := &ast.File{Types: []*ast.TypeDecl{{Fields: []*ast.FieldDecl{{Default: expression}}}}}
		_, mapping := cloneFixture(t, []*ast.File{file})
		if mapping.Exprs[expression] == expression {
			t.Fatalf("first expression aliases source: %T", expression)
		}
		assertCloneExpressionMetadata(t, mapping)
	}
	for statement := range sourceNodes.Statements {
		file := &ast.File{APIs: []*ast.ApiDecl{{Body: &ast.Block{Stmts: []ast.Stmt{statement}}}}}
		_, mapping := cloneFixture(t, []*ast.File{file})
		if mapping.Statements[statement] == statement {
			t.Fatalf("first statement aliases source: %T", statement)
		}
	}
}

func TestCloneFilesConcurrentContexts(t *testing.T) {
	file := analyzedCloneFixture(t).Files[0]
	results := make(chan *ast.File, 8)
	for range 8 {
		go func() {
			files, mapping, err := ast.CloneFiles([]*ast.File{file})
			if err != nil {
				t.Error(err)
				results <- nil
				return
			}
			mapping.TypeRefs[file.Functions[0].ReturnType].Name = "String"
			results <- files[0]
		}()
	}
	seen := make(map[*ast.File]bool)
	for range 8 {
		clone := <-results
		if clone == nil || clone == file || seen[clone] {
			t.Fatal("concurrent clone shared ownership")
		}
		seen[clone] = true
	}
	if file.Functions[0].ReturnType.Name != "Int" {
		t.Fatal("concurrent compilation changed the source tree")
	}
}

func BenchmarkCloneFiles(b *testing.B) {
	tokens, lexErrors := lexer.New(testutil.CompilerDemoV1(), "benchmark.luxo").Tokenize()
	if len(lexErrors) != 0 {
		b.Fatal(lexErrors)
	}
	file, parseErrors := parser.New(tokens).Parse("benchmark.luxo")
	if len(parseErrors) != 0 {
		b.Fatal(parseErrors)
	}
	files := []*ast.File{file}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, _, err := ast.CloneFiles(files); err != nil {
			b.Fatal(err)
		}
	}
}

func TestCloneFilesPositiveSemanticMarkers(t *testing.T) {
	file := parseCloneFixture(t, `
model User @noTime { id: Int @id score: Int nickname: String? }
enum Role { USER ADMIN }
fn optional(): Result<Int?> @native
api markers(user: User?, role: Role): Int {
 val outcome = optional()?
 val safe = user?.nickname
 val known = role == Role.ADMIN
 if user != null { user.score += 1 }
 return outcome ?: 0
}`)
	result := semantic.Analyze([]*ast.File{file})
	if len(result.Errors) != 0 {
		t.Fatal(result.Errors)
	}
	_, mapping := cloneFixture(t, result.Files)
	assertPositiveCloneMarkers(t, mapping)
	for arg, clone := range mapping.Arguments {
		if arg.ExpectedType != nil && clone.ExpectedType != mapping.TypeRefs[arg.ExpectedType] {
			t.Fatal("argument destination type was not rebound")
		}
	}
}

func assertPositiveCloneMarkers(t *testing.T, mapping *ast.CloneMap) {
	t.Helper()
	var narrowed, resultNullable, safeNullable, enumConstant, declared bool
	for expression, clone := range mapping.Exprs {
		switch node := expression.(type) {
		case *ast.Ident:
			if node.TypeTagged != clone.(*ast.Ident).TypeTagged {
				t.Fatal("narrowed identifier metadata changed")
			}
			narrowed = narrowed || node.NarrowedTag
		case *ast.UnaryExpr:
			if node.TypeTagged != clone.(*ast.UnaryExpr).TypeTagged {
				t.Fatal("nullable Result payload metadata changed")
			}
			resultNullable = resultNullable || node.ResultValueNullable
		case *ast.MemberExpr:
			copied := clone.(*ast.MemberExpr)
			if node.TypeTagged != copied.TypeTagged || node.SafeCall != copied.SafeCall || node.ValueNullable != copied.ValueNullable || node.EnumConstant != copied.EnumConstant || node.DeclaredField != copied.DeclaredField {
				t.Fatal("resolved member metadata changed")
			}
			safeNullable = safeNullable || node.SafeCall && node.ValueNullable
			enumConstant = enumConstant || node.EnumConstant
			declared = declared || node.DeclaredField
		}
	}
	atomic := assertCloneAtomicMarkers(t, mapping)
	if !narrowed || !resultNullable || !safeNullable || !enumConstant || !declared || !atomic {
		t.Fatal("the real semantic fixture must set every special marker", narrowed, resultNullable, safeNullable, enumConstant, declared, atomic)
	}
}

func assertCloneAtomicMarkers(t *testing.T, mapping *ast.CloneMap) bool {
	t.Helper()
	var atomic bool
	for statement, clone := range mapping.Statements {
		if assign, ok := statement.(*ast.AssignStmt); ok {
			if assign.Atomic != clone.(*ast.AssignStmt).Atomic {
				t.Fatal("atomic model mutation metadata changed")
			}
			atomic = atomic || assign.Atomic
		}
	}
	return atomic
}

func TestCloneFilesConstructorArguments(t *testing.T) {
	file := parseCloneFixture(t, `type box<T> { value: T } fn make(): box<Int> { return box<Int>(value: 1) }`)
	result := semantic.Analyze([]*ast.File{file})
	if len(result.Errors) != 0 {
		t.Fatal(result.Errors)
	}
	call := file.Functions[0].Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	clones, mapping := cloneFixture(t, []*ast.File{file})
	copy := mapping.Exprs[call].(*ast.CallExpr)
	if call.ConstructorType == nil || copy.ConstructorType == call.ConstructorType || copy.ConstructorType.TypeArgs[0] != mapping.TypeRefs[call.ConstructorType.TypeArgs[0]] {
		t.Fatal("checked constructor marker was not deeply mapped")
	}
	copy.ConstructorType.Name = "changed"
	if call.ConstructorType.Name != "box" {
		t.Fatal("constructor marker changed source semantics")
	}
	if copy.TypeArgs[0] == call.TypeArgs[0] || copy.TypeArgs[0] != mapping.TypeRefs[call.TypeArgs[0]] {
		t.Fatal("constructor type arguments alias source")
	}
	copy.TypeArgs[0].Name = "String"
	copy.TypeArgs[0] = clones[0].Functions[0].ReturnType
	if call.TypeArgs[0].Name != "Int" {
		t.Fatal("constructor type argument storage is shared")
	}
}
