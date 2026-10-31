package api

import "context"

// EventStream holds typed subscription snapshots. The hub lock protects both
// collections, so cancellation cannot leave snapshots behind or close a queue
// during dispatch. One event listener serves all subscribers of this route.
type EventStream[P any] struct {
	hub     *StreamHub
	apiName string
	subs    []*eventStreamSub[P]
}

type eventStreamSub[P any] struct {
	sub    *StreamSub
	params P
	index  int
}

// RegisterEventStream validates and owns parameters before a transport ACK.
// prepare must not retain Request buffers or acquire resources needing cleanup.
// Register routes at startup; matchers must treat their parameters as read-only.
func RegisterEventStream[P any](router *Router, apiName string, prepare func(context.Context, *Request, any) (P, error)) *EventStream[P] {
	topic := &EventStream[P]{hub: router.Streams, apiName: apiName}
	route := streamHandlerRoute{}
	if prepare != nil {
		route.eventPrepare = func(ctx context.Context, req *Request, identity any) (func(*StreamSub), error) {
			params, err := prepare(ctx, req, identity)
			if err != nil {
				return nil, err
			}
			return func(sub *StreamSub) { topic.attach(sub, params) }, nil
		}
	}
	router.streamHandlers[apiName] = route
	return topic
}

// attach and detach run under hub.mu, including capacity rejection and ACK
// failure cleanup. Swap removal releases the snapshot without a per-event map.
func (t *EventStream[P]) attach(sub *StreamSub, params P) {
	entry := &eventStreamSub[P]{sub: sub, params: params, index: len(t.subs)}
	t.subs = append(t.subs, entry)
	sub.onClose = func() {
		last := len(t.subs) - 1
		t.subs[entry.index] = t.subs[last]
		t.subs[entry.index].index = entry.index
		t.subs[last] = nil
		t.subs = t.subs[:last]
		var empty P
		entry.params, entry.sub = empty, nil
	}
}

// Dispatch matches typed snapshots against an already decoded event. Encodings
// are shared by mode and projection, just as in the untyped hub dispatcher.
// Matchers and encoders must not register or cancel subscriptions while called.
func (t *EventStream[P]) Dispatch(match func(*P, any) bool, encode func([]byte, bool) ([]byte, error)) error {
	t.hub.mu.RLock()
	var encodings streamEncodings
	var slow []*StreamSub
	defer t.hub.finishDispatch(t.apiName, &slow)
	for _, entry := range t.subs {
		sub := entry.sub
		if match != nil && !match(&entry.params, sub.Identity) {
			continue
		}
		data, err := cachedStreamEncoding(&encodings, sub.FieldMask, sub.Binary, encode)
		if err != nil {
			return err
		}
		if !enqueueStreamData(sub, data) {
			slow = append(slow, sub)
		}
	}
	return nil
}
