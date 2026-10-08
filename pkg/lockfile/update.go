package lockfile

import (
	"maps"
	"slices"
)

// Clone mutable state directly, without JSON round trips or reflection. Counters
// are derived from the persisted assignments, never from stale transient values.
func (lf *LockFile) cloneForUpdate() *LockFile {
	next := &LockFile{
		Version:     lf.Version,
		Models:      cloneModelLocks(lf.Models),
		Types:       cloneModelLocks(lf.Types),
		Events:      cloneModelLocks(lf.Events),
		APIs:        make(map[string]*APILock, len(lf.APIs)),
		ReservedAPI: slices.Clone(lf.ReservedAPI),
	}
	for name, api := range lf.APIs {
		next.APIs[name] = &APILock{
			ID: api.ID, Params: maps.Clone(api.Params), ParamTypes: maps.Clone(api.ParamTypes),
			ReturnType: api.ReturnType, Contract: api.Contract, Reserved: slices.Clone(api.Reserved),
		}
	}
	next.computeNextAPI()
	return next
}

func cloneModelLocks(source map[string]*ModelLock) map[string]*ModelLock {
	if source == nil {
		return nil
	}
	cloned := make(map[string]*ModelLock, len(source))
	for name, model := range source {
		fields := make(map[string]int, len(model.Fields))
		maps.Copy(fields, model.Fields)
		cloned[name] = &ModelLock{
			NextID: model.NextID, Fields: fields,
			FieldTypes: maps.Clone(model.FieldTypes), Reserved: slices.Clone(model.Reserved),
		}
	}
	return cloned
}
