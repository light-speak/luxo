package lux

import (
	"testing"
)

func TestBuildSelectSimple(t *testing.T) {
	sql, args := BuildSelectSQL("users", nil, nil, nil, 0, 0)
	if sql != "SELECT * FROM users" {
		t.Errorf("SQL = %q", sql)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want empty", args)
	}
}

func TestBuildSelectWithConditions(t *testing.T) {
	conds := []Condition{
		NewIntField("id").Eq(1),
		NewStringField("name").Eq("lin"),
	}
	sql, args := BuildSelectSQL("users", nil, conds, nil, 0, 0)
	want := "SELECT * FROM users WHERE id = $1 AND name = $2"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 2 {
		t.Errorf("args len = %d, want 2", len(args))
	}
}

func TestBuildSelectWithOrderBy(t *testing.T) {
	sql, _ := BuildSelectSQL("users", nil, nil, []string{"name ASC", "id DESC"}, 0, 0)
	want := "SELECT * FROM users ORDER BY name ASC, id DESC"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
}

func TestBuildSelectWithLimitOffset(t *testing.T) {
	sql, args := BuildSelectSQL("users", nil, nil, nil, 10, 20)
	want := "SELECT * FROM users LIMIT $1 OFFSET $2"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 2 || args[0] != 10 || args[1] != 20 {
		t.Errorf("args = %v", args)
	}
}

func TestBuildSelectFull(t *testing.T) {
	conds := []Condition{NewStringField("role").Eq("ADMIN")}
	sql, args := BuildSelectSQL("users", nil, conds, []string{"created_at DESC"}, 5, 10)
	want := "SELECT * FROM users WHERE role = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 3 {
		t.Fatalf("args len = %d, want 3", len(args))
	}
}

func TestBuildSelectWithFields(t *testing.T) {
	sql, _ := BuildSelectSQL("users", []string{"id", "name", "email"}, nil, nil, 0, 0)
	want := "SELECT id, name, email FROM users"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
}

func TestBuildSelectWithFieldsAndConditions(t *testing.T) {
	conds := []Condition{NewIntField("id").Gt(10)}
	sql, args := BuildSelectSQL("users", []string{"id", "name"}, conds, nil, 5, 0)
	want := "SELECT id, name FROM users WHERE id > $1 LIMIT $2"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 2 {
		t.Errorf("args len = %d, want 2", len(args))
	}
}

func TestBuildCount(t *testing.T) {
	sql, args := BuildCountSQL("users", nil)
	if sql != "SELECT COUNT(*) FROM users" {
		t.Errorf("SQL = %q", sql)
	}
	if len(args) != 0 {
		t.Errorf("args = %v", args)
	}
}

func TestBuildCountWithCondition(t *testing.T) {
	conds := []Condition{NewBoolField("active").IsTrue()}
	sql, args := BuildCountSQL("users", conds)
	want := "SELECT COUNT(*) FROM users WHERE active = $1"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 1 {
		t.Errorf("args len = %d", len(args))
	}
}

func TestBuildDelete(t *testing.T) {
	conds := []Condition{NewIntField("id").Eq(42)}
	sql, args := BuildDeleteSQL("users", conds)
	want := "DELETE FROM users WHERE id = $1"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 1 {
		t.Errorf("args len = %d", len(args))
	}
}

func TestBuildDeleteNoConds(t *testing.T) {
	sql, _ := BuildDeleteSQL("users", nil)
	if sql != "DELETE FROM users" {
		t.Errorf("SQL = %q", sql)
	}
}

func TestBuildUpdate(t *testing.T) {
	conds := []Condition{NewIntField("id").Eq(1)}
	sets := []SetField{
		{Col: "name", Val: "alice"},
		{Col: "email", Val: "alice@example.com"},
	}
	sql, args := BuildUpdateSQL("users", sets, conds)
	want := "UPDATE users SET name = $1, email = $2 WHERE id = $3"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 3 {
		t.Fatalf("args len = %d, want 3", len(args))
	}
}

func TestBuildSelectWithInCondition(t *testing.T) {
	conds := []Condition{NewIntField("id").In(1, 2, 3)}
	sql, args := BuildSelectSQL("users", nil, conds, nil, 0, 0)
	want := "SELECT * FROM users WHERE id IN ($1, $2, $3)"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 3 {
		t.Errorf("args len = %d, want 3", len(args))
	}
}

func TestBuildSelectMultipleConditionsArgOffset(t *testing.T) {
	conds := []Condition{
		NewStringField("role").In("USER", "ADMIN"),
		NewIntField("age").Gt(18),
	}
	sql, args := BuildSelectSQL("users", nil, conds, nil, 0, 0)
	want := "SELECT * FROM users WHERE role IN ($1, $2) AND age > $3"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 3 {
		t.Errorf("args len = %d, want 3", len(args))
	}
}

