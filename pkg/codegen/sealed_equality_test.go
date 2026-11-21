package codegen

import (
	"strings"
	"testing"
)

const sealedEqualitySource = `model User @noTime { id: Int @id values: [Int] }
type Plain { value: Int }
sealed Outcome { Success(value: Int, at: DateTime? = null, amount: Decimal? = null) Model(user: User) Snapshot(value: Plain) Pending }
fn equal(left: Outcome, right: Outcome): Boolean { return left == right }
fn different(left: Outcome, right: Outcome): Boolean { return left != right }
fn optional(left: Outcome?, right: Outcome?): Boolean { return left == right }
fn mixed(left: Outcome, right: Outcome?): Boolean { return left == right }
`

func TestGeneratedSealedValueEquality(t *testing.T) {
	runGeneratedFixture(t, sealedEqualitySource, sealedEqualityAssertions, sealedEqualityIDs())
}

func TestGeneratedSealedNullAndTypePatterns(t *testing.T) {
	runGeneratedFixture(t, sealedEqualitySource+`
fn absent(value: Outcome?): Boolean { return value == null }
fn present(value: Outcome?): Boolean { return null != value }
fn neverNull(value: Outcome): Boolean { return value == null }
fn optionalType(value: Outcome?): Int { return when(value) { is Pending -> 1 null -> 2 else -> 3 } }
fn complete(value: Outcome): Int {
 return when(value) { null -> 6 Outcome.Success(7) -> 1 is Success -> 2 is Model -> 3 is Snapshot -> 4 is Pending -> 5 }
}
`, `package generated
import "testing"
func TestSealedNullAndTypePatterns(t *testing.T){
 app:=&App{};pending:=(Outcome{}).NewVariant4();success:=(Outcome{}).NewVariant1(7,nil,nil)
 for _,sample:=range []struct{value *Outcome;absent bool;pattern int64}{{nil,true,2},{&pending,false,1},{&success,false,3}}{
  value,err:=app.absent(t.Context(),sample.value);if err!=nil||value!=sample.absent{t.Fatal("null equality",value,err)}
  value,err=app.present(t.Context(),sample.value);if err!=nil||value==sample.absent{t.Fatal("reversed inequality",value,err)}
  pattern,err:=app.optionalType(t.Context(),sample.value);if err!=nil||pattern!=sample.pattern{t.Fatal("nullable type pattern",pattern,err)}
 }
 if value,err:=app.neverNull(t.Context(),pending);err!=nil||value{t.Fatal("non-null comparison",value,err)}
 for _,sample:=range []struct{value Outcome;want int64}{{success,1},{(Outcome{}).NewVariant1(8,nil,nil),2},{pending,5}}{
  value,err:=app.complete(t.Context(),sample.value);if err!=nil||value!=sample.want{t.Fatal("exhaustive patterns",value,err)}
 }
 for _,bad:=range []Outcome{{},{_tag:1}}{
  if _,err:=app.absent(t.Context(),&bad);err==nil{t.Fatal("invalid optional null comparison")}
  if _,err:=app.present(t.Context(),&bad);err==nil{t.Fatal("invalid reversed null comparison")}
  if _,err:=app.neverNull(t.Context(),bad);err==nil{t.Fatal("invalid non-null comparison")}
  if _,err:=app.complete(t.Context(),bad);err==nil{t.Fatal("invalid exhaustive pattern")}
 }
}
`, sealedEqualityIDs())
}

