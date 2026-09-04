package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

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

// TraceRecord contains the request data needed by an optional trace exporter.
type TraceRecord struct {
	TraceID       string
	APIName       string
	Duration      time.Duration
	StatusCode    int
	ClientName    string
	ClientVersion string
	Timestamp     time.Time
}

// TraceRecorder is implemented by metrics collectors that also export traces.
type TraceRecorder interface {
	RecordTrace(record TraceRecord)
}

// Router maps API names to handlers and serves the /luvia endpoint.
type Router struct {
	handlers               map[string]HandlerFunc
	streamMatchers         map[string]StreamMatcher     // @stream API name → matcher function
	streamHandlers         map[string]StreamHandlerFunc // @stream @native (no event) → handler
	requiredStreams        map[string]struct{}          // generated @stream APIs that must have an implementation
	translator             *i18n.Translator
	devMode                bool
	Registry               *APIRegistry                                           // binary protocol API ID mapping
	Schema                 *schema.Schema                                         // model/API metadata for Binary↔JSON conversion
	Streams                *StreamHub                                             // WebSocket stream subscription manager
	IntrospectionKey       string                                                 // key for schema introspection (empty = disabled)
	Version                string                                                 // application version exposed with schema introspection
	WSOrigins              []string                                               // allowed WebSocket origins (empty = allow all in dev mode)
	WSAllowAllOrigins      bool                                                   // explicitly allow every WebSocket origin
	IdentityExtractor      func(context.Context) any                              // extracts authenticated stream identity from request context
	InternalRequestContext func(context.Context, string) (context.Context, error) // verifies RPC bearer metadata and enriches context
	metrics                MetricsRecorder                                        // optional metrics collector
	requestLogging         bool
	debugParamShape        bool
	logWriter              io.Writer
}

// RouterOptions configures optional request observability outside the hot path.
type RouterOptions struct {
	RequestLogging      bool
	DebugParamStructure bool
	LogWriter           io.Writer
}

// SetMetricsCollector configures request metrics collection.
func (rt *Router) SetMetricsCollector(mc MetricsRecorder) {
	rt.metrics = mc
}

// NewRouter creates an empty router.
func NewRouter() *Router {
	return NewRouterWithOptions(RouterOptions{
		RequestLogging:      os.Getenv("LOG_REQUESTS") == "true",
		DebugParamStructure: os.Getenv("LOG_LEVEL") == "debug",
	})
}

