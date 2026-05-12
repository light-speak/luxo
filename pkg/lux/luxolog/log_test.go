package luxolog

import (
	"bytes"
	"os"
	"testing"
)

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
	Level = LevelDebug
	out := captureStderr(func() { Info("hello info") })
	if out == "" {
		t.Error("Info should print")
	}
	if ret := Info("test"); ret != "test" {
		t.Error("Info should return input string")
	}
}

func TestDebug(t *testing.T) {
	Level = LevelDebug
	out := captureStderr(func() { Debug("debug msg") })
	if out == "" {
		t.Error("Debug should print at debug level")
	}
	Level = LevelInfo
	out = captureStderr(func() { Debug("hidden") })
	if out != "" {
		t.Error("Debug should not print at info level")
	}
}

func TestWarn(t *testing.T) {
	Level = LevelDebug
	out := captureStderr(func() { Warn("warning") })
	if out == "" {
		t.Error("Warn should print")
	}
	if ret := Warn("w"); ret != "w" {
		t.Error("Warn should return input")
	}
}

func TestError(t *testing.T) {
	Level = LevelDebug
	out := captureStderr(func() { Error("err msg") })
	if out == "" {
		t.Error("Error should print")
	}
	if ret := Error("e"); ret != "e" {
		t.Error("Error should return input")
	}
}

func TestLevelFiltering(t *testing.T) {
	Level = LevelError
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
