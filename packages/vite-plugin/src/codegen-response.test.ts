import { describe, expect, it } from 'vitest'
import { stripTypeScriptTypes } from 'node:module'
import { Decoder, decodeJSONValue, int64FromJSON } from '@luxojs/client'
import type { LuxoSchema } from '@luxojs/client'
import { genResponseBody, hasStructuredReturn, isScalar, luxoTypeToTS, schemaEntry } from './codegen-response'

const schema: LuxoSchema = {
  models: { Result: { name: 'Result', fields: [{ id: 1, name: 'id', type: 'Int' }] } },
  enums: { State: { name: 'State', values: ['ok'] } },
  apis: {},
}

describe('response generation', () => {
  it('distinguishes own enum declarations from inherited directory properties', () => {
    for (const name of ['constructor', '__proto__', 'toString']) {
      const api = { id: 1, name: 'read', module: 'test', returnType: name }
      const empty: LuxoSchema = { models: {}, apis: {}, enums: {} }
      expect(schemaEntry(empty.enums, name)).toBeUndefined()
      expect(schemaEntry(undefined, name)).toBeUndefined()
      expect(hasStructuredReturn(api, empty)).toBe(true)
      const declaration = { name, values: ['ok'] }
      const declared = { ...empty, enums: { [name]: declaration } }
      expect(schemaEntry(declared.enums, name)).toBe(declaration)
      expect(hasStructuredReturn(api, declared)).toBe(false)
      expect(genResponseBody(api, declared, name, '  ')).toContain(`decodeEnum$${name}`)
    }
  })

  it.each([false, true])('decodes explicit nullable roots (list=%s) and rejects malformed envelopes', list => {
    const api = { id: 1, name: 'optional', module: 'test', returnType: 'Int', returnList: list, returnNullable: true }
    const body = genResponseBody(api, schema, `${list ? 'bigint[]' : 'bigint'} | null`, '  ')
    const source = stripTypeScriptTypes(`function decode(d: unknown) {\n${body}}`)
    // Execute generated source only in the test; production code remains static.
    const factory = new Function('Decoder', 'int64FromJSON', `${source}\nreturn decode`) as
      (decoder: typeof Decoder, integer: typeof int64FromJSON) => (data: unknown) => unknown
    const decode = factory(Decoder, int64FromJSON)
    expect(decode(null)).toBeNull()
    expect(decode(new Uint8Array([0]))).toBeNull()
    expect(decode(list ? [] : 0)).toEqual(list ? [] : 0n)
    expect(decode(new Uint8Array([1, 0]))).toEqual(list ? [] : 0n)
    expect(decode(new Uint8Array(list ? [1, 1, 84] : [1, 84]))).toEqual(list ? [42n] : 42n)
    for (const invalid of [[], [1], [2], [0, 0], list ? [1, 0, 0] : [1, 84, 0]]) {
      expect(() => decode(new Uint8Array(invalid))).toThrow()
    }
  })

  it('executes scalar/list completion checks for every generated Binary response type', () => {
    const fixtures: { type: string; bytes: number[]; value: unknown }[] = [
      { type: 'Int', bytes: [84], value: 42n },
      { type: 'Duration', bytes: [84], value: 42n },
      { type: 'Float', bytes: [0, 0, 0, 0, 0, 0, 248, 63], value: 1.5 },
      { type: 'Boolean', bytes: [1], value: true },
      { type: 'String', bytes: [2, 111, 107], value: 'ok' },
      { type: 'Enum', bytes: [2, 111, 107], value: 'ok' },
      { type: 'Decimal', bytes: [3, 49, 46, 53], value: '1.5' },
      { type: 'Bytes', bytes: [2, 1, 2], value: new Uint8Array([1, 2]) },
      { type: 'JSON', bytes: [4, 110, 117, 108, 108], value: null },
      { type: 'UUID', bytes: new Array<number>(16).fill(0), value: '00000000-0000-0000-0000-000000000000' },
      { type: 'DateTime', bytes: [0, 0], value: '1970-01-01T00:00:00Z' },
    ]
    for (const fixture of fixtures) {
      for (const list of [false, true]) {
        for (const nullable of [false, true]) {
          const body = genResponseBody({ id: 1, name: 'value', module: 'test', returnType: fixture.type,
            returnList: list, returnNullable: nullable }, schema, 'unknown', '  ')
          const source = stripTypeScriptTypes(`function decode(d: unknown) {\n${body}}`)
          const factory = new Function('Decoder', 'decodeJSONValue', `${source}\nreturn decode`) as
            (decoder: typeof Decoder, json: typeof decodeJSONValue) => (data: unknown) => unknown
          const decode = factory(Decoder, decodeJSONValue)
          const payload = list ? [2, ...fixture.bytes, ...fixture.bytes] : fixture.bytes
          const wire = new Uint8Array(nullable ? [1, ...payload] : payload)
          const label = `${fixture.type} list=${list} nullable=${nullable}`
          expect(decode(wire), label).toEqual(list ? [fixture.value, fixture.value] : fixture.value)
          expect(() => decode(new Uint8Array([...wire, 0])), label).toThrow()
          for (let length = 0; length < wire.length; length++) {
            expect(() => decode(wire.subarray(0, length)), `${label} cut=${length}`).toThrow()
          }
          if (nullable) expect(decode(new Uint8Array([0])), label).toBeNull()
        }
      }
    }
  })

  it.each(['Int', 'Duration', 'Float', 'Boolean', 'String', 'Bytes', 'JSON', 'DateTime', 'UUID', 'Decimal', 'Enum'])
  ('emits exactly one completion check for %s values and lists before delivery', type => {
    for (const list of [false, true]) {
      const body = genResponseBody({ id: 1, name: 'value', module: 'test', returnType: type, returnList: list },
        schema, luxoTypeToTS(type) + (list ? '[]' : ''), '  ')
      expect(body.match(/dec\.error !== null/g)).toHaveLength(1)
      expect(body.match(/dec\.remaining !== 0/g)).toHaveLength(1)
      expect(body.indexOf('dec.error')).toBeLessThan(body.indexOf('return value'))
      expect(body).not.toContain('onData(')
      expect(body).not.toContain('Checked(')
      expect(body).not.toContain('decodeScalarArray(')
      expect(body).not.toContain('new Function')
    }
  })

  it('uses an explicit unknown-member decoder for named enum responses', () => {
    const api = { id: 1, name: 'state', module: 'test', returnType: 'State' }
    expect(hasStructuredReturn(api, schema)).toBe(false)
    for (const list of [false, true]) {
      const type = list ? 'State[]' : 'State'
      const body = genResponseBody({ ...api, returnList: list }, schema, type, '    ')
      expect(body).toContain(list ? 'value.map(decodeEnum$State)' : 'decodeEnum$State(value)')
      expect(body).not.toContain(`as ${type}`)
      expect(body).not.toContain('decodeState')
      expect(body).not.toContain('State<')
    }
  })

  it('keeps model, list and pagination decoders responsible for their existing boundaries', () => {
    const api = { id: 1, name: 'model', module: 'test', returnType: 'Result' }
    expect(hasStructuredReturn(api, schema)).toBe(true)
    expect(genResponseBody(api, schema, 'Result<true>', '  ')).toContain('decodeResult(new Decoder(d))')
    expect(genResponseBody({ ...api, returnList: true }, schema, 'Result<true>[]', '  ')).toContain('decodeColumnarResult(d)')
    expect(genResponseBody({ ...api, returnList: true, paginated: true }, schema, 'Page<Result<true>>', '  '))
      .toContain('decodePaginatedResult(d)')
  })

  it('preserves implicit Int results and does not classify prototype names as scalar types', () => {
    const api = { id: 1, name: 'count', module: 'test' }
    expect(hasStructuredReturn(api, schema)).toBe(false)
    expect(genResponseBody(api, schema, 'bigint', '  ')).toContain('const value = dec.readInt()')
    expect(luxoTypeToTS('Result')).toBe('Result')
    expect(isScalar('constructor')).toBe(false)
    expect(isScalar('__proto__')).toBe(false)
  })
})
