package api

import (
	"bytes"
	"context"
	"fmt"

	luxerrors "github.com/light-speak/luxo/pkg/lux/errors"
)

// PreparedStreamHandler owns validated subscription inputs. It must not retain
// the borrowed Request or its buffers. WebSocket and RPC transports invoke it
// only after the ACK succeeds; direct OpenInternalStream calls schedule it immediately.
type PreparedStreamHandler func(context.Context, *Stream) error

// StreamPrepareFunc checks authorization and reads an owned typed snapshot before
// accepting a subscription. It must not start workers or acquire resources that
// require cleanup: transport failure or subscriber limits may discard its result.
type StreamPrepareFunc func(context.Context, *Request, any) (PreparedStreamHandler, error)

// PreparedStreamSource owns a ready remote subscription. Run starts forwarding
// only after ACK. Close must release resources without requiring Run to start;
// the router invokes it once, outside hub locks, including failed admission.
type PreparedStreamSource struct {
	Run   PreparedStreamHandler
	Close func()
}

// StreamForwardPrepareFunc negotiates with the remote service before accepting
// the local subscription. It must honor ctx and not retain Request buffers.
// If it returns an error with Close set, the router still performs that cleanup.
type StreamForwardPrepareFunc func(context.Context, *Request, any) (PreparedStreamSource, error)

type streamHandlerRoute struct {
	handler        StreamHandlerFunc
	prepare        StreamPrepareFunc
	eventPrepare   func(context.Context, *Request, any) (func(*StreamSub), error)
	forwardPrepare StreamForwardPrepareFunc
}

type preparedStream struct {
	run     PreparedStreamHandler
	bind    func(*StreamSub)
	release func()
}

func (p preparedStream) valid() bool { return p.run != nil || p.bind != nil }

func (r streamHandlerRoute) preparesInputs() bool { return r.prepare != nil || r.eventPrepare != nil }

// HandleStreamPrepared registers a synchronous preparation boundary for a native
// stream. Errors reject the subscription, not an already acknowledged stream.
// Local JSON inputs stay JSON; only forwarding routes need Binary encoding.
func (rt *Router) HandleStreamPrepared(apiName string, prepare StreamPrepareFunc) {
	rt.streamHandlers[apiName] = streamHandlerRoute{prepare: prepare}
}

// HandleStreamForwarded registers an RPC preparation boundary. JSON requests
// are encoded once for forwarding; unlike local streams they are not read into
// a second generic parameter map or copied into a legacy StreamParams body.
func (rt *Router) HandleStreamForwarded(apiName string, prepare StreamForwardPrepareFunc) {
	rt.streamHandlers[apiName] = streamHandlerRoute{forwardPrepare: prepare}
}

func (rt *Router) prepareStreamHandler(ctx context.Context, req *Request, identity any) (prepared preparedStream, err error) {
	defer func() {
		if recover() != nil {
			// Do not format arbitrary panic values or expose them to clients.
			prepared, err = preparedStream{}, luxerrors.InternalError
		}
	}()
	route, registered := rt.streamHandlers[req.API]
	if route.forwardPrepare != nil {
		return prepareForwardedStream(ctx, req, identity, route.forwardPrepare)
	}
	if route.eventPrepare != nil {
		bind, err := route.eventPrepare(ctx, req, identity)
		return preparedStream{bind: bind}, err
	}
	prepare := route.prepare
	if prepare == nil {
		if registered && route.handler == nil {
			return preparedStream{}, fmt.Errorf("api: stream %q has no registered implementation", req.API)
		}
		return preparedStream{}, nil
	}
	handler, err := prepare(ctx, req, identity)
	if err != nil {
		return preparedStream{}, err
	}
	if handler == nil {
		return preparedStream{}, fmt.Errorf("api: stream %q preparation returned no handler", req.API)
	}
	return preparedStream{run: handler}, nil
}

func prepareForwardedStream(ctx context.Context, req *Request, identity any, prepare StreamForwardPrepareFunc) (preparedStream, error) {
	if err := ctx.Err(); err != nil {
		return preparedStream{}, err
	}
	source, err := prepare(ctx, req, identity)
	if err == nil && (source.Run == nil || source.Close == nil) {
		err = fmt.Errorf("api: stream %q preparation returned an incomplete source", req.API)
	}
	if err != nil {
		if source.Close != nil {
			source.Close()
		}
		return preparedStream{}, err
	}
	return preparedStream{run: source.Run, release: source.Close}, nil
}

func preparedStreamFieldMask(req *Request, prepared preparedStream) []byte {
	if prepared.valid() {
		return bytes.Clone(req.FieldMask)
	}
	return req.FieldMask
}

func (rt *Router) streamInputValues(req *Request) map[string]any {
	if route := rt.streamHandlers[req.API]; route.preparesInputs() || route.forwardPrepare != nil {
		return nil
	}
	return binaryStreamParams(req)
}
