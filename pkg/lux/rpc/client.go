package rpc

import (
	"fmt"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

// Client sends RPC requests to a remote Luxo service.
// Uses connection pooling for high throughput.
type Client struct {
	pool *Pool
}

// NewClient creates an RPC client for the given service address.
func NewClient(addr string) *Client {
	return &Client{pool: NewPool(addr, 32)}
}

// Call sends a binary-encoded request and returns the response body.
// apiID: from luxo.lock. params: pre-encoded binary (fieldID+value pairs + 0x00).
// Returns the response body (without status byte) and any error.
func (c *Client) Call(apiID int, params []byte) ([]byte, error) {
	return c.CallWithMask(apiID, nil, params)
}

// CallWithMask sends a request with field mask for field selection.
func (c *Client) CallWithMask(apiID int, fieldMask []byte, params []byte) ([]byte, error) {
	// Build request payload: [API ID varint] [mask len varint] [mask] [params]
	var payload []byte
	payload = codec.AppendVarint(payload, uint64(apiID))
	payload = codec.AppendVarint(payload, uint64(len(fieldMask)))
	if len(fieldMask) > 0 {
		payload = append(payload, fieldMask...)
	}
	if len(params) > 0 {
		payload = append(payload, params...)
	} else {
		payload = append(payload, 0x00) // empty params terminator
	}

	// Get pooled connection
	conn, err := c.pool.Get()
	if err != nil {
		return nil, fmt.Errorf("rpc: dial %s: %w", c.pool.addr, err)
	}

	// Send request frame
	if err := WriteFrame(conn, payload); err != nil {
		conn.Close()
		return nil, fmt.Errorf("rpc: write: %w", err)
	}

	// Read response frame
	resp, err := ReadFrame(conn, nil)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("rpc: read: %w", err)
	}

	// Return connection to pool
	c.pool.Put(conn)

	if len(resp) == 0 {
		return nil, fmt.Errorf("rpc: empty response")
	}

	status := resp[0]
	body := resp[1:]

	if status == statusError {
		return nil, decodeError(body)
	}

	return body, nil
}

// Close closes the connection pool.
func (c *Client) Close() {
	c.pool.Close()
}

// decodeError decodes a binary error response.
func decodeError(data []byte) error {
	dec := codec.NewDecoder(data)
	var code int64
	var name, message string
	for dec.NextField() {
		switch dec.FieldID() {
		case 1:
			code = dec.ReadInt()
		case 2:
			name = dec.ReadString()
		case 3:
			message = dec.ReadString()
		}
	}
	return fmt.Errorf("rpc error %d %s: %s", code, name, message)
}

// EncodeParams builds binary params from typed values.
// Helper for constructing RPC call params.
func EncodeParams(fields ...ParamField) []byte {
	var enc codec.Encoder
	for _, f := range fields {
		switch v := f.Value.(type) {
		case int64:
			enc.WriteFieldInt(f.FieldID, v)
		case float64:
			enc.WriteFieldFloat(f.FieldID, v)
		case string:
			enc.WriteFieldString(f.FieldID, v)
		case bool:
			enc.WriteFieldBool(f.FieldID, v)
		}
	}
	enc.WriteEnd()
	return enc.Bytes()
}

// ParamField represents a single parameter for EncodeParams.
type ParamField struct {
	FieldID int
	Value   any
}
