package codegen

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

// The hashes pin the unformatted resolver source, including indentation, loader
// keys, depth checks and trace scope.
func TestRelationResolverEmissionSource(t *testing.T) {
	model := &ast.ModelDecl{Name: "Source"}
	for _, sample := range []struct {
		name                      string
		nullable, list, recursive bool
		computed                  bool
		want                      string
	}{
		{"nullable-single", true, false, true, false, "dbf4466e921cd5220ac8049377fc33d9571b4a039312db237d4ff12a22c6265e"},
		{"required-list", false, true, true, false, "b64c6e827c5322b45d5c64ef0c40a8ff55f70c6b43c0d28e7484bd2cf12e0902"},
		{"leaf-list", true, true, false, false, "a772e379f445d3b1e726a254da013fbbb549ce7a8dda8f38a6fbef2865e4bccb"},
		{"nullable-list", true, true, true, false, "f79f422dfad935c9e71f4dc4c154a2ac3d743486ea096e5c54d2c868f74a7ce4"},
		{"computed-single", false, false, true, true, "c4cee66e2d72b125704da34a6cd1f039130f294b051370ed83dc46ee0eef0b3d"},
		{"computed-list", true, true, true, true, "fa5dce51f7c8ecc03a67b53d46e33552ccb52b31265485690562f05de75afe18"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			relation := Relation{FieldName: "targets", TargetName: "Target", LocalKey: "groupKey", RemoteKey: "groupKey", KeyGoType: "string", FKNullable: sample.nullable, IsList: sample.list}
			var output strings.Builder
			generateRelationResolver(&output, model, []Relation{relation}, map[string]bool{"Target": sample.recursive}, map[string]bool{"Target": sample.computed})
			assertRelationResolverSource(t, output.String(), sample.want)
		})
	}

	relations := []Relation{
		{FieldName: "targets", TargetName: "Target", LocalKey: "groupKey", RemoteKey: "groupKey", KeyGoType: "string", IsList: true},
		{FieldName: "primary", TargetName: "Target", LocalKey: "id", RemoteKey: "id", KeyGoType: "int64", FKNullable: true},
	}
	for _, sample := range []struct {
		name   string
		values bool
		want   string
	}{
		{"reference-list", false, "02117eefb5971449d099a6bcabf60d80cc78e1c5ea728f23d3a8d91a64a2f0d9"},
		{"value-list", true, "aa08c13b1ab5b305660c7938699a72c168436f909a4238f98761216bedbb12ab"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			var output strings.Builder
			generateListRelationResolverMode(&output, model, relations, map[string]bool{"Target": true}, sample.values, map[string]bool{"Target": true})
			assertRelationResolverSource(t, output.String(), sample.want)
		})
	}
}

func assertRelationResolverSource(t *testing.T, output, want string) {
	t.Helper()
	got := fmt.Sprintf("%x", sha256.Sum256([]byte(output)))
	if got != want {
		t.Fatalf("resolver source SHA = %s, want %s:\n%s", got, want, output)
	}
}

const nestedRelationBatchSource = `
model Node @noTime {
 id: Int @id
 parentId: Int?
 parent: Node? @by(id, parentId)
 children: [Node] @by(parentId, id)
}
`

func TestGeneratedNestedRelationsBatchAcrossParents(t *testing.T) {
	runGeneratedFixture(t, nestedRelationBatchSource, nestedRelationBatchAssertions, StableIDs{
		ModelFields: map[string]map[string]int{"Node": {"id": 1, "parentId": 2, "parent": 3, "children": 4}},
	})
}

func TestGeneratedNestedRelationsPostgres(t *testing.T) {
	runGeneratedFixture(t, `
model User @noTime { id: Int @id posts: [Post] @by(userId, id) }
model Post @noTime { id: Int @id userId: Int comments: [Comment] @by(postId, id) val commentCount: Int get @count(comments) }
model Comment @noTime { id: Int @id postId: Int }
api queryUsers(): [User] { return User.all() }
api listTop10Users
api listUsers
`, nestedRelationPostgresAssertions, StableIDs{ModelFields: map[string]map[string]int{
		"User": {"id": 1, "posts": 2}, "Post": {"id": 1, "userId": 2, "comments": 3, "commentCount": 4},
		"Comment": {"id": 1, "postId": 2},
	}})
}

