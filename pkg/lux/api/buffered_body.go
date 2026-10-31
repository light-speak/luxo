package api

import (
	"io"
)

// BufferedBody exposes transport-owned, fully received storage to the Binary
// router without a second copy. The transport must not mutate or recycle the
// storage until ServeHTTP returns. Handlers must not retain borrowed parameters.
// Middleware can read it normally; replacing Body disables the borrowing path.
type BufferedBody struct {
	data   []byte
	offset int
	closed bool
}

// NewBufferedBody borrows data; it does not transfer ownership or copy bytes.
func NewBufferedBody(data []byte) *BufferedBody { return &BufferedBody{data: data} }

func (b *BufferedBody) Read(dst []byte) (int, error) {
	if b.closed {
		return 0, io.ErrClosedPipe
	}
	if b.offset == len(b.data) {
		return 0, io.EOF
	}
	n := copy(dst, b.data[b.offset:])
	b.offset += n
	return n, nil
}

func (b *BufferedBody) Close() error {
	b.closed = true
	return nil
}

func (b *BufferedBody) borrow(limit int) ([]byte, error) {
	if b.closed {
		return nil, io.ErrClosedPipe
	}
	if len(b.data)-b.offset > limit {
		return nil, errRequestBodyTooLarge
	}
	data := b.data[b.offset:len(b.data):len(b.data)]
	b.offset = len(b.data)
	return data, nil
}

func readTransportBody(body io.ReadCloser) ([]byte, *[]byte, error) {
	return readTransportBodyLimit(body, MaxRequestBodySize)
}

func readTransportBodyLimit(body io.ReadCloser, limit int) ([]byte, *[]byte, error) {
	return readTransportBodyBudget(body, limit, nil)
}

func readTransportBodyBudget(body io.ReadCloser, limit int, lease *receiveLease) ([]byte, *[]byte, error) {
	if buffered, ok := body.(*BufferedBody); ok {
		if err := lease.acquire(cap(buffered.data)); err != nil {
			return nil, nil, err
		}
		data, err := buffered.borrow(limit)
		return data, nil, err
	}
	return readBodyLimitBudget(body, limit, lease)
}
