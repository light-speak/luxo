package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"nhooyr.io/websocket"
)

// The old dispatch predicate is retained only for this local comparison.
func webSocketUpgradeBeforeTokens(request *http.Request) bool {
	return request.Header.Get("Upgrade") == "websocket"
}

func BenchmarkWebSocketOriginHandshake(b *testing.B) {
	for _, sample := range []string{"native", "listed"} {
		b.Run(sample, func(b *testing.B) {
			router := NewRouter()
			policy, err := ParseOriginPolicy("https://app.example")
			if err != nil {
				b.Fatal(err)
			}
			router.SetOriginPolicy(policy)
			router.WSOrigins = []string{"https://app.example"}
			if os.Getenv("LUXO_ORIGIN_BASELINE") == "1" {
				// The previous gateway passed only the hostname to the library.
				router.WSOrigins = []string{"app.example"}
			}
			if err := router.Prepare(); err != nil {
				b.Fatal(err)
			}
			server := httptest.NewServer(router)
			defer server.Close()
			address := "ws" + strings.TrimPrefix(server.URL, "http")
			headers := make(http.Header)
			if sample == "listed" {
				headers.Set("Origin", "https://app.example")
			}
			options := &websocket.DialOptions{HTTPHeader: headers}
			b.ReportAllocs()
			for b.Loop() {
				conn, _, err := websocket.Dial(b.Context(), address, options)
				if err != nil {
					b.Fatal(err)
				}
				if err := conn.Close(websocket.StatusNormalClosure, ""); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkOriginPolicyInitialization(b *testing.B) {
	for _, sample := range []string{"*", "https://app.example", "https://first.example,https://second.example,null", "*,invalid"} {
		b.Run(sample, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, err := ParseOriginPolicy(sample)
				if (err != nil) != (sample == "*,invalid") {
					b.Fatal(err)
				}
			}
		})
	}
}

var webSocketUpgradeSink bool
var originPolicySink *OriginPolicy
var originPolicyRouterSink *Router

func BenchmarkOriginPolicyStartup(b *testing.B) {
	policy, err := ParseOriginPolicy("https://app.example")
	if err != nil {
		b.Fatal(err)
	}
	for _, sample := range []string{"default", "published"} {
		b.Run(sample, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				router := NewRouterWithOptions(RouterOptions{})
				if sample == "published" {
					router.SetOriginPolicy(policy)
				}
				if err := router.Prepare(); err != nil {
					b.Fatal(err)
				}
				originPolicyRouterSink = router
			}
		})
	}
}

func BenchmarkOriginPolicyLookup(b *testing.B) {
	policy, err := ParseOriginPolicy("https://app.example")
	if err != nil {
		b.Fatal(err)
	}
	for _, sample := range []string{"published", "invalid_legacy"} {
		b.Run(sample, func(b *testing.B) {
			router := NewRouterWithOptions(RouterOptions{})
			if sample == "published" {
				router.SetOriginPolicy(policy)
			} else {
				router.WSOrigins = []string{"invalid"}
			}
			b.ReportAllocs()
			for b.Loop() {
				originPolicySink, err = router.configuredOriginPolicy()
				if (err != nil) != (sample == "invalid_legacy") {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkOriginPolicyPrepared(b *testing.B) {
	router := NewRouterWithOptions(RouterOptions{})
	if err := router.Prepare(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := router.Prepare(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWebSocketUpgradeDetection(b *testing.B) {
	for _, sample := range []struct{ name, upgrade string }{
		{"http", ""}, {"h2c", "h2c"}, {"lowercase", "websocket"},
		{"mixed", "WebSocket"}, {"list", "h2c, websocket"},
	} {
		request := httptest.NewRequest(http.MethodGet, "/luvia", nil)
		if sample.upgrade != "" {
			request.Header.Set("Upgrade", sample.upgrade)
			request.Header.Set("Connection", "keep-alive, Upgrade")
		}
		for _, candidate := range []struct {
			name  string
			check func(*http.Request) bool
		}{{"before", webSocketUpgradeBeforeTokens}, {"current", IsWebSocketUpgrade}} {
			b.Run(sample.name+"/"+candidate.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					webSocketUpgradeSink = candidate.check(request)
				}
			})
		}
	}
}
