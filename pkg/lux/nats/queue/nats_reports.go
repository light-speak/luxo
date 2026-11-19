package queue

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/light-speak/luxo/internal/diagnostics"
)

// queueDiagnostic keeps contextual formatting on the reporting worker.
type queueDiagnostic struct {
	queue     string
	operation string
	cause     error
}

func (d queueDiagnostic) Error() string {
	message := d.operation
	if d.queue != "" {
		message = d.queue + ": " + message
	}
	if d.cause != nil {
		message += ": " + d.cause.Error()
	}
	return message
}

// Only diagnostic records may be dropped. Task delivery and its cancellation
// cause never depend on successful log output.
type renewalReports = diagnostics.Reporter

func newRenewalReports(writer io.Writer, capacity int) *renewalReports {
	return diagnostics.New(writer, capacity, formatQueueDiagnostic)
}

func formatQueueDiagnostic(failure error) string {
	prefix := "queue: progress renewal failed; delivery may repeat: "
	if _, ok := failure.(queueDiagnostic); ok {
		prefix = "queue: "
	}
	return diagnostics.Line(prefix, failure.Error())
}

func (n *NATSQueue) reportRenewal(err error) {
	n.mu.Lock()
	if n.reports == nil {
		if n.closed {
			n.reportsRejected.Add(1)
			n.mu.Unlock()
			return
		}
		n.reports = newRenewalReports(os.Stderr, 16)
	}
	reports := n.reports
	n.mu.Unlock()
	reports.Submit(err)
}

// RenewalReportStats counts rejected diagnostic records and failed writes.
// These counters do not count or discard business tasks.
func (n *NATSQueue) RenewalReportStats() (dropped, failures uint64) {
	n.mu.Lock()
	reports := n.reports
	n.mu.Unlock()
	dropped = n.reportsRejected.Load()
	if reports != nil {
		rejected, failed := reports.Stats()
		dropped += rejected
		failures = failed
	}
	return
}

func (n *NATSQueue) closeReports(deadline time.Time) {
	n.mu.Lock()
	reports := n.reports
	n.mu.Unlock()
	if reports == nil {
		return
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	if err := reports.Close(ctx); err != nil {
		n.closeErr = errors.Join(n.closeErr, err)
	}
}
