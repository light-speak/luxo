package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/token"
)

const asyncFailureSource = `
model Counter @noTime { id: Int @id value: Int }
event Ping(value: Int)
fn after(): Result<Int> @native
fn explode(mode: Int): Result<Int> @native
fn start(): Int {
    async { emit Ping(value: 42) emit Ping(value: 43) val _ = after()? }
    return 42
}
fn startSingle(): Int { async { emit Ping(value: 42) val _ = after()? } return 42 }
fn startControl(): Int { async { val _ = after()? } return 42 }
fn startNative(mode: Int): Int { async { val _ = explode(mode)? } return 42 }
`

func asyncFailureIDs() StableIDs {
	return StableIDs{
		ModelFields: map[string]map[string]int{"Counter": {"id": 1, "value": 2}},
		EventFields: map[string]map[string]int{"Ping": {"value": 1}},
	}
}

func TestGeneratedAsyncEmitFailureIsObserved(t *testing.T) {
	runGeneratedFixture(t, asyncFailureSource, asyncEmitAssertions, asyncFailureIDs())
}

func TestCompileAsyncEmitFailureReturn(t *testing.T) {
	for _, module := range []string{"", "origin", "shared"} {
		t.Run("module="+module, func(t *testing.T) {
			c := newCompiler(nil)
			c.api = &ast.ApiDecl{Pos: token.Position{File: "origin/origin/handlers.luxo"}}
			c.generator = mustNewGenerator(t, GeneratorConfig{Events: &EventContext{
				EventModule: map[string]string{"Ping": module},
			}})
			output := c.compileAsync(&ast.AsyncExpr{Body: &ast.Block{Stmts: []ast.Stmt{
				&ast.EmitStmt{EventName: "Ping"}, &ast.EmitStmt{EventName: "Ping"},
			}}})
			if strings.Count(output, "if err := ") != 2 ||
				strings.Count(strings.Join(strings.Fields(output), " "), "luxolog.Error((err).Error()) return }") != 2 {
				t.Fatal("async emit must stop after either error", output)
			}
			want := "EmitPing(ctx, app.EventBus, PingEvent{})"
			if module == "shared" {
				want = "shared_luxo.EmitPing(ctx, app.EventBus, shared_luxo.PingEvent{})"
			}
			if strings.Count(output, want) != 2 {
				t.Fatal("event module changed", output)
			}
		})
	}
}

const asyncEmitAssertions = `package generated
import (
    "context"
    "testing"
    "testing/synctest"
    "github.com/light-speak/luxo/pkg/lux/event"
)
type trackedAsyncError struct{ observed chan struct{} }
func (e trackedAsyncError) Error() string {
    e.observed <- struct{}{}
    return "async event publication failed"
}
type trackedAsyncBus struct {
    event.Bus
    observed chan struct{}
    attempts chan int64
}
func (b *trackedAsyncBus) Emit(ctx context.Context, name string, payload any) error {
    b.attempts <- payload.(*PingEvent).Value
    if err := b.Bus.Emit(ctx, name, payload); err != nil {
        return trackedAsyncError{b.observed}
    }
    return nil
}
type asyncAfterResolver struct{ called chan struct{} }
func (r *asyncAfterResolver) After(context.Context) (int64, error) {
    r.called <- struct{}{}
    return 1, nil
}
func (*asyncAfterResolver) Explode(context.Context, int64) (int64, error) { return 42, nil }
func TestAsyncEmit(t *testing.T) {
    for _, fail := range []bool{false, true} {
        synctest.Test(t, func(t *testing.T) {
            bus := event.NewChanBus(1)
            if fail { if err := bus.Close(); err != nil { t.Fatal(err) } }
            t.Cleanup(func(){ if err := bus.Close(); err != nil { t.Error(err) } })
            observed, after := make(chan struct{}, 2), make(chan struct{}, 1)
            attempts := make(chan int64, 2)
            app := &App{EventBus: &trackedAsyncBus{bus, observed, attempts}, Resolver: &asyncAfterResolver{after}}
            if value, err := app.start(t.Context()); err != nil || value != 42 {
                t.Fatal("async changed caller result", value, err)
            }
            synctest.Wait()
            wantAttempts, wantObserved, wantAfter := 2, 0, 1
            if fail { wantAttempts, wantObserved, wantAfter = 1, 1, 0 }
            if len(attempts) != wantAttempts || len(observed) != wantObserved || len(after) != wantAfter {
                t.Fatal("async event failure was ignored or stopped success", fail, len(attempts), len(observed), len(after))
            }
            if first := <-attempts; first != 42 { t.Fatal("first event changed", first) }
            if !fail { if second := <-attempts; second != 43 { t.Fatal("second event changed", second) } }
        })
    }
}
func BenchmarkGeneratedAsyncEmitLifecycle(b *testing.B) {
    for _, sample := range []string{"control", "single", "double"} {
        b.Run(sample, func(b *testing.B) {
            bus := event.NewChanBus(1)
            b.Cleanup(func(){ if err := bus.Close(); err != nil { b.Error(err) } })
            after := make(chan struct{})
            app := &App{EventBus: bus, Resolver: &asyncAfterResolver{after}}
            call := app.startControl
            if sample == "single" { call = app.startSingle }
            if sample == "double" { call = app.start }
            ctx := context.Background()
            b.ReportAllocs()
            for b.Loop() {
                if value, err := call(ctx); value != 42 || err != nil { b.Fatal(value, err) }
                <-after
            }
        })
    }
}
`

