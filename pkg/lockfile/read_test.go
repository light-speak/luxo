package lockfile

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestLoadRejectsAmbiguousJSONMetadata(t *testing.T) {
	for _, raw := range []string{
		`{"version":1,"version":2}`,
		`{"version":1,"ver\u0073ion":2}`,
		`{"models":{"M":{"next_id":3,"fields":{"id":1,"id":2}}}}`,
		`{"apis":{"a":{"id":1,"id":2}}}`,
		`{"apis":{"a":{"id":1},"a":{"id":2}}}`,
		`{"apis":{"a":{"id":1,"params":{"id":1,"id":2}}}}`,
		`{"models":{"M":{"next_id":2,"fields":{"id":1},"field_types":{"id":"Int","id":"String"}}}}`,
		`{"apis":{"a":{"id":1,"contract":true,"contract":false}}}`,
		`{"models":{"\ud800":{"next_id":1,"fields":{}}}}`,
		`{"apis":{"a":{"id":1,"return_type":"\ud800"}}}`,
		"{\"apis\":{\"a\":{\"id\":1,\"return_type\":\"\xff\"}}}",
		`{"version":null}`,
		`{"apis":{"a":{"id":1,"contract":null}}}`,
		`{"apis":{"a":{"id":1,"return_type":null}}}`,
		`{"apis":{"a":{"id":1,"param_types":{"id":null}}}}`,
		`{"types":null}`,
		`{"reserved_api_ids":[null]}`,
		`{"reserved_api_ids":null}`,
		`{"versoin":2}`,
		`{"models":{"M":{"next_id":1,"fieldz":{}}}}`,
		`{"apis":{"a":{"id":1,"contrcat":true}}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			path := lockValidationPath(t)
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if got, err := Load(path); err == nil || got != nil {
				t.Fatalf("ambiguous lock accepted: %#v %v", got, err)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != raw {
				t.Fatal("failed read changed the file")
			}
		})
	}
}

const completeLockJSON = `{"version":2,"models":{"模型":{"next_id":4,"fields":{"id":1,"名称":2},"field_types":{"id":"Int","名称":"String"},"reserved":[3]}},"types":{"Snapshot":{"next_id":2,"fields":{"name":1}}},"events":{"Changed":{"next_id":2,"fields":{"value":1}}},"apis":{"read":{"id":1,"params":{"id":1},"param_types":{"id":"Int"},"return_type":"模型","contract":true,"reserved":[2]},"old":{"id":2,"contract":false}},"reserved_api_ids":[3]}`

func TestReadLockPreservesOmittedDefaults(t *testing.T) {
	for _, raw := range []string{`{}`, `{"version":1}`, `{"version":0}`, `{"types":{}}`, `{"models":{},"apis":{},"events":{}}`} {
		got, err := readLock([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		want := New()
		if err := json.Unmarshal([]byte(raw), want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: omitted defaults changed: got %#v want %#v", raw, got, want)
		}
	}
}

func TestReadLockRoundTrip(t *testing.T) {
	lf, err := readLock([]byte(completeLockJSON))
	if err != nil {
		t.Fatal(err)
	}
	want := New()
	if err := json.Unmarshal([]byte(completeLockJSON), want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(lf, want) {
		t.Fatalf("metadata changed: got %#v want %#v", lf, want)
	}
	path := lockValidationPath(t)
	if err := lf.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want.computeNextAPI()
	if !reflect.DeepEqual(got, want) {
		t.Fatal("save/load changed metadata")
	}
}

func TestReadLockRejectsEveryDuplicateMember(t *testing.T) {
	for _, scope := range []struct {
		prefix, suffix string
		members        map[string]string
	}{
		{"", "", map[string]string{"version": "2", "models": "{}", "types": "{}", "events": "{}", "apis": "{}", "reserved_api_ids": "[]"}},
		{`{"models":{"M":`, `}}`, map[string]string{"next_id": "1", "fields": "{}", "field_types": "{}", "reserved": "[]"}},
		{`{"apis":{"a":`, `}}`, map[string]string{"id": "1", "params": "{}", "param_types": "{}", "return_type": `"Int"`, "contract": "false", "reserved": "[]"}},
	} {
		for name, value := range scope.members {
			for _, alias := range []string{name, fmt.Sprintf(`\u%04x%s`, name[0], name[1:])} {
				raw := scope.prefix + `{"` + name + `":` + value + `,"` + alias + `":` + value + `}` + scope.suffix
				if got, err := readLock([]byte(raw)); err == nil || got != nil {
					t.Fatalf("duplicate member accepted: %s", raw)
				}
			}
		}
	}
	for _, member := range []string{"models", "types", "events", "apis"} {
		raw := `{"` + member + `":{"M":{},"\u004d":{}}}`
		if got, err := readLock([]byte(raw)); err == nil || got != nil {
			t.Fatalf("duplicate declaration accepted: %s", raw)
		}
		nestedMembers := []string{"fields", "field_types"}
		if member == "apis" {
			nestedMembers = []string{"params", "param_types"}
		}
		for _, nested := range nestedMembers {
			value := "1"
			if strings.HasSuffix(nested, "types") {
				value = `"Int"`
			}
			raw := `{"` + member + `":{"M":{"` + nested + `":{"x":` + value + `,"\u0078":` + value + `}}}}`
			if got, err := readLock([]byte(raw)); err == nil || got != nil {
				t.Fatalf("duplicate assignment accepted: %s", raw)
			}
		}
	}
}

func TestReadLockOwnsNamesAndValues(t *testing.T) {
	raw := []byte(completeLockJSON)
	lf, err := readLock(raw)
	if err != nil {
		t.Fatal(err)
	}
	clear(raw)
	if lf.Models["模型"].Fields["名称"] != 2 || lf.APIs["read"].ReturnType != "模型" || lf.APIs["read"].ParamTypes["id"] != "Int" {
		t.Fatal("metadata retained borrowed input")
	}
}

func TestReadLockRejectsTruncationAndIncorrectTypes(t *testing.T) {
	for end := range len(completeLockJSON) {
		if got, err := readLock([]byte(completeLockJSON[:end])); err == nil || got != nil {
			t.Fatalf("accepted truncated lock at %d", end)
		}
	}
	for _, raw := range []string{
		`{} {}`, `{} garbage`, `[]`, `true`, `{"version":1.0}`, `{"version":1e0}`,
		`{"version":9999999999999999999999999999}`, `{"version":"2"}`,
		`{"reserved_api_ids":{}}`, `{"reserved_api_ids":[1,]}`,
		`{"models":{"M":{"next_id":1,"fields":null}}}`,
		`{"models":{"M":{"next_id":1,"field_types":{"id":true}}}}`,
		`{"apis":{"a":{"id":1,"contract":`,
		`{"apis":{"a":{"id":1,"contract":"false"}}}`,
		`{"Version":2}`, `{"events":null}`, `{"apis":{"a":[]}}`,
	} {
		if got, err := readLock([]byte(raw)); err == nil || got != nil {
			t.Fatalf("accepted malformed lock: %s", raw)
		}
	}
}

func TestReadLockPreservesDecodedNames(t *testing.T) {
	lf, err := readLock([]byte(`{"ver\u0073ion":1,"models":{"\ud83d\ude00":{"next_id":2,"fields":{"\u540d":1}}}}`))
	if err != nil || lf.Version != 1 || lf.Models["\U0001f600"].Fields["名"] != 1 {
		t.Fatalf("valid Unicode changed: %#v %v", lf, err)
	}
	// Force multiple decoder buffer refills before checking retained names.
	var raw strings.Builder
	raw.WriteString(`{"apis":{`)
	for i := range 128 {
		if i > 0 {
			raw.WriteByte(',')
		}
		fmt.Fprintf(&raw, `"%s%d":{"id":%d,"return_type":"%s%d"}`, strings.Repeat("a", 128), i, i+1, strings.Repeat("T", 128), i)
	}
	raw.WriteString(`}}`)
	lf, err = readLock([]byte(raw.String()))
	if err != nil {
		t.Fatal(err)
	}
	for i := range 128 {
		a := lf.APIs[fmt.Sprintf("%s%d", strings.Repeat("a", 128), i)]
		if a == nil || a.ID != i+1 || a.ReturnType != fmt.Sprintf("%s%d", strings.Repeat("T", 128), i) {
			t.Fatalf("name/value lifetime broken at %d", i)
		}
	}
}

func TestValidateRejectsNullInMemoryCatalogs(t *testing.T) {
	for _, mutate := range []func(*LockFile){
		func(lf *LockFile) { lf.Models = nil },
		func(lf *LockFile) { lf.APIs = nil },
		func(lf *LockFile) { lf.APIs["a"] = nil },
	} {
		lf := New()
		mutate(lf)
		if err := lf.Validate(); err == nil {
			t.Fatal("null in-memory catalog accepted")
		}
	}
}
