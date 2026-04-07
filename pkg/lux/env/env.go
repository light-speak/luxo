// Package env provides dotenv file loading for Luxo applications.
// It reads a .env file and sets environment variables without overriding
// existing values (system env takes precedence).
package env

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Load reads a .env file at the given path and sets environment variables.
// Existing environment variables are NOT overridden (system env wins).
func Load(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("env: open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || line[0] == '#' {
			continue
		}

		key, value, err := parseLine(line)
		if err != nil {
			return fmt.Errorf("env: %s:%d: %w", path, lineNum, err)
		}

		// Do not override existing environment variables
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}
	return scanner.Err()
}

// MustLoad is like Load but panics on error.
func MustLoad(path string) {
	if err := Load(path); err != nil {
		panic(fmt.Sprintf("env: %v", err))
	}
}

// Get reads an environment variable. Returns the value and whether it was set.
// Works with both system env vars and values loaded from .env.
func Get(key string) (string, bool) {
	return os.LookupEnv(key)
}

// MustGet reads an environment variable and panics if it is not set.
func MustGet(key string) string {
	val, ok := os.LookupEnv(key)
	if !ok {
		panic(fmt.Sprintf("env: required variable %q is not set", key))
	}
	return val
}

// parseLine parses a single KEY=VALUE line.
func parseLine(line string) (string, string, error) {
	// Split on first '='
	idx := strings.IndexByte(line, '=')
	if idx < 0 {
		return "", "", fmt.Errorf("missing '=' in %q", line)
	}

	key := strings.TrimSpace(line[:idx])
	if key == "" {
		return "", "", fmt.Errorf("empty key in %q", line)
	}

	value := strings.TrimSpace(line[idx+1:])
	value = unquote(value)

	return key, value, nil
}

// unquote removes surrounding quotes (single or double) from a value.
func unquote(s string) string {
	if len(s) < 2 {
		return s
	}
	if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}
