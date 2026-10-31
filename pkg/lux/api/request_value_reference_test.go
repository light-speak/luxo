package api

// Snapshot from 1e0a1b2 for the independently measured slot-state change.
// Its null semantics are known to be incorrect; it is not a valid alternative.
func (r *Request) setParamSlotBeforeStatic(index int, value any) {
	if index < 0 || index >= 16 {
		return
	}
	r.markParamPresent(index)
	existing := r.paramSlots[index]
	if existing == nil {
		r.paramSlots[index] = value
		return
	}
	// Accumulate into slice
	switch ev := existing.(type) {
	case int64:
		if iv, ok := value.(int64); ok {
			r.paramSlots[index] = []int64{ev, iv}
			return
		}
	case []int64:
		if iv, ok := value.(int64); ok {
			r.paramSlots[index] = append(ev, iv)
			return
		}
	case string:
		if sv, ok := value.(string); ok {
			r.paramSlots[index] = []string{ev, sv}
			return
		}
	case []string:
		if sv, ok := value.(string); ok {
			r.paramSlots[index] = append(ev, sv)
			return
		}
	}
	// Type mismatch or unsupported — overwrite
	r.paramSlots[index] = value
}
