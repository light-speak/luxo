package lux

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestCmpCondToSQL(t *testing.T) {
	tests := []struct {
		name      string
		cond      Condition
		argOffset int
		wantSQL   string
		wantArgs  []any
	}{
		{"eq int", NewIntField("id").Eq(42), 1, "id = $1", []any{int64(42)}},
		{"neq int", NewIntField("id").Neq(5), 3, "id != $3", []any{int64(5)}},
		{"gt int", NewIntField("age").Gt(18), 1, "age > $1", []any{int64(18)}},
		{"gte int", NewIntField("age").Gte(18), 1, "age >= $1", []any{int64(18)}},
		{"lt int", NewIntField("age").Lt(65), 1, "age < $1", []any{int64(65)}},
		{"lte int", NewIntField("age").Lte(65), 1, "age <= $1", []any{int64(65)}},
		{"eq float", NewFloatField("price").Eq(9.99), 1, "price = $1", []any{9.99}},
		{"neq float", NewFloatField("price").Neq(0), 1, "price != $1", []any{float64(0)}},
		{"gt float", NewFloatField("price").Gt(10), 1, "price > $1", []any{float64(10)}},
		{"gte float", NewFloatField("price").Gte(10), 1, "price >= $1", []any{float64(10)}},
		{"lt float", NewFloatField("price").Lt(100), 1, "price < $1", []any{float64(100)}},
		{"lte float", NewFloatField("price").Lte(100), 1, "price <= $1", []any{float64(100)}},
		{"eq string", NewStringField("name").Eq("lin"), 2, "name = $2", []any{"lin"}},
		{"neq string", NewStringField("name").Neq("x"), 1, "name != $1", []any{"x"}},
		{"like string", NewStringField("name").Like("%lin%"), 1, "name LIKE $1", []any{"%lin%"}},
		{"eq bool", NewBoolField("active").Eq(true), 1, "active = $1", []any{true}},
		{"isTrue", NewBoolField("active").IsTrue(), 1, "active = $1", []any{true}},
		{"isFalse", NewBoolField("active").IsFalse(), 1, "active = $1", []any{false}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql, args := tt.cond.ToSQL(tt.argOffset)
			if sql != tt.wantSQL {
				t.Errorf("SQL = %q, want %q", sql, tt.wantSQL)
			}
			if len(args) != len(tt.wantArgs) {
				t.Fatalf("args len = %d, want %d", len(args), len(tt.wantArgs))
			}
			for i, a := range args {
				if a != tt.wantArgs[i] {
					t.Errorf("args[%d] = %v, want %v", i, a, tt.wantArgs[i])
				}
			}
		})
	}
}

func TestTimeFieldConditions(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		cond    Condition
		wantSQL string
	}{
		{"eq", NewTimeField("created_at").Eq(now), "created_at = $1"},
		{"before", NewTimeField("created_at").Before(now), "created_at < $1"},
		{"after", NewTimeField("created_at").After(now), "created_at > $1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql, args := tt.cond.ToSQL(1)
			if sql != tt.wantSQL {
				t.Errorf("SQL = %q, want %q", sql, tt.wantSQL)
			}
			if len(args) != 1 {
				t.Fatalf("args len = %d, want 1", len(args))
			}
			if args[0] != now {
				t.Errorf("args[0] = %v, want %v", args[0], now)
			}
		})
	}
}

func TestInCondToSQL(t *testing.T) {
	t.Run("int in", func(t *testing.T) {
		cond := NewIntField("id").In(1, 2, 3)
		sql, args := cond.ToSQL(5)
		want := "id IN ($5, $6, $7)"
		if sql != want {
			t.Errorf("SQL = %q, want %q", sql, want)
		}
		if len(args) != 3 {
			t.Fatalf("args len = %d, want 3", len(args))
		}
	})

	t.Run("string in", func(t *testing.T) {
		cond := NewStringField("role").In("USER", "ADMIN")
		sql, args := cond.ToSQL(1)
		want := "role IN ($1, $2)"
		if sql != want {
			t.Errorf("SQL = %q, want %q", sql, want)
		}
		if len(args) != 2 {
			t.Fatalf("args len = %d, want 2", len(args))
		}
	})
}

func TestUUIDFieldConditions(t *testing.T) {
	id1 := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	id2 := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")

	t.Run("eq", func(t *testing.T) {
		sql, args := NewUUIDField("id").Eq(id1).ToSQL(1)
		if sql != "id = $1" {
			t.Errorf("SQL = %q", sql)
		}
		if args[0] != id1 {
			t.Errorf("args[0] = %v", args[0])
		}
	})

	t.Run("neq", func(t *testing.T) {
		sql, _ := NewUUIDField("id").Neq(id1).ToSQL(1)
		if sql != "id != $1" {
			t.Errorf("SQL = %q", sql)
		}
	})

	t.Run("in", func(t *testing.T) {
		sql, args := NewUUIDField("id").In(id1, id2).ToSQL(1)
		if sql != "id IN ($1, $2)" {
			t.Errorf("SQL = %q", sql)
		}
		if len(args) != 2 {
			t.Errorf("args len = %d", len(args))
		}
	})
}

