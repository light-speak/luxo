package errors

import (
	stderrors "errors"
	"fmt"
	"testing"
)

func TestAppErrorError(t *testing.T) {
	e := New("NotFound", 404, "error.not_found")
	if e.Error() != "NotFound" {
		t.Errorf("got %q", e.Error())
	}
}

func TestAppErrorErrorWithCause(t *testing.T) {
	e := New("Internal", 500, "error.internal").WithCause(fmt.Errorf("db down"))
	if e.Error() != "Internal: db down" {
		t.Errorf("got %q", e.Error())
	}
}

func TestAppErrorUnwrap(t *testing.T) {
	cause := fmt.Errorf("connection refused")
	e := New("Internal", 500, "error.internal").WithCause(cause)
	if e.Unwrap() != cause {
		t.Error("Unwrap should return cause")
	}
}

func TestAppErrorUnwrapNil(t *testing.T) {
	e := New("NotFound", 404, "error.not_found")
	if e.Unwrap() != nil {
		t.Error("Unwrap should return nil when no cause")
	}
}

func TestErrorsAs(t *testing.T) {
	e := NotFound.WithData(ResourceError{Resource: "User"})
	var wrapped error = e

	var appErr *AppError
	if !stderrors.As(wrapped, &appErr) {
		t.Fatal("errors.As should match AppError")
	}
	if appErr.Code != 404 {
		t.Errorf("code = %d, want 404", appErr.Code)
	}
	re, ok := appErr.Data.(ResourceError)
	if !ok || re.Resource != "User" {
		t.Error("data should be ResourceError with Resource=User")
	}
}

func TestWithDataCopies(t *testing.T) {
	original := NotFound
	derived := original.WithData(ResourceError{Resource: "Post"})

	if original.Data != nil {
		t.Error("original should not be modified")
	}
	re, ok := derived.Data.(ResourceError)
	if !ok || re.Resource != "Post" {
		t.Error("derived should have data")
	}
	if derived.Name != "NotFound" || derived.Code != 404 {
		t.Error("derived should copy name and code")
	}
}

func TestWithCauseCopies(t *testing.T) {
	original := InternalError
	cause := fmt.Errorf("disk full")
	derived := original.WithCause(cause)

	if original.Cause != nil {
		t.Error("original should not be modified")
	}
	if derived.Cause != cause {
		t.Error("derived should have cause")
	}
}

func TestWrap(t *testing.T) {
	cause := fmt.Errorf("unexpected EOF")
	e := Wrap(cause)

	if e.Name != "Internal" {
		t.Errorf("name = %q, want Internal", e.Name)
	}
	if e.Code != 500 {
		t.Errorf("code = %d, want 500", e.Code)
	}
	if !e.Internal {
		t.Error("should be internal")
	}
	if e.Cause != cause {
		t.Error("should wrap cause")
	}
}

func TestBuiltinErrors(t *testing.T) {
	tests := []struct {
		err  *AppError
		name string
		code int
	}{
		{NotFound, "NotFound", 404},
		{Unauthorized, "Unauthorized", 401},
		{Forbidden, "Forbidden", 403},
		{BadRequest, "BadRequest", 400},
		{Conflict, "Conflict", 409},
		{InternalError, "Internal", 500},
		{ValidationFailed, "ValidationFailed", 422},
		{RateLimited, "RateLimited", 429},
		{ServiceTimeout, "ServiceTimeout", 502},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Name != tt.name {
				t.Errorf("name = %q, want %q", tt.err.Name, tt.name)
			}
			if tt.err.Code != tt.code {
				t.Errorf("code = %d, want %d", tt.err.Code, tt.code)
			}
		})
	}
}

func TestErrorsAsChain(t *testing.T) {
	// AppError wrapped in fmt.Errorf should still be extractable via errors.As
	inner := NotFound.WithData(ResourceError{Resource: "Order"})
	wrapped := fmt.Errorf("handler: %w", inner)

	var appErr *AppError
	if !stderrors.As(wrapped, &appErr) {
		t.Fatal("errors.As should find AppError in chain")
	}
	if appErr.Code != 404 {
		t.Errorf("code = %d, want 404", appErr.Code)
	}
}

func TestNew(t *testing.T) {
	e := New("CustomError", 418, "error.teapot")
	if e.Name != "CustomError" || e.Code != 418 || e.Message != "error.teapot" {
		t.Errorf("got %+v", e)
	}
	if e.Data != nil || e.Cause != nil || e.Internal {
		t.Error("new error should have zero optional fields")
	}
}

func TestBuiltinErrorData(t *testing.T) {
	for _, test := range []struct {
		data        ErrorData
		json        string
		field, text string
	}{
		{ParamError{Param: "email", Error: "invalid format"}, `{"param":"email","error":"invalid format"}`, "param", "email"},
		{ParamError{Param: "email", Error: "invalid format"}, `{"param":"email","error":"invalid format"}`, "error", "invalid format"},
		{ResourceError{Resource: "User", ID: IntID(42)}, `{"resource":"User","id":42}`, "id", "42"},
		{ResourceError{Resource: "User", ID: IntID(0)}, `{"resource":"User","id":0}`, "resource", "User"},
		{ResourceError{Resource: "User", ID: StringID("")}, `{"resource":"User","id":""}`, "id", ""},
		{ResourceError{Resource: "User", ID: UUIDID([16]byte{15: 1})}, `{"resource":"User","id":"00000000-0000-0000-0000-000000000001"}`, "id", "00000000-0000-0000-0000-000000000001"},
		{ResourceError{Resource: "Post"}, `{"resource":"Post"}`, "resource", "Post"},
	} {
		got, err := test.data.AppendJSON(nil)
		if err != nil || string(got) != test.json {
			t.Fatalf("%s: %v; want %s", got, err, test.json)
		}
		text, found := test.data.AppendText([]byte("prefix:"), test.field)
		if !found || string(text) != "prefix:"+test.text {
			t.Fatalf("text: %s, %v", text, found)
		}
		text, found = test.data.AppendText([]byte("prefix:"), "missing")
		if found || string(text) != "prefix:" {
			t.Fatal("unknown field changed prefix")
		}
	}
	var absent ErrorData = ResourceError{Resource: "Post"}
	if _, found := absent.AppendText(nil, "id"); found {
		t.Fatal("absent ID interpolated")
	}
}

func TestBuiltinInternalErrorIsPrivate(t *testing.T) {
	if !InternalError.Internal || !InternalError.WithData(ParamError{Param: "private"}).Internal {
		t.Fatal("built-in Internal error must retain the privacy flag")
	}
}
