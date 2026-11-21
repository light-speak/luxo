package storage

import (
	"path"
	"testing"

	"github.com/google/uuid"
)

// The old body differs only in its name; it may panic on entropy failure.
func generateKeyBeforeUUIDErrors(model, field, filename string) string {
	ext := path.Ext(filename)
	id := uuid.New().String()
	return model + "/" + field + "/" + id + ext
}

var uuidStorageKeyResult string

func BenchmarkStorageUUID(b *testing.B) {
	for _, filename := range []string{"photo.png", "README"} {
		b.Run(filename+"/before", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				uuidStorageKeyResult = generateKeyBeforeUUIDErrors("user", "avatar", filename)
			}
		})
		b.Run(filename+"/current", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				key, err := GenerateKey("user", "avatar", filename)
				if err != nil {
					b.Fatal(err)
				}
				uuidStorageKeyResult = key
			}
		})
	}
}
