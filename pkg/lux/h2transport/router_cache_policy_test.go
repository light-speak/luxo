package h2transport

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync/atomic"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/cachepolicy"
	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/cache"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"golang.org/x/net/http2"
)

func TestRouterNoStoreSurvivesH2OwnershipTransfer(t *testing.T) {
	router := api.NewRouter()
	if err := router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "read", ReturnType: "Int"}); err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	router.Handle("read", func(_ context.Context, req *api.Request) error {
		req.Buf.B = codec.AppendSvarint(req.Buf.B, 42)
		return nil
	})
	var transfers atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		router.ServeHTTP(w, r)
		if owned, ok := w.(*response); ok && owned.owner != nil {
			transfers.Add(1)
		}
	})
	client, endpoint := cachePolicyTLSClient(t, handler)
	for _, binary := range []bool{false, true} {
		for _, valid := range []bool{false, true} {
			checkCachePolicyResponse(t, client, endpoint, binary, valid)
		}
	}
	if transfers.Load() != 1 {
		t.Fatalf("Binary success bypassed ownership transfer: %d transfers", transfers.Load())
	}
}

func cachePolicyTLSClient(t *testing.T, handler http.Handler) (*http.Client, string) {
	t.Helper()
	engine, err := NewServer(handler)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	if err := engine.ConfigureHTTPServer(server.Config); err != nil {
		t.Fatal(err)
	}
	server.TLS = server.Config.TLSConfig.Clone()
	server.StartTLS()
	t.Cleanup(func() { _ = engine.Close(); server.Close() })
	tlsConfig := server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	transport := &http2.Transport{TLSClientConfig: tlsConfig}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 3 * time.Second}, server.URL
}

func checkCachePolicyResponse(t *testing.T, client *http.Client, endpoint string, binary, valid bool) {
	t.Helper()
	body := []byte(`{"$api":"read"}`)
	if binary {
		body = []byte{1, 0, 0}
	}
	wantStatus := http.StatusOK
	if !valid {
		body, wantStatus = []byte{0xff}, http.StatusBadRequest
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if binary {
		request.Header.Set("X-Luxo-Mode", "binary")
	}
	result, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body.Close()
	data, err := io.ReadAll(result.Body)
	if err != nil || result.ProtoMajor != 2 || result.StatusCode != wantStatus || result.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("binary=%t valid=%t: status=%d proto=%s headers=%v body=%q err=%v", binary, valid, result.StatusCode, result.Proto, result.Header, data, err)
	}
	if valid && binary && !bytes.Equal(data, []byte{84}) {
		t.Fatalf("transferred Binary body changed: %x", data)
	}
}

func TestRouterResponseContractAcrossHTTPTransports(t *testing.T) {
	for _, transport := range []string{"H1", "StandardH2", "LuxoH2"} {
		for _, mode := range []string{"nil", "replace", "empty", "replaceBytes"} {
			for _, binary := range []bool{false, true} {
				format := "JSON"
				if binary {
					format = "Binary"
				}
				t.Run(transport+"/"+mode+"/"+format, func(t *testing.T) {
					checkRouterResponseContract(t, transport, mode, binary)
				})
			}
		}
	}
}

