package codegen

import (
	"strconv"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
)

var queryLoopDemandSink map[ast.Expr]loadProjection
var queryLoopProjectionSink map[*ast.NamedArg]loadProjection

var queryValueBenchmarks = []struct{ name, expression string }{
	{"for-query", `for id in [2] { Node.find(id: id) }`},
	{"literal-query", `[Node.find(id: 2)]`},
	{"mixed-literal", `[Node.find(id: 2), Node { id: 3, parentId: null, parent: null }]`},
	{"load-literal", `[Node.load(2) ?: throw error.NotFound]`},
}

func BenchmarkQueryValueDemand(b *testing.B) {
	for _, load := range []bool{false, true} {
		for _, sample := range queryValueBenchmarks {
			if load && sample.name != "load-literal" {
				continue
			}
			kind := "query/"
			if load {
				kind = "load/"
			}
			b.Run(kind+sample.name, func(b *testing.B) {
				input, _ := queryLoopBenchmarkInput(b, sample.expression, "direct")
				declaration := input.Files[0].APIs[0]
				models := map[string]*ast.ModelDecl{"Node": input.Files[0].Models[0]}
				b.ReportAllocs()
				for b.Loop() {
					if load {
						queryLoopProjectionSink = analyzeLoadProjections(declaration.Body, models, declaration.ReturnType)
					} else {
						queryLoopDemandSink = analyzeQueryRelations(declaration.Body, declaration.ReturnType, models, nil)
					}
				}
			})
		}
	}
}

