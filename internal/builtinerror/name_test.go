package builtinerror

import "testing"

func TestGoName(t *testing.T) {
	for _, names := range [][]string{
		{"NotFound", "notFound", "not_found"},
		{"Unauthorized", "unauthorized"},
		{"Forbidden", "forbidden"},
		{"BadRequest", "badRequest", "bad_request"},
		{"Conflict", "conflict"},
		{"InternalError", "internalError", "internal_error"},
		{"ValidationFailed", "validationFailed", "validation_failed"},
		{"RateLimited", "rateLimited", "rate_limited"},
		{"ServiceTimeout", "serviceTimeout", "service_timeout"},
	} {
		for _, name := range names {
			if got := GoName(name); got != names[0] {
				t.Fatalf("%s resolved to %q", name, got)
			}
		}
	}
	for _, name := range []string{"", "InvalidParam", "NotFount", "Internal", "NOT_FOUND", "not__found"} {
		if GoName(name) != "" {
			t.Fatalf("unknown error %q accepted", name)
		}
	}
}
