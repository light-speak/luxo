package luxolog

import (
	"bytes"
	"os"
	"sync"
	"testing"
)

func TestConcurrentLazyLevel(t *testing.T) {
	resetLevel(t)
	t.Setenv("LOG_LEVEL", "debug")
	const readers = 32
	start := make(chan struct{})
	results := make(chan int, readers)
	var workers sync.WaitGroup
	for range readers {
		workers.Go(func() {
			<-start
			results <- GetLevel()
		})
	}
	close(start)
	workers.Wait()
	close(results)
	for result := range results {
		if result != LevelDebug {
			t.Errorf("level = %d, want debug", result)
		}
	}
}

func resetLevel(t *testing.T) {
	t.Helper()
	previous := levelState.Swap(0)
	t.Cleanup(func() { levelState.Store(previous) })
}

func setTestLevel(t *testing.T, value int) {
	t.Helper()
	previous := levelState.Load()
	t.Cleanup(func() { levelState.Store(previous) })
	if err := SetLevel(value); err != nil {
		t.Fatal(err)
	}
}

func TestLevelInitialization(t *testing.T) {
	resetLevel(t)
	t.Setenv("LOG_LEVEL", "error")
	if levelState.Load() != 0 {
		t.Fatal("environment must be read lazily")
	}
	t.Setenv("LOG_LEVEL", "WaRn")
	if got := GetLevel(); got != LevelWarn {
		t.Fatalf("level = %d, want warn", got)
	}
	t.Setenv("LOG_LEVEL", "debug")
	if got := GetLevel(); got != LevelWarn {
		t.Fatalf("initialized level changed with environment: %d", got)
	}
}

func TestSetLevel(t *testing.T) {
	resetLevel(t)
	t.Setenv("LOG_LEVEL", "error")
	for value := LevelDebug; value <= LevelError; value++ {
		if err := SetLevel(value); err != nil {
			t.Fatal(err)
		}
		if got := GetLevel(); got != value {
			t.Fatalf("level = %d, want %d", got, value)
		}
	}
	for _, value := range []int{-1, 4, int(^uint(0) >> 1)} {
		if err := SetLevel(value); err == nil {
			t.Fatalf("accepted invalid level %d", value)
		}
		if got := GetLevel(); got != LevelError {
			t.Fatalf("invalid update changed level: %d", got)
		}
	}
}

func TestInvalidLevelKeepsLazyEnvironment(t *testing.T) {
	resetLevel(t)
	t.Setenv("LOG_LEVEL", "warn")
	if err := SetLevel(-1); err == nil {
		t.Fatal("accepted negative level")
	}
	if levelState.Load() != 0 {
		t.Fatal("invalid update initialized state")
	}
	if got := GetLevel(); got != LevelWarn {
		t.Fatalf("level = %d, want environment value", got)
	}
}

func TestInitializationDoesNotOverwriteSetLevel(t *testing.T) {
	resetLevel(t)
	setTestLevel(t, LevelError)
	// Simulate an environment read that completed after an explicit update.
	if got := initializeLevel(LevelDebug + 1); got != LevelError {
		t.Fatalf("stale initializer replaced explicit level: %d", got)
	}
}

func TestConcurrentSetAndGetLevel(t *testing.T) {
	resetLevel(t)
	t.Setenv("LOG_LEVEL", "info")
	var workers sync.WaitGroup
	start := make(chan struct{})
	for worker := range 32 {
		workers.Go(func() {
			<-start
			for range 1000 {
				if worker%2 == 0 {
					if err := SetLevel(worker % 4); err != nil {
						t.Error(err)
					}
				}
				if got := GetLevel(); got < LevelDebug || got > LevelError {
					t.Errorf("invalid concurrent level: %d", got)
				}
			}
		})
	}
	close(start)
	workers.Wait()
}

func captureStderr(fn func()) string {
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	fn()
	w.Close()
	os.Stderr = old
	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String()
}

func TestInfo(t *testing.T) {
	setTestLevel(t, LevelDebug)
	out := captureStderr(func() { Info("hello info") })
	if out == "" {
		t.Error("Info should print")
	}
	if ret := Info("test"); ret != "test" {
		t.Error("Info should return input string")
	}
}

func TestDebug(t *testing.T) {
	setTestLevel(t, LevelDebug)
	out := captureStderr(func() { Debug("debug msg") })
	if out == "" {
		t.Error("Debug should print at debug level")
	}
	setTestLevel(t, LevelInfo)
	out = captureStderr(func() { Debug("hidden") })
	if out != "" {
		t.Error("Debug should not print at info level")
	}
}

func TestWarn(t *testing.T) {
	setTestLevel(t, LevelDebug)
	out := captureStderr(func() { Warn("warning") })
	if out == "" {
		t.Error("Warn should print")
	}
	if ret := Warn("w"); ret != "w" {
		t.Error("Warn should return input")
	}
}

func TestError(t *testing.T) {
	setTestLevel(t, LevelDebug)
	out := captureStderr(func() { Error("err msg") })
	if out == "" {
		t.Error("Error should print")
	}
	if ret := Error("e"); ret != "e" {
		t.Error("Error should return input")
	}
}

func TestLevelFiltering(t *testing.T) {
	setTestLevel(t, LevelError)
	out := captureStderr(func() {
		Info("should not print")
		Warn("should not print")
		Debug("should not print")
	})
	if out != "" {
		t.Errorf("nothing below Error should print, got: %s", out)
	}
	out = captureStderr(func() { Error("should print") })
	if out == "" {
		t.Error("Error should still print at Error level")
	}
}

func TestParseLevel(t *testing.T) {
	if parseLevel("debug") != LevelDebug {
		t.Error("debug")
	}
	if parseLevel("warn") != LevelWarn {
		t.Error("warn")
	}
	if parseLevel("error") != LevelError {
		t.Error("error")
	}
	if parseLevel("") != LevelInfo {
		t.Error("default should be info")
	}
	if parseLevel("unknown") != LevelInfo {
		t.Error("unknown should default to info")
	}
}
