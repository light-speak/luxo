package rpc

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func TestTCPObserverDelaysPeerUntilReadiness(t *testing.T) {
	for _, scenario := range []string{"disconnect", "one-queued", "overflow"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			var hold atomic.Bool
			entered := make(chan struct{})
			release := make(chan struct{})
			canceled := make(chan struct{})
			router := api.NewRouter()
			if err := router.Registry.Register("ping", 1); err != nil {
				t.Fatal(err)
			}
			router.Handle("ping", func(ctx context.Context, request *api.Request) error {
				calls.Add(1)
				if hold.Swap(false) {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						close(canceled)
						return ctx.Err()
					}
				}
				request.Buf.B = append(request.Buf.B, 42)
				return nil
			})
			server := NewServer(router)
			peer := dialTestRPCConn(t, startTestRPCServer(t, server))
			defer peer.Close()
			if err := initiateHandshake(t.Context(), peer, time.Second); err != nil {
				t.Fatal(err)
			}
			if err := peer.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			request := encodeRequestEnvelope(requestKindCall, "", encodeCallRequest(1, nil, nil))

			write := func() {
				t.Helper()
				if err := WriteFrame(peer, request); err != nil {
					t.Fatal(err)
				}
			}
			read := func() {
				t.Helper()
				got, err := ReadFrame(peer, nil)
				if err != nil || len(got) != 2 || got[0] != statusOK || got[1] != 42 {
					t.Fatalf("response: %x %v", got, err)
				}
			}

			reader := warmTCPTestReader(t, server, write, read)
			waitUntilTCPReader(t, reader, func(r *connectionReader) bool { return !r.busy })
			warmCalls := calls.Load()
			hold.Store(true)
			write()
			awaitTCPObserverSignal(t, entered)
			if observer := reader.observation.observer; observer == nil || !observer.armed.Load() {
				t.Fatal("reused connection did not arm the observer")
			}
			switch scenario {
			case "disconnect":
				if err := peer.Close(); err != nil {
					t.Fatal(err)
				}
				awaitTCPObserverSignal(t, canceled)
			case "one-queued":
				write()
				waitUntilTCPReader(t, reader, func(r *connectionReader) bool { return r.queued })
				close(release)
				read()
				read()
				if got := calls.Load(); got != warmCalls+2 {
					t.Fatalf("expected the queued call to run once, got %d total calls", got)
				}
			case "overflow":
				write()
				write()
				awaitTCPObserverSignal(t, canceled)
				if got := calls.Load(); got != warmCalls+1 {
					t.Fatalf("queued call reached the handler after overflow: %d total calls", got)
				}
			}
			var closing sync.WaitGroup
			closing.Go(server.Close)
			closing.Go(server.Close)
			closing.Wait()
			if server.readObservers.poller != nil {
				t.Fatal("server retained the epoll waiter after closing")
			}
		})
	}
}

func warmTCPTestReader(t *testing.T, server *Server, write, read func()) *connectionReader {
	t.Helper()
	write()
	read()
	server.connMu.Lock()
	coldPoller := server.readObservers.poller
	server.connMu.Unlock()
	if coldPoller != nil {
		t.Fatal("first exchange opened a kernel observer")
	}
	for attempts := 0; attempts < 20; attempts++ {
		time.Sleep(time.Millisecond)
		write()
		read()
		if reader := singleTCPTestReader(t, server); reader != nil {
			return reader
		}
	}
	t.Fatal("reused connection never attached to the epoll waiter")
	return nil
}

func singleTCPTestReader(t *testing.T, server *Server) *connectionReader {
	t.Helper()
	server.connMu.Lock()
	poller := server.readObservers.poller
	server.connMu.Unlock()
	if poller == nil {
		return nil
	}
	poller.mu.Lock()
	defer poller.mu.Unlock()
	if poller.first == nil || len(poller.peers) != 0 {
		t.Fatal("expected one attached TCP connection")
	}
	return poller.first.reader
}

