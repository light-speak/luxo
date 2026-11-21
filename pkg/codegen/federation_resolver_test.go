package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
	"github.com/light-speak/luxo/pkg/token"
)

func TestGeneratedResolverRegistrationUsesStableParameters(t *testing.T) {
	g := mustNewGenerator(t, GeneratorConfig{IDs: StableIDs{
		APIs:      map[string]int{"svc:resolve:Post:userId": 42},
		APIParams: map[string]map[string]int{"svc:resolve:Post:userId": {"keys": 7, "shape": 11}},
	}})
	var source strings.Builder
	source.WriteString(`package generated
import("context";"github.com/light-speak/luxo/pkg/lux/api")
type App struct{}
func handleResolvePostByUserId(*App)api.HandlerFunc{return func(context.Context,*api.Request)error{return nil}}
`)
	g.registerResolveEndpoints(&source, []resolveEndpoint{{modelName: "Post", fkField: "userId", svcName: "svc:resolve:Post:userId", keyType: "Int"}})
	runGeneratedProtocolPackage(t, source.String(), `package generated
import("testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/schema")
func TestResolverRegistration(t *testing.T){
 router:=api.NewRouter()
 if err:=router.Schema.RegisterModel(&schema.Model{Name:"Post",Fields:[]schema.Field{{ID:1,Name:"id",Type:schema.FieldInt,PrimaryKey:true}}});err!=nil{t.Fatal(err)}
 if err:=router.Schema.RegisterAPI(&schema.API{ID:42,Name:"svc:resolve:Post:userId",Module:"post",Internal:true,ReturnType:"Post",ReturnList:true,Params:[]schema.Param{{ID:7,Name:"keys",Type:schema.FieldInt,IsList:true},{ID:11,Name:"shape",Type:schema.FieldInt}}});err!=nil{t.Fatal(err)}
 if err:=router.Registry.RegisterSchemaAPIs(router.Schema);err!=nil{t.Fatal(err)}
 if err:=RegisterFederationResolvers(router,&App{});err!=nil{t.Fatal("resolver registration contradicts stable schema",err)}
 params:=router.Registry.ParamOrder("svc:resolve:Post:userId")
 if len(params)!=2||params[0].FieldID!=7||params[1].FieldID!=11{t.Fatal("stable resolver parameters changed",params)}
}
`)
}

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
	apis := g.collectSchemaAPIs(result, nil)
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
import (
"github.com/light-speak/luxo/internal/testutil"
"testing"; "github.com/light-speak/luxo/pkg/lux/api"; "github.com/light-speak/luxo/pkg/lux/codec"; "github.com/light-speak/luxo/pkg/lux/luvia")
func TestNullableForeignKey(t *testing.T) {
	key:=int64(9)
	req:=&api.Request{Buf:&api.ResponseBuf{},FieldMask:testutil.Bytes(t)(codec.AppendSelectionMask(nil,testutil.Bytes(t)(codec.FieldMaskSet(nil,1)),nil))}
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
"github.com/light-speak/luxo/internal/testutil"

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
            req := &api.Request{Buf: &api.ResponseBuf{B: []byte{0x77}}, FieldMask: testutil.Bytes(t)(codec.AppendSelectionMask(nil, testutil.Bytes(t)(codec.FieldMaskSet(nil, 1)), nil))}
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

func TestGeneratedResolverPreservesVisibilityIdentity(t *testing.T) {
	visible := &ast.Directive{Name: "visible", Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: &ast.BinaryExpr{
		Left: &ast.MemberExpr{Object: &ast.Ident{Name: "my"}, Field: "role"}, Op: "!=", Right: &ast.Literal{Kind: token.String, Value: "blocked"},
	}}}}}
	post := &ast.ModelDecl{Name: "Post", Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "secret", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{visible}},
	}}
	g := generatorWithModelFieldIDs(map[string]map[string]int{"Post": {"id": 1, "userId": 2, "secret": 3}})
	var source strings.Builder
	source.WriteString("package generated\nimport (\"fmt\"; \"github.com/light-speak/luxo/pkg/lux/api\"; \"github.com/light-speak/luxo/pkg/lux/codec\")\n")
	generateModel(&source, post, nil)
	g.generateWriteLuxo(&source, post, nil)
	g.generateWriteColumnar(&source, post, nil)
	g.generateReadLuxo(&source, post, nil)
	g.generateReadColumnar(&source, post, nil)
	generateResolveGroupWriter(&source, resolveEndpoint{modelName: "Post", fkField: "userId", keyGoType: "int64"})
	runGeneratedProtocolPackage(t, source.String(), resolverVisibilityAssertions)
}

