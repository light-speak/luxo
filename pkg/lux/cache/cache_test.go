package cache

import (
	"testing"
	"time"
)

func TestCache_SetGet(t *testing.T) {
	c := New()
	c.Set("key1", []byte("value1"), time.Minute)
	if got := c.Get("key1"); string(got) != "value1" {
		t.Errorf("got %q", got)
	}
}

func TestCache_Miss(t *testing.T) {
	c := New()
	if got := c.Get("missing"); got != nil {
		t.Errorf("miss should return nil, got %v", got)
	}
}

func TestCache_Expired(t *testing.T) {
	c := New()
	c.Set("key", []byte("val"), time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	if got := c.Get("key"); got != nil {
		t.Errorf("expired should return nil, got %v", got)
	}
}

func TestCache_Invalidate(t *testing.T) {
	c := New()
	c.Set("User:1", []byte("a"), time.Minute)
	c.Set("User:2", []byte("b"), time.Minute)
	c.Set("Post:1", []byte("c"), time.Minute)
	c.Invalidate("User:")
	if c.Get("User:1") != nil || c.Get("User:2") != nil {
		t.Error("User entries should be invalidated")
	}
	if c.Get("Post:1") == nil {
		t.Error("Post entry should still exist")
	}
}

func TestCache_InvalidateAll(t *testing.T) {
	c := New()
	c.Set("a", []byte("1"), time.Minute)
	c.Set("b", []byte("2"), time.Minute)
	c.InvalidateAll()
	if c.Get("a") != nil || c.Get("b") != nil {
		t.Error("all entries should be cleared")
	}
}
