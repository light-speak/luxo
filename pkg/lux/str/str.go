package str

import (
	"encoding/binary"
	"errors"
	"math/bits"
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
		upper := r >= 'A' && r <= 'Z'
		lower := r
		if upper {
			lower += 'a' - 'A'
		} else if r >= utf8.RuneSelf {
			upper = unicode.IsUpper(r)
			lower = unicode.ToLower(r)
		}
		if upper && i > 0 {
			var next rune
			if i+1 < len(runes) {
				next = runes[i+1]
			}
			if snakeWordBoundary(runes[i-1], next) {
				b.WriteByte('_')
			}
		}
		if lower < utf8.RuneSelf {
			b.WriteByte(byte(lower))
		} else {
			b.WriteRune(lower)
		}
	}
	return b.String()
}

// CapitalizedASCIISnakeCaseLen measures ToSnakeCase(Capitalize(s)) for
// NUL-free ASCII inputs without constructing either intermediate string.
func CapitalizedASCIISnakeCaseLen(s string) (int, bool) {
	if s == "" {
		return 0, true
	}
	first := s[0]
	if first == 0 || first >= utf8.RuneSelf {
		return 0, false
	}
	previous := rune(first)
	if previous >= 'a' && previous <= 'z' {
		previous -= 'a' - 'A'
	}
	size := len(s)
	for i := 1; i < len(s); i++ {
		current := rune(s[i])
		if current == 0 || current >= utf8.RuneSelf {
			return 0, false
		}
		if current >= 'A' && current <= 'Z' {
			var next rune
			if i+1 < len(s) {
				next = rune(s[i+1])
			}
			if snakeWordBoundary(previous, next) {
				size++
			}
		}
		previous = current
	}
	return size, true
}

func snakeWordBoundary(previous, next rune) bool {
	// Start a word after lowercase or at the end of an acronym.
	return unicode.IsLower(previous) || unicode.IsUpper(previous) && unicode.IsLower(next)
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

var errNegativeRepeatCount = errors.New("repeat count must not be negative")
var errRepeatLengthOverflow = errors.New("repeat result length overflows int")
var errSubstringRange = errors.New("substring range is out of bounds")
var errSubstringBoundary = errors.New("substring range splits a UTF-8 character")

// Substring validates the existing byte-indexed range before returning a view.
// Character-counting policy is separate; no successful result splits UTF-8.
func Substring(s string, start, end int) (string, error) {
	if start < 0 || end < start || end > len(s) {
		return "", errSubstringRange
	}
	if start < len(s) && !utf8.RuneStart(s[start]) || end < len(s) && !utf8.RuneStart(s[end]) {
		return "", errSubstringBoundary
	}
	return s[start:end], nil
}

// SubstringFrom returns the suffix from a checked byte boundary.
func SubstringFrom(s string, start int) (string, error) {
	return Substring(s, start, len(s))
}

// Repeat rejects invalid counts and length overflow before allocating its result.
func Repeat(s string, n int) (string, error) {
	if n < 0 {
		return "", errNegativeRepeatCount
	}
	if n == 0 || s == "" {
		return "", nil
	}
	if n == 1 {
		return s, nil
	}
	high, size := bits.Mul(uint(len(s)), uint(n))
	if high != 0 || size > uint(^uint(0)>>1) {
		return "", errRepeatLengthOverflow
	}
	return strings.Repeat(s, n), nil
}

// PadLeft pads s on the left up to byte length n, retaining complete UTF-8 characters.
func PadLeft(s string, n int, pad string) string {
	if n <= len(s) || pad == "" {
		return s
	}
	return padString(s, n, pad, true)
}

// PadRight pads s on the right up to byte length n, retaining complete UTF-8 characters.
func PadRight(s string, n int, pad string) string {
	if n <= len(s) || pad == "" {
		return s
	}
	return padString(s, n, pad, false)
}

func padString(s string, n int, pad string, left bool) string {
	// Single-byte patterns always end on a byte boundary.
	if len(pad) > 1 {
		// Only the final pattern can be partial; round its prefix down to a rune boundary.
		remainder := (n - len(s)) % len(pad)
		prefix := remainder
		for prefix > 0 && !utf8.RuneStart(pad[prefix]) {
			prefix--
		}
		n -= remainder - prefix
		if n == len(s) {
			return s
		}
	}
	var b strings.Builder
	b.Grow(n)
	if !left {
		b.WriteString(s)
	}
	appendPadding(&b, pad, n-len(s))
	if left {
		b.WriteString(s)
	}
	return b.String()
}

func appendPadding(b *strings.Builder, pad string, size int) {
	start := b.Len()
	b.WriteString(pad[:min(len(pad), size)])
	for written := b.Len() - start; written < size; written = b.Len() - start {
		count := min(written, size-written)
		b.WriteString(b.String()[start : start+count])
	}
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
	if len(s) != len(pattern) && len(s) <= shortMaskInput {
		return maskShortUTF8(s, pattern)
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

const shortMaskInput = 32

func maskShortUTF8(s, pattern string) string {
	// A malformed input byte may expand into a three-byte replacement rune.
	var storage [3 * shortMaskInput]byte
	masked := storage[:0]
	position := 0
	for _, r := range s {
		if position == len(pattern) {
			return maskAll(s)
		}
		if pattern[position] == '#' {
			masked = utf8.AppendRune(masked, r)
		} else {
			masked = append(masked, '*')
		}
		position++
	}
	if position != len(pattern) {
		return maskAll(s)
	}
	return string(masked)
}

func maskAll(s string) string {
	return strings.Repeat("*", utf8.RuneCountInString(s))
}

// MaskEmail preserves the first UTF-8 rune of the local part and masks the rest.
// It formats an email rather than validating its address syntax.
func MaskEmail(s string) string {
	at := strings.IndexByte(s, '@')
	if at <= 1 {
		return s
	}
	_, firstSize := utf8.DecodeRuneInString(s)
	if firstSize == at {
		return s
	}
	local := s[firstSize:at]
	hidden := 0
	// Skip full ASCII words; leave all other decoding to the standard library.
	for len(local) >= 8 && binary.LittleEndian.Uint64([]byte(local))&0x8080808080808080 == 0 {
		hidden += 8
		local = local[8:]
	}
	hidden += utf8.RuneCountInString(local)
	var b strings.Builder
	b.Grow(len(s) - at + firstSize + hidden)
	b.WriteString(s[:firstSize])
	appendPadding(&b, "*", hidden)
	b.WriteString(s[at:])
	return b.String()
}

// Reverse returns s with its UTF-8 runes in reverse order.
func Reverse(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}
