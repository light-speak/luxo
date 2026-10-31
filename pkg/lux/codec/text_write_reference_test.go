package codec

// Frozen before the String writer contract changed. These helpers are only a
// valid-input performance reference, never a way to encode invalid test data.
func appendStringBeforeUTF8(dst []byte, value string) []byte {
	dst = AppendVarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func writeStringValueBeforeUTF8(e *Encoder, value string, _ []string) error {
	e.buf = appendStringBeforeUTF8(e.buf, value)
	return nil
}

func writeStringFieldBeforeUTF8(e *Encoder, value string, _ []string) error {
	e.buf = AppendVarint(e.buf, 256)
	e.buf = appendStringBeforeUTF8(e.buf, value)
	return nil
}

func writeStringPtrBeforeUTF8(e *Encoder, value string, _ []string) error {
	e.writeStringPtrBeforeUTF8(256, &value)
	return nil
}

func (e *Encoder) writeStringPtrBeforeUTF8(id int, value *string) {
	e.buf = AppendVarint(e.buf, uint64(id))
	if value == nil {
		e.buf = AppendNull(e.buf)
	} else {
		e.buf = AppendPresent(e.buf)
		e.buf = appendStringBeforeUTF8(e.buf, *value)
	}
}

func writeStringArrayBeforeUTF8(e *Encoder, _ string, values []string) error {
	e.buf = AppendVarint(e.buf, 256)
	e.buf = AppendArrayHeader(e.buf, len(values))
	for _, value := range values {
		e.buf = appendStringBeforeUTF8(e.buf, value)
	}
	return nil
}

func writeStringColumnBeforeUTF8(w *ColumnarWriter, values []string) error {
	var data []byte
	for _, value := range values {
		w.totalStringLen += len(value)
		data = appendStringBeforeUTF8(data, value)
	}
	w.columns = append(w.columns, column{fieldID: 256, data: data})
	return nil
}

func writeStringPtrColumnBeforeUTF8(w *ColumnarWriter, values []*string) error {
	var data []byte
	for _, value := range values {
		if value == nil {
			data = AppendNull(data)
			continue
		}
		w.totalStringLen += len(*value)
		data = AppendPresent(data)
		data = appendStringBeforeUTF8(data, *value)
	}
	w.columns = append(w.columns, column{fieldID: 256, data: data})
	return nil
}
