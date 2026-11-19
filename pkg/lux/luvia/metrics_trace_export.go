package luvia

import (
	"context"

	"github.com/light-speak/luxo/pkg/lux/api"
)

type traceExport struct {
	collector        *MetricsCollector
	ctx              context.Context
	records          []api.TraceRecord
	next, kept, sent int
	batch            observationBatch
	scratch          observationJSON
	dropped          uint64
}

func (mc *MetricsCollector) exportTraceRecords(records []api.TraceRecord) {
	batch, err := mc.newObservationBatch("ingestTraces", `,"traces":[`)
	if err != nil {
		mc.addInvalidTraces(uint64(len(records)))
		clear(records)
		return
	}
	ctx, cancel := mc.observationContext()
	defer cancel()
	e := traceExport{collector: mc, ctx: ctx, records: records, batch: batch}
	e.run()
	if e.dropped != 0 {
		mc.addInvalidTraces(e.dropped)
	}
}

func (e *traceExport) run() {
	for e.next < len(e.records) {
		index := e.next
		e.next++
		e.scratch.buf.B, e.scratch.err = e.scratch.buf.B[:0], nil
		appendTraceRecord(&e.scratch, &e.records[index])
		if e.scratch.err != nil {
			e.dropped++
			continue
		}
		e.records[e.kept] = e.records[index]
		e.kept++
		if !e.batch.fits(len(e.scratch.buf.B)) && e.batch.count != 0 {
			if !e.send() {
				e.retry()
				return
			}
		}
		if !e.batch.fits(len(e.scratch.buf.B)) {
			e.kept--
			e.records[e.kept] = api.TraceRecord{}
			e.dropped++
			continue
		}
		e.batch.append(e.scratch.buf.B, len(e.records)-e.sent-int(e.dropped))
	}
	if e.batch.count != 0 && !e.send() {
		e.retry()
		return
	}
	clear(e.records)
}

func (e *traceExport) send() bool {
	payload := append(e.batch.body, ']', '}')
	if !e.collector.sendObservationBatch(e.ctx, payload, "traces") {
		return false
	}
	previous := e.sent
	e.sent += e.batch.count
	clear(e.records[previous:e.sent])
	e.batch.reset()
	return true
}

func (e *traceExport) retry() {
	// Compact unsent validated records before the untouched suffix. append handles
	// overlap; acknowledged records and already isolated failures stay excluded.
	records := append(e.records[e.sent:e.kept], e.records[e.next:]...)
	clear(e.records[e.sent+len(records):])
	e.collector.reenqueueTraces(records)
}
