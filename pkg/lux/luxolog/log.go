package luxolog

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// ANSI colors
const (
	reset   = "\033[0m"
	dim     = "\033[2m"
	red     = "\033[31m"
	yellow  = "\033[33m"
	blue    = "\033[34m"
	cyan    = "\033[36m"
	magenta = "\033[35m"
)

// Zero means uninitialized; configured levels are stored as level + 1.
var levelState atomic.Int32

var errInvalidLevel = errors.New("luxolog: level must be Debug, Info, Warn or Error")

// GetLevel returns the minimum log level. LOG_LEVEL is read on first use,
// after application environment loading, unless SetLevel was called first.
func GetLevel() int {
	if state := levelState.Load(); state != 0 {
		return int(state - 1)
	}
	return initializeLevel(int32(parseLevel(os.Getenv("LOG_LEVEL"))) + 1)
}

func initializeLevel(state int32) int {
	// A concurrent explicit update must take precedence over environment loading.
	levelState.CompareAndSwap(0, state)
	return int(levelState.Load() - 1)
}

// SetLevel changes the minimum log level safely during concurrent logging.
// Invalid values return an error without changing the current state.
func SetLevel(value int) error {
	if value < LevelDebug || value > LevelError {
		return errInvalidLevel
	}
	levelState.Store(int32(value) + 1)
	return nil
}

const (
	LevelDebug = 0
	LevelInfo  = 1
	LevelWarn  = 2
	LevelError = 3
)

func parseLevel(s string) int {
	switch strings.ToLower(s) {
	case "debug":
		return LevelDebug
	case "warn":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

// Debug prints a debug message (blue).
func Debug(msg string) string {
	if GetLevel() <= LevelDebug {
		ts := time.Now().Format("15:04:05")
		fmt.Fprintf(os.Stderr, "%s%s%s %sDEBUG%s %s\n", dim, ts, reset, blue, reset, msg)
	}
	return msg
}

// Info prints an info message (cyan).
func Info(msg string) string {
	if GetLevel() <= LevelInfo {
		ts := time.Now().Format("15:04:05")
		fmt.Fprintf(os.Stderr, "%s%s%s %sINFO%s  %s\n", dim, ts, reset, cyan, reset, msg)
	}
	return msg
}

// Warn prints a warning message (yellow).
func Warn(msg string) string {
	if GetLevel() <= LevelWarn {
		ts := time.Now().Format("15:04:05")
		fmt.Fprintf(os.Stderr, "%s%s%s %sWARN%s  %s\n", dim, ts, reset, yellow, reset, msg)
	}
	return msg
}

// Error prints an error message (red).
func Error(msg string) string {
	if GetLevel() <= LevelError {
		ts := time.Now().Format("15:04:05")
		fmt.Fprintf(os.Stderr, "%s%s%s %sERROR%s %s\n", dim, ts, reset, red, reset, msg)
	}
	return msg
}
