package api

import (
	"context"
	"crypto/subtle"
	stderrors "errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/i18n"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/light-speak/luxo/pkg/lux/selection"
)

// Pre-allocated response framing bytes — avoids per-request []byte conversion.
var (
	jsonDataPrefix = []byte(`{"data":`)
	jsonDataSuffix = []byte(`}`)
)

// HandlerFunc is the signature for API handlers.
// Handlers write their response directly to req.Buf — zero allocation, zero any.
type HandlerFunc func(ctx context.Context, req *Request) error

type handlerRoute struct {
	call      HandlerFunc
	localJSON bool
}

// StreamHandlerFunc is called when a client subscribes to a @stream @native API without event source.
// The handler pushes data via stream.Send(). Return nil for completion or an
// error for terminal failure; accepted messages precede the terminal result.
type StreamHandlerFunc func(ctx context.Context, params *StreamParams, identity any, stream *Stream) error

// MetricsRecorder is called for each completed request to collect metrics.
type MetricsRecorder interface {
	Record(apiName string, duration time.Duration, isError bool)
}

type metricsRecorderState struct {
	recorder MetricsRecorder
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
	Spans         string
	Selection     string
	HeadSampled   bool
}

// TraceRecorder is implemented by metrics collectors that also export traces.
type TraceRecorder interface {
	RecordTrace(record TraceRecord)
}

// TraceSampler decides whether a request should collect detailed production spans.
type TraceSampler interface {
	ShouldTrace(traceID string) bool
}

// Router maps API names to handlers and serves the /luvia endpoint.
type Router struct {
	binaryRoutes           map[*binaryAPIPlan]handlerRoute
	wsLimits               WebSocketLimits
	wsCalls                atomic.Int64
	wsConnections          atomic.Int64
	wsWriteBytes           atomic.Int64
	prepareMu              sync.Mutex
	prepared               bool
	responseCache          *ResponseCache
	requiredCache          map[string]struct{}
	cachePlans             map[string]*cacheDependencyPlan
	cacheBindings          map[string]*cacheRouteBinding
	handlers               map[string]handlerRoute
	internalHandlers       map[string]HandlerFunc
	streamMatchers         map[string]StreamMatcher      // @stream API name → matcher function
	streamHandlers         map[string]streamHandlerRoute // @stream @native (no event) → handler
	requiredStreams        map[string]struct{}           // generated @stream APIs that must have an implementation
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
	metrics                atomic.Pointer[metricsRecorderState]                   // optional metrics collector
	debugTraceSecret       string
	requestLogging         bool
	debugParamShape        bool
	logWriter              io.Writer
	asyncLogs              *asyncRequestLogs
	logSampleEvery         uint64
	logSequence            atomic.Uint64
	bodyLimits             BodyLimits
	receiveBytes           atomic.Int64
}

// RouterOptions configures runtime resource budgets and request observability.
type RouterOptions struct {
	BodyLimits          BodyLimits
	WebSocket           WebSocketLimits
	ResponseCache       *ResponseCache
	RequestLogging      bool
	DebugParamStructure bool
	LogWriter           io.Writer
	// SynchronousRequestLogs opts out of bounded asynchronous request output.
	SynchronousRequestLogs bool
	// RequestLogSampleEvery logs one of every N successful requests; 0 defaults to 100, 1 explicitly logs all.
	// Errors bypass sampling, but queue and record size limits still apply.
	RequestLogSampleEvery uint64
}

// SetMetricsCollector configures request metrics collection.
func (rt *Router) SetMetricsCollector(mc MetricsRecorder) {
	if mc == nil {
		rt.metrics.Store(nil)
		return
	}
	rt.metrics.Store(&metricsRecorderState{recorder: mc})
}

// RequestMetricsRecorder returns the current request metrics sink.
// Callers retain the returned recorder for one request so configuration changes
// cannot split a measurement across different collectors.
func (rt *Router) RequestMetricsRecorder() MetricsRecorder {
	state := rt.metrics.Load()
	if state == nil {
		return nil
	}
	return state.recorder
}

// NewRouter creates an empty router.
func NewRouter() *Router {
	return NewRouterWithOptions(RouterOptionsFromEnv())
}

