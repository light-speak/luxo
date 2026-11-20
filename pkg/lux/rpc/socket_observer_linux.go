package rpc

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
)

const peerCloseEvents = syscall.EPOLLRDHUP | syscall.EPOLLONESHOT

type socketObservation struct {
	observer  *fdObserver
	exchanged bool
	attempted bool
}

func (r *connectionReader) closeObservation() {
	if observer := r.observation.observer; observer != nil {
		observer.poller.release(observer)
	}
}

// Only the current reader may inspect its read-ahead bytes.
func (r *connectionReader) hasBufferedInput() bool {
	return r.input.start != r.input.end || r.input.pending != nil
}

func (r *connectionReader) observePeer(readAhead bool) {
	observer := r.observation.observer
	if observer == nil && r.observation.exchanged && !r.observation.attempted {
		observer = r.startObservation()
	}
	if observer == nil || readAhead || !observer.arm() {
		r.ready.Signal()
	}
}

// Let single-exchange connections use only the existing portable reader.
func (r *connectionReader) startObservation() *fdObserver {
	r.observation.attempted = true
	if r.ctx.Err() != nil {
		return nil
	}
	r.observation.observer = r.server.registerReadObserver(r)
	return r.observation.observer
}

func (r *connectionReader) noteExchange() {
	r.observation.exchanged = true
}

func (r *connectionReader) responseWriter() io.Writer {
	if observer := r.observation.observer; observer != nil {
		return observer
	}
	return r.conn
}

// Each serving instance shares one epoll wait across all established TCP
// connections. Idle observers neither read frames nor wake connection workers.
type socketObserverPool struct {
	poller *socketPoller
}

func (s *Server) registerReadObserver(reader *connectionReader) *fdObserver {
	conn, ok := reader.conn.(*net.TCPConn)
	if !ok {
		return nil
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		return nil
	}
	s.connMu.Lock()
	defer s.connMu.Unlock()
	if s.closing {
		return nil
	}
	if !s.readObserversStarted {
		s.readObservers.poller = newSocketPoller(socketPollerSetup{
			create: syscall.EpollCreate1,
			pipe:   syscall.Pipe2,
			ctl:    syscall.EpollCtl,
		})
		s.readObserversStarted = true
	}
	poller := s.readObservers.poller
	if poller == nil {
		return nil
	}
	return poller.register(reader, raw)
}

func (s *Server) closeReadObservers() {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	if poller := s.readObservers.poller; poller != nil {
		poller.close()
		s.readObservers.poller = nil
	}
}

type socketPoller struct {
	mu       sync.Mutex
	nextID   uint64
	first    *fdObserver
	peers    map[uint64]*fdObserver
	spare    *fdObserver
	failed   atomic.Bool
	fd       int
	stopPipe [2]int
	stopped  chan struct{}
}

type socketPollerSetup struct {
	create func(int) (int, error)
	pipe   func([]int, int) error
	ctl    func(int, int, int, *syscall.EpollEvent) error
}

func newSocketPoller(setup socketPollerSetup) *socketPoller {
	fd, err := setup.create(syscall.EPOLL_CLOEXEC)
	if err != nil {
		return nil
	}
	var stopPipe [2]int
	if err := setup.pipe(stopPipe[:], syscall.O_CLOEXEC); err != nil {
		syscall.Close(fd)
		return nil
	}
	stop := syscall.EpollEvent{Events: syscall.EPOLLIN}
	if err := setup.ctl(fd, syscall.EPOLL_CTL_ADD, stopPipe[0], &stop); err != nil {
		syscall.Close(stopPipe[0])
		syscall.Close(stopPipe[1])
		syscall.Close(fd)
		return nil
	}
	p := &socketPoller{fd: fd, stopPipe: stopPipe, stopped: make(chan struct{})}
	go p.run()
	return p
}

