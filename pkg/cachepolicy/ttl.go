package cachepolicy

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// ParseTTL shares the exact TTL rules between DSL directives and policy files.
// Durations have a single ms/s/m/h/d unit; integers are decimal seconds.
func ParseTTL(text string, duration bool) (time.Duration, bool) {
	amount, unit := text, time.Second
	if duration {
		amount, unit = durationParts(text)
	}
	if unit == 0 || amount == "" || amount[0] < '0' || amount[0] > '9' {
		return 0, false
	}
	value, err := strconv.ParseUint(amount, 10, 64)
	if err != nil || value == 0 || value > uint64(math.MaxInt64/unit) {
		return 0, false
	}
	return time.Duration(value) * unit, true
}

func durationParts(value string) (string, time.Duration) {
	for _, unit := range []struct {
		suffix string
		nanos  time.Duration
	}{
		{"ms", time.Millisecond}, {"s", time.Second}, {"m", time.Minute},
		{"h", time.Hour}, {"d", 24 * time.Hour},
	} {
		if strings.HasSuffix(value, unit.suffix) {
			return strings.TrimSuffix(value, unit.suffix), unit.nanos
		}
	}
	return "", 0
}
