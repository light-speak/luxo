package env

import (
	"errors"
	"os"
	"strings"
)

// CheckBackend rejects runtime attempts to change the compiled adapter.
// An unset value leaves the build selection intact; it never selects a default.
func CheckBackend(name, compiled string) error {
	if configured := os.Getenv(name); configured != "" && configured != compiled {
		return errors.New("env: " + name + " differs from the compiled backend; regenerate and rebuild")
	}
	return nil
}

// Required reads connection configuration without exposing credentials in errors.
func Required(name string) (string, error) {
	value := os.Getenv(name)
	if strings.TrimSpace(value) == "" {
		return "", errors.New("env: required configuration " + name + " is empty")
	}
	return value, nil
}
