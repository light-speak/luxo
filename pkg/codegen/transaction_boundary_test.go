package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/lexer"
	"github.com/light-speak/luxo/pkg/parser"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestTransactionStatementPropagatesError(t *testing.T) {
	for _, expression := range []ast.Expr{
		&ast.TransactionExpr{Body: &ast.Block{}},
		&ast.CallExpr{Func: &ast.Ident{Name: "transaction"}, Args: []*ast.NamedArg{{Value: &ast.LambdaExpr{Body: &ast.Block{}}}}},
	} {
		c := newCompiler(nil)
		c.inFunction, c.functionResult = true, &ast.TypeRef{Name: "Int"}
		c.compileExprStmt(&ast.ExprStmt{Expr: expression})
		output := compilerOut(c)
		if !strings.Contains(output, "if err := app.Tx(ctx, func(app *App) error {") ||
			!strings.Contains(output, "return _value, err") {
			t.Fatalf("transaction statement ignored failure: %s", output)
		}
	}
}

func TestGeneratedTransactionBindsModelClients(t *testing.T) {
	runGeneratedTransactionFixture(t, transactionBoundarySource, transactionBoundaryAssertions+generatedMutationAssertions, generatedCRUDAssertions)
}

func TestGeneratedNamedLoaderApplication(t *testing.T) {
	runGeneratedTransactionFixture(t, `
model User {
  id: Int @id
  name: String
}
api usersNamed(name: String): [User] {
  return User.load(name: name)
}
`, `package generated
import (
  "testing"
  "context"
  "strings"
  "github.com/light-speak/luxo/pkg/lux/dataloader"
  "github.com/light-speak/luxo/pkg/lux/selection"
)
func TestNamedLoaderApp(t *testing.T) {
  app:=&App{}
  if app.loaders.UserByName!=nil {t.Fatal("zero app contains initialized loader")}
}
func TestCustomLoaderRequiresTransactionFactory(t *testing.T) {
  app:=&App{}
  loader:=dataloader.New(func(context.Context,[]string,[]*selection.Field)(map[string][]*User,error){return nil,nil},dataloader.DefaultConfig())
  app.SetLoaders(Loaders{UserByName:loader})
  called:=false
  err:=app.Tx(t.Context(),func(*App)error{called=true;return nil})
  if err==nil || !strings.Contains(err.Error(),"transaction factory") || called {t.Fatalf("unsafe transaction: err=%v called=%v",err,called)}
  app.SetLoaders(app.loaders.WithTransactionFactory(nil))
  if app.Tx(t.Context(),func(*App)error{t.Fatal("nil factory called business code");return nil})==nil {t.Fatal("nil factory accepted")}
}
func TestReplacingBoundLoaderRequiresNewFactory(t *testing.T) {
  for _,remove:=range []bool{false,true} {
    app:=&App{}
    loaders:=NewDefaultLoaders(app,dataloader.DefaultConfig())
    original:=loaders.UserByName
    if remove {loaders.UserByName=nil} else {
      loaders.UserByName=dataloader.New(func(context.Context,[]string,[]*selection.Field)(map[string][]*User,error){return nil,nil},dataloader.DefaultConfig())
    }
    if !loaders.transactionFactoryRequired() {t.Fatalf("replacement retained stale binding: remove=%v",remove)}
    app.SetLoaders(loaders)
    err:=app.Tx(t.Context(),func(*App)error{t.Fatal("stale factory entered transaction");return nil})
    if err==nil || !strings.Contains(err.Error(),"transaction factory") {t.Fatalf("replacement accepted: %v",err)}
    loaders.UserByName=original
    if loaders.transactionFactoryRequired() {t.Fatal("restoring exact loader set invalidated factory")}
  }
}
func TestExplicitFactoryBindsCurrentLoaderSet(t *testing.T) {
  app:=&App{}
  loaders:=NewDefaultLoaders(app,dataloader.DefaultConfig())
  loaders.UserByName=nil
  declared:=loaders.WithTransactionFactory(func(tx *App)Loaders{return NewDefaultLoaders(tx,dataloader.DefaultConfig())})
  if declared.transactionFactoryRequired() {t.Fatal("explicit current binding rejected")}
  if !loaders.transactionFactoryRequired() {t.Fatal("declaring factory changed original value")}
}
`)
}

