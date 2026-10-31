package codec

import "fmt"

// Frozen before unsigned metadata checks. Benchmarks compare the same valid
// bytes and retain the original call structure; malformed inputs are not a
// performance baseline because the old implementation can panic.

func readArrayHeaderBeforeUnsigned(buf []byte, off int) (count int, n int) {
	v, n := ReadVarint(buf, off)
	if n <= 0 {
		return 0, 0
	}
	return int(v), n
}

func (d *Decoder) readArrayLengthBeforeUnsigned() int {
	count, n := readArrayHeaderBeforeUnsigned(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid array header at offset %d", d.off)
		return 0
	}
	if count > MaxArrayElements {
		d.err = fmt.Errorf("codec: array size %d exceeds limit %d at offset %d", count, MaxArrayElements, d.off)
		return 0
	}
	d.off += n
	return count
}

func (d *Decoder) skipArrayBeforeUnsigned(elementType FieldSkipType) {
	count, n := readArrayHeaderBeforeUnsigned(d.buf, d.off)
	if n == 0 {
		d.err = fmt.Errorf("codec: invalid array header at offset %d", d.off)
		return
	}
	if count > MaxArrayElements {
		d.err = fmt.Errorf("codec: array size %d exceeds limit %d at offset %d", count, MaxArrayElements, d.off)
		return
	}
	d.off += n
	for range count {
		d.SkipValue(elementType)
		if d.err != nil {
			return
		}
	}
}

func (d *Decoder) nextFieldBeforeUnsigned() bool {
	if d.err != nil {
		return false
	}
	if d.off >= len(d.buf) {
		d.err = fmt.Errorf("codec: missing end marker at offset %d", d.off)
		return false
	}
	id, n := ReadVarint(d.buf, d.off)
	if n <= 0 {
		if n < 0 {
			d.err = fmt.Errorf("codec: varint overflow at offset %d", d.off)
		} else {
			d.err = fmt.Errorf("codec: truncated varint at offset %d", d.off)
		}
		return false
	}
	d.off += n
	d.fieldID = int(id)
	return d.fieldID != 0 // 0 = end marker
}

func (d *Decoder) skipArenaHeaderBeforeUnsigned() {
	_, n := ReadVarint(d.buf, d.off)
	if n <= 0 {
		d.err = fmt.Errorf("codec: invalid arena header varint at offset %d", d.off)
		return
	}
	d.off += n
}

func (r *ColumnarReader) nextColumnBeforeUnsigned() bool {
	if r.err != nil || r.off >= len(r.buf) {
		return false
	}
	id, n := ReadVarint(r.buf, r.off)
	if n <= 0 {
		return false
	}
	if id == 0 {
		r.off += n // advance past 0x00 end marker
		return false
	}
	r.off += n
	r.fieldID = int(id)
	return true
}
