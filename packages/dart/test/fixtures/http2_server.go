package main

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync/atomic"
)

func main() {
	var http1Requests atomic.Int64
	var redirectTargets atomic.Int64
	bodyStarted := make(chan struct{})
	bodyCancelled := make(chan struct{})
	largeCancelled := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect-target":
			redirectTargets.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		case "/redirect-target-count":
			_, _ = io.WriteString(w, strconv.FormatInt(redirectTargets.Load(), 10))
			return
		case "/redirect":
			status, err := strconv.Atoi(r.URL.Query().Get("status"))
			if err != nil || (status != 301 && status != 302 && status != 303 && status != 307 && status != 308) {
				http.Error(w, "invalid redirect status", 400)
				return
			}
			w.Header().Set("Location", r.URL.Query().Get("target"))
			w.Header().Set("X-Test-Connection", r.RemoteAddr)
			w.WriteHeader(status)
			return
		case "/http1-count":
			_, _ = io.WriteString(w, strconv.FormatInt(http1Requests.Load(), 10))
			return
		case "/large-open":
			_, _ = w.Write(make([]byte, 1025))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			close(largeCancelled)
			return
		case "/large-cancelled":
			select {
			case <-largeCancelled:
				w.WriteHeader(http.StatusNoContent)
			case <-r.Context().Done():
			}
			return
		case "/slow-body":
			_, _ = w.Write(make([]byte, 256))
			w.(http.Flusher).Flush()
			close(bodyStarted)
			<-r.Context().Done()
			close(bodyCancelled)
			return
		case "/body-started", "/body-cancelled":
			ready := bodyStarted
			if r.URL.Path == "/body-cancelled" {
				ready = bodyCancelled
			}
			select {
			case <-ready:
				w.WriteHeader(http.StatusNoContent)
			case <-r.Context().Done():
			}
			return
		}
		if r.URL.Path == "/slow" {
			<-r.Context().Done()
			return
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if r.URL.Path == "/early" {
			w.Header().Set("X-Early", "hint")
			w.WriteHeader(http.StatusEarlyHints)
			w.Header().Del("X-Early")
			w.Header().Set("X-Final", "response")
		}
		w.Header().Set("X-Test-Protocol", r.Proto)
		w.Header().Set("X-Test-Connection", r.RemoteAddr)
		if r.URL.Path == "/no-content" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/head" {
			w.Header().Set("Content-Length", "5")
			data = []byte("hello")
		}
		if r.URL.Path == "/large" {
			data = make([]byte, 4096)
		}
		if r.URL.Path == "/error" {
			w.WriteHeader(401)
		}
		_, _ = w.Write(data)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	http1 := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http1Requests.Add(1)
		w.Header().Set("X-Test-Protocol", r.Proto)
		w.WriteHeader(http.StatusOK)
	}))
	http1.StartTLS()
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	key, err := x509.MarshalPKCS8PrivateKey(server.TLS.Certificates[0].PrivateKey)
	if err != nil {
		server.Close()
		http1.Close()
		return
	}
	testKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})
	if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"url": server.URL, "http1": http1.URL, "certificate": string(certificate), "testKey": string(testKey)}); err != nil {
		server.Close()
		http1.Close()
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	server.Close()
	http1.Close()
}
