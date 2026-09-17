package codec

import (
	"fmt"
	"unsafe"
)

// Frozen before allocation preflight. These methods retain the former scalar
// reads, allocation and loops; invalid inputs are only used to measure the
// avoided allocation, never as an equivalent correct decoding baseline.

func (d *Decoder) readIntArrayBeforePayload() []int64 {
	count := d.ReadArrayLength()
	if d.err != nil {
		return nil
	}
	result := make([]int64, 0, count)
	for i := 0; i < count; i++ {
		result = append(result, d.ReadInt())
		if d.err != nil {
			return nil
		}
	}
	return result
}

func (d *Decoder) readFloatArrayBeforePayload() []float64 {
	count := d.ReadArrayLength()
	if d.err != nil {
		return nil
	}
	result := make([]float64, 0, count)
	for i := 0; i < count; i++ {
		result = append(result, d.ReadFloat())
		if d.err != nil {
			return nil
		}
	}
	return result
}

func (d *Decoder) readStringArrayBeforePayload() []string {
	count := d.ReadArrayLength()
	if d.err != nil {
		return nil
	}
	result := make([]string, 0, count)
	for i := 0; i < count; i++ {
		result = append(result, d.ReadString())
		if d.err != nil {
			return nil
		}
	}
	return result
}

func (d *Decoder) readBoolArrayBeforePayload() []bool {
	count := d.ReadArrayLength()
	if d.err != nil {
		return nil
	}
	result := make([]bool, 0, count)
	for i := 0; i < count; i++ {
		result = append(result, d.ReadBool())
		if d.err != nil {
			return nil
		}
	}
	return result
}

func (d *Decoder) readBytesArrayBeforePayload() [][]byte {
	count := d.ReadArrayLength()
	if d.err != nil {
		return nil
	}
	result := make([][]byte, 0, count)
	for i := 0; i < count; i++ {
		result = append(result, d.ReadBytes())
		if d.err != nil {
			return nil
		}
	}
	return result
}

func (d *Decoder) readUUIDArrayBeforePayload() [][16]byte {
	count := d.ReadArrayLength()
	if d.err != nil {
		return nil
	}
	result := make([][16]byte, 0, count)
	for i := 0; i < count; i++ {
		u, un := ReadUUID(d.buf, d.off)
		if un == 0 {
			d.err = fmt.Errorf("codec: truncated uuid at array index %d", i)
			return nil
		}
		d.off += un
		result = append(result, u)
	}
	return result
}

func (r *ColumnarReader) readColumnIntBeforePayload() []int64 {
	result := make([]int64, 0, r.count)
	for i := 0; i < r.count; i++ {
		v, n := ReadSvarint(r.buf, r.off)
		if n <= 0 {
			r.err = fmt.Errorf("codec: truncated int column at record %d", i)
			return nil
		}
		r.off += n
		result = append(result, v)
	}
	return result
}

func (r *ColumnarReader) readColumnFloatBeforePayload() []float64 {
	result := make([]float64, 0, r.count)
	for i := 0; i < r.count; i++ {
		v, n := ReadFixed64(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated float column at record %d", i)
			return nil
		}
		r.off += n
		result = append(result, v)
	}
	return result
}

func (r *ColumnarReader) readColumnStringBeforePayload() []string {
	result := make([]string, 0, r.count)
	for i := 0; i < r.count; i++ {
		v, n := ReadString(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated string column at record %d", i)
			return nil
		}
		r.off += n
		result = append(result, v)
	}
	return result
}

func (r *ColumnarReader) readColumnBoolBeforePayload() []bool {
	result := make([]bool, 0, r.count)
	for i := 0; i < r.count; i++ {
		v, n := ReadBool(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated bool column at record %d", i)
			return nil
		}
		r.off += n
		result = append(result, v)
	}
	return result
}

func (r *ColumnarReader) readColumnBytesBeforePayload() [][]byte {
	result := make([][]byte, 0, r.count)
	for i := 0; i < r.count; i++ {
		v, n := ReadBytes(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated bytes column at record %d", i)
			return nil
		}
		r.off += n
		result = append(result, v)
	}
	return result
}

func (r *ColumnarReader) readColumnUUIDBeforePayload() [][16]byte {
	result := make([][16]byte, 0, r.count)
	for i := 0; i < r.count; i++ {
		u, n := ReadUUID(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated uuid column at record %d", i)
			return nil
		}
		r.off += n
		result = append(result, u)
	}
	return result
}

func (r *ColumnarReader) readColumnIntPtrBeforePayload() []*int64 {
	result := make([]*int64, 0, r.count)
	for i := 0; i < r.count; i++ {
		present, n := ReadNullable(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated nullable at record %d", i)
			return nil
		}
		r.off += n
		if !present {
			result = append(result, nil)
			continue
		}
		v, n := ReadSvarint(r.buf, r.off)
		if n <= 0 {
			r.err = fmt.Errorf("codec: truncated int value at record %d", i)
			return nil
		}
		r.off += n
		result = append(result, &v)
	}
	return result
}

func (r *ColumnarReader) readColumnFloatPtrBeforePayload() []*float64 {
	result := make([]*float64, 0, r.count)
	for i := 0; i < r.count; i++ {
		present, n := ReadNullable(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated nullable at record %d", i)
			return nil
		}
		r.off += n
		if !present {
			result = append(result, nil)
			continue
		}
		v, n := ReadFixed64(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated float value at record %d", i)
			return nil
		}
		r.off += n
		result = append(result, &v)
	}
	return result
}

