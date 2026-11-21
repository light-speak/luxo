package builtinerror

// GoName resolves the supported source spellings to runtime error exports.
func GoName(name string) string {
	switch name {
	case "NotFound", "notFound", "not_found":
		return "NotFound"
	case "Unauthorized", "unauthorized":
		return "Unauthorized"
	case "Forbidden", "forbidden":
		return "Forbidden"
	case "BadRequest", "badRequest", "bad_request":
		return "BadRequest"
	case "Conflict", "conflict":
		return "Conflict"
	case "InternalError", "internalError", "internal_error":
		return "InternalError"
	case "ValidationFailed", "validationFailed", "validation_failed":
		return "ValidationFailed"
	case "RateLimited", "rateLimited", "rate_limited":
		return "RateLimited"
	case "ServiceTimeout", "serviceTimeout", "service_timeout":
		return "ServiceTimeout"
	}
	return ""
}
