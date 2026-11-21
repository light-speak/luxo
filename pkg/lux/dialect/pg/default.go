package pg

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/light-speak/luxo/pkg/lux"
)

// DefaultSQL uses typed constants so SQL parsing cannot change Float64 rounding
// or confuse SQL NULL with JSON null and the string "null".
func (Dialect) DefaultSQL(value lux.ColumnDefault) (string, error) {
	switch value.Kind {
	case lux.DefaultNull:
		return "NULL", nil
	case lux.DefaultString:
		if !utf8.ValidString(value.Text) || strings.IndexByte(value.Text, 0) >= 0 {
			return "", fmt.Errorf("PostgreSQL text defaults require valid UTF-8 without NUL")
		}
		return quoteDefaultString(value.Text), nil
	case lux.DefaultInt:
		return strconv.FormatInt(value.Int, 10), nil
	case lux.DefaultFloat:
		if math.IsNaN(value.Float) || math.IsInf(value.Float, 0) {
			return "", fmt.Errorf("Float default must be finite")
		}
		return "'" + strconv.FormatFloat(value.Float, 'g', -1, 64) + "'::DOUBLE PRECISION", nil
	case lux.DefaultBoolean:
		if value.Bool {
			return "TRUE", nil
		}
		return "FALSE", nil
	case lux.DefaultDuration:
		if value.Int%1000 != 0 {
			return "", fmt.Errorf("PostgreSQL Duration defaults require whole microseconds")
		}
		return "INTERVAL '" + strconv.FormatInt(value.Int/1000, 10) + " microseconds'", nil
	case lux.DefaultJSONNull:
		return "'null'::JSONB", nil
	default:
		return "", fmt.Errorf("unsupported column default kind %d", value.Kind)
	}
}

func quoteDefaultString(value string) string {
	value = strings.ReplaceAll(value, "'", "''")
	if strings.ContainsRune(value, '\\') {
		return "E'" + strings.ReplaceAll(value, `\`, `\\`) + "'"
	}
	return "'" + value + "'"
}
