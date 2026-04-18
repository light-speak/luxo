package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// mockDB implements DB for unit testing error paths without a real database.
type mockDB struct {
	execErr     error
	queryResult *mockRows
	queryErr    error
	queryRowVal Row
	beginTx     Tx
	beginErr    error
	lockErr     error
	closed      bool
}

func (m *mockDB) Exec(ctx context.Context, sql string, args ...any) error { return m.execErr }
func (m *mockDB) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	if m.queryErr != nil {
		return nil, m.queryErr
	}
	return m.queryResult, nil
}
func (m *mockDB) QueryRow(ctx context.Context, sql string, args ...any) Row { return m.queryRowVal }
func (m *mockDB) Begin(ctx context.Context) (Tx, error) {
	if m.beginErr != nil {
		return nil, m.beginErr
	}
	return m.beginTx, nil
}
func (m *mockDB) AcquireLock(ctx context.Context) error { return m.lockErr }
func (m *mockDB) ReleaseLock(ctx context.Context)       {}
func (m *mockDB) Close()                                { m.closed = true }

// mockRows simulates query results.
type mockRows struct {
	data    [][]any
	pos     int
	scanErr error
}

func (r *mockRows) Next() bool {
	if r.pos < len(r.data) {
		r.pos++
		return true
	}
	return false
}

func (r *mockRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	row := r.data[r.pos-1]
	for i, d := range dest {
		if i < len(row) {
			switch v := d.(type) {
			case *string:
				*v = row[i].(string)
			case *int64:
				*v = row[i].(int64)
			}
		}
	}
	return nil
}

func (r *mockRows) Close() {}

// mockTx simulates a transaction.
type mockTx struct {
	execErr   error
	commitErr error
}

func (t *mockTx) Exec(ctx context.Context, sql string, args ...any) error { return t.execErr }
func (t *mockTx) Commit(ctx context.Context) error                        { return t.commitErr }
func (t *mockTx) Rollback(ctx context.Context) error                      { return nil }

// mockRow simulates a single row result.
type mockRow struct {
	scanErr error
}

func (r *mockRow) Scan(dest ...any) error { return r.scanErr }

// --- Tests ---

func TestNewFromDBAndClose(t *testing.T) {
	db := &mockDB{}
	r := NewFromDB(db, "/tmp")
	r.Close()
	if !db.closed {
		t.Error("Close should close the DB")
	}
}

