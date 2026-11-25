// Package sqllex scans PostgreSQL comments and quoted tokens without copying.
package sqllex

import "strings"

// IsIdentifierStart reports whether the byte can begin an unquoted identifier.
func IsIdentifierStart(char byte) bool {
	return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char == '_' || char >= 0x80
}

// IsIdentifierByte reports whether the byte can continue an unquoted identifier.
func IsIdentifierByte(char byte) bool {
	return IsIdentifierStart(char) || isSQLDigit(char) || char == '$'
}

// SkipComment returns the end of a PostgreSQL line or nested block comment.
func SkipComment(statement string, index int) (int, bool) {
	if index+1 >= len(statement) {
		return index, false
	}
	if statement[index:index+2] == "--" {
		if end := strings.IndexAny(statement[index+2:], "\r\n"); end >= 0 {
			return index + end + 3, true
		}
		return len(statement), true
	}
	if statement[index:index+2] != "/*" {
		return index, false
	}
	depth := 1
	for cursor := index + 2; cursor+1 < len(statement); cursor += 2 {
		switch statement[cursor : cursor+2] {
		case "/*":
			depth++
		case "*/":
			depth--
			if depth == 0 {
				return cursor + 2, true
			}
		default:
			cursor--
		}
	}
	return len(statement), true
}

// SkipLiteral returns the end of a quoted string, including dollar quotes.
func SkipLiteral(statement string, index int) (int, bool) {
	if statement[index] == '\'' {
		escapes := hasEscapePrefix(statement, index)
		end := SkipQuoted(statement, index+1, '\'', escapes)
		if escapes {
			for next := continuedString(statement, end); next != end; next = continuedString(statement, end) {
				end = SkipQuoted(statement, next+1, '\'', true)
			}
		}
		return end, true
	}
	if statement[index] != '$' || !canStartDollarQuote(statement, index) {
		return index, false
	}
	delimiterEnd := index + 1
	for delimiterEnd < len(statement) && (IsIdentifierStart(statement[delimiterEnd]) || isSQLDigit(statement[delimiterEnd])) {
		delimiterEnd++
	}
	if delimiterEnd == len(statement) || statement[delimiterEnd] != '$' {
		return index, false
	}
	delimiter := statement[index : delimiterEnd+1]
	end := strings.Index(statement[delimiterEnd+1:], delimiter)
	if end < 0 {
		return len(statement), true
	}
	return delimiterEnd + 1 + end + len(delimiter), true
}

func canStartDollarQuote(statement string, index int) bool {
	return index+1 < len(statement) && !isSQLDigit(statement[index+1]) &&
		(index == 0 || !IsIdentifierByte(statement[index-1]))
}

func hasEscapePrefix(statement string, quoteIndex int) bool {
	return quoteIndex > 0 && (statement[quoteIndex-1] == 'e' || statement[quoteIndex-1] == 'E') &&
		(quoteIndex == 1 || !IsIdentifierByte(statement[quoteIndex-2]))
}

// An escape string retains its escape rules in a newline-separated segment.
func continuedString(statement string, end int) int {
	newline := false
	for index := end; index < len(statement); index++ {
		switch statement[index] {
		case ' ', '\t', '\f', '\v':
		case '\n', '\r':
			newline = true
		case '-':
			if index+1 >= len(statement) || statement[index+1] != '-' {
				return end
			}
			index, _ = SkipComment(statement, index)
			index--
			newline = true
		case '\'':
			if newline {
				return index
			}
			return end
		default:
			return end
		}
	}
	return end
}

// SkipQuoted scans from just after an opening quote to the end of the token.
func SkipQuoted(statement string, index int, quote byte, backslashEscapes bool) int {
	for index < len(statement) {
		if backslashEscapes && statement[index] == '\\' && index+1 < len(statement) {
			index += 2
			continue
		}
		if statement[index] != quote {
			index++
			continue
		}
		if index+1 < len(statement) && statement[index+1] == quote {
			index += 2
			continue
		}
		return index + 1
	}
	return len(statement)
}

func isSQLDigit(char byte) bool {
	return char >= '0' && char <= '9'
}