func TestBuildSelectLimitOnly(t *testing.T) {
	sql, args := BuildSelectSQL("users", nil, nil, nil, 5, 0)
	want := "SELECT * FROM users LIMIT $1"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 1 {
		t.Errorf("args len = %d", len(args))
	}
}

func TestBuildSelectOffsetOnly(t *testing.T) {
	sql, args := BuildSelectSQL("users", nil, nil, nil, 0, 10)
	want := "SELECT * FROM users OFFSET $1"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 1 {
		t.Errorf("args len = %d", len(args))
	}
}

// ===== BuildExistsSQL =====

func TestBuildExistsSQLNoConditions(t *testing.T) {
	sql, args := BuildExistsSQL("users", nil)
	want := "SELECT EXISTS(SELECT 1 FROM users LIMIT 1)"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want empty", args)
	}
}

func TestBuildExistsSQLWithCondition(t *testing.T) {
	conds := []Condition{NewIntField("id").Eq(42)}
	sql, args := BuildExistsSQL("users", conds)
	want := "SELECT EXISTS(SELECT 1 FROM users WHERE id = $1 LIMIT 1)"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 1 || args[0] != int64(42) {
		t.Errorf("args = %v, want [42]", args)
	}
}

func TestBuildExistsSQLWithMultipleConditions(t *testing.T) {
	conds := []Condition{
		NewStringField("role").Eq("ADMIN"),
		NewBoolField("active").IsTrue(),
	}
	sql, args := BuildExistsSQL("users", conds)
	want := "SELECT EXISTS(SELECT 1 FROM users WHERE role = $1 AND active = $2 LIMIT 1)"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 2 {
		t.Errorf("args len = %d, want 2", len(args))
	}
}

// ===== BuildUpdateSQL — verify strconv.AppendInt path =====

func TestBuildUpdateSQLNoConditions(t *testing.T) {
	sets := []SetField{{Col: "name", Val: "bob"}}
	sql, args := BuildUpdateSQL("users", sets, nil)
	want := "UPDATE users SET name = $1"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 1 || args[0] != "bob" {
		t.Errorf("args = %v", args)
	}
}

func TestBuildUpdateSQLManyFields(t *testing.T) {
	sets := []SetField{
		{Col: "a", Val: 1},
		{Col: "b", Val: 2},
		{Col: "c", Val: 3},
	}
	conds := []Condition{NewIntField("id").Eq(99)}
	sql, args := BuildUpdateSQL("posts", sets, conds)
	want := "UPDATE posts SET a = $1, b = $2, c = $3 WHERE id = $4"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 4 {
		t.Errorf("args len = %d, want 4", len(args))
	}
}

func TestBuildAggregateSQLSimple(t *testing.T) {
	sql, args := BuildAggregateSQL("orders", "SUM", "amount", nil)
	want := "SELECT COALESCE(SUM(amount), 0) FROM orders"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 0 {
		t.Errorf("args len = %d, want 0", len(args))
	}
}

func TestBuildAggregateSQLWithConditions(t *testing.T) {
	conds := []Condition{NewStringField("status").Eq("paid")}
	sql, args := BuildAggregateSQL("orders", "AVG", "total", conds)
	want := "SELECT COALESCE(AVG(total), 0) FROM orders WHERE status = $1"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 1 || args[0] != "paid" {
		t.Errorf("args = %v", args)
	}
}

func TestIntRangeNormal(t *testing.T) {
	got := IntRange(1, 5)
	if len(got) != 5 || got[0] != 1 || got[4] != 5 {
		t.Errorf("IntRange(1,5) = %v", got)
	}
}

func TestIntRangeInverted(t *testing.T) {
	got := IntRange(5, 1)
	if got != nil {
		t.Errorf("IntRange(5,1) should return nil, got %v", got)
	}
}

func TestIntRangeSingle(t *testing.T) {
	got := IntRange(3, 3)
	if len(got) != 1 || got[0] != 3 {
		t.Errorf("IntRange(3,3) = %v", got)
	}
}

func TestBuildUpdateSQLAtomic(t *testing.T) {
	sets := []SetField{
		{Col: "counter", Val: 1, Atomic: "+"},
		{Col: "name", Val: "alice"},
	}
	conds := []Condition{NewIntField("id").Eq(1)}
	sql, args := BuildUpdateSQL("users", sets, conds)
	want := "UPDATE users SET counter = counter + $1, name = $2 WHERE id = $3"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 3 {
		t.Errorf("args len = %d, want 3", len(args))
	}
}
