package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestResolverAliasesShareOneEndpoint(t *testing.T) {
	post := &ast.ModelDecl{Name: "Post", Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
	}}
	result := &semantic.Result{Files: []*ast.File{{Name: "origin/post.luxo", Models: []*ast.ModelDecl{post}, Extends: []*ast.ExtendDecl{
		{Name: "User", Fields: []*ast.FieldDecl{{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}}}},
		{Name: "User", Fields: []*ast.FieldDecl{{Name: "primaryPost", Type: &ast.TypeRef{Name: "Post", Nullable: true}}}},
	}}}}
	g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{APIs: map[string]int{"svc:resolve:Post:userId": 17}, ModelFields: map[string]map[string]int{"User": {"posts": 2, "primaryPost": 3}}}})
	var source strings.Builder
	g.generateFederationResolvers(&source, result, []*ast.ModelDecl{post}, nil)
	if count := strings.Count(source.String(), "func handleResolvePostByUserId("); count != 1 {
		t.Fatalf("duplicate resolver definitions: %d", count)
	}
	if !strings.Contains(source.String(), `req.ParamInt("shape")`) {
		t.Fatal("missing explicit result shape")
	}
	apis := g.collectSchemaAPIs(result, nil, map[string]string{"User": "user", "Post": "post"}, nil)
	count := 0
	for _, api := range apis {
		if api.name != "svc:resolve:Post:userId" {
			continue
		}
		count++
		if len(api.params) != 2 || api.params[1].Name != "shape" {
			t.Fatal("schema omitted resolver shape parameter")
		}
	}
	if count != 1 {
		t.Fatalf("duplicate resolver schema APIs: %d", count)
	}
	registered := string(g.generateSchemaFile(result, "generated", nil))
	for _, name := range []string{"posts", "primaryPost"} {
		if !strings.Contains(registered, `Name: "`+name+`"`) {
			t.Fatalf("schema lost resolver alias %s", name)
		}
	}
	if strings.Count(registered, `ForeignKey: "userId"`) != 2 {
		t.Fatal("schema alias points at a different resolver endpoint")
	}
}

func TestGeneratedResolverCanonicalGroups(t *testing.T) {
	post := &ast.ModelDecl{Name: "Post", Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "amount", Type: &ast.TypeRef{Name: "Float"}},
	}}
	g := generatorWithModelFieldIDs(map[string]map[string]int{"Post": {"id": 1, "userId": 2, "amount": 3}})
	var source strings.Builder
	source.WriteString("package generated\nimport (\"fmt\"; \"github.com/light-speak/luxo/pkg/lux/api\"; \"github.com/light-speak/luxo/pkg/lux/codec\")\n")
	generateModel(&source, post, nil)
	g.generateWriteLuxo(&source, post, nil)
	g.generateWriteColumnar(&source, post, nil)
	g.generateReadLuxo(&source, post, nil)
	g.generateReadColumnar(&source, post, nil)
	generateResolveGroupWriter(&source, resolveEndpoint{modelName: "Post", fkField: "userId", keyGoType: "int64"})
	runGeneratedProtocolPackage(t, source.String(), resolverGroupAssertions)
}

func TestResolverEndpointCollectionScope(t *testing.T) {
	g := defaultGenerator()
	if got := g.collectResolveEndpoints(&semantic.Result{}, nil, nil); len(got) != 0 {
		t.Fatal("empty module generated endpoints")
	}
	post := &ast.ModelDecl{Name: "Post"}
	result := &semantic.Result{Files: []*ast.File{
		{Name: "post.luxo", Extends: []*ast.ExtendDecl{{Name: "Post"}}},
		{Name: "user.luxo", Extends: []*ast.ExtendDecl{{Name: "User", Fields: []*ast.FieldDecl{{Name: "posts", Type: &ast.TypeRef{Name: "Post", IsList: true}}}}}},
	}}
	if got := g.collectResolveEndpoints(result, []*ast.ModelDecl{post}, nil); len(got) != 0 {
		t.Fatal("included owned extension or another service")
	}
}

