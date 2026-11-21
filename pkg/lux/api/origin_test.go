package api

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestOriginPolicyNormalization(t *testing.T) {
	for _, test := range []struct{ configured, origin string }{
		{"https://app.example", "HTTPS://APP.EXAMPLE:0443"},
		{"http://app.example:80", "http://app.example"},
		{"https://[2001:db8::1]", "https://[2001:0db8:0:0:0:0:0:1]:443"},
		{"https://xn--bcher-kva.example", "https://bücher.example"},
		{" https://first.example, https://second.example:8443 ", "https://second.example:8443"},
		{"https://app.example:0", "https://app.example:000"},
		{"null", "null"}, {"*, null", "null"}, {"*", "http://other.example"},
		{"*, https://app.example", "https://app.example"}, {"https://app.example", "https://app.example"},
	} {
		policy, err := ParseOriginPolicy(test.configured)
		if err != nil {
			t.Fatal(test, err)
		}
		request := httptest.NewRequest(http.MethodPost, "/", nil)
		request.Header.Set("Origin", test.origin)
		if _, allowed := policy.Match(request); !allowed {
			t.Error("normalized origin rejected", test)
		}
	}
}

func TestOriginPolicyInvalidValues(t *testing.T) {
	policy, err := ParseOriginPolicy("*")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		"", "null", "https://", "relative/path", "ftp://app.example", "https:app.example",
		"https://user:pass@app.example", "https://app.example/", "https://app.example/path",
		"https://app.example?", "https://app.example#", "https://app.example\\evil",
		"https://app.example https://other.example", " https://app.example", "https://app.example\t",
		"https://app.example:", "https://app.example:-1", "https://app.example:65536", "https://app.example:abc",
		"https://*.example", "https://[example.com]", "https://[127.0.0.1]", "https://[]",
		"https://[::1%25zone]", "https://[invalid::host]", "https://a%2eb.example",
		"https://[::1", "https://[::1]extra", "https://[::1]:", "https://[::1]:65536",
		"https://app.example:+80", "https://app.example:80:90", "https://app.example\x00",
	} {
		t.Run(value, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", nil)
			request.Header.Set("Origin", value)
			if _, allowed := policy.Match(request); allowed {
				t.Error("wildcard accepted invalid/opaque origin", value)
			}
			if value != "null" {
				if got, err := ParseOriginPolicy(value); err == nil || got != nil {
					// Configuration whitespace separates entries and is intentionally trimmed.
					if value != " https://app.example" && value != "https://app.example\t" {
						t.Error("invalid configuration accepted", value)
					}
				}
			}
		})
	}
}

func TestOriginPolicyHeaderAndPortBoundaries(t *testing.T) {
	policy, err := ParseOriginPolicy("https://app.example")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	if _, allowed := policy.Match(request); !allowed {
		t.Fatal("native client rejected")
	}
	for _, values := range [][]string{nil, {}, {""}, {"https://app.example", "https://app.example"},
		{"https://app.example,http://evil.example"}, {"null"}, {"http://app.example"}, {"https://app.example:444"}, {"https://other.example"}} {
		request.Header["Origin"] = values
		if _, allowed := policy.Match(request); allowed {
			t.Error("unlisted or malformed origin accepted", values)
		}
	}
	for _, config := range []string{",", "*,", ",null", "https://app.example,,null", "*,broken"} {
		if policy, err := ParseOriginPolicy(config); err == nil || policy != nil {
			t.Error("invalid list accepted", config)
		}
	}
}

func TestOriginPolicyPrepareAndRetry(t *testing.T) {
	router := NewRouter()
	router.WSOrigins = []string{"bad"}
	if err := router.Prepare(); err == nil {
		t.Fatal("invalid origin reached prepared state")
	}
	router.WSOrigins = []string{"https://app.example", "null"}
	router.WSAllowAllOrigins = true
	if err := router.Prepare(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Origin", "null")
	policy, err := router.configuredOriginPolicy()
	if err != nil {
		t.Fatal(err)
	}
	if _, allowed := policy.Match(request); !allowed {
		t.Fatal("explicit null lost under wildcard")
	}
}

func TestOriginPolicySharedSnapshot(t *testing.T) {
	policy, err := ParseOriginPolicy("https://app.example")
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter()
	router.SetOriginPolicy(policy)
	if err := router.Prepare(); err != nil {
		t.Fatal(err)
	}
	if got, err := router.configuredOriginPolicy(); err != nil || got != policy {
		t.Fatal("router replaced shared policy")
	}
	for range 8 {
		t.Run("concurrent", func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Origin", "https://APP.example:443")
			if origin, allowed := policy.Match(request); !allowed || origin != "https://APP.example:443" {
				t.Fatal(origin, allowed)
			}
		})
	}
}

func TestOriginPolicyConcurrentPublication(t *testing.T) {
	policy, err := ParseOriginPolicy("https://app.example")
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter()
	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Go(func() {
		<-start
		for range 1024 {
			router.SetOriginPolicy(policy)
		}
	})
	workers.Go(func() {
		<-start
		for range 1024 {
			if _, err := router.configuredOriginPolicy(); err != nil {
				t.Error(err)
			}
		}
	})
	close(start)
	workers.Wait()
	if got, err := router.configuredOriginPolicy(); err != nil || got != policy {
		t.Fatal("published policy lost", got, err)
	}
}

func TestOriginPolicyConcurrentPreparePublication(t *testing.T) {
	policy, err := ParseOriginPolicy("https://app.example")
	if err != nil {
		t.Fatal(err)
	}
	for range 128 {
		router := NewRouter()
		start := make(chan struct{})
		var workers sync.WaitGroup
		workers.Go(func() {
			<-start
			if err := router.Prepare(); err != nil {
				t.Error(err)
			}
		})
		workers.Go(func() {
			<-start
			router.SetOriginPolicy(policy)
		})
		close(start)
		workers.Wait()
		if err := router.Prepare(); err != nil {
			t.Fatal(err)
		}
		if got, err := router.configuredOriginPolicy(); err != nil || got != policy {
			t.Fatal("cached fallback replaced published policy", got, err)
		}
	}
}

func TestOriginPolicyNilResetsLegacyStartupPolicy(t *testing.T) {
	policy, err := ParseOriginPolicy("https://app.example")
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter()
	router.WSOrigins = []string{"https://legacy.example"}
	router.SetOriginPolicy(policy)
	router.SetOriginPolicy(nil)
	got, err := router.configuredOriginPolicy()
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Origin", "https://legacy.example")
	if _, allowed := got.Match(request); !allowed {
		t.Fatal("nil policy did not preserve legacy startup configuration")
	}
}
