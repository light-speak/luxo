package queue

import (
	"github.com/light-speak/luxo/internal/testdeps"
	"testing"
)

func TestPortableQueueDoesNotImportNATS(t *testing.T) { testdeps.Excludes(t, "github.com/nats-io/") }
