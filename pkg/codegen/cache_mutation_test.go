package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestGeneratedAppDoesNotExposeReliableCacheInfrastructure(t *testing.T) {
	model := testModel("User", nil, []*ast.FieldDecl{
		testField("id", "Int", &ast.Directive{Name: "id"}), testField("name", "String"),
	})
	input := result(&ast.File{Models: []*ast.ModelDecl{model}})
	app := generateAppFile(input, "generated", nil, DriverPG)
	db := defaultGenerator().generateDBFile(input, "generated", nil)
	for _, source := range []string{string(app), string(db)} {
		for _, forbidden := range []string{"NewWithCacheOutbox", "CacheModelBindings", "cache/outbox", "CacheWriterStage"} {
			if strings.Contains(source, forbidden) {
				t.Fatalf("TTL app still exports reliable invalidation infrastructure: %s", forbidden)
			}
		}
	}
	if !strings.Contains(string(app), "func NewFromDB(") || !strings.Contains(string(app), "func (a *App) Tx(") {
		t.Fatal("cache cleanup removed ordinary application or transaction construction")
	}
}

// App/Client code is generated and compiled, not checked only by source text.
const generatedMutationAssertions = `
func TestGeneratedMutationsWithoutCacheInfrastructure(t *testing.T) {
  db:=transactionFixtureDB(t)
  app:=NewFromDB(db)
  if app.User.db!=db || app.Post.db!=db {t.Fatal("model clients replaced explicit database")}
  user,err:=app.User.Create().SetId(2).SetName("created").Exec(t.Context())
  if err!=nil || user==nil || user.Id!=2 {t.Fatalf("create: %v %v",user,err)}
  rows,err:=app.User.CreateMany().Add(UserCreateInput{Id:3,Name:"three"}).Add(UserCreateInput{Id:4,Name:"four"}).Exec(t.Context())
  if err!=nil || len(rows)!=2 {t.Fatalf("batch: %v %v",rows,err)}
  // Ordinary writes and nested transactions do not require cache metadata.
  err=app.Tx(t.Context(),func(tx *App)error {
    if _,err:=tx.User.Where(UserWhere.Id.Eq(2)).Update(t.Context(),lux.SetField{Col:"name",Val:"updated"});err!=nil {return err}
    return tx.Tx(t.Context(),func(child *App)error {
      if _,err:=child.User.Where(UserWhere.Id.Eq(3)).Delete(t.Context());err!=nil {return err}
      _,err:=child.User.Where(UserWhere.Id.Eq(4)).Delete(t.Context())
      return err
    })
  })
  if err!=nil {t.Fatal(err)}
  testGeneratedMutationHandlers(t,app)
  user,err=app.User.Find(t.Context(),2)
  if err!=nil || user==nil || user.Name!="updated" {t.Fatalf("nested writes lost committed update: %v %v",user,err)}
  n,err:=db.QueryScalar[int64](t.Context(),"SELECT count(*) FROM users WHERE id IN (3,4)")
  if err!=nil || n!=0 {t.Fatalf("nested deletions not committed: %d %v",n,err)}
  assertNoCacheInfrastructure(t,db)
}

func testGeneratedMutationHandlers(t *testing.T,app *App) {
  t.Helper()
  for _,tc:=range []struct{handler api.HandlerFunc; want string; fail bool}{
    {handleRollbackUser(app),"original",true},
    {handleNestedRollback(app),"original",true},
    {handleCommitUser(app),"committed",false},
  } {
    req:=&api.Request{Params:map[string]json.RawMessage{"id":json.RawMessage("1")},Buf:&api.ResponseBuf{}}
    err:=tc.handler(t.Context(),req)
    if tc.fail && !errors.Is(err,luxerrors.NotFound) || !tc.fail && err!=nil {t.Fatalf("compiled API: %v",err)}
    user,err:=app.User.Find(t.Context(),1)
    if err!=nil || user==nil || user.Name!=tc.want {t.Fatalf("compiled transaction escaped: %v %v",user,err)}
  }
}
`
