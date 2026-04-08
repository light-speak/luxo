package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// HandlerFunc is the signature for API handlers.
// It receives a context and parsed request, returns the result to be serialized.
type HandlerFunc func(ctx context.Context, req *Request) (any, error)

// Router maps API names to handlers and serves the /luvia endpoint.
type Router struct {
	handlers map[string]HandlerFunc
}

// NewRouter creates an empty router.
func NewRouter() *Router {
	return &Router{
		handlers: make(map[string]HandlerFunc),
	}
}

// Handle registers a handler for an API name.
func (rt *Router) Handle(name string, fn HandlerFunc) {
	rt.handlers[name] = fn
}

// ServeHTTP implements http.Handler for the /luvia endpoint.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	req, err := ParseRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	fn, ok := rt.handlers[req.API]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("unknown api '%s'", req.API))
		return
	}

	result, err := fn(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Apply field selection
	data, err := FilterFields(result, req.Select)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "serialize: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"data":`))
	w.Write(data)
	w.Write([]byte(`}`))
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, code int, msg string) {
	w.WriteHeader(code)
	resp, _ := json.Marshal(map[string]string{"error": msg})
	w.Write(resp)
}
