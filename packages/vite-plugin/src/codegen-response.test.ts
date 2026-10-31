import { describe, expect, it } from 'vitest'
import { stripTypeScriptTypes } from 'node:module'
import { Decoder, int64FromJSON } from '@luxojs/client'
import type { LuxoSchema } from '@luxojs/client'
import { genResponseBody, hasStructuredReturn, isScalar, luxoTypeToTS } from './codegen-response'

const schema: LuxoSchema = {
  models: { Result: { name: 'Result', fields: [{ id: 1, name: 'id', type: 'Int' }] } },
  enums: { State: { name: 'State', values: ['ok'] } },
  apis: {},
}

describe('response generation', () => {
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
