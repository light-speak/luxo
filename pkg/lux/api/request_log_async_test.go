package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

type blockedLogWriter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	buf     bytes.Buffer
}

func (w *blockedLogWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	return w.buf.Write(p)
}

func TestAsyncRequestLogsBoundedDrain(t *testing.T) {
	w := &blockedLogWriter{entered: make(chan struct{}), release: make(chan struct{})}
	l := newAsyncRequestLogs(w, 1)
	l.submit(logBuffer("first"))
	<-w.entered
	l.submit(logBuffer("second"))
	l.submit(logBuffer("dropped"))
	if l.dropped.Load() != 1 {
		t.Fatal("full queue did not count its drop")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("close: %v", err)
	}
	l.submit(logBuffer("closed"))
	close(w.release)
	if err := l.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := w.buf.String(); got != "firstsecond" {
		t.Fatalf("got %q", got)
	}
	if l.dropped.Load() != 2 {
		t.Fatal("closed queue did not count its drop")
	}
}

func logBuffer(s string) *[]byte {
	b := requestLogBuffers.Get().(*[]byte)
	*b = append((*b)[:0], s...)
	return b
}

type shortLogWriter struct{}

func (shortLogWriter) Write(p []byte) (int, error) { return 0, nil }

func TestAsyncRequestLogsWriteFailure(t *testing.T) {
	l := newAsyncRequestLogs(shortLogWriter{}, 1)
	l.submit(logBuffer("line"))
	if err := l.close(context.Background()); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("close: %v", err)
	}
	if l.failures.Load() != 1 {
		t.Fatal("missing write failure count")
	}
}

func TestRouterAsyncRequestLogsSampling(t *testing.T) {
	var out bytes.Buffer
	r := NewRouterWithOptions(RouterOptions{RequestLogging: true, LogWriter: &out, RequestLogSampleEvery: 2})
	for range 4 {
		r.logRequest("success", time.Millisecond, nil)
	}
	r.logRequest("failed", time.Millisecond, errors.New("secret"))
	if err := r.CloseRequestLogs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if bytes.Count(out.Bytes(), []byte("success")) != 2 || !bytes.Contains(out.Bytes(), []byte("failed")) {
		t.Fatalf("logs: %q", out.String())
	}
	if bytes.Contains(out.Bytes(), []byte("secret")) {
		t.Fatal("raw error leaked")
	}
}

func TestAsyncRequestLogsConcurrentClose(t *testing.T) {
	var output bytes.Buffer
	l := newAsyncRequestLogs(&output, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				l.submit(logBuffer("entry"))
			}
		})
	}
	for range 8 {
		wg.Go(func() { _ = l.close(context.Background()) })
	}
	wg.Wait()
	if err := l.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	written := bytes.Count(output.Bytes(), []byte("entry"))
	if written*len("entry") != output.Len() {
		t.Fatal("partial or corrupted log record")
	}
	if uint64(written)+l.dropped.Load() != 800 || l.failures.Load() != 0 {
		t.Fatalf("written=%d dropped=%d failures=%d", written, l.dropped.Load(), l.failures.Load())
	}
}

func TestAsyncRequestLogsOversize(t *testing.T) {
	l := newAsyncRequestLogs(io.Discard, 1)
	b := requestLogBuffers.Get().(*[]byte)
	*b = make([]byte, maxRequestLogBytes+1)
	l.submit(b)
	if err := l.close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if l.dropped.Load() != 1 {
		t.Fatal("oversized record was not rejected")
	}
}

func TestRouterDisabledLogClose(t *testing.T) {
	r := NewRouterWithOptions(RouterOptions{})
	if err := r.CloseRequestLogs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if drops, failures := r.RequestLogStats(); drops != 0 || failures != 0 {
		t.Fatal("disabled log stats")
	}
}
