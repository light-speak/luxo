package luvia

import "context"

type metricExport struct {
	collector             *MetricsCollector
	ctx                   context.Context
	pending               map[metricBucketKey]*metricBucket
	batch                 observationBatch
	keys                  []metricBucketKey
	scratch               observationJSON
	dropped, measurements uint64
}

func (mc *MetricsCollector) exportMetricBuckets(buckets map[metricBucketKey]*metricBucket) {
	batch, err := mc.newObservationBatch("ingestMetrics", `,"buckets":[`)
	if err != nil {
		mc.discardMetricBuckets(buckets)
		return
	}
	ctx, cancel := mc.observationContext()
	defer cancel()
	e := metricExport{collector: mc, ctx: ctx, pending: buckets, batch: batch, keys: make([]metricBucketKey, 0, len(buckets))}
	e.run()
	if e.dropped != 0 {
		mc.addInvalidMetrics(e.dropped, e.measurements)
	}
}

func (e *metricExport) run() {
	for key, bucket := range e.pending {
		e.scratch.buf.B, e.scratch.err = e.scratch.buf.B[:0], nil
		appendMetricBucket(&e.scratch, bucket)
		if e.scratch.err != nil {
			e.reject(key, bucket)
			continue
		}
		if !e.batch.fits(len(e.scratch.buf.B)) && e.batch.count != 0 {
			if !e.send() {
				e.collector.reenqueue(e.pending)
				return
			}
		}
		if !e.batch.fits(len(e.scratch.buf.B)) {
			e.reject(key, bucket)
			continue
		}
		e.batch.append(e.scratch.buf.B, len(e.pending))
		e.keys = append(e.keys, key)
	}
	if e.batch.count != 0 && !e.send() {
		e.collector.reenqueue(e.pending)
	}
}

func (e *metricExport) reject(key metricBucketKey, bucket *metricBucket) {
	delete(e.pending, key)
	e.dropped++
	e.measurements += uint64(max(0, bucket.totalCount))
}

func (e *metricExport) send() bool {
	if !e.collector.sendObservationBatch(e.ctx, append(e.batch.body, ']', '}'), "metrics") {
		return false
	}
	for _, key := range e.keys {
		delete(e.pending, key)
	}
	clear(e.keys)
	e.keys = e.keys[:0]
	e.batch.reset()
	return true
}
