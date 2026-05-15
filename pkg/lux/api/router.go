package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/i18n"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

// Pre-allocated response framing bytes — avoids per-request []byte conversion.
var (
	jsonDataPrefix = []byte(`{"data":`)
	jsonDataSuffix = []byte(`}`)
)

// HandlerFunc is the signature for API handlers.
// Handlers write their response directly to req.Buf — zero allocation, zero any.
type HandlerFunc func(ctx context.Context, req *Request) error

// StreamHandlerFunc is called when a client subscribes to a @stream @native API without event source.
// The handler receives a Stream and pushes data via stream.Send(). Return when done or context cancelled.
type StreamHandlerFunc func(ctx context.Context, params *StreamParams, identity any, stream *Stream)

// MetricsRecorder is called for each completed request to collect metrics.
type MetricsRecorder interface {
	Record(apiName string, duration time.Duration, isError bool)
}

// Router maps API names to handlers and serves the /luvia endpoint.
type Router struct {
	handlers         map[string]HandlerFunc
	streamMatchers   map[string]StreamMatcher     // @stream API name → matcher function
	streamHandlers   map[string]StreamHandlerFunc // @stream @native (no event) → handler
	translator       *i18n.Translator
	devMode          bool
	Registry         *APIRegistry    // binary protocol API ID mapping
	Schema           *schema.Schema  // model/API metadata for Binary↔JSON conversion
	Streams          *StreamHub      // WebSocket stream subscription manager
	IntrospectionKey string          // key for schema introspection (empty = disabled)
	WSOrigins        []string        // allowed WebSocket origins (empty = allow all in dev mode)
	metrics          MetricsRecorder // optional metrics collector
}

// SetMetricsCollector configures request metrics collection.
func (rt *Router) SetMetricsCollector(mc MetricsRecorder) {
	rt.metrics = mc
}

// NewRouter creates an empty router.
func NewRouter() *Router {
	return &Router{
		handlers:       make(map[string]HandlerFunc),
		streamMatchers: make(map[string]StreamMatcher),
		streamHandlers: make(map[string]StreamHandlerFunc),
		Registry:       NewAPIRegistry(),
		Schema:         schema.New(),
		Streams:        NewStreamHub(),
	}
}

// HandleStream registers a matcher for a @stream API.
// When events are dispatched, the matcher decides per-subscriber whether to push.
// matcher can be nil for broadcast (all subscribers receive).
func (rt *Router) HandleStream(apiName string, matcher StreamMatcher) {
	rt.streamMatchers[apiName] = matcher
}

// HandleStreamNative registers a handler for @stream @native without event source.
// The handler is invoked per subscription and controls push timing via stream.Send().
func (rt *Router) HandleStreamNative(apiName string, handler StreamHandlerFunc) {
	rt.streamHandlers[apiName] = handler
}

// Handle registers a handler for an API name.
func (rt *Router) Handle(name string, fn HandlerFunc) {
	rt.handlers[name] = fn
}

// SetTranslator configures i18n translation for error responses.
func (rt *Router) SetTranslator(t *i18n.Translator) {
	rt.translator = t
}

// SetDevMode enables development mode (includes cause in error responses).
func (rt *Router) SetDevMode(dev bool) {
	rt.devMode = dev
}

// ExportHandlers returns the handler map for sharing with RPC server.
func (rt *Router) ExportHandlers() map[string]HandlerFunc {
	return rt.handlers
}

