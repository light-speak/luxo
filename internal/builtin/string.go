package builtin

import "github.com/light-speak/luxo/pkg/ast"

// StringParameter is a compile-time binding, never emitted as runtime metadata.
type StringParameter struct{ Name, Type string }

type StringSignature struct {
	MinArgs, MaxArgs int
	Parameters       [2]StringParameter
}

// StringMethod describes only implemented operations and their static arguments.
func StringMethod(name string) (StringSignature, bool) {
	switch name {
	case "lowercase", "uppercase", "trim", "reversed", "toInt", "toFloat":
		return StringSignature{}, true
	case "trimStart", "trimEnd":
		return StringSignature{0, 1, [2]StringParameter{{"cutset", "String"}}}, true
	case "contains":
		return StringSignature{1, 1, [2]StringParameter{{"substr", "String"}}}, true
	case "startsWith":
		return StringSignature{1, 1, [2]StringParameter{{"prefix", "String"}}}, true
	case "endsWith":
		return StringSignature{1, 1, [2]StringParameter{{"suffix", "String"}}}, true
	case "matches":
		return StringSignature{1, 1, [2]StringParameter{{"pattern", "String"}}}, true
	case "split":
		return StringSignature{1, 1, [2]StringParameter{{"sep", "String"}}}, true
	case "replace", "replaceAll":
		return StringSignature{2, 2, [2]StringParameter{{"old", "String"}, {"new", "String"}}}, true
	case "substring":
		return StringSignature{1, 2, [2]StringParameter{{"start", "Int"}, {"end", "Int"}}}, true
	case "repeat":
		return StringSignature{1, 1, [2]StringParameter{{"count", "Int"}}}, true
	case "padStart", "padEnd":
		return StringSignature{2, 2, [2]StringParameter{{"length", "Int"}, {"pad", "String"}}}, true
	default:
		return StringSignature{}, false
	}
}

type StringBindingIssue struct {
	Kind string
	Name string
}

// Bind maps declaration positions to source positions without rearranging ASTs.
func (s StringSignature) Bind(arguments []*ast.NamedArg) ([2]int, StringBindingIssue) {
	positions := [2]int{-1, -1}
	if len(arguments) < s.MinArgs || len(arguments) > s.MaxArgs {
		return positions, StringBindingIssue{Kind: "count"}
	}
	namedSeen := false
	for index, argument := range arguments {
		position := index
		if argument.Name != "" {
			namedSeen = true
			position = s.parameterIndex(argument.Name)
			if position < 0 {
				return positions, StringBindingIssue{Kind: "unknown", Name: argument.Name}
			}
		} else if namedSeen {
			return positions, StringBindingIssue{Kind: "positional after named"}
		}
		if positions[position] >= 0 {
			return positions, StringBindingIssue{Kind: "duplicate", Name: s.Parameters[position].Name}
		}
		positions[position] = index
	}
	for position := range s.MinArgs {
		if positions[position] < 0 {
			return positions, StringBindingIssue{Kind: "missing", Name: s.Parameters[position].Name}
		}
	}
	return positions, StringBindingIssue{}
}

func (s StringSignature) parameterIndex(name string) int {
	for position := range s.MaxArgs {
		if s.Parameters[position].Name == name {
			return position
		}
	}
	return -1
}
