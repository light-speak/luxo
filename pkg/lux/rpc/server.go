package rpc

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"

	stderrors "errors"
	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/codec"

	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

// Server handles internal RPC connections using Luxo binary protocol.
// Dispatches requests to the same handlers as the HTTP router.
type Server struct {
	handlers map[string]api.HandlerFunc
	registry *api.APIRegistry
	listener net.Listener
	wg       sync.WaitGroup
	done     chan struct{}
}

// NewServer creates an RPC server that shares handlers with the HTTP router.
func NewServer(router *api.Router) *Server {
	return &Server{
		handlers: router.ExportHandlers(),
		registry: router.Registry,
		done:     make(chan struct{}),
	}
}

// ListenAndServe starts the RPC server on the given TCP address.
func (s *Server) ListenAndServe(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.listener = ln
	fmt.Printf("  Luxo RPC on %s\n", addr)

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.done:
				return nil
			default:
			}
			continue
		}
		if tc, ok := conn.(*net.TCPConn); ok {
			tc.SetNoDelay(true)
		}
		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

// Close stops accepting new connections and waits for active ones to finish.
func (s *Server) Close() {
	close(s.done)
	if s.listener != nil {
		s.listener.Close()
	}
	s.wg.Wait()
}

func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	var readBuf []byte
	for {
		// Read request frame
		payload, err := ReadFrame(conn, readBuf)
		if err != nil {
			if err != io.EOF {
				// Connection error — close
			}
			return
		}
		readBuf = payload[:0] // reuse buffer

		// Parse and dispatch request, write response directly to conn
		if err := s.processRequest(conn, payload); err != nil {
			return
		}
	}
}

func (s *Server) processRequest(conn io.Writer, payload []byte) error {
	// Decode: [API ID varint] [field mask len varint] [field mask] [params binary]
	off := 0
	apiID, n := codec.ReadVarint(payload, off)
	if n <= 0 {
		return WriteFrame(conn, encodeError(400, "BadRequest", "invalid API ID"))
	}
	off += n

	apiName, ok := s.registry.NameByID(int(apiID))
	if !ok {
		return WriteFrame(conn, encodeError(404, "NotFound", fmt.Sprintf("unknown API ID: %d", apiID)))
	}

	// Field mask
	maskLen, n := codec.ReadVarint(payload, off)
	if n <= 0 {
		return WriteFrame(conn, encodeError(400, "BadRequest", "invalid field mask"))
	}
	off += n
	var fieldMask []byte
	if maskLen > 0 {
		if off+int(maskLen) > len(payload) {
			return WriteFrame(conn, encodeError(400, "BadRequest", "truncated field mask"))
		}
		fieldMask = payload[off : off+int(maskLen)]
		off += int(maskLen)
	}

	// Build Request with paramSlots
	paramMeta := s.registry.ParamOrder(apiName)
	paramNames := s.registry.ParamNames(apiName)
	req := &api.Request{
		API:        apiName,
		BinaryMode: true,
		FieldMask:  fieldMask,
		Page:       1,
		PageSize:   20,
	}
	req.SetBinaryParams(paramNames, len(paramMeta))

	// Decode params inline
	paramBuf := payload[off:]
	poff := 0
	for poff < len(paramBuf) {
		fid, n := codec.ReadVarint(paramBuf, poff)
		if n <= 0 {
			return WriteFrame(conn, encodeError(400, "BadRequest", "truncated param field ID"))
		}
		if fid == 0 {
			break // end-of-params marker
		}
		poff += n

		paramIdx := -1
		var meta *api.ParamMeta
		for i := range paramMeta {
			if paramMeta[i].FieldID == int(fid) {
				paramIdx = i
				meta = &paramMeta[i]
				break
			}
		}
		if meta == nil {
			return WriteFrame(conn, encodeError(400, "BadRequest", fmt.Sprintf("unknown param field ID %d", fid)))
		}
		if paramIdx >= 16 {
			return WriteFrame(conn, encodeError(400, "BadRequest", "too many params (max 16)"))
		}

		switch meta.Type {
		case "Int":
			v, n := codec.ReadSvarint(paramBuf, poff)
			if n <= 0 {
				return WriteFrame(conn, encodeError(400, "BadRequest", "truncated param: "+meta.Name))
			}
			poff += n
			req.SetParamSlot(paramIdx, v)
		case "Float":
			v, n := codec.ReadFixed64(paramBuf, poff)
			if n == 0 {
				return WriteFrame(conn, encodeError(400, "BadRequest", "truncated param: "+meta.Name))
			}
			poff += n
			req.SetParamSlot(paramIdx, v)
		case "String":
			v, n := codec.ReadString(paramBuf, poff)
			if n == 0 {
				return WriteFrame(conn, encodeError(400, "BadRequest", "truncated param: "+meta.Name))
			}
			poff += n
			req.SetParamSlot(paramIdx, v)
		case "Boolean":
			v, n := codec.ReadBool(paramBuf, poff)
			if n == 0 {
				return WriteFrame(conn, encodeError(400, "BadRequest", "truncated param: "+meta.Name))
			}
			poff += n
			req.SetParamSlot(paramIdx, v)
		}
	}

	// Dispatch to handler
	fn, ok := s.handlers[apiName]
	if !ok {
		return WriteFrame(conn, encodeError(404, "NotFound", "handler not found: "+apiName))
	}

	buf := api.GetBuf()
	// Reserve 1 byte for status prefix — handler appends after it
	buf.B = append(buf.B, statusOK)
	req.Buf = buf

	herr := s.callHandler(fn, req)
	if herr != nil {
		api.PutBuf(buf)
		var appErr *luxerrors.AppError
		if stderrors.As(herr, &appErr) {
			return WriteFrame(conn, encodeError(appErr.Code, appErr.Name, appErr.Message))
		}
		return WriteFrame(conn, encodeError(500, "Internal", herr.Error()))
	}

	// Zero-copy: write [frame header][statusOK + handler payload] directly from pooled buf
	err := WriteFrame(conn, buf.B)
	api.PutBuf(buf)
	return err
}

// callHandler calls the handler with panic recovery so a panicking handler
// does not crash the connection goroutine.
func (s *Server) callHandler(fn api.HandlerFunc, req *api.Request) (herr error) {
	defer func() {
		if p := recover(); p != nil {
			herr = fmt.Errorf("panic: %v", p)
		}
	}()
	return fn(context.Background(), req)
}

func encodeError(code int, name, message string) []byte {
	var enc codec.Encoder
	enc.WriteFieldInt(1, int64(code))
	enc.WriteFieldString(2, name)
	enc.WriteFieldString(3, message)
	enc.WriteEnd()

	resp := make([]byte, 0, 1+len(enc.Bytes()))
	resp = append(resp, statusError)
	resp = append(resp, enc.Bytes()...)
	return resp
}