func TestDecimalFieldConditions(t *testing.T) {
	v := decimal.NewFromFloat(99.99)

	tests := []struct {
		name    string
		cond    Condition
		wantSQL string
	}{
		{"eq", NewDecimalField("price").Eq(v), "price = $1"},
		{"neq", NewDecimalField("price").Neq(v), "price != $1"},
		{"gt", NewDecimalField("price").Gt(v), "price > $1"},
		{"gte", NewDecimalField("price").Gte(v), "price >= $1"},
		{"lt", NewDecimalField("price").Lt(v), "price < $1"},
		{"lte", NewDecimalField("price").Lte(v), "price <= $1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql, args := tt.cond.ToSQL(1)
			if sql != tt.wantSQL {
				t.Errorf("SQL = %q, want %q", sql, tt.wantSQL)
			}
			if len(args) != 1 {
				t.Fatalf("args len = %d", len(args))
			}
			if !args[0].(decimal.Decimal).Equal(v) {
				t.Errorf("args[0] = %v", args[0])
			}
		})
	}
}

func TestNullCondToSQL(t *testing.T) {
	t.Run("is null", func(t *testing.T) {
		sql, args := NewTimeField("deleted_at").IsNull().ToSQL(1)
		if sql != "deleted_at IS NULL" {
			t.Errorf("SQL = %q", sql)
		}
		if len(args) != 0 {
			t.Errorf("args should be empty")
		}
	})

	t.Run("is not null", func(t *testing.T) {
		sql, args := NewTimeField("deleted_at").IsNotNull().ToSQL(1)
		if sql != "deleted_at IS NOT NULL" {
			t.Errorf("SQL = %q", sql)
		}
		if len(args) != 0 {
			t.Errorf("args should be empty")
		}
	})
}

func TestNullCondArgOffsetUnused(t *testing.T) {
	// IS NULL produces no args, so argOffset doesn't matter
	conds := []Condition{
		NewTimeField("deleted_at").IsNull(),
		NewIntField("id").Eq(1),
	}
	sql, args := BuildSelectSQL("users", nil, conds, nil, 0, 0)
	want := "SELECT * FROM users WHERE deleted_at IS NULL AND id = $1"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 1 {
		t.Errorf("args len = %d, want 1", len(args))
	}
}

func TestNewFieldConstructors(t *testing.T) {
	// Verify constructors create usable fields.
	intF := NewIntField("id")
	sql, _ := intF.Eq(1).ToSQL(1)
	if sql != "id = $1" {
		t.Errorf("IntField: %q", sql)
	}

	floatF := NewFloatField("price")
	sql, _ = floatF.Eq(1.5).ToSQL(1)
	if sql != "price = $1" {
		t.Errorf("FloatField: %q", sql)
	}

	strF := NewStringField("name")
	sql, _ = strF.Eq("x").ToSQL(1)
	if sql != "name = $1" {
		t.Errorf("StringField: %q", sql)
	}

	boolF := NewBoolField("active")
	sql, _ = boolF.Eq(true).ToSQL(1)
	if sql != "active = $1" {
		t.Errorf("BoolField: %q", sql)
	}

	timeF := NewTimeField("ts")
	sql, _ = timeF.Eq(time.Now()).ToSQL(1)
	if sql != "ts = $1" {
		t.Errorf("TimeField: %q", sql)
	}
}

func TestIntFieldFilterOp(t *testing.T) {
	f := NewIntField("age")
	tests := []struct {
		op, val, wantSQL string
	}{
		{"eq", "25", "age = $1"},
		{"ne", "25", "age != $1"},
		{"gt", "18", "age > $1"},
		{"gte", "18", "age >= $1"},
		{"lt", "100", "age < $1"},
		{"lte", "100", "age <= $1"},
		{"unknown", "0", "age = $1"},
	}
	for _, tt := range tests {
		sql, _ := f.FilterOp(tt.op, tt.val).ToSQL(1)
		if sql != tt.wantSQL {
			t.Errorf("IntField.FilterOp(%q, %q) = %q, want %q", tt.op, tt.val, sql, tt.wantSQL)
		}
	}
}

func TestStringFieldFilterOp(t *testing.T) {
	f := NewStringField("name")
	tests := []struct {
		op, val, wantSQL string
	}{
		{"eq", "lin", "name = $1"},
		{"ne", "lin", "name != $1"},
		{"contains", "lin", "name LIKE $1"},
		{"startswith", "li", "name LIKE $1"},
		{"endswith", "in", "name LIKE $1"},
		{"unknown", "x", "name = $1"},
	}
	for _, tt := range tests {
		sql, _ := f.FilterOp(tt.op, tt.val).ToSQL(1)
		if sql != tt.wantSQL {
			t.Errorf("StringField.FilterOp(%q, %q) = %q, want %q", tt.op, tt.val, sql, tt.wantSQL)
		}
	}
}

