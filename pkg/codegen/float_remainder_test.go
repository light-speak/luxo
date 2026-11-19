package codegen

import "testing"

func TestGeneratedFloatRemainder(t *testing.T) {
	runGeneratedFixture(t, floatRemainderSource, floatRemainderAssertions, StableIDs{
		ModelFields: map[string]map[string]int{"Principal": {"id": 1, "score": 2}, "Box": {"id": 1, "value": 2, "touches": 3, "mutations": 4}, "RemainderSnapshot": {"value": 1, "secret": 2}},
		EventFields: map[string]map[string]int{"Changed": {"value": 1}},
		APIs:        map[string]int{"modulo": 401}, APIParams: map[string]map[string]int{"modulo": {"left": 1, "right": 2}},
	}, floatRemainderBenchmarks)
}

const floatRemainderSource = `model Principal @noTime @withAuth(stores: [id, score]) { id: Int @id score: Float }
model Box @noTime @crud { id: Int @id value: Float @beforeSave { it % 2 } touches: Int mutations: Int }
type RemainderSnapshot {
  value: Float @transform { it % 2 }
  secret: Float? @visible(my.score % 2 == 1.5)
}
event Changed(value: Float)
api watch(): Float @stream(Changed) { it.value % 2 == 1.5 }
api modulo(left: Float, right: Float): Float { return left % right }
fn remainder(left: Float, right: Float): Float { return left % right }
fn remainderMixedFI(left: Float, right: Int): Float { return left % right }
fn remainderMixedIF(left: Int, right: Float): Float { return left % right }
fn literalRemainder(): Float { return 5.5 % 2.0 }
fn compoundRemainder(left: Float, right: Float): Float { left %= right return left }
fn shadow(math: Float, right: Float): Float { return math % right }
fn target(box: Box): Box { box.touches += 1 return box }
fn mutate(box: Box): Float { box.mutations += 1 box.value = 99.0 return 2.0 }
fn remainderEffects(box: Box): Float { return box.value % mutate(box) }
fn compoundEffects(box: Box): Float { target(box).value %= mutate(box) return box.value }
fn literalStages(value: Float): Float { return (0.1 + 0.2) - value }
fn defaultStages(value: Float = 0.1): Float { return value + 0.2 }
fn invokeDefaultStages(): Float { return defaultStages() }
fn nullableStages(value: Float?): Float { return (value ?: 0.1) + 0.2 }
fn productStages(): Float { return 10000000000000000.0 * 1.0000000000000001 - 10000000000000000.0 }
`
const floatRemainderAssertions = `package generated

import (
	"bytes"
	"context"
	"encoding/json"
 "sync/atomic"
	"fmt"
	"math"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/auth"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/rpc"
)

func assertFloatBits(t *testing.T, got, want float64, err error) {
	t.Helper()
	if err != nil || math.Float64bits(got) != math.Float64bits(want) {
		t.Fatalf("bits=%016x want=%016x error=%v", math.Float64bits(got), math.Float64bits(want), err)
	}
}

func remainderPairs() [][2]float64 {
	return [][2]float64{
		{5.5, 2}, {-5.5, 2}, {5.5, -2}, {-5.5, -2},
		{math.Copysign(0, -1), 2}, {0, -2}, {-4, 2},
		{math.SmallestNonzeroFloat64, 2}, {math.MaxFloat64, 3},
	}
}

func TestFloatRemainderCandidates(t *testing.T) {
	app := &App{}
	for _, pair := range remainderPairs() {
		for _, call := range []func(context.Context, float64, float64) (float64, error){app.remainder, app.compoundRemainder, app.shadow} {
			got, err := call(t.Context(), pair[0], pair[1])
			assertFloatBits(t, got, math.Mod(pair[0], pair[1]), err)
		}
	}
	got, err := app.literalRemainder(t.Context())
	assertFloatBits(t, got, 1.5, err)
	for _, sample := range []struct {
		call    func(context.Context, *Box) (float64, error)
		touches int64
		value   float64
	}{
		{app.remainderEffects, 0, 99}, {app.compoundEffects, 1, 1.5},
	} {
		box := &Box{Value: 5.5}
		got, err := sample.call(t.Context(), box)
		assertFloatBits(t, got, 1.5, err)
		if box.Touches != sample.touches || box.Mutations != 1 || box.Value != sample.value {
			t.Fatal("wrong target/RHS evaluation count or assignment ordering", box)
		}
	}
}

func TestLiteralRoundingControls(t *testing.T) {
	app := &App{}
	left, right := 0.1, 0.2
	sum := left + right
	got, err := app.literalStages(t.Context(), sum)
	assertFloatBits(t, got, 0, err)
	got, err = app.invokeDefaultStages(t.Context())
	assertFloatBits(t, got, sum, err)
	got, err = app.nullableStages(t.Context(), nil)
	assertFloatBits(t, got, sum, err)
	large, one := 10000000000000000.0, 1.0000000000000001
	got, err = app.productStages(t.Context())
	assertFloatBits(t, got, large*one-large, err)
}

func remainderClaims(t *testing.T, score float64) auth.Claims {
	t.Helper()
	cfg := &auth.Config{Secret: "float-remainder-prototype-only", Expires: time.Hour}
	token, err := auth.SignForSubject(cfg, "Principal", func(dst []byte) ([]byte, error) {
		return fmt.Appendf(dst, "{\"id\":1,\"score\":%g}", score), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := auth.Verify(cfg, token)
	if err != nil {
		t.Fatal(err)
	}
	return claims
}

func TestRemainderFieldWriters(t *testing.T) {
	for _, score := range []float64{5.5, -5.5} {
		secret := 42.0
		original := RemainderSnapshot{Value: 5.5, Secret: &secret}
		for _, column := range []bool{false, true} {
			buf := &api.ResponseBuf{Identity: remainderClaims(t, score)}
			var decoded RemainderSnapshot
			if column {
				if err := WriteColumnarRemainderSnapshot(buf, []RemainderSnapshot{original}, nil); err != nil {
					t.Fatal(err)
				}
				rows, err := ReadColumnarRemainderSnapshot(buf.B)
				if err != nil || len(rows) != 1 {
					t.Fatalf("column read: %d %v", len(rows), err)
				}
				decoded = rows[0]
			} else {
				if err := original.WriteLuxo(buf, nil); err != nil {
					t.Fatal(err)
				}
				decoder := codec.NewDecoder(buf.B)
				decoded.ReadLuxo(decoder)
				if err := decoder.Finish(); err != nil {
					t.Fatal(err)
				}
			}
			assertFloatBits(t, decoded.Value, 1.5, nil)
			if (decoded.Secret != nil) != (score == 5.5) || original.Value != 5.5 || *original.Secret != 42 {
				t.Fatal("visibility mismatch or output transform mutated its source")
			}
		}
	}
	for _, value := range []float64{5.5, -5.5, 7.5, 4} {
		if matchWatch(value, &WatchStreamParams{}, nil) != (math.Mod(value, 2) == 1.5) {
			t.Fatal("matcher mismatch", value)
		}
	}
}

func remainderRouter(t *testing.T) *api.Router {
	t.Helper()
	router := api.NewRouter()
	if err := RegisterSchema(router.Schema); err != nil {
		t.Fatal(err)
	}
	if err := RegisterHandlers(router, &App{}); err != nil {
		t.Fatal(err)
	}
	if err := router.Prepare(); err != nil {
		t.Fatal(err)
	}
	return router
}

func TestRemainderHTTPAndLRPC(t *testing.T) {
	router := remainderRouter(t)
	id := router.Schema.APIs["modulo"].ID
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := rpc.NewServer(router)
	done := make(chan error, 1)
	counted := &remainderListener{Listener: listener}
 go func() { done <- server.Serve(counted) }()
	defer func() {
		server.Close()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	client := rpc.NewClient(listener.Addr().String())
	defer func(){client.Close();if counted.accepted.Load()!=1{t.Error("LRPC connections",counted.accepted.Load())}}()
	for _, pair := range remainderPairs() {
		want := math.Mod(pair[0], pair[1])
		body, err := remainderRequest(id, pair[0], pair[1])
		if err != nil {
			t.Fatal(err)
		}
		for _, binary := range []bool{false, true} {
			payload := body
			if !binary {
				payload = fmt.Appendf(nil, "{\"$api\":\"modulo\",\"left\":%g,\"right\":%g}", pair[0], pair[1])
			}
			request := httptest.NewRequest("POST", "/luvia", bytes.NewReader(payload))
			request.Header.Set("Content-Type", "application/json")
			if binary {
				request.Header.Set("Content-Type", "application/x-luxo")
				request.Header.Set("X-Luxo-Mode", "binary")
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != 200 {
				t.Fatalf("HTTP: %d %s", response.Code, response.Body)
			}
			if binary {
				decoder := codec.NewDecoder(response.Body.Bytes())
				got := decoder.ReadFloat()
				assertFloatBits(t, got, want, decoder.Finish())
			} else {
				var envelope struct{ Data *float64 }
				if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.Data == nil {
					t.Fatalf("JSON: %s error=%v", response.Body, err)
				}
				assertFloatBits(t, *envelope.Data, want, nil)
			}
		}
		// The RPC route receives parameters without the outer API and selection headers.
		params := body[len(codec.AppendVarint(nil, uint64(id)))+1:]
		raw, err := client.CallWithMaskContext(t.Context(), "", id, nil, params)
		if err != nil {
			t.Fatal(err)
		}
		decoder := codec.NewDecoder(raw)
		got := decoder.ReadFloat()
		assertFloatBits(t, got, want, decoder.Finish())
	}
	for _, binary := range []bool{false, true} {
		payload := []byte("{\"$api\":\"modulo\",\"left\":1.0,\"right\":0.0}")
		if binary {
			payload, err = remainderRequest(id, 1.0, 0.0)
			if err != nil {
				t.Fatal(err)
			}
		}
		request := httptest.NewRequest("POST", "/luvia", bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		if binary {
			request.Header.Set("X-Luxo-Mode", "binary")
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != 500 {
			t.Fatalf("nonfinite result must fail encoding: HTTP %d, body %s",response.Code,response.Body)
		}
	}
}

type remainderListener struct {net.Listener;accepted atomic.Int64}
func(l *remainderListener)Accept()(net.Conn,error){conn,err:=l.Listener.Accept();if err==nil{l.accepted.Add(1)};return conn,err}
func remainderRequest(id int,left,right float64)([]byte,error){
 var params codec.Encoder
 if err:=params.WriteFieldFloat(1,left);err!=nil{return nil,err}
 if err:=params.WriteFieldFloat(2,right);err!=nil{return nil,err}
 params.WriteEnd()
 header:=codec.AppendVarint(nil,uint64(id));header=codec.AppendVarint(header,0)
 return append(header,params.Bytes()...),nil
}
`
