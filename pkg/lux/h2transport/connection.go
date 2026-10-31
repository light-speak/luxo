package h2transport

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/net/http2"
)

const candidateStreams = 64
const candidateBody = 2 << 20

type requestTask struct {
	admitted                     bool
	deadline                     time.Time
	id                           uint32
	request                      *receivedRequest
	response                     *response
	ctx                          context.Context
	cancel                       context.CancelFunc
	dispatched, done, outputDone bool
}

type candidateConnection struct {
	ackDeadline         time.Time
	limits              Limits
	slots               *byteBudget
	deadline            time.Time
	appliedReadDeadline time.Time
	rate                controlRate
	windowCredit        windowCredit
	budget              *byteBudget
	ctx                 context.Context
	conn                net.Conn
	tlsState            *tls.ConnectionState
	handler             http.Handler
	inbox               *controlMailbox
	writer              *controlWriter
	receiver            *requestReceiver
	mu                  sync.Mutex // Reader state and worker completion, never held around handler/I/O.
	tasks               map[uint32]*requestTask
	draining            bool
	openingComplete     bool
	readDeadlineSet     bool
	jobs                chan *requestTask
}

func newCandidateConnection(ctx context.Context, conn net.Conn, handler http.Handler) *candidateConnection {
	q, _ := newDataQueue(candidateStreams, candidateStreams*candidateBody, func([]byte) {})
	r, _ := newRequestReceiver(candidateStreams, candidateBody, candidateStreams*candidateBody)
	m, _ := newControlMailbox(512)
	c := &candidateConnection{ctx: ctx, conn: conn, handler: handler, inbox: m, receiver: r, tasks: make(map[uint32]*requestTask), jobs: make(chan *requestTask, candidateStreams)}
	c.rate = controlRate{burst: 1024, interval: time.Second}
	c.limits = DefaultLimits()
	q.released = c.outputFinished
	c.writer = newControlWriter(newDataWriter(q, conn, 16<<10))
	c.writer.data.windowCredit = &c.windowCredit
	return c
}

func (c *candidateConnection) run() error {
	ctx, cancel := context.WithCancel(c.ctx)
	c.ctx = ctx
	writerDone := make(chan error, 1)
	go func() { writerDone <- c.writer.run(ctx, c.conn, c.inbox, c.limits.WriteTimeout) }()
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(c.work)
	}
	err := c.read()
	cancel()
	_ = c.conn.Close()
	close(c.jobs)
	<-writerDone
	workers.Wait()
	c.mu.Lock()
	for _, task := range c.tasks {
		task.cancel()
		task.response.release()
		if task.admitted {
			c.slots.give(1)
		}
	}
	clear(c.tasks)
	c.receiver.close()
	c.mu.Unlock()
	return err
}

func (c *candidateConnection) read() error {
	if err := c.conn.SetReadDeadline(time.Now().Add(c.limits.HandshakeTimeout)); err != nil {
		return err
	}
	f, opening, err := readClientOpening(bufio.NewReaderSize(c.conn, 8<<10))
	if err != nil {
		return err
	}
	if err := c.inbox.put(opening); err != nil {
		return err
	}
	c.mu.Lock()
	c.openingComplete = true
	c.ackDeadline = time.Now().Add(c.limits.HandshakeTimeout)
	c.mu.Unlock()
	for {
		if err := c.setReadDeadline(time.Now()); err != nil {
			return err
		}
		frame, err := f.ReadFrame()
		if err == nil {
			if settings, ok := frame.(*http2.SettingsFrame); ok && settings.IsAck() {
				c.mu.Lock()
				c.ackDeadline = time.Time{}
				c.mu.Unlock()
			}
			err = c.dispatchFrame(frame)
		}
		if err == nil {
			continue
		}
		var streamError http2.StreamError
		if errors.As(err, &streamError) {
			if !c.rate.allow(time.Now()) {
				return http2.ConnectionError(http2.ErrCodeEnhanceYourCalm)
			}
			c.cancelTask(streamError.StreamID)
			if err := c.inbox.put(controlCommand{kind: abortStreamCommand, streamID: streamError.StreamID, code: streamError.Code}); err == nil {
				continue
			}
		}
		return err
	}
}