func responseContractRouter(t *testing.T, mode string) *api.Router {
	t.Helper()
	router := api.NewRouter()
	for i, name := range []string{"fault", "zero", "nullable"} {
		if err := router.Schema.RegisterAPI(&schema.API{ID: i + 1, Name: name, ReturnType: "Int", ReturnNullable: name == "nullable"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	router.Handle("fault", func(_ context.Context, req *api.Request) error {
		req.Buf.B = codec.AppendSvarint(req.Buf.B, 42)
		switch mode {
		case "nil":
			req.Buf = nil
		case "replace":
			req.Buf = &api.ResponseBuf{B: codec.AppendSvarint(nil, 7)}
		case "empty":
			req.Buf.B = req.Buf.B[:0]
		case "replaceBytes":
			req.Buf.B = codec.AppendSvarint(nil, 7)
		}
		return nil
	})
	for _, name := range []string{"zero", "nullable"} {
		router.Handle(name, func(_ context.Context, req *api.Request) error {
			req.Buf.B = append(req.Buf.B, 0)
			return nil
		})
	}
	return router
}

func responseContractTLSClient(t *testing.T, handler http.Handler, transport string) (*http.Client, string) {
	t.Helper()
	if transport == "LuxoH2" {
		return cachePolicyTLSClient(t, handler)
	}
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = transport == "StandardH2"
	server.StartTLS()
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 3 * time.Second
	t.Cleanup(client.CloseIdleConnections)
	return client, server.URL
}

func checkRouterResponseContract(t *testing.T, transport, mode string, binary bool) {
	t.Helper()
	client, endpoint := responseContractTLSClient(t, responseContractRouter(t, mode), transport)
	var first httptrace.GotConnInfo
	for _, id := range []int{1, 1, 2, 3} {
		payload, status, proto, current := responseContractRequest(t, client, endpoint, id, binary)
		wantProto := 2
		if transport == "H1" {
			wantProto = 1
		}
		if proto != wantProto {
			t.Fatalf("transport fell back: protocol=%d want=%d", proto, wantProto)
		}
		if first.Conn == nil {
			first = current
		} else if first.Conn != current.Conn || !current.Reused {
			t.Fatal("response failure replaced the existing connection")
		}
		wantStatus := http.StatusOK
		if id == 1 && mode != "replaceBytes" {
			wantStatus = http.StatusInternalServerError
		}
		if status != wantStatus {
			t.Fatalf("status=%d want=%d body=%q", status, wantStatus, payload)
		}
		assertResponseContractBody(t, payload, binary, id, wantStatus)
	}
}

func responseContractRequest(t *testing.T, client *http.Client, endpoint string, id int, binary bool) ([]byte, int, int, httptrace.GotConnInfo) {
	t.Helper()
	names := []string{"", "fault", "zero", "nullable"}
	body := []byte(`{"$api":"` + names[id] + `"}`)
	if binary {
		body = []byte{byte(id), 0, 0}
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if binary {
		request.Header.Set("X-Luxo-Mode", "binary")
	}
	connections := make(chan httptrace.GotConnInfo, 4)
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		select {
		case connections <- info:
		default:
		}
	}}))
	result, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(result.Body)
	closeErr := result.Body.Close()
	if readErr != nil || closeErr != nil || len(connections) != 1 || result.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("invalid response/connection lifecycle: reads=%v close=%v connections=%d cache=%q", readErr, closeErr, len(connections), result.Header.Get("Cache-Control"))
	}
	return data, result.StatusCode, result.ProtoMajor, <-connections
}

func assertResponseContractBody(t *testing.T, payload []byte, binary bool, id, status int) {
	t.Helper()
	if status == http.StatusInternalServerError {
		assertResponseContractError(t, payload, binary)
		return
	}
	if binary {
		want := []byte{0}
		if id == 1 {
			want = codec.AppendSvarint(nil, 7)
		}
		if !bytes.Equal(payload, want) {
			t.Fatalf("zero, null or buffer-slice value changed: %x", payload)
		}
		return
	}
	var envelope struct{ Data json.RawMessage }
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatal(err)
	}
	want := "0"
	if id == 1 {
		want = "7"
	} else if id == 3 {
		want = "null"
	}
	if string(envelope.Data) != want {
		t.Fatalf("data=%s want=%s", envelope.Data, want)
	}
}

func assertResponseContractError(t *testing.T, payload []byte, binary bool) {
	t.Helper()
	if binary {
		failure, err := api.DecodeBinaryError(payload, 0)
		if err != nil || failure.Code != 500 || failure.Name != "Internal" || failure.Message != "error.internal" || len(failure.Data) != 0 {
			t.Fatalf("invalid Internal error: %+v err=%v", failure, err)
		}
		return
	}
	var failure struct {
		Code                  int
		Error, Message, Cause string
		Data                  json.RawMessage
	}
	if err := json.Unmarshal(payload, &failure); err != nil || failure.Code != 500 || failure.Error != "Internal" || failure.Message != "error.internal" || len(failure.Data) != 0 || failure.Cause != "" {
		t.Fatalf("private or incomplete JSON error: %s err=%v", payload, err)
	}
}

