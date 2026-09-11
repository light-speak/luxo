package codegen

import (
	"strconv"
	"strings"
)

type generatedImports uint32

func (imports generatedImports) has(name string) bool { return imports&generatedPackageBit(name) != 0 }

// generatedPackages recognizes package-root selectors without allocating tokens
// or reparsing a Go AST. Go literal boundaries come from the standard library;
// comments/whitespace preserve the preceding identifier, other tokens clear it.
// This scan is generation-only and retains no state between output files.
func generatedPackages(body string) generatedImports {
	var imports generatedImports
	identifier := ""
	afterDot := false
	for index := 0; index < len(body); {
		ch := body[index]
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			index++
			continue
		}
		if ch == '/' && index+1 < len(body) && (body[index+1] == '/' || body[index+1] == '*') {
			index = skipGeneratedComment(body, index)
			continue
		}
		if ch == '"' || ch == '\'' || ch == '`' {
			quoted, err := strconv.QuotedPrefix(body[index:])
			if err != nil {
				return imports
			} // Invalid output is rejected by the generator's format/parse step.
			index += len(quoted)
			identifier, afterDot = "", false
			continue
		}
		if generatedIdentifierByte(ch) {
			start := index
			for index < len(body) && generatedIdentifierByte(body[index]) {
				index++
			}
			identifier = ""
			if !afterDot {
				identifier = body[start:index]
			}
			afterDot = false
			continue
		}
		if ch == '.' {
			imports |= generatedPackageBit(identifier)
		}
		identifier, afterDot = "", ch == '.'
		index++
	}
	return imports
}

func generatedIdentifierByte(ch byte) bool {
	return ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch >= 128
}

func skipGeneratedComment(body string, start int) int {
	terminator := "\n"
	if body[start+1] == '*' {
		terminator = "*/"
	}
	if end := strings.Index(body[start+2:], terminator); end >= 0 {
		return start + 2 + end + len(terminator)
	}
	return len(body)
}

func generatedPackageBit(name string) generatedImports {
	switch name {
	case "strconv":
		return 1 << 0
	case "strings":
		return 1 << 1
	case "luvia":
		return 1 << 2
	case "luxocrypto":
		return 1 << 3
	case "time":
		return 1 << 4
	case "json":
		return 1 << 5
	case "fmt":
		return 1 << 6
	case "regexp":
		return 1 << 7
	case "lux":
		return 1 << 8
	case "errors":
		return 1 << 9
	case "selection":
		return 1 << 10
	case "pg":
		return 1 << 11
	case "uuid":
		return 1 << 12
	case "decimal":
		return 1 << 13
	case "codec":
		return 1 << 14
	case "errgroup":
		return 1 << 15
	case "luxolog":
		return 1 << 16
	default:
		return 0
	}
}
