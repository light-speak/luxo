package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/schema"
)

func TestBinaryPlanRegistrationOrder(t *testing.T) {
	for _, paramsFirst := range []bool{false, true} {
		r := NewAPIRegistry()
		params := []ParamMeta{{Name: "x", Type: "Int", FieldID: 42}}
		if paramsFirst {
			if err := r.RegisterParams("read", params); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.Register("read", 2147483647); err != nil {
			t.Fatal(err)
		}
		if !paramsFirst {
			if err := r.RegisterParams("read", params); err != nil {
				t.Fatal(err)
			}
		}
		p := r.binaryPlans[2147483647]
		if len(r.binaryPlans) != 1 || p.name != "read" || p.names[0] != "x" || p.lookup.find(42) != 0 {
			t.Fatal("incomplete plan")
		}
		old := p
		if err := r.RegisterParams("read", []ParamMeta{{Name: "x", Type: "Int", FieldID: 43}}); err == nil {
			t.Fatal("accepted conflict")
		}
		if r.binaryPlans[2147483647] != old {
			t.Fatal("failed registration changed plan")
		}
	}
}

func TestPreparedBinaryRoutesExcludeInternal(t *testing.T) {
	rt := NewRouter()
	publicCalls, internalCalls := 0, 0
	rt.Handle("public", func(_ context.Context, req *Request) error { publicCalls++; req.Buf.AppendByte(0); return nil })
	rt.HandleInternal("private", func(context.Context, *Request) error { internalCalls++; return nil })
	for i, name := range []string{"public", "private"} {
		if err := rt.Registry.Register(name, i+1); err != nil {
			t.Fatal(err)
		}
	}
	if err := rt.Prepare(); err != nil {
		t.Fatal(err)
	}
	if len(rt.binaryRoutes) != 1 {
		t.Fatal("internal route leaked into public plan")
	}
	for i, want := range []int{200, 404} {
		r := httptest.NewRequest(http.MethodPost, "/luvia", bytes.NewReader([]byte{byte(i + 1), 0, 0}))
		r.Header.Set("X-Luxo-Mode", "binary")
		w := httptest.NewRecorder()
		rt.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("status=%d want=%d", w.Code, want)
		}
	}
	if publicCalls != 1 || internalCalls != 0 {
		t.Fatal("incorrect handler dispatch")
	}
}

func TestBinaryPlanSchemaFailureIsAtomic(t *testing.T) {
	r := NewAPIRegistry()
	if err := r.Register("read", 1); err != nil {
		t.Fatal(err)
	}
	old := r.binaryPlans[1]
	s := schema.New()
	if err := s.RegisterAPI(&schema.API{ID: 2, Name: "read", ReturnType: "Int"}); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSchemaAPIs(s); err == nil {
		t.Fatal("accepted conflict")
	}
	if r.binaryPlans[1] != old || len(r.binaryPlans) != 1 {
		t.Fatal("failed schema install changed plan")
	}
}

func TestBinaryPlanSchemaReplacement(t *testing.T) {
	r := NewAPIRegistry()
	if err := r.Register("read", 1); err != nil {
		t.Fatal(err)
	}
	old := r.binaryPlans[1]
	s := schema.New()
	if err := s.RegisterAPI(&schema.API{ID: 1, Name: "read", ReturnType: "Int"}); err != nil {
		t.Fatal(err)
	}
	if err := r.SetSchema(s); err != nil {
		t.Fatal(err)
	}
	if r.binaryPlans[1] == old || old.definition != nil || r.binaryPlans[1].definition != s.APIs["read"] {
		t.Fatal("plan is not a replaced snapshot")
	}
	if err := r.RegisterSchemaAPIs(s); err != nil {
		t.Fatal(err)
	}
	if r.binaryPlans[1].definition != s.APIs["read"] {
		t.Fatal("batch plan missing schema")
	}
}

func TestBinaryParamLookupCollisions(t *testing.T) {
	params := make([]ParamMeta, 0, 16)
	for id := 1; len(params) < 16; id++ {
		if uint32(id)*0x9e3779b1>>27 == 0 {
			params = append(params, ParamMeta{FieldID: id})
		}
	}
	lookup := newBinaryParamLookup(params)
	for i, p := range params {
		if got := lookup.find(p.FieldID); got != i {
			t.Fatalf("slot=%d want=%d", got, i)
		}
	}
	for id := params[len(params)-1].FieldID + 1; ; id++ {
		if uint32(id)*0x9e3779b1>>27 == 0 {
			if lookup.find(id) != -1 {
				t.Fatal("accepted unknown colliding ID")
			}
			break
		}
	}
}
