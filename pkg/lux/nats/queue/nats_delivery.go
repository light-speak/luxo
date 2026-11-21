package queue

import (
	"strconv"
	"strings"

	"github.com/nats-io/nats.go/jetstream"
)

// natsDeliveryCount reads only the delivery count from the ACK reply subject.
// nats.go's Metadata builds a token slice and a complete metadata object on
// every call; neither is needed for retry isolation. Keep the same v1 (9 tokens)
// and v2 (11+ tokens) layouts as nats.go, including optional v2 suffixes, while
// rejecting an overflowing/zero delivery count instead of wrapping it.
func natsDeliveryCount(reply string) (uint64, error) {
	rest, ok := strings.CutPrefix(reply, "$JS.ACK.")
	if !ok {
		return 0, jetstream.ErrNotJSMessage
	}
	var fields [9]string
	count := 0
	for {
		field, tail, more := strings.Cut(rest, ".")
		if field == "" {
			return 0, jetstream.ErrNotJSMessage
		}
		if count < len(fields) {
			fields[count] = field
		}
		count++
		if !more {
			break
		}
		rest = tail
	}
	index := 2 // v1: stream, consumer, delivered, streamSeq, consumerSeq, time, pending.
	if count != 7 {
		if count < 9 {
			return 0, jetstream.ErrNotJSMessage
		}
		index = 4 // v2 prepends domain and account hash.
	}
	text := fields[index]
	if text[0] < '0' || text[0] > '9' {
		return 0, jetstream.ErrNotJSMessage
	}
	delivered, err := strconv.ParseUint(text, 10, 64)
	if err != nil || delivered == 0 {
		return 0, jetstream.ErrNotJSMessage
	}
	return delivered, nil
}