func awaitTCPObserverSignal(t *testing.T, ready <-chan struct{}) {
	t.Helper()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("observer did not release the handler")
	}
}

func waitUntilTCPReader(t *testing.T, reader *connectionReader, ready func(*connectionReader) bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		reader.mu.Lock()
		reached := ready(reader)
		reader.mu.Unlock()
		if reached {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("reader did not reach the expected state")
}

func TestSocketObserverRejectsUnusableConnections(t *testing.T) {
	server := NewServer(api.NewRouter())
	defer server.Close()
	if observer := server.registerReadObserver(&connectionReader{conn: &net.TCPConn{}}); observer != nil {
		t.Fatal("zero TCP connection unexpectedly received a watcher")
	}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if observer := server.registerReadObserver(&connectionReader{conn: a}); observer != nil {
		t.Fatal("abstract connection must use the portable observer")
	}
}

func TestSocketObserverSetupFailuresReleasePartialState(t *testing.T) {
	for _, failure := range []string{"create", "pipe", "ctl"} {
		t.Run(failure, func(t *testing.T) {
			pollFD := -1
			pipe := [2]int{-1, -1}
			setup := socketPollerSetup{
				create: func(flags int) (int, error) {
					if failure == "create" {
						return -1, syscall.EMFILE
					}
					var err error
					pollFD, err = syscall.EpollCreate1(flags)
					return pollFD, err
				},
				pipe: func(fds []int, flags int) error {
					if failure == "pipe" {
						return syscall.EMFILE
					}
					if err := syscall.Pipe2(fds, flags); err != nil {
						return err
					}
					copy(pipe[:], fds)
					return nil
				},
				ctl: func(int, int, int, *syscall.EpollEvent) error { return syscall.ENOMEM },
			}
			if poller := newSocketPoller(setup); poller != nil {
				poller.close()
				t.Fatal("partial socket poller escaped setup")
			}
			for _, fd := range []int{pollFD, pipe[0], pipe[1]} {
				if fd >= 0 {
					var st syscall.Stat_t
					if err := syscall.Fstat(fd, &st); err != syscall.EBADF {
						t.Fatalf("descriptor %d survived failure: %v", fd, err)
					}
				}
			}
		})
	}
}

func TestSocketWaitFailureRestoresPortableObserver(t *testing.T) {
	server := NewServer(api.NewRouter())
	defer server.Close()
	socket := newObserverTestSocket(t)
	reader := newConnectionReader(server, socket)
	defer reader.close()
	poller := &socketPoller{fd: -1, stopped: make(chan struct{}), peers: make(map[uint64]*fdObserver)}
	observer := &fdObserver{poller: poller, reader: reader}
	observer.armed.Store(true)
	poller.first = observer
	poller.peers[2] = &fdObserver{poller: poller, reader: reader}
	if !poller.continueWaiting(syscall.EINTR) {
		t.Fatal("interrupted waiter did not retry")
	}
	poller.run()
	if !poller.failed.Load() || observer.arm() || poller.register(reader, nil) != nil {
		t.Fatal("failed waiter continued accepting observations")
	}
}

func TestSocketObserverFollowsCancellationAndRegistrationFailure(t *testing.T) {
	server := NewServer(api.NewRouter())
	defer server.Close()
	socket := newObserverTestSocket(t)
	reader := newConnectionReader(server, socket)
	reader.cancel()
	defer reader.close()
	if observer := reader.startObservation(); observer != nil || reader.observation.observer != nil {
		t.Fatal("canceled connection retained a readiness observer")
	}
	server.readObserversStarted = true
	if observer := server.registerReadObserver(reader); observer != nil {
		t.Fatal("unavailable poller accepted the socket")
	}
	server.Close()
	if observer := server.registerReadObserver(reader); observer != nil {
		t.Fatal("closed observer pool accepted the socket")
	}
	raw, err := socket.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	poller := &socketPoller{fd: -1}
	if observer := poller.register(reader, raw); observer != nil {
		t.Fatal("socket attached to an invalid epoll descriptor")
	}
}

func newObserverTestSocket(t *testing.T) *net.TCPConn {
	t.Helper()
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	peer, err := net.DialTCP("tcp", nil, listener.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { peer.Close() })
	socket, err := listener.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { socket.Close() })
	return socket
}

