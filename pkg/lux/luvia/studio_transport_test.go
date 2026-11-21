package luvia

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStudioURLPolicy(t *testing.T) {
	for _, test := range []struct {
		name, url, allow string
		valid            bool
	}{
		{"https", "https://studio.example.com/base/", "", true},
		{"ipv4", "http://127.0.0.1:9100", "", true},
		{"ipv6", "http://[::1]:9100", "", true},
		{"localhost", "http://localhost:9100", "", true},
		{"remote", "http://studio.example.com", "", false},
		{"private explicit", "http://studio:9100", "true", true},
		{"private disabled", "http://studio:9100", "false", false},
		{"bad opt in", "https://studio.example.com", "typo", false},
		{"lookalike", "http://localhost.example.com", "", false},
		{"userinfo", "https://user:secret@studio.example.com", "", false},
		{"query", "https://studio.example.com?key=secret", "", false},
		{"fragment", "https://studio.example.com#section", "", false},
		{"scheme", "ftp://studio.example.com", "true", false},
		{"relative", "/studio", "", false},
		{"malformed", "://", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("LUXO_STUDIO_URL", test.url)
			t.Setenv("LUXO_STUDIO_ALLOW_INSECURE_HTTP", test.allow)
			got, err := studioURLFromEnv()
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v, error=%v", test.valid, err)
			}
			if test.valid && got != strings.TrimRight(test.url, "/") {
				t.Fatalf("URL=%q", got)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("error leaks credentials")
			}
		})
	}
}

func TestStudioClientRejectsCredentialRedirects(t *testing.T) {
	var forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	for _, code := range []int{301, 302, 303, 307, 308} {
		source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL, code)
		}))
		req, err := http.NewRequest(http.MethodPost, source.URL, strings.NewReader(`{"apiKey":"test-secret"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer test-secret")
		resp, err := newStudioHTTPClient().Do(req)
		source.Close()
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != code {
			t.Fatalf("redirect %d followed", code)
		}
	}
	if forwarded.Load() != 0 {
		t.Fatal("credential request forwarded")
	}
}

func TestInvalidStudioTransportStopsStartup(t *testing.T) {
	t.Setenv("LUXO_STUDIO_URL", "http://studio.example.com")
	t.Setenv("LUXO_STUDIO_ALLOW_INSECURE_HTTP", "false")
	t.Setenv("LUXO_API_KEY", "test-key")
	t.Setenv("LUXO_PROJECT_ID", testStudioProjectID)
	if NewGatewayRegistrar("0") != nil || NewMetricsCollector() != nil {
		t.Fatal("insecure remote URL started a credential exporter")
	}
	if err := New().Serve("test"); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("Serve must reject insecure Studio transport: %v", err)
	}
}

func TestGatewayCloseCancelsStudioRequests(t *testing.T) {
	for _, operation := range []string{"register", "heartbeat"} {
		t.Run(operation, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if strings.Contains(string(body), "deregister") {
					w.WriteHeader(http.StatusOK)
					return
				}
				close(started)
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			gr := &GatewayRegistrar{studioURL: server.URL, client: newStudioHTTPClient(),
				done: make(chan struct{}), ctx: ctx, cancel: cancel, startedAt: time.Now()}
			gr.worker.Add(1)
			go func() {
				defer gr.worker.Done()
				if operation == "register" {
					gr.register()
				} else {
					gr.heartbeat()
				}
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("request did not start")
			}
			closed := make(chan struct{})
			go func() { gr.Close(); close(closed) }()
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("Close did not cancel in-flight request")
			}
		})
	}
}
