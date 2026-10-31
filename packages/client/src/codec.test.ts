import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import {
  Encoder, Decoder, ColumnarDecoder, fieldMaskSet, fieldMaskHas,
  formatUUID, parseUUID, decodeScalarArray, unixSecondsToISO,
} from './codec'

describe('encoder buffer ownership transfer', () => {
  it('writes typed delimited values without a capturing callback and rolls back failures', () => {
    const encoder = new Encoder(1)
    const input = new Uint8Array(130).fill(9)
    encoder.writeVarint(7)
    encoder.writeDelimitedValue(input, (target, value) => {
      expect(value).toBe(input)
      target.writeRawBytes(value)
    })
    const expected = new Encoder()
    expected.writeVarint(7)
    expected.writeBytes(input)
    expect(encoder.bytes()).toEqual(expected.bytes())
    const snapshot = encoder.bytes()
    expect(() => encoder.writeDelimitedValue(42n, (target, value) => {
      target.writeSvarint(value)
      throw Error('writer failure')
    })).toThrow('writer failure')
    expect(encoder.bytes()).toEqual(snapshot)
  })
  it('does not retain a large unused backing buffer for a short response', () => {
    const encoder = new Encoder(65536)
    encoder.writeSvarint(1n)
    const result = encoder.takeBytes()
    expect([...result]).toEqual([2])
    expect(result.buffer.byteLength).toBe(1)
  })
  it('returns only encoded bytes and isolates later writes and resets', () => {
    const encoder = new Encoder(16)
    encoder.writeSvarint(42n)
    const first = encoder.takeBytes()
    expect([...first]).toEqual([84])
    expect(first.buffer.byteLength).toBe(16)
    encoder.reset()
    encoder.writeSvarint(99n)
    const second = encoder.takeBytes()
    expect([...first]).toEqual([84])
    expect([...second]).toEqual([198, 1])
    expect(first.buffer).not.toBe(second.buffer)
    first[0] = 0
    expect([...second]).toEqual([198, 1])
    expect(encoder.takeBytes()).toHaveLength(0)
    encoder.writeFixed64(1.25)
    expect(new Decoder(encoder.takeBytes()).readFloat()).toBe(1.25)
  })
})

describe('canonical UUID text', () => {
  const fixture = JSON.parse(readFileSync(new URL('../../../pkg/lux/codec/testdata/protocol-v1.json', import.meta.url), 'utf8')) as {
    uuidCanonicalCases: { input: string; canonical: string | null }[]
  }
  it('rejects every corrupted position and non-canonical length', () => {
    const valid = '00112233-4455-6677-8899-aabbccddeeff'
    for (let index = 0; index < valid.length; index++) {
      for (const replacement of valid[index] === '-' ? ['0', '_', '−'] : ['g', '-', '\u0000', 'é']) {
        expect(parseUUID(valid.slice(0, index) + replacement + valid.slice(index + 1))).toBeNull()
      }
    }
    for (const input of ['', valid.slice(1), valid + '0', valid.replaceAll('-', '')]) {
      expect(parseUUID(input)).toBeNull()
    }
  })
  it.each(fixture.uuidCanonicalCases)('checks all separators and hex digits: $input', ({ input, canonical }) => {
    const bytes = parseUUID(input)
    const encoder = new Encoder()
    if (canonical === null) {
      expect(bytes).toBeNull()
      expect(() => encoder.writeUUID(input)).toThrow()
      expect(encoder.bytes()).toHaveLength(0)
    } else {
      expect(bytes).not.toBeNull()
      encoder.writeUUID(input)
      expect(new Decoder(encoder.bytes()).readUUID()).toBe(canonical)
    }
  })
})

describe('structured response boundaries', () => {
  it('requires row and column end markers even when values finish exactly at EOF', () => {
    const row = new Decoder(new Uint8Array([0, 1, 84]))
    row.skipArenaHeader()
    expect(row.nextField()).toBe(true)
    expect(row.readInt()).toBe(42n)
    expect(row.nextField()).toBe(false)
    expect(row.error).toMatch(/end marker/)

    const column = new ColumnarDecoder(new Uint8Array([1, 0, 1, 128, 1]))
    expect(column.nextColumn()).toBe(true)
    expect(column.readColumnInt()).toEqual([64n])
    expect(column.nextColumn()).toBe(false)
    expect(column.error).toMatch(/end marker/)
  })
})

