package codec

import (
	"errors"
	"time"

	"github.com/light-speak/luxo/pkg/lux/datetime"
)

// ErrInvalidDateTime rejects incomplete pairs and nanoseconds outside [0, 1e9).
var ErrInvalidDateTime = errors.New("codec: invalid DateTime seconds/nanoseconds pair")

// AppendDateTime appends floor Unix seconds followed by unsigned nanoseconds.
// time.Time already normalizes the fractional component to [0, 1e9).
func AppendDateTime(dst []byte, value time.Time) ([]byte, error) {
	seconds := value.Unix()
	if !datetime.ValidSeconds(seconds) {
		return dst, ErrInvalidDateTime
	}
	dst = AppendSvarint(dst, seconds)
	return AppendVarint(dst, uint64(value.Nanosecond())), nil
}

// ReadDateTime reads an exact seconds/nanoseconds pair. A zero size means invalid
// input; it never normalizes invalid nanoseconds or falls back to integer seconds.
func ReadDateTime(buf []byte, off int) (time.Time, int) {
	seconds, nanos, size := readTimestampParts(buf, off)
	if size == 0 {
		return time.Time{}, 0
	}
	return time.Unix(seconds, int64(nanos)).UTC(), size
}

func readTimestampParts(buf []byte, off int) (int64, uint64, int) {
	if off < 0 || off >= len(buf) {
		return 0, 0, 0
	}
	seconds, size := ReadSvarint(buf, off)
	if size == 0 || !datetime.ValidSeconds(seconds) {
		return 0, 0, 0
	}
	nanos, count := ReadVarint(buf, off+size)
	if count <= 0 || nanos >= 1_000_000_000 {
		return 0, 0, 0
	}
	return seconds, nanos, size + count
}

// WriteDateTime writes an exact timestamp without a field header.
func (e *Encoder) WriteDateTime(value time.Time) error {
	var err error
	e.buf, err = AppendDateTime(e.buf, value)
	return err
}

// WriteFieldDateTime writes a field ID and an exact timestamp.
func (e *Encoder) WriteFieldDateTime(fieldID int, value time.Time) error {
	dst, err := AppendDateTime(AppendVarint(e.buf, uint64(fieldID)), value)
	if err == nil {
		e.buf = dst
	}
	return err
}

// WriteFieldDateTimePtr preserves the distinction between null and zero time.
func (e *Encoder) WriteFieldDateTimePtr(fieldID int, value *time.Time) error {
	dst := AppendVarint(e.buf, uint64(fieldID))
	if value == nil {
		e.buf = AppendNull(dst)
		return nil
	}
	dst, err := AppendDateTime(AppendPresent(dst), *value)
	if err == nil {
		e.buf = dst
	}
	return err
}

// WriteFieldDateTimeArray writes a count-prefixed timestamp array.
func (e *Encoder) WriteFieldDateTimeArray(fieldID int, values []time.Time) error {
	dst := AppendArrayHeader(AppendVarint(e.buf, uint64(fieldID)), len(values))
	for _, value := range values {
		var err error
		dst, err = AppendDateTime(dst, value)
		if err != nil {
			return err
		}
	}
	e.buf = dst
	return nil
}

// ReadDateTime reads one timestamp, preserving the decoder's first error.
func (d *Decoder) ReadDateTime() time.Time {
	if d.err != nil {
		return time.Time{}
	}
	value, size := ReadDateTime(d.buf, d.off)
	if size == 0 {
		d.err = ErrInvalidDateTime
		return time.Time{}
	}
	d.off += size
	return value
}

// ReadDateTimePtr reads a nullable timestamp; a failed read never returns a value.
func (d *Decoder) ReadDateTimePtr() *time.Time {
	if !d.readTimestampPresent() {
		return nil
	}
	value := d.ReadDateTime()
	if d.err != nil {
		return nil
	}
	return &value
}

// ReadDateTimeArray checks the minimum two-byte payload before allocating.
func (d *Decoder) ReadDateTimeArray() []time.Time {
	if d.err != nil {
		return nil
	}
	count := d.ReadArrayLength()
	if !d.CheckElementBytes(count, 2) {
		return nil
	}
	values := make([]time.Time, count)
	for i := range values {
		values[i] = d.ReadDateTime()
		if d.err != nil {
			return nil
		}
	}
	return values
}

// SkipDateTime validates both components without allocating or constructing time.
func (d *Decoder) SkipDateTime() {
	if d.err != nil {
		return
	}
	_, _, size := readTimestampParts(d.buf, d.off)
	if size == 0 {
		d.err = ErrInvalidDateTime
		return
	}
	d.off += size
}

// SkipNullableDateTime validates the presence flag and any timestamp payload.
func (d *Decoder) SkipNullableDateTime() {
	if d.readTimestampPresent() {
		d.SkipDateTime()
	}
}

func (d *Decoder) readTimestampPresent() bool {
	if d.err != nil {
		return false
	}
	present, size := ReadNullable(d.buf, d.off)
	if size == 0 {
		d.err = ErrInvalidDateTime
		return false
	}
	d.off += size
	return present
}
