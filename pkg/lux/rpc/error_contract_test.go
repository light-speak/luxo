package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/api"
	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

type rpcDepthData string

func (d rpcDepthData) AppendJSON(dst []byte) ([]byte, error)          { return append(dst, d...), nil }
func (d rpcDepthData) AppendText(dst []byte, _ string) ([]byte, bool) { return dst, false }

func TestRPCErrorDataIndependentJSONDepth(t *testing.T) {
	for _, depth := range []int{127, 128, 129} {
		raw := strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth)
		direct := errorContractRouter(t, func(context.Context, *api.Request) error {
			return luxerrors.BadRequest.WithData(rpcDepthData(raw))
		})
		client := NewClient(startTestRPCServer(t, NewServer(direct)))
		t.Cleanup(func() { client.Close() })
		_, err := client.CallWithMaskContext(t.Context(), "", 1, nil, nil)
		appErr, ok := errors.AsType[*luxerrors.AppError](err)
		if !ok {
			t.Fatalf("depth %d: expected declared error, got %v", depth, err)
		}
		if depth > 128 {
			if appErr.Code != 500 || appErr.Data != nil {
				t.Fatalf("deep data exposed: %+v", appErr)
			}
			continue
		}
		if appErr.Code != 400 || appErr.Data == nil {
			t.Fatalf("valid data rejected: %+v", appErr)
		}
		data, encodeErr := appErr.Data.AppendJSON(nil)
		if encodeErr != nil || string(data) != raw {
			t.Fatalf("depth %d: %q / %v", depth, data, encodeErr)
		}
	}
}

func errorContractRouter(t *testing.T, handler api.HandlerFunc) *api.Router {
	t.Helper()
	router := api.NewRouter()
	if err := router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "reject", ReturnType: "Int"}); err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	router.Handle("reject", handler)
	if err := router.Prepare(); err != nil {
		t.Fatal(err)
	}
	return router
}

func TestRPCErrorPrivacyMatchesDirectHTTP(t *testing.T) {
	private := luxerrors.New("Private", 503, "private diagnostic").WithData(rpcPrecisionData{})
	private.Internal = true
	for _, sample := range []struct {
		name    string
		source  error
		code    int
		message string
		data    bool
	}{
		{"public-500", luxerrors.New("DeclaredFailure", 500, "error.declared").WithData(rpcPrecisionData{}).WithCause(errors.New("private diagnostic")), 500, "error.declared", true},
		{"public-503", luxerrors.New("Busy", 503, "error.busy").WithData(rpcPrecisionData{}).WithCause(errors.New("private diagnostic")), 503, "error.busy", true},
		{"private", private, 503, "error.internal", false},
		{"unexpected", errors.New("private diagnostic"), 500, "error.internal", false},
		{"missing-code", luxerrors.New("Private", 0, "private diagnostic"), 500, "error.internal", false},
		{"success-code", luxerrors.New("Private", 200, "private diagnostic"), 500, "error.internal", false},
		{"redirect-code", luxerrors.New("Private", 302, "private diagnostic"), 500, "error.internal", false},
		{"oversized-code", luxerrors.New("Private", 1000, "private diagnostic"), 500, "error.internal", false},
		{"negative-code", luxerrors.New("Private", -1, "private diagnostic"), 500, "error.internal", false},
		{"nil-error", (*luxerrors.AppError)(nil), 500, "error.internal", false},
		{"lower-bound", luxerrors.New("Rejected", 400, "error.rejected"), 400, "error.rejected", false},
		{"upper-bound", luxerrors.New("Rejected", 599, "error.rejected"), 599, "error.rejected", false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			direct := errorContractRouter(t, func(context.Context, *api.Request) error { return sample.source })
			client := NewClient(startTestRPCServer(t, NewServer(direct)))
			defer client.Close()
			gateway := errorContractRouter(t, func(ctx context.Context, _ *api.Request) error {
				_, err := client.CallWithMaskContext(ctx, "", 1, nil, nil)
				return err
			})
			for _, router := range []*api.Router{direct, gateway} {
				for _, binary := range []bool{false, true} {
					body := []byte(`{"$api":"reject"}`)
					contentType := "application/json"
					if binary {
						var err error
						body, err = api.EncodeBinaryRequest(1, nil, nil, nil)
						if err != nil {
							t.Fatal(err)
						}
						contentType = "application/x-luxo"
					}
					request := httptest.NewRequest("POST", "/luvia", bytes.NewReader(body))
					request.Header.Set("Content-Type", contentType)
					if binary {
						request.Header.Set("X-Luxo-Mode", "binary")
					}
					response := httptest.NewRecorder()
					router.ServeHTTP(response, request)
					if response.Code != sample.code {
						t.Fatalf("status=%d body=%s", response.Code, response.Body)
					}
					var name, message string
					var data []byte
					if binary {
						value, err := api.DecodeBinaryError(response.Body.Bytes(), 0)
						if err != nil {
							t.Fatal(err)
						}
						name, message, data = value.Name, value.Message, value.Data
					} else {
						var value struct {
							Name    string          `json:"error"`
							Message string          `json:"message"`
							Data    json.RawMessage `json:"data"`
						}
						if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
							t.Fatal(err)
						}
						name, message, data = value.Name, value.Message, value.Data
					}
					expectedName := "Internal"
					if declared, ok := errors.AsType[*luxerrors.AppError](sample.source); ok && declared != nil && declared.Code >= 400 && declared.Code <= 599 {
						expectedName = declared.Name
					}
					if name != expectedName {
						t.Fatalf("error name changed: %q != %q", name, expectedName)
					}
					if message != sample.message {
						t.Fatalf("gateway=%t binary=%t message=%q", router == gateway, binary, message)
					}
					if sample.data {
						if string(data) != rpcPrecisionJSON {
							t.Fatal("error data changed", string(data))
						}
					} else if len(data) != 0 && string(data) != "null" {
						t.Fatal("private data exposed", string(data))
					}
					if bytes.Contains(response.Body.Bytes(), []byte("private diagnostic")) {
						t.Fatal("private diagnostic exposed")
					}
				}
			}
		})
	}
}

