package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

func mutationInputModel() *ast.ModelDecl {
	hook := &ast.Directive{Name: "beforeSave", Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{Expr: &ast.CallExpr{
		Func: &ast.Ident{Name: "recordHook"}, Args: []*ast.NamedArg{{Value: &ast.Ident{Name: "it"}}},
	}}}}}
	return &ast.ModelDecl{Name: "Record", Directives: []*ast.Directive{{Name: "crud"}}, Fields: []*ast.FieldDecl{
		{Name: "id", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "id"}, {Name: "auto"}}},
		{Name: "name", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "notBlank"}, hook}},
		{Name: "score", Type: &ast.TypeRef{Name: "Int"}, Directives: []*ast.Directive{{Name: "range", Args: []*ast.NamedArg{
			{Value: &ast.Literal{Kind: token.Int, Value: "0"}}, {Value: &ast.Literal{Kind: token.Int, Value: "100"}},
		}}}},
		{Name: "note", Type: &ast.TypeRef{Name: "String", Nullable: true}, Directives: []*ast.Directive{hook}},
		{Name: "tags", Type: &ast.TypeRef{Name: "String", IsList: true, Nullable: true}},
		{Name: "state", Type: &ast.TypeRef{Name: "State"}, Default: &ast.MemberExpr{Object: &ast.Ident{Name: "State"}, Field: "OPEN"}},
		{Name: "frozen", Type: &ast.TypeRef{Name: "String"}, Default: &ast.Literal{Kind: token.String, Value: "fixed"}, Directives: []*ast.Directive{{Name: "immutable"}}},
	}}
}

func TestGeneratedMutationInputOrder(t *testing.T) {
	var source strings.Builder
	source.WriteString(mutationInputFixture)
	enums := map[string]bool{"State": true}
	generateEnum(&source, &ast.EnumDecl{Name: "State", Values: []string{"OPEN", "CLOSED"}})
	model := mutationInputModel()
	generateCreateHandler(&source, model, "createRecord", enums, false, nil)
	generateUpdateHandler(&source, model, "updateRecord", "int64", enums, false, nil)
	generatePreviousCreateHandler(&source, model, "previousCreateRecord", enums, false, nil)
	generatePreviousUpdateHandler(&source, model, "previousUpdateRecord", "int64", enums, false, nil)
	runGeneratedProtocolPackage(t, source.String(), mutationInputAssertions+mutationInputBenchmarks)
}

func TestGeneratedLocalCRUDRegistration(t *testing.T) {
	code := string(generateHandlerFile(result(&ast.File{Models: []*ast.ModelDecl{mutationInputModel()}}), "generated", map[string]bool{"State": true}))
	for _, name := range []string{"getRecord", "listRecords", "createRecord", "updateRecord", "deleteRecord", "deleteRecords"} {
		if !strings.Contains(code, `router.HandleLocal("`+name+`",`) {
			t.Errorf("CRUD %s is not registered as local input", name)
		}
	}
}

func TestGeneratedMutationHash(t *testing.T) {
	var source strings.Builder
	source.WriteString(`package generated
import("fmt";"github.com/light-speak/luxo/pkg/lux/api";luxocrypto "github.com/light-speak/luxo/pkg/lux/crypto")
type capture struct{value string;set bool}
func(c *capture)Set(value string){c.value=value;c.set=true}
func hashInput(req *api.Request,builder *capture)error{
`)
	field := &ast.FieldDecl{Name: "password", Type: &ast.TypeRef{Name: "String"}, Directives: []*ast.Directive{{Name: "hash"}}}
	generateParamSet(&source, field, "Set", "", nil)
	source.WriteString("return nil\n}\n")
	runGeneratedProtocolPackage(t, source.String(), `package generated
import("encoding/json";"strings";"testing";"github.com/light-speak/luxo/pkg/lux/api")
func TestHashAfterRead(t *testing.T){
    for _,value:=range []string{"short",strings.Repeat("x",73)}{
        raw:=[]byte("\""+value+"\"");out:=&capture{}
        err:=hashInput(&api.Request{Params:map[string]json.RawMessage{"password":raw}},out)
        if len(value)>72 {if err==nil||out.set{t.Fatal("hash failure reached setter")};continue}
        if err!=nil||!out.set||out.value==value||!strings.HasPrefix(out.value,"$2"){t.Fatalf("hash not applied: %v %v",out,err)}
    }
}
`)
}

// Existing scalar fixtures exercise the same read/apply helpers in isolation.
func generateParamSet(b *strings.Builder, field *ast.FieldDecl, setter, indent string, enums map[string]bool) {
	input := mutationInputField{field: field}
	generateMutationInputRead(b, input, indent, enums)
	generateMutationInputApply(b, input, setter, indent)
}

