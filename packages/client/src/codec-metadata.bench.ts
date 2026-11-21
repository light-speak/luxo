import type * as Codec from './codec'
import { measure } from './measure.bench'

// Pass separately compiled snapshots so both versions use the same TS/Node
// toolchain without Vite module-runner wrappers or a runtime compatibility branch.
type Codecs = typeof Codec
type Operation = () => unknown

type Int64Writer = (bytes: Uint8Array, offset: number, value: bigint) => number

/** Compare separately compiled integer writers with prebuilt inputs and reused destinations. */
export function measureInt64Writes(before: Int64Writer, after: Int64Writer): void {
  const maximum = (1n << 63n) - 1n
  const samples: [string, bigint[]][] = [
    ['zero', [0n]], ['small', [42n]], ['negative', [-42n]],
    ['int32-max', [2147483647n]], ['int32-min', [-2147483648n]],
    ['uint32', [4294967295n]], ['safe-limit', [9007199254740991n]],
    ['above-safe-limit', [9007199254740993n]], ['int64-max', [maximum]], ['int64-min', [-(1n << 63n)]],
    ['128-small', Array.from({ length: 128 }, (_, i) => BigInt(i - 64))],
    ['128-mixed', Array.from({ length: 128 }, (_, i) => i % 2 ? BigInt(i) : maximum - BigInt(i))],
  ]
  for (const [name, values] of samples) {
    const operation = (writer: Int64Writer, bytes: Uint8Array): (() => number) => () => {
      let offset = 0
      for (const value of values) offset = writer(bytes, offset, value)
      return offset
    }
    const oldBytes = new Uint8Array(values.length * 10), newBytes = oldBytes.slice()
    const previous = operation(before, oldBytes), current = operation(after, newBytes)
    if (previous() !== current() || oldBytes.some((byte, i) => byte !== newBytes[i])) {
      throw new Error(`${name}: integer writer wire differs`)
    }
    measure(`Int64 ${name}`, previous, current)
  }
}

/** Compare selection helpers on valid IDs only; the legacy path aliases large IDs. */
export function measureFieldMasks(codec: Pick<Codecs, 'fieldMaskHas' | 'fieldMaskSet'>): void {
  for (const id of [1, 128, 65535]) {
    const mask = codec.fieldMaskSet(new Uint8Array(0), id)
    measure(`mask has id=${id}`, () => legacyMaskHas(mask, id), () => codec.fieldMaskHas(mask, id))
    measure(`mask set reused id=${id}`, () => legacyMaskSet(mask, id), () => codec.fieldMaskSet(mask, id))
    measure(`mask set fresh id=${id}`, () => legacyMaskSet(new Uint8Array(0), id), () => codec.fieldMaskSet(new Uint8Array(0), id))
  }
}

// Frozen helpers from before the classified ID boundary, for benchmarks only.
function legacyMaskHas(mask: Uint8Array, fieldID: number): boolean {
  if (!Number.isInteger(fieldID) || fieldID <= 0) return false
  const bit = fieldID - 1
  const byteIdx = bit >>> 3
  if (byteIdx >= mask.length) return false
  return (mask[byteIdx] & (1 << (bit & 7))) !== 0
}

function legacyMaskSet(mask: Uint8Array, fieldID: number): Uint8Array {
  if (!Number.isInteger(fieldID) || fieldID <= 0) return mask
  const bit = fieldID - 1
  const byteIdx = bit >>> 3
  const bitIdx = bit & 7
  if (byteIdx >= mask.length) {
    const next = new Uint8Array(byteIdx + 1)
    next.set(mask, 0)
    next[byteIdx] = 1 << bitIdx
    return next
  }
  mask[byteIdx] |= 1 << bitIdx
  return mask
}

/** Compare separately built UUID parsers without charging malformed-input fixes to valid wire. */
export function measureUUID(before: Pick<Codecs, 'parseUUID' | 'Encoder'>, after: Pick<Codecs, 'parseUUID' | 'Encoder'>): void {
  const samples = [
    '00112233-4455-6677-8899-aabbccddeeff',
    'AABBCCDD-EEFF-0011-2233-445566778899',
    '00000000-0000-0000-0000-000000000000',
  ]
  for (const sample of samples) {
    measure(`UUID parse ${sample}`, () => before.parseUUID(sample), () => after.parseUUID(sample))
    for (const count of [1, 128]) {
      const operation = (codec: Pick<Codecs, 'Encoder'>) => () => {
        const encoder = new codec.Encoder(16 * count)
        for (let index = 0; index < count; index++) encoder.writeUUID(sample)
        return encoder.bytes()
      }
      measure(`UUID encode ${sample} count=${count}`, operation(before), operation(after))
    }
  }
}

export function measureMetadata(before: Codecs, after: Codecs): void {
  measure('row: four fields', rowOperation(before), rowOperation(after))
  for (const count of [1, 16, 128]) {
    for (const kind of ['array', 'cell', 'int', 'float', 'string'] as const) {
      const bytes = fixture(after, count, kind)
      measure(`${kind}: ${count}`, operation(before, bytes, kind), operation(after, bytes, kind))
    }
  }
}

