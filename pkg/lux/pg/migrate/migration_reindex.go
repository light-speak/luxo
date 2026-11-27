package migrate

import (
	"strconv"
	"strings"

	"github.com/light-speak/luxo/internal/sqllex"
)

func (c *migrationTokens) reindexAutocommit() bool {
	var concurrent bool
	if c.accept("(") {
		var complete bool
		concurrent, complete = c.reindexOptions()
		if !complete {
			return false
		}
	}
	switch strings.ToUpper(c.next()) {
	case "SCHEMA", "DATABASE", "SYSTEM":
		return true
	case "INDEX", "TABLE":
		// A relation named concurrently may also start a qualified name.
		return concurrent || c.accept("CONCURRENTLY") && !c.accept(".")
	default:
		return false
	}
}

func (c *migrationTokens) reindexOptions() (concurrent, complete bool) {
	for {
		isConcurrent := c.reindexConcurrentOption()
		start := c.pos
		for token := c.next(); ; token = c.next() {
			switch token {
			case "":
				return false, false
			case ",", ")":
				if isConcurrent {
					concurrent = reindexBoolean(c.sql[start : c.pos-1])
				}
				if token == ")" {
					return concurrent, true
				}
			default:
				continue
			}
			break
		}
	}
}

func (c *migrationTokens) reindexConcurrentOption() bool {
	token := c.next()
	if strings.EqualFold(token, "CONCURRENTLY") {
		return true
	}
	if strings.HasPrefix(token, `"`) || c.reindexUnicodePrefix(token, '"') {
		value, ok := c.reindexOptionString(token)
		return ok && value == "concurrently"
	}
	return false
}

// PostgreSQL utility options use defGetBoolean, not the SQL bool input type.
func reindexBoolean(sql string) bool {
	c := migrationTokens{sql: sql}
	token := c.next()
	if token == "" {
		return true // An omitted argument is true.
	}
	if token[0] >= '0' && token[0] <= '9' || token == "+" || token == "-" {
		return c.reindexIntegerOne(token)
	}
	value, ok := c.reindexOptionString(token)
	return ok && c.next() == "" && (strings.EqualFold(value, "true") || strings.EqualFold(value, "on"))
}

func (c *migrationTokens) reindexIntegerOne(token string) bool {
	if token == "-" {
		return false
	}
	if token == "+" {
		token = c.next()
	}
	if token == "" || token[0] < '0' || token[0] > '9' {
		return false
	}
	start := c.pos - len(token)
	for c.pos < len(c.sql) && sqllex.IsIdentifierByte(c.sql[c.pos]) {
		c.pos++
	}
	raw := c.sql[start:c.pos]
	// PostgreSQL accepts explicit radix prefixes and decimal leading zeroes.
	base := 10
	if len(raw) > 2 && raw[0] == '0' && strings.ContainsRune("bBoOxX", rune(raw[1])) {
		base = 0
	} else if strings.ContainsRune(raw, '_') {
		// A decimal leading zero is never Go's implicit octal in PostgreSQL.
		for i := 1; i+1 < len(raw); i++ {
			if raw[i] == '_' && (raw[i-1] < '0' || raw[i-1] > '9' || raw[i+1] < '0' || raw[i+1] > '9') {
				return false
			}
		}
		if strings.HasSuffix(raw, "_") {
			return false
		}
		raw = strings.ReplaceAll(raw, "_", "")
	}
	value, err := strconv.ParseInt(raw, base, 32)
	return err == nil && value == 1 && c.next() == ""
}

func (c *migrationTokens) reindexUnicodePrefix(token string, quote byte) bool {
	return strings.EqualFold(token, "U") && c.pos+1 < len(c.sql) && c.sql[c.pos] == '&' && c.sql[c.pos+1] == quote
}

func (c *migrationTokens) reindexOptionString(token string) (string, bool) {
	switch {
	case token[0] == '$':
		if i := strings.IndexByte(token[1:], '$') + 2; i > 1 && len(token) >= 2*i && strings.HasSuffix(token[i:], token[:i]) {
			return token[i : len(token)-i], true
		}
		return "", false
	case token[0] == '"' || token[0] == '\'':
		return c.reindexQuoted(c.pos-len(token), false)
	case strings.EqualFold(token, "E") && c.pos < len(c.sql) && c.sql[c.pos] == '\'':
		return c.reindexQuoted(c.pos, true)
	case c.reindexUnicodePrefix(token, '\'') || c.reindexUnicodePrefix(token, '"'):
		return c.reindexUnicodeString()
	default:
		return token, true
	}
}

