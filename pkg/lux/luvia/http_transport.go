package luvia

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/light-speak/luxo/pkg/lux/api"
	"net"
	"net/http"

	"github.com/light-speak/luxo/pkg/lux/h2transport"
)

type gatewayHTTPRuntime interface {
	Shutdown(context.Context) error
	RegisterOnShutdown(func())
}

type gatewayHTTPServer struct {
	*http.Server
	h2 *h2transport.Server
}

func (s *gatewayHTTPServer) Close() error {
	if s.h2 == nil {
		return s.Server.Close()
	}
	return errors.Join(s.h2.Close(), s.Server.Close())
}

func (s *gatewayHTTPServer) Shutdown(ctx context.Context) error {
	if s.h2 == nil {
		return s.Server.Shutdown(ctx)
	}
	// Hijacked cleartext and ALPN peers are not drained by net/http itself.
	done := make(chan error, 1)
	go func() { done <- s.h2.Shutdown(ctx) }()
	return errors.Join(s.Server.Shutdown(ctx), <-done)
}

func newGatewayHTTPTransport(handler http.Handler, timeouts serverTimeouts, body api.BodyLimits) (*h2transport.Server, error) {
	switch mode := envOr("APP_HTTP_TRANSPORT", "luxo"); mode {
	case "standard":
		return nil, nil
	case "luxo":
		limits := h2transport.DefaultLimits()
		limits.MaxRequestBodyBytes, limits.MaxResponseBodyBytes = body.RequestBytes, body.ResponseBytes
		limits.BodyTimeout, limits.ResponseTimeout = timeouts.read, timeouts.write
		limits.IdleTimeout, limits.WriteTimeout = timeouts.idle, timeouts.write
		return h2transport.NewServerWithLimits(handler, limits)
	default:
		return nil, fmt.Errorf("APP_HTTP_TRANSPORT must be standard or luxo, got %q", mode)
	}
}

// startConfiguredGatewayServer takes ownership of listener, including on setup failure.
func startConfiguredGatewayServer(listener net.Listener, server *http.Server, h2 *h2transport.Server, useTLS bool) (*gatewayHTTPServer, <-chan error, error) {
	if h2 != nil {
		if err := h2.ConfigureHTTPServer(server); err != nil {
			// Configuration failed before serving; release the owned listening socket.
			_ = listener.Close()
			return nil, nil, err
		}
	}
	if useTLS {
		listener = tls.NewListener(listener, server.TLSConfig)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	return &gatewayHTTPServer{Server: server, h2: h2}, serveErr, nil
}