func TestRouterCacheRejectsInvalidResponsesAcrossHTTPTransports(t *testing.T) {
	for _, transport := range []string{"H1", "StandardH2", "LuxoH2"} {
		for _, mode := range []string{"nil", "replace", "empty"} {
			for _, nullable := range []bool{false, true} {
				for _, binary := range []bool{false, true} {
					result, format := "zero", "JSON"
					if nullable {
						result = "null"
					}
					if binary {
						format = "Binary"
					}
					t.Run(transport+"/"+mode+"/"+result+"/"+format, func(t *testing.T) {
						checkRouterCacheResponseRecovery(t, transport, mode, nullable, binary)
					})
				}
			}
		}
	}
}

func cacheResponseRecoveryRouter(t *testing.T, mode string, nullable bool, backend cache.Cache, calls *atomic.Int32) *api.Router {
	t.Helper()
	binding, err := api.NewResponseCache(backend,
		api.CacheNamespace{Project: "response", Environment: "test", Service: "contract", Revision: "1"},
		api.ResponseOriginOptions{MaxConcurrent: 2, MaxWaiting: 2, WaitTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	router := api.NewRouterWithOptions(api.RouterOptions{ResponseCache: binding})
	definition := &schema.API{ID: 1, Name: "fault", ReturnType: "Int", ReturnNullable: nullable,
		CachePolicy: &schema.CachePolicy{Mode: cachepolicy.TTL, TTL: time.Minute, Scope: cachepolicy.Private}, CacheDependencies: &schema.CacheDependencies{}}
	if err := router.Schema.RegisterAPI(definition); err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	router.RequireCache("fault")
	router.Handle("fault", func(ctx context.Context, req *api.Request) error {
		return api.CacheResponsePolicy(ctx, req, "", func() error {
			if calls.Add(1) == 1 {
				req.Buf.B = codec.AppendSvarint(req.Buf.B, 42)
				switch mode {
				case "nil":
					req.Buf = nil
				case "replace":
					req.Buf = &api.ResponseBuf{B: codec.AppendSvarint(nil, 7)}
				case "empty":
					req.Buf.B = req.Buf.B[:0]
				}
				return nil
			}
			req.Buf.B = append(req.Buf.B, 0)
			return nil
		})
	})
	if err := router.Prepare(); err != nil {
		t.Fatal(err)
	}
	return router
}

func checkRouterCacheResponseRecovery(t *testing.T, transport, mode string, nullable, binary bool) {
	t.Helper()
	backend := cache.NewMemory()
	t.Cleanup(func() { _ = backend.Close() })
	var calls atomic.Int32
	router := cacheResponseRecoveryRouter(t, mode, nullable, backend, &calls)
	client, endpoint := responseContractTLSClient(t, router, transport)
	var first httptrace.GotConnInfo
	for attempt := range 3 {
		payload, status, proto, current := responseContractRequest(t, client, endpoint, 1, binary)
		wantProto := 2
		if transport == "H1" {
			wantProto = 1
		}
		if proto != wantProto {
			t.Fatalf("transport fell back: protocol=%d want=%d", proto, wantProto)
		}
		if first.Conn == nil {
			first = current
		} else if first.Conn != current.Conn || !current.Reused {
			t.Fatal("cached response failure replaced the existing connection")
		}
		wantStatus, wantCalls, wantEntries := 200, int32(2), 1
		if attempt == 0 {
			wantStatus, wantCalls, wantEntries = 500, 1, 0
		}
		if status != wantStatus || calls.Load() != wantCalls || backend.Len() != wantEntries {
			t.Fatalf("attempt=%d status=%d calls=%d entries=%d; want=%d/%d/%d body=%q", attempt, status, calls.Load(), backend.Len(), wantStatus, wantCalls, wantEntries, payload)
		}
		id := 2
		if nullable {
			id = 3
		}
		assertResponseContractBody(t, payload, binary, id, wantStatus)
	}
}
