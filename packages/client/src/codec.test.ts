import { describe, it, expect } from 'vitest'
import {
  Encoder, Decoder, ColumnarDecoder, fieldMaskSet, fieldMaskHas,
  formatUUID, parseUUID, decodeScalarArray, unixSecondsToISO,
} from './codec'

describe('Encoder', () => {
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
    enc.writeSvarint(0)
    enc.writeSvarint(1)
    enc.writeSvarint(2)
    const bytes = enc.bytes()
    // zigzag: 0→0, 1→2, 2→4
    expect(bytes[0]).toBe(0)
    expect(bytes[1]).toBe(2)
    expect(bytes[2]).toBe(4)
  })

  it('writeSvarint encodes negative values with zigzag', () => {
    const enc = new Encoder()
    enc.writeSvarint(-1)
    enc.writeSvarint(-2)
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
    enc.writeFieldInt(1, 42)
    enc.writeEnd()

    const dec = new Decoder(enc.bytes())
    expect(dec.nextField()).toBe(true)
    expect(dec.fieldID).toBe(1)
    expect(dec.readInt()).toBe(42)
    expect(dec.nextField()).toBe(false)
  })

  it('reads negative int', () => {
    const enc = new Encoder()
    enc.writeFieldInt(2, -100)
    enc.writeEnd()

    const dec = new Decoder(enc.bytes())
    expect(dec.nextField()).toBe(true)
    expect(dec.fieldID).toBe(2)
    expect(dec.readInt()).toBe(-100)
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
    enc.writeFieldInt(1, 999)
    enc.writeFieldFloat(2, -0.5)
    enc.writeFieldString(3, 'hello world')
    enc.writeFieldBool(4, true)
    enc.writeEnd()

    const dec = new Decoder(enc.bytes())

    expect(dec.nextField()).toBe(true)
    expect(dec.fieldID).toBe(1)
    expect(dec.readInt()).toBe(999)

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
    enc.writeFieldInt(1, 5)
    enc.writeEnd()
    const bytes = enc.bytes()

    const dec = new Decoder(bytes)
    const total = dec.remaining
    expect(total).toBe(bytes.length)

    dec.nextField()
    dec.readInt()
    expect(dec.remaining).toBeLessThan(total)
  })
})

describe('fieldMask', () => {
  it('fieldMaskSet sets bits correctly', () => {
    let mask = new Uint8Array(1)
    mask = fieldMaskSet(mask, 0)
    expect(mask[0] & 1).toBe(1)

    mask = fieldMaskSet(mask, 7)
    expect(mask[0] & 0x80).toBe(0x80)
  })

  it('fieldMaskSet grows mask when needed', () => {
    let mask = new Uint8Array(1)
    mask = fieldMaskSet(mask, 8) // needs byte index 1
    expect(mask.length).toBe(2)
    expect(mask[1] & 1).toBe(1)
  })

  it('fieldMaskSet grows for large field IDs', () => {
    let mask = new Uint8Array(0)
    mask = fieldMaskSet(mask, 16)
    expect(mask.length).toBe(3)
    expect(fieldMaskHas(mask, 16)).toBe(true)
  })

  it('fieldMaskHas returns true for set bits', () => {
    let mask = new Uint8Array(2)
    mask = fieldMaskSet(mask, 3)
    mask = fieldMaskSet(mask, 10)
    expect(fieldMaskHas(mask, 3)).toBe(true)
    expect(fieldMaskHas(mask, 10)).toBe(true)
    expect(fieldMaskHas(mask, 4)).toBe(false)
  })

  it('fieldMaskHas returns false for out-of-range field IDs', () => {
    const mask = new Uint8Array(1)
    expect(fieldMaskHas(mask, 100)).toBe(false)
  })
})

describe('DateTime (svarint unix seconds → RFC3339 string)', () => {
  // 2026-05-28T12:00:00Z = 1779710400 unix seconds (whole seconds, no fractional part).
  const SECONDS = Math.floor(Date.UTC(2026, 4, 28, 12, 0, 0) / 1000)
  const ISO = '2026-05-28T12:00:00Z'

  it('unixSecondsToISO matches Go RFC3339 output (no .000 millis)', () => {
    expect(unixSecondsToISO(SECONDS)).toBe(ISO)
    expect(unixSecondsToISO(0)).toBe('1970-01-01T00:00:00Z')
  })

  it('readDateTime decodes svarint(seconds) to ISO string', () => {
    const enc = new Encoder()
    enc.writeSvarint(SECONDS)
    const dec = new Decoder(enc.bytes())
    expect(dec.readDateTime()).toBe(ISO)
  })

  it('readDateTime handles epoch and pre-epoch (negative) values', () => {
    const enc = new Encoder()
    enc.writeSvarint(0)
    enc.writeSvarint(-1)
    const dec = new Decoder(enc.bytes())
    expect(dec.readDateTime()).toBe('1970-01-01T00:00:00Z')
    expect(dec.readDateTime()).toBe('1969-12-31T23:59:59Z')
  })

  it('readDateTimePtr returns null for 0x00 flag', () => {
    const data = new Uint8Array([0x00])
    expect(new Decoder(data).readDateTimePtr()).toBe(null)
  })

  it('readDateTimePtr decodes present value (0x01 + svarint)', () => {
    const enc = new Encoder()
    enc.writeBool(true) // 0x01 present flag
    enc.writeSvarint(SECONDS)
    const dec = new Decoder(enc.bytes())
    expect(dec.readDateTimePtr()).toBe(ISO)
  })

  it('readDateTimePtr returns null at EOF', () => {
    expect(new Decoder(new Uint8Array(0)).readDateTimePtr()).toBe(null)
  })

  it('ColumnarDecoder.readColumnDateTime decodes a DateTime column', () => {
    const enc = new Encoder()
    enc.writeVarint(2) // count
    enc.writeVarint(5) // fieldID
    enc.writeSvarint(SECONDS)
    enc.writeSvarint(0)
    enc.writeEnd()
    const r = new ColumnarDecoder(enc.bytes())
    expect(r.nextColumn()).toBe(true)
    expect(r.fieldID).toBe(5)
    expect(r.readColumnDateTime()).toEqual([ISO, '1970-01-01T00:00:00Z'])
  })

  it('ColumnarDecoder.readColumnDateTimePtr handles nulls', () => {
    const enc = new Encoder()
    enc.writeVarint(2) // count
    enc.writeVarint(5) // fieldID
    enc.writeBool(false) // null flag for first
    enc.writeBool(true) // present flag for second
    enc.writeSvarint(SECONDS)
    enc.writeEnd()
    const r = new ColumnarDecoder(enc.bytes())
    expect(r.nextColumn()).toBe(true)
    expect(r.readColumnDateTimePtr()).toEqual([null, ISO])
  })

  it('decodeScalarArray returns ISO strings for DateTime', () => {
    const enc = new Encoder()
    enc.writeVarint(2) // array count
    enc.writeSvarint(SECONDS)
    enc.writeSvarint(0)
    expect(decodeScalarArray(enc.bytes(), 'DateTime')).toEqual([ISO, '1970-01-01T00:00:00Z'])
  })

  it('decodeScalarArray keeps Duration as raw nanosecond numbers', () => {
    const enc = new Encoder()
    enc.writeVarint(2)
    enc.writeSvarint(1000000000)
    enc.writeSvarint(500)
    expect(decodeScalarArray(enc.bytes(), 'Duration')).toEqual([1000000000, 500])
  })
})
