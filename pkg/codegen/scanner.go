package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/ast"
)

// generateScanner generates a per-model scan function with zero reflection.
//
// For model User { id: Int, name: String, email: String }
// generates:
//
//	func scanUser(rows pg.Rows) (*User, error) {
//	    var v User
//	    fds := rows.FieldDescriptions()
//	    dests := make([]any, len(fds))
//	    for i, fd := range fds {
//	        switch string(fd.Name) {
//	        case "id":    dests[i] = &v.Id
//	        case "name":  dests[i] = &v.Name
//	        case "email": dests[i] = &v.Email
//	        }
//	    }
//	    return &v, rows.Scan(dests...)
//	}
func generateScanner(b *strings.Builder, m *ast.ModelDecl) {
	name := m.Name
	lower := strings.ToLower(name[:1]) + name[1:]
	fnName := "scan" + name

	fmt.Fprintf(b, "// %s scans a row into a %s with zero reflection.\n", fnName, name)
	fmt.Fprintf(b, "func %s(rows pg.Rows) (*%s, error) {\n", fnName, name)
	fmt.Fprintf(b, "\tvar %s %s\n", lower, name)
	fmt.Fprintf(b, "\tfds := rows.FieldDescriptions()\n")
	fmt.Fprintf(b, "\tdests := make([]any, len(fds))\n")
	fmt.Fprintf(b, "\tfor i, fd := range fds {\n")
	fmt.Fprintf(b, "\t\tswitch string(fd.Name) {\n")

	for _, f := range m.Fields {
		if f.Computed != nil {
			continue
		}
		col := toSnakeCase(f.Name)
		goField := capitalize(f.Name)
		fmt.Fprintf(b, "\t\tcase %q:\n", col)
		fmt.Fprintf(b, "\t\t\tdests[i] = &%s.%s\n", lower, goField)
	}

	// default: discard unknown columns safely
	fmt.Fprintf(b, "\t\tdefault:\n")
	fmt.Fprintf(b, "\t\t\tdests[i] = new(any)\n")

	fmt.Fprintf(b, "\t\t}\n")
	fmt.Fprintf(b, "\t}\n")
	fmt.Fprintf(b, "\treturn &%s, rows.Scan(dests...)\n", lower)
	fmt.Fprintf(b, "}\n\n")
}