const nestedRelationBatchAssertions = `package generated
import("context";"errors";"reflect";"testing";"testing/synctest";"time";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/dataloader";"github.com/light-speak/luxo/pkg/lux/selection")
type nestedTenantKey struct{}
type nestedBatchCall struct{kind,path string;keys []int64}
func TestNestedBatchAcrossParents(t *testing.T){
 for _,list:=range []bool{false,true}{for _,values:=range []bool{false,true}{for _,traced:=range []bool{false,true}{for _,maxBatch:=range []int{1,100}{
  synctest.Test(t,func(t *testing.T){
   var calls []nestedBatchCall;app:=nestedBatchApp(t,&calls,maxBatch,0)
   ctx:=context.WithValue(context.Background(),nestedTenantKey{},"tenant-a");if traced{ctx,_=api.WithDebugTrace(ctx,"nested-batch")}
   scope,err:=dataloader.NewScope(ctx);if err!=nil{t.Fatal(err)}
   nodes:=[]Node{{Id:1,ParentId:new(int64(10))},{Id:2,ParentId:new(int64(20))}}
   chosen:="parent{parent{id}}";if list{chosen="children{parent{id}}"};fields,err:=selection.Parse(chosen);if err!=nil{t.Fatal(err)}
   started:=time.Now()
   if values{err=resolveNodeValueListRelations(scope,app,nodes,fields)}else{err=resolveNodeListRelations(scope,app,[]*Node{&nodes[0],&nodes[1]},fields)}
   if err!=nil{t.Fatal(err)}
   if len(calls)!=2||!time.Now().Equal(started){t.Fatalf("nested list used per-parent queries or batch-window waits: list=%v values=%v max=%d calls=%+v elapsed=%s",list,values,maxBatch,calls,time.Since(started))}
   first,second:=[]int64{10,20},[]int64{110,120};if list{first,second=[]int64{1,2},[]int64{101,102,201,202}}
   if !reflect.DeepEqual(calls[0].keys,first)||!reflect.DeepEqual(calls[1].keys,second){t.Fatal("batch keys lost parent order",calls)}
   path:="";if traced{path="parent";if list{path="children"}}
   if calls[1].path!=path{t.Fatal("merged nested batch lost trace path",calls)}
   for _,node:=range nodes{if list{if len(node.Children)!=2{t.Fatal("children lost",node)};for _,child:=range node.Children{if child.Parent==nil||child.Parent.Id!=*child.ParentId{t.Fatal("child was mapped to another parent",child)}}}else if node.Parent==nil||node.Parent.Parent==nil||node.Parent.Parent.Id!=*node.Parent.ParentId{t.Fatal("nested scalar relation missing",node)}}
  })
 }}}}
}
func nestedBatchApp(t *testing.T,calls *[]nestedBatchCall,maxBatch,failAt int)*App{
 t.Helper();app:=&App{};cfg:=dataloader.DefaultConfig();cfg.MaxBatch=maxBatch;cfg.Wait=time.Second
 record:=func(ctx context.Context,kind string,keys []int64)error{
  if ctx.Value(nestedTenantKey{})!="tenant-a"{return errors.New("request context changed")}
  *calls=append(*calls,nestedBatchCall{kind:kind,path:api.TraceFieldPath(ctx),keys:append([]int64(nil),keys...)})
  if len(*calls)==failAt{return nestedBatchFailure};return nil
 }
 app.SetLoaders(NewLoaders(cfg,
  func(ctx context.Context,keys []int64,_ []*selection.Field)(map[int64]*Node,error){
   if err:=record(ctx,"parent",keys);err!=nil{return nil,err};rows:=map[int64]*Node{}
   for _,key:=range keys{row:=&Node{Id:key};if key<100{row.ParentId=new(key+100)};rows[key]=row};return rows,nil
  },
  func(ctx context.Context,keys []int64,_ []*selection.Field)(map[int64][]*Node,error){
   if err:=record(ctx,"children",keys);err!=nil{return nil,err};rows:=map[int64][]*Node{}
   for _,key:=range keys{rows[key]=[]*Node{{Id:key*10+1,ParentId:new(key*100+1)},{Id:key*10+2,ParentId:new(key*100+2)}}};return rows,nil
  },
 ));return app
}
var nestedBatchFailure=errors.New("nested batch failed")
func TestEmptyNestedListDoesNotAllocateReferences(t *testing.T){
 var calls []nestedBatchCall;app:=nestedBatchApp(t,&calls,100,0)
 ctx:=context.WithValue(t.Context(),nestedTenantKey{},"tenant-a")
 fields,err:=selection.Parse("parent{parent{id}}");if err!=nil{t.Fatal(err)}
 allocs:=testing.AllocsPerRun(20,func(){if err:=resolveNodeListRelations(ctx,app,nil,fields);err!=nil{t.Fatal(err)}})
 if allocs!=0||len(calls)!=0{t.Fatal("empty nested list allocated references or queried",allocs,calls)}
}
func TestSingleParentListReusesChildReferences(t *testing.T){
 parents:=map[int64]*Node{10:{Id:10,ParentId:new(int64(110))},110:{Id:110}}
 children:=map[int64][]*Node{1:{parents[10]}}
 app:=&App{};app.SetLoaders(NewLoaders(dataloader.Config{MaxBatch:1},
  func(context.Context,[]int64,[]*selection.Field)(map[int64]*Node,error){return parents,nil},
  func(context.Context,[]int64,[]*selection.Field)(map[int64][]*Node,error){return children,nil},
 ))
 row:=&Node{Id:1,ParentId:new(int64(10))};rows:=[]*Node{row};ctx:=t.Context()
 fields,err:=selection.Parse("parent{parent{id}}");if err!=nil{t.Fatal(err)}
 direct:=testing.AllocsPerRun(20,func(){if err:=resolveNodeRelations(ctx,app,row,fields);err!=nil{t.Fatal(err)}})
 list:=testing.AllocsPerRun(20,func(){if err:=resolveNodeListRelations(ctx,app,rows,fields);err!=nil{t.Fatal(err)}})
 if list>direct{t.Fatal("single-parent list allocated staging references",list,direct)}
}
func TestSingleRelationDoesNotWaitForBatchWindow(t *testing.T){
 for _,maxBatch:=range []int{1,100}{synctest.Test(t,func(t *testing.T){
  var calls []nestedBatchCall;app:=nestedBatchApp(t,&calls,maxBatch,0)
  ctx:=context.WithValue(context.Background(),nestedTenantKey{},"tenant-a")
  fields,err:=selection.Parse("parent{parent{id}}");if err!=nil{t.Fatal(err)}
  row:=&Node{Id:1,ParentId:new(int64(10))};started:=time.Now()
  if err:=resolveNodeRelations(ctx,app,row,fields);err!=nil||!time.Now().Equal(started)||len(calls)!=2||row.Parent==nil||row.Parent.Parent==nil{t.Fatal("known single key entered the automatic batch window",err,calls,time.Since(started))}
 })}
}
func TestNilNestedModelIsRejected(t *testing.T){
 defer func(){if value:=recover();value!=nil{t.Fatalf("nil model caused a panic instead of an error: %v",value)}}()
 var calls []nestedBatchCall;app:=nestedBatchApp(t,&calls,1,0)
 ctx:=context.WithValue(t.Context(),nestedTenantKey{},"tenant-a")
 fields,err:=selection.Parse("parent{parent{id}}");if err!=nil{t.Fatal(err)}
 if err:=resolveNodeListRelations(ctx,app,[]*Node{nil},fields);!errors.Is(err,codec.ErrNullModelElement)||len(calls)!=0{t.Fatal("nil input reached the backend",err,calls)}
 app.loaders.NodeChildren=dataloader.New(func(context.Context,[]int64,[]*selection.Field)(map[int64][]*Node,error){return map[int64][]*Node{1:{nil}},nil},dataloader.DefaultConfig())
 fields,err=selection.Parse("children{parent{id}}");if err!=nil{t.Fatal(err)}
 if err:=resolveNodeListRelations(ctx,app,[]*Node{{Id:1}},fields);!errors.Is(err,codec.ErrNullModelElement)||len(calls)!=0{t.Fatal("nil child reached the next backend",err,calls)}
}
func TestNestedBatchLargeAndSharedChildren(t *testing.T){
 for _,count:=range []int{1,8,32}{
  var calls []nestedBatchCall;app:=nestedBatchApp(t,&calls,100,0)
  ctx:=context.WithValue(t.Context(),nestedTenantKey{},"tenant-a")
  fields,err:=selection.Parse("children{parent{id}}");if err!=nil{t.Fatal(err)}
  rows:=make([]*Node,count);for i:=range rows{rows[i]=&Node{Id:int64(i%4+1)}}
  if err:=resolveNodeListRelations(ctx,app,rows,fields);err!=nil||len(calls)!=2{t.Fatal("large or repeated parent keys split nested batches",err,calls)}
  for _,row:=range rows{if len(row.Children)!=2||row.Children[0].Parent==nil||row.Children[1].Parent==nil||row.Children[0].Parent.Id!=*row.Children[0].ParentId{t.Fatal("shared child ownership changed",row)}}
 }
}
func TestRelationCancellationStopsFurtherLoading(t *testing.T){
 synctest.Test(t,func(t *testing.T){
  ctx,cancel:=context.WithCancelCause(context.WithValue(context.Background(),nestedTenantKey{},"tenant-a"));defer cancel(nil)
  cause:=errors.New("request ended");called:=false;app:=&App{};cfg:=dataloader.DefaultConfig()
  app.SetLoaders(NewLoaders(cfg,
   func(inner context.Context,_ []int64,_ []*selection.Field)(map[int64]*Node,error){called=true;<-inner.Done();if context.Cause(inner)!=cause{t.Error("cancellation cause changed")};return nil,inner.Err()},
   func(context.Context,[]int64,[]*selection.Field)(map[int64][]*Node,error){t.Error("canceled relation continued loading");return nil,nil},
  ))
  fields,err:=selection.Parse("parent{children{id}}");if err!=nil{t.Fatal(err)}
  go func(){time.Sleep(time.Millisecond);cancel(cause)}()
  if err:=resolveNodeRelations(ctx,app,&Node{Id:1,ParentId:new(int64(10))},fields);!errors.Is(err,context.Canceled)||!called{t.Fatal("canceled relation returned success",err,called)}
 })
}
func TestNestedBatchFailureAndEmptyControls(t *testing.T){
 for _,list:=range []bool{false,true}{for _,failAt:=range []int{1,2}{
  synctest.Test(t,func(t *testing.T){
   var calls []nestedBatchCall;app:=nestedBatchApp(t,&calls,100,failAt)
   ctx,err:=dataloader.NewScope(context.WithValue(context.Background(),nestedTenantKey{},"tenant-a"));if err!=nil{t.Fatal(err)}
   chosen:="parent{parent{id}}";if list{chosen="children{parent{id}}"};fields,err:=selection.Parse(chosen);if err!=nil{t.Fatal(err)}
   nodes:=[]*Node{{Id:1,ParentId:new(int64(10))},{Id:2,ParentId:new(int64(20))}}
   if err:=resolveNodeListRelations(ctx,app,nodes,fields);!errors.Is(err,nestedBatchFailure)||len(calls)!=failAt{t.Fatal("nested failure was swallowed or retried",err,calls)}
   calls=nil
   if err:=resolveNodeListRelations(ctx,app,nil,fields);err!=nil||len(calls)!=0{t.Fatal("empty list queried the backend",err,calls)}
  })
 }}
}
`

