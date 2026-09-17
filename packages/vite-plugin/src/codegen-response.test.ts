import { describe, expect, it } from 'vitest'
import type { LuxoSchema } from '@luxojs/client'
import { genResponseBody, hasStructuredReturn, isScalar, luxoTypeToTS } from './codegen-response'

const schema: LuxoSchema = {
  models: { Result: { name: 'Result', fields: [{ id: 1, name: 'id', type: 'Int' }] } },
  enums: { State: { name: 'State', values: ['ok'] } },
  apis: {},
}

describe('response generation', () => {
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

  it('treats named enum returns as scalars without changing their declared type', () => {
    const api = { id: 1, name: 'state', module: 'test', returnType: 'State' }
    expect(hasStructuredReturn(api, schema)).toBe(false)
    for (const list of [false, true]) {
      const type = list ? 'State[]' : 'State'
      const body = genResponseBody({ ...api, returnList: list }, schema, type, '    ')
      expect(body).toContain(`return value as ${type}`)
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
