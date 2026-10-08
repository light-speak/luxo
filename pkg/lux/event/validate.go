package event

import (
	"math"
	"time"
	"unicode/utf8"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/datetime"
	"github.com/light-speak/luxo/pkg/lux/jsonutil"
)

// These checks share the wire domain without writing an intermediate buffer.
func CheckString(value string) error {
	if !utf8.ValidString(value) {
		return codec.ErrInvalidUTF8
	}
	return nil
}

func CheckFloat(value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return codec.ErrNonFiniteFloat
	}
	return nil
}

func CheckDateTime(value time.Time) error {
	if !datetime.ValidSeconds(value.Unix()) {
		return codec.ErrInvalidDateTime
	}
	return nil
}

func CheckJSON(value []byte) error {
	if !jsonutil.ValidUTF8JSON(value) {
		return codec.ErrInvalidJSON
	}
	return nil
}
