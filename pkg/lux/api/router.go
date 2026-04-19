package api

import (
	"context"
	stderrors "errors"
	"fmt"
	"net/http"

	"github.com/bytedance/sonic"
	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/i18n"
)

// Pre-allocated response framing bytes — avoids per-request []byte conversion.
var (
	jsonDataPrefix = []byte(`{"data":`)
	jsonDataSuffix = []byte(`}`)
)

// HandlerFunc is the signature for API handlers.
// Handlers write their response directly to req.Buf — zero allocation, zero any.
type HandlerFunc func(ctx context.Context, req *Request) error

// Router maps API names to handlers and serves the /luvia endpoint.
type Router struct {
	handlers   map[string]HandlerFunc
	translator *i18n.Translator
	devMode    bool
	Registry   *APIRegistry // binary protocol API ID mapping
}

// NewRouter creates an empty router.
func NewRouter() *Router {
	return &Router{
		handlers: make(map[string]HandlerFunc),
		Registry: NewAPIRegistry(),
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

// ExportHandlers returns the handler map for sharing with RPC server.
func (rt *Router) ExportHandlers() map[string]HandlerFunc {
	return rt.handlers
}

// ServeHTTP implements http.Handler for the /luvia endpoint.
// Supports JSON (default) and Luxo binary (X-Luxo-Mode: binary) protocols.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		defer bodyPool.Put(bp)
		req, err = rt.Registry.ParseBinaryRequest(body)
	} else {
		req, err = ParseRequest(r)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	fn, ok := rt.handlers[req.API]
	if !ok {
		rt.writeAppError(w, r, binaryMode, errors.NotFound.WithData(errors.ResourceError{Resource: req.API}))
		return
	}

	// Get pooled buffer, set on request, handler writes directly
	buf := GetBuf()
	req.Buf = buf

	herr := rt.callHandler(fn, r.Context(), req)
	if herr != nil {
		PutBuf(buf)
		rt.writeAppError(w, r, binaryMode, herr)
		return
	}

	if binaryMode {
		w.Header().Set("Content-Type", "application/x-luxo")
		w.Header().Set("X-Luxo-Mode", "binary")
		w.WriteHeader(http.StatusOK)
		w.Write(buf.B)
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(jsonDataPrefix)
		w.Write(buf.B)
		w.Write(jsonDataSuffix)
	}

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
			buf.AppendString(`,"data":`)
			dataBytes, _ := sonic.Marshal(appErr.Data)
			buf.B = append(buf.B, dataBytes...)
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

// writeError writes a simple JSON error response for infrastructure errors.
func writeError(w http.ResponseWriter, code int, msg string) {
	w.WriteHeader(code)
	w.Write([]byte(`{"error":"`))
	w.Write([]byte(msg))
	w.Write([]byte(`"}`))
}