func BenchmarkQueryValueGeneration(b *testing.B) {
	for _, sample := range queryValueBenchmarks {
		b.Run(sample.name, func(b *testing.B) {
			input, ids := queryLoopBenchmarkInput(b, sample.expression, "direct")
			generator := mustNewGenerator(b, GeneratorConfig{IDs: ids})
			b.ReportAllocs()
			for b.Loop() {
				if _, err := generator.Generate(input, "generated", nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkQueryRelationLoopAnalysis(b *testing.B) {
	for _, load := range []bool{false, true} {
		kind, query := "query", "Node.where(id: 2).all()"
		if load {
			kind, query = "load", "Node.load(id: 2)"
		}
		for _, shape := range []string{"direct", "collected", "yielded"} {
			b.Run(kind+"/"+shape, func(b *testing.B) {
				input, _ := queryLoopBenchmarkInput(b, query, shape)
				declaration := input.Files[0].APIs[0]
				models := map[string]*ast.ModelDecl{"Node": input.Files[0].Models[0]}
				b.ReportAllocs()
				for b.Loop() {
					if load {
						queryLoopProjectionSink = analyzeLoadProjections(declaration.Body, models, declaration.ReturnType)
					} else {
						queryLoopDemandSink = analyzeQueryRelations(declaration.Body, declaration.ReturnType, models, nil)
					}
				}
			})
		}
	}
}

func BenchmarkQueryRelationLoopGeneration(b *testing.B) {
	for _, shape := range []string{"direct", "collected", "yielded"} {
		b.Run(shape, func(b *testing.B) {
			input, ids := queryLoopBenchmarkInput(b, "Node.where(id: 2).all()", shape)
			generator := mustNewGenerator(b, GeneratorConfig{IDs: ids})
			b.ReportAllocs()
			for b.Loop() {
				if _, err := generator.Generate(input, "generated", nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func queryLoopBenchmarkInput(tb testing.TB, query, shape string) (*semantic.Result, StableIDs) {
	tb.Helper()
	result, body := "[Node]", "return "+query
	if shape == "collected" {
		body = "val rows = " + query + " return for row in rows { row }"
	} else if shape == "yielded" {
		result, body = "Node?", "val rows = "+query+" return for row in rows { if row.id == 2 { yield row } }"
	}
	source := "model Node @noTime { id: Int @id parentId: Int? parent: Node? @by(id, parentId) }\napi lookup(): " + result + " { " + body + " }"
	tokens, lexErrors := lexer.New(source, "loop.luxo").Tokenize()
	if len(lexErrors) != 0 {
		tb.Fatal(lexErrors)
	}
	file, parseErrors := parser.New(tokens).Parse("loop.luxo")
	if len(parseErrors) != 0 {
		tb.Fatal(parseErrors)
	}
	input := semantic.Analyze([]*ast.File{file})
	if len(input.Errors) != 0 {
		tb.Fatal(input.Errors)
	}
	return input, completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Node": {"id": 1, "parentId": 2, "parent": 3}}})
}

func BenchmarkLoadDemandGeneration(b *testing.B) {
	for _, private := range []bool{false, true} {
		for _, count := range []int{1, 128} {
			b.Run(strconv.FormatBool(private)+"/"+strconv.Itoa(count), func(b *testing.B) {
				input, ids := loadDemandBenchmarkInput(count, private)
				if len(input.Errors) != 0 {
					b.Fatal(input.Errors)
				}
				generator := mustNewGenerator(b, GeneratorConfig{IDs: ids})
				b.ReportAllocs()
				for b.Loop() {
					if _, err := generator.Generate(input, "generated", nil); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func loadDemandBenchmarkInput(count int, private bool) (*semantic.Result, StableIDs) {
	field := &ast.FieldDecl{Name: "name", Type: &ast.TypeRef{Name: "String"}}
	if private {
		field.Directives = []*ast.Directive{{Name: "hidden"}}
	}
	file := &ast.File{Name: "origin/product/model.luxo", Models: []*ast.ModelDecl{{Name: "Product", Directives: []*ast.Directive{{Name: "noTime"}}, Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}}}, field,
	}}}}
	for index := range count {
		name := "lookup" + strconv.Itoa(index)
		file.APIs = append(file.APIs, &ast.ApiDecl{Name: name, Params: []*ast.ParamDecl{{Name: "id", Type: &ast.TypeRef{Name: "Int"}}}, ReturnType: &ast.TypeRef{Name: "String"}, Body: &ast.Block{Stmts: []ast.Stmt{
			&ast.ValStmt{Name: "product", Value: &ast.ElvisExpr{
				Left:  &ast.CallExpr{Func: &ast.MemberExpr{Object: &ast.Ident{Name: "Product"}, Field: "load"}, Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "id"}}}},
				Right: &ast.UnaryExpr{Op: "throw", Value: &ast.MemberExpr{Object: &ast.Ident{Name: "error"}, Field: "NotFound"}},
			}},
			&ast.ValStmt{Name: "alias", Value: &ast.Ident{Name: "product"}},
			&ast.ReturnStmt{Value: &ast.MemberExpr{Object: &ast.Ident{Name: "alias"}, Field: "name"}},
		}}})
	}
	input := semantic.Analyze([]*ast.File{file})
	ids := completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Product": {"id": 1, "name": 2}}})
	return input, ids
}

const loadDemandRuntimeBenchmark = `package generated
import("bytes";"context";"encoding/json";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/dataloader";"github.com/light-speak/luxo/pkg/lux/selection")
var loadDemandHandlerSink api.HandlerFunc
func BenchmarkGeneratedLoadHandler(b *testing.B){
 app:=&App{};b.ReportAllocs()
 for b.Loop(){loadDemandHandlerSink=handleLookup(app)}
}
func BenchmarkGeneratedLoadCall(b *testing.B){
 app:=&App{};cfg:=dataloader.DefaultConfig();cfg.MaxBatch=1
 rows:=map[int64]*Product{42:{Id:42,Name:"wanted",Price:99}}
 app.SetLoaders(NewLoaders(cfg,func(context.Context,[]int64,[]*selection.Field)(map[int64]*Product,error){return rows,nil}))
 req:=&api.Request{Params:map[string]json.RawMessage{"id":json.RawMessage("42")},Buf:&api.ResponseBuf{B:make([]byte,0,128)}}
 if RETURN_MODEL {req.Select=[]*selection.Field{{Name:"name"}};mask,err:=codec.FieldMaskSet(nil,2);if err!=nil{b.Fatal(err)};mask,err=codec.AppendSelectionMask(nil,mask,nil);if err!=nil{b.Fatal(err)};req.FieldMask=mask}
 handler:=handleLookup(app);ctx:=b.Context();b.ReportAllocs()
 for b.Loop(){req.Buf.B=req.Buf.B[:0];if err:=handler(ctx,req);err!=nil{b.Fatal(err)}}
 if !bytes.Contains(req.Buf.B,[]byte("wanted")){b.Fatal("wrong output")}
}
`

func loadRuntimeBenchmarkSource(model bool) string {
	return strings.ReplaceAll(loadDemandRuntimeBenchmark, "RETURN_MODEL", strconv.FormatBool(model))
}
