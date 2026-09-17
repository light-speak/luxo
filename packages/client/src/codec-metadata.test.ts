import { describe, expect, it } from 'vitest'
import { ColumnarDecoder, Decoder, Encoder, decodeScalarArray } from './codec'
import type { FieldType } from './codec'

const rowReaders: Record<string, (decoder: Decoder) => unknown[]> = {
  Int: d => d.readIntArray(), Float: d => d.readFloatArray(),
  String: d => d.readStringArray(), Bool: d => d.readBoolArray(),
  UUID: d => d.readUUIDArray(), Bytes: d => d.readBytesArray(),
}
const columnReaders: Record<string, (decoder: ColumnarDecoder) => unknown[]> = {
  Int: d => d.readColumnInt(), Float: d => d.readColumnFloat(),
  String: d => d.readColumnString(), Bool: d => d.readColumnBool(),
  UUID: d => d.readColumnUUID(), Bytes: d => d.readColumnBytes(),
  DateTime: d => d.readColumnDateTime(), IntPtr: d => d.readColumnIntPtr(),
  FloatPtr: d => d.readColumnFloatPtr(), StringPtr: d => d.readColumnStringPtr(),
  BoolPtr: d => d.readColumnBoolPtr(), UUIDPtr: d => d.readColumnUUIDPtr(),
  BytesPtr: d => d.readColumnBytesPtr(), DateTimePtr: d => d.readColumnDateTimePtr(),
}

function unsigned(value: bigint): number[] {
  const bytes: number[] = []
  do {
    const part = Number(value & 127n)
    value >>= 7n
    bytes.push(part | (value === 0n ? 0 : 128))
  } while (value !== 0n)
  return bytes
}

function batch(count: bigint, payload: number[] = [], arena = 0n): Uint8Array {
  return Uint8Array.from([...unsigned(count), ...unsigned(arena), ...payload])
}

