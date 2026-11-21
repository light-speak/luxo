package scripts_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Container creation may return both a handle and an error when Ryuk or a
// startup hook fails. Register cleanup before Fatal/return on every such path,
// including the actual Go programs embedded in code-generation test fixtures.
func TestContainerFixturesRegisterCleanupBeforeErrors(t *testing.T) {
	checked := 0
	err := filepath.WalkDir("../pkg", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !hasContainerFactory(string(source)) {
			return nil
		}
		checked += checkContainerCleanupSource(t, path, string(source))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("container fixture audit checked no creation sites")
	}
}

func hasContainerFactory(source string) bool {
	return strings.Contains(source, "postgres.Run(") || strings.Contains(source, "testcontainers.GenericContainer(")
}

func TestContainerFixtureAuditReadsCompleteStringExpressions(t *testing.T) {
	prefix := "package generated\nfunc fixture() {\ncontainer, err := postgres.Run(ctx)\n"
	suffix := "testcontainers.CleanupContainer(t, container)\nif err != nil { t.Fatal(err) }\n}\n"
	for name, expression := range map[string]string{
		"literal":      strconv.Quote(prefix + suffix),
		"concatenated": strconv.Quote(prefix) + " + (" + strconv.Quote(suffix) + ")",
	} {
		t.Run(name, func(t *testing.T) {
			source := "package fixture\nconst program = " + expression
			if checked := checkContainerCleanupSource(t, name, source); checked != 1 {
				t.Fatalf("checked %d creation sites, want 1", checked)
			}
		})
	}
}

func checkContainerCleanupSource(t *testing.T, name, source string) int {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, source, 0)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	ast.Inspect(file, func(node ast.Node) bool {
		if expression, ok := node.(ast.Expr); ok {
			if value, constant := containerFixtureString(expression); constant {
				if strings.HasPrefix(value, "package ") && hasContainerFactory(value) {
					checked += checkContainerCleanupSource(t, name+"/embedded", value)
				}
				// A concatenated program must be checked once as a complete file,
				// not again as syntactically incomplete literal fragments.
				return false
			}
		}
		block, ok := node.(*ast.BlockStmt)
		if !ok {
			return true
		}
		for i, statement := range block.List {
			variable := createdContainerVariable(statement)
			if variable == "" {
				continue
			}
			checked++
			if i+1 >= len(block.List) || !cleansCreatedContainer(block.List[i+1], variable) {
				t.Errorf("%s: register CleanupContainer before checking the startup error for %s", fset.Position(statement.Pos()), variable)
			}
		}
		return true
	})
	return checked
}

func containerFixtureString(expression ast.Expr) (string, bool) {
	switch value := expression.(type) {
	case *ast.BasicLit:
		if value.Kind == token.STRING {
			text, err := strconv.Unquote(value.Value)
			return text, err == nil
		}
	case *ast.ParenExpr:
		return containerFixtureString(value.X)
	case *ast.BinaryExpr:
		if value.Op == token.ADD {
			left, leftOK := containerFixtureString(value.X)
			right, rightOK := containerFixtureString(value.Y)
			if leftOK && rightOK {
				return left + right, true
			}
		}
	}
	return "", false
}

func TestContainerFixtureStringRejectsNonconstantExpressions(t *testing.T) {
	for _, source := range []string{"42", "name", `"left" - "right"`, `"left" + name`, `name + "right"`} {
		expression, err := parser.ParseExpr(source)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := containerFixtureString(expression); ok {
			t.Fatalf("accepted nonconstant string %s", source)
		}
	}
}

func TestContainerFixtureAuditRejectsMissingCleanup(t *testing.T) {
	const childMode = "LUXO_TEST_CONTAINER_AUDIT_FAILURE"
	if mode := os.Getenv(childMode); mode != "" {
		prefix := "package generated\nfunc fixture() {\ncontainer, err := postgres.Run(ctx)\n"
		suffix := "if err != nil { t.Fatal(err) }\n"
		if mode == "late" {
			suffix += "testcontainers.CleanupContainer(t, container)\n"
		}
		source := "package fixture\nconst program = " + strconv.Quote(prefix) + " + " + strconv.Quote(suffix+"}\n")
		checkContainerCleanupSource(t, mode, source)
		return
	}
	for _, mode := range []string{"missing", "late"} {
		t.Run(mode, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestContainerFixtureAuditRejectsMissingCleanup$")
			command.Env = append(os.Environ(), childMode+"="+mode)
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "register CleanupContainer before checking the startup error") {
				t.Fatalf("unsafe fixture was not rejected: %v\n%s", err, output)
			}
		})
	}
}

func createdContainerVariable(statement ast.Stmt) string {
	assignment, ok := statement.(*ast.AssignStmt)
	if !ok || len(assignment.Lhs) != 2 || len(assignment.Rhs) != 1 {
		return ""
	}
	call, ok := assignment.Rhs[0].(*ast.CallExpr)
	if !ok || (!namedContainerCall(call, "postgres", "Run") && !namedContainerCall(call, "testcontainers", "GenericContainer")) {
		return ""
	}
	variable, ok := assignment.Lhs[0].(*ast.Ident)
	if !ok {
		return ""
	}
	return variable.Name
}

func cleansCreatedContainer(statement ast.Stmt, variable string) bool {
	expression, ok := statement.(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := expression.X.(*ast.CallExpr)
	if !ok || !namedContainerCall(call, "testcontainers", "CleanupContainer") || len(call.Args) < 2 {
		return false
	}
	argument, ok := call.Args[1].(*ast.Ident)
	return ok && argument.Name == variable
}

func namedContainerCall(call *ast.CallExpr, pkg, name string) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != name {
		return false
	}
	qualifier, ok := selector.X.(*ast.Ident)
	return ok && qualifier.Name == pkg
}
