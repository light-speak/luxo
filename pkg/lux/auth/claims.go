package auth

import (
	"bytes"
	"encoding/json/jsontext"
	"fmt"
	"strconv"
	"strings"

	"github.com/light-speak/luxo/pkg/lux/jsonutil"
)

// Claims owns immutable verified identity fields. No accessor exposes mutable
// storage; cached instances can be shared safely across authenticated requests.
type Claims struct {
	data *claimData
}

type claimData struct {
	fields map[string]ClaimValue
	raw    string
}

// ClaimValue retains the original JSON number rather than a float64 surrogate.
// Its zero value represents a missing field, distinct from a JSON null.
type ClaimValue struct {
	raw     string
	text    string
	integer int64
	isInt   bool
}

// Raw returns immutable JSON text. Missing values return an empty string.
func (v ClaimValue) Raw() string { return v.raw }

// Int64 reports whether this value is a signed integer without coercion.
func (v ClaimValue) Int64() (int64, bool) { return v.integer, v.isInt }

// Float64 is an explicit, possibly rounding conversion to a finite float64.
func (v ClaimValue) Float64() (float64, bool) {
	if len(v.raw) == 0 || (v.raw[0] != '-' && (v.raw[0] < '0' || v.raw[0] > '9')) {
		return 0, false
	}
	value, err := jsonutil.ParseFloat64Text(v.raw)
	return value, err == nil
}

// String reports a JSON string without coercing another JSON kind.
func (v ClaimValue) String() (string, bool) {
	return v.text, len(v.raw) > 0 && v.raw[0] == '"'
}

// Bool reports a JSON boolean without truthiness conversion.
func (v ClaimValue) Bool() (bool, bool) {
	return v.raw == "true", v.raw == "true" || v.raw == "false"
}

func (c Claims) Get(key string) ClaimValue {
	if c.data == nil {
		return ClaimValue{}
	}
	return c.data.fields[key]
}

func (c Claims) Raw() string {
	if c.data == nil {
		return ""
	}
	return c.data.raw
}

func (c Claims) Has(key string) bool      { return c.Get(key).raw != "" }
func (c Claims) ID() int64                { return c.Int("id") }
func (c Claims) Int(key string) int64     { value, _ := c.Get(key).Int64(); return value }
func (c Claims) String(key string) string { value, _ := c.Get(key).String(); return value }
func (c Claims) Bool(key string) bool     { value, _ := c.Get(key).Bool(); return value }
func (c Claims) Float(key string) float64 { value, _ := c.Get(key).Float64(); return value }

func readClaimsObject(reader *jsonutil.Reader, raw []byte) (Claims, error) {
	start, err := reader.ReadToken()
	if err != nil {
		return Claims{}, err
	}
	if start.Kind() != '{' {
		return Claims{}, fmt.Errorf("JWT identity must be an object")
	}
	begin := int(reader.InputOffset()) - 1
	claims := &claimData{fields: make(map[string]ClaimValue)}
	text := jsonutil.NewTextBuffer(len(raw) - begin)
	for {
		key, err := reader.ReadToken()
		if err != nil {
			return Claims{}, err
		}
		if key.Kind() == '}' {
			break
		}
		name := text.Copy([]byte(key.String()))
		value, err := reader.ReadValue()
		if err != nil {
			return Claims{}, err
		}
		claims.fields[name] = validatedClaimValue(value, &text)
	}
	claims.raw = string(raw[begin:reader.InputOffset()])
	return Claims{data: claims}, nil
}

func finishClaimsObject(reader *jsonutil.Reader, raw []byte) error {
	if len(bytes.Trim(raw[reader.InputOffset():], " \t\r\n")) != 0 {
		return fmt.Errorf("unexpected data after JWT claims")
	}
	return nil
}

// Only the strict reader calls this after validating the complete JSON value,
// including UTF-8 and surrogate pairs. Unquoting cannot fail at this boundary.
func validatedClaimValue(raw []byte, text *jsonutil.TextBuffer) ClaimValue {
	value := ClaimValue{raw: text.Copy(raw)}
	if raw[0] == '"' {
		if !strings.ContainsRune(value.raw, '\\') {
			value.text = value.raw[1 : len(value.raw)-1]
		} else {
			var scratch [128]byte
			decoded, _ := jsontext.AppendUnquote(scratch[:0], raw)
			value.text = text.Copy(decoded)
		}
		return value
	}
	if (raw[0] == '-' || raw[0] >= '0' && raw[0] <= '9') && !strings.ContainsAny(value.raw, ".eE") {
		number, err := strconv.ParseInt(value.raw, 10, 64)
		if err == nil {
			value.integer, value.isInt = number, true
		}
	}
	return value
}
