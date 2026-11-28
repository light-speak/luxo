package h2transport

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
)

// ConfigureHTTPServer installs HTTP/2 dispatch before the HTTP server starts.
// HTTP/1.1 (including WebSocket upgrades) retains net/http ownership.
// HTTP/2 prior knowledge and TLS use Luxo; obsolete h2c Upgrade is ignored.
// The owner must shut down both servers: net/http does not drain hijacked peers.
func (s *Server) ConfigureHTTPServer(server *http.Server) error {
	if server == nil || server.TLSNextProto != nil || server.Protocols != nil {
		return errors.New("HTTP/2 dispatch requires an unconfigured HTTP server")
	}
	config := server.TLSConfig
	if config == nil {
		config = &tls.Config{}
	} else {
		config = config.Clone()
	}
	if config.GetConfigForClient != nil || config.MaxVersion != 0 && config.MaxVersion < tls.VersionTLS12 {
		return errors.New("HTTP/2 dispatch requires a stable TLS configuration supporting TLS 1.2")
	}
	config.MinVersion = max(config.MinVersion, tls.VersionTLS12)
	config.NextProtos = []string{"h2", "http/1.1"}
	server.TLSConfig = config
	server.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){
		"h2": func(_ *http.Server, conn *tls.Conn, handler http.Handler) {
			ctx := context.Background()
			// net/http passes the listener and connection context through this handler.
			if h, ok := handler.(interface{ BaseContext() context.Context }); ok {
				ctx = h.BaseContext()
			}
			_ = s.ServeConn(ctx, conn)
		},
	}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PRI" || r.RequestURI != "*" || r.Proto != "HTTP/2.0" {
			s.handler.ServeHTTP(w, r)
			return
		}
		s.servePriorKnowledge(w, r)
	})
	return nil
}

func (s *Server) servePriorKnowledge(w http.ResponseWriter, r *http.Request) {
	if r.TLS != nil {
		rejectUnnegotiatedTLS(w)
		return
	}
	conn, buffered, err := http.NewResponseController(w).Hijack()
	if err != nil {
		http.Error(w, "HTTP/2 connection handoff unavailable", http.StatusInternalServerError)
		return
	}
	// net/http consumed only the request-shaped prefix. Replay it, then retain
	// every buffered byte (including the rest of the preface and initial frames).
	_ = s.ServeConn(r.Context(), newPrefacedConn(conn, buffered.Reader))
}

func rejectUnnegotiatedTLS(w http.ResponseWriter) {
	w.Header().Set("Connection", "close")
	http.Error(w, "HTTP/2 over TLS requires h2 ALPN negotiation", http.StatusBadRequest)
}

const consumedPriorKnowledgePrefix = "PRI * HTTP/2.0\r\n\r\n"

type prefacedConn struct {
	net.Conn
	reader *bufio.Reader
	offset int
}

func newPrefacedConn(conn net.Conn, reader *bufio.Reader) *prefacedConn {
	return &prefacedConn{Conn: conn, reader: reader}
}

func (c *prefacedConn) Read(p []byte) (int, error) {
	if c.offset < len(consumedPriorKnowledgePrefix) {
		n := copy(p, consumedPriorKnowledgePrefix[c.offset:])
		c.offset += n
		return n, nil
	}
	if c.reader == nil {
		return 0, io.EOF
	}
	n, err := c.reader.Read(p)
	// EOF is terminal for this handoff, including when it arrives with bytes.
	if err == io.EOF {
		c.reader = nil
	}
	return n, err
}
