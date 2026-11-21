package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestRequestProductionHasNoTargetDecoders(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Recv == nil {
				continue
			}
			if strings.HasPrefix(fn.Name.Name, "LegacyParamJSON") {
				t.Errorf("historical target decoder leaked into production: %s", name)
			}
			switch fn.Name.Name {
			case "ParamJSON", "ParamJSONOptional", "ParamJSONNullable", "ParamJSONOptionalNullable":
				t.Errorf("production file %s still declares arbitrary-target decoder %s", name, fn.Name.Name)
			}
		}
	}
}

func TestRequestBoundariesHaveNoReflectiveJSON(t *testing.T) {
	for _, name := range []string{"request.go", "request_value.go", "request_list.go", "json_params.go", "binary.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		jsonAlias := "json"
		for _, imported := range file.Imports {
			if imported.Path.Value == `"reflect"` {
				t.Errorf("request boundary %s imports reflect", name)
			}
			if imported.Path.Value == `"encoding/json"` && imported.Name != nil {
				jsonAlias = imported.Name.Name
				if jsonAlias == "." {
					t.Errorf("request boundary %s hides JSON calls with a dot import", name)
				}
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			owner, ok := selector.X.(*ast.Ident)
			if ok && owner.Name == jsonAlias {
				switch selector.Sel.Name {
				case "Marshal", "Unmarshal", "NewEncoder", "NewDecoder", "MarshalIndent":
					t.Errorf("request boundary %s still uses reflective JSON %s", name, selector.Sel.Name)
				}
			}
			return true
		})
	}
}
