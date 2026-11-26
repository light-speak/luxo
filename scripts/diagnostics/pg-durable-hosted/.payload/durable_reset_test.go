package migratepair

import (
	"strings"

	"github.com/light-speak/luxo/pkg/lux/pg/migrate"
)

// Only the durable arm has a committed Down receipt. Acknowledge it outside
// the timer, as the operator would, before resetting the same migration.
func init() {
	completeStandalone = func(f fixture, sample scenario) {
		if !sample.index {
			return
		}
		attempt, err := f.runner.PendingAttempt(f.ctx)
		if err != nil {
			f.t.Fatal(err)
		}
		if sample.operation == "up" {
			if attempt != nil {
				f.t.Fatalf("standalone Up left an attempt: %+v", attempt)
			}
			return
		}
		if attempt == nil || !attempt.HistoryCommitted || attempt.Name != first ||
			!strings.EqualFold(attempt.Direction, "DOWN") {
			f.t.Fatalf("standalone Down did not retain a committed receipt: %+v", attempt)
		}
		if err := f.runner.Reconcile(f.ctx, attempt.ID, migrate.AcknowledgeMigration); err != nil {
			f.t.Fatal(err)
		}
		if pending, err := f.runner.PendingAttempt(f.ctx); err != nil || pending != nil {
			f.t.Fatalf("standalone acknowledgement failed to clear its receipt: %+v %v", pending, err)
		}
	}
}
