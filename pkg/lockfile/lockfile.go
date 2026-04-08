package lockfile

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/light-speak/luxo/pkg/ast"
)

// LockFile represents the luxo.lock file that tracks stable field IDs
// for binary protocol compatibility.
type LockFile struct {
	Version int                   `json:"version"`
	Models  map[string]*ModelLock `json:"models"`
	APIs    map[string]int        `json:"apis"`
	nextAPI int                   // transient: next API ID to assign
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
	b = append(b, []byte(fmt.Sprintf("%d", ml.NextID))...)

	b = append(b, `,"fields":{`...)
	for i, e := range entries {
		if i > 0 {
			b = append(b, ',')
		}
		key, _ := json.Marshal(e.Name)
		b = append(b, key...)
		b = append(b, ':')
		b = append(b, []byte(fmt.Sprintf("%d", e.ID))...)
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
		APIs:    make(map[string]int),
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
	lf.updateAPIs(files)
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

// updateAPIs assigns stable IDs to all API declarations.
func (lf *LockFile) updateAPIs(files []*ast.File) {
	for _, file := range files {
		for _, api := range file.APIs {
			if _, ok := lf.APIs[api.Name]; ok {
				continue
			}
			lf.APIs[api.Name] = lf.nextAPI + 1
			lf.nextAPI++
		}
	}
}

// computeNextAPI finds the max API ID for new assignments.
func (lf *LockFile) computeNextAPI() {
	maxID := 0
	for _, id := range lf.APIs {
		if id > maxID {
			maxID = id
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
	return lf.APIs[name]
}
