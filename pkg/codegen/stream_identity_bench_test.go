package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

var streamEnumGenerationResult *GenerateResult

func BenchmarkGenerateStreamIdentityEnumOwnership(b *testing.B) {
	for _, mode := range []struct {
		name, owner string
		shared      bool
	}{{"local", "feed", false}, {"remote", "auth", false}, {"shared", "auth", true}, {"no_enums", "feed", false}} {
		b.Run(mode.name, func(b *testing.B) {
			source := streamIdentityEnumSource
			var files []*ast.File
			if mode.name == "no_enums" {
				source = `model User @noTime @withAuth(stores: [id, role]) { id: Int @id role: Boolean }
event Changed(value: Int)
api sameRole(): Int @auth @stream(Changed) { my.role == my.role }
api negatedRole(): Int @auth @stream(Changed) { !(my.role != my.role) }
api skippedRole(): Int @auth @stream(Changed) { true || my.role == my.role }`
			} else {
				files = append(files, parseStreamEnumSource(b, "origin/"+mode.owner+"/types.luxo", "enum Role { MEMBER BLOCKED }"))
			}
			files = append(files, parseStreamEnumSource(b, "origin/feed/api.luxo", source))
			input := semantic.Analyze(files)
			if len(input.Errors) != 0 {
				b.Fatal(input.Errors)
			}
			context := BuildEventContext(input.Files, "example.com/stream-enum")
			if mode.shared {
				context.SharedEnumPackage = "example.com/stream-enum/internal/luxotypes"
			}
			ids := completeFixtureAPIIDs(input, StableIDs{
				ModelFields: map[string]map[string]int{"User": {"id": 1, "role": 2}},
				EventFields: map[string]map[string]int{"Changed": {"value": 1}},
			})
			generator, err := NewGenerator(GeneratorConfig{Events: context, IDs: ids})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				output, err := generator.Generate(input, "feed_luxo", nil)
				if err != nil {
					b.Fatal(err)
				}
				streamEnumGenerationResult = output
			}
		})
	}
}

func TestGeneratedNativeStreamIdentityPerformanceFixture(t *testing.T) {
	input := analyzePermissionFixture(t, nativeVisibilitySource)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	generated, err := Generate(input, "generated", GeneratorConfig{Driver: DriverPG, IDs: nativeVisibilityStableIDs()})
	if err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("LUXO_NATIVE_STREAM_FIXTURE"); dir != "" {
		exportNativeIdentityFixture(t, dir, generated)
		return
	}
	runGeneratedCompilation(t, generated, nativeIdentityBenchmarkAssertions)
}

