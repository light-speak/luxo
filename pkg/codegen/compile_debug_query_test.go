package codegen

import (
	"strings"
	"testing"
)

func TestDebugQueriesRealDB(t *testing.T) {
	_, shared, ok := strings.Cut(paginatedLoopAssertions, "type pageSQLLog struct")
	if !ok {
		t.Fatal("missing shared SQL observer")
	}
	shared, _, ok = strings.Cut(shared, "type pageCase struct")
	if !ok {
		t.Fatal("missing SQL observer boundary")
	}
	runGeneratedFixture(t, debugQuerySource, debugQueryImports+"type pageSQLLog struct"+shared+debugQueryAssertions, StableIDs{ModelFields: map[string]map[string]int{
		"Node": {"id": 1, "name": 2, "parentId": 3, "parent": 4},
	}})
}

func TestDebugPaginationFormalGeneration(t *testing.T) {
	for _, body := range []string{
		`return Node.select().orderBy(id.asc).all().d`,
		`val rows = Node.select().orderBy(id.asc).all() return rows.d`,
		`val rows = Node.select().orderBy(id.asc).all().d return rows`,
		`val rows = Node.select().orderBy(id.asc).all() rows.d return rows`,
	} {
		input := analyzePermissionFixture(t, `model Node @noTime { id: Int @id } api page(): [Node] @paginate { `+body+` }`)
		config := GeneratorConfig{IDs: completeFixtureAPIIDs(input, StableIDs{ModelFields: map[string]map[string]int{"Node": {"id": 1}}})}
		generated, err := Generate(input, "generated", config)
		if err != nil {
			t.Errorf("diagnostic changed a safe query: %s / %v", body, err)
			continue
		}
		if !strings.Contains(string(generated.Files["handler.gen.go"]), ".AllWithCount(ctx)") {
			t.Error("diagnostic removed SQL paging", body)
		}
	}
}

const debugQuerySource = `
model Node @noTime { id: Int @id name: String parentId: Int? parent: Node? @by(id, parentId) }
fn debugQueryAll(): [Node] { return Node.select().orderBy(id.asc).all().d }
api plainRows(): [Node] { return Node.select().orderBy(id.asc).all() }
api loggedRows(): [Node] { return Node.select().orderBy(id.asc).all().d }
api aliasRows(): [Node] { val rows = Node.select().orderBy(id.asc).all() return rows.d }
api indirectRows(): [Node] { return debugQueryAll() }
api loggedFirst(inputId: Int): Node? { return Node.where(it.id == inputId).select().first()?.d }
api plainFirst(inputId: Int): Node? { return Node.where(it.id == inputId).select().first() }
api plainPage(): [Node] @paginate { return Node.select().orderBy(id.asc).all() }
api loggedPage(): [Node] @paginate { return Node.select().orderBy(id.asc).all().d }
api aliasPage(): [Node] @paginate { val rows = Node.select().orderBy(id.asc).all() return rows.d }
api assignedPage(): [Node] @paginate { val rows = Node.select().orderBy(id.asc).all().d return rows }
api statementPage(): [Node] @paginate { val rows = Node.select().orderBy(id.asc).all() rows.d return rows }
`

const debugQueryImports = `package generated
import("bytes";"context";"encoding/json";"fmt";"net";"net/http/httptest";"strings";"sync";"testing";
"github.com/jackc/pgx/v5";"github.com/jackc/pgx/v5/pgxpool";
"github.com/light-speak/luxo/internal/testutil";"github.com/light-speak/luxo/pkg/lux/api";
"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/dataloader";"github.com/light-speak/luxo/pkg/lux/luxolog";
"github.com/light-speak/luxo/pkg/lux/pg";"github.com/light-speak/luxo/pkg/lux/rpc";
"github.com/light-speak/luxo/pkg/lux/schema";"github.com/light-speak/luxo/pkg/lux/selection";
"github.com/testcontainers/testcontainers-go";"github.com/testcontainers/testcontainers-go/modules/postgres")
`