const mutationInputFixture = `package generated
import("context";"fmt";"strings";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/errors")
type App struct{Record *recordClient}
type mutationProbe struct{effects []string;values map[string]any;missing bool}
var activeProbe *mutationProbe
func recordHook(value string)string{activeProbe.effects=append(activeProbe.effects,"hook");return strings.TrimSpace(value)}
type recordClient struct{db *mutationProbe}
func(c *recordClient)Create()*recordBuilder{c.db.effects=append(c.db.effects,"create");return &recordBuilder{c.db}}
func(c *recordClient)Find(context.Context,int64)(*Record,error){c.db.effects=append(c.db.effects,"find");if c.db.missing{return nil,nil};return &Record{},nil}
func newRecordUpdateBuilder(db *mutationProbe,_ string,_ int64)*recordBuilder{db.effects=append(db.effects,"update");return &recordBuilder{db}}
type recordBuilder struct{db *mutationProbe}
func(b *recordBuilder)SetName(v string){b.db.values["name"]=v}
func(b *recordBuilder)SetScore(v int64){b.db.values["score"]=v}
func(b *recordBuilder)SetNote(v *string){b.db.values["note"]=v}
func(b *recordBuilder)SetTags(v []string){b.db.values["tags"]=v}
func(b *recordBuilder)SetState(v State){b.db.values["state"]=v}
func(b *recordBuilder)SetFrozen(v string){b.db.values["frozen"]=v}
func(b *recordBuilder)Exec(context.Context)(*Record,error){b.db.effects=append(b.db.effects,"exec");return &Record{},nil}
type Record struct{}
// The fixture isolates input ordering; response-model encoding is tested elsewhere.
func(*Record)WriteLuxo(buf *api.ResponseBuf,_ []byte)error{buf.B=codec.AppendSvarint(buf.B,1);return nil}
`

