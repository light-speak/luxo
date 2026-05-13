package luxolog

import (
	"fmt"
	"os"
	"strings"
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

// Level controls minimum log level. Set via LOG_LEVEL env (debug/info/warn/error).
var Level = parseLevel(os.Getenv("LOG_LEVEL"))

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
	if Level <= LevelDebug {
		ts := time.Now().Format("15:04:05")
		fmt.Fprintf(os.Stderr, "%s%s%s %sDEBUG%s %s\n", dim, ts, reset, blue, reset, msg)
	}
	return msg
}

// Info prints an info message (cyan).
func Info(msg string) string {
	if Level <= LevelInfo {
		ts := time.Now().Format("15:04:05")
		fmt.Fprintf(os.Stderr, "%s%s%s %sINFO%s  %s\n", dim, ts, reset, cyan, reset, msg)
	}
	return msg
}

// Warn prints a warning message (yellow).
func Warn(msg string) string {
	if Level <= LevelWarn {
		ts := time.Now().Format("15:04:05")
		fmt.Fprintf(os.Stderr, "%s%s%s %sWARN%s  %s\n", dim, ts, reset, yellow, reset, msg)
	}
	return msg
}

// Error prints an error message (red).
func Error(msg string) string {
	if Level <= LevelError {
		ts := time.Now().Format("15:04:05")
		fmt.Fprintf(os.Stderr, "%s%s%s %sERROR%s %s\n", dim, ts, reset, red, reset, msg)
	}
	return msg
}
