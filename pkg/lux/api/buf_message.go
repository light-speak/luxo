package api

import "encoding/binary"

// BeginMessage reserves a canonical length prefix for a nested message.
// Generated writers append directly to this buffer, preserving its identity.
func (r *ResponseBuf) BeginMessage() int {
	mark := len(r.B)
	r.B = append(r.B, 0)
	return mark
}

// EndMessage completes the most recent unfinished BeginMessage. Marks must be
// closed inside-out and must not outlive Reset. Small messages need no move;
// larger messages grow the reserved prefix without a temporary payload buffer.
func (r *ResponseBuf) EndMessage(mark int) {
	size := len(r.B) - mark - 1
	r.endPrefix(mark, size)
}

// BeginArenaHeader reserves the string-byte count for a columnar message.
// Counting while writing avoids evaluating transforms or masks twice.
func (r *ResponseBuf) BeginArenaHeader() int {
	return r.BeginMessage()
}

// EndArenaHeader patches the arena count after all selected columns are written.
func (r *ResponseBuf) EndArenaHeader(mark, stringBytes int) {
	r.endPrefix(mark, stringBytes)
}

func (r *ResponseBuf) endPrefix(mark, size int) {
	if size < 128 {
		r.B[mark] = byte(size)
		return
	}
	var prefix [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(prefix[:], uint64(size))
	end := len(r.B)
	r.B = append(r.B, prefix[1:n]...)
	copy(r.B[mark+n:], r.B[mark+1:end])
	copy(r.B[mark:mark+n], prefix[:n])
}