/** Isolate the nonempty-column preflight from earlier numeric/text checks. */
export function measureProjection(before: Codecs, after: Codecs): void {
  for (const count of [0, 1, 128]) {
    for (const fieldID of count === 0 ? [1] : [1, 128]) {
      const encoder = new after.Encoder()
      encoder.writeVarint(count)
      encoder.writeVarint(0)
      if (count > 0) {
        encoder.writeVarint(fieldID)
        for (let index = 0; index < count; index++) encoder.writeSvarint(BigInt(index))
      }
      encoder.writeEnd()
      const bytes = encoder.bytes()
      for (const header of [true, false]) {
        measure(`projection count=${count} id=${fieldID} header=${header}`,
          projectionOperation(before, bytes, header), projectionOperation(after, bytes, header))
      }
    }
  }
}

function projectionOperation(codec: Codecs, bytes: Uint8Array, header: boolean): Operation {
  return () => {
    const decoder = new codec.ColumnarDecoder(bytes)
    if (decoder.error) throw new Error(decoder.error)
    if (header) return decoder.count + decoder.arenaSize + decoder.offset()
    let values: bigint[] = []
    if (decoder.nextColumn()) {
      values = decoder.readColumnInt()
      if (decoder.nextColumn()) throw new Error('unexpected extra column')
    }
    if (decoder.error || decoder.offset() !== bytes.length) throw new Error(decoder.error ?? 'trailing data')
    return values
  }
}

export function measureText(before: Codecs, after: Codecs): void {
  const fixtures = { empty: '', ascii: 'hello-world', unicode: '发布检查 café', long: 'a'.repeat(4096) }
  for (const [name, value] of Object.entries(fixtures)) {
    for (const kind of ['row', 'array', 'column', 'nullable'] as const) {
      const encoder = new after.Encoder()
      const count = kind === 'row' ? 1 : 16
      if (kind !== 'row') encoder.writeVarint(count)
      if (kind === 'column' || kind === 'nullable') encoder.writeVarint(0)
      if (kind === 'column' || kind === 'nullable') encoder.writeVarint(1)
      for (let index = 0; index < count; index++) {
        if (kind === 'nullable') encoder.writeBool(true)
        encoder.writeString(value)
      }
      if (kind === 'column' || kind === 'nullable') encoder.writeEnd()
      const bytes = encoder.bytes()
      measure(`${name}: ${kind}`, textOperation(before, bytes, kind), textOperation(after, bytes, kind))
    }
    const json = new TextEncoder().encode(JSON.stringify(value))
    measure(`${name}: JSON`, () => before.decodeJSONValue(json), () => after.decodeJSONValue(json))
  }
}

function textOperation(codec: Codecs, bytes: Uint8Array, kind: string): Operation {
  if (kind === 'row') return () => new codec.Decoder(bytes).readString()
  if (kind === 'array') return () => new codec.Decoder(bytes).readStringArray()
  return () => {
    const decoder = new codec.ColumnarDecoder(bytes)
    if (!decoder.nextColumn()) throw new Error(decoder.error ?? 'missing text column')
    const result = kind === 'column' ? decoder.readColumnString() : decoder.readColumnStringPtr()
    if (decoder.error) throw new Error(decoder.error)
    return result
  }
}

function fixture(codec: Codecs, count: number, kind: string): Uint8Array {
  const encoder = new codec.Encoder()
  encoder.writeVarint(count)
  if (kind !== 'array' && kind !== 'cell') encoder.writeVarint(0)
  if (kind !== 'array' && kind !== 'cell') encoder.writeVarint(1)
  for (let index = 0; index < count; index++) {
    if (kind === 'float') encoder.writeFixed64(index + 0.5)
    else if (kind === 'string') encoder.writeString(index % 2 ? '发布检查' : '')
    else encoder.writeSvarint(-(1n << 63n) + BigInt(index))
  }
  if (kind !== 'array' && kind !== 'cell') encoder.writeEnd()
  return encoder.bytes()
}

function operation(codec: Codecs, bytes: Uint8Array, kind: string): Operation {
  if (kind === 'array') return () => new codec.Decoder(bytes).readIntArray()
  if (kind === 'cell') return () => codec.decodeScalarArray(bytes, 'Int')
  return () => {
    const decoder = new codec.ColumnarDecoder(bytes)
    if (!decoder.nextColumn()) throw new Error(decoder.error ?? 'missing scalar column')
    const result = kind === 'int' ? decoder.readColumnInt() : kind === 'float' ? decoder.readColumnFloat() : decoder.readColumnString()
    if (decoder.error) throw new Error(decoder.error)
    return result
  }
}

function rowOperation(codec: Codecs): Operation {
  const encoder = new codec.Encoder()
  encoder.writeVarint(0)
  for (const id of [1, 128, 16384, 2097152]) encoder.writeFieldInt(id, (1n << 63n) - 1n)
  encoder.writeEnd()
  const bytes = encoder.bytes()
  return () => {
    const decoder = new codec.Decoder(bytes)
    decoder.skipArenaHeader()
    let sum = 0n
    while (decoder.nextField()) sum ^= decoder.readInt() ^ BigInt(decoder.fieldID)
    if (decoder.error) throw new Error(decoder.error)
    return sum
  }
}
