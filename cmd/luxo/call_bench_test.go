package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
)

type cliJSONAuthorityTransport struct {
	request *http.Request
}

func (transport *cliJSONAuthorityTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.request = request
	if err := request.Body.Close(); err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"data":42}`)),
		Request:    request,
	}, nil
}

func BenchmarkCLIJSONAPIAuthority(b *testing.B) {
	for _, sample := range []struct {
		name, oldAPI string
		params       []string
	}{
		{"empty", `"requested"`, nil},
		{"scalars", `"requested"`, []string{"id=42", "name=alice", "enabled=true"}},
		{"nested", `"requested"`, []string{`payload={"$api":"nested","values":[9223372036854775807,1.234567890123456789]}`}},
		{"shadow_string", `"unexpected"`, []string{"$api=unexpected"}},
		{"shadow_null", `null`, []string{"$api=null"}},
	} {
		b.Run(sample.name, func(b *testing.B) {
			quietCLIOutput(b)
			transport := new(cliJSONAuthorityTransport)
			previousClient, previousHost, previousSelect := http.DefaultClient, callHost, callSelect
			http.DefaultClient, callHost, callSelect = &http.Client{Transport: transport}, "http://cli.invalid", ""
			b.Cleanup(func() { http.DefaultClient, callHost, callSelect = previousClient, previousHost, previousSelect })
			b.ReportAllocs()
			for b.Loop() {
				if err := callJSONMode("requested", sample.params); err != nil {
					b.Fatal(err)
				}
			}
			reader, err := transport.request.GetBody()
			if err != nil {
				b.Fatal(err)
			}
			data, err := io.ReadAll(reader)
			reader.Close()
			want := `"requested"`
			if os.Getenv("LUXO_CLI_API_AUTHORITY_BASELINE") == "1" {
				want = sample.oldAPI
			}
			if err != nil || !bytes.HasPrefix(data, []byte(`{"$api":`+want)) {
				b.Fatalf("unexpected request identity: %s %v", data, err)
			}
		})
	}
}

func BenchmarkCLIJSONInference(b *testing.B) {
	for _, raw := range []string{"42", "1.25", "true", "hello"} {
		before, _ := json.Marshal(previousCLIInference(raw))
		after, _ := json.Marshal(inferType(raw))
		if string(before) != string(after) {
			b.Fatal("non-equivalent benchmark input")
		}
		for _, side := range []struct {
			name  string
			parse func(string) any
		}{
			{"before", previousCLIInference}, {"after", inferType},
		} {
			b.Run(raw+"/"+side.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					data, err := json.Marshal(side.parse(raw))
					if err != nil || len(data) == 0 {
						b.Fatal("invalid benchmark output", err)
					}
				}
			})
		}
	}
}

// Frozen CLI inference before preserving complete JSON tokens.
func previousCLIInference(value string) any {
	var integer int64
	if _, err := fmt.Sscanf(value, "%d", &integer); err == nil && fmt.Sprintf("%d", integer) == value {
		return integer
	}
	var floating float64
	if _, err := fmt.Sscanf(value, "%g", &floating); err == nil {
		return floating
	}
	if value == "true" {
		return true
	}
	if value == "false" {
		return false
	}
	return value
}

func BenchmarkCLICallResponseRead(b *testing.B) {
	for _, mode := range []string{"json", "binary"} {
		for _, framing := range []string{"valid", "truncated", "http_error"} {
			b.Run(mode+"/"+framing, func(b *testing.B) {
				run := setupCLIResponseReadBenchmark(b, mode, framing)
				wantError := framing != "valid" && os.Getenv("LUXO_CLI_READ_BASELINE") != "1"
				if err := run(); (err != nil) != wantError {
					b.Fatal("unexpected fixture outcome", err)
				}
				b.ReportAllocs()
				for b.Loop() {
					if err := run(); (err != nil) != wantError {
						b.Fatal("response outcome changed", err)
					}
				}
			})
		}
	}
}

func setupCLIResponseReadBenchmark(b *testing.B, mode, framing string) func() error {
	b.Helper()
	enterCacheGenerationScratch(b)
	writeCLIResponseReadSchema(b)
	quietCLIOutput(b)
	body := []byte(`{"data":42}`)
	if mode == "binary" && framing != "http_error" {
		body = []byte{84}
	}
	length := len(body)
	if framing == "truncated" {
		length++
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(length))
		if mode == "binary" && framing != "http_error" {
			w.Header().Set("X-Luxo-Mode", "binary")
		}
		if framing == "http_error" {
			w.WriteHeader(http.StatusInternalServerError)
		}
		_, _ = w.Write(body)
	}))
	b.Cleanup(server.Close)
	useCLICallHost(b, server.URL)
	invoke := callJSONMode
	if mode == "binary" {
		invoke = callBinaryMode
	}
	return func() error { return invoke("echo", nil) }
}

func quietCLIOutput(b *testing.B) {
	b.Helper()
	sink, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		b.Fatal(err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = sink, sink
	b.Cleanup(func() {
		os.Stdout, os.Stderr = stdout, stderr
		_ = sink.Close()
	})
}
