package schema

import (
	"testing"
)

func FuzzNestedJSONBoundary(f *testing.F) {
	for _, list := range []bool{false, true} {
		_, _, child := nestedBoundaryFixture(f, list, false)
		for _, raw := range [][]byte{nil, {0}, {0, 0}, {0, 0, 0}, child} {
			f.Add(raw, list, false)
			f.Add(raw, list, true)
		}
	}
	f.Fuzz(func(t *testing.T, child []byte, list, nullable bool) {
		if len(child) > 8192 {
			return
		}
		for _, plainType := range []bool{false, true} {
			checkNestedJSONBoundary(t, child, list, nullable, plainType)
		}
	})
}

func checkNestedJSONBoundary(t *testing.T, child []byte, list, nullable, plainType bool) {
	t.Helper()
	s, parent, _ := nestedBoundaryFixture(t, list, nullable)
	model := s.Models["Child"]
	if plainType {
		s.RegisterType(&TypeDecl{Name: model.Name, Fields: model.Fields})
		delete(s.Models, model.Name)
	}
	var value []byte
	var childErr error
	if list {
		value, childErr = BinaryListToJSON(nil, child, model, s)
	} else {
		value, childErr = BinaryToJSON(nil, child, model, s)
	}
	row := `{"id":42,"child":` + string(value) + `}`
	wants := map[string]string{"row": row, "column": "[" + row + "]", "page": `{"items":[` + row + `],"total":1,"page":1,"pageSize":10}`}
	for path, convert := range nestedBoundaryConverters(t, parent, child, true, s) {
		got, err := convert([]byte("prefix"))
		if childErr != nil {
			if err == nil || string(got) != "prefix" {
				t.Fatalf("%s lost child failure: %s / %v", path, got, err)
			}
		} else if err != nil || string(got) != "prefix"+wants[path] {
			t.Fatalf("%s changed child: %s / %v", path, got, err)
		}
	}
}