func TestGeneratedSealedAcyclicEqualityAvoidsGraphState(t *testing.T) {
	input := analyzePermissionFixture(t, `sealed Inner { Value(value: Int) Empty }
sealed Outer { Value(value: Inner, optional: Inner?) Empty }
fn equal(left: Outer, right: Outer): Boolean { return left == right }
`)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	ids := StableIDs{ModelFields: map[string]map[string]int{
		"Inner": {"Value": 1, "Empty": 2}, "Inner.Value": {"value": 1},
		"Outer": {"Value": 1, "Empty": 2}, "Outer.Value": {"value": 1, "optional": 2},
	}}
	generated, err := mustNewGenerator(t, GeneratorConfig{Driver: DriverPG, IDs: ids}).Generate(input, "generated", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(generated.Files["sealed.gen.go"]), "_luxoSealedEquality") {
		t.Fatal("acyclic sealed equality allocated a graph-state type")
	}
	runGeneratedCompilation(t, generated, `package generated
import "testing"
func TestAcyclicValueEquality(t *testing.T){
 app:=&App{};a,b:=(Inner{}).NewVariant1(7),(Inner{}).NewVariant1(7)
 left,right:=(Outer{}).NewVariant1(a,&a),(Outer{}).NewVariant1(b,&b)
 if equal,err:=app.equal(t.Context(),left,right);err!=nil||!equal{t.Fatal(equal,err)}
 right=(Outer{}).NewVariant1((Inner{}).NewVariant1(8),&b)
 if equal,err:=app.equal(t.Context(),left,right);err!=nil||equal{t.Fatal(equal,err)}
 if equal,err:=app.equal(t.Context(),left,(Outer{}).NewVariant1(a,nil));err!=nil||equal{t.Fatal(equal,err)}
 if _,err:=app.equal(t.Context(),left,(Outer{}).NewVariant1(a,&Inner{}));err==nil{t.Fatal("invalid child accepted")}
}
func BenchmarkSealedAcyclicEquality(b *testing.B){
 app:=&App{};a,z:=(Inner{}).NewVariant1(7),(Inner{}).NewVariant1(7);left,right:=(Outer{}).NewVariant1(a,&a),(Outer{}).NewVariant1(z,&z)
 b.ReportAllocs();for range b.N{equal,err:=app.equal(b.Context(),left,right);if err!=nil||!equal{b.Fatal(equal,err)}}
}
`)
}

func sealedEqualityIDs() StableIDs {
	return StableIDs{ModelFields: map[string]map[string]int{
		"User": {"id": 1, "values": 2}, "Plain": {"value": 1},
		"Outcome":         {"Success": 1, "Model": 2, "Snapshot": 3, "Pending": 4},
		"Outcome.Success": {"value": 1, "at": 2, "amount": 3}, "Outcome.Model": {"user": 1}, "Outcome.Snapshot": {"value": 1},
	}}
}

const sealedEqualityAssertions = `package generated
import("testing";"time";"github.com/shopspring/decimal")
func TestSealedActiveValueEquality(t *testing.T){
 app:=&App{};a:=(Outcome{}).NewVariant1(7,nil,nil);same:=(Outcome{}).NewVariant1(7,nil,nil);different:=(Outcome{}).NewVariant1(8,nil,nil);pending:=(Outcome{}).NewVariant4()
 user:=&User{Id:1,Values:[]int64{1}};other:=&User{Id:1,Values:[]int64{1}}
 at:=time.Unix(123,456).UTC();zone:=at.In(time.FixedZone("offset",3600));amount:=decimal.RequireFromString("1.0");scale:=decimal.RequireFromString("1.00")
 for _,pair:=range []struct{left,right Outcome;want bool}{
  {a,same,true},{a,different,false},{a,pending,false},{pending,(Outcome{}).NewVariant4(),true},
  {(Outcome{}).NewVariant2(user),(Outcome{}).NewVariant2(user),true},{(Outcome{}).NewVariant2(user),(Outcome{}).NewVariant2(other),false},
  {(Outcome{}).NewVariant3(Plain{Value:7}),(Outcome{}).NewVariant3(Plain{Value:7}),true},
  {(Outcome{}).NewVariant1(7,&at,&amount),(Outcome{}).NewVariant1(7,&zone,&scale),true},
 }{equal,err:=app.equal(t.Context(),pair.left,pair.right);if err!=nil||equal!=pair.want{t.Fatal("equal",pair,equal,err)};reverse,err:=app.equal(t.Context(),pair.right,pair.left);if err!=nil||reverse!=pair.want{t.Fatal("asymmetric",reverse,err)};different,err:=app.different(t.Context(),pair.left,pair.right);if err!=nil||different==pair.want{t.Fatal("different",different,err)}}
 for _,pair:=range []struct{left,right *Outcome;want bool}{{nil,nil,true},{&a,&same,true},{&a,nil,false},{nil,&a,false}}{equal,err:=app.optional(t.Context(),pair.left,pair.right);if err!=nil||equal!=pair.want{t.Fatal("nullable",equal,err)}}
 if equal,err:=app.mixed(t.Context(),a,&same);err!=nil||!equal{t.Fatal("mixed",equal,err)}
 for _,bad:=range []Outcome{{},{_tag:1}}{if _,err:=app.equal(t.Context(),bad,a);err==nil{t.Fatal("invalid holder accepted")};if _,err:=app.optional(t.Context(),nil,&bad);err==nil{t.Fatal("invalid nullable holder accepted")}}
}
func BenchmarkSealedValueEquality(b *testing.B){
 app:=&App{};left:=(Outcome{}).NewVariant1(7,nil,nil)
 for _,sample:=range []struct{name string;right Outcome;failed bool}{{"leaf",(Outcome{}).NewVariant1(7,nil,nil),false},{"different-case",(Outcome{}).NewVariant4(),false},{"invalid",Outcome{},true}}{
  b.Run(sample.name,func(b *testing.B){b.ReportAllocs();for range b.N{_,err:=app.equal(b.Context(),left,sample.right);if (err!=nil)!=sample.failed{b.Fatal(err)};if err!=nil{_ = err.Error()}}})
 }
}
`

