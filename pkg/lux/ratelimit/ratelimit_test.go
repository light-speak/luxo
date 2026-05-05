package ratelimit

import (
	"testing"
	"time"
)

func TestLimiter_Allow(t *testing.T) {
	l := New(3, time.Second)
	// First 3 should pass
	for i := 0; i < 3; i++ {
		if !l.Allow("key") {
			t.Errorf("request %d should be allowed", i+1)
		}
	}
	// 4th should be blocked
	if l.Allow("key") {
		t.Error("4th request should be rate limited")
	}
}

func TestLimiter_DifferentKeys(t *testing.T) {
	l := New(1, time.Second)
	if !l.Allow("a") {
		t.Error("first key should pass")
	}
	if !l.Allow("b") {
		t.Error("different key should pass")
	}
	if l.Allow("a") {
		t.Error("same key should be limited")
	}
}

func TestLimiter_Refill(t *testing.T) {
	l := New(1, 50*time.Millisecond)
	l.Allow("key") // use the token
	if l.Allow("key") {
		t.Error("should be limited")
	}
	time.Sleep(60 * time.Millisecond) // wait for refill
	if !l.Allow("key") {
		t.Error("should be allowed after refill")
	}
}