func (r *ColumnarReader) readColumnStringPtrBeforePayload() []*string {
	result := make([]*string, 0, r.count)
	for i := 0; i < r.count; i++ {
		present, n := ReadNullable(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated nullable at record %d", i)
			return nil
		}
		r.off += n
		if !present {
			result = append(result, nil)
			continue
		}
		v, n := ReadString(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated string value at record %d", i)
			return nil
		}
		r.off += n
		result = append(result, &v)
	}
	return result
}

func (r *ColumnarReader) readColumnBoolPtrBeforePayload() []*bool {
	result := make([]*bool, 0, r.count)
	for i := 0; i < r.count; i++ {
		present, n := ReadNullable(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated nullable at record %d", i)
			return nil
		}
		r.off += n
		if !present {
			result = append(result, nil)
			continue
		}
		v, n := ReadBool(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated bool value at record %d", i)
			return nil
		}
		r.off += n
		result = append(result, &v)
	}
	return result
}

func (r *ColumnarReader) readColumnBytesPtrBeforePayload() []*[]byte {
	result := make([]*[]byte, 0, r.count)
	for i := 0; i < r.count; i++ {
		present, n := ReadNullable(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated nullable at record %d", i)
			return nil
		}
		r.off += n
		if !present {
			result = append(result, nil)
			continue
		}
		v, bn := ReadBytes(r.buf, r.off)
		if bn == 0 {
			r.err = fmt.Errorf("codec: truncated bytes value at record %d", i)
			return nil
		}
		r.off += bn
		b := v
		result = append(result, &b)
	}
	return result
}

func (r *ColumnarReader) readColumnUUIDPtrBeforePayload() []*[16]byte {
	result := make([]*[16]byte, 0, r.count)
	for i := 0; i < r.count; i++ {
		present, n := ReadNullable(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated nullable at record %d", i)
			return nil
		}
		r.off += n
		if !present {
			result = append(result, nil)
			continue
		}
		u, un := ReadUUID(r.buf, r.off)
		if un == 0 {
			r.err = fmt.Errorf("codec: truncated uuid value at record %d", i)
			return nil
		}
		r.off += un
		result = append(result, &u)
	}
	return result
}

func (r *ColumnarReader) readColumnStringArenaBeforePayload(arena []byte, arenaOff *int) []string {
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

func (r *ColumnarReader) readColumnStringPtrArenaBeforePayload(arena []byte, arenaOff *int) []*string {
	result := make([]*string, 0, r.count)
	for i := 0; i < r.count; i++ {
		present, n := ReadNullable(r.buf, r.off)
		if n == 0 {
			r.err = fmt.Errorf("codec: truncated nullable at record %d", i)
			return nil
		}
		r.off += n
		if !present {
			result = append(result, nil)
			continue
		}
		b, bn := ReadBytes(r.buf, r.off)
		if bn == 0 {
			r.err = fmt.Errorf("codec: truncated string value at record %d", i)
			return nil
		}
		r.off += bn
		if len(b) == 0 {
			s := ""
			result = append(result, &s)
			continue
		}
		start := *arenaOff
		end := start + len(b)
		if end > len(arena) {
			s := string(b)
			result = append(result, &s)
			continue
		}
		copy(arena[start:end], b)
		*arenaOff = end
		s := unsafe.String(&arena[start], len(b))
		result = append(result, &s)
	}
	return result
}

func payloadReaderCasesBefore() []payloadReaderCase {
	return []payloadReaderCase{
		{"int", 1, func(d *Decoder) int { return len(d.readIntArrayBeforePayload()) }, func(r *ColumnarReader) int { return len(r.readColumnIntBeforePayload()) }},
		{"float", 8, func(d *Decoder) int { return len(d.readFloatArrayBeforePayload()) }, func(r *ColumnarReader) int { return len(r.readColumnFloatBeforePayload()) }},
		{"string", 1, func(d *Decoder) int { return len(d.readStringArrayBeforePayload()) }, func(r *ColumnarReader) int { return len(r.readColumnStringBeforePayload()) }},
		{"bool", 1, func(d *Decoder) int { return len(d.readBoolArrayBeforePayload()) }, func(r *ColumnarReader) int { return len(r.readColumnBoolBeforePayload()) }},
		{"bytes", 1, func(d *Decoder) int { return len(d.readBytesArrayBeforePayload()) }, func(r *ColumnarReader) int { return len(r.readColumnBytesBeforePayload()) }},
		{"uuid", 16, func(d *Decoder) int { return len(d.readUUIDArrayBeforePayload()) }, func(r *ColumnarReader) int { return len(r.readColumnUUIDBeforePayload()) }},
		{"int-nullable", 1, nil, func(r *ColumnarReader) int { return len(r.readColumnIntPtrBeforePayload()) }},
		{"float-nullable", 1, nil, func(r *ColumnarReader) int { return len(r.readColumnFloatPtrBeforePayload()) }},
		{"string-nullable", 1, nil, func(r *ColumnarReader) int { return len(r.readColumnStringPtrBeforePayload()) }},
		{"bool-nullable", 1, nil, func(r *ColumnarReader) int { return len(r.readColumnBoolPtrBeforePayload()) }},
		{"bytes-nullable", 1, nil, func(r *ColumnarReader) int { return len(r.readColumnBytesPtrBeforePayload()) }},
		{"uuid-nullable", 1, nil, func(r *ColumnarReader) int { return len(r.readColumnUUIDPtrBeforePayload()) }},
		{"string-arena", 1, nil, func(r *ColumnarReader) int { var off int; return len(r.readColumnStringArenaBeforePayload(nil, &off)) }},
		{"string-nullable-arena", 1, nil, func(r *ColumnarReader) int {
			var off int
			return len(r.readColumnStringPtrArenaBeforePayload(nil, &off))
		}},
	}
}