func TestGeneratedSealedEqualityClosesRecursiveGraphs(t *testing.T) {
	runGeneratedFixture(t, `sealed Node { Link(value: Int, left: Node?, right: Node?) End }
sealed Left { Link(value: Int, next: Right?) End }
sealed Right { Link(value: Int, next: Left?) End }
sealed Real { Value(value: Float) }
sealed left { Link(value: Int, next: left?) End }
fn equal(left: Node, right: Node): Boolean { return left == right }
fn equalLeft(left: Left, right: Left): Boolean { return left == right }
fn equalReal(left: Real, right: Real): Boolean { return left == right }
fn equalLower(a: left, b: left): Boolean { return a == b }
`, sealedRecursiveEqualityAssertions, StableIDs{ModelFields: map[string]map[string]int{
		"Node": {"Link": 1, "End": 2}, "Node.Link": {"value": 1, "left": 2, "right": 3},
		"Left": {"Link": 1, "End": 2}, "Left.Link": {"value": 1, "next": 2},
		"Right": {"Link": 1, "End": 2}, "Right.Link": {"value": 1, "next": 2},
		"Real": {"Value": 1}, "Real.Value": {"value": 1},
		"left": {"Link": 1, "End": 2}, "left.Link": {"value": 1, "next": 2},
	}})
}

const sealedRecursiveEqualityAssertions = `package generated
import("math";"testing")
func lowerSealedFixture()left{return (left{}).NewVariant1(7,nil)}
func TestSealedRecursiveEquality(t *testing.T){
 app:=&App{};left:=(Node{}).NewVariant1(7,nil,nil);right:=(Node{}).NewVariant1(7,nil,nil);left.AsVariant1().F2=&left;right.AsVariant1().F2=&right
 check:=func(a,b Node,want bool){t.Helper();equal,err:=app.equal(t.Context(),a,b);if err!=nil||equal!=want{t.Fatal(equal,err,want)};reverse,err:=app.equal(t.Context(),b,a);if err!=nil||reverse!=want{t.Fatal("asymmetric",reverse,err)}}
 check(left,right,true);right.AsVariant1().F1=8;check(left,right,false);right.AsVariant1().F1=7;check(left,right,true)
 shared:=(Node{}).NewVariant1(9,nil,nil);a:=(Node{}).NewVariant1(7,&shared,&shared);x:=(Node{}).NewVariant1(9,nil,nil);y:=(Node{}).NewVariant1(9,nil,nil);b:=(Node{}).NewVariant1(7,&x,&y)
 check(a,b,true);y.AsVariant1().F1=10;check(a,b,false);y.AsVariant1().F1=9;check(a,b,true)
 nodesA,nodesB:=make([]Node,4096),make([]Node,4096);for i:=range nodesA{nodesA[i]=(Node{}).NewVariant1(int64(i),nil,nil);nodesB[i]=(Node{}).NewVariant1(int64(i),nil,nil)};for i:=range nodesA{nodesA[i].AsVariant1().F2=&nodesA[(i+1)%len(nodesA)];nodesB[i].AsVariant1().F2=&nodesB[(i+1)%len(nodesB)]};check(nodesA[0],nodesB[0],true)
 bad:=(Node{}).NewVariant1(7,&Node{},nil);if _,err:=app.equal(t.Context(),bad,bad);err==nil{t.Fatal("invalid descendant accepted")};if _,err:=app.equal(t.Context(),(Node{}).NewVariant1(7,nil,nil),bad);err==nil{t.Fatal("invalid descendant hidden by null")}
 l1,l2:=(Left{}).NewVariant1(1,nil),(Left{}).NewVariant1(1,nil);r1,r2:=(Right{}).NewVariant1(2,&l1),(Right{}).NewVariant1(2,&l2);l1.AsVariant1().F2=&r1;l2.AsVariant1().F2=&r2
 equal,err:=app.equalLeft(t.Context(),l1,l2);if err!=nil||!equal{t.Fatal("mutual recursion",equal,err)};r2.AsVariant1().F1=3;equal,err=app.equalLeft(t.Context(),l1,l2);if err!=nil||equal{t.Fatal("mutual mismatch",equal,err)}
 real:=(Real{}).NewVariant1(math.NaN());if equal,err:=app.equalReal(t.Context(),real,real);err!=nil||equal{t.Fatal("float rule lost",equal,err)}
 lowerA,lowerB:=lowerSealedFixture(),lowerSealedFixture();lowerA.AsVariant1().F2=&lowerA;lowerB.AsVariant1().F2=&lowerB;if equal,err:=app.equalLower(t.Context(),lowerA,lowerB);err!=nil||!equal{t.Fatal("lowercase owner",equal,err)}
}
func BenchmarkSealedRecursiveEquality(b *testing.B){
 app:=&App{};left,right:=make([]Node,32),make([]Node,32);for i:=range left{left[i]=(Node{}).NewVariant1(int64(i),nil,nil);right[i]=(Node{}).NewVariant1(int64(i),nil,nil)};for i:=range left{left[i].AsVariant1().F2=&left[(i+1)%len(left)];right[i].AsVariant1().F2=&right[(i+1)%len(right)]}
 a,z:=(Node{}).NewVariant1(7,&left[0],&left[0]),(Node{}).NewVariant1(7,&right[0],&right[0])
 bad:=(Node{}).NewVariant1(7,&Node{},nil)
 for _,sample:=range []struct{name string;left,right Node;failed bool}{{"cycle32",left[0],right[0],false},{"shared",a,z,false},{"invalid-child",bad,bad,true}}{
  b.Run(sample.name,func(b *testing.B){b.ReportAllocs();for range b.N{equal,err:=app.equal(b.Context(),sample.left,sample.right);if (err!=nil)!=sample.failed||!sample.failed&&!equal{b.Fatal(equal,err)};if err!=nil{_ = err.Error()}}})
 }
}
`

