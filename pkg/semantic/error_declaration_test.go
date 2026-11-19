package semantic

import (
	"fmt"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestErrorDeclarationStatusRange(t *testing.T) {
	for _, code := range []int{0, 99, 200, 399, 600, 999} {
		source := fmt.Sprintf(`error Rejected { code: %d message: "rejected" }`, code)
		expectError(t, analyze(t, source), "error status code must be between 400 and 599")
	}
	expectError(t, analyze(t, `error Rejected { message: "rejected" }`), "error status code must be between 400 and 599")
	for _, code := range []int{400, 422, 500, 599} {
		result := analyze(t, fmt.Sprintf(`error Rejected { code: %d message: "rejected" }`, code))
		if len(result.Errors) != 0 {
			t.Fatal(result.Errors)
		}
	}
}

func TestErrorDeclarationRejectsUnknownFieldTypes(t *testing.T) {
	for _, fieldType := range []string{"Missing", "Missing?", "[Missing]", "Page<Missing>", "(Known, Missing)"} {
		t.Run(fieldType, func(t *testing.T) {
			source := fmt.Sprintf(`type Known { value: Int }
error Rejected(value: %s) { code: 422 message: "error.rejected" }`, fieldType)
			expectError(t, analyze(t, source), "unknown type 'Missing'")
		})
	}
}

func TestErrorDeclarationRejectsDuplicateFields(t *testing.T) {
	result := analyze(t, `error Rejected(value: Int, value: JSON) { code: 422 message: "error.rejected" }`)
	expectError(t, result, "duplicate parameter 'value' in error 'Rejected'")
}

func TestErrorDeclarationRejectsDuplicateNames(t *testing.T) {
	const declaration = `error Rejected(value: Int) { code: 422 message: "rejected" }`
	t.Run("same-file", func(t *testing.T) {
		expectError(t, analyze(t, declaration+"\n"+declaration), "error 'Rejected' already declared")
	})
	t.Run("different-modules", func(t *testing.T) {
		first := parseFileWithName(t, declaration, "origin/task/errors.luxo")
		second := parseFileWithName(t, declaration, "origin/project/errors.luxo")
		expectError(t, AnalyzeModules([]*ast.File{first, second}), "error 'Rejected' already declared")
	})
}

func TestErrorDeclarationValidatesFieldDefaults(t *testing.T) {
	for _, test := range []struct{ field, message string }{
		{`value: Int = "wrong"`, "expects 'Int', got 'String'"},
		{`value: Int = null`, "expects 'Int', got 'null'"},
		{`value: DateTime = now()`, "must be a compile-time constant"},
	} {
		t.Run(test.field, func(t *testing.T) {
			source := fmt.Sprintf(`error Rejected(%s) { code: 422 message: "error.rejected" }`, test.field)
			expectError(t, analyze(t, source), test.message)
		})
	}
}

func TestErrorDeclarationTypeDiagnosticUsesFieldPosition(t *testing.T) {
	file := parseFileWithName(t, `error Rejected(
  value: Missing
) { code: 422 message: "error.rejected" }`, "origin/task/errors.luxo")
	result := Analyze([]*ast.File{file})
	if len(result.Errors) != 1 || result.Errors[0].Pos != file.Errors[0].Fields[0].Pos {
		t.Fatalf("error field diagnostic must identify the field: %+v", result.Errors)
	}
}

func TestErrorDeclarationRetainsKnownFieldTypes(t *testing.T) {
	for _, fieldType := range []string{
		"Int", "Duration", "Float", "String", "Boolean", "DateTime", "UUID", "Decimal", "Bytes", "JSON",
		"Int?", "[Int]", "[JSON]", "[JSON]?", "Details", "Details?", "[Details]", "Status",
	} {
		t.Run(fieldType, func(t *testing.T) {
			source := fmt.Sprintf(`type Details { value: JSON }
enum Status { OPEN CLOSED }
error Rejected(value: %s) { code: 422 message: "error.rejected" }`, fieldType)
			expectNoErrors(t, analyze(t, source))
		})
	}
}

func TestErrorDeclarationRespectsModuleVisibility(t *testing.T) {
	remote := parseFileWithName(t, `model Project { id: Int @id name: String }`, "origin/project/model.luxo")
	for _, fieldType := range []string{"Project", "[Project]", "Project?", "Page<Project>"} {
		t.Run(fieldType, func(t *testing.T) {
			source := fmt.Sprintf(`error Rejected(value: %s) { code: 422 message: "error.rejected" }`, fieldType)
			local := parseFileWithName(t, source, "origin/task/errors.luxo")
			expectError(t, AnalyzeModules([]*ast.File{remote, local}), "type 'Project' is from module 'project'")
		})
	}
	local := parseFileWithName(t, `extend Project { id: Int name: String }
error Rejected(value: Project) { code: 422 message: "error.rejected" }`, "origin/task/errors.luxo")
	expectNoErrors(t, AnalyzeModules([]*ast.File{remote, local}))
}

func TestErrorDeclarationAllowsCommonAndLocalTypes(t *testing.T) {
	for _, modelFile := range []string{"origin/common/model.luxo", "origin/task/model.luxo"} {
		t.Run(modelFile, func(t *testing.T) {
			model := parseFileWithName(t, `type Details { value: JSON }`, modelFile)
			declaration := parseFileWithName(t, `error Rejected(value: Details) { code: 422 message: "error.rejected" }`, "origin/task/errors.luxo")
			expectNoErrors(t, AnalyzeModules([]*ast.File{model, declaration}))
		})
	}
}