func exportNativeIdentityFixture(t *testing.T, dir string, generated *GenerateResult) {
	t.Helper()
	root, err := filepath.Abs("../../.tmp/audit-native-stream-cost")
	if err != nil {
		t.Fatal(err)
	}
	target, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == "." || filepath.IsAbs(relative) || strings.HasPrefix(relative, "..") {
		t.Fatalf("native stream fixture must be below %s", root)
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, data := range generated.Files {
		if filepath.Base(name) != name {
			t.Fatalf("invalid generated file %q", name)
		}
		if err := os.WriteFile(filepath.Join(target, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(target, "native_identity_test.go"), []byte(nativeIdentityBenchmarkAssertions), 0o600); err != nil {
		t.Fatal(err)
	}
}

const nativeIdentityBenchmarkAssertions = `package generated

import (
	"bytes"
	"context"
	"fmt"
	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/auth"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"os"
	"testing"
	"time"
)

type benchmarkIdentityKey struct{}
type benchmarkStreamResolver struct {
	count  int
	object Profile
	list   []Profile
}

func (r *benchmarkStreamResolver) HandleWatch(_ context.Context, _ *WatchStreamParams, _ any, stream *api.TypedStream[Profile]) error {
	for range r.count {
		if err := stream.Send(r.object); err != nil {
			return err
		}
	}
	return nil
}
func (r *benchmarkStreamResolver) HandleWatchList(_ context.Context, _ *WatchListStreamParams, _ any, stream *api.TypedStream[[]Profile]) error {
	for range r.count {
		if err := stream.Send(r.list); err != nil {
			return err
		}
	}
	return nil
}

func benchmarkNativeClaims(tb testing.TB, id int64) auth.Claims {
	tb.Helper()
	cfg := &auth.Config{Secret: "native-identity-benchmark", Expires: time.Hour}
	token, err := auth.SignForSubject(cfg, "User", func(dst []byte) ([]byte, error) { return fmt.Appendf(dst, "{\"id\":%d}", id), nil })
	if err != nil {
		tb.Fatal(err)
	}
	claims, err := auth.Verify(cfg, token)
	if err != nil {
		tb.Fatal(err)
	}
	return claims
}

func newIdentityBenchmarkRouter(tb testing.TB, count int) *api.Router {
	tb.Helper()
	secret, zero := "private-for-42", "private-for-zero"
	value := Profile{Id: 1, Secret: &secret, Zero: &zero, Phone: "13812341234", Detail: &Detail{Secret: &secret, Phone: "13812341234"}}
	router := api.NewRouter()
	router.IdentityExtractor = func(ctx context.Context) any { return ctx.Value(benchmarkIdentityKey{}) }
	if err := RegisterSchema(router.Schema); err != nil {
		tb.Fatal(err)
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		tb.Fatal(err)
	}
	resolver := &benchmarkStreamResolver{count: count, object: value, list: []Profile{value, value}}
	if err := RegisterStreams(router, nil, resolver); err != nil {
		tb.Fatal(err)
	}
	if err := router.Prepare(); err != nil {
		tb.Fatal(err)
	}
	return router
}

func identityBenchmarkMask(tb testing.TB, minimal bool) []byte {
	tb.Helper()
	fields := byte(31)
	if minimal {
		fields = 1
	}
	child, err := codec.AppendSelectionMask(nil, []byte{3}, nil)
	if err != nil {
		tb.Fatal(err)
	}
	var children []codec.SelectionMaskChild
	if !minimal {
		children = []codec.SelectionMaskChild{{FieldID: 5, Mask: child}}
	}
	mask, err := codec.AppendSelectionMask(nil, []byte{fields}, children)
	if err != nil {
		tb.Fatal(err)
	}
	return mask
}

func identityBenchmarkRequest(tb testing.TB, router *api.Router, name string, minimal bool) *api.Request {
	tb.Helper()
	wire, err := api.EncodeBinaryRequest(router.Schema.APIs[name].ID, identityBenchmarkMask(tb, minimal), nil, nil)
	if err != nil {
		tb.Fatal(err)
	}
	req, err := router.Registry.ParseBinaryRequest(wire)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { api.PutRequest(req) })
	return req
}

func TestNativeIdentityEncodingControls(t *testing.T) {
	router := newIdentityBenchmarkRouter(t, 2)
	baseline := os.Getenv("LUXO_NATIVE_IDENTITY_BASELINE") == "1"
	for _, name := range []string{"watch", "watchList"} {
		for _, minimal := range []bool{false, true} {
			for _, id := range []int64{42, 99} {
				req := identityBenchmarkRequest(t, router, name, minimal)
				ctx := context.WithValue(t.Context(), benchmarkIdentityKey{}, benchmarkNativeClaims(t, id))
				stream, err := router.OpenInternalStream(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for body := range stream.Messages() {
					count++
					if count == 1 {
						t.Logf("payload name=%s minimal=%t id=%d hex=%x", name, minimal, id, body)
					}
					text, err := router.StreamPayloadJSON(name, body)
					if err != nil {
						t.Fatal(err)
					}
					if bytes.Contains(body, []byte("private-for-42")) != (id == 42 && !minimal && !baseline) {
						t.Fatalf("wrong identity output %s", text)
					}
					if bytes.Contains(body, []byte("13812341234")) {
						t.Fatal("raw phone leaked")
					}
				}
				stream.Close()
				if count != 2 || stream.Err() != nil {
					t.Fatal(count, stream.Err())
				}
			}
		}
	}
}

func BenchmarkGeneratedNativeStreamIdentity(b *testing.B) {
	for _, name := range []string{"watch", "watchList"} {
		for _, projection := range []string{"Public", "Visible", "IDOnly"} {
			for _, messages := range []int{1, 32} {
				b.Run(fmt.Sprintf("%s/%s/messages=%d", name, projection, messages), func(b *testing.B) {
					router := newIdentityBenchmarkRouter(b, messages)
					id := int64(42)
					if projection == "Public" {
						id = 99
					}
					ctx := context.WithValue(b.Context(), benchmarkIdentityKey{}, benchmarkNativeClaims(b, id))
					req := identityBenchmarkRequest(b, router, name, projection == "IDOnly")
					expected := benchmarkNativeStreamPayload(b, router, ctx, req, messages)
					b.ReportAllocs()
					for b.Loop() {
						stream, err := router.OpenInternalStream(ctx, req)
						if err != nil {
							b.Fatal(err)
						}
						count := 0
						for body := range stream.Messages() {
							if !bytes.Equal(body, expected) {
								b.Fatal("payload changed")
							}
							count++
						}
						stream.Close()
						if count != messages || stream.Err() != nil {
							b.Fatal(count, stream.Err())
						}
					}
				})
			}
		}
	}
}

func benchmarkNativeStreamPayload(tb testing.TB, router *api.Router, ctx context.Context, req *api.Request, messages int) []byte {
	tb.Helper()
	stream, err := router.OpenInternalStream(ctx, req)
	if err != nil {
		tb.Fatal(err)
	}
	defer stream.Close()
	var expected []byte
	count := 0
	for body := range stream.Messages() {
		if count == 0 {
			expected = append([]byte(nil), body...)
		} else if !bytes.Equal(body, expected) {
			tb.Fatal("unstable fixture payload")
		}
		count++
	}
	if count != messages || stream.Err() != nil {
		tb.Fatal(count, stream.Err())
	}
	return expected
}
`