func (p *socketPoller) register(reader *connectionReader, conn syscall.RawConn) *fdObserver {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failed.Load() {
		return nil
	}
	p.nextID++
	id := p.nextID
	// IDs identify registrations even after the OS recycles a socket descriptor.
	o := p.spare
	if o == nil {
		o = &fdObserver{}
		o.modify = o.modifyEvents
	}
	p.spare = nil
	o.poller, o.reader, o.conn, o.id = p, reader, conn, id
	o.event = syscall.EpollEvent{Events: peerCloseEvents, Fd: int32(id), Pad: int32(id >> 32)}
	o.operation = syscall.EPOLL_CTL_ADD
	if conn.Control(o.modify) != nil || o.ctlError != nil {
		p.recycleLocked(o)
		return nil
	}
	o.operation = syscall.EPOLL_CTL_MOD
	if p.first == nil {
		p.first = o
	} else {
		if p.peers == nil {
			p.peers = make(map[uint64]*fdObserver)
		}
		p.peers[id] = o
	}
	return o
}

func (p *socketPoller) run() {
	defer close(p.stopped)
	var events [64]syscall.EpollEvent
	for {
		count, err := syscall.EpollWait(p.fd, events[:], -1)
		if !p.continueWaiting(err) {
			return
		}
		for _, event := range events[:max(count, 0)] {
			id := uint64(uint32(event.Fd)) | uint64(uint32(event.Pad))<<32
			if id == 0 {
				return
			}
			p.notify(id)
		}
	}
}

func (p *socketPoller) continueWaiting(err error) bool {
	if err == nil || err == syscall.EINTR {
		return true
	}
	p.enableFallback()
	return false
}

func (p *socketPoller) notify(id uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	peer := p.first
	if peer == nil || peer.id != id {
		peer = p.peers[id]
	}
	wakeObservation(peer)
}

func wakeObservation(peer *fdObserver) {
	if peer != nil && peer.armed.Load() {
		peer.reader.ready.Signal()
	}
}

func (p *socketPoller) enableFallback() {
	p.failed.Store(true)
	p.mu.Lock()
	defer p.mu.Unlock()
	wakeObservation(p.first)
	for _, peer := range p.peers {
		wakeObservation(peer)
	}
}

func (p *socketPoller) close() {
	syscall.Close(p.stopPipe[1])
	<-p.stopped
	syscall.Close(p.stopPipe[0])
	syscall.Close(p.fd)
}

type fdObserver struct {
	poller    *socketPoller
	reader    *connectionReader
	conn      syscall.RawConn
	modify    func(uintptr)
	ctlError  error
	id        uint64
	operation int32
	event     syscall.EpollEvent
	armed     atomic.Bool
}

func (o *fdObserver) arm() bool {
	o.armed.Store(true)
	return !o.poller.failed.Load() && o.updateEvents(peerCloseEvents|syscall.EPOLLIN)
}

// Only the dispatcher changes interest flags. Reuse the control callback and
// event storage across frames, while Control pins the socket descriptor.
func (o *fdObserver) updateEvents(events uint32) bool {
	o.event.Events = events
	return o.conn.Control(o.modify) == nil && o.ctlError == nil
}

func (o *fdObserver) modifyEvents(fd uintptr) {
	o.ctlError = syscall.EpollCtl(o.poller.fd, int(o.operation), int(fd), &o.event)
}

func (o *fdObserver) Write(p []byte) (int, error) {
	if o.armed.Swap(false) {
		// The next request can use the foreground read after the response.
		o.updateEvents(peerCloseEvents)
	}
	return o.reader.conn.Write(p)
}

// No connection worker uses the observer after serve has joined them.
func (p *socketPoller) release(o *fdObserver) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.first == o {
		p.first = nil
	} else {
		delete(p.peers, o.id)
	}
	p.recycleLocked(o)
}

func (p *socketPoller) recycleLocked(o *fdObserver) {
	if p.spare != nil {
		return
	}
	o.poller, o.reader, o.conn, o.ctlError = nil, nil, nil, nil
	o.armed.Store(false)
	p.spare = o
}
