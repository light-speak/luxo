package api

import (
	"context"
	"errors"
	"time"

	"nhooyr.io/websocket"
)

// WebSocketTokenExpired identifies a connection whose verified access token expired.
const WebSocketTokenExpired websocket.StatusCode = 4001

type webSocketTokenExpiryKey struct{}

var errWebSocketTokenExpired = errors.New("access token expired")

// A sibling context can lag while parent cancellation visits other children.
// Check the authenticated parent before starting a frame, without cancelling I/O.
func (c *wsConn) operationError() error {
	if c.operationCtx != nil {
		return c.operationCtx.Err()
	}
	return nil
}

// WithWebSocketTokenExpiry binds a verified access-token deadline to an upgrade.
// Authentication middleware must supply the signed deadline, never client input.
func WithWebSocketTokenExpiry(ctx context.Context, expiry time.Time) context.Context {
	return context.WithValue(ctx, webSocketTokenExpiryKey{}, expiry)
}

// WithoutWebSocketTokenExpiry clears a deadline inherited from a previous
// authentication boundary while preserving cancellation and other context values.
func WithoutWebSocketTokenExpiry(ctx context.Context) context.Context {
	return context.WithValue(ctx, webSocketTokenExpiryKey{}, false)
}

// Keep transport I/O alive long enough to send the close frame after cancelling
// business work. Expiration does not re-verify tokens or create per-message timers.
func webSocketAuthContext(parent context.Context, conn *websocket.Conn) (context.Context, func()) {
	expiry, ok := parent.Value(webSocketTokenExpiryKey{}).(time.Time)
	if !ok {
		return parent, func() {}
	}
	ctx, cancel := context.WithCancelCause(parent)
	remaining := time.Until(expiry)
	if remaining <= 0 {
		cancel(errWebSocketTokenExpired)
	}
	timer := time.AfterFunc(remaining, func() {
		cancel(errWebSocketTokenExpired)
		_ = conn.Close(WebSocketTokenExpired, errWebSocketTokenExpired.Error())
	})
	return ctx, func() {
		timer.Stop()
		if context.Cause(ctx) == errWebSocketTokenExpired {
			_ = conn.Close(WebSocketTokenExpired, errWebSocketTokenExpired.Error())
		}
		cancel(context.Canceled)
	}
}