func (c *migrationTokens) reindexQuoted(start int, escaped bool) (string, bool) {
	quote := c.sql[start]
	var body strings.Builder
	for {
		end := sqllex.SkipQuoted(c.sql, start+1, quote, escaped)
		if end <= start+1 || c.sql[end-1] != quote {
			return "", false
		}
		c.pos = end
		part := c.sql[start+1 : end-1]
		if escaped {
			var ok bool
			part, ok = decodeReindexEscapes(part, '\\', false)
			if !ok {
				return "", false
			}
		}
		next := end
		if quote == '\'' {
			next = sqllex.ContinuedString(c.sql, end)
		}
		if next == end && body.Len() == 0 {
			return part, true
		}
		body.WriteString(part)
		if next == end {
			return body.String(), true
		}
		start = next
	}
}

func (c *migrationTokens) reindexUnicodeString() (string, bool) {
	value, escape, ok := c.unicodeEscapeSource()
	if !ok {
		return "", false
	}
	return decodeReindexEscapes(value, escape, true)
}

func (c *migrationTokens) unicodeEscapeSource() (string, byte, bool) {
	value, ok := c.reindexQuoted(c.pos+1, false)
	if !ok {
		return "", 0, false
	}
	escape := byte('\\')
	if c.accept("UESCAPE") {
		token := c.next()
		if token == "" || token[0] != '\'' && token[0] != '$' && !strings.EqualFold(token, "E") {
			return "", 0, false
		}
		arg, valid := c.reindexOptionString(token)
		if !valid || len(arg) != 1 || strings.ContainsRune("0123456789abcdefABCDEF+'\" \t\n\r\f\v", rune(arg[0])) {
			return "", 0, false
		}
		escape = arg[0]
	}
	return value, escape, true
}

// Only ASCII option names and boolean words need to be decoded.
func decodeReindexEscapes(value string, escape byte, unicode bool) (string, bool) {
	if strings.IndexByte(value, escape) < 0 {
		return value, true
	}
	var decoded strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != escape {
			decoded.WriteByte(value[i])
			continue
		}
		i++
		if i == len(value) {
			return "", false
		}
		var next byte
		var width int
		var ok bool
		if unicode {
			next, width, ok = reindexUnicodeEscape(value[i:], escape)
		} else {
			next, width, ok = reindexBackslashEscape(value[i:])
		}
		if !ok {
			return "", false
		}
		decoded.WriteByte(next)
		i += width - 1
	}
	return decoded.String(), true
}

func reindexUnicodeEscape(value string, escape byte) (byte, int, bool) {
	switch value[0] {
	case escape:
		return escape, 1, true
	case '+':
		return reindexEscapeNumber(value, 1, 6, 6, 16)
	default:
		return reindexEscapeNumber(value, 0, 4, 4, 16)
	}
}

func reindexBackslashEscape(value string) (byte, int, bool) {
	switch value[0] {
	case 'u':
		return reindexEscapeNumber(value, 1, 4, 4, 16)
	case 'U':
		return reindexEscapeNumber(value, 1, 8, 8, 16)
	case 'x':
		decoded, width, ok := reindexEscapeNumber(value, 1, 1, 2, 16)
		if width == 1 {
			// PostgreSQL treats \x without a hex digit like any escaped letter.
			return 'x', 1, true
		}
		return decoded, width, ok
	case '0', '1', '2', '3', '4', '5', '6', '7':
		return reindexEscapeNumber(value, 0, 1, 3, 8)
	case 'b', 'f', 'n', 'r', 't':
		// A control character cannot be part of any accepted option word.
		return 0, 0, false
	default:
		return value[0], 1, true
	}
}

func reindexEscapeNumber(value string, start, min, max, base int) (byte, int, bool) {
	i, number := start, 0
	for ; i < len(value) && i-start < max; i++ {
		digit := strings.IndexByte("0123456789abcdefABCDEF", value[i])
		if digit >= 16 {
			digit -= 6
		}
		if digit < 0 || digit >= base {
			break
		}
		number = number*base + digit
	}
	if base == 8 {
		number = int(byte(number))
	}
	return byte(number), i, i-start >= min && number > 0 && number < 128
}
