package datetime

import (
	"errors"
	"time"
)

const (
	MinSeconds int64 = -62167219200 // 0000-01-01T00:00:00Z
	MaxSeconds int64 = 253402300799 // 9999-12-31T23:59:59Z
)

var ErrInvalid = errors.New("DateTime requires RFC3339 with UTC year 0000..9999 and at most 9 fractional digits")

// ValidSeconds checks the UTC calendar range without constructing a calendar.
func ValidSeconds(seconds int64) bool {
	return seconds >= MinSeconds && seconds <= MaxSeconds
}

// ParseRFC3339 rejects host-parser extensions and precision loss. Lowercase
// t/z are RFC3339 equivalents; formatting always uses uppercase UTC notation.
func ParseRFC3339(value string) (time.Time, error) {
	if !validTimestampShape(value) {
		return time.Time{}, ErrInvalid
	}
	if value[10] == 't' || value[len(value)-1] == 'z' {
		var normalized [35]byte
		copy(normalized[:], value)
		normalized[10] = 'T'
		if normalized[len(value)-1] == 'z' {
			normalized[len(value)-1] = 'Z'
		}
		value = string(normalized[:len(value)])
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || !ValidSeconds(parsed.Unix()) {
		return time.Time{}, ErrInvalid
	}
	return parsed.UTC(), nil
}

func validTimestampShape(value string) bool {
	if len(value) < 20 || len(value) > 35 {
		return false
	}
	if value[4] != '-' || value[7] != '-' || (value[10] != 'T' && value[10] != 't') || value[13] != ':' || value[16] != ':' {
		return false
	}
	zone := 19
	if value[zone] == '.' {
		zone++
		for zone < len(value) && value[zone] >= '0' && value[zone] <= '9' {
			zone++
		}
		if zone == 20 || zone > 29 {
			return false
		}
	}
	if zone >= len(value) {
		return false
	}
	if value[zone] == 'Z' || value[zone] == 'z' {
		return zone+1 == len(value)
	}
	if len(value)-zone != 6 || (value[zone] != '+' && value[zone] != '-') || value[zone+3] != ':' {
		return false
	}
	return validPair(value[zone+1:zone+3], 23) && validPair(value[zone+4:], 59)
}

func validPair(value string, maximum int) bool {
	return value[0] >= '0' && value[0] <= '9' && value[1] >= '0' && value[1] <= '9' &&
		int(value[0]-'0')*10+int(value[1]-'0') <= maximum
}