describe('Encoder', () => {
  it('preserves Float boundaries after small-buffer and large-buffer growth', () => {
    const enc = new Encoder(1)
    enc.writeVarint(42)
    enc.writeRawBytes(new Uint8Array(4096))
    enc.writeFixed64(-0)
    enc.writeFixed64(Number.MIN_VALUE)
    enc.writeRawBytes(new Uint8Array(16384))
    enc.writeFixed64(Number.MAX_VALUE)
    const bytes = enc.bytes()
    const data = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
    expect(Object.is(data.getFloat64(4097, true), -0)).toBe(true)
    expect(data.getFloat64(4105, true)).toBe(Number.MIN_VALUE)
    expect(data.getFloat64(20497, true)).toBe(Number.MAX_VALUE)
  })

  it('writeVarint encodes small values in 1 byte', () => {
    const enc = new Encoder()
    enc.writeVarint(0)
    enc.writeVarint(1)
    enc.writeVarint(127)
    const bytes = enc.bytes()
    expect(bytes[0]).toBe(0)
    expect(bytes[1]).toBe(1)
    expect(bytes[2]).toBe(127)
    expect(bytes.length).toBe(3)
  })

  it('writeVarint encodes multi-byte values', () => {
    const enc = new Encoder()
    enc.writeVarint(128)
    const bytes = enc.bytes()
    // 128 = 0x80 → LEB128: [0x80, 0x01]
    expect(bytes[0]).toBe(0x80)
    expect(bytes[1]).toBe(0x01)
  })

  it('writeVarint encodes large values', () => {
    const enc = new Encoder()
    enc.writeVarint(300)
    const bytes = enc.bytes()
    // 300 = 0x12C → LEB128: [0xAC, 0x02]
    expect(bytes[0]).toBe(0xAC)
    expect(bytes[1]).toBe(0x02)
  })

  it('writeSvarint encodes positive values with zigzag', () => {
    const enc = new Encoder()
    enc.writeSvarint(BigInt(0))
    enc.writeSvarint(BigInt(1))
    enc.writeSvarint(BigInt(2))
    const bytes = enc.bytes()
    // zigzag: 0→0, 1→2, 2→4
    expect(bytes[0]).toBe(0)
    expect(bytes[1]).toBe(2)
    expect(bytes[2]).toBe(4)
  })

  it('writeSvarint encodes negative values with zigzag', () => {
    const enc = new Encoder()
    enc.writeSvarint(BigInt(-1))
    enc.writeSvarint(BigInt(-2))
    const bytes = enc.bytes()
    // zigzag: -1→1, -2→3
    expect(bytes[0]).toBe(1)
    expect(bytes[1]).toBe(3)
  })

  it('writeFixed64 writes 8 bytes little-endian float64', () => {
    const enc = new Encoder()
    enc.writeFixed64(3.14)
    const bytes = enc.bytes()
    expect(bytes.length).toBe(8)
    const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
    expect(view.getFloat64(0, true)).toBeCloseTo(3.14)
  })

  it('writeString writes length-prefixed UTF-8', () => {
    const enc = new Encoder()
    enc.writeString('hello')
    const bytes = enc.bytes()
    // length prefix (5) + 5 bytes of "hello"
    expect(bytes[0]).toBe(5)
    expect(bytes.length).toBe(6)
  })

  it('writeString handles empty string', () => {
    const enc = new Encoder()
    enc.writeString('')
    const bytes = enc.bytes()
    expect(bytes[0]).toBe(0)
    expect(bytes.length).toBe(1)
  })

  it('writeBool writes 0 or 1', () => {
    const enc = new Encoder()
    enc.writeBool(true)
    enc.writeBool(false)
    const bytes = enc.bytes()
    expect(bytes[0]).toBe(1)
    expect(bytes[1]).toBe(0)
  })

  it('writeEnd writes 0x00 terminator', () => {
    const enc = new Encoder()
    enc.writeEnd()
    const bytes = enc.bytes()
    expect(bytes[0]).toBe(0x00)
    expect(bytes.length).toBe(1)
  })

  it('writes length-delimited values in place without an intermediate buffer', () => {
    const enc = new Encoder(4)
    enc.writeDelimited(value => {
      value.writeVarint(7)
      value.writeString('go')
    })
    expect(Array.from(enc.bytes())).toEqual([4, 7, 2, 103, 111])

    const large = new Encoder(4)
    large.writeDelimited(value => value.writeRawBytes(new Uint8Array(130).fill(9)))
    const bytes = large.bytes()
    expect(Array.from(bytes.slice(0, 2))).toEqual([130, 1])
    expect(bytes.length).toBe(132)
    expect(bytes.slice(2).every(byte => byte === 9)).toBe(true)
  })

  it('reset allows encoder reuse', () => {
    const enc = new Encoder()
    enc.writeVarint(42)
    enc.reset()
    enc.writeVarint(7)
    const bytes = enc.bytes()
    expect(bytes.length).toBe(1)
    expect(bytes[0]).toBe(7)
  })

  it('grows buffer when needed', () => {
    const enc = new Encoder(4) // small initial size
    enc.writeString('this is a longer string that exceeds initial buffer')
    const bytes = enc.bytes()
    expect(bytes.length).toBeGreaterThan(4)
  })
})

