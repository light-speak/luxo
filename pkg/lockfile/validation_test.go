package lockfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/lux/codec"
)

func lockValidationPath(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "lock-validation-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	return filepath.Join(dir, "luxo.lock")
}

func TestLoadRejectsCorruptIdentifierAssignments(t *testing.T) {
	for _, raw := range []string{
		`null`,
		`{"version":0}`,
		`{"models":null}`,
		`{"apis":null}`,
		`{"apis":{"a":null}}`,
		`{"models":{"M":null}}`,
		`{"types":{"T":null}}`,
		`{"events":{"E":null}}`,
		`{"apis":{"a":{"id":0}}}`,
		`{"apis":{"a":{"id":-1}}}`,
		`{"apis":{"a":{"id":1},"b":{"id":1}}}`,
		`{"apis":{"a":{"id":1}},"reserved_api_ids":[1]}`,
		`{"reserved_api_ids":[2,2]}`,
		`{"reserved_api_ids":[0]}`,
		`{"apis":{"a":{"id":1,"params":{"x":1,"y":1}}}}`,
		`{"apis":{"a":{"id":1,"params":{"x":0}}}}`,
		`{"apis":{"a":{"id":1,"params":{"x":2147483646}}}}`,
		`{"apis":{"a":{"id":1,"params":{"x":1},"reserved":[1]}}}`,
		`{"models":{"M":{"next_id":1,"fields":{"x":1}}}}`,
		`{"models":{"M":{"next_id":3,"reserved":[3]}}}`,
		`{"models":{"M":{"next_id":3,"fields":{"x":1,"y":1}}}}`,
		`{"types":{"T":{"next_id":0}}}`,
		`{"events":{"E":{"next_id":2,"fields":{"x":-1}}}}`,
		`{"models":{"":{"next_id":1}}}`,
		`{"apis":{"":{"id":1}}}`,
		`{"apis":{"a":{"id":1,"params":{"":1}}}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			path := lockValidationPath(t)
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if got, err := Load(path); err == nil || got != nil {
				t.Fatalf("corrupt lock accepted: %#v %v", got, err)
			}
		})
	}
}

func TestValidateNilAndFutureLock(t *testing.T) {
	var missing *LockFile
	if err := missing.Validate(); err == nil {
		t.Fatal("nil lock accepted")
	}
	lf := New()
	lf.Version = currentVersion + 1
	if err := lf.Validate(); err == nil {
		t.Fatal("future version accepted")
	}
}

func TestExhaustedIdentifiersNeverPersistWrappedAssignments(t *testing.T) {
	for _, kind := range []string{"model", "api", "param"} {
		t.Run(kind, func(t *testing.T) {
			path := lockValidationPath(t)
			lf := New()
			switch kind {
			case "model":
				lf.Models["M"] = &ModelLock{NextID: codec.MaxSelectionFieldID + 1, Fields: map[string]int{}}
			case "api":
				lf.ReservedAPI = []int{codec.MaxAPIID}
			case "param":
				lf.APIs["a"] = &APILock{ID: 1, Reserved: []int{MaxParameterID}}
			}
			if err := lf.Save(path); err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			lf, err = Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "model" {
				lf.assignModelFields(model("M", field("x", "Int")))
			} else {
				lf.ensureAPIID("a", []string{"x"})
			}
			if err := lf.Save(path); err == nil {
				t.Fatal("exhausted namespace saved invalid IDs")
			}
			current, err := os.ReadFile(path)
			if err != nil || string(current) != string(original) {
				t.Fatal("exhaustion replaced persisted lock")
			}
		})
	}
}

func TestSaveDoesNotOverwriteWithCorruptAssignments(t *testing.T) {
	path := lockValidationPath(t)
	if err := os.WriteFile(path, []byte("preserve original"), 0600); err != nil {
		t.Fatal(err)
	}
	lf := New()
	lf.APIs["a"] = &APILock{ID: 1}
	lf.APIs["b"] = &APILock{ID: 1}
	if err := lf.Save(path); err == nil {
		t.Fatal("invalid lock saved")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "preserve original" {
		t.Fatalf("original replaced: %q %v", data, err)
	}
}

func TestIdentifierSpacesAreIndependent(t *testing.T) {
	path := lockValidationPath(t)
	raw := `{"version":2,"models":{"M":{"next_id":4,"fields":{"id":1},"reserved":[3,2]}},"types":{"T":{"next_id":2,"fields":{"x":1}}},"events":{"E":{"next_id":2,"fields":{"x":1}}},"apis":{"a":{"id":1,"params":{"x":1},"reserved":[2]},"b":{"id":2,"params":{"x":1}}},"reserved_api_ids":[3]}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	lf, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if lf.nextAPI != 3 {
		t.Fatalf("next API reused reservation: %d", lf.nextAPI)
	}
	if err := lf.Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), `"reserved"`) {
		t.Fatalf("reservation lost: %s %v", data, err)
	}
}

func TestClassifiedIdentifierLimits(t *testing.T) {
	for _, group := range []struct {
		name  string
		limit int
	}{
		{"models", 65535}, {"types", 65535}, {"events", 2147483647},
	} {
		t.Run(group.name, func(t *testing.T) {
			for _, reserved := range []bool{false, true} {
				lf := New()
				entry := &ModelLock{NextID: group.limit + 1, Fields: map[string]int{"last": group.limit}}
				if reserved {
					entry.Fields = nil
					entry.Reserved = []int{group.limit}
				}
				switch group.name {
				case "models":
					lf.Models["M"] = entry
				case "types":
					lf.Types = map[string]*ModelLock{"M": entry}
				case "events":
					lf.Events["M"] = entry
				}
				if err := lf.Validate(); err != nil {
					t.Fatalf("maximum valid ID rejected: %v", err)
				}
				entry.NextID++
				if reserved {
					entry.Reserved[0]++
				} else {
					entry.Fields["last"]++
				}
				if err := lf.Validate(); err == nil {
					t.Fatalf("out-of-range ID accepted (reserved=%t)", reserved)
				}
				entry.Fields, entry.Reserved = nil, nil
				if err := lf.Validate(); err == nil {
					t.Fatal("out-of-range next_id accepted")
				}
			}
		})
	}
	for _, reserved := range []bool{false, true} {
		lf := New()
		if reserved {
			lf.ReservedAPI = []int{2147483647}
		} else {
			lf.APIs["last"] = &APILock{ID: 2147483647}
		}
		if err := lf.Validate(); err != nil {
			t.Fatal(err)
		}
		if reserved {
			lf.ReservedAPI[0]++
		} else {
			lf.APIs["last"].ID++
		}
		if err := lf.Validate(); err == nil {
			t.Fatalf("out-of-range API ID accepted (reserved=%t)", reserved)
		}
	}
}
