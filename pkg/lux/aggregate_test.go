package lux

import (
	"strings"
	"testing"
)

func TestAggregateSQL_Count(t *testing.T) {
	sql := AggregateSQL("COUNT", "posts", "user_id", "")
	if !strings.Contains(sql, "COUNT(*)") || !strings.Contains(sql, "user_id") {
		t.Errorf("COUNT = %s", sql)
	}
}

func TestAggregateSQL_Sum(t *testing.T) {
	sql := AggregateSQL("SUM", "orders", "user_id", "total")
	if !strings.Contains(sql, "SUM(total)") || !strings.Contains(sql, "COALESCE") {
		t.Errorf("SUM = %s", sql)
	}
}

func TestAggregateSQL_Placeholder(t *testing.T) {
	old := PlaceholderFn
	defer func() { PlaceholderFn = old }()
	PlaceholderFn = func(i int) string { return "?" }

	sql := AggregateSQL("COUNT", "posts", "user_id", "")
	if !strings.Contains(sql, "?") {
		t.Errorf("custom placeholder should use ?, got %s", sql)
	}
	if strings.Contains(sql, "$1") {
		t.Errorf("should not contain $1: %s", sql)
	}
}