const debugQueryAssertions = `
func TestDebugQuerySQL(t *testing.T) {
 container,err:=postgres.Run(t.Context(),testutil.PostgresImage(),postgres.WithDatabase("debug_query"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies())
 testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)}
 cfg,err:=pgxpool.ParseConfig(connection);if err!=nil{t.Fatal(err)};queries:=&pageSQLLog{};cfg.ConnConfig.Tracer=queries
 pool,err:=pgxpool.NewWithConfig(t.Context(),cfg);if err!=nil{t.Fatal(err)};db:=pg.NewDBFromPool(pool);t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE nodes(id bigint PRIMARY KEY,name text NOT NULL,parent_id bigint);INSERT INTO nodes VALUES(1,'one',NULL),(2,'two',1),(3,'three',1)");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);app.SetLoaders(NewDefaultLoaders(app,dataloader.DefaultConfig()))
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};if err:=router.Prepare();err!=nil{t.Fatal(err)}
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)};server:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-server.Serve(listener)}()
 defer func(){server.Close();if err:=<-done;err!=nil{t.Error(err)}}();client:=rpc.NewClient(listener.Addr().String());defer client.Close()
 previous:=luxolog.GetLevel();defer luxolog.SetLevel(previous)
 for _,level:=range []int{luxolog.LevelInfo,luxolog.LevelDebug}{
  if err:=luxolog.SetLevel(level);err!=nil{t.Fatal(err)}
  for _,name:=range []string{"plainRows","loggedRows","aliasRows","indirectRows","plainFirst","loggedFirst"}{
   for _,related:=range []bool{false,true}{for mode:=0;mode<3;mode++{t.Run(fmt.Sprintf("%s/related=%v/mode=%d/level=%d",name,related,mode,level),func(t *testing.T){
    queries.take();data,err:=callDebugQuery(t,router,client,name,related,mode,2);if err!=nil{t.Fatal(err,queries.take())}
    isList:=strings.HasSuffix(name,"Rows");assertDebugQuery(t,data,isList,related)
    observed:=queries.take();want:=1;if related{want=2};if len(observed)!=want{t.Fatalf("unexpected query count: %q",observed)}
    for _,query:=range observed{if strings.Contains(query,"*")||strings.Contains(query,"name"){t.Error("diagnostic expanded projection",query)}}
   })}}
  }
  for mode:=0;mode<3;mode++{t.Run(fmt.Sprintf("missing/mode=%d/level=%d",mode,level),func(t *testing.T){
   queries.take();data,err:=callDebugQuery(t,router,client,"loggedFirst",true,mode,99);if err!=nil||string(data)!="null"{t.Fatal(string(data),err)}
   if observed:=queries.take();len(observed)!=1{t.Fatal("missing record triggered relation reads",observed)}
  })}
  for _,name:=range []string{"plainPage","loggedPage","aliasPage","assignedPage","statementPage"}{for _,page:=range []int64{1,2,5}{for mode:=0;mode<3;mode++{t.Run(fmt.Sprintf("%s/page=%d/mode=%d/level=%d",name,page,mode,level),func(t *testing.T){
   queries.take();data,err:=callDebugQuery(t,router,client,name,false,mode,page);if err!=nil{t.Fatal(err)}
   var value struct{Items []struct{Id int64};Total int64;Page int64;PageSize int64};if err:=json.Unmarshal(data,&value);err!=nil{t.Fatal(err)}
   count:=0;if page<=3{count=1};if value.Total!=3||value.Page!=page||value.PageSize!=1||len(value.Items)!=count||count==1&&value.Items[0].Id!=page{t.Fatal("pagination changed",string(data))}
   observed:=queries.take();if len(observed)!=2{t.Fatal("unexpected page SQL count",observed)}
   for _,query:=range observed{upper:=strings.ToUpper(query);if !strings.Contains(upper,"COUNT(")&&!strings.Contains(upper," LIMIT $"){t.Error("full list read before pagination",query)};if strings.Contains(query,"name")||strings.Contains(query,"parent_id"){t.Error("page diagnostic expanded columns",query)}}
  })}}}
 }
 if _,err:=pg.Exec(t.Context(),db,"TRUNCATE nodes");err!=nil{t.Fatal(err)}
 for mode:=0;mode<3;mode++{t.Run(fmt.Sprintf("empty/mode=%d",mode),func(t *testing.T){
  queries.take();data,err:=callDebugQuery(t,router,client,"loggedRows",true,mode,0);if err!=nil||string(data)!="[]"{t.Fatal(string(data),err)}
  if observed:=queries.take();len(observed)!=1{t.Fatal("empty list triggered relation reads",observed)}
 })}
 if _,err:=pg.Exec(t.Context(),db,"DROP TABLE nodes");err!=nil{t.Fatal(err)}
 for mode:=0;mode<3;mode++{t.Run(fmt.Sprintf("failure/mode=%d",mode),func(t *testing.T){
  if data,err:=callDebugQuery(t,router,client,"loggedRows",false,mode,0);err==nil{t.Fatal("query failure became success",string(data))}
 })}
}

func callDebugQuery(t *testing.T,router *api.Router,client *rpc.Client,name string,related bool,mode int,inputID int64)([]byte,error){
 t.Helper();fields:=[]*selection.Field{{Name:"id"}};selected:="id";if related{fields=append(fields,&selection.Field{Name:"parent",Children:[]*selection.Field{{Name:"id"}}});selected+=",parent{id}"}
 mask,err:=schema.SelectToFieldMask(fields,router.Schema.Models["Node"],router.Schema);if err!=nil{t.Fatal(err)}
 params:=map[string]any{};if strings.HasSuffix(name,"First"){params["inputId"]=inputID}
 if strings.HasSuffix(name,"Page"){params["page"]=inputID;params["pageSize"]=int64(1)}
 definition:=router.Schema.APIs[name];body,err:=api.EncodeBinaryRequest(definition.ID,mask,params,router.Registry.ParamOrder(name));if err!=nil{t.Fatal(err)}
 if mode==2{_,offset:=codec.ReadVarint(body,0);length,n:=codec.ReadVarint(body,offset);offset+=n+int(length);body,err=client.CallWithMaskContext(t.Context(),"",definition.ID,mask,body[offset:]);if err!=nil{return nil,err}}
 if mode!=2{
  if mode==0{document:=map[string]any{"$api":name,"$select":selected};for k,v:=range params{document[k]=v};body,err=json.Marshal(document);if err!=nil{t.Fatal(err)}}
  request:=httptest.NewRequest("POST","/",bytes.NewReader(body));if mode==1{request.Header.Set("X-Luxo-Mode","binary")};response:=httptest.NewRecorder();router.ServeHTTP(response,request)
  if response.Code!=200{return nil,fmt.Errorf("status=%d body=%s",response.Code,response.Body.String())};body=response.Body.Bytes()
 }
 if mode==0{var envelope struct{Data json.RawMessage};if err:=json.Unmarshal(body,&envelope);err!=nil{t.Fatal(err)};return envelope.Data,nil}
 if strings.HasSuffix(name,"Page"){return schema.BinaryPaginatedListToJSON(nil,body,router.Schema.Models["Node"],router.Schema)}
 return router.StreamPayloadJSON(name,body)
}

func assertDebugQuery(t *testing.T,data []byte,list,related bool){
 t.Helper();var rows []map[string]json.RawMessage
 if list{if err:=json.Unmarshal(data,&rows);err!=nil{t.Fatal(err)}}else{var row map[string]json.RawMessage;if err:=json.Unmarshal(data,&row);err!=nil{t.Fatal(err)};rows=append(rows,row)}
 want:=1;if list{want=3};if len(rows)!=want{t.Fatal("row count changed",string(data))}
 for index,row:=range rows{
  id:=index+1;if !list{id=2};if string(row["id"])!=fmt.Sprint(id){t.Fatal("id changed",string(data))}
  count:=1;if related{count=2;expected:="null";if id>1{expected="{\"id\":1}"};if string(row["parent"])!=expected{t.Fatal("relation missing",string(data))}}
  if len(row)!=count{t.Fatal("projection changed",string(data))}
 }
}
`