func TestGeneratedSealedWhenUsesValueEqualityLazily(t *testing.T) {
	runGeneratedFixture(t, sealedEqualitySource+`
fn candidate(): Result<Outcome> @native
fn otherCandidate(): Result<Outcome> @native
fn match(value: Outcome): Int {
 return when(value) { is Snapshot -> 3 candidate()? -> 1 otherCandidate()? -> 2 else -> 0 }
}
fn optionalMatch(value: Outcome?): Int { return when(value) { null -> 4 Outcome.Pending() -> 2 else -> 0 } }
fn impossibleNull(value: Outcome): Int { return when(value) { null -> 1 else -> 0 } }
`, sealedWhenEqualityAssertions, sealedEqualityIDs())
}

const sealedWhenEqualityAssertions = `package generated
import("context";"errors";"testing")
type sealedPatternResolver struct{first,second int;failure error}
func(r *sealedPatternResolver)Candidate(context.Context)(Outcome,error){r.first++;return (Outcome{}).NewVariant1(7,nil,nil),r.failure}
func(r *sealedPatternResolver)OtherCandidate(context.Context)(Outcome,error){r.second++;return (Outcome{}).NewVariant4(),nil}
func TestSealedWhenValueEquality(t *testing.T){
 resolver:=&sealedPatternResolver{};app:=&App{Resolver:resolver}
 for _,test:=range []struct{value Outcome;want int64;first,second int}{
  {(Outcome{}).NewVariant3(Plain{Value:7}),3,0,0},{(Outcome{}).NewVariant1(7,nil,nil),1,1,0},{(Outcome{}).NewVariant4(),2,1,1},
 }{resolver.first,resolver.second=0,0;got,err:=app.match(t.Context(),test.value);if err!=nil||got!=test.want||resolver.first!=test.first||resolver.second!=test.second{t.Fatal(got,err,resolver,test)}}
 sentinel:=errors.New("candidate failure");resolver.failure=sentinel;if _,err:=app.match(t.Context(),(Outcome{}).NewVariant4());!errors.Is(err,sentinel){t.Fatal("pattern error lost",err)}
 if got,err:=app.optionalMatch(t.Context(),nil);err!=nil||got!=4{t.Fatal(got,err)};pending:=(Outcome{}).NewVariant4();if got,err:=app.optionalMatch(t.Context(),&pending);err!=nil||got!=2{t.Fatal(got,err)}
 if got,err:=app.impossibleNull(t.Context(),pending);err!=nil||got!=0{t.Fatal("non-null pattern",got,err)}
}
`
