package cachepolicy

import (
	"errors"
	"fmt"
	"os"
)

const Filename = "luxo.cache.json"

// Load reads an optional project catalog. Missing catalogs are allowed until a
// policy is referenced; unreadable or malformed catalogs are never ignored.
func Load(path string) (*Catalog, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read cache policies %s: %w", path, err)
	}
	policies, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse cache policies %s: %w", path, err)
	}
	return policies, nil
}
