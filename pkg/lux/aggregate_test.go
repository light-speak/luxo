package lux

import (
	"strings"
	"sync"
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
	sql := AggregateSQLWithPlaceholder("COUNT", "posts", "user_id", "", func(int) string { return "?" })
	if !strings.Contains(sql, "?") {
		t.Errorf("custom placeholder should use ?, got %s", sql)
	}
	if strings.Contains(sql, "$1") {
		t.Errorf("should not contain $1: %s", sql)
	}
	if fallback := AggregateSQLWithPlaceholder("COUNT", "posts", "user_id", "", nil); !strings.Contains(fallback, "$1") {
		t.Errorf("nil placeholder should use PostgreSQL syntax, got %s", fallback)
	}
}

func TestAggregateSQLPlaceholderIsConcurrentSafe(t *testing.T) {
	var wait sync.WaitGroup
	for index, placeholder := range []func(int) string{
		func(int) string { return "?" },
		func(int) string { return ":value" },
	} {
		wait.Add(1)
		go func(index int, placeholder func(int) string) {
			defer wait.Done()
			for range 100 {
				sql := AggregateSQLWithPlaceholder("COUNT", "posts", "user_id", "", placeholder)
				if !strings.Contains(sql, placeholder(1)) {
					t.Errorf("placeholder %d contaminated: %s", index, sql)
					return
				}
			}
		}(index, placeholder)
	}
	wait.Wait()
}
