package api

import (
	"errors"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

const debugTraceVersion = 4

var errInvalidDebugTrace = errors.New("invalid debug trace metadata")

type debugTextField struct {
	id    int
	name  string
	value string
}

// debugTextFields shares the static field mapping between Binary and JSON.
func debugTextFields(span *DebugSpan) [15]debugTextField {
	return [15]debugTextField{
		{1, "name", span.Name}, {2, "category", span.Category},
		{3, "service", span.Service}, {4, "operation", span.Operation}, {5, "field", span.Field},
		{10, "fieldPath", span.FieldPath}, {11, "dependency", string(span.Dependency)}, {12, "selection", span.Selection},
		{13, "databaseBackend", span.DatabaseBackend}, {14, "databaseName", span.DatabaseName},
		{15, "databaseOperation", span.DatabaseOperation}, {16, "databaseResource", span.DatabaseResource},
		{17, "statement", span.Statement}, {18, "fingerprint", span.Fingerprint}, {21, "errorCode", span.ErrorCode},
	}
}

// EncodeDebugTrace encodes a trace without reflection or JSON serialization.
// Failure returns no partial trace; transports use EncodeDebugTraceResult to
// report diagnostic failure without changing the business response.
func EncodeDebugTrace(snapshot DebugTraceSnapshot) ([]byte, error) {
	if (snapshot.TraceID == "" && !snapshot.EncodingFailed) || snapshot.Duration < 0 {
		return nil, errInvalidDebugTrace
	}
	var enc codec.Encoder
	enc.WriteVarint(debugTraceVersion)
	if err := enc.WriteFieldString(1, snapshot.TraceID); err != nil {
		return nil, err
	}
	enc.WriteFieldInt(2, int64(snapshot.Duration))
	enc.WriteFieldHeader(3)
	enc.WriteArrayHeader(len(snapshot.Spans))
	for i := range snapshot.Spans {
		span, err := encodeDebugSpan(&snapshot.Spans[i])
		if err != nil {
			return nil, err
		}
		enc.WriteBytes(span)
	}
	if snapshot.Truncated {
		enc.WriteFieldBool(4, true)
	}
	if snapshot.EncodingFailed {
		enc.WriteFieldBool(5, true)
	}
	enc.WriteEnd()
	if len(enc.Bytes()) > MaxDebugResponseTraceBytes {
		return nil, errors.New("debug trace exceeds maximum encoded size")
	}
	return enc.Bytes(), nil
}

// EncodeDebugTraceResult encodes either a complete trace or a fixed diagnostic
// failure marker. No invalid text or partially encoded spans escape on failure.
func EncodeDebugTraceResult(snapshot DebugTraceSnapshot) []byte {
	data, err := EncodeDebugTrace(snapshot)
	if err != nil {
		return []byte{debugTraceVersion, 5, 1, 0}
	}
	return data
}

func encodeDebugSpan(span *DebugSpan) ([]byte, error) {
	if !validDebugSpan(span) {
		return nil, errInvalidDebugTrace
	}
	var enc codec.Encoder
	for _, field := range debugTextFields(span) {
		if field.id > 2 && field.value == "" {
			continue
		}
		if err := enc.WriteFieldString(field.id, field.value); err != nil {
			return nil, err
		}
	}
	enc.WriteFieldInt(6, int64(span.Start))
	enc.WriteFieldInt(7, int64(span.Duration))
	enc.WriteFieldInt(8, int64(span.ID))
	if span.ParentID != 0 {
		enc.WriteFieldInt(9, int64(span.ParentID))
	}
	if span.ArgumentCount != 0 {
		enc.WriteFieldInt(19, int64(span.ArgumentCount))
	}
	if span.RowsKnown {
		enc.WriteFieldInt(20, span.RowsAffected)
	}
	if span.StatementTruncated {
		enc.WriteFieldBool(22, true)
	}
	enc.WriteEnd()
	// The encoder owns this buffer and is not reused after returning.
	return enc.Bytes(), nil
}

func validDebugSpan(span *DebugSpan) bool {
	return span.Name != "" && span.Category != "" && span.Start >= 0 && span.Duration >= 0
}