func TestGeneratedAsyncNativePanic(t *testing.T) {
	runGeneratedFixture(t, asyncFailureSource, asyncNativeAssertions, asyncFailureIDs())
}

func TestGeneratedAsyncNativeGoexit(t *testing.T) {
	assertions := strings.ReplaceAll(asyncNativeAssertions, "[]int64{1, 2, 3, 4, 0}", "[]int64{2, 0}")
	runGeneratedFixture(t, asyncFailureSource, assertions, asyncFailureIDs())
}

func TestGeneratedAsyncReturnEvaluatesVoidExpression(t *testing.T) {
	runGeneratedFixture(t, asyncVoidReturnSource, asyncVoidReturnAssertions, asyncFailureIDs())
}

func TestGeneratedVoidReturnBoundaries(t *testing.T) {
	runGeneratedFixture(t, `
model Counter @noTime { id: Int @id value: Int }
fn closeSignal(done: Channel<Int>) { done.close() }
fn direct(done: Channel<Int>) { return done.close() }
fn helper(done: Channel<Int>) { return closeSignal(done) }
fn launch(done: Channel<Int>) { return async { done.close() } }
fn transactional(done: Channel<Int>) { transaction { return done.close() } }
fn failing() { throw error.NotFound }
fn propagate() { return failing() }
api directSignal(): Int { val done: Channel<Int> = Channel(1) direct(done) return 42 }
api helperSignal(): Int { val done: Channel<Int> = Channel(1) helper(done) return 42 }
api failSignal(): Int { propagate() return 42 }
`, `package generated
import("bytes";"context";"net/http/httptest";"testing";"testing/synctest";
 "github.com/light-speak/luxo/pkg/lux/codec";"github.com/light-speak/luxo/pkg/lux/luvia")
func TestVoidReturns(t *testing.T) {
    app := &App{}
    for _, call := range []func(context.Context, chan int64) error{app.direct, app.helper, app.launch} {
        synctest.Test(t, func(t *testing.T) {
            done := make(chan int64)
            if err := call(t.Context(), done); err != nil { t.Fatal(err) }
            synctest.Wait()
            select { case _, open := <-done: if open { t.Fatal("close produced a value") }; default: t.Fatal("Void return skipped its operation") }
        })
    }
}
func TestVoidAPIReturns(t *testing.T) {
    router := luvia.New().Router
    if err := RegisterSchema(router.Schema); err != nil { t.Fatal(err) }
    if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil { t.Fatal(err) }
    if err := RegisterHandlers(router, &App{}); err != nil { t.Fatal(err) }
    if err := router.Prepare(); err != nil { t.Fatal(err) }
    for _, name := range []string{"directSignal", "helperSignal", "failSignal"} { for _, binary := range []bool{false, true} {
        want := 200
        if name == "failSignal" { want = 404 }
        body := []byte("{\"$api\":\""+name+"\"}")
        if binary { body = codec.AppendVarint(nil, uint64(router.Schema.APIs[name].ID)); body = append(body, 0, 0) }
        request := httptest.NewRequest("POST", "/luvia", bytes.NewReader(body))
        request.Header.Set("Content-Type", "application/json")
        if binary { request.Header.Set("Content-Type", "application/x-luxo"); request.Header.Set("X-Luxo-Mode", "binary") }
        response := httptest.NewRecorder()
        router.ServeHTTP(response, request)
        if response.Code != want { t.Fatal("Void return lost success or error", name, binary, response.Code, response.Body.String()) }
        if binary && want == 200 && !bytes.Equal(response.Body.Bytes(), codec.AppendSvarint(nil, 42)) { t.Fatal("normal API response changed", response.Body.Bytes()) }
    } }
}
`, asyncFailureIDs())
}