const resolverVisibilityAssertions = `package generated
import("testing"; "context"; "net"; "fmt"; "github.com/light-speak/luxo/pkg/lux/auth"; "github.com/light-speak/luxo/pkg/lux/rpc"; "github.com/light-speak/luxo/internal/testutil"; "github.com/light-speak/luxo/pkg/lux/api"; "github.com/light-speak/luxo/pkg/lux/codec"; "github.com/light-speak/luxo/pkg/lux/luvia")
type identity struct{ role string }
func(i identity) ID()int64{return 1}
func(i identity) Int(string)int64{return 0}
func(i identity) String(key string)string{if key=="role" {return i.role};return ""}
func TestVisibilityForEveryGroup(t *testing.T){
 auth.ResetConfig();auth.ResetVerifyCache();t.Cleanup(auth.ResetConfig);t.Cleanup(auth.ResetVerifyCache)
 t.Setenv("JWT_SECRET","resolver-visibility-regression")
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 for _,role:=range []string{"blocked","admin"}{
  token,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){return fmt.Appendf(dst,"{\"id\":42,\"role\":%q}",role),nil});if err!=nil{t.Fatal(err)}
  claims,err:=auth.Verify(cfg,token);if err!=nil{t.Fatal(err)}
  for _,subject:=range []struct{value any;expectedRole string}{{claims,role},{identity{role},"blocked"}}{
  for shape:=int64(0);shape<4;shape++{
   mask:=testutil.Bytes(t)(codec.FieldMaskSet(nil,1))
   mask=testutil.Bytes(t)(codec.FieldMaskSet(mask,3))
   req:=&api.Request{Buf:&api.ResponseBuf{Identity:subject.value},FieldMask:testutil.Bytes(t)(codec.AppendSelectionMask(nil,mask,nil))}
   rows:=[]*Post{{Id:1,UserId:10,Secret:71},{Id:2,UserId:20,Secret:72}}
   if err:=writeResolvePostByUserIdGroups(req,[]int64{10,20},rows,shape);err!=nil{t.Fatal(err)}
   groups:=luvia.ParseGroupedResponse(req.Buf.B,shape&2!=0)
   if len(groups)!=2{t.Fatal("missing groups")}
   for i,group:=range groups{assertVisibleRPCGroup(t,group,shape,subject.expectedRole,rows[i].Secret)}
  }
  }
 }
}
func TestResolverVisibilityOverAuthenticatedRPC(t *testing.T){
 auth.ResetConfig();auth.ResetVerifyCache()
 t.Cleanup(auth.ResetConfig);t.Cleanup(auth.ResetVerifyCache)
 t.Setenv("JWT_SECRET","resolver-visibility-regression")
 cfg,err:=auth.LoadConfig();if err!=nil{t.Fatal(err)}
 gateway:=luvia.New()
 rows:=[]*Post{{Id:1,UserId:10,Secret:71},{Id:2,UserId:20,Secret:72}}
 for shape:=int64(0);shape<4;shape++{
  name:=fmt.Sprintf("resolve%d",shape)
  gateway.Router.HandleInternal(name,func(ctx context.Context,req *api.Request)error{
   return writeResolvePostByUserIdGroups(req,[]int64{10,20},rows,shape)
  })
  if err:=gateway.Router.Registry.Register(name,int(shape+1));err!=nil{t.Fatal(err)}
 }
 server:=rpc.NewServer(gateway.Router)
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 done:=make(chan error,1);go func(){done<-server.Serve(listener)}()
 defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}()
 client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 mask:=testutil.Bytes(t)(codec.FieldMaskSet(nil,1))
 mask=testutil.Bytes(t)(codec.FieldMaskSet(mask,3))
 mask=testutil.Bytes(t)(codec.AppendSelectionMask(nil,mask,nil))
 for _,role:=range []string{"admin","blocked","admin","blocked"}{
  token,err:=auth.Sign(cfg,func(dst []byte)([]byte,error){return fmt.Appendf(dst,"{\"id\":42,\"role\":%q}",role),nil});if err!=nil{t.Fatal(err)}
  for shape:=int64(0);shape<4;shape++{
   body,err:=client.CallWithMaskContext(t.Context(),token,int(shape+1),mask,nil);if err!=nil{t.Fatal(err)}
   groups:=luvia.ParseGroupedResponse(body,shape&2!=0);if len(groups)!=2{t.Fatal("missing groups")}
   for i,group:=range groups{assertVisibleRPCGroup(t,group,shape,role,rows[i].Secret)}
  }
 }
}
func assertVisibleRPCGroup(t *testing.T,group []byte,shape int64,role string,secret int64){
 t.Helper();dec:=codec.NewDecoder(group)
 if shape&2!=0&&!dec.ReadBool(){t.Fatal("unexpected null")}
 body:=dec.ReadBytes();if err:=dec.Finish();err!=nil{t.Fatal(err)}
 var post Post
 if shape&1!=0{items,err:=ReadColumnarPost(body);if err!=nil||len(items)!=1{t.Fatalf("bad list: %v",err)};post=items[0]
 }else{inner:=codec.NewDecoder(body);post.ReadLuxo(inner);if err:=inner.Finish();err!=nil{t.Fatal(err)}}
 if role=="blocked"{secret=0}
 if post.Secret!=secret{t.Fatalf("RPC role=%s shape=%d secret=%d want=%d",role,shape,post.Secret,secret)}
}

`

