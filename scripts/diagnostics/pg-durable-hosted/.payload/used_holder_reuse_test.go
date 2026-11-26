package advispair

import (
	"slices"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/pg/migrate"
)

// benchmarkUsedHolderFirstReuse measures the cache behavior of the second
// migration on a physical holder that was dialed after public pool use.
func benchmarkUsedHolderFirstReuse(b *testing.B) {
	f := newFixture(b)
	dir, before := prepareAppliedFirstQuery(f)
	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		benchmarkUsedHolderReuse(b, f, dir, before)
		b.StartTimer()
	}
	assertNoFirstQueryJournal(f)
}

func benchmarkUsedHolderReuse(b *testing.B, f fixture, dir string, before migrationState) {
	db := f.open()
	defer db.Close()
	pid, err := usedPoolPID(f, db)
	if err != nil {
		b.Fatal(err)
	}
	runner := migrate.NewFromDB(db, dir)
	names, err := runner.Up(f.ctx)
	verifyAppliedFirstQuery(f, before, names, err)
	initial := f.activity()
	if initial.held != 0 || initial.unlocked == 0 || initial.unlocked == pid || len(initial.sessions) != 2 {
		b.Fatalf("first operation did not release its independent holder: %+v, pool PID %d", initial, pid)
	}
	b.StartTimer()
	names, err = runner.Up(f.ctx)
	b.StopTimer()
	verifyAppliedFirstQuery(f, before, names, err)
	got := f.activity()
	if got.held != 0 || got.unlocked != initial.unlocked || !slices.Equal(got.sessions, initial.sessions) {
		b.Fatalf("first reuse replaced a session: %+v → %+v", initial, got)
	}
	db.Close()
	f.assertClosed()
}