const mutationInputAssertions = `package generated
import("bytes";"context";"encoding/json";"fmt";"net/http/httptest";"os";"reflect";"testing";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/schema")
func mutationApp()(*App,*mutationProbe){p:=&mutationProbe{values:map[string]any{}};activeProbe=p;return &App{Record:&recordClient{db:p}},p}
func TestMutationReadsAllInputBeforeEffects(t *testing.T){
    for _,update:=range []bool{false,true}{
        for _,binary:=range []bool{false,true}{
            for _,sample:=range []struct{raw string;value any}{
                {"\"bad\"","bad"},{"-1",int64(-1)},{"101",int64(101)},{"null",nil},
            }{
                app,probe:=mutationApp();handler:=handleCreateRecord(app);if update{handler=handleUpdateRecord(app)}
                req:=&api.Request{Buf:&api.ResponseBuf{},Params:map[string]json.RawMessage{"id":[]byte("42"),"name":[]byte("\" valid \""),"score":[]byte(sample.raw)}}
                if binary{req.SetBinaryParams([]string{"id","name","score"},3);req.SetParamSlot(0,int64(42));req.SetParamSlot(1," valid ");req.SetParamSlot(2,sample.value)}
                err:=handler(context.Background(),req)
                if err==nil{t.Fatal("invalid mutation input accepted")}
                if len(probe.effects)!=0{t.Errorf("update=%v binary=%v score=%s ran effects before rejection: %v",update,binary,sample.raw,probe.effects)}
            }
        }
    }
}
func TestPreviousMutationReferenceStillReproducesOrderingBug(t *testing.T){
    for _,update:=range []bool{false,true}{
        app,probe:=mutationApp();handler:=handlePreviousCreateRecord(app);if update{handler=handlePreviousUpdateRecord(app)}
        req:=&api.Request{Buf:&api.ResponseBuf{},Params:map[string]json.RawMessage{"id":[]byte("42"),"name":[]byte("\"valid\""),"score":[]byte("-1")}}
        if err:=handler(context.Background(),req);err==nil||len(probe.effects)==0{t.Fatalf("reference lost original ordering: %v %v",err,probe.effects)}
    }
}
func mutationRouter(t testing.TB,update bool)(*api.Router,*mutationProbe){
    t.Helper();router,probe:=mutationPolicyRouter(t,update,"local")
    handler:=router.ExportHandlers()["mutate"]
    router.HandleLocal("mutate",func(ctx context.Context,req *api.Request)error{
        if req.Params!=nil&&len(req.BinaryParams())!=0{t.Fatal("local mutation created intermediate Binary")};return handler(ctx,req)
    })
    return router,probe
}
func mutationPolicyRouter(t testing.TB,update bool,policy string)(*api.Router,*mutationProbe){
    t.Helper();app,probe:=mutationApp();router:=api.NewRouter()
    params:=[]schema.Param{{Name:"name",ID:1,Type:schema.FieldString},{Name:"score",ID:2,Type:schema.FieldInt},
        {Name:"note",ID:3,Type:schema.FieldString,Nullable:true,HasDefault:true},{Name:"tags",ID:4,Type:schema.FieldString,IsList:true,Nullable:true,HasDefault:true},
        {Name:"state",ID:5,Type:schema.FieldString,TypeName:"State",HasDefault:true}}
    handler:=handleCreateRecord(app)
    if update{handler=handleUpdateRecord(app);for i:=range params{params[i].HasDefault=true};params=append(params,schema.Param{Name:"id",ID:6,Type:schema.FieldInt})
    }else{params=append(params,schema.Param{Name:"frozen",ID:6,Type:schema.FieldString,HasDefault:true})}
    router.Schema.RegisterEnum(&schema.Enum{Name:"State",Values:[]string{"OPEN","CLOSED"}})
    router.Schema.RegisterAPI(&schema.API{Name:"mutate",ID:1,ReturnType:"Int",Params:params});router.Registry.RegisterSchemaAPIs(router.Schema)
    if policy=="previous"{handler=handlePreviousCreateRecord(app);if update{handler=handlePreviousUpdateRecord(app)}}
    if policy=="local"{router.HandleLocal("mutate",handler)}else{router.Handle("mutate",handler)}
    if err:=router.Prepare();err!=nil{t.Fatal(err)};return router,probe
}
func TestMutationLocalJSONPresenceAndValidation(t *testing.T){
    for _,update:=range []bool{false,true}{
        for _,sample:=range []struct{extra string;status int}{
            {"",200},{",\"note\":null,\"tags\":null",200},{",\"note\":\" text \",\"tags\":[]",200},
            {",\"state\":\"CLOSED\"",200},{",\"state\":\"FUTURE\"",400},{",\"state\":null",400},
            {",\"tags\":[1]",400},{",\"note\":1",400},{",\"unknown\":1",400},
        }{
            router,probe:=mutationRouter(t,update)
            id:="";if update{id=",\"id\":42"}
            raw:=fmt.Sprintf("{\"$api\":\"mutate\",\"name\":\" valid \",\"score\":42%s%s}",id,sample.extra)
            request:=httptest.NewRequest("POST","/",bytes.NewBufferString(raw));request.Header.Set("Content-Type","application/json")
            response:=httptest.NewRecorder();router.ServeHTTP(response,request)
            if response.Code!=sample.status{t.Fatalf("%s got %d: %s",raw,response.Code,response.Body.String())}
            if sample.status!=200{if len(probe.effects)!=0{t.Errorf("invalid local input ran effects: %v",probe.effects)};continue}
            if probe.values["name"]!="valid"||probe.values["score"]!=int64(42){t.Fatalf("incorrect setters: %v",probe.values)}
            switch sample.extra{
            case "":if _,ok:=probe.values["note"];ok{t.Fatal("missing field assigned")};if _,ok:=probe.values["state"];ok{t.Fatal("missing field overrode builder default")}
            case ",\"note\":null,\"tags\":null":if !reflect.DeepEqual(probe.values["note"],(*string)(nil))||!reflect.DeepEqual(probe.values["tags"],[]string(nil)){t.Fatal("null lost")}
            case ",\"note\":\" text \",\"tags\":[]":if *probe.values["note"].(*string)!="text"||!reflect.DeepEqual(probe.values["tags"],[]string{}){t.Fatal("empty/non-null lost")}
            }
        }
    }
}
func TestMutationPartialAndMissingInput(t *testing.T){
    for _,sample:=range []struct{update,missing bool;params string;status int;effects []string}{
        {false,false,"",400,nil},
        {false,false,"\"name\":\"ok\"",400,nil},
        {true,false,"\"id\":42",200,[]string{"find","update","exec"}},
        {true,false,"\"id\":42,\"score\":0",200,[]string{"find","update","exec"}},
        {true,false,"\"id\":42,\"frozen\":\"change\"",400,nil},
        {true,true,"\"id\":42,\"name\":\"ok\"",404,[]string{"find"}},
        {true,true,"\"id\":42,\"name\":\"ok\",\"score\":-1",400,nil},
        {false,false,"\"name\":\"ok\",\"score\":0,\"frozen\":\"chosen\"",200,[]string{"create","hook","exec"}},
    }{
        router,probe:=mutationRouter(t,sample.update);probe.missing=sample.missing
        raw:="{\"$api\":\"mutate\"";if sample.params!=""{raw+=","+sample.params};raw+="}"
        request:=httptest.NewRequest("POST","/",bytes.NewBufferString(raw));request.Header.Set("Content-Type","application/json")
        response:=httptest.NewRecorder();router.ServeHTTP(response,request)
        if response.Code!=sample.status||!reflect.DeepEqual(probe.effects,sample.effects){t.Fatalf("%s got status=%d effects=%v body=%s",raw,response.Code,probe.effects,response.Body.String())}
        if sample.update {if _,ok:=probe.values["name"];ok{t.Fatal("absent name was assigned")}}
        if !sample.update&&sample.status==200&&probe.values["frozen"]!="chosen"{t.Fatal("immutable field missing on create")}
    }
}

func mutationBody(t testing.TB,router *api.Router,params map[string]any,binary bool)[]byte{
    t.Helper();var body []byte;var err error
    if binary {body,err=api.EncodeBinaryRequest(1,nil,params,router.Registry.ParamOrder("mutate"))
    }else{envelope:=map[string]any{"$api":"mutate"};for key,value:=range params{envelope[key]=value};body,err=json.Marshal(envelope)}
    if err!=nil{t.Fatal(err)};return body
}
func mutationParams(update bool,shape string)map[string]any{
    params:=map[string]any{"name":" value ","score":int64(42)}
    if update{params["id"]=int64(42)}
    switch shape{
    case "null":params["note"]=nil;params["tags"]=nil
    case "values":params["note"]=" note ";params["tags"]=[]string{"one","two"};params["state"]="CLOSED"
    }
    return params
}
func TestMutationMatchesPreviousValidInput(t *testing.T){
    for _,update:=range []bool{false,true}{
        for _,binary:=range []bool{false,true}{
            for _,shape:=range []string{"required","null","values"}{
                var want *mutationProbe
                for _,policy:=range []string{"previous","canonical","local"}{
                    router,probe:=mutationPolicyRouter(t,update,policy)
                    body:=mutationBody(t,router,mutationParams(update,shape),binary)
                    request:=httptest.NewRequest("POST","/",bytes.NewReader(body));request.Header.Set("Content-Type","application/json")
                    if binary{request.Header.Set("X-Luxo-Mode","binary")}
                    response:=httptest.NewRecorder();router.ServeHTTP(response,request)
                    if response.Code!=200{t.Fatalf("update=%v binary=%v %s/%s: %d %s",update,binary,shape,policy,response.Code,response.Body.String())}
                    if want==nil{want=probe}else if !reflect.DeepEqual(probe,want){t.Fatalf("mutation differs from reference: %#v %#v",probe,want)}
                }
            }
        }
    }
}
func TestMutationAbsentNullableDoesNotAllocateSnapshot(t *testing.T){
    for _,update:=range []bool{false,true}{
        for _,null:=range []bool{false,true}{
            var previous float64
            for _,old:=range []bool{true,false}{
                app,probe:=mutationApp();handler:=handleCreateRecord(app)
                if update{handler=handleUpdateRecord(app)}
                if old{handler=handlePreviousCreateRecord(app);if update{handler=handlePreviousUpdateRecord(app)}}
                req:=&api.Request{Buf:&api.ResponseBuf{},Params:map[string]json.RawMessage{"id":[]byte("42"),"name":[]byte("\"valid\""),"score":[]byte("42")}}
                if null{req.Params["note"]=[]byte("null")}
                allocations:=testing.AllocsPerRun(100,func(){
                    probe.effects=probe.effects[:0];req.Buf.B=req.Buf.B[:0]
                    if err:=handler(context.Background(),req);err!=nil{t.Fatal(err)}
                })
                if old{previous=allocations}else if allocations>previous{t.Errorf("update=%v null=%v: %.0f allocations, previous %.0f",update,null,allocations,previous)}
            }
        }
    }
}
`

