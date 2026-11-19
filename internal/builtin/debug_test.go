package builtin

import (
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
)

func TestDebugMethod(t *testing.T) {
	for name, want := range map[string]string{"d": "Debug", "i": "Info", "w": "Warn", "e": "Error", "": "", "Debug": "", "size": ""} {
		if got := DebugMethod(name); got != want {
			t.Errorf("%q: %q, want %q", name, got, want)
		}
	}
}

func TestEnumMembersAreNotDebugProperties(t *testing.T) {
	member := &ast.MemberExpr{Field: "d"}
	if !IsDebugProperty(member) {
		t.Fatal("debug property not recognized")
	}
	member.EnumConstant = true
	if IsDebugProperty(member) {
		t.Fatal("enum member misclassified as diagnostic")
	}
	member.EnumConstant = false
	member.DeclaredField = true
	if IsDebugProperty(member) {
		t.Fatal("declared field misclassified as diagnostic")
	}
}
