package h2transport

import (
	"context"
	"errors"
	"sync"
)

var errMailboxFull = errors.New("HTTP/2 control mailbox full")

// controlMailbox is the bounded handoff between readers and the connection
// writer. Control fields are copied by value; unary response pointers transfer
// ownership and remain bounded by admitted tasks and their response size limit.
// Overflow must terminate the connection, never silently lose an update/body.
type controlMailbox struct {
	mu          sync.Mutex
	items       []controlCommand
	head, count int
	closed      bool
	wake, done  chan struct{}
}

func newControlMailbox(capacity int) (*controlMailbox, error) {
	if capacity < 1 || capacity > 65535 {
		return nil, errQueueConfig
	}
	return &controlMailbox{items: make([]controlCommand, capacity), wake: make(chan struct{}, 1), done: make(chan struct{})}, nil
}

func (m *controlMailbox) put(command controlCommand) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errQueueClosed
	}
	if m.count == len(m.items) {
		return errMailboxFull
	}
	m.items[(m.head+m.count)%len(m.items)] = command
	m.count++
	if m.count == 1 {
		select {
		case m.wake <- struct{}{}:
		default:
		}
	}
	return nil
}

func (m *controlMailbox) poll() (controlCommand, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return controlCommand{}, false, errQueueClosed
	}
	if m.count == 0 {
		return controlCommand{}, false, nil
	}
	command := m.items[m.head]
	m.items[m.head] = controlCommand{}
	m.head = (m.head + 1) % len(m.items)
	m.count--
	return command, true, nil
}

func (m *controlMailbox) wait(ctx context.Context) (controlCommand, error) {
	for {
		if err := ctx.Err(); err != nil {
			return controlCommand{}, err
		}
		command, ok, err := m.poll()
		if err != nil || ok {
			return command, err
		}
		select {
		case <-ctx.Done():
			return controlCommand{}, ctx.Err()
		case <-m.done:
			return controlCommand{}, errQueueClosed
		case <-m.wake:
		}
	}
}

func (m *controlMailbox) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	clear(m.items)
	m.count = 0
	close(m.done)
}
