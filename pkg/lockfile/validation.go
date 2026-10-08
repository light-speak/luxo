package lockfile

import (
	"fmt"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

// MaxParameterID excludes the existing $filters and $sorters control IDs.
const MaxParameterID = codec.MaxParameterID

// Validate rejects ambiguous assignments before generation or persistence.
// IDs are never repaired: choosing either duplicate could change the wire ABI.
func (lf *LockFile) Validate() error {
	if lf == nil {
		return fmt.Errorf("luxo.lock must be an object")
	}
	if lf.Version < 1 || lf.Version > currentVersion {
		return fmt.Errorf("unsupported luxo.lock version %d", lf.Version)
	}
	if lf.Models == nil || lf.APIs == nil {
		return fmt.Errorf("luxo.lock models and apis must not be null")
	}
	for _, group := range []struct {
		name         string
		declarations map[string]*ModelLock
		limit        int
	}{
		{"models", lf.Models, codec.MaxSelectionFieldID},
		{"types", lf.Types, codec.MaxSelectionFieldID},
		{"events", lf.Events, codec.MaxEventFieldID},
	} {
		if err := validateModelIDs(group.name, group.declarations, group.limit); err != nil {
			return err
		}
	}
	return lf.validateAPIIDs()
}

func validateModelIDs(kind string, models map[string]*ModelLock, limit int) error {
	for name, model := range models {
		if name == "" || model == nil {
			return fmt.Errorf("luxo.lock %s has an empty name or null declaration", kind)
		}
		scope := kind + "." + name
		maximum, err := ValidateAssignments(scope, model.Fields, model.Reserved, limit)
		if err != nil {
			return err
		}
		if model.NextID <= maximum {
			return fmt.Errorf("luxo.lock %s next_id %d must exceed assigned/reserved ID %d", scope, model.NextID, maximum)
		}
		if uint64(model.NextID) > uint64(limit)+1 {
			return fmt.Errorf("luxo.lock %s next_id %d exceeds exhausted sentinel %d", scope, model.NextID, uint64(limit)+1)
		}
	}
	return nil
}

func (lf *LockFile) validateAPIIDs() error {
	apis := make(map[string]int, len(lf.APIs))
	for name, api := range lf.APIs {
		if api == nil {
			return fmt.Errorf("luxo.lock API %q must not be null", name)
		}
		apis[name] = api.ID
		if _, err := ValidateAssignments("apis."+name+".params", api.Params, api.Reserved, MaxParameterID); err != nil {
			return err
		}
	}
	_, err := ValidateAssignments("apis", apis, lf.ReservedAPI, codec.MaxAPIID)
	return err
}

// ValidateAssignments checks one independent identifier space without mutation.
// It returns the highest occupied ID (zero for an empty space). Generation tools
// share this check so direct callers cannot bypass lockfile assignment rules.
func ValidateAssignments(scope string, assigned map[string]int, reserved []int, limit int) (int, error) {
	seen := make(map[int]string, len(assigned)+len(reserved))
	maximum := 0
	for name, id := range assigned {
		if name == "" {
			return 0, fmt.Errorf("luxo.lock %s contains an empty declaration name", scope)
		}
		if err := claimID(scope, seen, id, name, limit); err != nil {
			return 0, err
		}
		maximum = max(maximum, id)
	}
	for _, id := range reserved {
		if err := claimID(scope, seen, id, "<reserved>", limit); err != nil {
			return 0, err
		}
		maximum = max(maximum, id)
	}
	return maximum, nil
}

func claimID(scope string, seen map[int]string, id int, name string, limit int) error {
	if id <= 0 || id > limit {
		return fmt.Errorf("luxo.lock %s ID %d is outside 1..%d", scope, id, limit)
	}
	if previous, exists := seen[id]; exists {
		return fmt.Errorf("luxo.lock %s ID %d is shared by %q and %q", scope, id, previous, name)
	}
	seen[id] = name
	return nil
}
