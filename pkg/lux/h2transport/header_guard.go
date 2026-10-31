package h2transport

import (
	"io"

	"golang.org/x/net/http2"
)

// headerGuard observes framing before Framer's internal HPACK continuation loop.
// It does not decode HTTP/2 or HPACK; those validations remain in x/net.
// Limits include padding/priority bytes and allow at most 64 frames / 64KiB per
// compressed header block, in addition to the decoded header-list limit.
type headerGuard struct {
	source                                        io.Reader
	header                                        [9]byte
	headerBytes, remaining, fragments, compressed int
	failed                                        error
}

func (g *headerGuard) Read(dst []byte) (int, error) {
	if g.failed != nil {
		return 0, g.failed
	}
	if len(dst) == 0 {
		return 0, nil
	}
	if g.remaining > 0 {
		n, err := g.source.Read(dst[:min(len(dst), g.remaining)])
		g.remaining -= n
		return n, err
	}
	n, err := g.source.Read(dst[:min(len(dst), 9-g.headerBytes)])
	copy(g.header[g.headerBytes:], dst[:n])
	g.headerBytes += n
	if g.headerBytes == 9 {
		g.headerBytes = 0
		g.remaining = int(g.header[0])<<16 | int(g.header[1])<<8 | int(g.header[2])
		if !g.acceptHeader() {
			g.failed = http2.ConnectionError(http2.ErrCodeEnhanceYourCalm)
			return 0, g.failed
		}
	}
	return n, err
}

func (g *headerGuard) acceptHeader() bool {
	kind := http2.FrameType(g.header[3])
	if kind != http2.FrameHeaders && kind != http2.FrameContinuation {
		return true
	}
	if kind == http2.FrameHeaders {
		g.fragments, g.compressed = 0, 0
	}
	g.fragments++
	g.compressed += g.remaining
	if g.fragments > 64 || g.compressed > 64<<10 {
		return false
	}
	if g.header[4]&4 != 0 {
		g.fragments, g.compressed = 0, 0
	}
	return true
}
