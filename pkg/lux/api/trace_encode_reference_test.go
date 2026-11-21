package api

import (
	"strconv"
	"time"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

// Frozen immediately before trace error propagation. Binary uses the same strict
// String writers on both sides; this isolates trace handling, not all UTF-8 cost.
// encodeDebugTraceBeforeTextErrors encodes a trace without reflection or JSON serialization.
func encodeDebugTraceBeforeTextErrors(snapshot DebugTraceSnapshot) []byte {
	spans := make([][]byte, len(snapshot.Spans))
	for i := range snapshot.Spans {
		spans[i] = encodeDebugSpanBeforeTextErrors(snapshot.Spans[i])
	}
	var enc codec.Encoder
	enc.WriteFieldString(1, snapshot.TraceID)
	enc.WriteFieldInt(2, int64(snapshot.Duration))
	enc.WriteFieldBytesArray(3, spans)
	if snapshot.Truncated {
		enc.WriteFieldBool(4, true)
	}
	enc.WriteEnd()
	return append([]byte{3}, enc.Bytes()...)
}

func encodeDebugSpanBeforeTextErrors(span DebugSpan) []byte {
	var enc codec.Encoder
	enc.WriteFieldString(1, span.Name)
	enc.WriteFieldString(2, span.Category)
	if span.Service != "" {
		enc.WriteFieldString(3, span.Service)
	}
	if span.Operation != "" {
		enc.WriteFieldString(4, span.Operation)
	}
	if span.Field != "" {
		enc.WriteFieldString(5, span.Field)
	}
	enc.WriteFieldInt(6, int64(span.Start))
	enc.WriteFieldInt(7, int64(span.Duration))
	enc.WriteFieldInt(8, int64(span.ID))
	if span.ParentID != 0 {
		enc.WriteFieldInt(9, int64(span.ParentID))
	}
	if span.FieldPath != "" {
		enc.WriteFieldString(10, span.FieldPath)
	}
	if span.Dependency != "" {
		enc.WriteFieldString(11, string(span.Dependency))
	}
	if span.Selection != "" {
		enc.WriteFieldString(12, span.Selection)
	}
	if span.DatabaseBackend != "" {
		enc.WriteFieldString(13, span.DatabaseBackend)
	}
	if span.DatabaseName != "" {
		enc.WriteFieldString(14, span.DatabaseName)
	}
	if span.DatabaseOperation != "" {
		enc.WriteFieldString(15, span.DatabaseOperation)
	}
	if span.DatabaseResource != "" {
		enc.WriteFieldString(16, span.DatabaseResource)
	}
	if span.Statement != "" {
		enc.WriteFieldString(17, span.Statement)
	}
	if span.Fingerprint != "" {
		enc.WriteFieldString(18, span.Fingerprint)
	}
	if span.ArgumentCount != 0 {
		enc.WriteFieldInt(19, int64(span.ArgumentCount))
	}
	if span.RowsKnown {
		enc.WriteFieldInt(20, span.RowsAffected)
	}
	if span.ErrorCode != "" {
		enc.WriteFieldString(21, span.ErrorCode)
	}
	if span.StatementTruncated {
		enc.WriteFieldBool(22, true)
	}
	enc.WriteEnd()
	return append([]byte(nil), enc.Bytes()...)
}

// debugTraceJSONBeforeTextErrors formats spans for Studio persistence without reflection.
func debugTraceJSONBeforeTextErrors(snapshot DebugTraceSnapshot) string {
	buf := make([]byte, 0, len(snapshot.Spans)*128)
	buf = append(buf, '[')
	for i := range snapshot.Spans {
		if i > 0 {
			buf = append(buf, ',')
		}
		span := &snapshot.Spans[i]
		buf = append(buf, '{')
		buf = append(buf, `"id":`...)
		buf = strconv.AppendUint(buf, uint64(span.ID), 10)
		buf = append(buf, `,"parentId":`...)
		buf = strconv.AppendUint(buf, uint64(span.ParentID), 10)
		buf = appendDebugJSONStringBeforeTextErrors(buf, "name", span.Name, true)
		buf = appendDebugJSONStringBeforeTextErrors(buf, "category", span.Category, true)
		buf = appendDebugJSONStringBeforeTextErrors(buf, "service", span.Service, true)
		buf = appendDebugJSONStringBeforeTextErrors(buf, "operation", span.Operation, true)
		buf = appendDebugJSONStringBeforeTextErrors(buf, "field", span.Field, true)
		buf = appendDebugJSONStringBeforeTextErrors(buf, "fieldPath", span.FieldPath, true)
		buf = appendDebugJSONStringBeforeTextErrors(buf, "selection", span.Selection, true)
		buf = appendDebugJSONStringBeforeTextErrors(buf, "dependency", string(span.Dependency), true)
		if span.DatabaseBackend != "" {
			buf = appendDebugJSONStringBeforeTextErrors(buf, "databaseBackend", span.DatabaseBackend, true)
		}
		if span.DatabaseName != "" {
			buf = appendDebugJSONStringBeforeTextErrors(buf, "databaseName", span.DatabaseName, true)
		}
		if span.DatabaseOperation != "" {
			buf = appendDebugJSONStringBeforeTextErrors(buf, "databaseOperation", span.DatabaseOperation, true)
		}
		if span.DatabaseResource != "" {
			buf = appendDebugJSONStringBeforeTextErrors(buf, "databaseResource", span.DatabaseResource, true)
		}
		if span.Statement != "" {
			buf = appendDebugJSONStringBeforeTextErrors(buf, "statement", span.Statement, true)
		}
		if span.Fingerprint != "" {
			buf = appendDebugJSONStringBeforeTextErrors(buf, "fingerprint", span.Fingerprint, true)
		}
		if span.ArgumentCount != 0 {
			buf = append(buf, `,"argumentCount":`...)
			buf = strconv.AppendInt(buf, int64(span.ArgumentCount), 10)
		}
		if span.RowsKnown {
			buf = append(buf, `,"rowsAffected":`...)
			buf = strconv.AppendInt(buf, span.RowsAffected, 10)
		}
		if span.ErrorCode != "" {
			buf = appendDebugJSONStringBeforeTextErrors(buf, "errorCode", span.ErrorCode, true)
		}
		if span.StatementTruncated {
			buf = append(buf, `,"statementTruncated":true`...)
		}
		buf = append(buf, `,"start":`...)
		buf = strconv.AppendFloat(buf, float64(span.Start)/float64(time.Millisecond), 'f', -1, 64)
		buf = append(buf, `,"duration":`...)
		buf = strconv.AppendFloat(buf, float64(span.Duration)/float64(time.Millisecond), 'f', -1, 64)
		buf = append(buf, '}')
	}
	buf = append(buf, ']')
	return string(buf)
}

func appendDebugJSONStringBeforeTextErrors(buf []byte, name, value string, comma bool) []byte {
	if comma {
		buf = append(buf, ',')
	}
	buf = strconv.AppendQuote(buf, name)
	buf = append(buf, ':')
	return strconv.AppendQuote(buf, value)
}
