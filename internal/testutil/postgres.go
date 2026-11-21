package testutil

import "os"

// PostgresImage allows an explicit local test image without changing the CI default.
// Each test still creates and cleans up its own container and database.
func PostgresImage() string {
	if image := os.Getenv("LUXO_TEST_POSTGRES_IMAGE"); image != "" {
		return image
	}
	return "postgres:17-alpine"
}
