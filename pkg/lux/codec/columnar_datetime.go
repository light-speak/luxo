package codec

import "time"

// WriteColumnDateTime writes a full column of exact seconds/nanoseconds pairs.
func (w *ColumnarWriter) WriteColumnDateTime(fieldID int, values []time.Time) error {
	var data []byte
	for _, value := range values {
		var err error
		data, err = AppendDateTime(data, value)
		if err != nil {
			return err
		}
	}
	w.appendColumn(fieldID, len(values), data)
	return nil
}

// WriteColumnDateTimePtr writes nullable timestamp values without rounding.
func (w *ColumnarWriter) WriteColumnDateTimePtr(fieldID int, values []*time.Time) error {
	var data []byte
	for _, value := range values {
		if value == nil {
			data = AppendNull(data)
		} else {
			var err error
			data, err = AppendDateTime(AppendPresent(data), *value)
			if err != nil {
				return err
			}
		}
	}
	w.appendColumn(fieldID, len(values), data)
	return nil
}

// ReadColumnDateTime validates the payload lower bound before allocating rows.
func (r *ColumnarReader) ReadColumnDateTime() []time.Time {
	if !r.checkElements(2) {
		return nil
	}
	decoder := Decoder{buf: r.buf, off: r.off}
	values := make([]time.Time, r.count)
	for i := range values {
		values[i] = decoder.ReadDateTime()
		if decoder.err != nil {
			break
		}
	}
	r.off, r.err = decoder.off, decoder.err
	if r.err != nil {
		return nil
	}
	return values
}

// ReadColumnDateTimePtr preserves nullable timestamps and rejects partial columns.
func (r *ColumnarReader) ReadColumnDateTimePtr() []*time.Time {
	if !r.checkElements(1) {
		return nil
	}
	decoder := Decoder{buf: r.buf, off: r.off}
	values := make([]*time.Time, r.count)
	for i := range values {
		values[i] = decoder.ReadDateTimePtr()
		if decoder.err != nil {
			break
		}
	}
	r.off, r.err = decoder.off, decoder.err
	if r.err != nil {
		return nil
	}
	return values
}

// SkipColumnDateTime validates timestamp pairs without materializing row values.
func (r *ColumnarReader) SkipColumnDateTime() {
	r.skipDateTimeColumn(false)
}

// SkipColumnDateTimePtr validates nullable timestamp pairs without allocations.
func (r *ColumnarReader) SkipColumnDateTimePtr() {
	r.skipDateTimeColumn(true)
}

func (r *ColumnarReader) skipDateTimeColumn(nullable bool) {
	minimum := 2
	if nullable {
		minimum = 1
	}
	if !r.checkElements(minimum) {
		return
	}
	decoder := Decoder{buf: r.buf, off: r.off}
	for range r.count {
		if nullable {
			decoder.SkipNullableDateTime()
		} else {
			decoder.SkipDateTime()
		}
		if decoder.err != nil {
			break
		}
	}
	r.off, r.err = decoder.off, decoder.err
}
