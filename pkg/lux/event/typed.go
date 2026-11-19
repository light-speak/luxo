package event

import (
	"context"
	"errors"
)

// ErrUnexpectedPayload rejects a value that is neither the registered event
// type nor wire bytes. It never includes potentially sensitive payload data.
var ErrUnexpectedPayload = errors.New("event: payload does not match registered event type")

// ErrDecoderRequired rejects subscriptions without a wire-format decoder.
var ErrDecoderRequired = errors.New("event: decoder is required")

// OnDecode registers a typed broadcast handler with automatic wire-format decoding.
// ChanBus delivers an independently owned struct; NATSBus delivers []byte
// which is decoded via unmarshal. This eliminates any from handler signatures.
func OnDecode[T any](bus Bus, name string, unmarshal func([]byte, any) error, handler func(ctx context.Context, payload T) error) error {
	return subscribeDecoded(func(callback Handler) error { return bus.On(name, callback) }, unmarshal, handler)
}

// OnQueueDecode registers a typed queue handler with automatic wire-format decoding.
// Only one instance per group receives each event (competing consumers).
func OnQueueDecode[T any](bus Bus, name string, group string, unmarshal func([]byte, any) error, handler func(ctx context.Context, payload T) error) error {
	return subscribeDecoded(func(callback Handler) error { return bus.OnQueue(name, group, callback) }, unmarshal, handler)
}

// Validate callbacks before invoking the subscription, including a nil backend.
func subscribeDecoded[T any](subscribe func(Handler) error, unmarshal func([]byte, any) error, handler func(context.Context, T) error) error {
	if handler == nil {
		return ErrHandlerRequired
	}
	if unmarshal == nil {
		return ErrDecoderRequired
	}
	return subscribe(func(ctx context.Context, raw any) error {
		switch value := raw.(type) {
		case T:
			return handler(ctx, value)
		case []byte:
			var decoded T
			if err := unmarshal(value, &decoded); err != nil {
				return err
			}
			return handler(ctx, decoded)
		}
		return ErrUnexpectedPayload
	})
}

// OnValueDecode registers a broadcast handler with a concrete value decoder.
// Generated listeners use this path so wire decoding needs no boxed target.
func OnValueDecode[T any](bus Bus, name string, decode func([]byte) (T, error), handler func(context.Context, T) error) error {
	if handler == nil {
		return ErrHandlerRequired
	}
	if decode == nil {
		return ErrDecoderRequired
	}
	return bus.On(name, func(ctx context.Context, raw any) error {
		switch value := raw.(type) {
		case T:
			return handler(ctx, value)
		case []byte:
			decoded, err := decode(value)
			if err != nil {
				return err
			}
			return handler(ctx, decoded)
		}
		return ErrUnexpectedPayload
	})
}

// OnQueueValueDecode registers a queue handler with a concrete value decoder.
// The group is passed to the backend unchanged, including an empty group.
func OnQueueValueDecode[T any](bus Bus, name, group string, decode func([]byte) (T, error), handler func(context.Context, T) error) error {
	if handler == nil {
		return ErrHandlerRequired
	}
	if decode == nil {
		return ErrDecoderRequired
	}
	return bus.OnQueue(name, group, func(ctx context.Context, raw any) error {
		switch value := raw.(type) {
		case T:
			return handler(ctx, value)
		case []byte:
			decoded, err := decode(value)
			if err != nil {
				return err
			}
			return handler(ctx, decoded)
		}
		return ErrUnexpectedPayload
	})
}
