package codegen

import (
	"errors"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestGeneratedOrdinaryIs(t *testing.T) {
	runGeneratedFixture(t, `
interface Named { name: String }
model Entity @noTime { id: Int @id }
model Left : Entity {}
model Right : Entity {}
model User : Left, Right, Named @noTime { name: String }
model Other @noTime { id: Int @id }
type Payload { value: Int }
enum Role { ON OFF }
sealed Outcome { Ready() }
fn matchesModel(value: User?): Boolean { return value is User }
fn parent(value: User?): Boolean { return value is Entity }
fn named(value: User?): Boolean { return value is Named }
fn other(value: User?): Boolean { return value is Other }
fn identity(left: User, right: User): Boolean { return left is User && right is User && left == right }
fn literal(): Boolean { return User { id: 1, name: "user" } is User }
fn date(value: DateTime): Boolean { return value is DateTime }
fn optionalDate(value: DateTime?): Boolean { return value is DateTime }
fn number(value: Decimal): Boolean { return value is Decimal }
fn optionalNumber(value: Decimal?): Boolean { return value is Decimal }
fn different(value: Int): Boolean { return value is String }
fn empty(): Boolean { return null is User }
fn list(value: [Int]): Boolean { return value is Int }
fn emptyList(): Boolean { return [] is Int }
fn object(value: Payload?): Boolean { return value is Payload }
fn choice(value: Role): Boolean { return value is Role }
fn sum(value: Outcome): Boolean { return value is Outcome }
fn shadow(User: Int, value: User): Boolean { return value is User }
fn queried(id: Int): Boolean { return User.find(id: id) is User }
api test(value: Int): Boolean { return value is Int }
`, ordinaryIsAssertions, StableIDs{ModelFields: map[string]map[string]int{
		"Entity": {"id": 1}, "Left": {"id": 1}, "Right": {"id": 1}, "User": {"id": 1, "name": 2}, "Other": {"id": 1},
		"Payload": {"value": 1}, "Outcome": {"Ready": 1}, "Outcome.Ready": {},
	}})
}

const ordinaryIsAssertions = `package generated
import("testing";"time";"github.com/shopspring/decimal")
func TestOrdinaryIs(t *testing.T){
 app:=&App{};ctx:=t.Context();user:=&User{Id:1,Name:"user"};copy:=&User{Id:1,Name:"user"}
 for _,value:=range []*User{nil,user}{
  for _,call:=range []func() (bool,error){func()(bool,error){return app.matchesModel(ctx,value)},func()(bool,error){return app.parent(ctx,value)},func()(bool,error){return app.named(ctx,value)}}{
   got,err:=call();if err!=nil||got!=(value!=nil){t.Fatal("model type",got,err)}
  }
  if got,err:=app.other(ctx,value);err!=nil||got{t.Fatal("different model",got,err)}
 }
 for _,right:=range []*User{user,copy}{got,err:=app.identity(ctx,user,right);if err!=nil||got!=(user==right){t.Fatal("identity",got,err)}}
 now:=time.Now();number:=decimal.New(123,-2);payload:=Payload{Value:1}
 for _,call:=range []func()(bool,error){func()(bool,error){return app.literal(ctx)},func()(bool,error){return app.date(ctx,now)},func()(bool,error){return app.optionalDate(ctx,&now)},func()(bool,error){return app.number(ctx,number)},func()(bool,error){return app.optionalNumber(ctx,&number)},func()(bool,error){return app.object(ctx,&payload)},func()(bool,error){return app.choice(ctx,RoleON)},func()(bool,error){return app.sum(ctx,(Outcome{}).NewVariant1())},func()(bool,error){return app.shadow(ctx,9,user)}}{
  if got,err:=call();err!=nil||!got{t.Fatal("matching type",got,err)}
 }
 for _,call:=range []func()(bool,error){func()(bool,error){return app.optionalDate(ctx,nil)},func()(bool,error){return app.optionalNumber(ctx,nil)},func()(bool,error){return app.object(ctx,nil)},func()(bool,error){return app.different(ctx,1)},func()(bool,error){return app.empty(ctx)},func()(bool,error){return app.list(ctx,[]int64{1})},func()(bool,error){return app.emptyList(ctx)}}{
  if got,err:=call();err!=nil||got{t.Fatal("nonmatching type",got,err)}
 }
}
`

func TestGenerateOrdinaryIsRejectsInvalidSource(t *testing.T) {
	for _, expression := range []string{`value is 1`, `value is missing`, `value is value`, `Int is Int`} {
		t.Run(expression, func(t *testing.T) {
			input := analyzePermissionFixture(t, `fn test(value: Int): Boolean { return `+expression+` }`)
			if len(input.Errors) == 0 {
				t.Fatal("invalid type test passed semantic analysis")
			}
			generated, err := mustNewGenerator(t, GeneratorConfig{}).Generate(input, "generated", nil)
			if err == nil || generated != nil || !strings.Contains(err.Error(), "successful semantic analysis") {
				t.Fatalf("invalid source emitted a partial result: generated=%v error=%v", generated != nil, err)
			}
		})
	}
}

func TestGenerateOrdinaryIsRequiresAnalyzedOperands(t *testing.T) {
	for _, operand := range []string{"target", "subject"} {
		t.Run(operand, func(t *testing.T) {
			input := analyzePermissionFixture(t, `fn test(value: Int): Boolean { return value is Int }`)
			if len(input.Errors) != 0 {
				t.Fatal(input.Errors)
			}
			statement := input.Files[0].Functions[0].Body.Stmts[0].(*ast.ReturnStmt)
			expression := statement.Value.(*ast.BinaryExpr)
			if operand == "target" {
				expression.Right.SetTypeTag("")
			} else {
				expression.Left.SetTypeTag("")
			}
			generated, err := mustNewGenerator(t, GeneratorConfig{}).Generate(input, "generated", nil)
			var failure *InternalCompilerError
			if generated != nil || !errors.As(err, &failure) || !strings.Contains(failure.Detail, "type test requires analyzed") {
				t.Fatalf("missing metadata escaped the controlled boundary: generated=%v error=%v", generated != nil, err)
			}
		})
	}
}

func TestGeneratedOrdinaryIsEvaluation(t *testing.T) {
	runGeneratedFixture(t, `
fn next(value: Int): Result<Int?> @native
fn matching(): Boolean { return next(1)? is Int }
fn different(): Boolean { return next(1)? is String }
fn shortCircuit(enabled: Boolean): Boolean { return enabled && next(1)? is Int }
`, ordinaryIsEvaluationAssertions, StableIDs{})
}

const ordinaryIsEvaluationAssertions = `package generated
import("testing";"context";"errors")
var failure=errors.New("type-test failed")
type typeResolver struct{calls int;value *int64;fail bool}
func(r *typeResolver) Next(_ context.Context,value int64)(*int64,error){r.calls++;if r.fail{return nil,failure};return r.value,nil}
func TestOrdinaryIsEvaluation(t *testing.T){
 value:=int64(7)
 for _,sample:=range []struct{value *int64;fail bool}{{&value,false},{nil,false},{nil,true}}{
  for _,different:=range []bool{false,true}{
   resolver:=&typeResolver{value:sample.value,fail:sample.fail};app:=&App{Resolver:resolver};got,err:=false,error(nil)
   if different{got,err=app.different(t.Context())}else{got,err=app.matching(t.Context())}
   if resolver.calls!=1{t.Fatal("left operand did not execute exactly once",resolver.calls)}
   if sample.fail{if !errors.Is(err,failure){t.Fatal("left error lost",err)}}else if err!=nil||got!=(!different&&sample.value!=nil){t.Fatal(got,err)}
  }
 }
 resolver:=&typeResolver{fail:true};app:=&App{Resolver:resolver}
 if got,err:=app.shortCircuit(t.Context(),false);err!=nil||got||resolver.calls!=0{t.Fatal("short circuit",got,err,resolver.calls)}
 if _,err:=app.shortCircuit(t.Context(),true);!errors.Is(err,failure)||resolver.calls!=1{t.Fatal("short circuit error",err,resolver.calls)}
}
`