describe('binary metadata bounds', () => {
  it('rejects nonempty zero-column and impossible-row lists before allocation', () => {
    for (const count of [1n, 128n, 10_000_000n]) {
      for (const payload of [
        [], [0], [1, 0], [0, 1, 2, 3, 4],
        [0x80, 0, 1, 2], [0x80, 0x80, 0, 1], [0x80, 1, 0],
      ]) {
        const decoder = new ColumnarDecoder(batch(count, payload))
        expect(decoder.error).not.toBeNull()
        expect(decoder.nextColumn()).toBe(false)
      }
    }
  })

  it('accepts empty lists and nullable columns with multibyte field IDs', () => {
    for (const count of [0, 1, 2]) {
      for (const id of count === 0 ? [0n] : [1n, 128n, 16384n]) {
        const payload = count === 0 ? [0] : [...unsigned(id), ...new Array<number>(count).fill(0), 0]
        const decoder = new ColumnarDecoder(batch(BigInt(count), payload))
        expect(decoder.error).toBeNull()
        if (count > 0) {
          expect(decoder.nextColumn()).toBe(true)
          expect(decoder.fieldID).toBe(Number(id))
          expect(decoder.readColumnIntPtr()).toEqual(new Array<null>(count).fill(null))
        }
        expect(decoder.nextColumn()).toBe(false)
        expect(decoder.error).toBeNull()
      }
    }
  })

  it('rejects oversized array headers before calling a model decoder', () => {
    for (const size of [1_000_001n, 1n << 32n, (1n << 53n) - 1n, 1n << 63n]) {
      const decoder = new Decoder(Uint8Array.from(unsigned(size)))
      expect(decoder.readArray(() => { throw new Error('must not decode an element') })).toEqual([])
      expect(decoder.error).not.toBeNull()
    }
  })

  it('rejects truncated arrays before allocating or invoking callbacks', () => {
    for (const [name, read] of Object.entries(rowReaders)) {
      for (const count of [1n, 1_000_001n, 1n << 32n]) {
        const decoder = new Decoder(Uint8Array.from(unsigned(count)))
        expect(read(decoder), name).toEqual([])
        expect(decoder.error, name).not.toBeNull()
      }
    }
    const decoder = new Decoder(Uint8Array.of(1))
    expect(decoder.readArray(() => { throw new Error('missing element') })).toEqual([])
    expect(decoder.error).not.toBeNull()
  })

  it('checks the array cap even when the payload has enough bytes', () => {
    const count = 1_000_001
    const prefix = unsigned(BigInt(count))
    const bytes = new Uint8Array(prefix.length + count)
    bytes.set(prefix)
    const decoder = new Decoder(bytes)
    expect(decoder.readBoolArray().length).toBe(0)
    expect(decoder.error).toContain('limit')
  })

  it('accepts the inclusive array limit and leaves the cursor at the next field', () => {
    for (const count of [0, 1, 128, 1_000_000]) {
      const prefix = unsigned(BigInt(count))
      const bytes = new Uint8Array(prefix.length + count + 1)
      bytes.set(prefix)
      bytes[bytes.length - 1] = 7
      const decoder = new Decoder(bytes)
      expect(decoder.readBoolArray()).toHaveLength(count)
      expect(decoder.error).toBeNull()
      expect(decoder.nextField()).toBe(true)
      expect(decoder.fieldID).toBe(7)
    }
  })

  it('bounds column counts without narrowing large values into valid counts', () => {
    for (const count of [0n, 1n, 10_000_000n]) {
      const header = batch(count)
      const bytes = new Uint8Array(header.length + Number(count) + 2)
      bytes.set(header)
      if (count > 0n) bytes[header.length] = 1
      const decoder = new ColumnarDecoder(bytes)
      expect(decoder.count).toBe(Number(count))
      expect(decoder.error).toBeNull()
    }
    for (const count of [10_000_001n, 1n << 32n, 1n << 53n, (1n << 64n) - 1n]) {
      const decoder = new ColumnarDecoder(batch(count))
      expect(decoder.count).toBe(0)
      expect(decoder.error).not.toBeNull()
      expect(decoder.nextColumn()).toBe(false)
    }
  })

  it('checks row and column arena sizes against the same inclusive limit', () => {
    for (const arena of [0n, 1n, 67_108_864n, 67_108_865n, 1n << 32n, 1n << 63n]) {
      const row = new Decoder(Uint8Array.from([...unsigned(arena), 0]))
      row.skipArenaHeader()
      const column = new ColumnarDecoder(batch(0n, [], arena))
      const valid = arena <= 67_108_864n
      expect(row.error === null).toBe(valid)
      expect(column.error === null).toBe(valid)
      expect(column.arenaSize).toBe(valid ? Number(arena) : 0)
    }
  })

  it('preserves safe field IDs without 32-bit wrapping or an end-marker alias', () => {
    for (const id of [0n, 1n, 127n, 128n, 1n << 32n, (1n << 53n) - 1n]) {
      const row = new Decoder(Uint8Array.from(unsigned(id)))
      const column = new ColumnarDecoder(batch(0n, unsigned(id)))
      expect(row.nextField()).toBe(id !== 0n)
      expect(column.nextColumn()).toBe(id !== 0n)
      expect(row.fieldID).toBe(Number(id))
      expect(column.fieldID).toBe(Number(id))
      expect(row.error).toBeNull()
      expect(column.error).toBeNull()
    }
  })

  it('reports malformed metadata instead of treating it as a valid empty value', () => {
    for (const bytes of [[], [128], Array<number>(8).fill(255), unsigned(1n << 53n)]) {
      const row = new Decoder(Uint8Array.from(bytes))
      expect(row.readArray(() => { throw new Error('invalid header') })).toEqual([])
      expect(row.error).not.toBeNull()
      const column = new ColumnarDecoder(Uint8Array.from(bytes))
      expect(column.error).not.toBeNull()
      const error = column.error
      expect(column.readColumnFloat()).toEqual([])
      expect(column.error).toBe(error)
    }
    const decoder = new Decoder(Uint8Array.of(128))
    decoder.readIntArray()
    const error = decoder.error
    expect(decoder.readIntArray()).toEqual([])
    expect(decoder.error).toBe(error)
    expect(decoder.remaining).toBe(0)
  })

  it('matches independently encoded safe metadata over 10,000 mixed-width values', () => {
    let state = 0x12345678
    for (let index = 0; index < 10_000; index++) {
      state = (Math.imul(state, 1664525) + 1013904223) >>> 0
      const high = state & 0x1fffff
      state = (Math.imul(state, 1664525) + 1013904223) >>> 0
      const id = ((BigInt(high) << 32n) | BigInt(state)) >> BigInt(index % 53)
      const decoder = new Decoder(Uint8Array.from([...unsigned(id), 7]))
      expect(decoder.nextField()).toBe(id !== 0n)
      expect(decoder.fieldID).toBe(Number(id))
      expect(decoder.remaining).toBe(1)
      expect(decoder.error).toBeNull()
    }
  })
})

