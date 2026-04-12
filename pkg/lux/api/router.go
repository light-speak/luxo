package api

import (
	"context"
	stderrors "errors"
	"fmt"
	"net/http"

	"github.com/bytedance/sonic"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/i18n"
)

// HandlerFunc is the signature for API handlers.
// Handlers write their response directly to req.Buf — zero allocation, zero any.
type HandlerFunc func(ctx context.Context, req *Request) error

// Router maps API names to handlers and serves the /luvia endpoint.
type Router struct {
	handlers   map[string]HandlerFunc
	translator *i18n.Translator
	devMode    bool
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

// SetTranslator configures i18n translation for error responses.
func (rt *Router) SetTranslator(t *i18n.Translator) {
	rt.translator = t
}

// SetDevMode enables development mode (includes cause in error responses).
func (rt *Router) SetDevMode(dev bool) {
	rt.devMode = dev
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
		rt.writeAppError(w, r, errors.NotFound.WithData(map[string]any{
			"resource": req.API,
		}))
		return
	}

	// Get pooled buffer, set on request, handler writes directly
	buf := GetBuf()
	req.Buf = buf

	herr := rt.callHandler(fn, r.Context(), req)
	if herr != nil {
		PutBuf(buf)
		rt.writeAppError(w, r, herr)
		return
	}

	// Write response: {"data": <buf> }
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"data":`))
	w.Write(buf.B)
	w.Write([]byte(`}`))

	PutBuf(buf)
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
func (rt *Router) writeAppError(w http.ResponseWriter, r *http.Request, err error) {
	var appErr *errors.AppError
	if !stderrors.As(err, &appErr) {
		appErr = errors.Wrap(err)
	}

	traceID := TraceID(r.Context())

	message := appErr.Message
	if rt.translator != nil && !appErr.Internal {
		locale := i18n.ParseAcceptLanguage(r.Header.Get("Accept-Language"))
		message = rt.translator.Translate(locale, appErr.Message, appErr.Data)
	}

	buf := GetBuf()
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
		buf.AppendString(`,"data":`)
		dataBytes, _ := sonic.Marshal(appErr.Data)
		buf.B = append(buf.B, dataBytes...)
	}
	if rt.devMode && appErr.Cause != nil {
		buf.AppendString(`,"cause":`)
		buf.AppendJSONString(appErr.Cause.Error())
	}
	buf.AppendByte('}')

	w.WriteHeader(appErr.Code)
	w.Write(buf.B)
	PutBuf(buf)
}

// writeError writes a simple JSON error response for infrastructure errors.
func writeError(w http.ResponseWriter, code int, msg string) {
	w.WriteHeader(code)
	w.Write([]byte(`{"error":"`))
	w.Write([]byte(msg))
	w.Write([]byte(`"}`))
}
