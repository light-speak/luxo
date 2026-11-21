package storage

import (
	"github.com/light-speak/luxo/internal/testdeps"
	"testing"
)

func TestStorageInterfaceDoesNotImportS3Driver(t *testing.T) {
	testdeps.Excludes(t, "github.com/minio/")
}
