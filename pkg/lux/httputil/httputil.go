// Package httputil provides HTTP client utility functions for the Luxo runtime.
package httputil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

// DefaultTimeout is the default timeout for HTTP requests.
const DefaultTimeout = 10 * time.Second

// DefaultResponseBodyLimit is the byte budget for buffered HTTP responses.
const DefaultResponseBodyLimit int64 = 16 << 20

var errInvalidResponseBodyLimit = errors.New("HTTP response byte limit must be positive")

// ResponseBodyLimitError reports an HTTP response that exceeded its byte budget.
type ResponseBodyLimitError struct {
	Limit int64
}

func (e *ResponseBodyLimitError) Error() string {
	return fmt.Sprintf("HTTP response body exceeds %d bytes", e.Limit)
}

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

// Do sends an HTTP request and buffers up to DefaultResponseBodyLimit bytes.
// Context controls cancellation and timeout.
func Do(ctx context.Context, method, url string, body string, headers map[string]string) (*Response, error) {
	return DoWithResponseLimit(ctx, method, url, body, headers, DefaultResponseBodyLimit)
}

// DoWithResponseLimit sends an HTTP request with an explicit response byte budget.
// The limit is applied to the body exposed by the HTTP client after transparent decompression.
func DoWithResponseLimit(ctx context.Context, method, url string, body string, headers map[string]string, limit int64) (*Response, error) {
	if limit <= 0 {
		return nil, errInvalidResponseBodyLimit
	}
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

	respBody, err := ReadResponseBody(resp.Body, limit)
	if err != nil {
		return nil, err
	}

	return &Response{
		StatusCode: resp.StatusCode,
		Body:       string(respBody),
		Headers:    resp.Header.Clone(),
	}, nil
}

// ReadResponseBody reads a complete body within limit and discards any partial
// data on error. The caller retains ownership of the reader.
func ReadResponseBody(body io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, errInvalidResponseBodyLimit
	}
	// The extra byte distinguishes an oversized response from one that fits.
	// A byte slice cannot contain more than MaxInt64 bytes.
	data, err := io.ReadAll(io.LimitReader(body, min(limit, math.MaxInt64-1)+1))
	if int64(len(data)) > limit {
		return nil, &ResponseBodyLimitError{Limit: limit}
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}
