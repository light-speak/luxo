package semantic

import (
	"testing"

	"github.com/light-speak/luxo/internal/builtin"
)

func TestStringMethodsRejectInvalidArguments(t *testing.T) {
	for _, sample := range []struct {
		method, result, unwrap string
		arguments              []string
	}{
		{"lowercase", "String", "", []string{"1", "tick()"}},
		{"uppercase", "String", "", []string{"1"}},
		{"trim", "String", "", []string{"1"}},
		{"reversed", "String", "", []string{"1"}},
		{"toInt", "Int", "?", []string{"1"}},
		{"toFloat", "Float", "?", []string{"1"}},
		{"trimStart", "String", "", []string{`"x", "y"`, "1", "null", "optionalText"}},
		{"trimEnd", "String", "", []string{`"x", "y"`, "1", "[\"x\"]"}},
		{"contains", "Boolean", "", []string{"", `"x", tick()`, "1", "null", "optionalText", `["x"]`}},
		{"startsWith", "Boolean", "", []string{"", `"x", "y"`, "1"}},
		{"endsWith", "Boolean", "", []string{"", `"x", "y"`, "1"}},
		{"matches", "Boolean", "", []string{"", `"x", "y"`, "1"}},
		{"split", "[String]", "", []string{"", `"x", "y"`, "1"}},
		{"replace", "String", "", []string{"", `"x"`, `"x", "y", "z"`, `1, "x"`, `"x", 1`, `"x", optionalText`}},
		{"replaceAll", "String", "", []string{`"x"`, `"x", "y", "z"`, `"x", null`}},
		{"substring", "String", "", []string{"", "0, 1, 2", "0.5", `"x"`, "0, optionalCount", "[0]"}},
		{"repeat", "String", "", []string{"", "1, 2", "0.5", `"x"`, "null"}},
		{"padStart", "String", "", []string{"", "1", `1, "x", "y"`, `"x", "y"`, `1, 2`, `optionalCount, "x"`}},
		{"padEnd", "String", "", []string{"", "1", `1, "x", "y"`, `1, null`}},
	} {
		for _, arguments := range sample.arguments {
			t.Run(sample.method+"/"+arguments, func(t *testing.T) {
				source := "fn tick(): Int { return 1 } fn run(value: String, optionalText: String?, optionalCount: Int?): " + sample.result + " { return value." + sample.method + "(" + arguments + ")" + sample.unwrap + " }"
				expectError(t, analyze(t, source), "String."+sample.method)
			})
		}
	}
}

func TestStringMethodArgumentControls(t *testing.T) {
	for _, expression := range []string{
		`value.lowercase()`, `value.uppercase()`, `value.trim()`, `value.reversed()`,
		`value.trimStart()`, `value.trimEnd()`, `value.trimStart("x")`, `value.trimEnd("x")`,
		`value.contains("x")`, `value.startsWith("x")`, `value.endsWith("x")`, `value.matches("x")`,
		`value.split("x")`, `value.replace("x", "y")`, `value.replaceAll("x", "y")`,
		`value.substring(0)`, `value.substring(0, 1)`, `value.repeat(1)`,
		`value.padStart(10, "0")`, `value.padEnd(10, "0")`, `value.toInt()?`, `value.toFloat()?`,
	} {
		expectNoErrors(t, analyze(t, "fn run(value: String) { val result = "+expression+" }"))
		expectNoErrors(t, analyze(t, "fn run(value: String?) { val result = "+"value?."+expression[len("value."):]+" }"))
	}
}

func TestStringNamedArgumentsBindByParameter(t *testing.T) {
	for _, expression := range []string{
		`value.replace(new: "y", old: "x")`,
		`value.replaceAll("x", new: "y")`,
		`value.substring(end: 2, start: 0)`,
		`value.substring(start: 0)`,
		`value.padStart(pad: "0", length: 10)`,
		`value.padEnd(length: 10, pad: "0")`,
		`value.trimStart(cutset: "x")`, `value.trimEnd(cutset: "x")`,
		`value.contains(substr: "x")`, `value.startsWith(prefix: "x")`,
		`value.endsWith(suffix: "x")`, `value.matches(pattern: "x")`,
		`value.split(sep: "x")`, `value.repeat(count: 1)`,
	} {
		t.Run(expression, func(t *testing.T) {
			expectNoErrors(t, analyze(t, "fn run(value: String) { val result = "+expression+" }"))
			expectNoErrors(t, analyze(t, "fn run(value: String?) { val result = value?."+expression[len("value."):]+" }"))
		})
	}
}

func TestStringNamedArgumentsRejectInvalidBindings(t *testing.T) {
	for _, expression := range []string{
		`value.replace(wrong: "x", new: "y")`,
		`value.replace(old: "x", old: "y")`,
		`value.replace("x", old: "y")`,
		`value.replace(new: "y", "x")`,
		`value.substring(end: 2)`,
		`value.padStart(pad: 1, length: "x")`,
		`value.trimStart(wrong: "x")`,
		`value.contains(wrong: "x")`,
		`value.repeat(wrong: 1)`,
	} {
		t.Run(expression, func(t *testing.T) {
			expectError(t, analyze(t, "fn run(value: String) { val result = "+expression+" }"), "String.")
		})
	}
}

func TestStringMethodsRejectLambdaArguments(t *testing.T) {
	for _, sample := range []struct{ expression, result string }{
		{`value.contains { "x" }`, "Boolean"},
		{`value.trimStart { "x" }`, "String"},
		{`value.replace("x") { "y" }`, "String"},
		{`value.substring { 0 }`, "String"},
		{`value.repeat { 1 }`, "String"},
		{`value.padStart(10) { "x" }`, "String"},
	} {
		expectError(t, analyze(t, "fn run(value: String): "+sample.result+" { return "+sample.expression+" }"), "String.")
	}
}

func TestStringMethodUnknownArgumentPreservesDiagnostic(t *testing.T) {
	expectError(t, analyze(t, `fn run(value: String): Boolean { return value.contains(missing) }`), "undefined")
}

func TestNullableStringResultPreservesNullability(t *testing.T) {
	for _, sample := range []struct{ method, result string }{{"toInt", "Int"}, {"toFloat", "Float"}} {
		result := analyze(t, "fn run(value: String?): "+sample.result+" { return value?."+sample.method+"()? }")
		if len(result.Errors) == 0 {
			t.Error("nullable Result call became non-null", sample.method)
		}
		expectNoErrors(t, analyze(t, "fn run(value: String?): "+sample.result+"? { return value?."+sample.method+"()? }"))
	}
}

func TestStringBuiltinMethodsHaveSignatures(t *testing.T) {
	for name, field := range BuiltinTypes()["String"].Fields {
		if field.IsMethod {
			if _, found := builtin.StringMethod(name); !found {
				t.Errorf("String.%s has no call signature", name)
			}
		}
	}
}
