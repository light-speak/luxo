package event

import (
	"bytes"
	"errors"
	"unsafe"
)

// ErrInvalidSnapshot rejects payloads without an explicit ownership contract.
var ErrInvalidSnapshot = errors.New("event: payload requires a valid owned snapshot")

// MaxSnapshotDepth bounds structured type nesting on every event input boundary.
// The first type value is depth one; the event envelope and list containers do
// not add a level. Siblings each start at the same depth.
const MaxSnapshotDepth = 64

// ErrSnapshotDepth rejects deep or cyclic native values and hostile wire input.
var ErrSnapshotDepth = errors.New("event: snapshot nesting exceeds 64 levels")

// Snapshot is a private, validated event value. Only this package can implement
// its delivery operations; generated publishers construct it with NewSnapshot.
type Snapshot interface {
	copyPayload() any
	takePayload() any
}

// SnapshotSource validates native values and creates a structurally independent
// copy. Implementations must not retain mutable aliases into the returned value.
// Generated pointer receivers also reject nil payloads without reflection.
type SnapshotSource interface {
	SnapshotEvent() (Snapshot, error)
}

type inlineSnapshot[T any] struct {
	value T
	clone func(T) T
}

type boxedSnapshot[T any] struct {
	// Box the owned value once, rather than retaining T in this wrapper and
	// boxing it again when the final listener takes ownership.
	value any
	clone func(T) T
}

type immutableSnapshot struct{ value any }

// NewImmutableSnapshot takes an already validated value with no mutable
// descendants. Only the generator's statically immutable field set uses this
// path; callers must never supply pointers, slices or mutable nested objects.
// Box once and share that immutable value across listeners without cloning.
func NewImmutableSnapshot[T any](value T) Snapshot { return immutableSnapshot{value: value} }

func (s immutableSnapshot) copyPayload() any { return s.value }
func (s immutableSnapshot) takePayload() any { return s.value }

// NewSnapshot takes ownership of an already validated value. clone must copy all
// mutable descendants and must not mutate its input. Generated code supplies a
// static function, not a per-message closure or a serialization round trip.
func NewSnapshot[T any](value T, clone func(T) T) (Snapshot, error) {
	if clone == nil {
		return nil, ErrInvalidSnapshot
	}
	// Only the static layout matters; Sizeof never inspects payload data.
	// Small values retain the smaller wrapper. Large values avoid keeping
	// both an inline struct and a separately boxed final-listener payload.
	if unsafe.Sizeof(value) <= unsafe.Sizeof(any(nil)) {
		return inlineSnapshot[T]{value: value, clone: clone}, nil
	}
	return boxedSnapshot[T]{value: value, clone: clone}, nil
}

func (s inlineSnapshot[T]) copyPayload() any { return s.clone(s.value) }
func (s inlineSnapshot[T]) takePayload() any { return s.value }

// Interfaces and pointer-like values use inlineSnapshot; boxed values cannot
// be a nil interface and always retain their constructor's concrete T.
func (s boxedSnapshot[T]) copyPayload() any { return s.clone(s.value.(T)) }
func (s boxedSnapshot[T]) takePayload() any { return s.value }

func captureSnapshot(payload any) (Snapshot, error) {
	switch value := payload.(type) {
	case SnapshotSource:
		owned, err := value.SnapshotEvent()
		if err != nil {
			return nil, err
		}
		if owned == nil {
			return nil, ErrInvalidSnapshot
		}
		return owned, nil
	case []byte:
		// Low-level wire payloads are validated by the registered decoder. They
		// still need ownership isolation, including between raw listeners.
		return NewSnapshot(bytes.Clone(value), bytes.Clone)
	default:
		return nil, ErrInvalidSnapshot
	}
}