// NewRouterWithOptions creates a router with explicit observability settings.
func NewRouterWithOptions(options RouterOptions) *Router {
	s := schema.New()
	registry := NewAPIRegistry()
	registry.SetSchema(s)
	logWriter := options.LogWriter
	if logWriter == nil {
		logWriter = os.Stderr
	}
	return &Router{
		handlers:        make(map[string]HandlerFunc),
		streamMatchers:  make(map[string]StreamMatcher),
		streamHandlers:  make(map[string]StreamHandlerFunc),
		requiredStreams: make(map[string]struct{}),
		Registry:        registry,
		Schema:          s,
		Streams:         NewStreamHub(),
		requestLogging:  options.RequestLogging,
		debugParamShape: options.DebugParamStructure,
		logWriter:       logWriter,
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

// RequireStream marks a generated stream route as requiring an implementation.
func (rt *Router) RequireStream(apiName string) {
	rt.requiredStreams[apiName] = struct{}{}
}

// Validate checks startup invariants that cannot be proven by code generation.
func (rt *Router) Validate() error {
	missing := make([]string, 0)
	for apiName := range rt.requiredStreams {
		if _, registered := rt.streamMatchers[apiName]; registered {
			continue
		}
		if _, registered := rt.streamHandlers[apiName]; !registered {
			missing = append(missing, apiName)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("api: stream %q has no registered implementation", missing[0])
}

func (rt *Router) isStreamAPI(apiName string) bool {
	if _, ok := rt.streamMatchers[apiName]; ok {
		return true
	}
	if _, ok := rt.streamHandlers[apiName]; ok {
		return true
	}
	definition := rt.Schema.APIs[apiName]
	return definition != nil && definition.Stream
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

	// Schema introspection: GET /luvia?$schema with X-Introspection-Key.
	if r.URL.Query().Has("$schema") {
		rt.handleIntrospection(w, r)
		return
	}

	binaryMode := r.Header.Get("X-Luxo-Mode") == "binary"

	if r.Method != http.MethodPost {
		rt.writeAppError(w, r, binaryMode, errors.New("MethodNotAllowed", http.StatusMethodNotAllowed, "POST only"))
		return
	}

	var req *Request
	var err error
	if binaryMode {
		body, bp, readErr := readBody(r.Body)
		if readErr != nil {
			putBody(bp)
			rt.writeAppError(w, r, true, errors.New("BadRequest", http.StatusBadRequest, readErr.Error()))
			return
		}
		defer putBody(bp)
		req, err = rt.Registry.ParseBinaryRequest(body)
	} else {
		req, err = ParseRequest(r)
	}
	if err != nil {
		if rt.requestLogging {
			mode := "json"
			if binaryMode {
				mode = "binary"
			}
			fmt.Fprintf(rt.logWriter, "%s%s%s %s[parse]%s %s %s✗ %s%s\n",
				colorDim, time.Now().Format("15:04:05"), colorReset,
				colorRed, colorReset, mode,
				colorRed, err.Error(), colorReset)
		}
		rt.writeAppError(w, r, binaryMode, errors.New("BadRequest", http.StatusBadRequest, err.Error()))
		return
	}
	req.ClientKey = directClientKey(r.RemoteAddr)

	fn, ok := rt.handlers[req.API]
	if !ok {
		rt.writeAppError(w, r, binaryMode, errors.NotFound.WithData(errors.ResourceError{Resource: req.API}))
		return
	}

	// Force handler to always write Luxo binary.
	// Luvia converts to JSON if client wants JSON.
	req.BinaryMode = true

	if err = rt.prepareRequest(req, binaryMode); err != nil {
		rt.writeAppError(w, r, binaryMode, errors.New("BadRequest", http.StatusBadRequest, err.Error()))
		return
	}

	// Get pooled buffer, set on request, handler writes directly
	buf := GetBuf()
	req.Buf = buf

	observed := rt.metrics != nil || rt.requestLogging
	var start time.Time
	if observed {
		start = time.Now()
	}
	herr := rt.callHandler(fn, r.Context(), req)
	var duration time.Duration
	if observed {
		duration = time.Since(start)
	}

	if rt.metrics != nil {
		rt.metrics.Record(req.API, duration, herr != nil)
		if recorder, ok := rt.metrics.(TraceRecorder); ok {
			recorder.RecordTrace(newTraceRecord(r, req.API, start, duration, herr))
		}
	}

	if herr != nil {
		rt.logRequest(req.API, duration, herr)
		// Debug level: log param details for debugging (user explicitly opts in)
		if rt.requestLogging && rt.debugParamShape {
			rt.logParamStructure(req)
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

func directClientKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return host
	}
	return remoteAddr
}

func (rt *Router) prepareRequest(req *Request, binaryMode bool) error {
	if err := rt.applyJSONSelection(req, binaryMode); err != nil {
		return err
	}
	if binaryMode {
		return nil
	}
	return rt.Registry.prepareJSONRequest(req)
}

func (rt *Router) applyJSONSelection(req *Request, binaryMode bool) error {
	if binaryMode || req.Select == nil || rt.Schema == nil {
		return nil
	}
	apiMeta := rt.Schema.APIs[req.API]
	if apiMeta == nil || apiMeta.ReturnType == "" {
		return nil
	}
	model := schemaObject(rt.Schema, apiMeta.ReturnType)
	if model == nil {
		return nil
	}
	mask, err := schema.SelectToFieldMask(req.Select, model, rt.Schema)
	if err == nil {
		req.FieldMask = mask
	}
	return err
}

func newTraceRecord(r *http.Request, apiName string, startedAt time.Time, duration time.Duration, err error) TraceRecord {
	statusCode := http.StatusOK
	if err != nil {
		statusCode = http.StatusInternalServerError
		var appErr *errors.AppError
		if stderrors.As(err, &appErr) {
			statusCode = appErr.Code
		}
	}
	return TraceRecord{
		TraceID:       TraceID(r.Context()),
		APIName:       apiName,
		Duration:      duration,
		StatusCode:    statusCode,
		ClientName:    r.Header.Get("X-Luxo-Client"),
		ClientVersion: r.Header.Get("X-Luxo-Client-Version"),
		Timestamp:     startedAt.UTC(),
	}
}

// handleIntrospection serves the schema as JSON for SDK generation.
// Protected by INTROSPECTION_KEY — returns 403 without valid key.
func (rt *Router) handleIntrospection(w http.ResponseWriter, r *http.Request) {
	if rt.IntrospectionKey == "" {
		writeError(w, http.StatusForbidden, "introspection disabled")
		return
	}
	key := r.Header.Get("X-Introspection-Key")
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
	if rt.Version != "" {
		w.Header().Set("X-Luxo-Version", rt.Version)
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
	model := schemaObject(rt.Schema, apiMeta.ReturnType)
	if model == nil {
		wireType := apiMeta.ReturnType
		if rt.Schema.Enums[wireType] != nil {
			wireType = "String"
		}
		if apiMeta.ReturnList {
			return schema.BinaryScalarListToJSON(nil, data, wireType)
		}
		return schema.BinaryScalarToJSON(nil, data, wireType)
	}

	if apiMeta.ReturnList {
		if apiMeta.Paginated {
			return schema.BinaryPaginatedListToJSON(nil, data, model, rt.Schema)
		}
		return schema.BinaryListToJSON(nil, data, model, rt.Schema)
	}
	return schema.BinaryToJSON(nil, data, model, rt.Schema)
}

// StreamPayloadJSON converts a generated binary stream payload through the
// same schema-driven path as an ordinary API response.
func (rt *Router) StreamPayloadJSON(apiName string, data []byte) []byte {
	return rt.convertBinaryToJSON(apiName, data)
}

func schemaObject(s *schema.Schema, typeName string) *schema.Model {
	if model := s.Models[typeName]; model != nil {
		return model
	}
	if declaration := s.Types[typeName]; declaration != nil {
		return declaration.AsModel()
	}
	return nil
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
// Binary mode uses the canonical binary error envelope; JSON mode uses JSON.
func (rt *Router) writeAppError(w http.ResponseWriter, r *http.Request, binaryMode bool, err error) {
	werr := rt.buildWireError(r.Context(), r.Header.Get("Accept-Language"), err)
	buf := GetBuf()

	if binaryMode {
		buf.B = appendBinaryError(buf.B, werr)
		w.Header().Set("Content-Type", "application/x-luxo")
		w.Header().Set("X-Luxo-Mode", "binary")
	} else {
		appendJSONError(buf, werr)
	}

	w.WriteHeader(werr.Code)
	w.Write(buf.B)
	PutBuf(buf)
}

func (rt *Router) buildWireError(ctx context.Context, acceptLanguage string, err error) wireError {
	var appErr *errors.AppError
	if !stderrors.As(err, &appErr) {
		appErr = errors.Wrap(err)
	}

	message := appErr.Message
	if rt.translator != nil && !appErr.Internal {
		locale := i18n.ParseAcceptLanguage(acceptLanguage)
		if appErr.Data != nil {
			message = rt.translator.Translate(locale, appErr.Message, appErr.Data.I18nData())
		} else {
			message = rt.translator.Translate(locale, appErr.Message, nil)
		}
	}

	werr := wireError{
		Code:    appErr.Code,
		Name:    appErr.Name,
		Message: message,
		TraceID: TraceID(ctx),
	}
	if appErr.Data != nil && !appErr.Internal {
		werr.Data, _ = json.Marshal(appErr.Data)
	}
	if rt.devMode && appErr.Cause != nil {
		werr.Cause = appErr.Cause.Error()
	}
	return werr
}

func appendJSONError(buf *ResponseBuf, e wireError) {
	buf.AppendByte('{')
	appendJSONErrorFields(buf, e)
	buf.AppendByte('}')
}

func appendJSONErrorFields(buf *ResponseBuf, e wireError) {
	buf.AppendString(`"error":`)
	buf.AppendJSONString(e.Name)
	buf.AppendString(`,"code":`)
	buf.AppendInt(int64(e.Code))
	buf.AppendString(`,"message":`)
	buf.AppendJSONString(e.Message)
	if e.TraceID != "" {
		buf.AppendString(`,"traceId":`)
		buf.AppendJSONString(e.TraceID)
	}
	if len(e.Data) > 0 {
		buf.AppendString(`,"data":`)
		buf.B = append(buf.B, e.Data...)
	}
	if e.Cause != "" {
		buf.AppendString(`,"cause":`)
		buf.AppendJSONString(e.Cause)
	}
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
	if !rt.requestLogging {
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
		errorName := safeLogErrorName(err)
		fmt.Fprintf(rt.logWriter, "%s %s[%s]%s %s %s%.1fms%s %s✗ %s%s\n",
			colorDim+ts+colorReset,
			mc, mod, colorReset,
			apiName,
			durColor, ms, colorReset,
			colorRed, errorName, colorReset,
		)
	} else {
		fmt.Fprintf(rt.logWriter, "%s %s[%s]%s %s %s%.1fms%s %s✓%s\n",
			colorDim+ts+colorReset,
			mc, mod, colorReset,
			apiName,
			durColor, ms, colorReset,
			colorGreen, colorReset,
		)
	}
}

func safeLogErrorName(err error) string {
	var appErr *errors.AppError
	if stderrors.As(err, &appErr) {
		return appErr.Name
	}
	return "Internal"
}

func (rt *Router) logParamStructure(req *Request) {
	params := rt.Registry.ParamOrder(req.API)
	if req.paramNames == nil {
		for i, param := range params {
			_, present := req.Params[param.Name]
			rt.writeParamStructure(i, param.Name, param.Type, present)
		}
		return
	}
	for i := 0; i < req.paramCount && i < len(req.paramNames); i++ {
		typeName := "Unknown"
		if i < len(params) {
			typeName = params[i].Type
		}
		rt.writeParamStructure(i, req.paramNames[i], typeName, req.paramSlots[i] != nil)
	}
}

func (rt *Router) writeParamStructure(index int, name string, typeName string, present bool) {
	fmt.Fprintf(rt.logWriter, "    param[%d] %s %s present=%t\n", index, name, typeName, present)
}

// writeError writes a simple JSON error response for infrastructure errors.
func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	// Escape msg to prevent JSON injection
	escaped := strconv.Quote(msg)
	w.Write([]byte(`{"error":` + escaped + `}`))
}
