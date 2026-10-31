package pg

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

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