// RouterOptionsFromEnv reads startup logging settings without creating a router.
// Callers can attach resource settings before passing these options to NewRouterWithOptions.
func RouterOptionsFromEnv() RouterOptions {
	sampleEvery := uint64(100)
	if os.Getenv("LOG_REQUESTS_FULL") == "true" {
		sampleEvery = 1
	}
	return RouterOptions{
		RequestLogging:        os.Getenv("LOG_REQUESTS") == "true",
		RequestLogSampleEvery: sampleEvery,
		DebugParamStructure:   os.Getenv("LOG_LEVEL") == "debug",
	}
}

// NewRouterWithOptions creates a router with explicit resource and observability settings.
func NewRouterWithOptions(options RouterOptions) *Router {
	if options.RequestLogSampleEvery == 0 {
		options.RequestLogSampleEvery = 100
	}
	s := schema.New()
	registry := NewAPIRegistry()
	// Both objects are fresh; their shared schema is validated after registration.
	registry.schema = s
	logWriter := options.LogWriter
	if logWriter == nil {
		logWriter = os.Stderr
	}
	router := &Router{
		bodyLimits:      normalizeBodyLimits(options.BodyLimits),
		wsLimits:        normalizeWSLimits(options.WebSocket),
		responseCache:   options.ResponseCache,
		handlers:        make(map[string]handlerRoute),
		streamMatchers:  make(map[string]StreamMatcher),
		streamHandlers:  make(map[string]streamHandlerRoute),
		requiredStreams: make(map[string]struct{}),
		Registry:        registry,
		Schema:          s,
		Streams:         NewStreamHub(),
		requestLogging:  options.RequestLogging,
		debugParamShape: options.DebugParamStructure,
		logWriter:       logWriter,
		logSampleEvery:  options.RequestLogSampleEvery,
	}
	if options.RequestLogging && !options.SynchronousRequestLogs {
		router.asyncLogs = newAsyncRequestLogs(logWriter, requestLogQueueCapacity)
	}
	return router
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
	rt.streamHandlers[apiName] = streamHandlerRoute{handler: handler}
}

// RequireStream marks a generated stream route as requiring an implementation.
func (rt *Router) RequireStream(apiName string) {
	rt.requiredStreams[apiName] = struct{}{}
}

// Prepare freezes a successfully validated startup snapshot for HTTP and RPC.
// Finish registration first. Failed validation may be corrected and retried;
// successful preparation is shared without repeating initialization. Do not
// mutate registrations or schema after success; prepare a new router instead.
func (rt *Router) Prepare() error {
	rt.prepareMu.Lock()
	defer rt.prepareMu.Unlock()
	if rt.prepared {
		return nil
	}
	if err := rt.Validate(); err != nil {
		return err
	}
	for name, binding := range rt.cacheBindings {
		binding.plan.Store(rt.cachePlans[name])
	}
	rt.prepareBinaryRoutes()
	rt.prepared = true
	return nil
}

// Validate checks startup invariants and prepares immutable cache plans.
// Complete registration first; do not call it concurrently with requests.
func (rt *Router) Validate() error {
	if err := rt.bodyLimits.validate(); err != nil {
		return err
	}
	if err := rt.wsLimits.validate(); err != nil {
		return err
	}
	if rt.Schema != nil {
		if err := rt.Schema.Prepare(); err != nil {
			return err
		}
	}
	if err := rt.validateRouteScopes(); err != nil {
		return err
	}
	for name, route := range rt.handlers {
		if route.localJSON {
			if _, err := rt.Registry.localJSONDefinition(name); err != nil {
				return err
			}
		}
	}
	for name, route := range rt.streamHandlers {
		if route.handler == nil && !route.preparesInputs() && route.forwardPrepare == nil {
			return fmt.Errorf("api: stream %q has no registered implementation", name)
		}
		if route.preparesInputs() || route.forwardPrepare != nil {
			if _, err := rt.Registry.localJSONDefinition(name); err != nil {
				return err
			}
		}
	}
	plans, err := rt.prepareCacheRequirements()
	if err != nil {
		return err
	}
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
		rt.cachePlans = plans
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("api: stream %q has no registered implementation", missing[0])
}

