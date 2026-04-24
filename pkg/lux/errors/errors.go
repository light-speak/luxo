// Package errors provides structured error types for the Luxo runtime.
// All application errors use AppError, which carries an HTTP status code,
// error name, i18n message key, and typed data for template substitution.
package errors

// ErrorData is the interface for typed error data attached to AppError.
// Implementations: ParamError, ResourceError, MapData.
type ErrorData interface {
	// I18nData returns key-value pairs for i18n template substitution.
	I18nData() map[string]any
}

// AppError is the structured error type for all Luxo application errors.
type AppError struct {
	Name     string    // PascalCase identifier: "NotFound", "OutOfStock"
	Code     int       // HTTP status code: 404, 409, 500
	Message  string    // i18n key: "error.not_found"
	Data     ErrorData // typed data: ParamError, ResourceError, MapData, or nil
	Internal bool      // if true, message/data not exposed to client
	Cause    error     // wrapped underlying error
}

// ParamError is error data for invalid parameters.
type ParamError struct {
	Param string `json:"param"`
	Error string `json:"error"`
}

func (p ParamError) I18nData() map[string]any {
	return map[string]any{"param": p.Param, "error": p.Error}
}

// ResourceError is error data for missing resources.
type ResourceError struct {
	Resource string `json:"resource"`
	ID       any    `json:"id,omitempty"`
}

func (r ResourceError) I18nData() map[string]any {
	m := map[string]any{"resource": r.Resource}
	if r.ID != nil {
		m["id"] = r.ID
	}
	return m
}

// MapData is typed error data from a key-value map.
// Used by generated error constructors for custom error fields.
type MapData map[string]any

func (d MapData) I18nData() map[string]any {
	return map[string]any(d)
}

func (e *AppError) Error() string {
	if e.Cause != nil {
		return e.Name + ": " + e.Cause.Error()
	}
	return e.Name
}

func (e *AppError) Unwrap() error {
	return e.Cause
}

// New creates an AppError with the given name, HTTP status code, and i18n message key.
func New(name string, code int, message string) *AppError {
	return &AppError{Name: name, Code: code, Message: message}
}

// WithData returns a copy with the given data fields set.
// The original error is not modified.
func (e *AppError) WithData(data ErrorData) *AppError {
	cp := *e
	cp.Data = data
	return &cp
}

// WithCause returns a copy with a wrapped cause error.
// The original error is not modified.
func (e *AppError) WithCause(cause error) *AppError {
	cp := *e
	cp.Cause = cause
	return &cp
}

// Wrap wraps a plain error as an Internal AppError (500).
func Wrap(err error) *AppError {
	return &AppError{
		Name:     "Internal",
		Code:     500,
		Message:  "error.internal",
		Internal: true,
		Cause:    err,
	}
}

// Built-in errors. Use WithData/WithCause to create concrete instances.
var (
	NotFound         = New("NotFound", 404, "error.not_found")
	Unauthorized     = New("Unauthorized", 401, "error.unauthorized")
	Forbidden        = New("Forbidden", 403, "error.forbidden")
	BadRequest       = New("BadRequest", 400, "error.bad_request")
	Conflict         = New("Conflict", 409, "error.conflict")
	InternalError    = New("Internal", 500, "error.internal")
	ValidationFailed = New("ValidationFailed", 422, "error.validation_failed")
	RateLimited      = New("RateLimited", 429, "error.rate_limited")
	ServiceTimeout   = New("ServiceTimeout", 502, "error.service_timeout")
)