describe('column payload bounds', () => {
  it('rejects malformed scalar array cells rather than dropping the nested decoder error', () => {
    const types: FieldType[] = ['Int', 'Duration', 'Float', 'String', 'Boolean',
      'UUID', 'Bytes', 'DateTime', 'Enum', 'Decimal', 'JSON']
    for (const type of types) {
      for (const wire of [[], [1], [0, 99], unsigned(1_000_001n)]) {
        expect(() => decodeScalarArray(Uint8Array.from(wire), type), type).toThrow()
      }
      expect(decodeScalarArray(Uint8Array.of(0), type), type).toEqual([])
    }
    expect(() => decodeScalarArray(Uint8Array.of(0), 'Model')).toThrow('unsupported')
  })

  for (const [name, read] of Object.entries(columnReaders)) {
    it(`${name}: rejects missing payload and accepts empty batches`, () => {
      const truncated = new ColumnarDecoder(batch(1n))
      expect(read(truncated)).toEqual([])
      expect(truncated.error).not.toBeNull()
      expect(truncated.offset()).toBe(2)
      const empty = new ColumnarDecoder(batch(0n))
      expect(read(empty)).toEqual([])
      expect(empty.error).toBeNull()
      expect(empty.offset()).toBe(2)
    })
  }

  it('checks fixed-width columns before reading the first cell', () => {
    for (const [name, width] of [['Float', 8], ['UUID', 16]] as const) {
      const decoder = new ColumnarDecoder(batch(2n, Array<number>(2 * width - 1).fill(0)))
      expect(columnReaders[name](decoder)).toEqual([])
      expect(decoder.error).not.toBeNull()
      expect(decoder.offset()).toBe(2)
    }
  })

  it('checks every present nullable fixed-width cell', () => {
    for (const name of ['FloatPtr', 'UUIDPtr']) {
      const decoder = new ColumnarDecoder(batch(2n, [0, 1, 0]))
      columnReaders[name](decoder)
      expect(decoder.error).not.toBeNull()
      expect(decoder.offset()).toBeLessThanOrEqual(5)
    }
  })

  it('rejects oversized and truncated blob lengths without advancing past the buffer', () => {
    for (const size of [2n, 1n << 32n, (1n << 53n) - 1n, 1n << 63n]) {
      for (const name of ['String', 'Bytes', 'StringPtr', 'BytesPtr']) {
        const flags = name.endsWith('Ptr') ? [1] : []
        const bytes = batch(1n, [...flags, ...unsigned(size), 65])
        const decoder = new ColumnarDecoder(bytes)
        columnReaders[name](decoder)
        expect(decoder.error, name).not.toBeNull()
        expect(decoder.offset(), name).toBeLessThanOrEqual(bytes.length)
      }
    }
  })

  it('does not overwrite an earlier error or consume a subsequent column', () => {
    const decoder = new ColumnarDecoder(batch(1n, [2, 65]))
    decoder.readColumnString()
    const offset = decoder.offset()
    const error = decoder.error
    expect(error).not.toBeNull()
    for (const read of Object.values(columnReaders)) {
      expect(read(decoder)).toEqual([])
      expect(decoder.error).toBe(error)
      expect(decoder.offset()).toBe(offset)
    }
  })

  it('does not decode blob contents after an invalid nullable marker', () => {
    for (const read of [columnReaders.StringPtr, columnReaders.BytesPtr]) {
      const decoder = new ColumnarDecoder(batch(2n, [1, 2, 1, 0, 0]))
      expect(decoder.nextColumn()).toBe(true)
      expect(read(decoder)).toEqual([])
      expect(decoder.error).toContain('invalid nullable')
      expect(decoder.offset()).toBe(5)
    }
  })

  it('preserves present, empty and null blob cells and their following column', () => {
    for (const name of ['String', 'Bytes', 'StringPtr', 'BytesPtr']) {
      const nullable = name.endsWith('Ptr')
      const payload = nullable ? [0, 1, 0, 1, 2, 65, 66, 7] : [0, 2, 65, 66, 7]
      const decoder = new ColumnarDecoder(batch(nullable ? 3n : 2n, [1, ...payload]))
      expect(decoder.nextColumn()).toBe(true)
      const values = columnReaders[name](decoder)
      expect(values).toEqual(name.startsWith('String')
        ? (nullable ? [null, '', 'AB'] : ['', 'AB'])
        : (nullable ? [null, new Uint8Array(0), Uint8Array.of(65, 66)]
          : [new Uint8Array(0), Uint8Array.of(65, 66)]))
      expect(decoder.nextColumn()).toBe(true)
      expect(decoder.fieldID).toBe(7)
      expect(decoder.error).toBeNull()
    }
  })

  it('keeps full-width business integers independent from metadata bounds', () => {
    const encoder = new Encoder()
    encoder.writeVarint(2)
    encoder.writeVarint(0)
    encoder.writeVarint(1)
    encoder.writeSvarint(-(1n << 63n))
    encoder.writeSvarint((1n << 63n) - 1n)
    encoder.writeVarint(0)
    const decoder = new ColumnarDecoder(encoder.bytes())
    expect(decoder.nextColumn()).toBe(true)
    expect(decoder.readColumnInt()).toEqual([-(1n << 63n), (1n << 63n) - 1n])
    expect(decoder.nextColumn()).toBe(false)
    expect(decoder.error).toBeNull()
  })
})
