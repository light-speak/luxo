import { describe, expect, test } from 'vitest'
import { Encoder } from './codec'
import * as signed from './int64'
import * as unsigned from './uint64'
import { WsTransport } from './transport'

// Resolve module-runner getters once, outside all timed loops.
const { assertInt64, writeWireInt64 } = signed
const { jsonUint64, readWireUint64 } = unsigned

const ids = [1, 253, 65_535, 4_294_967_296, Number.MAX_SAFE_INTEGER]
const frames = ids.map(id => {
  const encoder = new Encoder()
  encoder.writeVarint(id)
  return encoder.bytes()
})

describe('WebSocket metadata precision hot paths', () => {
  test('unsigned header parsing', async ({ bench }) => {
    for (const [index, frame] of frames.entries()) expect(readWireUint64(frame, 0)?.value).toBe(ids[index])
    let index = 0
    const result = await bench.compare(
      bench('previous unchecked number header', () => batch(() => previousHeader(frames[index++ % frames.length]).value)),
      bench('checked uint64 header, small-ID path', () => batch(() => readWireUint64(frames[index++ % frames.length], 0)!.value as number)),
      { time: 500 },
    )
    report(result.get('previous unchecked number header'), result.get('checked uint64 header, small-ID path'))
  })

  test('JSON ID validation', async ({ bench }) => {
    let index = 0
    const result = await bench.compare(
      bench('previous unchecked JSON ID', () => batch(() => ids[index++ % ids.length])),
      bench('checked JSON ID, small-ID path', () => batch(() => jsonUint64(ids[index++ % ids.length]) as number)),
      { time: 500 },
    )
    report(result.get('previous unchecked JSON ID'), result.get('checked JSON ID, small-ID path'))
  })

  test('request sequence allocation', async ({ bench }) => {
    const transport = new WsTransport('ws://localhost/luvia') as unknown as { seq: number; nextRequestID(): number | bigint }
    const result = await bench.compare(
      bench('previous unchecked sequence increment', () => batch(() => ++transport.seq)),
      bench('bounded adaptive sequence increment', () => batch(() => transport.nextRequestID() as number)),
      { time: 500 },
    )
    report(result.get('previous unchecked sequence increment'), result.get('bounded adaptive sequence increment'))
  })

  test('shared word writer does not hide signed schema work', async ({ bench }) => {
    const values = [0n, -1n, 1n, -9223372036854775808n, 9007199254740993n, 9223372036854775807n]
    const before = new Uint8Array(10)
    const after = new Uint8Array(10)
    for (const value of values) {
      const n = previousSigned(before, value)
      expect(writeWireInt64(after, 0, value)).toBe(n)
      expect(after.subarray(0, n)).toEqual(before.subarray(0, n))
    }
    let index = 0
    const result = await bench.compare(
      bench('previous inline signed writer', () => batch(() => previousSigned(before, values[index++ % values.length]))),
      bench('shared word-loop signed writer', () => batch(() => writeWireInt64(after, 0, values[index++ % values.length]))),
      { time: 500 },
    )
    report(result.get('previous inline signed writer'), result.get('shared word-loop signed writer'))
  })
})

// Batch operations to avoid measuring one timer tick per nanosecond-scale call.
function batch(operation: () => number): number {
  let sum = 0
  for (let index = 0; index < 1000; index++) sum += operation()
  return sum
}

function report(
  before: { name: string; latency: { p50: number; mean: number; rme: number } },
  after: { name: string; latency: { p50: number; mean: number; rme: number } },
): void {
  console.log(JSON.stringify({
    operation: after.name,
    beforeMedianNs: before.latency.p50 * 1000, afterMedianNs: after.latency.p50 * 1000,
    beforeMeanNs: before.latency.mean * 1000, afterMeanNs: after.latency.mean * 1000,
    beforeRME: before.latency.rme, afterRME: after.latency.rme,
  }))
}

// Frozen pre-fix baselines only receive inputs they represented exactly.
function previousHeader(data: Uint8Array): { value: number; offset: number } {
  let value = 0
  let shift = 0
  let offset = 0
  while (offset < data.length) {
    const byte = data[offset++]
    value += (byte & 127) * (2 ** shift)
    if (byte < 128) break
    shift += 7
  }
  return { value, offset }
}

function previousSigned(bytes: Uint8Array, value: bigint): number {
  assertInt64(value)
  const low = Number(BigInt.asUintN(32, value))
  const high = Number(BigInt.asIntN(32, value >> 32n))
  const sign = high >> 31
  let lo = ((low << 1) ^ sign) >>> 0
  let hi = (((high << 1) | (low >>> 31)) ^ sign) >>> 0
  let offset = 0
  while (hi !== 0 || lo >= 128) {
    bytes[offset++] = (lo & 127) | 128
    lo = ((lo >>> 7) | (hi << 25)) >>> 0
    hi >>>= 7
  }
  bytes[offset++] = lo
  return offset
}
