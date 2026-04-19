package lockfile

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"

	"github.com/light-speak/luxo/pkg/ast"
)

// LockFile represents the luxo.lock file that tracks stable field IDs
// for binary protocol compatibility.
type LockFile struct {
	Version int                   `json:"version"`
	Models  map[string]*ModelLock `json:"models"`
	Events  map[string]*ModelLock `json:"events,omitempty"`
	APIs    map[string]*APILock   `json:"apis"`
	nextAPI int                   // transient: next API ID to assign
}

// APILock tracks ID and param field IDs for a single API.
type APILock struct {
	ID     int            `json:"id"`
	Params map[string]int `json:"params,omitempty"` // param_name → stable field ID
	nextP  int            // transient
}

// ModelLock tracks field ID assignments for a single model.
type ModelLock struct {
	NextID   int            `json:"next_id"`
	Fields   map[string]int `json:"fields"`
	Reserved []int          `json:"reserved,omitempty"`
}

// MarshalJSON outputs fields sorted by ID value instead of alphabetically.
func (ml *ModelLock) MarshalJSON() ([]byte, error) {
	type entry struct {
		Name string
		ID   int
	}
	entries := make([]entry, 0, len(ml.Fields))
	for name, id := range ml.Fields {
		entries = append(entries, entry{name, id})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ID < entries[j].ID
	})

	var b []byte
	b = append(b, '{')
	b = append(b, `"next_id":`...)
	b = strconv.AppendInt(b, int64(ml.NextID), 10)

	b = append(b, `,"fields":{`...)
	for i, e := range entries {
		if i > 0 {
			b = append(b, ',')
		}
		key, _ := json.Marshal(e.Name)
		b = append(b, key...)
		b = append(b, ':')
		b = strconv.AppendInt(b, int64(e.ID), 10)
	}
	b = append(b, '}')

	if len(ml.Reserved) > 0 {
		b = append(b, `,"reserved":`...)
		r, _ := json.Marshal(ml.Reserved)
		b = append(b, r...)
	}
	b = append(b, '}')
	return b, nil
}

const currentVersion = 1

// New creates an empty lock file.
func New() *LockFile {
	return &LockFile{
		Version: currentVersion,
		Models:  make(map[string]*ModelLock),
		Events:  make(map[string]*ModelLock),
		APIs:    make(map[string]*APILock),
		nextAPI: 0,
	}
}

// Load reads a luxo.lock file from disk.
// Returns a new empty lock file if the file does not exist.
func Load(path string) (*LockFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return New(), nil
		}
		return nil, err
	}
	lf := New()
	if err := json.Unmarshal(data, lf); err != nil {
		return nil, err
	}
	lf.computeNextAPI()
	return lf, nil
}

