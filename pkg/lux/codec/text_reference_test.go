package codec

import (
	"fmt"
	"unsafe"
)

// Frozen immediately before strict String UTF-8 validation. Allocation preflight
// and previous boundary checks remain enabled; only valid text is compared.

func readStringBeforeUTF8(buf []byte, off int) (string, int) {
	b, n := ReadBytes(buf, off)
	if n == 0 {
		return "", 0
	}
	return string(b), n
}

func (d *Decoder) readStringBeforeUTF8() string {
	v, n := readStringBeforeUTF8(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid string at offset %d", d.off)
		return ""
	}
	d.off += n
	return v
}

func (d *Decoder) readStringPtrBeforeUTF8() *string {
	present, n := ReadNullable(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid nullable at offset %d", d.off)
		return nil
	}
	d.off += n
	if !present {
		return nil
	}
	v := d.readStringBeforeUTF8()
	return &v
}

func (d *Decoder) readStringArenaBeforeUTF8(arena []byte, arenaOff *int) string {
	b, n := ReadBytes(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid string at offset %d", d.off)
		return ""
	}
	d.off += n
	if len(b) == 0 {
		return ""
	}
	start := *arenaOff
	end := start + len(b)
	if end > len(arena) {
		// Arena too small — fall back to regular allocation (no panic)
		return string(b)
	}
	copy(arena[start:end], b)
	*arenaOff = end
	return unsafe.String(&arena[start], len(b))
}

func (d *Decoder) skipStringBeforeUTF8() {
	_, n := ReadBytes(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid length-prefixed at offset %d", d.off)
		return
	}
	d.off += n
}

func (r *ColumnarReader) readColumnStringArenaBeforeUTF8(arena []byte, arenaOff *int) []string {
	if !r.checkElements(1) {
		return nil
	}
	result := make([]string, 0, r.count)
	for i := 0; i < r.count; i++ {
		b, n := ReadBytes(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated string column at record %d", i)
			return nil
		}
		r.off += n
		if len(b) == 0 {
			result = append(result, "")
			continue
		}
		start := *arenaOff
		end := start + len(b)
		if end > len(arena) {
			// Arena too small — fall back to regular allocation (no panic)
			result = append(result, string(b))
			continue
		}
		copy(arena[start:end], b)
		*arenaOff = end
		result = append(result, unsafe.String(&arena[start], len(b)))
	}
	return result
}

func (r *ColumnarReader) skipColumnStringBeforeUTF8() {
	for i := 0; i < r.count; i++ {
		_, n := ReadBytes(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated string column at record %d", i)
			return
		}
		r.off += n
	}
}