describe('Decoder', () => {
  it('reads varint via readInt (zigzag decoded)', () => {
    const enc = new Encoder()
    // Write field with ID=1 + signed int
    enc.writeFieldInt(1, 42n)
    enc.writeEnd()

    const dec = new Decoder(enc.bytes())
    expect(dec.nextField()).toBe(true)
    expect(dec.fieldID).toBe(1)
    expect(dec.readInt()).toBe(42n)
    expect(dec.nextField()).toBe(false)
  })

  it('reads negative int', () => {
    const enc = new Encoder()
    enc.writeFieldInt(2, -100n)
    enc.writeEnd()

    const dec = new Decoder(enc.bytes())
    expect(dec.nextField()).toBe(true)
    expect(dec.fieldID).toBe(2)
    expect(dec.readInt()).toBe(-100n)
  })

  it('reads float64', () => {
    const enc = new Encoder()
    enc.writeFieldFloat(3, 2.718)
    enc.writeEnd()

    const dec = new Decoder(enc.bytes())
    expect(dec.nextField()).toBe(true)
    expect(dec.fieldID).toBe(3)
    expect(dec.readFloat()).toBeCloseTo(2.718)
  })

  it('reads string', () => {
    const enc = new Encoder()
    enc.writeFieldString(4, 'luxo')
    enc.writeEnd()

    const dec = new Decoder(enc.bytes())
    expect(dec.nextField()).toBe(true)
    expect(dec.fieldID).toBe(4)
    expect(dec.readString()).toBe('luxo')
  })

  it('reads bool', () => {
    const enc = new Encoder()
    enc.writeFieldBool(5, true)
    enc.writeFieldBool(6, false)
    enc.writeEnd()

    const dec = new Decoder(enc.bytes())
    expect(dec.nextField()).toBe(true)
    expect(dec.fieldID).toBe(5)
    expect(dec.readBool()).toBe(true)
    expect(dec.nextField()).toBe(true)
    expect(dec.fieldID).toBe(6)
    expect(dec.readBool()).toBe(false)
    expect(dec.nextField()).toBe(false)
  })

  it('reads empty string', () => {
    const enc = new Encoder()
    enc.writeFieldString(1, '')
    enc.writeEnd()

    const dec = new Decoder(enc.bytes())
    expect(dec.nextField()).toBe(true)
    expect(dec.readString()).toBe('')
  })

  it('round-trip multiple fields', () => {
    const enc = new Encoder()
    enc.writeFieldInt(1, 999n)
    enc.writeFieldFloat(2, -0.5)
    enc.writeFieldString(3, 'hello world')
    enc.writeFieldBool(4, true)
    enc.writeEnd()

    const dec = new Decoder(enc.bytes())

    expect(dec.nextField()).toBe(true)
    expect(dec.fieldID).toBe(1)
    expect(dec.readInt()).toBe(999n)

    expect(dec.nextField()).toBe(true)
    expect(dec.fieldID).toBe(2)
    expect(dec.readFloat()).toBeCloseTo(-0.5)

    expect(dec.nextField()).toBe(true)
    expect(dec.fieldID).toBe(3)
    expect(dec.readString()).toBe('hello world')

    expect(dec.nextField()).toBe(true)
    expect(dec.fieldID).toBe(4)
    expect(dec.readBool()).toBe(true)

    expect(dec.nextField()).toBe(false)
  })

  it('remaining returns bytes left', () => {
    const enc = new Encoder()
    enc.writeFieldInt(1, 5n)
    enc.writeEnd()
    const bytes = enc.bytes()

    const dec = new Decoder(bytes)
    const total = dec.remaining
    expect(total).toBe(bytes.length)

    dec.nextField()
    dec.readInt()
    expect(dec.remaining).toBeLessThan(total)
  })

  it('round-trips bytes arrays', () => {
    const enc = new Encoder()
    enc.writeFieldBytesArray(1, [new Uint8Array([1, 2]), new Uint8Array(0)])
    const dec = new Decoder(enc.bytes())
    expect(dec.nextField()).toBe(true)
    expect(dec.readBytesArray()).toEqual([new Uint8Array([1, 2]), new Uint8Array(0)])
  })
})

