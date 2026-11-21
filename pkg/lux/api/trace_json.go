package api

import (
	"encoding/json/jsontext"
	"strconv"
	"time"
)

const failedDebugTraceJSON = `{"encodingFailed":true,"spans":[]}`

// DebugTraceJSON formats spans for Studio persistence without reflection.
// Only diagnostic failures use an envelope; successful traces remain arrays.
func DebugTraceJSON(snapshot DebugTraceSnapshot) (string, error) {
	buf := make([]byte, 0, len(snapshot.Spans)*128)
	if snapshot.EncodingFailed {
		buf = append(buf, `{"encodingFailed":true,"spans":`...)
	}
	buf = append(buf, '[')
	for i := range snapshot.Spans {
		if i > 0 {
			buf = append(buf, ',')
		}
		var err error
		buf, err = appendDebugSpanJSON(buf, &snapshot.Spans[i])
		if err != nil {
			return "", err
		}
	}
	buf = append(buf, ']')
	if snapshot.EncodingFailed {
		buf = append(buf, '}')
	}
	return string(buf), nil
}

func appendDebugSpanJSON(buf []byte, span *DebugSpan) ([]byte, error) {
	if !validDebugSpan(span) {
		return nil, errInvalidDebugTrace
	}
	buf = append(buf, `{"id":`...)
	buf = strconv.AppendUint(buf, uint64(span.ID), 10)
	buf = append(buf, `,"parentId":`...)
	buf = strconv.AppendUint(buf, uint64(span.ParentID), 10)
	for _, field := range debugTextFields(span) {
		if field.id >= 13 && field.value == "" {
			continue
		}
		buf = append(buf, ',', '"')
		buf = append(buf, field.name...)
		buf = append(buf, '"', ':')
		var err error
		buf, err = jsontext.AppendQuote(buf, field.value)
		if err != nil {
			return nil, err
		}
	}
	return appendDebugSpanNumbersJSON(buf, span), nil
}

func appendDebugSpanNumbersJSON(buf []byte, span *DebugSpan) []byte {
	if span.ArgumentCount != 0 {
		buf = append(buf, `,"argumentCount":`...)
		buf = strconv.AppendInt(buf, int64(span.ArgumentCount), 10)
	}
	if span.RowsKnown {
		buf = append(buf, `,"rowsAffected":`...)
		buf = strconv.AppendInt(buf, span.RowsAffected, 10)
	}
	if span.StatementTruncated {
		buf = append(buf, `,"statementTruncated":true`...)
	}
	buf = append(buf, `,"start":`...)
	buf = strconv.AppendFloat(buf, float64(span.Start)/float64(time.Millisecond), 'f', -1, 64)
	buf = append(buf, `,"duration":`...)
	buf = strconv.AppendFloat(buf, float64(span.Duration)/float64(time.Millisecond), 'f', -1, 64)
	return append(buf, '}')
}