func (c *candidateConnection) dispatchFrame(frame http2.Frame) error {
	if !c.allowFrame(frame) {
		return http2.ConnectionError(http2.ErrCodeEnhanceYourCalm)
	}
	if command, ok, err := decodeControl(frame); ok {
		if err != nil {
			return err
		}
		if command.kind == goAwayCommand {
			if command.code == http2.ErrCodeNo {
				return c.beginDrain()
			}
			return net.ErrClosed
		}
		if command.kind == resetCommand {
			c.cancelTask(command.streamID)
		}
		return c.inbox.put(command)
	}
	switch f := frame.(type) {
	case *http2.MetaHeadersFrame:
		return c.receive(f, nil)
	case *http2.DataFrame:
		return c.receive(nil, f)
	case *http2.PushPromiseFrame:
		return http2.ConnectionError(http2.ErrCodeProtocol)
	}
	return nil // Unknown extension frames and advisory priorities are ignored.
}

func (c *candidateConnection) receive(head *http2.MetaHeadersFrame, data *http2.DataFrame) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var req *receivedRequest
	var credit receiveCredit
	var err error
	var id uint32
	if head != nil {
		id = head.StreamID
		fresh := id > c.receiver.lastStream
		if fresh && c.draining {
			return http2.StreamError{StreamID: id, Code: http2.ErrCodeRefusedStream}
		}
		req, credit, err = c.receiver.headers(head)
		if err == nil && fresh {
			if !c.slots.take(1) {
				c.receiver.release(id)
				return http2.StreamError{StreamID: id, Code: http2.ErrCodeRefusedStream}
			}
			ctx, cancel := context.WithCancel(c.ctx)
			deadline := time.Now().Add(c.limits.BodyTimeout)
			c.tasks[id] = &requestTask{id: id, ctx: ctx, cancel: cancel, deadline: deadline, admitted: true}
			if c.deadline.IsZero() || deadline.Before(c.deadline) {
				c.deadline = deadline
			}
			err = c.inbox.put(controlCommand{kind: openStreamCommand, streamID: id})
		}
	} else {
		id = data.StreamID
		req, credit, err = c.receiver.data(data)
	}
	if credit.connection != 0 || credit.stream != 0 {
		if putErr := c.inbox.put(controlCommand{kind: receiveWindowCommand, streamID: id, credit: credit}); putErr != nil {
			return putErr
		}
	}
	if err != nil || req == nil {
		return err
	}
	task := c.tasks[id]
	c.replaceTaskDeadline(task, time.Now().Add(c.limits.ResponseTimeout))
	task.request, task.dispatched = req, true
	select {
	case c.jobs <- task:
		return nil
	default:
		task.dispatched = false
		return http2.StreamError{StreamID: id, Code: http2.ErrCodeRefusedStream}
	}
}

func (c *candidateConnection) beginDrain() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.draining {
		return nil
	}
	c.draining = true
	if !c.openingComplete {
		return c.conn.Close()
	}
	return c.inbox.put(controlCommand{kind: drainCommand, streamID: c.receiver.lastStream})
}

func (c *candidateConnection) cancelTask(id uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if task := c.tasks[id]; task != nil {
		task.cancel()
	}
}

func (c *candidateConnection) outputFinished(id uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if task := c.tasks[id]; task != nil {
		task.outputDone = true
		task.cancel()
		c.releaseTask(task)
	}
}

func (c *candidateConnection) releaseTask(task *requestTask) {
	if task.outputDone && (!task.dispatched || task.done) {
		if task.admitted {
			c.slots.give(1)
			task.admitted = false
		}
		task.response.release()
		c.receiver.release(task.id)
		delete(c.tasks, task.id)
		if !task.deadline.IsZero() && task.deadline.Equal(c.deadline) {
			c.refreshDeadline()
			// A completed request must not leave its expired deadline attached to
			// an in-progress frame read for a different request.
			if err := c.applyReadDeadlineLocked(c.lockedReadDeadline(time.Now())); err != nil {
				_ = c.conn.Close()
			}
		}
	}
}

func payloadFrame(frame http2.Frame) bool {
	switch f := frame.(type) {
	case *http2.MetaHeadersFrame:
		return true
	case *http2.DataFrame:
		return len(f.Data()) > 0
	default:
		return false
	}
}

// Configure once, before either connection goroutine starts. Keep the RFC
// initial connection credit as a floor; storage limits remain independent.
func (c *candidateConnection) configureReceiveWindows() {
	connection := uint32(max(65535, min(8<<20, c.receiver.limit)))
	stream := uint32(max(65535, min(2<<20, c.receiver.maxBody)))
	c.receiver.window.available = connection
	c.receiver.initialWindow = stream
	c.writer.receiveConnection, c.writer.receiveStream = connection, stream
}