describe('fieldMask', () => {
  it('rejects IDs outside the selection domain without aliasing or modifying the mask', () => {
    for (const fieldID of [0, -1, 1.5, NaN, Infinity, 65536, 2147483647, 4294967297, Number.MAX_SAFE_INTEGER]) {
      const mask = Uint8Array.of(1)
      expect(() => fieldMaskHas(mask, fieldID)).toThrow(RangeError)
      expect(() => fieldMaskSet(mask, fieldID)).toThrow(RangeError)
      expect(mask).toEqual(Uint8Array.of(1))
    }
  })

  it('keeps the maximum selection field in an 8 KiB mask', () => {
    const mask = fieldMaskSet(new Uint8Array(0), 65535)
    expect(mask.length).toBe(8192)
    expect(mask[8191]).toBe(0x40)
    expect(fieldMaskHas(mask, 65535)).toBe(true)
    expect(fieldMaskHas(mask, 1)).toBe(false)
  })

  it('fieldMaskSet sets bits correctly', () => {
    let mask: Uint8Array = new Uint8Array(1)
    mask = fieldMaskSet(mask, 1)
    expect(mask[0] & 1).toBe(1)

    mask = fieldMaskSet(mask, 8)
    expect(mask[0] & 0x80).toBe(0x80)
  })

  it('fieldMaskSet grows mask when needed', () => {
    let mask: Uint8Array = new Uint8Array(1)
    mask = fieldMaskSet(mask, 9) // needs byte index 1
    expect(mask.length).toBe(2)
    expect(mask[1] & 1).toBe(1)
  })

  it('fieldMaskSet grows for large field IDs', () => {
    let mask: Uint8Array = new Uint8Array(0)
    mask = fieldMaskSet(mask, 17)
    expect(mask.length).toBe(3)
    expect(fieldMaskHas(mask, 17)).toBe(true)
  })

  it('fieldMaskHas returns true for set bits', () => {
    let mask: Uint8Array = new Uint8Array(2)
    mask = fieldMaskSet(mask, 3)
    mask = fieldMaskSet(mask, 10)
    expect(fieldMaskHas(mask, 3)).toBe(true)
    expect(fieldMaskHas(mask, 10)).toBe(true)
    expect(fieldMaskHas(mask, 4)).toBe(false)
  })

  it('fieldMaskHas rejects zero and returns false for unselected valid IDs', () => {
    const mask = new Uint8Array(1)
    expect(() => fieldMaskHas(mask, 0)).toThrow(RangeError)
    expect(fieldMaskHas(mask, 100)).toBe(false)
  })
})

