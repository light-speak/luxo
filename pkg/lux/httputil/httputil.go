// Package httputil provides HTTP client utility functions for the Luxo runtime.
package httputil

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultTimeout is the default timeout for HTTP requests.
const DefaultTimeout = 10 * time.Second

// Response represents an HTTP response.
type Response struct {
	StatusCode int
	Body       string
	Headers    http.Header
}

var defaultClient = &http.Client{Timeout: DefaultTimeout}

// Get sends an HTTP GET request.
func Get(ctx context.Context, url string) (*Response, error) {
	return Do(ctx, http.MethodGet, url, "", nil)
}

// Post sends an HTTP POST request with JSON content type.
func Post(ctx context.Context, url string, body string) (*Response, error) {
	return Do(ctx, http.MethodPost, url, body, map[string]string{"Content-Type": "application/json"})
}

// Put sends an HTTP PUT request with JSON content type.
func Put(ctx context.Context, url string, body string) (*Response, error) {
	return Do(ctx, http.MethodPut, url, body, map[string]string{"Content-Type": "application/json"})
}

// Delete sends an HTTP DELETE request.
func Delete(ctx context.Context, url string) (*Response, error) {
	return Do(ctx, http.MethodDelete, url, "", nil)
}

// Do sends an HTTP request. Always sends a body reader (even for empty body).
// Context controls cancellation and timeout.
func Do(ctx context.Context, method, url string, body string, headers map[string]string) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := defaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return &Response{
		StatusCode: resp.StatusCode,
		Body:       string(respBody),
		Headers:    resp.Header.Clone(),
	}, nil
}