func TestGeneratedResolverNullableForeignKey(t *testing.T) {
	post := &ast.ModelDecl{Name: "Post", Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "userId", Type: &ast.TypeRef{Name: "Int", Nullable: true}},
	}}
	g := generatorWithModelFieldIDs(map[string]map[string]int{"Post": {"id": 1, "userId": 2}})
	var source strings.Builder
	source.WriteString("package generated\nimport (\"fmt\"; \"github.com/light-speak/luxo/pkg/lux/api\"; \"github.com/light-speak/luxo/pkg/lux/codec\")\n")
	generateModel(&source, post, nil)
	g.generateWriteLuxo(&source, post, nil)
	g.generateWriteColumnar(&source, post, nil)
	g.generateReadLuxo(&source, post, nil)
	g.generateReadColumnar(&source, post, nil)
	generateResolveGroupWriter(&source, resolveEndpoint{modelName: "Post", fkField: "userId", keyGoType: "int64", fkNullable: true})
	runGeneratedProtocolPackage(t, source.String(), `package generated
import ("testing"; "github.com/light-speak/luxo/pkg/lux/api"; "github.com/light-speak/luxo/pkg/lux/codec"; "github.com/light-speak/luxo/pkg/lux/luvia")
func TestNullableForeignKey(t *testing.T) {
	key:=int64(9)
	req:=&api.Request{Buf:&api.ResponseBuf{},FieldMask:codec.AppendSelectionMask(nil,codec.FieldMaskSet(nil,1),nil)}
	rows:=[]*Post{{Id:1},{Id:2,UserId:&key}}
	if err:=writeResolvePostByUserIdGroups(req,[]int64{9,0},rows,1); err!=nil { t.Fatal(err) }
	groups:=luvia.ParseGroupedResponse(req.Buf.B,false)
	if len(groups)!=2 { t.Fatal("invalid groups") }
	for i,group:=range groups {
		body,n:=codec.ReadBytes(group,0)
		if n!=len(group) { t.Fatal("invalid length") }
		items,err:=ReadColumnarPost(body)
		if err!=nil || len(items)!=1-i { t.Fatalf("null foreign key became a zero key: %v",err) }
		if i==0 && (items[0].Id!=2 || items[0].UserId!=nil) { t.Fatal("projection or row changed") }
	}
}
`)
}

const resolverGroupAssertions = `package generated
import (
    "bytes"
    "math"
    "testing"
    "github.com/light-speak/luxo/pkg/lux/api"
    "github.com/light-speak/luxo/pkg/lux/codec"
    "github.com/light-speak/luxo/pkg/lux/luvia"
)
func TestCanonicalGroups(t *testing.T) {
    for shape := int64(0); shape < 4; shape++ {
        for _, count := range []int{0,1,2} {
            rows := make([]*Post,count)
            for i := range rows { rows[i] = &Post{Id: math.MaxInt64-int64(i), UserId: math.MinInt64, Amount: -0.5} }
            req := &api.Request{Buf: &api.ResponseBuf{B: []byte{0x77}}, FieldMask: codec.AppendSelectionMask(nil, codec.FieldMaskSet(nil, 1), nil)}
            err := writeResolvePostByUserIdGroups(req, []int64{math.MinInt64,math.MinInt64}, rows, shape)
            invalid := shape&1 == 0 && (count > 1 || count == 0 && shape&2 == 0)
            if invalid {
                if err == nil || !bytes.Equal(req.Buf.B, []byte{0x77}) { t.Fatal("invalid single relation published bytes") }
                continue
            }
            if err != nil { t.Fatal(err) }
            values := luvia.ParseGroupedResponse(req.Buf.B[1:], shape&2 != 0)
            if len(values) != 2 { t.Fatal("request key order/count changed") }
            for _, value := range values {
                dec := codec.NewDecoder(value)
                if shape&2 != 0 && !dec.ReadBool() {
                    if count != 0 || shape&1 != 0 || dec.Finish()!=nil { t.Fatal("unexpected null") }
                    continue
                }
                body := dec.ReadBytes()
                if dec.Finish()!=nil { t.Fatal("bad field envelope") }
                if shape&1 != 0 {
                    result, err := ReadColumnarPost(body)
                    if err != nil || len(result)!=count { t.Fatalf("bad columnar: %v",err) }
                    for i, post := range result { if post.Id != rows[i].Id || post.UserId != 0 { t.Fatal("precision or projection changed") } }
                } else {
                    var post Post
                    inner := codec.NewDecoder(body)
                    post.ReadLuxo(inner)
                    if err := inner.Finish(); err!=nil || post.Id != math.MaxInt64 || post.UserId != 0 { t.Fatalf("bad single object: shape=%d body=%x id=%d fk=%d error=%v", shape,body,post.Id,post.UserId,err) }
                }
            }
        }
    }
}
func TestGroupWriterDiscardsEncodingErrors(t *testing.T) {
    for _, shape := range []int64{0,1,2,3} {
        req := &api.Request{Buf:&api.ResponseBuf{B:[]byte{0x77}}}
        rows := []*Post{{Id:1,UserId:1,Amount:math.Inf(1)}}
        if err := writeResolvePostByUserIdGroups(req, []int64{1}, rows, shape); err == nil || !bytes.Equal(req.Buf.B,[]byte{0x77}) { t.Fatal("non-finite Float published partial group") }
    }
}
`
