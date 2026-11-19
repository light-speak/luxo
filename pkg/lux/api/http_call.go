package api

import (
	"context"
	"net/http"
	"time"
)

// httpHandlerCall keeps ownership and observation together while a native
// handler or a user callback unwinds without returning to its caller.
// Keep the HTTP request separate: sharing this state with the handler's request
// makes Request.WithContext copies escape through the handler argument.
type httpHandlerCall struct {
	router          *Router
	req             *Request
	metrics         MetricsRecorder
	buf             *ResponseBuf
	startedAt       time.Time
	duration        time.Duration
	handlerReturned bool
	recorded        bool
}

func (c *httpHandlerCall) invoke(ctx context.Context, fn HandlerFunc, trace *DebugTraceSession) error {
	observed := c.metrics != nil || c.router.requestLogging || trace != nil
	if observed {
		c.startedAt = time.Now()
	}
	if trace != nil {
		var span DebugSpanHandle
		ctx, span = trace.StartSpan(ctx, DebugSpanMeta{
			Name: "handler.execute", Category: "gateway", Operation: c.req.API,
		})
		defer trace.FinishSpan(span)
	}
	err := c.router.callHandler(fn, ctx, c.req)
	c.handlerReturned = true
	if observed {
		c.duration = time.Since(c.startedAt)
	}
	return err
}

func (c *httpHandlerCall) record(r *http.Request, statusCode int) {
	// Mark first so a collector that exits cannot trigger a second notification.
	c.recorded = true
	c.router.recordRequestMetrics(r, c.req, c.metrics, c.startedAt, c.duration, statusCode)
}

func (c *httpHandlerCall) release() {
	if c.buf != nil {
		PutBuf(c.buf)
		c.buf = nil
	}
}

func (c *httpHandlerCall) finish(r *http.Request) {
	// Release before invoking optional observers; those can also abort.
	c.release()
	if c.recorded || c.metrics == nil {
		return
	}
	if !c.handlerReturned {
		c.duration = time.Since(c.startedAt)
	}
	c.record(r, http.StatusInternalServerError)
}

// Prepare the response once, before observation. Error callbacks must not run
// again just to classify a trace or completion log.
func (c *httpHandlerCall) fail(w http.ResponseWriter, r *http.Request, binaryMode bool, trace *DebugTraceSession, logStarted time.Time, source error) {
	c.release()
	if c.router.requestLogging && c.router.debugParamShape {
		c.router.logParamStructure(c.req)
	}
	writeDebugTraceHeaders(w.Header(), trace)
	werr := c.router.buildWireError(r.Context(), r.Header.Get("Accept-Language"), source)
	c.buf = GetBuf()
	werr = appendAppErrorResponse(c.buf, werr, binaryMode)
	c.record(r, werr.Code)
	writeAppErrorResponse(w, c.buf, werr.Code, binaryMode)
	c.release()
	c.router.logHTTPFailure(c.req.API, c.duration, logStarted, werr.Name)
}
