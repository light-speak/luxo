//go:build !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd && !windows

package main

import (
	"errors"
	"os"
)

func tryMigrationGenerationLock(file *os.File) (bool, error) {
	return false, errors.ErrUnsupported
}