func (rt *Router) validateRouteScopes() error {
	if rt.Schema == nil {
		return nil
	}
	for name, definition := range rt.Schema.APIs {
		if definition.Internal {
			_, public := rt.handlers[name]
			_, matcher := rt.streamMatchers[name]
			_, stream := rt.streamHandlers[name]
			if public || matcher || stream {
				return fmt.Errorf("api: internal route %q cannot be registered on a public transport", name)
			}
		} else if _, internal := rt.internalHandlers[name]; internal {
			return fmt.Errorf("api: public schema %q does not match its internal registration", name)
		}
	}
	return nil
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

// Handle registers a public HTTP/WebSocket handler, also available over RPC.
// Registration replaces any previous route with the same name and must finish
// before serving requests or exporting the RPC startup snapshot.
func (rt *Router) Handle(name string, fn HandlerFunc) {
	delete(rt.internalHandlers, name)
	rt.handlers[name] = handlerRoute{call: rt.bindHandler(name, fn)}
}

// HandleInternal registers an RPC-only handler. Public transports never look up
// this table; names and prefixes have no bearing on route visibility.
func (rt *Router) HandleInternal(name string, fn HandlerFunc) {
	if rt.internalHandlers == nil {
		rt.internalHandlers = make(map[string]HandlerFunc)
	}
	delete(rt.handlers, name)
	rt.internalHandlers[name] = rt.bindHandler(name, fn)
}

func (rt *Router) bindHandler(name string, fn HandlerFunc) HandlerFunc {
	if rt.responseCache != nil {
		if rt.cacheBindings == nil {
			rt.cacheBindings = make(map[string]*cacheRouteBinding)
		}
		binding := &cacheRouteBinding{cache: rt.responseCache, api: name}
		rt.cacheBindings[name] = binding
		fn = bindResponseCache(binding, fn)
	}
	return fn
}

// HandleLocal registers a statically typed handler that reads and validates all
// parameters before business effects or cache lookup. JSON stays in its original
// representation; forwarding handlers must use Handle for canonical wire input.
// Registration, like Schema construction, must finish before serving requests.
func (rt *Router) HandleLocal(name string, fn HandlerFunc) {
	rt.Handle(name, fn)
	route := rt.handlers[name]
	route.localJSON = true
	rt.handlers[name] = route
}

// SetTranslator configures i18n translation for error responses.
func (rt *Router) SetTranslator(t *i18n.Translator) {
	rt.translator = t
}

// SetDevMode enables development mode (includes cause in error responses).
func (rt *Router) SetDevMode(dev bool) {
	rt.devMode = dev
}

// SetDebugTraceKey configures the secret required for detailed production tracing.
func (rt *Router) SetDebugTraceKey(key string) {
	rt.debugTraceSecret = key
}

// ExportHandlers takes a startup snapshot for the RPC server. JSON preparation
// policy belongs to the public transport and is not part of RPC dispatch.
func (rt *Router) ExportHandlers() map[string]HandlerFunc {
	handlers := make(map[string]HandlerFunc, len(rt.handlers)+len(rt.internalHandlers))
	for name, route := range rt.handlers {
		handlers[name] = route.call
	}
	for name, handler := range rt.internalHandlers {
		handlers[name] = handler
	}
	return handlers
}

// ServeHTTP implements http.Handler for the /luvia endpoint.
// Supports JSON, Luxo binary, and WebSocket protocols on the same endpoint.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Upgrade") == "websocket" {
		rt.handleWebSocket(w, r)
		return
	}
	if r.URL.RawQuery != "" && r.URL.Query().Has("$schema") {
		rt.handleIntrospection(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	metrics := rt.RequestMetricsRecorder()
	r, debugTrace := rt.enableTrace(r, metrics)
	if debugTrace != nil && debugTrace.responseEnvelope {
		capture := newDebugResponseWriter(w)
		defer capture.flush(debugTrace)
		w = capture
	}

	binaryMode := r.Header.Get("X-Luxo-Mode") == "binary"

	if r.Method != http.MethodPost {
		rt.writeAppError(w, r, binaryMode, errors.New("MethodNotAllowed", http.StatusMethodNotAllowed, "POST only"))
		return
	}
	rt.serveCall(w, r, binaryMode, metrics, debugTrace)
}

func (rt *Router) serveCall(w http.ResponseWriter, r *http.Request, binaryMode bool, metrics MetricsRecorder, trace *DebugTraceSession) {
	var logStarted time.Time
	if rt.requestLogging {
		logStarted = time.Now()
	}
	var decodeStarted time.Time
	if trace != nil {
		decodeStarted = trace.Start()
	}
	lease := rt.newReceiveLease()
	defer lease.release()
	req, bp, err := rt.parseTransportRequestBudget(r, binaryMode, &lease)
	if trace != nil {
		trace.Finish(decodeStarted, DebugSpanMeta{Name: "request.decode", Category: "gateway"})
	}
	if err != nil {
		rt.writeParseError(w, r, binaryMode, trace, err)
		return
	}
	if binaryMode {
		defer PutRequest(req)
	}
	if bp != nil {
		defer putBody(bp)
	}
	req.ClientKey = directClientKey(r.RemoteAddr)
	fn, ok := rt.requestRoute(req)
	if !ok {
		writeDebugTraceHeaders(w.Header(), trace)
		rt.writeAppError(w, r, binaryMode, errors.NotFound.WithData(errors.ResourceError{Resource: req.API}))
		return
	}
	var prepareStarted time.Time
	if trace != nil {
		prepareStarted = trace.Start()
	}
	if err = rt.prepareCallRequest(req, binaryMode, fn.localJSON); err != nil {
		trace.Finish(prepareStarted, DebugSpanMeta{Name: "request.prepare", Category: "gateway", Operation: req.API})
		writeDebugTraceHeaders(w.Header(), trace)
		rt.writeAppError(w, r, binaryMode, errors.New("BadRequest", http.StatusBadRequest, err.Error()))
		return
	}
	if trace != nil {
		trace.Finish(prepareStarted, DebugSpanMeta{Name: "request.prepare", Category: "gateway", Operation: req.API})
	}
	rt.executeHTTPHandler(w, r, req, fn.call, binaryMode, metrics, trace, logStarted)
}

func (rt *Router) parseTransportRequest(r *http.Request, binaryMode bool) (*Request, *[]byte, error) {
	return rt.parseTransportRequestBudget(r, binaryMode, nil)
}

func (rt *Router) parseTransportRequestBudget(r *http.Request, binaryMode bool, lease *receiveLease) (*Request, *[]byte, error) {
	if r.ContentLength > int64(rt.bodyLimits.RequestBytes) {
		return nil, nil, errRequestBodyTooLarge
	}
	if !binaryMode {
		req, err := parseRequestLimitBudget(r, rt.bodyLimits.RequestBytes, lease)
		return req, nil, err
	}
	req := GetRequest()
	body, bp, err := readTransportBodyBudget(r.Body, rt.bodyLimits.RequestBytes, lease)
	if err == nil {
		err = rt.Registry.ParseBinaryRequestInto(body, req)
	}
	if err != nil {
		PutRequest(req)
		if bp != nil {
			putBody(bp)
		}
		return nil, nil, err
	}
	return req, bp, nil
}

func (rt *Router) writeParseError(w http.ResponseWriter, r *http.Request, binaryMode bool, trace *DebugTraceSession, err error) {
	if rt.requestLogging {
		mode := "json"
		if binaryMode {
			mode = "binary"
		}
		rt.logParseFailure(mode, err.Error())
	}
	writeDebugTraceHeaders(w.Header(), trace)
	if stderrors.Is(err, errReceiveCapacity) {
		rt.writeAppError(w, r, binaryMode, errors.New("Unavailable", http.StatusServiceUnavailable, err.Error()))
		return
	}
	if stderrors.Is(err, errRequestBodyTooLarge) {
		rt.writeAppError(w, r, binaryMode, errors.New("PayloadTooLarge", http.StatusRequestEntityTooLarge, err.Error()))
		return
	}
	rt.writeAppError(w, r, binaryMode, errors.New("BadRequest", http.StatusBadRequest, err.Error()))
}

func (rt *Router) executeHTTPHandler(w http.ResponseWriter, r *http.Request, req *Request, fn HandlerFunc, binaryMode bool, metrics MetricsRecorder, trace *DebugTraceSession, logStarted time.Time) {
	buf := GetBuf()
	req.Buf = buf
	startedAt, duration, handlerErr := rt.callObservedHandler(r.Context(), req, fn, metrics != nil || rt.requestLogging, trace)
	if handlerErr != nil {
		rt.recordRequestMetrics(r, req, metrics, startedAt, duration, handlerErr)
		rt.writeHandlerError(w, r, req, buf, binaryMode, trace, handlerErr)
		rt.logHTTPCompletion(req.API, duration, logStarted, handlerErr)
		return
	}
	jsonBody, encodeErr := rt.encodeHTTPResponse(req, buf, binaryMode, trace)
	if encodeErr == nil && responseBodySize(buf.B, jsonBody, binaryMode) > rt.responseBodyLimit() {
		encodeErr = errors.New("ResponseTooLarge", http.StatusInternalServerError, "response exceeds configured size limit")
	}
	rt.recordRequestMetrics(r, req, metrics, startedAt, duration, encodeErr)
	if encodeErr != nil {
		rt.writeHandlerError(w, r, req, buf, binaryMode, trace, encodeErr)
		rt.logHTTPCompletion(req.API, duration, logStarted, encodeErr)
		return
	}
	writeDebugTraceHeaders(w.Header(), trace)
	transferred := rt.writeOwnedResponse(w, buf, jsonBody, binaryMode)
	rt.logHTTPCompletion(req.API, duration, logStarted, nil)
	if !transferred {
		PutBuf(buf)
	}
}

func (rt *Router) callObservedHandler(ctx context.Context, req *Request, fn HandlerFunc, observed bool, trace *DebugTraceSession) (time.Time, time.Duration, error) {
	var start time.Time
	if observed || trace != nil {
		start = time.Now()
	}
	handlerCtx := ctx
	var handlerSpan DebugSpanHandle
	if trace != nil {
		handlerCtx, handlerSpan = trace.StartSpan(handlerCtx, DebugSpanMeta{
			Name: "handler.execute", Category: "gateway", Operation: req.API,
		})
	}
	err := rt.callHandler(fn, handlerCtx, req)
	var duration time.Duration
	if observed || trace != nil {
		duration = time.Since(start)
	}
	if trace != nil {
		trace.FinishSpan(handlerSpan)
	}
	return start, duration, err
}

func (rt *Router) recordRequestMetrics(r *http.Request, req *Request, metrics MetricsRecorder, startedAt time.Time, duration time.Duration, err error) {
	if metrics == nil {
		return
	}
	metrics.Record(req.API, duration, err != nil)
	if recorder, ok := metrics.(TraceRecorder); ok {
		recorder.RecordTrace(newTraceRecord(r, req.API, req.Select, startedAt, duration, err))
	}
}

func (rt *Router) writeHandlerError(w http.ResponseWriter, r *http.Request, req *Request, buf *ResponseBuf, binaryMode bool, trace *DebugTraceSession, err error) {
	if rt.requestLogging && rt.debugParamShape {
		rt.logParamStructure(req)
	}
	PutBuf(buf)
	writeDebugTraceHeaders(w.Header(), trace)
	rt.writeAppError(w, r, binaryMode, err)
}

func (rt *Router) encodeHTTPResponse(req *Request, buf *ResponseBuf, binaryMode bool, trace *DebugTraceSession) ([]byte, error) {
	var jsonBody []byte
	var err error
	if trace == nil {
		if !binaryMode {
			jsonBody, err = rt.convertBinaryToJSON(req.API, buf.B)
		}
		return jsonBody, err
	}
	encodeStarted := trace.Start()
	if !binaryMode {
		jsonBody, err = rt.convertBinaryToJSON(req.API, buf.B)
	}
	trace.Finish(encodeStarted, DebugSpanMeta{Name: "response.encode", Category: "gateway", Operation: req.API})
	return jsonBody, err
}

func (rt *Router) writeHTTPResponse(w http.ResponseWriter, binaryBody, jsonBody []byte, binaryMode bool) {
	if binaryMode {
		writeBinaryResponseHead(w, len(binaryBody))
		_, _ = w.Write(binaryBody)
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(jsonDataPrefix)
		_, _ = w.Write(jsonBody)
		_, _ = w.Write(jsonDataSuffix)
	}
}

func writeBinaryResponseHead(w http.ResponseWriter, size int) {
	w.Header().Set("Content-Length", strconv.Itoa(size))
	w.Header().Set("Content-Type", "application/x-luxo")
	w.Header().Set("X-Luxo-Mode", "binary")
	w.WriteHeader(http.StatusOK)
}

func (rt *Router) enableDebugTrace(r *http.Request) *http.Request {
	if !debugTraceRequested(r.Header.Get(DebugTraceRequestHeader)) {
		return r
	}
	authorized := rt.devMode
	if !authorized && rt.debugTraceSecret != "" {
		authorized = subtle.ConstantTimeCompare([]byte(r.Header.Get(DebugTraceKeyHeader)), []byte(rt.debugTraceSecret)) == 1
	}
	if !authorized {
		return r
	}
	ctx, trace := WithDebugTrace(r.Context(), TraceID(r.Context()))
	trace.responseEnvelope = debugTraceRequested(r.Header.Get(DebugTraceEnvelopeHeader))
	return r.WithContext(ctx)
}

func (rt *Router) enableTrace(r *http.Request, metrics MetricsRecorder) (*http.Request, *DebugTraceSession) {
	sampler, sampling := metrics.(TraceSampler)
	if !rt.devMode && rt.debugTraceSecret == "" && !sampling {
		return r, nil
	}
	if rt.devMode || rt.debugTraceSecret != "" {
		r = rt.enableDebugTrace(r)
		if trace := DebugTrace(r.Context()); trace != nil {
			return r, trace
		}
	}
	if !sampling || !sampler.ShouldTrace(TraceID(r.Context())) {
		return r, nil
	}
	ctx, trace := WithSampledTrace(r.Context(), TraceID(r.Context()))
	return r.WithContext(ctx), trace
}

func directClientKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return host
	}
	return remoteAddr
}

