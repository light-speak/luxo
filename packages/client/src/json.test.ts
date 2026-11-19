import { describe, expect, it, vi } from 'vitest'
import { readFileSync } from 'node:fs'
import { decodeLuxoJSON, encodeLuxoJSON, int64FromJSON, floatFromJSON, stringFromJSON, booleanFromJSON, normalizeLuxoJSON, jsonNumber, isJSONNumber } from './json'
import { decodeJSONValue, Encoder } from './codec'
import { decodeBinaryError, jsonResponseError } from './error-codec'
import { decodeBoundedLuxoJSON, decodeJSONEnvelope } from './json'
import { encodeJSONParam, encodeParam } from './transport'
import { Decoder } from './codec'

describe('lossless JSON', () => {
  it.each([undefined, () => {}, Symbol('not JSON'), { toJSON: () => undefined }])
  ('rejects non-serializable roots consistently through the binary JSON parameter encoder: %s', value => {
    expect(() => encodeLuxoJSON(value)).toThrow(TypeError)
    expect(() => encodeJSONParam(value)).toThrow('value is not JSON encodable')
  })

  it('propagates a business serializer error through the binary JSON parameter encoder', () => {
    const failure = new Error('business serialization failed')
    const value = { toJSON() { throw failure } }
    expect(() => encodeJSONParam(value)).toThrow(failure)
  })

  it.each(['rawJSON', 'isRawJSON'])('rejects unsupported runtimes before encoding or parsing (%s)', name => {
    const original = Object.getOwnPropertyDescriptor(JSON, name)
    expect(original).toBeDefined()
    Object.defineProperty(JSON, name, { ...original, value: undefined })
    try {
      expect(() => encodeLuxoJSON({ value: 42n })).toThrow('requires JSON.rawJSON')
      expect(() => decodeLuxoJSON('{"value":9223372036854775807}')).toThrow('requires JSON.rawJSON')
    } finally { Object.defineProperty(JSON, name, original!) }
  })

  it.each(['null', '[]', '"text"', '1', '1e0'])('rejects non-object response envelopes (%s)', text => {
    expect(() => decodeJSONEnvelope(text)).toThrow('expected a JSON response object')
  })

  it('rejects unterminated long strings even when quoted delimiters trigger the depth scanner', () => {
    const opening = '["' + '['.repeat(300)
    for (const text of [opening, opening + '\\"']) {
      expect(() => decodeBoundedLuxoJSON(text)).toThrow(SyntaxError)
      expect(() => decodeJSONValue(new TextEncoder().encode(text))).toThrow(SyntaxError)
    }
  })

  it('bounds every binary JSON value without counting typed list wrappers', () => {
    for (const depth of [127, 128, 129]) for (const leaf of ['0', '[]', '{}', '1e400']) {
      const text = '['.repeat(depth) + leaf + ']'.repeat(depth)
      const value = decodeLuxoJSON(text)
      const valid = depth + (leaf === '[]' || leaf === '{}' ? 1 : 0) <= 128
      const bytes = new TextEncoder().encode(text)
      const enc = new Encoder()
      enc.writeString('prefix')
      const prefix = enc.bytes().slice()
      if (valid) {
        expect(encodeLuxoJSON(decodeJSONValue(bytes))).toBe(text)
        enc.writeJSON(value)
        const dec = new Decoder(enc.bytes())
        expect(new TextDecoder().decode(dec.readBytes())).toBe('prefix')
        expect(new TextDecoder().decode(dec.readBytes())).toBe(text)
        expect(new TextDecoder().decode(encodeJSONParam(value))).toBe(text)
        const list = new Encoder()
        encodeParam(list, { fieldID: 1, type: 'JSON', isList: true }, [value, value])
        const listReader = new Decoder(list.bytes())
        expect(listReader.nextField()).toBe(true)
        const items = listReader.readBytesArray()
        expect(items).toHaveLength(2)
        for (const item of items) expect(encodeLuxoJSON(decodeJSONValue(item))).toBe(text)
      } else {
        expect(() => decodeJSONValue(bytes), `read ${depth}/${leaf}`).toThrow('128')
        expect(() => enc.writeJSON(value)).toThrow('128')
        expect(enc.bytes()).toEqual(prefix)
        expect(() => encodeJSONParam(value)).toThrow('128')
      }
    }
  })

  it('rejects deep values hidden by duplicate names before native parsing discards them', () => {
    for (const leaf of ['[]', '{}', '0']) {
      const text = '{"value":' + '['.repeat(128) + leaf + ']'.repeat(128) + ',"value":0}'
      expect(() => decodeBoundedLuxoJSON(text)).toThrow('128')
      expect(() => decodeJSONValue(new TextEncoder().encode(text))).toThrow('128')
    }
  })

  it('checks serialized generic JSON including toJSON output, with atomic failure', () => {
    const deep = decodeLuxoJSON('['.repeat(129) + '0' + ']'.repeat(129))
    const enc = new Encoder()
    enc.writeString('prefix')
    const prefix = enc.bytes().slice()
    expect(() => enc.writeJSON({ toJSON: () => deep })).toThrow('128')
    expect(enc.bytes()).toEqual(prefix)
    expect(() => encodeJSONParam({ toJSON: () => deep })).toThrow('128')
    const scalar = { deep, toJSON: () => 42 }
    enc.writeJSON(scalar)
    expect(new TextDecoder().decode(encodeJSONParam(scalar))).toBe('42')
  })

  it('ignores quoted delimiters and handles escaped quotes at depth boundaries', () => {
    const leaves = ['[]{}'.repeat(100), '\\"[]{}'.repeat(100), '\\\\"'.repeat(100), '中文'.repeat(200)]
    for (const leaf of leaves) for (const depth of [0, 127, 128, 129]) {
      const text = '['.repeat(depth) + JSON.stringify(leaf) + ']'.repeat(depth)
      if (depth <= 128) expect(encodeLuxoJSON(decodeBoundedLuxoJSON(text))).toBe(text)
      else expect(() => decodeBoundedLuxoJSON(text)).toThrow('128')
    }
    for (const text of ['"' + 'x'.repeat(300), '"' + 'x'.repeat(300) + '\\"', ']'.repeat(300), '['.repeat(128) + '0'.repeat(300)]) {
      expect(() => decodeBoundedLuxoJSON(text)).toThrow()
      expect(() => decodeBoundedLuxoJSON('[' + text)).toThrow()
    }
  })

  it('matches known mixed-container depths across deterministic generated documents', () => {
    let state = 20261110
    const random = (): number => { state = Math.imul(state, 1664525) + 1013904223 | 0; return state >>> 0 }
    for (let sample = 0; sample < 1000; sample++) {
      const depth = random() % 132
      const leaf = [null, [], {}, '[]"\\{}'.repeat(80), 42][random() % 5]
      let text = JSON.stringify(leaf)
      for (let layer = 0; layer < depth; layer++) {
        text = random() % 2 ? `[${text}]` : `{"\\\"[]":${text}}`
      }
      const total = depth + (leaf !== null && typeof leaf === 'object' ? 1 : 0)
      if (total <= 128) expect(encodeLuxoJSON(decodeBoundedLuxoJSON(text))).toBe(encodeLuxoJSON(decodeLuxoJSON(text)))
      else expect(() => decodeBoundedLuxoJSON(text)).toThrow('128')
    }
  })

  it('rejects excessive source depth before invoking the native reviver', () => {
    const parse = vi.spyOn(JSON, 'parse')
    try {
      expect(() => decodeBoundedLuxoJSON('['.repeat(3000) + '0' + ']'.repeat(3000))).toThrow('128')
      expect(parse).not.toHaveBeenCalled()
    } finally { parse.mockRestore() }
  })

  it('does not confuse wide containers with deep siblings after closed prefixes', () => {
    const prefix = '{},'.repeat(128)
    const wide = '[' + prefix + '{}]'
    expect(encodeLuxoJSON(decodeBoundedLuxoJSON(wide))).toBe(wide)
    for (const depth of [127, 128, 129]) {
      const text = '[' + prefix + '['.repeat(depth) + '0' + ']'.repeat(depth) + ']'
      if (depth === 127) expect(encodeLuxoJSON(decodeBoundedLuxoJSON(text))).toBe(text)
      else expect(() => decodeBoundedLuxoJSON(text)).toThrow('128')
    }
  })

  it('counts generic JSON containers at entry, including empty leaves and error data', () => {
    for (const shape of ['array', 'object'] as const) {
      for (const depth of [127, 128, 129]) {
        for (const leaf of ['0', '[]', '{}', '1e400']) {
          const open = shape === 'array' ? '[' : '{"child":'
          const close = shape === 'array' ? ']' : '}'
          const text = open.repeat(depth) + leaf + close.repeat(depth)
          const value = decodeLuxoJSON(text)
          const valid = depth + (leaf === '[]' || leaf === '{}' ? 1 : 0) <= 128
          const binary = new Encoder()
          binary.writeFieldInt(1, 400n)
          binary.writeFieldString(2, 'Custom')
          binary.writeFieldString(3, 'failure')
          binary.writeFieldString(5, text)
          binary.writeEnd()
          const jsonError = jsonResponseError(decodeJSONEnvelope(`{"error":"Custom","code":400,"message":"failure","data":${text}}`))
          const binaryError = decodeBinaryError(binary.bytes(), 400)
          if (valid) {
            expect(normalizeLuxoJSON(value)).toBe(value)
            expect(encodeLuxoJSON(decodeBoundedLuxoJSON(text))).toBe(text)
            expect(jsonError.error).toBe('Custom')
            expect(binaryError.error).toBe('Custom')
            expect(encodeLuxoJSON(jsonError.data)).toBe(text)
            expect(encodeLuxoJSON(binaryError.data)).toBe(text)
          } else {
            expect(() => normalizeLuxoJSON(value), `${shape}/${depth}/${leaf}`).toThrow('128')
            expect(() => decodeBoundedLuxoJSON(text)).toThrow('128')
            expect(jsonError.error).toBe('ParseError')
            expect(binaryError.error).toBe('ParseError')
          }
        }
      }
    }
  })

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
    for (const token of ['', ' 1', '1 ', '01', '+1', '1.', '1e', 'NaN', 'Infinity', 'null', 'true', '"1"', '[]', '{}', '0,1', '0,"extra":true', '0,"value":1']) {
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

  it('validates only own enumerable JSON properties', () => {
    const prototype = { inherited: '\ud800' }
    Object.defineProperty(prototype, 'unreadable', {
      enumerable: true,
      get: () => { throw new Error('inherited property must not be read') },
    })
    const value = Object.create(prototype) as Record<string, unknown>
    Object.defineProperty(value, 'hasOwnProperty', { value: false, enumerable: true })
    value.name = 'valid'
    Object.defineProperty(value, 'hidden', { value: '\ud800', enumerable: false })
    expect(normalizeLuxoJSON(value)).toBe(value)
    value.name = '\ud800'
    expect(() => normalizeLuxoJSON(value)).toThrow()
  })

  it('rejects invalid JSON and non-finite numbers instead of silently emitting null', () => {
    for (const text of ['', '01', '[1,]', '{"x":}', '+1']) expect(() => decodeLuxoJSON(text)).toThrow()
    for (const value of [NaN, Infinity, -Infinity, undefined]) expect(() => encodeLuxoJSON(value)).toThrow()
    const cycle: unknown[] = []
    cycle.push(cycle)
    expect(() => encodeLuxoJSON(cycle)).toThrow()
  })

  it('rejects direct non-finite JSON values without narrowing exact raw numbers', () => {
    for (const number of [NaN, Infinity, -Infinity]) {
      for (const value of [number, [number], { value: number }]) {
        expect(() => normalizeLuxoJSON(value)).toThrow()
      }
    }
    for (const value of [0, -0, Number.MIN_VALUE, Number.MAX_VALUE]) {
      expect(normalizeLuxoJSON(value)).toBe(value)
    }
    const exact = jsonNumber('1e400')
    expect(normalizeLuxoJSON(exact)).toBe(exact)
  })
})