// ServeHTTP implements http.Handler for the /luvia endpoint.
// Supports JSON, Luxo binary, and WebSocket protocols on the same endpoint.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// WebSocket upgrade: GET /luvia with Upgrade: websocket
	if r.Header.Get("Upgrade") == "websocket" {
		rt.handleWebSocket(w, r)
		return
	}

	// Schema introspection: GET /luvia?$schema&key=xxx
	if r.URL.Query().Has("$schema") {
		rt.handleIntrospection(w, r)
		return
	}

	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	binaryMode := r.Header.Get("X-Luxo-Mode") == "binary"

	var req *Request
	var err error
	if binaryMode {
		body, bp, readErr := readBody(r.Body)
		if readErr != nil {
			writeError(w, http.StatusBadRequest, readErr.Error())
			return
		}
		defer putBody(bp)
		req, err = rt.Registry.ParseBinaryRequest(body)
	} else {
		req, err = ParseRequest(r)
	}
	if err != nil {
		if isLogEnabled() {
			mode := "json"
			if binaryMode {
				mode = "binary"
			}
			fmt.Fprintf(os.Stderr, "%s%s%s %s[parse]%s %s %s✗ %s%s\n",
				colorDim, time.Now().Format("15:04:05"), colorReset,
				colorRed, colorReset, mode,
				colorRed, err.Error(), colorReset)
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	fn, ok := rt.handlers[req.API]
	if !ok {
		rt.writeAppError(w, r, binaryMode, errors.NotFound.WithData(errors.ResourceError{Resource: req.API}))
		return
	}

	// Force handler to always write Luxo binary.
	// Luvia converts to JSON if client wants JSON.
	req.BinaryMode = true

	// Convert $select to FieldMask for binary mode WriteLuxo
	if !binaryMode && req.Select != nil && rt.Schema != nil {
		apiMeta := rt.Schema.APIs[req.API]
		if apiMeta != nil && apiMeta.ReturnType != "" {
			model := rt.Schema.Models[apiMeta.ReturnType]
			if model != nil {
				req.FieldMask = schema.SelectToFieldMask(req.Select, model)
			}
		}
	}

	// Get pooled buffer, set on request, handler writes directly
	buf := GetBuf()
	req.Buf = buf

	start := time.Now()
	herr := rt.callHandler(fn, r.Context(), req)
	duration := time.Since(start)

	if rt.metrics != nil {
		rt.metrics.Record(req.API, duration, herr != nil)
	}

	if herr != nil {
		rt.logRequest(req.API, duration, herr)
		// Debug level: log param details for debugging (user explicitly opts in)
		if isLogEnabled() && os.Getenv("LOG_LEVEL") == "debug" && req.paramNames != nil {
			for i := 0; i < req.paramCount; i++ {
				fmt.Fprintf(os.Stderr, "    param[%d] %s = %v\n", i, req.paramNames[i], req.paramSlots[i])
			}
		}
		PutBuf(buf)
		rt.writeAppError(w, r, binaryMode, herr)
		return
	}
	rt.logRequest(req.API, duration, nil)

	if binaryMode {
		// Client wants binary: handler wrote Luxo binary, pass through
		w.Header().Set("Content-Type", "application/x-luxo")
		w.Header().Set("X-Luxo-Mode", "binary")
		w.WriteHeader(http.StatusOK)
		w.Write(buf.B)
	} else {
		// Client wants JSON: convert handler's Luxo binary → JSON via schema
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(jsonDataPrefix)
		w.Write(rt.convertBinaryToJSON(req.API, buf.B))
		w.Write(jsonDataSuffix)
	}

	PutBuf(buf)
}

// handleIntrospection serves the schema as JSON for SDK generation.
// Protected by INTROSPECTION_KEY — returns 403 without valid key.
func (rt *Router) handleIntrospection(w http.ResponseWriter, r *http.Request) {
	if rt.IntrospectionKey == "" {
		writeError(w, http.StatusForbidden, "introspection disabled")
		return
	}
	key := r.Header.Get("X-Introspection-Key")
	if key == "" {
		key = r.URL.Query().Get("key") // fallback for backward compatibility
	}
	if subtle.ConstantTimeCompare([]byte(key), []byte(rt.IntrospectionKey)) != 1 {
		writeError(w, http.StatusForbidden, "invalid introspection key")
		return
	}
	if rt.Schema == nil {
		writeError(w, http.StatusInternalServerError, "no schema registered")
		return
	}
	data, err := rt.Schema.ToJSON()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// convertBinaryToJSON converts handler's Luxo binary response to JSON using schema.
// Handles model, list, paginated list, and scalar return types.
func (rt *Router) convertBinaryToJSON(apiName string, data []byte) []byte {
	if len(data) == 0 {
		return []byte("null")
	}
	if rt.Schema == nil {
		return data // no schema — assume handler wrote JSON directly
	}
	apiMeta := rt.Schema.APIs[apiName]
	if apiMeta == nil {
		return data // unknown API — pass through as-is
	}

	// No return type → scalar (delete returns count as Int)
	if apiMeta.ReturnType == "" {
		return schema.BinaryScalarToJSON(nil, data, "Int")
	}

	// Check if return type is a model, type declaration, or scalar
	model := rt.Schema.Models[apiMeta.ReturnType]
	if model == nil {
		// Try type declarations (non-DB types like AuthPayload)
		if td := rt.Schema.Types[apiMeta.ReturnType]; td != nil {
			model = td.AsModel()
		}
	}
	if model == nil {
		// Scalar return — use declared type for precise decoding
		return schema.BinaryScalarToJSON(nil, data, apiMeta.ReturnType)
	}

	if apiMeta.ReturnList {
		if apiMeta.Paginated {
			return schema.BinaryPaginatedListToJSON(nil, data, model)
		}
		return schema.BinaryListToJSON(nil, data, model)
	}
	return schema.BinaryToJSON(nil, data, model, rt.Schema)
}

// callHandler executes a handler with panic recovery.
// If the handler panics, the panic is caught and returned as an InternalServerError.
func (rt *Router) callHandler(fn HandlerFunc, ctx context.Context, req *Request) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = errors.InternalError.WithCause(fmt.Errorf("panic: %v", p))
		}
	}()
	return fn(ctx, req)
}

