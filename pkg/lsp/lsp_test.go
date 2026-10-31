package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

// ========== Transport Tests ==========

func BenchmarkTransportReadMessage(b *testing.B) {
	for _, sample := range []struct{ name, body string }{
		{"initialize", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`},
		{"unicode-document", `{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"text":"` + strings.Repeat("字段选择", 1024) + `"}}`},
	} {
		b.Run(sample.name, func(b *testing.B) {
			input := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(sample.body), sample.body)
			reader := strings.NewReader(input)
			transport := NewTransport(reader, io.Discard)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				reader.Reset(input)
				transport.reader.Reset(reader)
				message, err := transport.ReadMessage()
				if err != nil || message.Method == "" {
					b.Fatalf("invalid read: %v / %v", message, err)
				}
			}
		})
	}
}

func FuzzTransportContentLength(f *testing.F) {
	for _, value := range []int64{-9223372036854775808, -1, 0, 1, 2, 4096, 52428801, 9223372036854775807} {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, length int64) {
		// Exercise both rejection boundaries without repeated large valid allocations.
		if length > 4096 && length <= 50*1024*1024 {
			t.Skip()
		}
		input := fmt.Sprintf("Content-Length: %d\r\n\r\n{}", length)
		message, err := NewTransport(strings.NewReader(input), io.Discard).ReadMessage()
		if length == 2 {
			if err != nil || message == nil {
				t.Fatalf("valid empty object: %v / %v", message, err)
			}
		} else if err == nil || message != nil {
			t.Fatalf("invalid length %d: %v / %v", length, message, err)
		}
	})
}

func TestTransportRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)

	id := json.RawMessage(`1`)
	err := transport.SendResponse(&id, map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("SendResponse error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Content-Length:") {
		t.Error("missing Content-Length header")
	}
	if !strings.Contains(output, `"key":"value"`) {
		t.Error("missing response body")
	}
}

func TestTransportReadMessage(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	input := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)

	transport := NewTransport(strings.NewReader(input), io.Discard)
	req, err := transport.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage error: %v", err)
	}
	if req.Method != "initialize" {
		t.Errorf("expected method 'initialize', got %q", req.Method)
	}
}

func TestTransportReadMessageInvalidHeader(t *testing.T) {
	input := "Invalid-Header: 123\r\n\r\n{}"
	transport := NewTransport(strings.NewReader(input), io.Discard)
	_, err := transport.ReadMessage()
	if err == nil {
		t.Error("expected error for missing Content-Length")
	}
}

func TestTransportSendNotification(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)

	err := transport.SendNotification("textDocument/publishDiagnostics", map[string]any{
		"uri":         "file:///test.luxo",
		"diagnostics": []any{},
	})
	if err != nil {
		t.Fatalf("SendNotification error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "publishDiagnostics") {
		t.Error("notification method not found in output")
	}
}

func TestTransportSendError(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)

	id := json.RawMessage(`1`)
	err := transport.SendError(&id, -32601, "method not found")
	if err != nil {
		t.Fatalf("SendError error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "method not found") {
		t.Error("error message not found in output")
	}
}

func TestTransportReadMessageEOFMidBody(t *testing.T) {
	// Content-Length says 100 but only 5 bytes available
	input := "Content-Length: 100\r\n\r\nhello"
	transport := NewTransport(strings.NewReader(input), io.Discard)
	_, err := transport.ReadMessage()
	if err == nil {
		t.Error("expected error for EOF mid-body")
	}
}

func TestTransportSendWriteError(t *testing.T) {
	w := &errWriter{}
	transport := NewTransport(strings.NewReader(""), w)
	id := json.RawMessage(`1`)
	err := transport.SendResponse(&id, "test")
	if err == nil {
		t.Error("expected error from writer")
	}
}

// errWriter always returns an error on Write
type errWriter struct{}

func (e *errWriter) Write(p []byte) (int, error) {
	return 0, fmt.Errorf("write error")
}

func TestTransportReadMessageInvalidContentLength(t *testing.T) {
	input := "Content-Length: notanumber\r\n\r\n{}"
	transport := NewTransport(strings.NewReader(input), io.Discard)
	_, err := transport.ReadMessage()
	if err == nil {
		t.Error("expected error for non-numeric Content-Length")
	}
	if !strings.Contains(err.Error(), "invalid Content-Length") {
		t.Errorf("expected 'invalid Content-Length' in error, got: %v", err)
	}
}

func TestTransportSendMarshalError(t *testing.T) {
	var buf bytes.Buffer
	transport := NewTransport(strings.NewReader(""), &buf)
	// Channels cannot be marshaled to JSON
	err := transport.send(make(chan int))
	if err == nil {
		t.Error("expected error when marshaling a channel")
	}
}

func TestReadMessageRejectsNegativeContentLength(t *testing.T) {
	for _, value := range []string{"-1", "-52428800", "-9223372036854775808"} {
		t.Run(value, func(t *testing.T) {
			transport := NewTransport(strings.NewReader("Content-Length: "+value+"\r\n\r\n"), io.Discard)
			message, err := transport.ReadMessage()
			if message != nil || err == nil || !strings.Contains(err.Error(), "invalid Content-Length") {
				t.Fatalf("message=%v error=%v", message, err)
			}
		})
	}
}

func TestReadMessageRejectsOversizedContentLength(t *testing.T) {
	transport := NewTransport(strings.NewReader("Content-Length: 52428801\r\n\r\n"), io.Discard)
	message, err := transport.ReadMessage()
	if message != nil || err == nil || !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("message=%v error=%v", message, err)
	}
}

// TestReadMessageZeroContentLength covers the Content-Length: 0 path.
func TestReadMessageZeroContentLength(t *testing.T) {
	input := "Content-Length: 0\r\n\r\n"
	transport := NewTransport(strings.NewReader(input), io.Discard)
	_, err := transport.ReadMessage()
	if err == nil {
		t.Error("expected error for Content-Length 0")
	}
	if !strings.Contains(err.Error(), "missing Content-Length") {
		t.Errorf("expected 'missing Content-Length' error, got: %v", err)
	}
}

// TestReadMessageHeaderReadError covers the header read error path.
func TestReadMessageHeaderReadError(t *testing.T) {
	// A reader that returns an error immediately
	r := &errReader{err: fmt.Errorf("connection reset")}
	transport := NewTransport(r, io.Discard)
	_, err := transport.ReadMessage()
	if err == nil {
		t.Error("expected error from ReadMessage")
	}
	if !strings.Contains(err.Error(), "reading header") {
		t.Errorf("expected 'reading header' in error, got: %v", err)
	}
}

type errReader struct {
	err error
}

func (e *errReader) Read(p []byte) (int, error) {
	return 0, e.err
}

// TestReadMessageInvalidJSON covers JSON unmarshal error after valid Content-Length.
func TestReadMessageInvalidJSON(t *testing.T) {
	body := "not valid json!"
	input := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
	transport := NewTransport(strings.NewReader(input), io.Discard)
	_, err := transport.ReadMessage()
	if err == nil {
		t.Error("expected error for invalid JSON body")
	}
	if !strings.Contains(err.Error(), "parsing JSON-RPC") {
		t.Errorf("expected 'parsing JSON-RPC' in error, got: %v", err)
	}
}

// TestTransportSendHeaderWriteError covers the header write error path in send().
func TestTransportSendHeaderWriteError(t *testing.T) {
	w := &errHeaderWriter{}
	transport := NewTransport(strings.NewReader(""), w)
	id := json.RawMessage(`1`)
	err := transport.SendResponse(&id, "test")
	if err == nil {
		t.Error("expected error from header write")
	}
	if !strings.Contains(err.Error(), "header write error") {
		t.Errorf("expected 'header write error', got: %v", err)
	}
}

// errHeaderWriter fails on the first Write (header write), succeeds on nothing.
type errHeaderWriter struct {
	calls int
}

func (e *errHeaderWriter) Write(p []byte) (int, error) {
	e.calls++
	if e.calls == 1 {
		return 0, fmt.Errorf("header write error")
	}
	return len(p), nil
}

// TestTransportSendBodyWriteError covers the body write error path in send().
func TestTransportSendBodyWriteError(t *testing.T) {
	// Writer that succeeds on header (first call via io.WriteString) but fails on body write
	w := &bodyErrWriter{}
	transport := NewTransport(strings.NewReader(""), w)
	id := json.RawMessage(`1`)
	err := transport.SendResponse(&id, "test")
	if err == nil {
		t.Error("expected error from body write")
	}
	if !strings.Contains(err.Error(), "body write error") {
		t.Errorf("expected 'body write error', got: %v", err)
	}
}

type bodyErrWriter struct {
	calls int
}

func (e *bodyErrWriter) Write(p []byte) (int, error) {
	e.calls++
	if e.calls == 1 {
		// Header write succeeds
		return len(p), nil
	}
	// Body write fails
	return 0, fmt.Errorf("body write error")
}
