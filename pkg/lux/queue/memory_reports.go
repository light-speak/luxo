package queue

import (
	"context"
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/light-speak/luxo/internal/diagnostics"
)

// ErrDiagnosticShutdown distinguishes failed reporting from unfinished tasks.
var ErrDiagnosticShutdown = errors.New("queue: diagnostic shutdown failed")

// memoryDiagnostic defers error methods and numeric formatting to the reporter.
type memoryDiagnostic struct {
	queue, operation string
	attempt, limit   int
	cause            error
}

func (d memoryDiagnostic) Error() string {
	message := d.operation
	if d.attempt != 0 {
		message = "attempt " + strconv.Itoa(d.attempt) + "/" + strconv.Itoa(d.limit) + " failed"
	}
	if d.cause != nil {
		message += ": " + d.cause.Error()
	}
	return "queue " + d.queue + ": " + message
}

func formatMemoryDiagnostic(failure error) string {
	return diagnostics.Line("", failure.Error())
}

// Only diagnostics can be rejected here, never business tasks or dead letters.
func (m *MemoryQueue) reportDiagnostic(failure memoryDiagnostic) {
	m.mu.Lock()
	if m.reportsClosed {
		m.reportsRejected.Add(1)
		m.mu.Unlock()
		return
	}
	if m.reports == nil {
		m.reports = diagnostics.New(os.Stderr, 16, formatMemoryDiagnostic)
	}
	reports := m.reports
	m.mu.Unlock()
	reports.Submit(failure)
}

// DiagnosticStats counts rejected reports and formatter/output failures.
// Neither counter measures business errors or discarded business tasks.
func (m *MemoryQueue) DiagnosticStats() (dropped, failures uint64) {
	m.mu.Lock()
	reports := m.reports
	m.mu.Unlock()
	dropped = m.reportsRejected.Load()
	if reports != nil {
		rejected, failed := reports.Stats()
		dropped += rejected
		failures = failed
	}
	return
}

func (m *MemoryQueue) closeReports(deadline time.Time) error {
	m.mu.Lock()
	m.reportsClosed = true
	reports := m.reports
	m.mu.Unlock()
	if reports == nil {
		return nil
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	if err := reports.Close(ctx); err != nil {
		return errors.Join(ErrDiagnosticShutdown, err)
	}
	return nil
}
