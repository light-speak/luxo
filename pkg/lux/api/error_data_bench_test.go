package api

import (
	"bytes"
	"context"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/errors"
	"github.com/light-speak/luxo/pkg/lux/i18n"
	"github.com/light-speak/luxo/pkg/lux/jsonutil"
)

// Frozen pre-validation reference for the error-path correctness cost only.
// It intentionally accepts malformed native JSON and is never used in production.
func (rt *Router) buildWireErrorBeforeDataValidation(ctx context.Context, acceptLanguage string, err error) wireError {
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

var errorDataValidationSink wireError

func BenchmarkErrorDataValidation(b *testing.B) {
	router := NewRouter()
	benchmarkWireError(b, router.buildWireErrorBeforeDataValidation, router.buildWireErrorBeforePanicIsolation)
}

func BenchmarkErrorPanicIsolation(b *testing.B) {
	router := NewRouter()
	benchmarkWireError(b, router.buildWireErrorBeforePanicIsolation, router.buildWireError)
}

func BenchmarkErrorPanicRecovery(b *testing.B) {
	router := NewRouter()
	value := errors.BadRequest.WithData(panickingErrorData{})
	b.ReportAllocs()
	for b.Loop() {
		errorDataValidationSink = router.buildWireError(b.Context(), "", value)
	}
}

func benchmarkWireError(b *testing.B, before, after func(context.Context, string, error) wireError) {
	for _, sample := range []struct {
		name string
		err  error
	}{
		{"NoData", errors.BadRequest},
		{"Internal", errors.InternalError.WithData(uncheckedErrorData(`{"secret":1}`))},
		{"Small", errors.BadRequest.WithData(uncheckedErrorData(`{"n":1}`))},
		{"Precision", errors.BadRequest.WithData(uncheckedErrorData(`{"max":9223372036854775807,"huge":123456789012345678901234567890,"decimal":1.234567890123456789,"text":"test"}`))},
		{"Long", errors.BadRequest.WithData(uncheckedErrorData(`{"text":"` + strings.Repeat("abc", 24576) + `"}`))},
	} {
		b.Run(sample.name, func(b *testing.B) {
			oldValue, newValue := before(b.Context(), "", sample.err), after(b.Context(), "", sample.err)
			if oldValue.Code != newValue.Code || oldValue.Name != newValue.Name ||
				oldValue.Message != newValue.Message || oldValue.TraceID != newValue.TraceID ||
				oldValue.Cause != newValue.Cause || !bytes.Equal(oldValue.Data, newValue.Data) {
				b.Fatal("panic isolation changed a valid error response")
			}
			for _, mode := range []struct {
				name string
				call func(context.Context, string, error) wireError
			}{
				{"Before", before}, {"After", after},
			} {
				b.Run(mode.name, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						errorDataValidationSink = mode.call(b.Context(), "", sample.err)
					}
				})
			}
		})
	}
}

// Frozen validated reference before panic isolation; not a production alternative.
func (rt *Router) buildWireErrorBeforePanicIsolation(ctx context.Context, acceptLanguage string, err error) wireError {
	appErr, ok := stderrors.AsType[*errors.AppError](err)
	if !ok {
		appErr = errors.Wrap(err)
	}

	var data []byte
	if appErr.Data != nil && !appErr.Internal {
		var encodeErr error
		data, encodeErr = appErr.Data.AppendJSON(nil)
		if encodeErr == nil && !jsonutil.ValidUTF8JSON(data) {
			encodeErr = stderrors.New("invalid error data JSON")
		}
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