func (rt *Router) prepareRequest(req *Request, binaryMode bool) error {
	return rt.prepareCallRequest(req, binaryMode, false)
}

func (rt *Router) prepareCallRequest(req *Request, binaryMode, localJSON bool) error {
	if err := rt.validatePublicSelection(req, binaryMode); err != nil {
		return err
	}
	if err := rt.applyJSONSelection(req, binaryMode); err != nil {
		return err
	}
	if binaryMode {
		return nil
	}
	if localJSON {
		return rt.Registry.prepareLocalJSONRequest(req)
	}
	return rt.Registry.prepareJSONRequest(req)
}

func (rt *Router) validatePublicSelection(req *Request, binaryMode bool) error {
	if req.Internal || rt.Schema == nil {
		return nil
	}
	apiMeta := rt.Schema.APIs[req.API]
	if apiMeta == nil || rt.Schema.LookupModel(apiMeta.ReturnType) == nil {
		return nil
	}
	if binaryMode {
		if len(req.FieldMask) == 0 {
			return fmt.Errorf("$select is required for structured response %s", apiMeta.ReturnType)
		}
		return nil
	}
	if len(req.Select) == 0 {
		return fmt.Errorf("$select is required for structured response %s", apiMeta.ReturnType)
	}
	return nil
}

