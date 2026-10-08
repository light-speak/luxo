package lux

import (
	"strings"
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

func TestBuildGroupBySQL(t *testing.T) {
	sql, args := BuildGroupBySQL("orders", []string{"status"}, []GroupAgg{
		{Col: "status"},
		{Fn: "COUNT", Alias: "cnt"},
		{Fn: "SUM", Col: "total", Alias: "total_sum"},
	}, nil, nil, 0, 0)
	want := "SELECT status, COUNT(*) AS cnt, COALESCE(SUM(total), 0) AS total_sum FROM orders GROUP BY status"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want empty", args)
	}
}

func TestBuildGroupBySQLHaving(t *testing.T) {
	query, args := BuildGroupBySQL("orders", []string{"status"}, []GroupAgg{{Fn: "COUNT", Alias: "count"}},
		[]Condition{NewIntField("owner_id").Eq(7)}, []string{"count DESC"}, 10, 2,
		AnyOf(NewIntField("COUNT(*)").Gt(1), NewStringField("status").Eq("OPEN")),
		NewIntField("COALESCE(SUM(points), 0)").Gte(4))
	want := "SELECT COUNT(*) AS count FROM orders WHERE owner_id = $1 GROUP BY status HAVING ((COUNT(*) > $2 OR status = $3) AND COALESCE(SUM(points), 0) >= $4) ORDER BY count DESC LIMIT $5 OFFSET $6"
	if query != want {
		t.Fatalf("query=%q want=%q", query, want)
	}
	wantArgs := []any{int64(7), int64(1), "OPEN", int64(4), 10, 2}
	if len(args) != len(wantArgs) {
		t.Fatal(args)
	}
	for index := range args {
		if args[index] != wantArgs[index] {
			t.Fatalf("argument %d = %v want %v", index, args[index], wantArgs[index])
		}
	}
}

func TestGroupExpressionOnlyAllocatesDestination(t *testing.T) {
	for _, aggregate := range []GroupAgg{{Fn: "COUNT"}, {Fn: "SUM", Col: "points"}, {Fn: "MIN", Col: "points"}} {
		allocations := testing.AllocsPerRun(100, func() {
			var destination strings.Builder
			destination.Grow(64)
			appendGroupExpression(&destination, aggregate)
			if destination.Len() == 0 {
				t.Fatal("empty expression")
			}
		})
		if allocations != 1 {
			t.Fatalf("%s: got %g allocations, want only the destination allocation", aggregate.Fn, allocations)
		}
	}
}

func TestGroupAggSQLExpression(t *testing.T) {
	for _, test := range []struct {
		column GroupAgg
		want   string
	}{
		{GroupAgg{Col: "status", Alias: "ignored"}, "status"},
		{GroupAgg{Fn: "COUNT"}, "COUNT(*)"},
		{GroupAgg{Fn: "SUM", Col: "points"}, "COALESCE(SUM(points), 0)"},
		{GroupAgg{Fn: "AVG", Col: "points"}, "COALESCE(AVG(points), 0)"},
		{GroupAgg{Fn: "MIN", Col: "points"}, "MIN(points)"},
	} {
		if got := test.column.SQLExpression(); got != test.want {
			t.Fatalf("got %q want %q", got, test.want)
		}
	}
}

func TestBuildGroupBySQLNilHaving(t *testing.T) {
	query, args := BuildGroupBySQL("orders", []string{"status"}, []GroupAgg{{Col: "status"}}, nil, nil, 0, 0, nil)
	if query != "SELECT status FROM orders GROUP BY status HAVING TRUE" || len(args) != 0 {
		t.Fatal(query, args)
	}
}

func TestBuildGroupBySQLExplicitProjection(t *testing.T) {
	sql, args := BuildGroupBySQL("orders", []string{"status", "owner_id"}, []GroupAgg{
		{Fn: "COUNT", Alias: "total"},
		{Col: "owner_id", Alias: "owner"},
		{Fn: "MIN", Col: "amount", Alias: "minimum"},
	}, nil, nil, 0, 0)
	want := "SELECT COUNT(*) AS total, owner_id AS owner, MIN(amount) AS minimum FROM orders GROUP BY status, owner_id"
	if sql != want || len(args) != 0 {
		t.Fatalf("projection changed: sql=%q args=%v", sql, args)
	}
}

func TestBuildGroupBySQLWithConditions(t *testing.T) {
	conds := []Condition{NewIntField("project_id").Eq(1)}
	sql, args := BuildGroupBySQL("traces", []string{"api_name", "status_code"}, []GroupAgg{
		{Col: "api_name"},
		{Col: "status_code"},
		{Fn: "COUNT", Alias: "cnt"},
		{Fn: "MAX", Col: "timestamp", Alias: "last_seen"},
	}, conds, []string{"cnt DESC"}, 10, 5)
	if !strings.Contains(sql, "GROUP BY api_name, status_code") {
		t.Errorf("missing GROUP BY: %s", sql)
	}
	if !strings.Contains(sql, "WHERE project_id = $1") {
		t.Errorf("missing WHERE: %s", sql)
	}
	if !strings.Contains(sql, "ORDER BY cnt DESC") {
		t.Errorf("missing ORDER BY: %s", sql)
	}
	if !strings.Contains(sql, "LIMIT $2") {
		t.Errorf("missing LIMIT: %s", sql)
	}
	if !strings.Contains(sql, "OFFSET $3") {
		t.Errorf("missing OFFSET: %s", sql)
	}
	if len(args) != 3 || args[0] != int64(1) || args[1] != 10 || args[2] != 5 {
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
