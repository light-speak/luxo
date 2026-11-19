package h2transport

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
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
		"h2": func(_ *http.Server, conn *tls.Conn, _ http.Handler) {
			_ = s.ServeConn(context.Background(), conn)
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
	reader := io.MultiReader(strings.NewReader("PRI * HTTP/2.0\r\n\r\n"), buffered.Reader)
	_ = s.ServeConn(r.Context(), &prefacedConn{Conn: conn, reader: reader})
}

func rejectUnnegotiatedTLS(w http.ResponseWriter) {
	w.Header().Set("Connection", "close")
	http.Error(w, "HTTP/2 over TLS requires h2 ALPN negotiation", http.StatusBadRequest)
}

type prefacedConn struct {
	net.Conn
	reader io.Reader
}

func (c *prefacedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