func (rt *Router) applyJSONSelection(req *Request, binaryMode bool) error {
	if binaryMode || req.Select == nil || rt.Schema == nil {
		return nil
	}
	apiMeta := rt.Schema.APIs[req.API]
	if apiMeta == nil || apiMeta.ReturnType == "" {
		return nil
	}
	model := rt.Schema.LookupModel(apiMeta.ReturnType)
	if model == nil {
		return nil
	}
	mask, err := schema.SelectToFieldMask(req.Select, model, rt.Schema)
	if err == nil {
		req.FieldMask = mask
	}
	return err
}

func newTraceRecord(r *http.Request, apiName string, selected []*selection.Field, startedAt time.Time, duration time.Duration, err error) TraceRecord {
	statusCode := http.StatusOK
	if err != nil {
		statusCode = http.StatusInternalServerError
		var appErr *errors.AppError
		if stderrors.As(err, &appErr) {
			statusCode = appErr.Code
		}
	}
	record := TraceRecord{
		TraceID:       TraceID(r.Context()),
		APIName:       apiName,
		Duration:      duration,
		StatusCode:    statusCode,
		ClientName:    r.Header.Get("X-Luxo-Client"),
		ClientVersion: r.Header.Get("X-Luxo-Client-Version"),
		Timestamp:     startedAt.UTC(),
	}
	if trace := DebugTrace(r.Context()); trace != nil {
		var encodeErr error
		record.Spans, encodeErr = DebugTraceJSON(trace.Snapshot())
		if encodeErr != nil {
			record.Spans = failedDebugTraceJSON
		}
		record.Selection = selection.Format(selected)
		record.HeadSampled = trace.Sampled()
	}
	return record
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
		// Unreachable with Schema's fixed JSON-safe fields; retain the public encoder's error contract.
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
func (rt *Router) convertBinaryToJSON(apiName string, data []byte) ([]byte, error) {
	if len(data) == 0 && (rt.Schema == nil || rt.Schema.APIs[apiName] == nil || rt.Schema.APIs[apiName].ReturnType == "") {
		return []byte("null"), nil
	}
	if rt.Schema == nil {
		return data, nil // no schema — assume handler wrote JSON directly
	}
	apiMeta := rt.Schema.APIs[apiName]
	if apiMeta == nil {
		return data, nil // unknown API — pass through as-is
	}
	if apiMeta.ReturnNullable {
		value, null, err := codec.UnwrapNullableRoot(data)
		if err != nil {
			return nil, err
		}
		if null {
			return []byte("null"), nil
		}
		data = value
	}

	// No return type → scalar (delete returns count as Int)
	if apiMeta.ReturnType == "" {
		return schema.BinaryScalarToJSON(nil, data, "Int")
	}

	// Check if return type is a model, type declaration, or scalar
	model := rt.Schema.LookupModel(apiMeta.ReturnType)
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
func (rt *Router) StreamPayloadJSON(apiName string, data []byte) ([]byte, error) {
	return rt.convertBinaryToJSON(apiName, data)
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
		werr = appendWireErrorFields(buf, werr, true)
		w.Header().Set("Content-Type", "application/x-luxo")
		w.Header().Set("X-Luxo-Mode", "binary")
	} else {
		buf.AppendByte('{')
		werr = appendWireErrorFields(buf, werr, false)
		buf.AppendByte('}')
	}

	w.WriteHeader(werr.Code)
	w.Write(buf.B)
	PutBuf(buf)
}

func (rt *Router) buildWireError(ctx context.Context, acceptLanguage string, err error) wireError {
	appErr, ok := stderrors.AsType[*errors.AppError](err)
	if !ok {
		appErr = errors.Wrap(err)
	}

	var data []byte
	if appErr.Data != nil && !appErr.Internal {
		var encodeErr error
		data, encodeErr = appErr.Data.AppendJSON(nil)
		if encodeErr != nil {
			appErr, data = errors.Wrap(encodeErr), nil
		}
	}
	message := appErr.Message
	if appErr.Internal {
		message = "error.internal"
	}
	if rt.translator != nil && !appErr.Internal {
		locale := i18n.ParseAcceptLanguage(acceptLanguage)
		message = rt.translator.Translate(locale, appErr.Message, appErr.Data)
	}

	werr := wireError{
		Code:    appErr.Code,
		Name:    appErr.Name,
		Message: message,
		TraceID: TraceID(ctx),
		Data:    data,
	}
	if rt.devMode && appErr.Cause != nil {
		werr.Cause = appErr.Cause.Error()
	}
	return werr
}

func appendJSONErrorFields(buf *ResponseBuf, e wireError) error {
	buf.AppendString(`"error":`)
	if err := buf.AppendJSONString(e.Name); err != nil {
		return err
	}
	buf.AppendString(`,"code":`)
	buf.AppendInt(int64(e.Code))
	buf.AppendString(`,"message":`)
	if err := buf.AppendJSONString(e.Message); err != nil {
		return err
	}
	if e.TraceID != "" {
		buf.AppendString(`,"traceId":`)
		if err := buf.AppendJSONString(e.TraceID); err != nil {
			return err
		}
	}
	if len(e.Data) > 0 {
		buf.AppendString(`,"data":`)
		buf.B = append(buf.B, e.Data...)
	}
	if e.Cause != "" {
		buf.AppendString(`,"cause":`)
		if err := buf.AppendJSONString(e.Cause); err != nil {
			return err
		}
	}
	return nil
}

// appendWireErrorFields discards the entire invalid error, not just its text.
// The fallback consists only of fixed valid strings and cannot recurse.
func appendWireErrorFields(buf *ResponseBuf, value wireError, binaryMode bool) wireError {
	prefix := len(buf.B)
	var err error
	if binaryMode {
		buf.B, err = appendBinaryError(buf.B, value)
	} else {
		err = appendJSONErrorFields(buf, value)
	}
	if err == nil {
		return value
	}
	buf.B = buf.B[:prefix]
	value = wireError{Code: 500, Name: "Internal", Message: "error.internal"}
	if binaryMode {
		buf.B = codec.AppendSvarint(append(buf.B, binaryErrorCodeField), 500)
		buf.B = codec.AppendBytes(append(buf.B, binaryErrorNameField), []byte("Internal"))
		buf.B = codec.AppendBytes(append(buf.B, binaryErrorMessageField), []byte("error.internal"))
		buf.AppendByte(0)
	} else {
		buf.AppendString(`"error":"Internal","code":500,"message":"error.internal"`)
	}
	return value
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
	moduleColorMap atomic.Pointer[map[string]string]
	moduleColorIdx int
)

func moduleColor(mod string) string {
	if snapshot := moduleColorMap.Load(); snapshot != nil {
		if color, ok := (*snapshot)[mod]; ok {
			return color
		}
	}
	return registerModuleColor(mod)
}

func registerModuleColor(mod string) string {
	moduleColorMu.Lock()
	defer moduleColorMu.Unlock()
	snapshot := moduleColorMap.Load()
	if snapshot != nil {
		if color, ok := (*snapshot)[mod]; ok {
			return color
		}
	}
	colors := make(map[string]string)
	if snapshot != nil {
		for name, color := range *snapshot {
			colors[name] = color
		}
	}
	color := moduleColors[moduleColorIdx%len(moduleColors)]
	colors[mod] = color
	moduleColorIdx++
	// Publish immutable metadata; accepted request logs do not take this lock.
	moduleColorMap.Store(&colors)
	return color
}

// logRequest prints a colored request log line:
//
//	12:34:56 [auth] login 2.3ms ✓
//	12:34:56 [auth] login 1.2ms ✗ InvalidCredentials
func (rt *Router) logRequest(apiName string, duration time.Duration, err error) {
	rt.logRequestTiming(apiName, duration, -1, err)
}

func (rt *Router) logHTTPCompletion(name string, duration time.Duration, started time.Time, err error) {
	if !started.IsZero() && rt.shouldLogRequest(err) {
		rt.logAcceptedRequestTiming(name, duration, time.Since(started), err)
	}
}

func (rt *Router) shouldLogRequest(err error) bool {
	return rt.requestLogging && (err != nil || rt.logSampleEvery <= 1 || rt.logSequence.Add(1)%rt.logSampleEvery == 0)
}

func (rt *Router) logRequestTiming(apiName string, duration, serverDuration time.Duration, err error) {
	if rt.shouldLogRequest(err) {
		rt.logAcceptedRequestTiming(apiName, duration, serverDuration, err)
	}
}

func (rt *Router) logAcceptedRequestTiming(apiName string, duration, serverDuration time.Duration, err error) {
	mod := ""
	if rt.Schema != nil {
		if a, ok := rt.Schema.APIs[apiName]; ok {
			mod = a.Module
		}
	}
	if mod == "" {
		mod = "api"
	}

	failure := ""
	if err != nil {
		failure = safeLogErrorName(err)
	}
	if !rt.allowLogParts(mod, apiName, failure) {
		return
	}
	buf := requestLogBuffers.Get().(*[]byte)
	*buf = appendRequestLog((*buf)[:0], time.Now(), mod, moduleColor(mod), apiName, duration, failure)
	if serverDuration >= 0 {
		*buf = append((*buf)[:len(*buf)-1], " server="...)
		*buf = strconv.AppendFloat(*buf, float64(serverDuration)/float64(time.Millisecond), 'f', 1, 64)
		*buf = append(*buf, "ms\n"...)
	}
	rt.writeLogBuffer(buf)
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
	if req.Params != nil {
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
	if !rt.allowLogParts(name, typeName) {
		return
	}
	buf := requestLogBuffers.Get().(*[]byte)
	*buf = append((*buf)[:0], "    param["...)
	*buf = strconv.AppendInt(*buf, int64(index), 10)
	*buf = append(*buf, "] "...)
	*buf = append(*buf, name...)
	*buf = append(*buf, ' ')
	*buf = append(*buf, typeName...)
	*buf = append(*buf, " present="...)
	*buf = strconv.AppendBool(*buf, present)
	*buf = append(*buf, '\n')
	rt.writeLogBuffer(buf)
}

// writeError writes a simple JSON error response for infrastructure errors.
func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	// Escape msg to prevent JSON injection
	escaped := strconv.Quote(msg)
	w.Write([]byte(`{"error":` + escaped + `}`))
}