func TestStringFieldFilterOpContainsValue(t *testing.T) {
	f := NewStringField("name")
	_, args := f.FilterOp("contains", "lin").ToSQL(1)
	if args[0] != "%lin%" {
		t.Errorf("contains value = %q, want %%lin%%", args[0])
	}
	_, args = f.FilterOp("startswith", "li").ToSQL(1)
	if args[0] != "li%" {
		t.Errorf("startswith value = %q, want li%%", args[0])
	}
	_, args = f.FilterOp("endswith", "in").ToSQL(1)
	if args[0] != "%in" {
		t.Errorf("endswith value = %q, want %%in", args[0])
	}
}

func TestBoolFieldFilterOp(t *testing.T) {
	f := NewBoolField("active")
	sql, args := f.FilterOp("eq", "true").ToSQL(1)
	if sql != "active = $1" || args[0] != true {
		t.Errorf("BoolField true: sql=%q args=%v", sql, args)
	}
	sql, args = f.FilterOp("eq", "false").ToSQL(1)
	if sql != "active = $1" || args[0] != false {
		t.Errorf("BoolField false: sql=%q args=%v", sql, args)
	}
	sql, args = f.FilterOp("eq", "1").ToSQL(1)
	if args[0] != true {
		t.Errorf("BoolField '1' should be true")
	}
}

func TestBetweenCond(t *testing.T) {
	t.Run("int between", func(t *testing.T) {
		sql, args := NewIntField("age").Between(18, 65).ToSQL(1)
		if sql != "age BETWEEN $1 AND $2" {
			t.Errorf("SQL = %q", sql)
		}
		if len(args) != 2 || args[0] != int64(18) || args[1] != int64(65) {
			t.Errorf("args = %v", args)
		}
	})

	t.Run("float between", func(t *testing.T) {
		sql, args := NewFloatField("price").Between(10.0, 99.99).ToSQL(3)
		if sql != "price BETWEEN $3 AND $4" {
			t.Errorf("SQL = %q", sql)
		}
		if len(args) != 2 {
			t.Errorf("args = %v", args)
		}
	})

	t.Run("time between", func(t *testing.T) {
		from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		to := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
		sql, args := NewTimeField("created_at").Between(from, to).ToSQL(1)
		if sql != "created_at BETWEEN $1 AND $2" {
			t.Errorf("SQL = %q", sql)
		}
		if len(args) != 2 || args[0] != from || args[1] != to {
			t.Errorf("args = %v", args)
		}
	})

	t.Run("decimal between", func(t *testing.T) {
		from := decimal.NewFromFloat(10)
		to := decimal.NewFromFloat(100)
		sql, args := NewDecimalField("amount").Between(from, to).ToSQL(1)
		if sql != "amount BETWEEN $1 AND $2" {
			t.Errorf("SQL = %q", sql)
		}
		if len(args) != 2 {
			t.Errorf("args = %v", args)
		}
	})
}

func TestTimeFieldExtendedConditions(t *testing.T) {
	now := time.Date(2026, 4, 13, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		cond    Condition
		wantSQL string
	}{
		{"neq", NewTimeField("ts").Neq(now), "ts != $1"},
		{"gte", NewTimeField("ts").Gte(now), "ts >= $1"},
		{"lte", NewTimeField("ts").Lte(now), "ts <= $1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql, args := tt.cond.ToSQL(1)
			if sql != tt.wantSQL {
				t.Errorf("SQL = %q, want %q", sql, tt.wantSQL)
			}
			if len(args) != 1 || args[0] != now {
				t.Errorf("args = %v", args)
			}
		})
	}
}

func TestTimeFieldFilterOp(t *testing.T) {
	f := NewTimeField("created_at")
	tests := []struct {
		op, val, wantSQL string
	}{
		{"eq", "2026-04-13T10:00:00Z", "created_at = $1"},
		{"ne", "2026-04-13T10:00:00Z", "created_at != $1"},
		{"gt", "2026-04-13T10:00:00Z", "created_at > $1"},
		{"after", "2026-04-13T10:00:00Z", "created_at > $1"},
		{"gte", "2026-04-13T10:00:00Z", "created_at >= $1"},
		{"lt", "2026-04-13T10:00:00Z", "created_at < $1"},
		{"before", "2026-04-13T10:00:00Z", "created_at < $1"},
		{"lte", "2026-04-13T10:00:00Z", "created_at <= $1"},
		{"unknown", "2026-04-13T10:00:00Z", "created_at = $1"},
	}
	for _, tt := range tests {
		sql, _ := f.FilterOp(tt.op, tt.val).ToSQL(1)
		if sql != tt.wantSQL {
			t.Errorf("TimeField.FilterOp(%q) = %q, want %q", tt.op, sql, tt.wantSQL)
		}
	}
}

func TestParseInt64(t *testing.T) {
	tests := []struct {
		input string
		want  int64
		err   bool
	}{
		{"42", 42, false},
		{"-7", -7, false},
		{"0", 0, false},
		{"abc", 0, true},
	}
	for _, tt := range tests {
		got, err := parseInt64(tt.input)
		if tt.err && err == nil {
			t.Errorf("parseInt64(%q) should error", tt.input)
		}
		if !tt.err && got != tt.want {
			t.Errorf("parseInt64(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}
