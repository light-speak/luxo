package luvia

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// The exact old body is retained for a scoped comparison only.
func corsMiddlewareBeforeProtocolHeaders(next http.Handler) http.Handler {
	origin := envOr("CORS_ORIGIN", "*")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Luxo-Mode, X-Request-Id")
		w.Header().Set("Access-Control-Max-Age", "86400")

		// Security headers — XSS, clickjacking, MIME sniffing protection
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func BenchmarkCORSProtocolHeaders(b *testing.B) {
	for _, kind := range []string{"native", "browser", "preflight"} {
		for _, warm := range []bool{false, true} {
			for _, before := range []bool{true, false} {
				temperature := "cold"
				if warm {
					temperature = "warm"
				}
				version := "current"
				if before {
					version = "before"
				}
				b.Run(kind+"/"+temperature+"/"+version, func(b *testing.B) {
					benchmarkCORSHeaders(b, kind, warm, before)
				})
			}
		}
	}
}

func benchmarkCORSHeaders(b *testing.B, kind string, warm, before bool) {
	method := http.MethodPost
	if kind == "preflight" {
		method = http.MethodOptions
	}
	request := httptest.NewRequest(method, "/luvia", nil)
	if kind != "native" {
		request.Header.Set("Origin", "https://app.example")
	}
	var handler http.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	if before {
		handler = corsMiddlewareBeforeProtocolHeaders(handler)
	} else {
		handler = testCORSMiddleware(b, handler)
	}
	writer := httptest.NewRecorder()
	b.ReportAllocs()
	for b.Loop() {
		if !warm {
			writer = httptest.NewRecorder()
		}
		handler.ServeHTTP(writer, request)
	}
	if writer.Header().Get("X-Content-Type-Options") != "nosniff" {
		b.Fatal("security header lost")
	}
}

// Exact pre-policy middleware; invalid origins were not rejected.
func corsMiddlewareBeforeOriginPolicy(next http.Handler) http.Handler {
	origin := envOr("CORS_ORIGIN", "*")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS
		w.Header().Set("Access-Control-Allow-Origin", origin)

		// Security headers — XSS, clickjacking, MIME sniffing protection
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")

		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", corsRequestHeaders)
			w.Header().Set("Access-Control-Max-Age", "86400")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Header.Get("Origin") != "" {
			w.Header().Set("Access-Control-Expose-Headers", corsResponseHeaders)
		}
		next.ServeHTTP(w, r)
	})
}

func BenchmarkCORSOriginPolicy(b *testing.B) {
	for _, sample := range []struct {
		name, config, origin string
		cold, preflight      bool
	}{
		{"native-warm", "*", "", false, false}, {"native-cold", "*", "", true, false},
		{"wildcard-warm", "*", "https://app.example", false, false}, {"wildcard-cold", "*", "https://app.example", true, false},
		{"exact-warm", "https://app.example", "https://app.example", false, false}, {"exact-cold", "https://app.example", "https://app.example", true, false},
		{"preflight", "https://app.example", "https://app.example", true, true}, {"invalid", "*", "https://app.example/path", true, false},
	} {
		b.Run(sample.name, func(b *testing.B) {
			b.Setenv("CORS_ORIGIN", sample.config)
			method := http.MethodPost
			if sample.preflight {
				method = http.MethodOptions
			}
			request := httptest.NewRequest(method, "/luvia", nil)
			if sample.origin != "" {
				request.Header.Set("Origin", sample.origin)
			}
			var handler http.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
			if os.Getenv("LUXO_ORIGIN_BASELINE") == "1" {
				handler = corsMiddlewareBeforeOriginPolicy(handler)
			} else {
				handler = testCORSMiddleware(b, handler)
			}
			writer := httptest.NewRecorder()
			b.ReportAllocs()
			for b.Loop() {
				if sample.cold {
					writer = httptest.NewRecorder()
				}
				handler.ServeHTTP(writer, request)
			}
			if writer.Header().Get("X-Content-Type-Options") != "nosniff" {
				b.Fatal("security header lost")
			}
		})
	}
}