func TestFederationReadsPreserveSoftDeleteCondition(t *testing.T) {
	for _, soft := range []bool{false, true} {
		model := &ast.ModelDecl{Name: "Post", Fields: []*ast.FieldDecl{
			{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
			{Name: "userId", Type: &ast.TypeRef{Name: "Int"}},
		}}
		if soft {
			model.Directives = []*ast.Directive{{Name: "soft"}}
		}
		g := generatorWithModelFieldIDs(map[string]map[string]int{"Post": {"id": 1, "userId": 2}})
		for _, resolver := range []bool{false, true} {
			var source strings.Builder
			if resolver {
				g.generateResolveHandler(&source, resolveEndpoint{model: model, modelName: "Post", tableName: "posts", fkField: "userId", fkColumn: "user_id", scanFn: "scanPost", keyType: "Int", keyGoType: "int64"}, nil)
			} else {
				g.generateBatchLoadHandlers(&source, []*ast.ModelDecl{model})
			}
			text := source.String()
			predicate := `lux.NewTimeField("\"deleted_at\"").IsNull()`
			count := 0
			if soft {
				count = 1
			}
			if got := strings.Count(text, predicate); got != count {
				t.Errorf("soft=%v resolver=%v: predicate count=%d want=%d", soft, resolver, got, count)
			}
			if soft && strings.Index(text, predicate) > strings.Index(text, "lux.BuildSelectSQL") {
				t.Error("deletion condition added after SQL construction")
			}
		}
	}
}

func TestGeneratedFederationSoftDeletePG(t *testing.T) {
	model := &ast.ModelDecl{Name: "Probe", Directives: []*ast.Directive{{Name: "soft"}}, Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}},
		{Name: "ownerId", Type: &ast.TypeRef{Name: "Int"}},
	}}
	g := generatorWithModelFieldIDs(map[string]map[string]int{"Probe": {"id": 1, "ownerId": 2, "deletedAt": 3}})
	var source strings.Builder
	source.WriteString("package generated\nimport(\"context\";\"fmt\";\"github.com/light-speak/luxo/pkg/lux\";\"github.com/light-speak/luxo/pkg/lux/api\";\"github.com/light-speak/luxo/pkg/lux/codec\";\"github.com/light-speak/luxo/pkg/lux/selection\")\n")
	ep := resolveEndpoint{model: model, modelName: "Probe", tableName: "probes", fkField: "ownerId", fkColumn: "owner_id", scanFn: "scanProbe", keyType: "Int", keyGoType: "int64"}
	g.generateResolveHandler(&source, ep, nil)
	generateResolveGroupWriter(&source, ep)
	source.WriteString(`type localProbeLoader struct { load func(context.Context,[]int64,[]*selection.Field)(map[int64]*Probe,error) }
func localProbe(app *App) localProbeLoader { return localProbeLoader{
`)
	g.generateExtendByPKBatchFunc(&source, "Probe", true)
	source.WriteString("}}\n")
	generateEnsureFieldHelper(&source)

	runGeneratedFixture(t, `model Probe @crud @soft @noTime { id: Int @id ownerId: Int }`, federationSoftDeletePGAssertions,
		StableIDs{ModelFields: map[string]map[string]int{"Probe": {"id": 1, "ownerId": 2, "deletedAt": 3}}}, source.String())
}

