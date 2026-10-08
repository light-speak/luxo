package api

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/schema"
)

func BenchmarkWSJSONSubscriptionMetadata(b *testing.B) {
	for _, apiName := range []string{`"watchIdentity"`, `"\u0077atchIdentity"`} {
		router := subscriptionMetadataBenchmarkRouter(b)
		raw := map[string]json.RawMessage{"$id": []byte("1"), "$sub": []byte(apiName), "value": []byte("9223372036854775807")}
		for _, version := range []struct {
			name  string
			parse func(map[string]json.RawMessage) (*Request, map[string]any, error)
		}{{"before", router.formerWSJSONSubscriptionMetadata}, {"after", router.prepareWSJSONSubscription}} {
			b.Run(apiName+"/"+version.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if _, _, err := version.parse(raw); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func subscriptionMetadataBenchmarkRouter(t testing.TB) *Router {
	t.Helper()
	router := testWSRouter()
	if err := router.Registry.Register("watchIdentity", 71); err != nil {
		t.Fatal(err)
	}
	if err := router.Registry.RegisterParams("watchIdentity", []ParamMeta{{Name: "value", Type: "Int", FieldID: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := router.Schema.RegisterAPI(&schema.API{
		ID: 71, Name: "watchIdentity", ReturnType: "Int", Stream: true,
		Params: []schema.Param{{ID: 1, Name: "value", Type: schema.FieldInt}},
	}); err != nil {
		t.Fatal(err)
	}
	return router
}

func TestWSJSONSubscriptionMetadataBenchmarkFixture(t *testing.T) {
	router := subscriptionMetadataBenchmarkRouter(t)
	for _, apiName := range []string{`"watchIdentity"`, `"\u0077atchIdentity"`} {
		for _, parse := range []func(map[string]json.RawMessage) (*Request, map[string]any, error){
			router.formerWSJSONSubscriptionMetadata, router.prepareWSJSONSubscription,
		} {
			raw := map[string]json.RawMessage{"$id": []byte("1"), "$sub": []byte(apiName), "value": []byte("9223372036854775807")}
			req, params, err := parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			if req.API != "watchIdentity" || params["value"] != int64(math.MaxInt64) {
				t.Fatalf("benchmark request lost API or exact integer: %s, %v", req.API, params)
			}
		}
	}
}

// Preserve the removed metadata preparation before any ACK or subscription
// allocation. Both paths retain the existing canonical parameter conversion.
func (rt *Router) formerWSJSONSubscriptionMetadata(raw map[string]json.RawMessage) (*Request, map[string]any, error) {
	var apiName string
	if err := json.Unmarshal(raw["$sub"], &apiName); err != nil || apiName == "" {
		return nil, nil, fmt.Errorf("invalid $sub API name")
	}
	req, params, err := rt.parseJSONSubscription(raw)
	if err != nil {
		return nil, nil, err
	}
	if !rt.isStreamAPI(apiName) {
		return nil, nil, fmt.Errorf("%s is not a stream API", apiName)
	}
	return req, params, nil
}

func BenchmarkJSONListMetadata(b *testing.B) {
	for _, item := range []struct{ name, key, value string }{
		{"filters", "$filters", `{"field":"projectId","op":"eq","value":9223372036854775807}`},
		{"escaped", "$filters", `{"fi\u0065ld":"name","op":"eq","value":"\u6771\u4eac"}`},
		{"sorters", "$sorters", `{"field":"createdAt","order":"desc"}`},
		{"chinese72KB", "$filters", `{"field":"name","op":"eq","value":"` + strings.Repeat("東京", 12000) + `"}`},
	} {
		for _, count := range []int{0, 1, 16, 100} {
			if item.name == "chinese72KB" && count != 1 {
				continue
			}
			raw := "[]"
			if count > 0 {
				raw = "[" + strings.Repeat(item.value+",", count-1) + item.value + "]"
			}
			benchmarkListMetadata(b, fmt.Sprintf("%s/%d", item.name, count), item.key, raw)
		}
	}
}

func benchmarkListMetadata(b *testing.B, name, key, raw string) {
	b.Run(name+"/metadata", func(b *testing.B) {
		values := map[string]json.RawMessage{key: []byte(raw)}
		b.ReportAllocs()
		for b.Loop() {
			var req Request
			if err := req.parseListParams(values); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run(name+"/request", func(b *testing.B) {
		body := `{"$api":"list","` + key + `":` + raw + `}`
		reader := strings.NewReader(body)
		req := &http.Request{Body: io.NopCloser(reader)}
		b.ReportAllocs()
		for b.Loop() {
			reader.Reset(body)
			if _, err := ParseRequest(req); err != nil {
				b.Fatal(err)
			}
		}
	})
}