// Save writes the lock file to disk as formatted JSON.
func (lf *LockFile) Save(path string) error {
	lf.sortReserved()
	data, err := json.MarshalIndent(lf, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

// Update synchronizes the lock file with the current AST.
// New fields get the next available ID. Removed fields have their IDs reserved.
// New APIs get the next available API ID.
func (lf *LockFile) Update(files []*ast.File) {
	lf.updateModels(files)
	lf.updateEvents(files)
	lf.updateAPIs(files)
}

// updateEvents assigns param IDs to all event declarations.
// Uses the same ModelLock structure (param name → stable ID).
func (lf *LockFile) updateEvents(files []*ast.File) {
	if lf.Events == nil {
		lf.Events = make(map[string]*ModelLock)
	}

	// Collect current events
	currentEvents := make(map[string]map[string]bool)
	for _, file := range files {
		for _, e := range file.Events {
			params := make(map[string]bool, len(e.Params))
			for _, p := range e.Params {
				params[p.Name] = true
			}
			currentEvents[e.Name] = params
		}
	}

	// Reserve removed params
	for name, el := range lf.Events {
		current, exists := currentEvents[name]
		if !exists {
			lf.reserveAllFields(el)
			continue
		}
		lf.reserveRemovedFields(el, current)
	}

	// Assign IDs to new params
	for _, file := range files {
		for _, e := range file.Events {
			el, exists := lf.Events[e.Name]
			if !exists {
				el = &ModelLock{
					NextID: 1,
					Fields: make(map[string]int),
				}
				lf.Events[e.Name] = el
			}
			for _, p := range e.Params {
				if _, ok := el.Fields[p.Name]; ok {
					continue
				}
				el.Fields[p.Name] = el.NextID
				el.NextID++
			}
		}
	}
}

// updateModels assigns field IDs to all model fields.
func (lf *LockFile) updateModels(files []*ast.File) {
	// Collect current model→field names from AST.
	currentModels := make(map[string]map[string]bool)
	for _, file := range files {
		for _, m := range file.Models {
			fields := make(map[string]bool, len(m.Fields))
			for _, f := range m.Fields {
				if f.Computed != nil {
					continue // computed fields don't get IDs
				}
				fields[f.Name] = true
			}
			currentModels[m.Name] = fields
		}
	}

	// For each model in the lock: reserve IDs for removed fields.
	for name, ml := range lf.Models {
		current, exists := currentModels[name]
		if !exists {
			// Model removed entirely — keep lock entry, all fields become reserved.
			lf.reserveAllFields(ml)
			continue
		}
		lf.reserveRemovedFields(ml, current)
	}

	// For each model in the AST: assign IDs to new fields.
	for _, file := range files {
		for _, m := range file.Models {
			lf.assignModelFields(m)
		}
	}
}

// assignModelFields ensures every non-computed field in a model has an ID.
func (lf *LockFile) assignModelFields(m *ast.ModelDecl) {
	ml, exists := lf.Models[m.Name]
	if !exists {
		ml = &ModelLock{
			NextID: 1,
			Fields: make(map[string]int),
		}
		lf.Models[m.Name] = ml
	}
	for _, f := range m.Fields {
		if f.Computed != nil {
			continue
		}
		if _, ok := ml.Fields[f.Name]; ok {
			continue // already assigned
		}
		ml.Fields[f.Name] = ml.NextID
		ml.NextID++
	}
}

// reserveAllFields moves all field IDs to reserved.
func (lf *LockFile) reserveAllFields(ml *ModelLock) {
	reserved := make(map[int]bool, len(ml.Reserved))
	for _, id := range ml.Reserved {
		reserved[id] = true
	}
	for name, id := range ml.Fields {
		if !reserved[id] {
			ml.Reserved = append(ml.Reserved, id)
		}
		delete(ml.Fields, name)
	}
}

// reserveRemovedFields reserves IDs for fields that no longer exist.
func (lf *LockFile) reserveRemovedFields(ml *ModelLock, current map[string]bool) {
	reserved := make(map[int]bool, len(ml.Reserved))
	for _, id := range ml.Reserved {
		reserved[id] = true
	}
	for name, id := range ml.Fields {
		if !current[name] {
			if !reserved[id] {
				ml.Reserved = append(ml.Reserved, id)
			}
			delete(ml.Fields, name)
		}
	}
}

// updateAPIs assigns stable IDs to all API declarations, including CRUD APIs.
func (lf *LockFile) updateAPIs(files []*ast.File) {
	// CRUD APIs from models with @crud
	for _, file := range files {
		for _, m := range file.Models {
			if !hasCrudDirective(m) {
				continue
			}
			name := m.Name
			plural := name + "s"
			// CRUD param names are well-known
			lf.ensureAPIID("get"+name, []string{"id"})
			lf.ensureAPIID("list"+plural, []string{"page", "pageSize"})
			lf.ensureAPIID("create"+name, collectFieldNames(m))
			lf.ensureAPIID("update"+name, append([]string{"id"}, collectFieldNames(m)...))
			lf.ensureAPIID("delete"+name, []string{"id"})
			lf.ensureAPIID("delete"+plural, []string{"ids"})
		}
	}
	// Declared APIs — params from AST
	for _, file := range files {
		for _, api := range file.APIs {
			var params []string
			for _, p := range api.Params {
				params = append(params, p.Name)
			}
			lf.ensureAPIID(api.Name, params)
		}
	}
}

func collectFieldNames(m *ast.ModelDecl) []string {
	var names []string
	for _, f := range m.Fields {
		if f.Computed != nil {
			continue
		}
		names = append(names, f.Name)
	}
	return names
}

func (lf *LockFile) ensureAPIID(name string, params []string) {
	al, ok := lf.APIs[name]
	if !ok {
		lf.nextAPI++
		al = &APILock{
			ID:     lf.nextAPI,
			Params: make(map[string]int),
			nextP:  0,
		}
		lf.APIs[name] = al
	}
	if al.Params == nil {
		al.Params = make(map[string]int)
	}
	// Compute nextP from existing params
	for _, id := range al.Params {
		if id > al.nextP {
			al.nextP = id
		}
	}
	// Assign IDs to new params
	for _, p := range params {
		if _, exists := al.Params[p]; !exists {
			al.nextP++
			al.Params[p] = al.nextP
		}
	}
}

func hasCrudDirective(m *ast.ModelDecl) bool {
	for _, d := range m.Directives {
		if d.Name == "crud" {
			return true
		}
	}
	return false
}

// computeNextAPI finds the max API ID for new assignments.
func (lf *LockFile) computeNextAPI() {
	maxID := 0
	for _, al := range lf.APIs {
		if al.ID > maxID {
			maxID = al.ID
		}
	}
	lf.nextAPI = maxID
}

// sortReserved sorts reserved IDs for deterministic output.
func (lf *LockFile) sortReserved() {
	for _, ml := range lf.Models {
		if len(ml.Reserved) > 0 {
			sort.Ints(ml.Reserved)
		}
	}
}

// FieldID returns the assigned field ID for a model field, or 0 if not found.
func (lf *LockFile) FieldID(model, field string) int {
	ml, ok := lf.Models[model]
	if !ok {
		return 0
	}
	return ml.Fields[field]
}

// APIID returns the assigned API ID, or 0 if not found.
func (lf *LockFile) APIID(name string) int {
	if al, ok := lf.APIs[name]; ok {
		return al.ID
	}
	return 0
}

// APIParamID returns the assigned param field ID for an API param, or 0 if not found.
func (lf *LockFile) APIParamID(apiName, paramName string) int {
	al, ok := lf.APIs[apiName]
	if !ok || al.Params == nil {
		return 0
	}
	return al.Params[paramName]
}

// EventFieldID returns the assigned param ID for an event param, or 0 if not found.
func (lf *LockFile) EventFieldID(eventName, paramName string) int {
	if lf.Events == nil {
		return 0
	}
	el, ok := lf.Events[eventName]
	if !ok {
		return 0
	}
	return el.Fields[paramName]
}
