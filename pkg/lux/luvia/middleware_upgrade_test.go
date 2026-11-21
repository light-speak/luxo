package luvia

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/auth"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"nhooyr.io/websocket"
)

func TestAuthQueryTokenRequiresWebSocketUpgrade(t *testing.T) {
	cfg := testCfg()
	token, err := auth.Sign(cfg, testClaimsJSON(`{"id":42}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, method        string
		upgrade, connection []string
		authenticated       bool
	}{
		{"ordinary", "GET", nil, nil, false},
		{"lowercase", "GET", []string{"websocket"}, []string{"Upgrade"}, true},
		{"mixed_case", "GET", []string{"WebSocket"}, []string{"upGRade"}, true},
		{"protocol_list", "GET", []string{"h2c, websocket"}, []string{"keep-alive, Upgrade"}, true},
		{"repeated_headers", "GET", []string{"h2c", "WebSocket"}, []string{"keep-alive", "Upgrade"}, true},
		{"missing_connection", "GET", []string{"websocket"}, nil, false},
		{"wrong_connection", "GET", []string{"websocket"}, []string{"keep-alive"}, false},
		{"post", "POST", []string{"websocket"}, []string{"Upgrade"}, false},
		{"unicode_case", "GET", []string{"websocKet"}, []string{"Upgrade"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := AuthMiddleware(cfg, http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
				identity := Identity(request.Context())
				if (identity != nil) != test.authenticated || (BearerToken(request.Context()) != "") != test.authenticated {
					t.Fatal("query token crossed HTTP/WS boundary", test.name)
				}
				if identity != nil && identity.ID() != 42 {
					t.Fatal("identity changed")
				}
			}))
			request := httptest.NewRequest(test.method, "/luvia?token="+url.QueryEscape(token), nil)
			request.Header["Upgrade"] = test.upgrade
			request.Header["Connection"] = test.connection
			handler.ServeHTTP(httptest.NewRecorder(), request)
		})
	}
}

func TestGatewayWebSocketUpgradeIdentityAndExpiry(t *testing.T) {
	cfg := testCfg()
	server := upgradeIdentityGateway(t, cfg)
	token, err := auth.SignWithExpiry(cfg, testClaimsJSON(`{"id":9007199254740993}`), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: upgradeIdentityTransport{transport}}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/luvia?token="+url.QueryEscape(token), &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	for _, message := range []struct {
		kind          websocket.MessageType
		request, want []byte
	}{
		{websocket.MessageText, []byte(`{"$id":1,"$api":"identity"}`), []byte(`{"$id":1,"data":9007199254740993}`)},
		{websocket.MessageBinary, []byte{api.BinaryFrameCallRequest, 2, 7, 0, 0}, codec.AppendSvarint([]byte{api.BinaryFrameCallSuccess, 2}, 9007199254740993)},
	} {
		if err := connection.Write(ctx, message.kind, message.request); err != nil {
			t.Fatal(err)
		}
		kind, response, err := connection.Read(ctx)
		if err != nil || kind != message.kind || !bytes.Equal(response, message.want) {
			t.Fatalf("identity response=%s error=%v", response, err)
		}
	}
	if _, _, err := connection.Read(ctx); websocket.CloseStatus(err) != api.WebSocketTokenExpired {
		t.Fatal("upgrade did not retain verified token expiry", err)
	}
}

func upgradeIdentityGateway(t *testing.T, cfg *auth.Config) *httptest.Server {
	t.Helper()
	t.Setenv("JWT_SECRET", cfg.Secret)
	t.Setenv("CORS_ORIGIN", "*")
	t.Setenv("APP_ENV", "production")
	auth.ResetConfig()
	t.Cleanup(auth.ResetConfig)
	gateway := New()
	if err := gateway.Router.Schema.RegisterAPI(&schema.API{ID: 7, Name: "identity", ReturnType: "Int"}); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Router.Registry.RegisterSchemaAPIs(gateway.Router.Schema); err != nil {
		t.Fatal(err)
	}
	gateway.Router.Handle("identity", func(ctx context.Context, request *api.Request) error {
		identity := Identity(ctx)
		if identity == nil {
			return errors.Unauthorized
		}
		request.Buf.B = codec.AppendSvarint(request.Buf.B, identity.ID())
		return nil
	})
	if err := gateway.Router.Prepare(); err != nil {
		t.Fatal(err)
	}
	mux, _ := buildTestMux(t, gateway, "upgrade-audit")
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestGatewayHTTPQueryTokenRequiresUpgrade(t *testing.T) {
	cfg := testCfg()
	server := upgradeIdentityGateway(t, cfg)
	token, err := auth.Sign(cfg, testClaimsJSON(`{"id":42}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"json", "binary"} {
		for _, test := range []struct {
			name, upgrade, connection string
			bearer                    bool
			status                    int
		}{
			{"ordinary", "", "", false, http.StatusUnauthorized},
			{"mixed", "WebSocket", "", false, http.StatusUnauthorized},
			{"wrong_connection", "websocket", "keep-alive", false, http.StatusUnauthorized},
			{"post_upgrade", "WebSocket", "Upgrade", false, http.StatusMethodNotAllowed},
			{"bearer_control", "", "", true, http.StatusOK},
		} {
			t.Run(mode+"/"+test.name, func(t *testing.T) {
				body := []byte(`{"$api":"identity"}`)
				if mode == "binary" {
					body = []byte{7, 0, 0}
				}
				request := httptest.NewRequest(http.MethodPost, server.URL+"/luvia?token="+url.QueryEscape(token), bytes.NewReader(body))
				request.RequestURI = ""
				request.Header.Set("X-Luxo-Mode", mode)
				request.Header.Set("Upgrade", test.upgrade)
				request.Header.Set("Connection", test.connection)
				if test.bearer {
					request.Header.Set("Authorization", "Bearer "+token)
				}
				response, err := server.Client().Do(request)
				if err != nil {
					t.Fatal(err)
				}
				_, readErr := io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if readErr != nil || response.StatusCode != test.status {
					t.Fatal("HTTP query token boundary", response.StatusCode, readErr)
				}
			})
		}
	}
}

type upgradeIdentityTransport struct{ base http.RoundTripper }

func (transport upgradeIdentityTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header["Upgrade"] = []string{"h2c", "WebSocket"}
	request.Header["Connection"] = []string{"keep-alive", "UpGRADE"}
	return transport.base.RoundTrip(request)
}