const asyncVoidReturnSource = `
model Counter @noTime { id: Int @id value: Int }
fn stop(done: Channel<Int>) { async { return done.close() } }
fn stopViaHelper(done: Channel<Int>) { async { return closeSignal(done) } }
fn statement(done: Channel<Int>) { async { done.close() } }
fn statementViaHelper(done: Channel<Int>) { async { closeSignal(done) } }
fn closeSignal(done: Channel<Int>) { done.close() }
`

const asyncVoidReturnAssertions = `package generated
import("context";"os";"testing";"testing/synctest")
func TestAsyncVoidReturnClosesChannel(t *testing.T) {
    app := &App{}
    for _, call := range []func(context.Context, chan int64) error{app.stop, app.stopViaHelper} {
    synctest.Test(t, func(t *testing.T) {
        done := make(chan int64)
        if err := call(t.Context(), done); err != nil { t.Fatal(err) }
        synctest.Wait()
        select {
        case _, open := <-done:
            if open { t.Fatal("close produced a value") }
        default:
            t.Fatal("async return discarded its side effect")
        }
    })
    }
}

func BenchmarkGeneratedAsyncVoidReturn(b *testing.B) {
    app := &App{}
    reference := os.Getenv("LUXO_ASYNC_RETURN_REFERENCE") == "1"
    for _, helper := range []bool{false, true} {
        name := "direct"
        if helper { name = "helper" }
        b.Run(name, func(b *testing.B) {
            call := app.stop
            if helper { call = app.stopViaHelper }
            if reference { call = app.statement; if helper { call = app.statementViaHelper } }
            ctx := context.Background()
            b.ReportAllocs()
            for b.Loop() {
                done := make(chan int64)
                if err := call(ctx, done); err != nil { b.Fatal(err) }
                <-done
            }
        })
    }
}
`

const asyncNativeAssertions = `package generated
import (
    "context"
    "io"
    "os"
    "runtime"
    "strings"
    "testing"
    "testing/synctest"
)
type asyncPanicResolver struct{}
type hostileAsyncPanic struct{}
func (hostileAsyncPanic) Error() string { panic("panic payload formatting must not run") }
func (*asyncPanicResolver) After(context.Context) (int64, error) { return 0, nil }
func (*asyncPanicResolver) Explode(ctx context.Context, mode int64) (int64, error) {
    switch mode {
    case 1: panic("private async native panic")
    case 2: runtime.Goexit()
    case 3: panic(hostileAsyncPanic{})
    case 4: panic(nil)
    }
    return 42, nil
}
func TestAsyncNativeExit(t *testing.T) {
    for _, mode := range []int64{1, 2, 3, 4, 0} {
        synctest.Test(t, func(t *testing.T) {
            reader, writer, err := os.Pipe()
            if err != nil { t.Fatal(err) }
            original := os.Stderr
            os.Stderr = writer
            defer func(){ os.Stderr = original; reader.Close(); writer.Close() }()
            app := &App{Resolver: &asyncPanicResolver{}}
            if value, err := app.startNative(t.Context(), mode); value != 42 || err != nil {
                t.Fatal("native exit changed async caller result", value, err)
            }
            synctest.Wait()
            writer.Close()
            output, err := io.ReadAll(reader)
            if err != nil { t.Fatal(err) }
            if mode == 0 {
                if len(output) != 0 { t.Fatal("successful async reported failure", string(output)) }
            } else if !strings.Contains(string(output), "async task") {
                t.Fatal("abnormal async exit was not reported", mode, string(output))
            }
            if strings.Contains(string(output), "panic payload formatting must not run") {
                t.Fatal("diagnostic invoked panic payload methods")
            }
        })
    }
}
`
