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

// EventMatcher skips on false and terminates only this subscription on error.
type EventMatcher[P any] func(params *P, identity any) (bool, error)

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
func (t *EventStream[P]) Dispatch(match EventMatcher[P], encode func([]byte, bool) ([]byte, error)) error {
	t.hub.mu.RLock()
	var encodings streamEncodings
	var slow []*StreamSub
	var failed []streamMatchFailure
	defer t.hub.finishEventDispatch(t.apiName, &slow, &failed)
	for _, entry := range t.subs {
		sub := entry.sub
		if match != nil {
			allowed, err := match(&entry.params, sub.Identity)
			if err != nil {
				failed = append(failed, streamMatchFailure{sub, err})
				continue
			}
			if !allowed {
				continue
			}
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

// DispatchIdentity encodes identity-dependent output separately for each
// subscriber. Identity values are never compared, hashed, or shared as cache keys.
// Matchers and encoders have the same lifecycle restrictions as Dispatch.
func (t *EventStream[P]) DispatchIdentity(match EventMatcher[P], encode func([]byte, bool, any) ([]byte, error)) error {
	return t.dispatchIdentity(match, encode, nil)
}

// DispatchIdentitySelection shares only projections proven identity-independent
// by generated field checks. Shared projections are encoded without an identity.
// A nil predicate retains per-subscriber encoding for every projection.
func (t *EventStream[P]) DispatchIdentitySelection(match EventMatcher[P], encode func([]byte, bool, any) ([]byte, error), independent func([]byte) bool) error {
	return t.dispatchIdentity(match, encode, independent)
}

func (t *EventStream[P]) dispatchIdentity(match EventMatcher[P], encode func([]byte, bool, any) ([]byte, error), independent func([]byte) bool) error {
	t.hub.mu.RLock()
	var slow []*StreamSub
	var failed []streamMatchFailure
	var encodings streamEncodings
	shared := func(mask []byte, binary bool) ([]byte, error) { return encode(mask, binary, nil) }
	defer t.hub.finishEventDispatch(t.apiName, &slow, &failed)
	for _, entry := range t.subs {
		sub := entry.sub
		if match != nil {
			allowed, err := match(&entry.params, sub.Identity)
			if err != nil {
				failed = append(failed, streamMatchFailure{sub, err})
				continue
			}
			if !allowed {
				continue
			}
		}
		var data []byte
		var err error
		if independent != nil && independent(sub.FieldMask) {
			data, err = cachedStreamEncoding(&encodings, sub.FieldMask, sub.Binary, shared)
		} else {
			data, err = encode(sub.FieldMask, sub.Binary, sub.Identity)
		}
		if err != nil {
			return err
		}
		if !enqueueStreamData(sub, data) {
			slow = append(slow, sub)
		}
	}
	return nil
}

type streamMatchFailure struct {
	sub *StreamSub
	err error
}

// Publish a condition failure only after releasing the dispatch read lock.
// Finishing one subscriber must not cancel other consumers or event delivery.
func (h *StreamHub) finishEventDispatch(apiName string, slow *[]*StreamSub, failed *[]streamMatchFailure) {
	h.mu.RUnlock()
	for _, failure := range *failed {
		h.finish(apiName, failure.sub, failure.err)
	}
	h.removeSlowSubscribers(apiName, *slow)
}