func runGeneratedTransactionFixture(t *testing.T, source, assertions string, additional ...string) {
	t.Helper()
	runGeneratedFixture(t, source, assertions, StableIDs{
		APIs: map[string]int{"getUser": 101, "listUsers": 102, "createUser": 103, "updateUser": 104, "deleteUser": 105, "deleteUsers": 106},
		APIParams: map[string]map[string]int{
			"listUsers": {"page": 1, "pageSize": 2},
			"getUser":   {"id": 1}, "createUser": {"id": 1, "name": 2},
			"updateUser": {"id": 1, "name": 2}, "deleteUser": {"id": 1}, "deleteUsers": {"ids": 1},
		},
		ModelFields: map[string]map[string]int{
			"User": {"id": 1, "name": 2},
			"Post": {"id": 1, "userId": 2, "user": 3},
		},
		EventFields: map[string]map[string]int{"Changed": {"id": 1}},
	}, additional...)
}

func runGeneratedFixture(t *testing.T, source, assertions string, ids StableIDs, additional ...string) {
	t.Helper()
	tokens, lexErrors := lexer.New(source, "transaction.luxo").Tokenize()
	if len(lexErrors) != 0 {
		t.Fatal(lexErrors)
	}
	file, parseErrors := parser.New(tokens).Parse("transaction.luxo")
	if len(parseErrors) != 0 {
		t.Fatal(parseErrors)
	}
	result := semantic.Analyze([]*ast.File{file})
	if len(result.Errors) != 0 {
		t.Fatal(result.Errors)
	}
	generator := mustNewGenerator(t, GeneratorConfig{Driver: DriverPG, IDs: completeFixtureAPIIDs(result, ids)})
	generated, err := generator.Generate(result, "generated", nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := writeProtocolFixture(t, "package generated\n", assertions)
	for i, source := range additional {
		if err := os.WriteFile(filepath.Join(dir, "additional_"+strconv.Itoa(i)+"_test.go"), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range generated.Files {
		if filepath.Base(name) != name {
			t.Fatalf("unexpected generated file %q", name)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"test", "-count=1", "-timeout=90s"}
	if testing.Verbose() {
		args = append(args, "-v")
	}
	if os.Getenv("LUXO_TRANSACTION_RACE") == "1" {
		args = append(args, "-race")
	}
	if os.Getenv("LUXO_TRANSACTION_BENCHMARK") == "1" {
		args = append(args, "-run=^$", "-bench=^BenchmarkTransactionLoaders$", "-benchtime=500ms")
	}
	if os.Getenv("LUXO_AUTH_HTTP_BENCHMARK") == "1" {
		args = append(args, "-run=^$", "-bench=^BenchmarkGeneratedAuthRefreshHTTP$", "-benchmem", "-benchtime=500ms")
	}
	if pattern := os.Getenv("LUXO_GENERATED_BENCHMARK"); pattern != "" {
		args = append(args, "-run=^$", "-bench="+pattern, "-benchmem", "-benchtime=200ms", "-cpu=2")
	}
	command := exec.Command("go", append(args, ".")...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated transaction regression: %v\n%s", err, output)
	} else if testing.Verbose() || os.Getenv("LUXO_TRANSACTION_BENCHMARK") == "1" {
		t.Logf("%s", output)
	}
}

const transactionBoundarySource = `
model User @crud {
  id: Int @id
  name: String
}
model Post {
  id: Int @id
  userId: Int
  user: User
}
event Changed(id: Int)
api nativeMarker(): Int @native
api rollbackUser(id: Int): Boolean {
  transaction {
    User.where(id: id).update(name: "changed")
    throw error.NotFound
  }
  return true
}
api commitUser(id: Int): Boolean {
  transaction {
    User.where(id: id).update(name: "committed")
  }
  return true
}
api nestedRollback(id: Int): Boolean {
  transaction {
    transaction {
      User.where(id: id).update(name: "nested")
    }
    throw error.NotFound
  }
  return true
}
`

const transactionBoundaryAssertions = `package generated
import (
  "context"
  "encoding/json"
  "errors"
  "testing"
  "github.com/light-speak/luxo/pkg/lux"
  "github.com/light-speak/luxo/pkg/lux/api"
  "github.com/light-speak/luxo/pkg/lux/dataloader"
  luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
  "github.com/light-speak/luxo/pkg/lux/event"
  "github.com/light-speak/luxo/pkg/lux/pg"
  "github.com/light-speak/luxo/pkg/lux/queue"
  "github.com/light-speak/luxo/pkg/lux/selection"
  "github.com/testcontainers/testcontainers-go"
  "github.com/testcontainers/testcontainers-go/modules/postgres"
)
type fixtureResolver struct{}
func (*fixtureResolver) NativeMarker(context.Context) (int64,error) { return 7,nil }

func TestGeneratedEventKeepsAssignedField(t *testing.T) {
  original:=ChangedEvent{Id:42}
  data,err:=original.MarshalLuxo()
  if err!=nil {t.Fatal(err)}
  var decoded ChangedEvent
  if err:=decoded.UnmarshalLuxo(data);err!=nil {t.Fatal(err)}
  if decoded.Id!=original.Id {t.Fatalf("event field lost on wire: %d",decoded.Id)}
}

func transactionFixtureDB(t testing.TB) *pg.DB {
  t.Helper()
  ctx:=t.Context()
  container,err:=postgres.Run(ctx,"postgres:17-alpine",postgres.WithDatabase("luxo_test"),postgres.WithUsername("test"),postgres.WithPassword("test"),postgres.BasicWaitStrategies())
  testcontainers.CleanupContainer(t,container)
  if err!=nil { t.Fatal(err) }
  connection,err:=container.ConnectionString(ctx,"sslmode=disable")
  if err!=nil {t.Fatal(err)}
  db,err:=pg.NewDB(ctx,connection)
  if err!=nil {t.Fatal(err)}
  t.Cleanup(db.Close)
  if _,err:=pg.Exec(ctx,db,"CREATE TABLE users(id bigint PRIMARY KEY,name text NOT NULL); INSERT INTO users VALUES(1,'original')");err!=nil {t.Fatal(err)}
  return db
}

func TestGeneratedTransactions(t *testing.T) {
  ctx:=t.Context()
  db:=transactionFixtureDB(t)
  app:=NewFromDB(db)
  app.Resolver=&fixtureResolver{}
  app.Queue=queue.NewMemoryQueue(queue.DefaultConfig())
  app.EventBus=event.NewChanBus(8)
  t.Cleanup(func(){_=app.Queue.Close();app.EventBus.Close()})
  for _,tc:=range []struct{name string; handler api.HandlerFunc; want string; wantError bool}{
    {"rollback",handleRollbackUser(app),"original",true},
    {"nested",handleNestedRollback(app),"original",true},
    {"commit",handleCommitUser(app),"committed",false},
  } {
    t.Run(tc.name,func(t *testing.T){
      req:=&api.Request{Params:map[string]json.RawMessage{"id":json.RawMessage("1")},Buf:&api.ResponseBuf{}}
      err:=tc.handler(ctx,req)
      if tc.wantError && !errors.Is(err,luxerrors.NotFound) {t.Fatalf("error=%v",err)}
      if !tc.wantError && err!=nil {t.Fatal(err)}
      value,err:=db.QueryScalar[string](ctx,"SELECT name FROM users WHERE id=1")
      if err!=nil || value!=tc.want {t.Fatalf("write escaped transaction: name=%q err=%v want=%q",value,err,tc.want)}
    })
  }
  if err:=app.Tx(ctx,func(tx *App) error {
    if tx==app || tx.DB==app.DB || tx.User==app.User {t.Fatal("transaction did not rebind model clients")}
    if tx.Resolver!=app.Resolver || tx.Queue!=app.Queue || tx.EventBus!=app.EventBus {t.Error("transaction lost injected application dependencies")}
    return nil
  });err!=nil {t.Fatal(err)}
  testTransactionLoaders(t,app)
  testConcurrentTransactionLoaders(t,app)
  testCustomTransactionFactory(t,app)
  testFactoryRejectsModifiedResult(t,app)
}

func testFactoryRejectsModifiedResult(t *testing.T,app *App) {
  t.Helper()
  configured:=NewDefaultLoaders(app,dataloader.DefaultConfig())
  app.SetLoaders(configured.WithTransactionFactory(func(tx *App)Loaders {
    child:=NewDefaultLoaders(tx,dataloader.DefaultConfig())
    child.PostUser=configured.PostUser
    return child
  }))
  called:=false
  err:=app.Tx(t.Context(),func(*App)error{called=true;return nil})
  if err==nil || called {t.Fatalf("factory returned stale binding: err=%v called=%v",err,called)}
}

func testCustomTransactionFactory(t *testing.T,app *App) {
  t.Helper()
  cfg:=dataloader.DefaultConfig()
  cfg.MaxBatch=1
  parent:=NewDefaultLoaders(app,cfg)
  calls:=0
  custom:=(Loaders{PostUser:parent.PostUser}).WithTransactionFactory(func(tx *App) Loaders {
    calls++
    if tx==app || tx.DB==app.DB {t.Fatal("custom factory received outer app")}
    bound:=NewDefaultLoaders(tx,cfg)
    return Loaders{PostUser:bound.PostUser}
  })
  app.SetLoaders(custom)
  ctx:=t.Context()
  err:=app.Tx(ctx,func(tx *App)error {
    if _,err:=pg.Exec(ctx,tx.DB,"INSERT INTO users VALUES(3,'custom uncommitted')");err!=nil{return err}
    return tx.Tx(ctx,func(inner *App)error {
      user,err:=inner.loaders.PostUser.Load(ctx,3,[]*selection.Field{{Name:"id"},{Name:"name"}})
      if err!=nil{return err}
      if user==nil || user.Name!="custom uncommitted" || inner.loaders.PostUser==parent.PostUser {t.Fatal("custom loader lost transaction binding")}
      return luxerrors.NotFound
    })
  })
  if !errors.Is(err,luxerrors.NotFound) || calls!=2 {t.Fatalf("factory calls=%d err=%v",calls,err)}
  count,err:=app.DB.QueryScalar[int64](ctx,"SELECT count(*) FROM users WHERE id=3")
  if err!=nil || count!=0 {t.Fatalf("custom loader transaction did not roll back: count=%d err=%v",count,err)}
  if app.loaders.PostUser!=parent.PostUser {t.Fatal("custom transaction replaced parent loaders")}
}

func testTransactionLoaders(t *testing.T,app *App) {
  t.Helper()
  ctx:=t.Context()
  app.SetLoaders(NewDefaultLoaders(app,dataloader.DefaultConfig()))
  original:=app.loaders.PostUser
  fields:=[]*selection.Field{{Name:"id"},{Name:"name"}}
  err:=app.Tx(ctx,func(tx *App) error {
    if _,err:=pg.Exec(ctx,tx.DB,"INSERT INTO users VALUES(2,'uncommitted')");err!=nil {return err}
    user,err:=tx.loaders.PostUser.Load(ctx,2,fields)
    if err!=nil {return err}
    if user==nil || user.Name!="uncommitted" {t.Errorf("loader cannot see transaction write: %#v",user)}
    if tx.loaders.PostUser==original {t.Error("transaction reused outer loader")}
    if err:=tx.Tx(ctx,func(inner *App) error {
      users,err:=inner.loaders.PostUser.LoadAll(ctx,[]int64{2},fields)
      if err!=nil {return err}
      if users[2]==nil || users[2].Name!="uncommitted" {t.Error("nested loader lost outer uncommitted write")}
      return nil
    });err!=nil {return err}
    return luxerrors.NotFound
  })
  if !errors.Is(err,luxerrors.NotFound) {t.Fatal(err)}
  if app.loaders.PostUser!=original {t.Error("transaction changed parent loaders")}
  user,err:=app.loaders.PostUser.Load(ctx,2,fields)
  if err!=nil || user!=nil {t.Fatalf("rolled back loader result escaped: %#v %v",user,err)}
}

func testConcurrentTransactionLoaders(t *testing.T,app *App) {
  t.Helper()
  ctx:=t.Context()
  done:=make(chan error,4)
  abort:=errors.New("rollback concurrent fixture")
  fields:=[]*selection.Field{{Name:"id"},{Name:"name"}}
  for id:=int64(10);id<14;id++ {
    go func(){
      err:=app.Tx(ctx,func(tx *App) error {
        if _,err:=pg.Exec(ctx,tx.DB,"INSERT INTO users VALUES($1,'parallel')",id);err!=nil {return err}
        user,err:=tx.loaders.PostUser.Load(ctx,id,fields)
        if err!=nil {return err}
        if user==nil || user.Id!=id || user.Name!="parallel" {return errors.New("transaction loader crossed connections")}
        return abort
      })
      if errors.Is(err,abort) {err=nil}
      done<-err
    }()
  }
  for range 4 {if err:=<-done;err!=nil {t.Error(err)}}
  count,err:=app.DB.QueryScalar[int64](ctx,"SELECT count(*) FROM users WHERE id>=10")
  if err!=nil || count!=0 {t.Fatalf("concurrent rollback retained rows: %d %v",count,err)}
}

func TestGeneratedTransactionalReadMethods(t *testing.T) {
  db:=transactionFixtureDB(t)
    app:=NewFromDB(db)
    err:=app.Tx(t.Context(),func(tx *App)error {
      if _,err:=tx.User.Where(UserWhere.Id.Eq(1)).Update(t.Context(),lux.SetField{Col:"name",Val:"uncommitted"});err!=nil {return err}
      if err:=tx.Tx(t.Context(),func(inner *App)error {
        rows,total,err:=inner.User.Where().Limit(1).AllWithCount(t.Context())
        if err!=nil {return err}
        if len(rows)!=1 || total!=1 || rows[0].Name!="uncommitted" {t.Fatal("pagination lost transaction binding")}
        type summary struct { Name string; Total int64 }
        groups,err:=inner.User.Where().GroupBy(t.Context(),[]string{"name"},[]lux.GroupAgg{{Col:"name"},{Fn:"COUNT",Alias:"total"}},func(rows pg.Rows)(*summary,error){
          var value summary
          if err:=rows.Scan(&value.Name,&value.Total);err!=nil{return nil,err}
          return &value,nil
        })
        if err!=nil {return err}
        if len(groups)!=1 || groups[0].Name!="uncommitted" || groups[0].Total!=1 {t.Fatal("aggregation lost transaction binding")}
        return nil
      });err!=nil {return err}
      return luxerrors.NotFound
    })
    if !errors.Is(err,luxerrors.NotFound) {t.Fatal(err)}
    user,err:=app.User.Find(t.Context(),1)
    if err!=nil || user==nil || user.Name!="original" {t.Fatalf("rollback failed: %#v %v",user,err)}
}

func BenchmarkTransactionLoaders(b *testing.B) {
  db:=transactionFixtureDB(b)
  ctx:=b.Context()
  fields:=[]*selection.Field{{Name:"id"},{Name:"name"}}
  for _,name:=range []string{"model-no-loaders","model-bound-loaders","bound-loader-read"} {
    b.Run(name,func(b *testing.B){
      app:=NewFromDB(db)
      if name!="model-no-loaders" {app.SetLoaders(NewDefaultLoaders(app,dataloader.DefaultConfig()))}
      operation:=func(tx *App) error {
        var user *User
        var err error
        if name=="bound-loader-read" {user,err=tx.loaders.PostUser.Load(ctx,1,fields)} else {
          user,err=tx.User.Where(UserWhere.Id.Eq(1)).First(ctx)
        }
        if err!=nil {return err}
        if user==nil || user.Id!=1 || user.Name!="original" {return errors.New("invalid transaction response")}
        return nil
      }
      if err:=app.Tx(ctx,operation);err!=nil {b.Fatal(err)}
      b.ReportAllocs()
      for b.Loop() {if err:=app.Tx(ctx,operation);err!=nil {b.Fatal(err)}}
    })
  }
}
`
