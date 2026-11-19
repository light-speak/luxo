// Command ws-codable serves a bounded adversarial JSON peer for native SDK tests.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"
)

type peer struct {
	ctx  context.Context
	wg   sync.WaitGroup
	next atomic.Uint64
}

type peerConnection struct {
	peer            *peer
	connection      *websocket.Conn
	id              uint64
	streams         map[uint64]string
	calls           int
	subscriptions   int
	unsubscriptions int
}

type request struct {
	ID          uint64  `json:"$id"`
	API         string  `json:"$api"`
	Subscribe   string  `json:"$sub"`
	Unsubscribe *uint64 `json:"$unsub"`
	Models      int     `json:"models"`
	JSON        int     `json:"json"`
	DataFirst   bool    `json:"dataFirst"`
	Target      string  `json:"target"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &peer{ctx: ctx}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	server := &http.Server{Handler: p, ReadHeaderTimeout: 5 * time.Second}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	fmt.Println("ws://" + listener.Addr().String() + "/ws")
	_, _ = io.Copy(io.Discard, os.Stdin)
	shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	err = server.Shutdown(shutdown)
	cancel()
	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-shutdown.Done():
		return shutdown.Err()
	}
	if servedErr := <-served; servedErr != http.ErrServerClosed {
		return servedErr
	}
	return err
}

func (p *peer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.wg.Add(1)
	defer p.wg.Done()
	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer connection.CloseNow()
	connection.SetReadLimit(64 << 10)
	c := &peerConnection{peer: p, connection: connection, id: p.next.Add(1), streams: make(map[uint64]string)}
	defer c.report()
	for {
		kind, data, err := connection.Read(p.ctx)
		if err != nil || kind != websocket.MessageText {
			return
		}
		var req request
		if json.Unmarshal(data, &req) != nil {
			return
		}
		if c.handle(req) != nil {
			return
		}
	}
}

func (c *peerConnection) handle(req request) error {
	if req.Unsubscribe != nil {
		// Count every frame so duplicate cancellation remains observable.
		c.unsubscriptions++
		delete(c.streams, *req.Unsubscribe)
		return nil
	}
	if req.Subscribe != "" {
		c.streams[req.ID] = req.Subscribe
		c.subscriptions++
		return c.write([]byte(fmt.Sprintf(`{"$sub":%d,"ok":true}`, req.ID)))
	}
	c.calls++
	switch req.API {
	case "stats":
		return c.data("$id", req.ID, c.stats(), false)
	case "depth":
		payload, err := depthJSON(req.Models, req.JSON)
		if err != nil {
			return c.badRequest(req.ID)
		}
		return c.data("$id", req.ID, payload, req.DataFirst)
	case "emit":
		return c.emit(req)
	default:
		return c.badRequest(req.ID)
	}
}

func depthJSON(models, depth int) ([]byte, error) {
	if models < 1 || models > 65 || depth < 0 || depth > 129 {
		return nil, fmt.Errorf("depth fixture values out of range")
	}
	var body strings.Builder
	body.Grow(models*10 + depth*2 + 16)
	body.WriteString(strings.Repeat(`{"child":`, models-1))
	body.WriteString(`{"metadata":`)
	body.WriteString(strings.Repeat("[", depth))
	body.WriteByte('0')
	body.WriteString(strings.Repeat("]", depth))
	body.WriteString(strings.Repeat("}", models))
	return []byte(body.String()), nil
}

func (c *peerConnection) emit(req request) error {
	var id uint64
	for candidate, name := range c.streams {
		if name == req.Target && candidate > id {
			id = candidate
		}
	}
	if id == 0 {
		return c.badRequest(req.ID)
	}
	payload := []byte("7")
	if req.Target != "peer" {
		var err error
		payload, err = depthJSON(req.Models, req.JSON)
		if err != nil {
			return c.badRequest(req.ID)
		}
	}
	if err := c.data("$stream", id, payload, req.DataFirst); err != nil {
		return err
	}
	return c.data("$id", req.ID, []byte("true"), false)
}

func (c *peerConnection) data(field string, id uint64, payload []byte, first bool) error {
	frame := make([]byte, 0, len(field)+len(payload)+48)
	frame = append(frame, '{')
	if first {
		frame = append(frame, `"data":`...)
		frame = append(frame, payload...)
		frame = append(frame, ',')
	}
	frame = append(frame, '"')
	frame = append(frame, field...)
	frame = append(frame, `":`...)
	frame = strconv.AppendUint(frame, id, 10)
	if !first {
		frame = append(frame, `,"data":`...)
		frame = append(frame, payload...)
	}
	return c.write(append(frame, '}'))
}

func (c *peerConnection) write(data []byte) error {
	return c.connection.Write(c.peer.ctx, websocket.MessageText, data)
}

func (c *peerConnection) badRequest(id uint64) error {
	return c.write([]byte(fmt.Sprintf(`{"$id":%d,"error":"BadRequest","code":400,"message":"invalid peer command"}`, id)))
}

func (c *peerConnection) stats() []byte {
	return []byte(fmt.Sprintf(`{"connection":%d,"calls":%d,"active":%d,"subscriptions":%d,"unsubscriptions":%d}`,
		c.id, c.calls, len(c.streams), c.subscriptions, c.unsubscriptions))
}

func (c *peerConnection) report() {
	fmt.Fprintf(os.Stderr, "connection=%d calls=%d subscriptions=%d unsubscriptions=%d remaining=%d\n",
		c.id, c.calls, c.subscriptions, c.unsubscriptions, len(c.streams))
}
