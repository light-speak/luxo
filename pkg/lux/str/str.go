package str

import (
	"strings"
	"unicode"
)

// === Case conversion (used by codegen + runtime) ===

// ToSnakeCase converts camelCase/PascalCase to snake_case.
func ToSnakeCase(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i, r := range s {
		if unicode.IsUpper(r) && i > 0 {
			b.WriteByte('_')
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
	return string(unicode.ToUpper(rune(s[0]))) + s[1:]
}

// LowerFirst returns the string with the first character lowercased.
func LowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
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

// PadLeft pads s on the left with pad to reach length n.
func PadLeft(s string, n int, pad string) string {
	for len(s) < n {
		s = pad + s
	}
	return s
}

// PadRight pads s on the right with pad to reach length n.
func PadRight(s string, n int, pad string) string {
	for len(s) < n {
		s = s + pad
	}
	return s
}