func TestInitError(t *testing.T) {
	db := &mockDB{execErr: fmt.Errorf("init fail")}
	r := NewFromDB(db, t.TempDir())
	err := r.Init(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestUpAcquireLockError(t *testing.T) {
	db := &mockDB{lockErr: fmt.Errorf("lock fail")}
	r := NewFromDB(db, t.TempDir())
	_, err := r.Up(context.Background())
	if err == nil || err.Error() != "acquire lock: lock fail" {
		t.Fatalf("expected lock error, got: %v", err)
	}
}

func TestUpVerifyChecksumQueryError(t *testing.T) {
	// Init succeeds, lock succeeds, verifyChecksums query fails
	db := &mockDB{queryErr: fmt.Errorf("query fail")}
	r := NewFromDB(db, t.TempDir())
	_, err := r.Up(context.Background())
	if err == nil {
		t.Fatal("expected error from verifyChecksums")
	}
}

func TestDownAcquireLockError(t *testing.T) {
	db := &mockDB{lockErr: fmt.Errorf("lock fail")}
	r := NewFromDB(db, t.TempDir())
	_, err := r.Down(context.Background(), 1)
	if err == nil {
		t.Fatal("expected lock error")
	}
}

func TestDownInitError(t *testing.T) {
	db := &mockDB{execErr: fmt.Errorf("init fail")}
	r := NewFromDB(db, t.TempDir())
	_, err := r.Down(context.Background(), 1)
	if err == nil {
		t.Fatal("expected init error")
	}
}

func TestDownQueryError(t *testing.T) {
	db := &mockDB{queryErr: fmt.Errorf("query fail")}
	r := NewFromDB(db, t.TempDir())
	_, err := r.Down(context.Background(), 1)
	if err == nil {
		t.Fatal("expected query error")
	}
}

func TestDownScanError(t *testing.T) {
	rows := &mockRows{data: [][]any{{"migration1"}}, scanErr: fmt.Errorf("scan fail")}
	db := &mockDB{queryResult: rows}
	r := NewFromDB(db, t.TempDir())
	_, err := r.Down(context.Background(), 1)
	if err == nil {
		t.Fatal("expected scan error")
	}
}

func TestStatusInitError(t *testing.T) {
	db := &mockDB{execErr: fmt.Errorf("init fail")}
	r := NewFromDB(db, t.TempDir())
	_, err := r.Status(context.Background())
	if err == nil {
		t.Fatal("expected init error")
	}
}

func TestStatusQueryError(t *testing.T) {
	db := &mockDB{queryErr: fmt.Errorf("query fail")}
	r := NewFromDB(db, t.TempDir())
	_, err := r.Status(context.Background())
	if err == nil {
		t.Fatal("expected query error")
	}
}

func TestDryRunInitError(t *testing.T) {
	db := &mockDB{execErr: fmt.Errorf("init fail")}
	r := NewFromDB(db, t.TempDir())
	_, _, err := r.DryRun(context.Background())
	if err == nil {
		t.Fatal("expected init error")
	}
}

func TestDryRunAppliedSetError(t *testing.T) {
	db := &mockDB{queryErr: fmt.Errorf("query fail")}
	r := NewFromDB(db, t.TempDir())
	_, _, err := r.DryRun(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDryRunParseSectionError(t *testing.T) {
	dir := t.TempDir()
	// Write a file without UP section
	os.WriteFile(filepath.Join(dir, "001_bad.sql"), []byte("no sections"), 0644)
	rows := &mockRows{data: [][]any{}} // empty applied
	db := &mockDB{queryResult: rows}
	r := NewFromDB(db, dir)
	_, _, err := r.DryRun(context.Background())
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestVerifyInitError(t *testing.T) {
	db := &mockDB{execErr: fmt.Errorf("init fail")}
	r := NewFromDB(db, t.TempDir())
	_, err := r.Verify(context.Background())
	if err == nil {
		t.Fatal("expected init error")
	}
}

func TestExecMigrationBeginError(t *testing.T) {
	db := &mockDB{beginErr: fmt.Errorf("begin fail")}
	r := NewFromDB(db, t.TempDir())
	err := r.execMigration(context.Background(), "test", "SELECT 1;", "abc")
	if err == nil {
		t.Fatal("expected begin error")
	}
}

func TestExecMigrationNonTxError(t *testing.T) {
	db := &mockDB{execErr: fmt.Errorf("exec fail")}
	r := NewFromDB(db, t.TempDir())
	// SQL with CONCURRENTLY triggers non-tx path
	err := r.execMigration(context.Background(), "test", "CREATE INDEX CONCURRENTLY idx ON t (c);", "abc")
	if err == nil {
		t.Fatal("expected non-tx exec error")
	}
}

func TestExecMigrationCommitError(t *testing.T) {
	tx := &mockTx{commitErr: fmt.Errorf("commit fail")}
	db := &mockDB{beginTx: tx}
	r := NewFromDB(db, t.TempDir())
	err := r.execMigration(context.Background(), "test", "SELECT 1;", "abc")
	if err == nil {
		t.Fatal("expected commit error")
	}
}

func TestExecMigrationTxExecError(t *testing.T) {
	tx := &mockTx{execErr: fmt.Errorf("tx exec fail")}
	db := &mockDB{beginTx: tx}
	r := NewFromDB(db, t.TempDir())
	err := r.execMigration(context.Background(), "test", "SELECT 1;", "abc")
	if err == nil {
		t.Fatal("expected tx exec error")
	}
}

func TestExecRollbackBeginError(t *testing.T) {
	db := &mockDB{beginErr: fmt.Errorf("begin fail")}
	r := NewFromDB(db, t.TempDir())
	err := r.execRollback(context.Background(), "test", "SELECT 1;")
	if err == nil {
		t.Fatal("expected begin error")
	}
}

func TestExecRollbackNonTxError(t *testing.T) {
	db := &mockDB{execErr: fmt.Errorf("exec fail")}
	r := NewFromDB(db, t.TempDir())
	err := r.execRollback(context.Background(), "test", "DROP INDEX CONCURRENTLY idx;")
	if err == nil {
		t.Fatal("expected non-tx exec error")
	}
}

func TestExecRollbackTxExecError(t *testing.T) {
	tx := &mockTx{execErr: fmt.Errorf("tx exec fail")}
	db := &mockDB{beginTx: tx}
	r := NewFromDB(db, t.TempDir())
	err := r.execRollback(context.Background(), "test", "SELECT 1;")
	if err == nil {
		t.Fatal("expected tx exec error")
	}
}

func TestExecRollbackCommitError(t *testing.T) {
	tx := &mockTx{commitErr: fmt.Errorf("commit fail")}
	db := &mockDB{beginTx: tx}
	r := NewFromDB(db, t.TempDir())
	err := r.execRollback(context.Background(), "test", "SELECT 1;")
	if err == nil {
		t.Fatal("expected commit error")
	}
}

func TestSplitConcurrentlyAllConcurrent(t *testing.T) {
	txSQL, nonTx := splitConcurrently("CREATE INDEX CONCURRENTLY a ON t(c); CREATE INDEX CONCURRENTLY b ON t(d);")
	if txSQL != "" {
		t.Errorf("txSQL should be empty, got: %q", txSQL)
	}
	if len(nonTx) != 2 {
		t.Errorf("expected 2 non-tx, got %d", len(nonTx))
	}
}

func TestVerifyChecksumsScanError(t *testing.T) {
	rows := &mockRows{data: [][]any{{"m1", "abc"}}, scanErr: fmt.Errorf("scan fail")}
	db := &mockDB{queryResult: rows}
	r := NewFromDB(db, t.TempDir())
	err := r.verifyChecksums(context.Background())
	if err == nil {
		t.Fatal("expected scan error")
	}
}

func TestAppliedMapScanError(t *testing.T) {
	rows := &mockRows{data: [][]any{{"m1"}}, scanErr: fmt.Errorf("scan fail")}
	db := &mockDB{queryResult: rows}
	r := NewFromDB(db, t.TempDir())
	_, err := r.appliedMap(context.Background())
	if err == nil {
		t.Fatal("expected scan error")
	}
}

func TestListTablesScanError(t *testing.T) {
	rows := &mockRows{data: [][]any{{"table1"}}, scanErr: fmt.Errorf("scan fail")}
	db := &mockDB{queryResult: rows}
	r := NewFromDB(db, t.TempDir())
	_, err := r.listTables(context.Background())
	if err == nil {
		t.Fatal("expected scan error")
	}
}

func TestListTablesQueryError(t *testing.T) {
	db := &mockDB{queryErr: fmt.Errorf("query fail")}
	r := NewFromDB(db, t.TempDir())
	_, err := r.listTables(context.Background())
	if err == nil {
		t.Fatal("expected query error")
	}
}

func TestCheckPendingQueryError(t *testing.T) {
	db := &mockDB{queryErr: fmt.Errorf("query fail")}
	r := NewFromDB(db, t.TempDir())
	drifts := r.checkPending(context.Background())
	// checkPending swallows errors and returns nil
	if drifts != nil {
		t.Errorf("expected nil on error, got: %v", drifts)
	}
}

func TestLoadExpectedTablesInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".state.json"), []byte("{invalid"), 0644)
	db := &mockDB{}
	r := NewFromDB(db, dir)
	tables := r.loadExpectedTables()
	if tables != nil {
		t.Errorf("expected nil on invalid JSON, got: %v", tables)
	}
}

func TestLoadExpectedTablesNoFile(t *testing.T) {
	db := &mockDB{}
	r := NewFromDB(db, t.TempDir())
	tables := r.loadExpectedTables()
	if tables != nil {
		t.Errorf("expected nil when no state file, got: %v", tables)
	}
}

func TestLoadExpectedTablesValid(t *testing.T) {
	dir := t.TempDir()
	state := map[string]any{
		"models": map[string]any{
			"User": map[string]any{"table": "users"},
			"Post": map[string]any{"table": "posts"},
		},
	}
	data, _ := json.Marshal(state)
	os.WriteFile(filepath.Join(dir, ".state.json"), data, 0644)
	db := &mockDB{}
	r := NewFromDB(db, dir)
	tables := r.loadExpectedTables()
	if len(tables) != 2 {
		t.Errorf("expected 2 tables, got %d: %v", len(tables), tables)
	}
}

func TestUpParseSectionError(t *testing.T) {
	dir := t.TempDir()
	emptyRows := &mockRows{data: [][]any{}}
	db := &mockDB{queryResult: emptyRows}
	r := NewFromDB(db, dir)
	os.WriteFile(filepath.Join(dir, "001_bad.sql"), []byte("no section"), 0644)
	_, err := r.Up(context.Background())
	if err == nil {
		t.Fatal("expected parse error in Up")
	}
}

func TestExecMigrationEmptyTxSQL(t *testing.T) {
	// All CONCURRENTLY - txSQL is empty, only non-tx runs
	tx := &mockTx{}
	db := &mockDB{beginTx: tx}
	r := NewFromDB(db, t.TempDir())
	err := r.execMigration(context.Background(), "test", "CREATE INDEX CONCURRENTLY idx ON t(c);", "abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecRollbackEmptyTxSQL(t *testing.T) {
	tx := &mockTx{}
	db := &mockDB{beginTx: tx}
	r := NewFromDB(db, t.TempDir())
	err := r.execRollback(context.Background(), "test", "DROP INDEX CONCURRENTLY idx;")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecMigrationInsertError(t *testing.T) {
	// tx.Exec succeeds first (SQL), fails second (INSERT into _migrations)
	callCount := 0
	tx := &mockTx{}
	// Override Exec to fail on second call
	origExec := tx.execErr
	_ = origExec
	failOnSecond := &countingTx{failAt: 2}
	db := &mockDB{beginTx: failOnSecond}
	r := NewFromDB(db, t.TempDir())
	_ = callCount
	err := r.execMigration(context.Background(), "test", "SELECT 1;", "abc")
	if err == nil {
		t.Fatal("expected INSERT error")
	}
}

func TestExecRollbackDeleteError(t *testing.T) {
	failOnSecond := &countingTx{failAt: 2}
	db := &mockDB{beginTx: failOnSecond}
	r := NewFromDB(db, t.TempDir())
	err := r.execRollback(context.Background(), "test", "SELECT 1;")
	if err == nil {
		t.Fatal("expected DELETE error")
	}
}

// countingTx fails on the Nth Exec call.
type countingTx struct {
	count  int
	failAt int
}

func (t *countingTx) Exec(ctx context.Context, sql string, args ...any) error {
	t.count++
	if t.count >= t.failAt {
		return fmt.Errorf("exec #%d failed", t.count)
	}
	return nil
}
func (t *countingTx) Commit(ctx context.Context) error   { return nil }
func (t *countingTx) Rollback(ctx context.Context) error { return nil }

func TestPendingFilesGlobError(t *testing.T) {
	// Use invalid glob pattern via a dir path with bad characters
	db := &mockDB{}
	r := NewFromDB(db, "/nonexistent/[invalid")
	_, err := r.pendingFiles(map[string]bool{})
	// filepath.Glob returns error for bad patterns
	if err == nil {
		t.Fatal("expected glob error")
	}
}

func TestNewSuccess(t *testing.T) {
	// Test the New function success path with real DB
	if os.Getenv("DATABASE_USER") == "" {
		t.Skip("DATABASE_USER not set")
	}
	ctx := context.Background()
	r, err := New(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer r.Close()
}

func TestStatusGlobError(t *testing.T) {
	emptyRows := &mockRows{data: [][]any{}}
	db := &mockDB{queryResult: emptyRows}
	r := NewFromDB(db, "/nonexistent/[invalid")
	_, err := r.Status(context.Background())
	if err == nil {
		t.Fatal("expected glob error")
	}
}