const mutationInputBenchmarks = `
func BenchmarkMutationInput(b *testing.B){
    policies:=[]string{"previous","canonical","local"}
    if os.Getenv("LUXO_CRUD_INPUT_BENCHMARK_REVERSE")=="1"{policies[0],policies[2]=policies[2],policies[0]}
    for _,update:=range []bool{false,true}{
        for _,binary:=range []bool{false,true}{
            for _,shape:=range []string{"required","null","values"}{
                operation,mode:="create","json";if update{operation="update"};if binary{mode="binary"}
                for _,policy:=range policies{
                    b.Run(operation+"/"+mode+"/"+shape+"/"+policy,func(b *testing.B){
                        router,probe:=mutationPolicyRouter(b,update,policy)
                        body:=mutationBody(b,router,mutationParams(update,shape),binary)
                        b.ReportAllocs();b.ResetTimer()
                        for b.Loop(){
                            probe.effects=probe.effects[:0]
                            request:=httptest.NewRequest("POST","/",bytes.NewReader(body));request.Header.Set("Content-Type","application/json")
                            if binary{request.Header.Set("X-Luxo-Mode","binary")}
                            response:=httptest.NewRecorder();router.ServeHTTP(response,request)
                            if response.Code!=200{b.Fatalf("%d %s",response.Code,response.Body.String())}
                        }
                        if probe.values["name"]!="value"||probe.values["score"]!=int64(42){b.Fatal("mutation not executed")}
                    })
                }
            }
        }
    }
}
`