describe('DateTime (seconds/nanoseconds → RFC3339 string)', () => {
  // 2026-05-28T12:00:00Z = 1779710400 unix seconds (whole seconds, no fractional part).
  const SECONDS = Math.floor(Date.UTC(2026, 4, 28, 12, 0, 0) / 1000)
  const ISO = '2026-05-28T12:00:00Z'

  it('unixSecondsToISO matches Go RFC3339 output (no .000 millis)', () => {
    expect(unixSecondsToISO(SECONDS)).toBe(ISO)
    expect(unixSecondsToISO(0)).toBe('1970-01-01T00:00:00Z')
  })

  it('readDateTime decodes seconds/nanoseconds to ISO string', () => {
    const enc = new Encoder()
    enc.writeDateTime(ISO)
    const dec = new Decoder(enc.bytes())
    expect(dec.readDateTime()).toBe(ISO)
  })

  it('readDateTime handles epoch and pre-epoch (negative) values', () => {
    const enc = new Encoder()
    enc.writeDateTime('1970-01-01T00:00:00Z')
    enc.writeDateTime('1969-12-31T23:59:59Z')
    const dec = new Decoder(enc.bytes())
    expect(dec.readDateTime()).toBe('1970-01-01T00:00:00Z')
    expect(dec.readDateTime()).toBe('1969-12-31T23:59:59Z')
  })

  it('readDateTimePtr returns null for 0x00 flag', () => {
    const data = new Uint8Array([0x00])
    expect(new Decoder(data).readDateTimePtr()).toBe(null)
  })

  it('readDateTimePtr decodes present value (0x01 + seconds/nanoseconds)', () => {
    const enc = new Encoder()
    enc.writeBool(true) // 0x01 present flag
    enc.writeDateTime(ISO)
    const dec = new Decoder(enc.bytes())
    expect(dec.readDateTimePtr()).toBe(ISO)
  })

  it('readDateTimePtr returns null at EOF', () => {
    expect(new Decoder(new Uint8Array(0)).readDateTimePtr()).toBe(null)
  })

  it('ColumnarDecoder.readColumnDateTime decodes a DateTime column', () => {
    const enc = new Encoder()
    enc.writeVarint(2) // count
    enc.writeVarint(0) // arena size
    enc.writeVarint(5) // fieldID
    enc.writeDateTime(ISO)
    enc.writeDateTime('1970-01-01T00:00:00Z')
    enc.writeEnd()
    const r = new ColumnarDecoder(enc.bytes())
    expect(r.arenaSize).toBe(0)
    expect(r.nextColumn()).toBe(true)
    expect(r.fieldID).toBe(5)
    expect(r.readColumnDateTime()).toEqual([ISO, '1970-01-01T00:00:00Z'])
  })

  it('ColumnarDecoder.readColumnDateTimePtr handles nulls', () => {
    const enc = new Encoder()
    enc.writeVarint(2) // count
    enc.writeVarint(0) // arena size
    enc.writeVarint(5) // fieldID
    enc.writeBool(false) // null flag for first
    enc.writeBool(true) // present flag for second
    enc.writeDateTime(ISO)
    enc.writeEnd()
    const r = new ColumnarDecoder(enc.bytes())
    expect(r.nextColumn()).toBe(true)
    expect(r.readColumnDateTimePtr()).toEqual([null, ISO])
  })

  it('decodeScalarArray returns ISO strings for DateTime', () => {
    const enc = new Encoder()
    enc.writeVarint(2) // array count
    enc.writeDateTime(ISO)
    enc.writeDateTime('1970-01-01T00:00:00Z')
    expect(decodeScalarArray(enc.bytes(), 'DateTime')).toEqual([ISO, '1970-01-01T00:00:00Z'])
  })

  it('decodeScalarArray keeps Duration as raw nanosecond numbers', () => {
    const enc = new Encoder()
    enc.writeVarint(2)
    enc.writeSvarint(BigInt(1000000000))
    enc.writeSvarint(BigInt(500))
    expect(decodeScalarArray(enc.bytes(), 'Duration')).toEqual([1000000000n, 500n])
  })

  it('rejects non-canonical boolean and nullable markers', () => {
    const boolDecoder = new Decoder(new Uint8Array([0x02]))
    expect(boolDecoder.readBool()).toBe(false)
    expect(boolDecoder.error).toContain('invalid bool')

    const nullableDecoder = new Decoder(new Uint8Array([0x02]))
    expect(nullableDecoder.readIntPtr()).toBe(null)
    expect(nullableDecoder.error).toContain('invalid nullable')
  })
})