// writeAppError writes a structured error response with i18n translation and traceId.
// Binary mode uses Luxo codec (fieldID 1=code, 2=name, 3=message); JSON mode uses JSON.
func (rt *Router) writeAppError(w http.ResponseWriter, r *http.Request, binaryMode bool, err error) {
	var appErr *errors.AppError
	if !stderrors.As(err, &appErr) {
		appErr = errors.Wrap(err)
	}

	traceID := TraceID(r.Context())

	message := appErr.Message
	if rt.translator != nil && !appErr.Internal {
		locale := i18n.ParseAcceptLanguage(r.Header.Get("Accept-Language"))
		if appErr.Data != nil {
			message = rt.translator.Translate(locale, appErr.Message, appErr.Data.I18nData())
		} else {
			message = rt.translator.Translate(locale, appErr.Message, nil)
		}
	}

	buf := GetBuf()

	if binaryMode {
		// Binary error: fieldID 1=code, 2=name, 3=message
		var enc codec.Encoder
		enc.WriteFieldInt(1, int64(appErr.Code))
		enc.WriteFieldString(2, appErr.Name)
		enc.WriteFieldString(3, message)
		enc.WriteEnd()
		buf.B = append(buf.B, enc.Bytes()...)

		w.Header().Set("Content-Type", "application/x-luxo")
		w.Header().Set("X-Luxo-Mode", "binary")
	} else {
		buf.AppendString(`{"error":`)
		buf.AppendJSONString(appErr.Name)
		buf.AppendString(`,"code":`)
		buf.AppendInt(int64(appErr.Code))
		buf.AppendString(`,"message":`)
		buf.AppendJSONString(message)
		if traceID != "" {
			buf.AppendString(`,"traceId":`)
			buf.AppendJSONString(traceID)
		}
		if appErr.Data != nil && !appErr.Internal {
			dataBytes, marshalErr := json.Marshal(appErr.Data)
			if marshalErr == nil {
				buf.AppendString(`,"data":`)
				buf.B = append(buf.B, dataBytes...)
			}
		}
		if rt.devMode && appErr.Cause != nil {
			buf.AppendString(`,"cause":`)
			buf.AppendJSONString(appErr.Cause.Error())
		}
		buf.AppendByte('}')
	}

	w.WriteHeader(appErr.Code)
	w.Write(buf.B)
	PutBuf(buf)
}

// ANSI color codes for module tags
var moduleColors = []string{
	"\033[36m", // cyan
	"\033[33m", // yellow
	"\033[35m", // magenta
	"\033[32m", // green
	"\033[34m", // blue
	"\033[91m", // bright red
	"\033[92m", // bright green
	"\033[93m", // bright yellow
	"\033[94m", // bright blue
	"\033[95m", // bright magenta
	"\033[96m", // bright cyan
}

const (
	colorReset = "\033[0m"
	colorDim   = "\033[2m"
	colorRed   = "\033[31m"
	colorGreen = "\033[32m"
)

// logEnabled controls whether request logging is active (LOG_REQUESTS env).
// Lazy-initialized on first check so .env is loaded before reading.
var logEnabled *bool

func isLogEnabled() bool {
	if logEnabled == nil {
		v := os.Getenv("LOG_REQUESTS") != "false"
		logEnabled = &v
	}
	return *logEnabled
}

// moduleColorMap caches color assignment per module name.
var (
	moduleColorMu  sync.Mutex
	moduleColorMap = make(map[string]string)
	moduleColorIdx int
)

func moduleColor(mod string) string {
	moduleColorMu.Lock()
	defer moduleColorMu.Unlock()
	if c, ok := moduleColorMap[mod]; ok {
		return c
	}
	c := moduleColors[moduleColorIdx%len(moduleColors)]
	moduleColorMap[mod] = c
	moduleColorIdx++
	return c
}

// logRequest prints a colored request log line:
//
//	12:34:56 [auth] login 2.3ms ✓
//	12:34:56 [auth] login 1.2ms ✗ InvalidCredentials
func (rt *Router) logRequest(apiName string, duration time.Duration, err error) {
	if !isLogEnabled() {
		return
	}
	mod := ""
	if rt.Schema != nil {
		if a, ok := rt.Schema.APIs[apiName]; ok {
			mod = a.Module
		}
	}
	if mod == "" {
		mod = "api"
	}

	ts := time.Now().Format("15:04:05")
	ms := float64(duration.Microseconds()) / 1000.0
	mc := moduleColor(mod)

	var durColor string
	if ms > 500 {
		durColor = colorRed
	} else if ms > 100 {
		durColor = "\033[33m" // yellow
	} else {
		durColor = colorDim
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %s[%s]%s %s %s%.1fms%s %s✗ %s%s\n",
			colorDim+ts+colorReset,
			mc, mod, colorReset,
			apiName,
			durColor, ms, colorReset,
			colorRed, err.Error(), colorReset,
		)
	} else {
		fmt.Fprintf(os.Stderr, "%s %s[%s]%s %s %s%.1fms%s %s✓%s\n",
			colorDim+ts+colorReset,
			mc, mod, colorReset,
			apiName,
			durColor, ms, colorReset,
			colorGreen, colorReset,
		)
	}
}

// writeError writes a simple JSON error response for infrastructure errors.
func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	// Escape msg to prevent JSON injection
	escaped := strconv.Quote(msg)
	w.Write([]byte(`{"error":` + escaped + `}`))
}
