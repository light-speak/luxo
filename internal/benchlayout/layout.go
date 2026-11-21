// Package benchlayout preserves benchmark identities across adapter moves.
package benchlayout

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var moves = []struct{ current, original string }{
	{"pkg/lux/cache/redis", "pkg/lux/cache"},
	{"pkg/lux/nats/queue", "pkg/lux/queue"},
	{"pkg/lux/nats/event", "pkg/lux/event"},
	{"pkg/lux/storage/s3", "pkg/lux/storage"},
	{"pkg/lux/dialect/pg", "pkg/lux/pg"},
	{"pkg/lux/pg/migrate", "pkg/lux/migrate"},
}

func Canonical(path string) string {
	for _, move := range moves {
		if path == move.current {
			return move.original
		}
	}
	return path
}

// Packages returns all physical packages belonging to one historical identity.
// A split is a group, not a fallback: both memory and Redis must be measured.
func Packages(root, logical string) ([]string, error) {
	if _, err := os.Stat(root); err != nil {
		return nil, err
	}
	if (!strings.HasPrefix(logical, "pkg/") && !strings.HasPrefix(logical, "scripts/")) || filepath.ToSlash(filepath.Clean(logical)) != logical || strings.ContainsAny(logical, "\\\t\r\n") {
		return nil, fmt.Errorf("invalid benchmark package %q", logical)
	}
	candidates := []string{logical}
	for _, move := range moves {
		if move.original == logical {
			candidates = append(candidates, move.current)
		}
	}
	var found []string
	for _, path := range candidates {
		info, err := os.Stat(filepath.Join(root, path))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			found = append(found, path)
		}
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("missing benchmark package %s in %s", logical, root)
	}
	return found, nil
}