const nestedRelationPostgresAssertions = `package generated
import("bytes";"encoding/json";"errors";"io";"net";"net/http";"net/http/httptest";"testing";"time";"github.com/light-speak/luxo/internal/testutil";"github.com/light-speak/luxo/pkg/lux/api";"github.com/light-speak/luxo/pkg/lux/dataloader";"github.com/light-speak/luxo/pkg/lux/pg";"github.com/light-speak/luxo/pkg/lux/rpc";"github.com/light-speak/luxo/pkg/lux/schema";"github.com/light-speak/luxo/pkg/lux/selection";"github.com/testcontainers/testcontainers-go";"github.com/testcontainers/testcontainers-go/modules/postgres")
func TestNestedRelationDatabaseQueries(t *testing.T){
 container,err:=postgres.Run(t.Context(),testutil.PostgresImage(),postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies());testcontainers.CleanupContainer(t,container);if err!=nil{t.Fatal(err)}
 connection,err:=container.ConnectionString(t.Context(),"sslmode=disable");if err!=nil{t.Fatal(err)};db,err:=pg.NewDB(t.Context(),connection);if err!=nil{t.Fatal(err)};t.Cleanup(db.Close)
 if _,err:=pg.Exec(t.Context(),db,"CREATE TABLE users(id bigint PRIMARY KEY);CREATE TABLE posts(id bigint PRIMARY KEY,user_id bigint);CREATE TABLE comments(id bigint PRIMARY KEY,post_id bigint);INSERT INTO users VALUES(1),(2),(3);INSERT INTO posts VALUES(11,1),(12,1),(21,2);INSERT INTO comments VALUES(111,11),(112,11),(211,21)");err!=nil{t.Fatal(err)}
 app:=NewFromDB(db);app.SetLoaders(NewDefaultLoaders(app,dataloader.DefaultConfig()))
 router:=api.NewRouter();if err:=RegisterSchema(router.Schema);err!=nil{t.Fatal(err)};if err:=RegisterHandlers(router,app);err!=nil{t.Fatal(err)};router.SetDebugTraceKey("nested-test");if err:=router.Prepare();err!=nil{t.Fatal(err)}
 server:=httptest.NewServer(api.TraceMiddleware(router));defer server.Close();httpClient:=server.Client();httpClient.Timeout=5*time.Second
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)};backend:=rpc.NewServer(router);done:=make(chan error,1);go func(){done<-backend.Serve(listener)}();defer func(){backend.Close();if err:=<-done;err!=nil{t.Error(err)}}();remote:=rpc.NewClient(listener.Addr().String());defer remote.Close()
 for _,name:=range []string{"queryUsers","listTop10Users","listUsers"}{for _,fields:=range []string{"id","id,posts{id,comments{id}}","id,posts{id,commentCount,comments{id}}"}{for mode:=0;mode<3;mode++{
  raw,snapshot,err:=nestedDatabaseCall(t,router,remote,httpClient,server.URL,name,fields,mode);if err!=nil{t.Fatal(err)}
  expected:=1;if fields!="id"{expected=3};if bytes.Contains([]byte(fields),[]byte("commentCount")){expected=4};if router.Schema.APIs[name].Paginated{expected++}
  queries:=nestedSQLQueries(snapshot);if queries!=expected{t.Fatalf("mode=%d API=%s selection=%s used %d SQL queries, expected %d: %+v",mode,name,fields,queries,expected,snapshot.Spans)}
  verifyNestedDatabaseRows(t,raw,fields,router.Schema.APIs[name].Paginated)
 }}}
 if _,err:=pg.Exec(t.Context(),db,"DROP TABLE comments");err!=nil{t.Fatal(err)}
 for mode:=0;mode<3;mode++{
  raw,trace,err:=nestedDatabaseCall(t,router,remote,httpClient,server.URL,"queryUsers","id,posts{id,comments{id}}",mode)
  if err==nil||len(raw)!=0||nestedSQLQueries(trace)!=3{t.Fatal("failed nested SQL became partial success",mode,string(raw),err,trace)}
  if _,_,err:=nestedDatabaseCall(t,router,remote,httpClient,server.URL,"queryUsers","id",mode);err!=nil{t.Fatal("nested failure poisoned subsequent call",err)}
 }
}
func nestedSQLQueries(trace api.DebugTraceSnapshot)int{
 count:=0;for _,span:=range trace.Spans{if span.Name=="database.query"&&span.DatabaseOperation=="SELECT"{count++}};return count
}
func nestedDatabaseCall(t *testing.T,router *api.Router,remote *rpc.Client,client *http.Client,url,name,chosen string,mode int)([]byte,api.DebugTraceSnapshot,error){
 t.Helper();fields,err:=selection.Parse(chosen);if err!=nil{t.Fatal(err)};mask,err:=schema.SelectToFieldMask(fields,router.Schema.Models["User"],router.Schema);if err!=nil{t.Fatal(err)}
 var raw []byte;var snapshot api.DebugTraceSnapshot
 if mode==2{
  ctx,trace:=api.WithDebugTrace(t.Context(),"nested-rpc");raw,err=remote.CallWithMaskContext(ctx,"",router.Schema.APIs[name].ID,mask,[]byte{0});snapshot=trace.Snapshot();if err!=nil{return nil,snapshot,err}
 }else{
  body,encodeErr:=json.Marshal(map[string]string{"$api":name,"$select":chosen});if encodeErr!=nil{t.Fatal(encodeErr)}
  if mode==1{body,err=api.EncodeBinaryRequest(router.Schema.APIs[name].ID,mask,nil,router.Registry.ParamOrder(name));if err!=nil{t.Fatal(err)}}
  request,err:=http.NewRequestWithContext(t.Context(),"POST",url,bytes.NewReader(body));if err!=nil{t.Fatal(err)};if mode==1{request.Header.Set("X-Luxo-Mode","binary")};request.Header.Set(api.DebugTraceRequestHeader,"true");request.Header.Set(api.DebugTraceKeyHeader,"nested-test")
  response,err:=client.Do(request);if err!=nil{t.Fatal(err)};raw,err=io.ReadAll(response.Body);response.Body.Close();if err!=nil{t.Fatal(err)}
  snapshot,err=api.DecodeDebugTraceHeader(response.Header.Get(api.DebugTraceResponseHeader));if err!=nil{t.Fatal(err)}
  if response.StatusCode!=200{if response.StatusCode!=500{t.Fatalf("unexpected status %d: %s",response.StatusCode,raw)};if mode==1{appError,decodeErr:=api.DecodeBinaryError(raw,response.StatusCode);if decodeErr!=nil||appError==nil||appError.Code!=500{t.Fatal("invalid failure envelope",appError,decodeErr)};err=appError}else{var failure struct{Code int};if json.Unmarshal(raw,&failure)!=nil||failure.Code!=500||bytes.Contains(raw,[]byte("\"data\":"))||bytes.Contains(raw,[]byte("comments")){t.Fatal("invalid error or leaked partial result/SQL",string(raw))};err=errors.New("HTTP 500")};return nil,snapshot,err}
  if mode==0{var output struct{Data json.RawMessage};if err:=json.Unmarshal(raw,&output);err!=nil{t.Fatal(err)};return output.Data,snapshot,nil}
 }
 raw,err=router.StreamPayloadJSON(name,raw);return raw,snapshot,err
}
func verifyNestedDatabaseRows(t *testing.T,raw []byte,fields string,paginated bool){
 t.Helper();if paginated{var page struct{Items json.RawMessage;Total,Page,PageSize int64};if err:=json.Unmarshal(raw,&page);err!=nil||page.Total!=3||page.Page!=1||page.PageSize<3{t.Fatal("pagination metadata changed",string(raw),err)};raw=page.Items};var rows []*User;if err:=json.Unmarshal(raw,&rows);err!=nil||len(rows)!=3{t.Fatal("root rows changed",string(raw),err)}
 seen:=map[int64]bool{};for _,row:=range rows{if row==nil||row.Id<1||row.Id>3||seen[row.Id]{t.Fatal("unexpected user",row)};seen[row.Id]=true;if fields=="id"{continue}
  wantPosts:=map[int64]int{1:2,2:1,3:0}[row.Id];if len(row.Posts)!=wantPosts{t.Fatal("posts crossed owner groups",row)}
  for _,post:=range row.Posts{if post==nil||post.Id/10!=row.Id{t.Fatal("post owner changed",row)};want:=map[int64]int64{11:2,12:0,21:1}[post.Id];if int64(len(post.Comments))!=want{t.Fatal("nested comments lost",post)}
   if bytes.Contains([]byte(fields),[]byte("commentCount"))&&post.CommentCount!=want{t.Fatal("computed result changed",post)}
   for _,comment:=range post.Comments{if comment==nil||comment.Id/10!=post.Id{t.Fatal("comment mapped to another post",post)}}
  }
 }
}
`
