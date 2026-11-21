package cache

import (
	"github.com/light-speak/luxo/internal/testdeps"
	"testing"
)

func TestPortableCacheDoesNotImportRedis(t *testing.T) { testdeps.Excludes(t, "github.com/redis/") }
