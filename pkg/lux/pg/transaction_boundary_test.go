package pg

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type lifecycleTransaction struct {
	pgx.Tx
	commits, rollbacks int
}

func (tx *lifecycleTransaction) Begin(context.Context) (pgx.Tx, error) { return tx, nil }
func (tx *lifecycleTransaction) Commit(context.Context) error          { tx.commits++; return nil }
func (tx *lifecycleTransaction) Rollback(context.Context) error        { tx.rollbacks++; return nil }

func TestTransactionAbnormalExitRollsBack(t *testing.T) {
	for _, mode := range []string{"panic", "goexit"} {
		t.Run(mode, func(t *testing.T) {
			tx := &lifecycleTransaction{}
			db := &DB{conn: tx}
			done := make(chan struct{})
			var recovered any
			go func() {
				defer close(done)
				defer func() { recovered = recover() }()
				_ = db.Tx(context.Background(), func(*DB) error {
					if mode == "panic" {
						panic("transaction failure")
					}
					runtime.Goexit()
					return nil
				})
			}()
			<-done
			if tx.commits != 0 || tx.rollbacks != 1 {
				t.Fatalf("commits=%d rollbacks=%d; want 0/1", tx.commits, tx.rollbacks)
			}
			if mode == "panic" && recovered != "transaction failure" {
				t.Fatalf("panic changed: %v", recovered)
			}
		})
	}
}

func TestTransactionNormalReturnFinalization(t *testing.T) {
	callbackErr := errors.New("callback failed")
	for _, test := range []struct {
		name               string
		err                error
		commits, rollbacks int
	}{{"commit", nil, 1, 0}, {"rollback", callbackErr, 0, 1}} {
		t.Run(test.name, func(t *testing.T) {
			tx := &lifecycleTransaction{}
			db := &DB{conn: tx}
			err := db.Tx(context.Background(), func(inner *DB) error {
				if inner.conn != tx {
					t.Fatal("callback is not bound to transaction")
				}
				return test.err
			})
			if err != test.err || tx.commits != test.commits || tx.rollbacks != test.rollbacks {
				t.Fatalf("error=%v commits=%d rollbacks=%d", err, tx.commits, tx.rollbacks)
			}
		})
	}
}

func TestTransactionGoexitDiscardsDatabaseWrites(t *testing.T) {
	for _, nested := range []bool{false, true} {
		name := "direct"
		if nested {
			name = "nested"
		}
		t.Run(name, func(t *testing.T) {
			db := testDB(t)
			ctx := t.Context()
			done := make(chan struct{})
			var operationErr error
			returned := false
			go func() {
				defer close(done)
				operationErr = db.Tx(ctx, func(tx *DB) error {
					if err := insertTransactionUser(ctx, tx, "outer"); err != nil {
						return err
					}
					if nested {
						return tx.Tx(ctx, func(inner *DB) error {
							if err := insertTransactionUser(ctx, inner, "inner"); err != nil {
								return err
							}
							runtime.Goexit()
							return nil
						})
					}
					runtime.Goexit()
					return nil
				})
				returned = true
			}()
			<-done
			if returned || operationErr != nil {
				t.Fatalf("callback returned: %v, error: %v", returned, operationErr)
			}
			count, err := db.QueryScalar[int64](ctx, "SELECT COUNT(*) FROM users")
			if err != nil || count != 0 {
				t.Fatalf("writes survived Goexit: count=%d error=%v", count, err)
			}
			if acquired := db.Pool().Stat().AcquiredConns(); acquired != 0 {
				t.Fatalf("leaked %d connections", acquired)
			}
			if err := db.Tx(ctx, func(tx *DB) error { return insertTransactionUser(ctx, tx, "after") }); err != nil {
				t.Fatal(err)
			}
			count, err = db.QueryScalar[int64](ctx, "SELECT COUNT(*) FROM users")
			if err != nil || count != 1 {
				t.Fatalf("subsequent commit: count=%d error=%v", count, err)
			}
		})
	}
}

func TestNestedTransactionUsesCurrentConnection(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	abort := errors.New("abort outer transaction")
	err := db.Tx(ctx, func(outer *DB) error {
		if err := insertTransactionUser(ctx, outer, "outer-visible"); err != nil {
			return err
		}
		if err := outer.Tx(ctx, func(inner *DB) error {
			count, err := inner.QueryScalar[int64](ctx, "SELECT COUNT(*) FROM users WHERE name='outer-visible'")
			if err != nil {
				return err
			}
			if count != 1 {
				t.Errorf("nested transaction cannot see outer write: count=%d", count)
			}
			return insertTransactionUser(ctx, inner, "inner-committed")
		}); err != nil {
			return err
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatalf("transaction error = %v", err)
	}
	count, err := db.QueryScalar[int64](ctx, "SELECT COUNT(*) FROM users")
	if err != nil || count != 0 {
		t.Fatalf("outer rollback retained writes: count=%d, err=%v", count, err)
	}
}

func TestNestedTransactionRollbackKeepsOuterUsable(t *testing.T) {
	db := testDB(t)
	ctx := t.Context()
	abort := errors.New("abort savepoint")
	err := db.Tx(ctx, func(outer *DB) error {
		if err := insertTransactionUser(ctx, outer, "before"); err != nil {
			return err
		}
		err := outer.Tx(ctx, func(inner *DB) error {
			if err := insertTransactionUser(ctx, inner, "rolled-back"); err != nil {
				return err
			}
			return abort
		})
		if !errors.Is(err, abort) {
			t.Errorf("savepoint error = %v, want %v", err, abort)
			return err
		}
		return insertTransactionUser(ctx, outer, "after")
	})
	if err != nil {
		t.Fatal(err)
	}
	count, err := db.QueryScalar[int64](ctx, "SELECT COUNT(*) FROM users WHERE name IN ('before','after')")
	if err != nil || count != 2 {
		t.Fatalf("outer commit lost writes: count=%d, err=%v", count, err)
	}
	count, err = db.QueryScalar[int64](ctx, "SELECT COUNT(*) FROM users WHERE name='rolled-back'")
	if err != nil || count != 0 {
		t.Fatalf("savepoint rollback retained a write: count=%d, err=%v", count, err)
	}
}

func insertTransactionUser(ctx context.Context, db *DB, name string) error {
	_, err := Exec(ctx, db, "INSERT INTO users(id,name,email) VALUES($1,$2,$3)", uuid.New(), name, name+"@test.invalid")
	return err
}
