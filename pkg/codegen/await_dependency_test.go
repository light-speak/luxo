package codegen

import (
	"strings"
	"testing"
)

func TestAwaitDependencyCreatesOrderedBatches(t *testing.T) {
	source := `fn compute(): Int {
 var result = 0
 await { val first = 40 val second = first + 1 val third = second + 1 result = third }
 return result
}`
	input := analyzePermissionFixture(t, source)
	output, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(output.Files["handler.gen.go"]), "api.NewTaskGroup(ctx)"); got != 3 {
		t.Fatalf("dependent tasks need three ordered batches, got %d", got)
	}
}

func TestGeneratedAwaitDependencyResults(t *testing.T) {
	runGeneratedFixture(t, awaitDependencySource, awaitDependencyAssertions,
		StableIDs{ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}}})
}

func TestAwaitDependencyKeepsQueryFusionWithinBatch(t *testing.T) {
	source := `model Counter @noTime { id: Int @id value: Int }
fn compute(): Int { var result = 0 await {
 val first = 41
 val second = Counter.where(value: first).count()
 val third = Counter.where(value: first).count()
 result = second + third
} return result }`
	input := analyzePermissionFixture(t, source)
	output, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG,
		IDs: StableIDs{ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}}}})
	if err != nil {
		t.Fatal(err)
	}
	body := string(output.Files["handler.gen.go"])
	if strings.Count(body, "api.NewTaskGroup(ctx)") != 2 || !strings.Contains(body, ".AggregateBatch(gctx,") {
		t.Fatal("query dependency or same-batch aggregate fusion was lost", body)
	}
}

func BenchmarkGenerateAwaitDependencies(b *testing.B) {
	benchmarkBindingGeneration(b, []struct{ name, source string }{
		{"control", bindingGenerationControls[0].source},
		{"chain", `fn compute(): Int { var result = 0 await { val first = 40 val second = first + 1 val third = second + 1 result = third } return result }`},
		{"closure", `fn compute(): Int { var result = 0 await { val first = 41 val values = [1].map { first + it } for value in values { result = value } } return result }`},
		{"ordered", `fn compute(): Int { var result = 0 await { val first = 41 result = first val second = result + 1 result = second } return result }`},
	})
}

const awaitDependencySource = `
model Counter @noTime { id: Int @id value: Int }
fn rendezvous(index: Int): Result<Int> @native
fn first(mode: Int): Result<Int> @native
fn next(value: Int): Result<Int> @native
fn chain(): Int { var result = 0 await { val first = 40 val second = first + 1 val third = second + 1 result = third } return result }
fn shadow(value: Int): Int { var result = 0 await { val value = value + 1 val second = value + 1 result = second } return result }
fn nullable(): Int { var result = 0 await { val first: Int? = 41 val second = (first ?: 0) + 1 result = second } return result }
fn closure(): Int { var result = 0 await { val first = 41 val values = [1].map { first + it } for value in values { result = value } } return result }
fn ordered(): Int { var result = 0 await { val first = 41 result = first val second = result + 1 result = second } return result }
fn guarded(mode: Int): Int { var result = 0 await { val value = first(mode)? val second = next(value)? result = second } return result }
fn parallel(): Int { var result = 0 await { val first = rendezvous(1)? val second = first + 1 val third = rendezvous(2)? result = first + second + third } return result }
fn independent(value: Int): Int { var result = 0 await { val first = value + 1 val second = value + 2 result = first + second } return result }
fn objectValue(): Int { var result = 0 await { val item = Counter { id: 1, value: 41 } val second = item.value + 1 result = second } return result }
`

const awaitDependencyAssertions = `package generated
import("context";"errors";"runtime";"sync/atomic";"testing";"time")
var dependencyFailure=errors.New("dependency failed")
type dependencyResolver struct{ entered chan struct{}; release chan struct{}; calls atomic.Int32 }
func(r *dependencyResolver)Rendezvous(ctx context.Context,index int64)(int64,error){r.entered<-struct{}{};select{case <-r.release:return index,nil;case <-ctx.Done():return 0,ctx.Err()}}
func(r *dependencyResolver)First(ctx context.Context,mode int64)(int64,error){switch mode{case -1:return 0,dependencyFailure;case -2:panic("native dependency panic");case -3:runtime.Goexit()};return 41,nil}
func(r *dependencyResolver)Next(ctx context.Context,value int64)(int64,error){r.calls.Add(1);if err:=ctx.Err();err!=nil{return 0,err};return value+1,nil}
func TestDependencyValues(t *testing.T){
 old:=runtime.GOMAXPROCS(1);defer runtime.GOMAXPROCS(old)
 app:=&App{Resolver:&dependencyResolver{}}
 for name,call:=range map[string]func(context.Context)(int64,error){"chain":app.chain,"nullable":app.nullable,"closure":app.closure,"ordered":app.ordered,"object":app.objectValue}{
  t.Run(name,func(t *testing.T){for range 32{if got,err:=call(t.Context());err!=nil||got!=42{t.Fatalf("got %d, %v; want 42",got,err)}}})
 }
 if got,err:=app.shadow(t.Context(),40);err!=nil||got!=42{t.Fatal("shadow dependency",got,err)}
 if got,err:=app.independent(t.Context(),40);err!=nil||got!=83{t.Fatal("outer parameter created a false dependency",got,err)}
}
func TestDependentFailureStopsLaterBatches(t *testing.T){
 r:=&dependencyResolver{};app:=&App{Resolver:r}
 if _,err:=app.guarded(t.Context(),-1);!errors.Is(err,dependencyFailure){t.Fatal("failure lost",err)}
 if got:=r.calls.Load();got!=0{t.Fatal("dependent native call started after failed prerequisite",got)}
 for _,mode:=range []int64{-2,-3}{if _,err:=app.guarded(t.Context(),mode);err==nil||r.calls.Load()!=0{t.Fatal("abnormal prerequisite started a dependent task",mode,err)}}
 if got,err:=app.guarded(t.Context(),0);err!=nil||got!=42{t.Fatal("later batch reused canceled context",got,err)}
}
func TestIndependentTasksStayParallelAcrossDependencies(t *testing.T){
 ctx,cancel:=context.WithTimeout(t.Context(),2*time.Second);defer cancel()
 r:=&dependencyResolver{entered:make(chan struct{},2),release:make(chan struct{})};app:=&App{Resolver:r};done:=make(chan error,1)
 go func(){value,err:=app.parallel(ctx);if err==nil&&value!=5{err=errors.New("incorrect dependency result")};done<-err}()
 for range 2{select{case <-r.entered:case <-ctx.Done():t.Fatal("independent tasks were serialized",ctx.Err())}}
 close(r.release);if err:=<-done;err!=nil{t.Fatal(err)}
}
func BenchmarkAwaitIndependent(b *testing.B){app:=&App{};ctx:=b.Context();b.ReportAllocs();for b.Loop(){if value,err:=app.independent(ctx,40);err!=nil||value!=83{b.Fatal(value,err)}}}
func BenchmarkAwaitDependencyChain(b *testing.B){app:=&App{};ctx:=b.Context();b.ReportAllocs();for b.Loop(){if value,err:=app.chain(ctx);err!=nil||value!=42{b.Fatal(value,err)}}}
`
