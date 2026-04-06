package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// Transport handles JSON-RPC 2.0 over stdin/stdout with LSP Content-Length headers.
type Transport struct {
	reader *bufio.Reader
	writer io.Writer
	mu     sync.Mutex // protects writer
}

// NewTransport creates a new LSP transport.
func NewTransport(reader io.Reader, writer io.Writer) *Transport {
	return &Transport{
		reader: bufio.NewReaderSize(reader, 64*1024),
		writer: writer,
	}
}

// ReadMessage reads one JSON-RPC message from stdin.
// LSP format: Content-Length: N\r\n\r\n{json}
func (t *Transport) ReadMessage() (*Request, error) {
	// read headers
	contentLength := 0
	for {
		line, err := t.reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("reading header: %w", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break // empty line = end of headers
		}
		if strings.HasPrefix(line, "Content-Length:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:"))
			contentLength, err = strconv.Atoi(val)
			if err != nil {
				return nil, fmt.Errorf("invalid Content-Length: %s", val)
			}
		}
	}

	if contentLength == 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}

	// guard against excessive memory allocation from malformed Content-Length
	const maxContentLength = 50 * 1024 * 1024 // 50 MB
	if contentLength > maxContentLength {
		return nil, fmt.Errorf("Content-Length %d exceeds maximum %d", contentLength, maxContentLength)
	}

	// read body
	body := make([]byte, contentLength)
	_, err := io.ReadFull(t.reader, body)
	if err != nil {
		return nil, fmt.Errorf("reading body: %w", err)
	}

	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("parsing JSON-RPC: %w", err)
	}

	return &req, nil
}

// SendResponse sends a JSON-RPC response.
func (t *Transport) SendResponse(id *json.RawMessage, result any) error {
	resp := Response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	return t.send(resp)
}

// SendError sends a JSON-RPC error response.
func (t *Transport) SendError(id *json.RawMessage, code int, message string) error {
	resp := Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RespError{Code: code, Message: message},
	}
	return t.send(resp)
}

// SendNotification sends a JSON-RPC notification (no ID).
func (t *Transport) SendNotification(method string, params any) error {
	notif := Notification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	return t.send(notif)
}

func (t *Transport) send(msg any) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := io.WriteString(t.writer, header); err != nil {
		return err
	}
	if _, err := t.writer.Write(body); err != nil {
		return err
	}
	return nil
}