func TestSocketObserverRecyclesOnlyDetachedRegistration(t *testing.T) {
	server := NewServer(api.NewRouter())
	defer server.Close()
	makeReader := func() *connectionReader {
		t.Helper()
		reader := newConnectionReader(server, newObserverTestSocket(t))
		t.Cleanup(reader.close)
		return reader
	}
	first, second := makeReader(), makeReader()
	one := server.registerReadObserver(first)
	two := server.registerReadObserver(second)
	if one == nil || two == nil || one == two || one.poller != two.poller {
		t.Fatal("active connections did not share independent registrations")
	}
	poller := one.poller
	oldID := one.id
	one.armed.Store(true)
	first.close()
	poller.release(one)
	second.close()
	poller.release(two)

	poller.mu.Lock()
	if poller.spare != one || poller.first != nil || len(poller.peers) != 0 ||
		one.reader != nil || one.conn != nil || one.armed.Load() {
		poller.mu.Unlock()
		t.Fatal("recycled observer retained state from its prior connection")
	}
	poller.mu.Unlock()

	third := makeReader()
	next := server.registerReadObserver(third)
	if next != one || next.id == oldID || next.reader != third {
		t.Fatal("observer storage was not reused with a fresh registration")
	}
	poller.notify(oldID)
	third.close()
	poller.release(next)
}

func TestTCPObserverWatchesStreamPreparationAfterUnaryReuse(t *testing.T) {
	for _, extraInput := range []bool{false, true} {
		name := "disconnect"
		if extraInput {
			name = "unexpected-input"
		}
		t.Run(name, func(t *testing.T) { testObservedStreamPreparation(t, extraInput) })
	}
}

func testObservedStreamPreparation(t *testing.T, extraInput bool) {
	t.Helper()
	router := api.NewRouter()
	for _, endpoint := range []*schema.API{
		{ID: 1, Name: "ping", ReturnType: "Int"},
		{ID: 2, Name: "wait", ReturnType: "Int", Stream: true},
	} {
		if err := router.Schema.RegisterAPI(endpoint); err != nil {
			t.Fatal(err)
		}
	}
	if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
		t.Fatal(err)
	}
	router.Handle("ping", func(_ context.Context, request *api.Request) error {
		request.Buf.B = append(request.Buf.B, 42)
		return nil
	})
	entered, canceled := make(chan struct{}), make(chan struct{})
	router.HandleStreamPrepared("wait", func(ctx context.Context, _ *api.Request, _ any) (api.PreparedStreamHandler, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	})
	server := NewServer(router)
	peer := dialTestRPCConn(t, startTestRPCServer(t, server))
	if err := initiateHandshake(t.Context(), peer, time.Second); err != nil {
		t.Fatal(err)
	}
	send := func(kind byte, id int) {
		if err := WriteFrame(peer, encodeRequestEnvelope(kind, "", encodeCallRequest(id, nil, nil))); err != nil {
			t.Fatal(err)
		}
	}
	reader := warmTCPTestReader(t, server, func() { send(requestKindCall, 1) }, func() {
		got, err := ReadFrame(peer, nil)
		if err != nil || len(got) != 2 || got[0] != statusOK || got[1] != 42 {
			t.Fatalf("unary response: %x %v", got, err)
		}
	})
	waitUntilTCPReader(t, reader, func(r *connectionReader) bool { return !r.busy })
	send(requestKindStream, 2)
	awaitTCPObserverSignal(t, entered)
	if extraInput {
		if _, err := peer.Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
	} else if err := peer.Close(); err != nil {
		t.Fatal(err)
	}
	awaitTCPObserverSignal(t, canceled)
}
