package h2transport

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
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