const federationSoftDeletePGAssertions = `package generated
import("bytes";"fmt";"net/http/httptest";"strings";"testing"
"github.com/light-speak/luxo/internal/testutil"
"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/pg"
"github.com/testcontainers/testcontainers-go";"github.com/testcontainers/testcontainers-go/modules/postgres")
func TestFederationSoftDeletePG(t *testing.T){
 container,err:=postgres.Run(t.Context(),testutil.PostgresImage(),postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies())
 testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE probes(id bigint PRIMARY KEY,owner_id bigint NOT NULL,deleted_at timestamptz); INSERT INTO probes VALUES(1,7,NULL),(2,7,now()),(3,8,now())");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db)
 for _,keys:=range [][]int64{{1,2,3},{2,3},{99},nil}{
  items,err:=localProbe(app).load(t.Context(),keys,nil);if err!=nil{t.Fatal(err)}
  want:=0;if len(keys)==3{want=1}
  if len(items)!=want||(want==1&&(items[1]==nil||items[1].OwnerId!=7)){t.Fatalf("local extend returned deleted rows: %v",items)}
 }
 request:=softDeleteRequest(t,"{\"keys\":[1,2,3],\"$select\":\"id,ownerId\"}")
 if err:=handleBatchLoadProbe(app)(t.Context(),request);err!=nil{t.Fatal(err)}
 expected:=&api.ResponseBuf{B:codec.AppendVarint(nil,1)}
 if err:=(&Probe{Id:1,OwnerId:7}).WriteLuxo(expected,request.FieldMask);err!=nil{t.Fatal(err)}
 if !bytes.Equal(request.Buf.B,expected.B){t.Fatalf("batch load included deleted rows: %x want %x",request.Buf.B,expected.B)}
 for _,owner:=range []int64{7,8,99}{
  for shape:=int64(0);shape<4;shape++{
   request:=softDeleteRequest(t,fmt.Sprintf("{\"keys\":[%d],\"shape\":%d,\"$select\":\"id,ownerId\"}",owner,shape))
   gotErr:=handleResolveProbeByOwnerId(app)(t.Context(),request)
   expected:=&api.Request{Buf:&api.ResponseBuf{},FieldMask:request.FieldMask}
   var live []*Probe;if owner==7{live=[]*Probe{{Id:1,OwnerId:7}}}
   wantErr:=writeResolveProbeByOwnerIdGroups(expected,[]int64{owner},live,shape)
   if (gotErr==nil)!=(wantErr==nil){t.Fatalf("owner=%d shape=%d: got error=%v want=%v",owner,shape,gotErr,wantErr)}
   if !bytes.Equal(request.Buf.B,expected.Buf.B){t.Fatalf("resolver owner=%d shape=%d included deleted row or partial response",owner,shape)}
  }
 }
}
func softDeleteRequest(t *testing.T,body string)*api.Request{
 t.Helper();req,err:=api.ParseRequest(httptest.NewRequest("POST","/luvia",strings.NewReader("{\"$api\":\"internal\","+body[1:])));if err!=nil{t.Fatal(err)}
 fields,err:=codec.FieldMaskSet(nil,1);if err!=nil{t.Fatal(err)}
 fields,err=codec.FieldMaskSet(fields,2);if err!=nil{t.Fatal(err)}
 req.FieldMask,err=codec.AppendSelectionMask(nil,fields,nil);if err!=nil{t.Fatal(err)}
 req.Buf=&api.ResponseBuf{};return req
}
`
