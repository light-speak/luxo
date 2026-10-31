package event

import (
	"github.com/light-speak/luxo/internal/testdeps"
	"testing"
)

func TestPortableEventDoesNotImportNATS(t *testing.T) { testdeps.Excludes(t, "github.com/nats-io/") }
