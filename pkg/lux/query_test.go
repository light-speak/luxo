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
