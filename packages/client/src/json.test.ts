import { describe, expect, it, vi } from 'vitest'
import { readFileSync } from 'node:fs'
import { decodeLuxoJSON, encodeLuxoJSON, int64FromJSON, floatFromJSON, stringFromJSON, booleanFromJSON, normalizeLuxoJSON, jsonNumber, isJSONNumber } from './json'
import { decodeJSONValue, Encoder } from './codec'
import { decodeBinaryError, jsonResponseError } from './error-codec'
import { decodeJSONEnvelope } from './json'

describe('lossless JSON', () => {
  it('requires actual JSON strings and booleans without coercion', () => {
    for (const value of ['', 'false', '9007199254740993', '发布 café', '\ufefftext']) {
      expect(stringFromJSON(value)).toBe(value)
      expect(() => booleanFromJSON(value)).toThrow(TypeError)
    }
    for (const value of [true, false]) {
      expect(booleanFromJSON(value)).toBe(value)
      expect(() => stringFromJSON(value)).toThrow(TypeError)
    }
    for (const value of [0, 1, 9007199254740993n, jsonNumber('1.0'), null, undefined, [], {}, new Boolean(false), new String('text')]) {
      expect(() => stringFromJSON(value)).toThrow(TypeError)
      expect(() => booleanFromJSON(value)).toThrow(TypeError)
    }
  })

  it('pretty-prints diagnostic JSON without rounding raw tokens', () => {
    const value = decodeLuxoJSON('{"id":9223372036854775807,"value":1.234567890123456789,"zero":-0}')
    expect(encodeLuxoJSON(value, 2)).toBe('{\n  "id": 9223372036854775807,\n  "value": 1.234567890123456789,\n  "zero": -0\n}')
  })
  it('rejects a parser without numeric source access instead of returning rounded values', () => {
    const parser = vi.spyOn(JSON, 'parse').mockImplementationOnce((_text, reviver) =>
      reviver?.('', 9223372036854775808))
    try {
      expect(() => decodeLuxoJSON('9223372036854775807')).toThrow('JSON.parse source access')
    } finally {
      parser.mockRestore()
    }
  })

  it('does not leak numeric-syntax state across keys or repeated decodes', () => {
    const cases = [
      ['1.25', 'object'], ['42', 'number'], ['1e0', 'object'], ['-0', 'object'], ['0', 'number'],
      ['1E+03', 'object'], ['9223372036854775807', 'bigint'], ['1.0', 'object'], ['-42', 'number'],
    ] as const
    for (let round = 0; round < 3; round++) {
      const ordered = round % 2 ? cases.toReversed() : cases
      const text = `[${ordered.map(([token]) => token).join(',')}]`
      const values = decodeLuxoJSON(text) as unknown[]
      expect(encodeLuxoJSON(values)).toBe(text)
      expect(values.map(value => typeof value)).toEqual(ordered.map(([, type]) => type))
      for (const [token] of ordered) expect(encodeLuxoJSON(decodeLuxoJSON(token))).toBe(token)
    }
  })

  it('matches canonical explicit integer and Float conversions without changing tokens', () => {
    const fixture = JSON.parse(readFileSync(new URL('../../../pkg/lux/codec/testdata/protocol-v1.json', import.meta.url), 'utf8')) as {
      jsonNumberConversions: { token: string; int64: string | null; float64Bits: string | null }[]
    }
    expect(fixture.jsonNumberConversions.length).toBeGreaterThan(0)
    const bits = new DataView(new ArrayBuffer(8))
    for (const item of fixture.jsonNumberConversions) {
      for (const value of [jsonNumber(item.token), decodeLuxoJSON(item.token)]) {
        if (item.int64 === null) expect(() => int64FromJSON(value), item.token).toThrow()
        else expect(int64FromJSON(value).toString(), item.token).toBe(item.int64)
        if (item.float64Bits === null) expect(() => floatFromJSON(value), item.token).toThrow()
        else {
          bits.setFloat64(0, floatFromJSON(value))
          expect(bits.getBigUint64(0).toString(16).padStart(16, '0'), item.token).toBe(item.float64Bits)
        }
        expect(encodeLuxoJSON(value), item.token).toBe(item.token)
      }
    }
  })

  it('preserves host negative zero without changing positive zero', () => {
    expect(encodeLuxoJSON(-0)).toBe('-0')
    expect(encodeLuxoJSON(0)).toBe('0')
    const value = Object.freeze({ n: -0, items: Object.freeze([0, -0, Number.MIN_VALUE, -Number.MIN_VALUE]) })
    expect(encodeLuxoJSON(value)).toBe('{"n":-0,"items":[0,-0,5e-324,-5e-324]}')
    const decoded = decodeLuxoJSON(encodeLuxoJSON(value)) as { n: unknown; items: unknown[] }
    expect(Object.is(floatFromJSON(decoded.n), -0)).toBe(true)
    expect(decoded.items.map(floatFromJSON)).toEqual([0, -0, Number.MIN_VALUE, -Number.MIN_VALUE])
    expect(encodeLuxoJSON({ toJSON: () => -0 })).toBe('-0')
    expect(Object.is(value.n, -0)).toBe(true)
  })

  it('validates explicitly constructed numbers and only converts on request', () => {
    for (const token of ['-0', '1.2300', '1E+3', '123456789012345678901234567890', '1e-400', '1e400']) {
      const value = jsonNumber(token)
      expect(isJSONNumber(value)).toBe(true)
      expect(Object.isFrozen(value)).toBe(true)
      expect(value.rawJSON).toBe(token)
      expect(encodeLuxoJSON(value)).toBe(token)
    }
    for (const token of ['', ' 1', '1 ', '01', '+1', '1.', '1e', 'NaN', 'Infinity', 'null', 'true', '"1"', '[]', '{}']) {
      expect(() => jsonNumber(token)).toThrow()
    }
    expect(isJSONNumber({ rawJSON: '1.2' })).toBe(false)
    expect(isJSONNumber(null)).toBe(false)
    expect(int64FromJSON(jsonNumber('-9223372036854775808'))).toBe(-(1n << 63n))
    for (const token of ['9223372036854775808', '1e0', '1.0']) expect(() => int64FromJSON(jsonNumber(token))).toThrow()
    for (const token of ['1e400', '1e-400', '-1e-400']) expect(() => floatFromJSON(jsonNumber(token))).toThrow()
    expect(floatFromJSON(jsonNumber('0.12345678901234567890123456789'))).toBe(0.12345678901234568)
    expect(floatFromJSON(jsonNumber('0e99999'))).toBe(0)
    expect(floatFromJSON(jsonNumber('-0e-400'))).toBe(-0)
  })

  it('preserves numeric tokens in generic fields and errors in both encodings', () => {
    for (const token of ['0', '-0', '1.0', '1E+03', '9007199254740993',
      '123456789012345678901234567890', '0.12345678901234567890123456789', '1e400', '1e-400']) {
      const text = `{"n":${token},"items":[${token},null],"nested":{"n":${token}}}`
      for (const typed of [false, true]) {
        const value = decodeLuxoJSON(text, typed)
        expect(encodeLuxoJSON(value), token).toBe(text)
        expect(encodeLuxoJSON(normalizeLuxoJSON(value)), token).toBe(text)
      }
      expect(encodeLuxoJSON(decodeJSONValue(new TextEncoder().encode(text))), token).toBe(text)
      const error = jsonResponseError(decodeJSONEnvelope(`{"error":"Custom","code":400,"message":"failure","data":${text}}`))
      const binary = new Encoder()
      binary.writeFieldInt(1, 400n)
      binary.writeFieldString(2, 'Custom')
      binary.writeFieldString(3, 'failure')
      binary.writeFieldString(5, text)
      binary.writeEnd()
      expect(encodeLuxoJSON(error.data), token).toBe(text)
      expect(encodeLuxoJSON(decodeBinaryError(binary.bytes(), 400).data), token).toBe(text)
    }
  })

  it('preserves signed 64-bit numeric tokens without quoting', () => {
    for (const value of [0n, 9007199254740993n, -(1n << 63n), (1n << 63n) - 1n]) {
      const text = encodeLuxoJSON({ value })
      expect(text).toBe(`{"value":${value}}`)
      const result = decodeLuxoJSON(text) as { value: unknown }
      expect(int64FromJSON(result.value)).toBe(value)
    }
  })

  it('rejects coercion, overflow and integer-looking Float syntax for Int', () => {
    for (const text of ['1.0', '1e0', '9223372036854775808', '-9223372036854775809', '"12"', 'true', 'null']) {
      expect(() => int64FromJSON(decodeLuxoJSON(text, true))).toThrow()
    }
    expect(int64FromJSON(decodeLuxoJSON('-0', true))).toBe(0n)
    expect(() => int64FromJSON(9007199254740992)).toThrow()
    expect(floatFromJSON(decodeLuxoJSON('1e2', true))).toBe(100)
    expect(floatFromJSON(decodeLuxoJSON('-0', true))).toBe(-0)
    expect(() => floatFromJSON(decodeLuxoJSON('1e400', true))).toThrow()
  })

  it('retains arbitrary JSON numbers, strings and own prototype-named properties', () => {
    const text = '{"__proto__":{"x":1},"nested":[1e400,1.0,1234567890123456789012345],"s":"1e400"}'
    const value = normalizeLuxoJSON(decodeLuxoJSON(text, true)) as Record<string, unknown>
    expect(Object.prototype.hasOwnProperty.call(value, '__proto__')).toBe(true)
    expect(encodeLuxoJSON(value)).toBe(text)
    const frozen = Object.freeze({ values: Object.freeze([jsonNumber('1.2300'), 1n]) })
    expect(normalizeLuxoJSON(frozen)).toBe(frozen)
    expect(encodeLuxoJSON(frozen)).toBe('{"values":[1.2300,1]}')
  })

  it('rejects invalid JSON and non-finite numbers instead of silently emitting null', () => {
    for (const text of ['', '01', '[1,]', '{"x":}', '+1']) expect(() => decodeLuxoJSON(text)).toThrow()
    for (const value of [NaN, Infinity, -Infinity, undefined]) expect(() => encodeLuxoJSON(value)).toThrow()
    const cycle: unknown[] = []
    cycle.push(cycle)
    expect(() => encodeLuxoJSON(cycle)).toThrow()
  })
})
