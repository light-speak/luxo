import { isUtf8 } from 'node:buffer'
import { describe, expect, it } from 'vitest'
import { ColumnarDecoder, Decoder, Encoder, decodeJSONValue, decodeScalarArray } from './codec'

const utf8 = new TextEncoder()
const invalid = [
  [0x80], [0xff], [0xc0, 0x80], [0xc2], [0xe0, 0x80, 0x80],
  [0xe2, 0x82], [0xed, 0xa0, 0x80], [0xf0, 0x80, 0x80, 0x80],
  [0xf0, 0x90, 0x80], [0xf4, 0x90, 0x80, 0x80], [0xf5, 0x80, 0x80, 0x80],
  [0xe2, 0x28, 0xa1],
].map(bytes => Uint8Array.from(bytes))

describe('lossless binary UTF-8', () => {
  it.each(['', 'ascii', '发布检查', '\u0000', '\ufffd', '\ufeff', '\ufefftext', 'a\ufeffb', '\u{10ffff}'])
  ('preserves valid string code points: %j', value => {
    const bytes = utf8.encode(value)
    const row = new Decoder(stringWire(bytes))
    expect(row.readString()).toBe(value)
    expect(row.error).toBeNull()
    for (const nullable of [false, true]) {
      const column = new ColumnarDecoder(columnWire([bytes, utf8.encode('next')], nullable))
      expect(column.nextColumn()).toBe(true)
      expect(nullable ? column.readColumnStringPtr() : column.readColumnString()).toEqual([value, 'next'])
      expect(column.error).toBeNull()
      expect(column.nextColumn()).toBe(false)
      expect(column.error).toBeNull()
    }
  })

  it('records malformed row and nullable strings without replacing or throwing', () => {
    for (const bytes of invalid) {
      for (const nullable of [false, true]) {
        const wire = Uint8Array.from([...stringWire(bytes), ...stringWire(utf8.encode('after'))])
        const row = new Decoder(nullable ? Uint8Array.from([1, ...wire]) : wire)
        expect(nullable ? row.readStringPtr() : row.readString()).toBe('')
        const error = row.error
        const remaining = row.remaining
        expect(error).toContain('UTF-8')
        expect(row.readString()).toBe('')
        expect(row.error).toBe(error)
        expect(row.remaining).toBe(remaining)
        expect(row.nextField()).toBe(false)
      }
    }
  })

  it('rejects malformed string array cells for all string-backed scalar types', () => {
    for (const type of ['String', 'Enum', 'Decimal'] as const) {
      for (const bytes of invalid) {
        expect(() => decodeScalarArray(Uint8Array.from([1, ...stringWire(bytes)]), type)).toThrow(/UTF-8/)
      }
    }
    expect(new Decoder(Uint8Array.of(0)).readStringPtr()).toBeNull()
  })

  it('discards malformed row arrays without consuming the next value', () => {
    const bytes = Uint8Array.from([3, ...stringWire(utf8.encode('before')),
      ...stringWire(invalid[0]), ...stringWire(utf8.encode('after'))])
    const row = new Decoder(bytes)
    expect(row.readStringArray()).toEqual([])
    expect(row.error).toContain('UTF-8')
    expect(row.remaining).toBe(stringWire(utf8.encode('after')).length)
    const healthy = new Decoder(stringWire(utf8.encode('\ufeffnext')))
    expect(healthy.readString()).toBe('\ufeffnext')
    expect(healthy.error).toBeNull()
  })

  it('discards malformed columns and does not consume later cells or columns', () => {
    for (const bytes of invalid) {
      for (const nullable of [false, true]) {
        const values = [utf8.encode('before'), bytes, utf8.encode('after')]
        const column = new ColumnarDecoder(columnWire(values, nullable))
        expect(column.nextColumn()).toBe(true)
        expect(nullable ? column.readColumnStringPtr() : column.readColumnString()).toEqual([])
        const error = column.error
        const offset = column.offset()
        expect(error).toContain('UTF-8')
        expect(column.readColumnString()).toEqual([])
        expect(column.readColumnStringPtr()).toEqual([])
        expect(column.nextColumn()).toBe(false)
        expect(column.error).toBe(error)
        expect(column.offset()).toBe(offset)
      }
    }
  })

  it('validates raw JSON bytes and escaped Unicode without altering numbers', () => {
    for (const bytes of invalid) {
      expect(() => decodeJSONValue(Uint8Array.from([34, ...bytes, 34]))).toThrow()
    }
    expect(decodeJSONValue(utf8.encode('"\ufeffvalue"'))).toBe('\ufeffvalue')
    expect(() => decodeJSONValue(utf8.encode('"\\ud800"'))).toThrow()
    expect(decodeJSONValue(utf8.encode('9223372036854775807'))).toBe(9223372036854775807n)
    expect(() => decodeJSONValue(utf8.encode('\ufeff{}'))).toThrow()
    expect(() => decodeJSONValue(utf8.encode('{'))).toThrow()
  })

  it('matches an independent UTF-8 validator on deterministic byte sequences', () => {
    let state = 0x51a7c0de
    for (let sample = 0; sample < 4352; sample++) {
      const bytes = new Uint8Array(sample < 256 ? 1 : sample % 32 + 1)
      for (let index = 0; index < bytes.length; index++) {
        state = (Math.imul(state, 1664525) + 1013904223) >>> 0
        bytes[index] = sample < 256 ? sample : state >>> 24
      }
      const row = new Decoder(stringWire(bytes))
      const value = row.readString()
      expect(row.error === null).toBe(isUtf8(bytes))
      if (row.error === null) expect(value).toBe(Buffer.from(bytes).toString('utf8'))
    }
  })

  it('does not apply UTF-8 validation to opaque Bytes', () => {
    for (const bytes of invalid) {
      const row = new Decoder(stringWire(bytes))
      expect(row.readBytes()).toEqual(bytes)
      expect(row.error).toBeNull()
    }
  })
})

function stringWire(bytes: Uint8Array): Uint8Array {
  const encoder = new Encoder()
  encoder.writeBytes(bytes)
  return encoder.bytes()
}

function columnWire(values: Uint8Array[], nullable: boolean): Uint8Array {
  const encoder = new Encoder()
  encoder.writeVarint(values.length)
  encoder.writeVarint(0)
  encoder.writeVarint(1)
  for (const value of values) {
    if (nullable) encoder.writeBool(true)
    encoder.writeBytes(value)
  }
  encoder.writeEnd()
  return encoder.bytes()
}
