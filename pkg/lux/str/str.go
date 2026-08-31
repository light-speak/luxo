package str

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// === Case conversion (used by codegen + runtime) ===

// ToSnakeCase converts camelCase/PascalCase to snake_case.
// Handles consecutive uppercase (acronyms): "userID" → "user_id", "HTMLParser" → "html_parser".
func ToSnakeCase(s string) string {
	runes := []rune(s)
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i, r := range runes {
		if unicode.IsUpper(r) && i > 0 {
			prev := runes[i-1]
			// Insert _ before: lowercase→Upper, or Upper→Upper when next is lowercase (end of acronym)
			if unicode.IsLower(prev) {
				b.WriteByte('_')
			} else if unicode.IsUpper(prev) && i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
				b.WriteByte('_')
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// Capitalize returns the string with the first character uppercased.
func Capitalize(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

// LowerFirst returns the string with the first character lowercased.
func LowerFirst(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToLower(r)) + s[size:]
}

// === Luxo String standard library (compiled from .luxo expressions) ===

// Trim removes leading and trailing whitespace.
func Trim(s string) string {
	return strings.TrimSpace(s)
}

// Lowercase converts the string to lowercase.
func Lowercase(s string) string {
	return strings.ToLower(s)
}

// Uppercase converts the string to uppercase.
func Uppercase(s string) string {
	return strings.ToUpper(s)
}

// Contains reports whether substr is within s.
func Contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// Replace replaces all occurrences of old with new in s.
func Replace(s, old, new string) string {
	return strings.ReplaceAll(s, old, new)
}

// Split splits s by sep.
func Split(s, sep string) []string {
	return strings.Split(s, sep)
}

// StartsWith reports whether s begins with prefix.
func StartsWith(s, prefix string) bool {
	return strings.HasPrefix(s, prefix)
}

// EndsWith reports whether s ends with suffix.
func EndsWith(s, suffix string) bool {
	return strings.HasSuffix(s, suffix)
}

// TrimPrefix removes prefix from s if present.
func TrimPrefix(s, prefix string) string {
	return strings.TrimPrefix(s, prefix)
}

// TrimSuffix removes suffix from s if present.
func TrimSuffix(s, suffix string) string {
	return strings.TrimSuffix(s, suffix)
}

// Repeat returns s repeated n times.
func Repeat(s string, n int) string {
	return strings.Repeat(s, n)
}

// PadLeft pads s on the left with pad to reach length n. Single allocation.
func PadLeft(s string, n int, pad string) string {
	diff := n - len(s)
	if diff <= 0 || pad == "" {
		return s
	}
	var b strings.Builder
	b.Grow(n)
	for b.Len() < diff {
		b.WriteString(pad)
	}
	prefix := b.String()[:diff]
	return prefix + s
}

// PadRight pads s on the right with pad to reach length n. Single allocation.
func PadRight(s string, n int, pad string) string {
	diff := n - len(s)
	if diff <= 0 || pad == "" {
		return s
	}
	var b strings.Builder
	b.Grow(n)
	b.WriteString(s)
	for b.Len() < n {
		b.WriteString(pad)
	}
	return b.String()[:n]
}

// Mask applies partial masking, preserving prefix and suffix characters.
// Mask("13812345678", 3, 4) → "138****5678"
func Mask(s string, keepPrefix, keepSuffix int) string {
	runes := []rune(s)
	n := len(runes)
	if keepPrefix+keepSuffix >= n {
		return s
	}
	masked := make([]rune, n)
	copy(masked, runes)
	for i := keepPrefix; i < n-keepSuffix; i++ {
		masked[i] = '*'
	}
	return string(masked)
}

// MaskPattern applies a positional mask where '#' preserves a rune and '*'
// replaces it. Invalid patterns and rune-count mismatches fail closed.
func MaskPattern(s, pattern string) string {
	for i := range len(pattern) {
		if pattern[i] != '#' && pattern[i] != '*' {
			return maskAll(s)
		}
	}
	if utf8.RuneCountInString(s) != len(pattern) {
		return maskAll(s)
	}

	var b strings.Builder
	b.Grow(len(s))
	if len(s) == len(pattern) {
		for i := range len(pattern) {
			if pattern[i] == '#' {
				b.WriteByte(s[i])
			} else {
				b.WriteByte('*')
			}
		}
		return b.String()
	}

	position := 0
	for _, r := range s {
		if pattern[position] == '#' {
			b.WriteRune(r)
		} else {
			b.WriteByte('*')
		}
		position++
	}
	return b.String()
}

func maskAll(s string) string {
	return strings.Repeat("*", utf8.RuneCountInString(s))
}

// MaskEmail masks an email: "user@example.com" → "u***@example.com"
func MaskEmail(s string) string {
	at := strings.IndexByte(s, '@')
	if at <= 1 {
		return s
	}
	return s[:1] + strings.Repeat("*", at-1) + s[at:]
}

// Reverse returns s with its UTF-8 runes in reverse order.
func Reverse(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}