func TestRPCPublicServerErrorPreservedForStreamPhases(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		name := "prepare"
		if terminal {
			name = "terminal"
		}
		t.Run(name, func(t *testing.T) {
			router := api.NewRouter()
			if err := router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "watch", ReturnType: "Int", Stream: true}); err != nil {
				t.Fatal(err)
			}
			if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
				t.Fatal(err)
			}
			rejected := luxerrors.New("Busy", 503, "error.busy").WithData(rpcPrecisionData{})
			router.HandleStreamPrepared("watch", func(context.Context, *api.Request, any) (api.PreparedStreamHandler, error) {
				if !terminal {
					return nil, rejected
				}
				return func(_ context.Context, stream *api.Stream) error {
					if err := stream.Send([]byte{84}); err != nil {
						return err
					}
					return rejected
				}, nil
			})
			client := NewClient(startTestRPCServer(t, NewServer(router)))
			defer client.Close()
			sub, err := client.SubscribeContext(t.Context(), "", 1, nil, nil)
			if terminal {
				if err != nil || sub == nil {
					t.Fatal("missing stream ACK", err)
				}
				defer sub.Close()
				count := 0
				for value := range sub.Messages() {
					if !bytes.Equal(value, []byte{84}) {
						t.Fatal("changed data", value)
					}
					count++
				}
				if count != 1 {
					t.Fatal("lost accepted data", count)
				}
				err = <-sub.Errors()
			} else if sub != nil {
				sub.Close()
				t.Fatal("rejected preparation acknowledged")
			}
			value, ok := errors.AsType[*luxerrors.AppError](err)
			if !ok || value.Code != 503 || value.Name != "Busy" || value.Message != "error.busy" || value.Internal || value.Data == nil {
				t.Fatalf("stream error changed: %+v", value)
			}
			raw, err := value.Data.AppendJSON(nil)
			if err != nil || string(raw) != rpcPrecisionJSON {
				t.Fatal("stream data changed", string(raw), err)
			}
		})
	}
}
