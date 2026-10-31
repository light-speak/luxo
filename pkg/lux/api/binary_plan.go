package api

import "github.com/light-speak/luxo/pkg/lux/schema"

// Plans are replaced during registration and remain read-only while serving.
type binaryAPIPlan struct {
	name       string
	params     []ParamMeta
	names      []string
	definition *schema.API
	lookup     binaryParamLookup
}

// Nearby IDs use offsets; sparse IDs use a fixed half-full hash table, never a max-ID allocation.
type binaryParamLookup struct {
	base  int
	slots [32]uint8
	ids   []int
}

func newBinaryParamLookup(params []ParamMeta) binaryParamLookup {
	var p binaryParamLookup
	if len(params) == 0 || len(params) > 16 {
		return p
	}
	p.base = params[0].FieldID
	high := p.base
	for _, m := range params {
		p.base = min(p.base, m.FieldID)
		high = max(high, m.FieldID)
	}
	if high-p.base < len(p.slots) {
		for i, m := range params {
			p.slots[m.FieldID-p.base] = uint8(i + 1)
		}
	} else {
		p.ids = make([]int, len(params))
		for i, m := range params {
			p.ids[i] = m.FieldID
			slot := binaryParamHash(m.FieldID)
			for p.slots[slot] != 0 {
				slot = (slot + 1) & 31
			}
			p.slots[slot] = uint8(i + 1)
		}
	}
	return p
}

func (p *binaryParamLookup) find(id int) int {
	if p.ids == nil {
		offset := uint(id) - uint(p.base)
		if offset < uint(len(p.slots)) {
			return int(p.slots[offset]) - 1
		}
		return -1
	}
	for slot := binaryParamHash(id); p.slots[slot] != 0; slot = (slot + 1) & 31 {
		index := int(p.slots[slot]) - 1
		if p.ids[index] == id {
			return index
		}
	}
	return -1
}

func binaryParamHash(id int) uint32 { return uint32(id) * 0x9e3779b1 >> 27 }

func (r *APIRegistry) refreshBinaryPlan(name string) {
	id, ok := r.nameToID[name]
	if !ok {
		return
	}
	if r.binaryPlans == nil {
		r.binaryPlans = make(map[int]*binaryAPIPlan)
	}
	params := r.paramOrder[name]
	r.binaryPlans[id] = &binaryAPIPlan{name: name, params: params, names: r.paramNames[name], definition: r.apiDefinition(name), lookup: newBinaryParamLookup(params)}
}

func (r *APIRegistry) rebuildBinaryPlans() {
	r.binaryPlans = make(map[int]*binaryAPIPlan, len(r.nameToID))
	for name := range r.nameToID {
		r.refreshBinaryPlan(name)
	}
}

func (rt *Router) prepareBinaryRoutes() {
	routes := make(map[*binaryAPIPlan]handlerRoute, len(rt.handlers))
	if rt.Registry != nil {
		for _, plan := range rt.Registry.binaryPlans {
			if route, ok := rt.handlers[plan.name]; ok {
				routes[plan] = route
			}
		}
	}
	rt.binaryRoutes = routes
}

func (rt *Router) requestRoute(req *Request) (handlerRoute, bool) {
	if req.binaryPlan != nil && rt.binaryRoutes != nil {
		route, ok := rt.binaryRoutes[req.binaryPlan]
		return route, ok
	}
	route, ok := rt.handlers[req.API]
	return route, ok
}
