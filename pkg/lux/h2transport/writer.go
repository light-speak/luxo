package h2transport

import (
	"bufio"
	"errors"
	"io"

	"golang.org/x/net/http2"
)

var errBatchLimit = errors.New("invalid HTTP/2 DATA batch limits")

// dataWriter is the DATA-only part of an experimental connection writer.
// It does not perform handshake, HEADERS, control frames or TLS negotiation.
// Callers own deadlines and must not concurrently use the queue or framer.
type dataWriter struct {
	windowCredit *windowCredit
	queue        *dataQueue
	buffer       *bufio.Writer
	framer       *http2.Framer
}

// batchResult counts frames encoded successfully, not peer acknowledgements.
type batchResult struct{ frames, bytes int }

func newDataWriter(queue *dataQueue, destination io.Writer, bufferSize int) *dataWriter {
	buffer := bufio.NewWriterSize(destination, bufferSize)
	return &dataWriter{queue: queue, buffer: buffer, framer: http2.NewFramer(buffer, nil)}
}

// writeBatch services only ready DATA, then flushes. Limits ensure the connection
// owner can service control messages between batches even with continuous load.
// FrameSize must not exceed the peer's accepted frame size. Zero payload still
// consumes one frame of the batch budget; no timer or wait is used to fill it.
func (w *dataWriter) writeBatch(connection *sendWindow, maxFrames, maxBytes, frameSize int) (batchResult, error) {
	var result batchResult
	if maxFrames < 1 || maxBytes < 1 || frameSize < 1 || frameSize > 1<<24-1 {
		return result, errBatchLimit
	}
	for result.frames < maxFrames && result.bytes < maxBytes {
		chunk, ok, err := w.queue.next(connection, min(frameSize, maxBytes-result.bytes))
		if err != nil {
			w.queue.close()
			return result, err
		}
		if !ok {
			break
		}
		if err := w.writeChunk(chunk); err != nil {
			return result, err
		}
		result.frames++
		result.bytes += len(chunk.data)
	}
	if result.frames > 0 {
		if err := w.buffer.Flush(); err != nil {
			w.queue.close()
			return result, err
		}
	}
	return result, nil
}

func (w *dataWriter) writeChunk(chunk dataChunk) error {
	// Publish before WriteData can flush to the peer, which may immediately
	// return credit. A failed write closes the connection and discards tokens.
	if len(chunk.data) > 0 && w.windowCredit != nil {
		w.windowCredit.sentData()
	}
	err := w.framer.WriteData(chunk.streamID, chunk.end, chunk.data)
	if err != nil {
		w.queue.close()
	}
	// Framer/Writer consume or copy the bytes synchronously. Recycling does not
	// wait for the peer, but must wait for WriteData to return, including failure.
	w.queue.completeCurrent()
	return err
}
