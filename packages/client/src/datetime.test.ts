import { describe, expect, it } from 'vitest'
import { ColumnarDecoder, Decoder, Encoder, decodeScalarArray } from './codec'
import { encodeParam } from './transport'
import { readFileSync } from 'node:fs'
import { dateTimeFromJSON } from './datetime'
import { JSONParamValidator } from './json-params'

describe('shared DateTime text boundaries', () => {
  const fixture = JSON.parse(readFileSync(new URL('../../../pkg/lux/codec/testdata/protocol-v1.json', import.meta.url), 'utf8')) as {
    dateTimeBoundaryCases: { name: string; input: string; canonical: string | null }[]
  }
  it.each(fixture.dateTimeBoundaryCases)('$name', ({ input, canonical }) => {
    const encoder = new Encoder()
    const validator = new JSONParamValidator()
    validator.setSchema({ echo: { id: 1, params: [{ name: 'value', type: 'DateTime', fieldID: 1 }] } })
    if (canonical === null) {
      expect(() => encoder.writeDateTime(input)).toThrow()
      expect(encoder.bytes()).toHaveLength(0)
      expect(() => dateTimeFromJSON(input)).toThrow()
      expect(() => validator.prepare('echo', { value: input })).toThrow()
    } else {
      encoder.writeDateTime(input)
      expect(new Decoder(encoder.bytes()).readDateTime()).toBe(canonical)
      expect(dateTimeFromJSON(input)).toBe(input)
      expect(validator.prepare('echo', { value: input })).toEqual({ value: input })
    }
  })
  it.each([-62167219201n, 253402300800n, -(1n << 63n), (1n << 63n) - 1n])('rejects out-of-calendar seconds %s', (seconds) => {
    const encoder = new Encoder()
    encoder.writeSvarint(seconds)
    encoder.writeVarint(0)
    expect(() => new Decoder(encoder.bytes()).readDateTime()).toThrow()
  })
})

describe('nanosecond DateTime wire', () => {
  it.each([
    ['1970-01-01T00:00:00.000000001Z', '1970-01-01T00:00:00.000000001Z', 0n, 1],
    ['1969-12-31T23:59:59.999999999Z', '1969-12-31T23:59:59.999999999Z', -1n, 999999999],
    ['1970-01-01T08:59:59.123456789+09:00', '1969-12-31T23:59:59.123456789Z', -1n, 123456789],
    ['9999-12-31T23:59:59.999999999Z', '9999-12-31T23:59:59.999999999Z', 253402300799n, 999999999],
  ] as const)('preserves %s through parameter and response codecs', (input, canonical, seconds, nanos) => {
    const parameter = new Encoder()
    encodeParam(parameter, { fieldID: 1, type: 'DateTime' }, input)
    const wire = new Encoder()
    wire.writeSvarint(seconds)
    wire.writeVarint(nanos)
    expect(parameter.bytes()).toEqual(new Uint8Array([1, ...wire.bytes()]))
    expect(new Decoder(wire.bytes()).readDateTime()).toBe(canonical)
    const list = new Encoder()
    list.writeVarint(1)
    list.writeRawBytes(wire.bytes())
    expect(decodeScalarArray(list.bytes(), 'DateTime')).toEqual([canonical])
    const column = new Encoder()
    for (const value of [1, 0, 1]) column.writeVarint(value)
    column.writeRawBytes(wire.bytes())
    column.writeEnd()
    const reader = new ColumnarDecoder(column.bytes())
    expect(reader.nextColumn()).toBe(true)
    expect(reader.readColumnDateTime()).toEqual([canonical])
    expect(reader.nextColumn()).toBe(false)
    expect(reader.error).toBeNull()
  })

  it('rejects incomplete or invalid nanos instead of returning seconds', () => {
    for (const data of [[0], [0, 128], [0, 128, 148, 235, 220, 3]]) {
      const reader = new Decoder(new Uint8Array(data))
      reader.readDateTime()
      expect(reader.error).not.toBeNull()
    }
  })
})
