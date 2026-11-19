package diagnostics

import (
	"bytes"
	"context"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf8"
)

type blockingWriter struct{ entered, release chan struct{} }

func (w *blockingWriter) Write(data []byte) (int, error) {
	select {
	case w.entered <- struct{}{}:
	default:
	}
	<-w.release
	return len(data), nil
}

func formatFailure(err error) string { return Line("diagnostic: ", err.Error()) }

func TestReporterCapacityAndDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		writer := &blockingWriter{entered: make(chan struct{}, 1), release: make(chan struct{})}
		reporter := New(writer, 2, formatFailure)
		reporter.Submit(nil)
		reporter.Submit(errors.New("first"))
		<-writer.entered
		for range 3 {
			reporter.Submit(errors.New("queued"))
		}
		if dropped, failed := reporter.Stats(); dropped != 1 || failed != 0 || reporter.Pending() != 2 {
			t.Fatal(dropped, failed, reporter.Pending())
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := reporter.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("blocked output hid close deadline", err)
		}
		reporter.Submit(errors.New("closed"))
		close(writer.release)
		if err := reporter.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if dropped, failed := reporter.Stats(); dropped != 2 || failed != 0 || reporter.Pending() != 0 {
			t.Fatal(dropped, failed, reporter.Pending())
		}
	})
}

type failingWriter struct {
	first, second error
	calls         int
}

func (w *failingWriter) Write([]byte) (int, error) {
	w.calls++
	if w.calls == 1 {
		return 0, w.first
	}
	return 0, w.second
}

func TestReporterPreservesFirstOutputFailure(t *testing.T) {
	first := errors.New("offline")
	for _, initial := range []error{first, nil} {
		writer := &failingWriter{first: initial, second: errors.New("later")}
		reporter := New(writer, 2, formatFailure)
		reporter.Submit(errors.New("one"))
		reporter.Submit(errors.New("two"))
		want := initial
		if want == nil {
			want = io.ErrShortWrite
		}
		if err := reporter.Close(t.Context()); !errors.Is(err, want) {
			t.Fatal("first output failure was lost", err)
		}
		if err := reporter.Close(t.Context()); !errors.Is(err, want) {
			t.Fatal("repeated close lost the output failure", err)
		}
		if dropped, failed := reporter.Stats(); dropped != 0 || failed != 2 {
			t.Fatal(dropped, failed)
		}
	}
}

func TestReporterContinuesAfterFormatterExit(t *testing.T) {
	var output bytes.Buffer
	reporter := New(&output, 4, func(err error) string {
		switch err.Error() {
		case "panic":
			panic("broken formatter")
		case "goexit":
			runtime.Goexit()
		}
		return formatFailure(err)
	})
	for _, message := range []string{"panic", "goexit", "healthy"} {
		reporter.Submit(errors.New(message))
	}
	if err := reporter.Close(t.Context()); !errors.Is(err, ErrAborted) {
		t.Fatal("formatter termination was lost", err)
	}
	if dropped, failed := reporter.Stats(); dropped != 0 || failed != 2 || output.String() != "diagnostic: healthy\n" {
		t.Fatal(dropped, failed, output.String())
	}
}

type writerFunc func([]byte) (int, error)

func (write writerFunc) Write(data []byte) (int, error) { return write(data) }

func TestReporterContinuesAfterWriterExit(t *testing.T) {
	calls := 0
	writer := writerFunc(func(data []byte) (int, error) {
		calls++
		switch calls {
		case 1:
			panic("broken writer")
		case 2:
			runtime.Goexit()
		}
		return len(data), nil
	})
	reporter := New(writer, 4, formatFailure)
	for _, message := range []string{"panic", "goexit", "healthy"} {
		reporter.Submit(errors.New(message))
	}
	if err := reporter.Close(t.Context()); !errors.Is(err, ErrAborted) {
		t.Fatal("writer termination was lost", err)
	}
	if dropped, failed := reporter.Stats(); dropped != 0 || failed != 2 || calls != 3 {
		t.Fatal(dropped, failed, calls)
	}
}

func TestDiagnosticTextIsBounded(t *testing.T) {
	for _, message := range []string{"", "hello", strings.Repeat("x", 5000), strings.Repeat("界", 1500)} {
		line := Line("prefix: ", message)
		if !strings.HasPrefix(line, "prefix: ") || !strings.HasSuffix(line, "\n") || !utf8.ValidString(line) || len(line) > 4096+len("prefix: \n") {
			t.Fatal("invalid diagnostic boundary", len(line))
		}
		if len(message) <= 4096 && line != "prefix: "+message+"\n" {
			t.Fatal("short diagnostic changed")
		}
	}
	if got := (eventFailure{name: "orders", operation: "stopped"}).Error(); got != "event orders: stopped" {
		t.Fatal(got)
	}
	if got := (eventFailure{name: "orders", operation: "handler error", cause: errors.New("offline")}).Error(); got != "event orders handler error: offline" {
		t.Fatal(got)
	}
}

func TestEventsLazyCloseAndRejection(t *testing.T) {
	var events Events
	if dropped, failed := events.Stats(); dropped != 0 || failed != 0 {
		t.Fatal(dropped, failed)
	}
	if err := events.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	events.Submit("orders", "handler error", errors.New("late"))
	if dropped, failed := events.Stats(); dropped != 1 || failed != 0 || events.reporter != nil {
		t.Fatal(dropped, failed)
	}
	if err := events.Drain(time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestEventsDrainReportsFailure(t *testing.T) {
	writer := &failingWriter{first: io.ErrClosedPipe}
	events := Events{reporter: New(writer, 2, formatFailure)}
	events.Submit("orders", "handler error", errors.New("offline"))
	if err := events.Drain(time.Second); !errors.Is(err, io.ErrClosedPipe) || !errors.Is(err, ErrDrain) {
		t.Fatal(err)
	}
	if dropped, failed := events.Stats(); dropped != 0 || failed != 1 {
		t.Fatal(dropped, failed)
	}
	var output bytes.Buffer
	healthy := Events{reporter: New(&output, 1, formatFailure)}
	healthy.Submit("orders", "handler error", errors.New("offline"))
	if err := healthy.Drain(time.Second); err != nil || output.Len() == 0 {
		t.Fatal("healthy reporting did not drain", err)
	}
}

func TestEventsStartsAndDrainsLazily(t *testing.T) {
	var events Events
	events.Submit("orders", "handler error", errors.New("offline"))
	if err := events.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if events.reporter == nil {
		t.Fatal("first failure did not initialize reporting")
	}
	if dropped, failed := events.Stats(); dropped != 0 || failed != 0 {
		t.Fatal(dropped, failed)
	}
}
