import { describe, expect, it } from 'vitest'
import { stripTypeScriptTypes } from 'node:module'
import * as runtime from '@luxojs/client'
import type { LuxoSchema, GeneratedRequest } from '@luxojs/client'
import { encodeBinaryBody } from '@luxojs/client'
import { genRequests } from './codegen-request'

function compile(schema: LuxoSchema): new (params: Record<string, unknown>) => GeneratedRequest {
  const source = genRequests(schema, name => name)
    .replace(/^import .*\n/gm, '').replace(/export class /g, 'class ')
  const js = stripTypeScriptTypes(source)
  const names = ['GeneratedRequest', 'Encoder', 'enumRawValue', 'stringFromJSON', 'booleanFromJSON', 'encodeJSONParam', 'utf8Length'] as const
  // Only the test executes generated source dynamically.
  return new Function(...names, `${js}\nreturn $saveRequest`)(...names.map(name => runtime[name])) as
    new (params: Record<string, unknown>) => GeneratedRequest
}

function compileStatic(schema: LuxoSchema): runtime.RequestEncoder<Record<string, unknown>> {
  const source = genRequests(schema, name => name, true)
    .replace(/^import .*\n/gm, '').replace(/export const /g, 'const ')
  const names = ['Encoder', 'enumRawValue', 'stringFromJSON', 'booleanFromJSON', 'encodeJSONParam', 'utf8Length'] as const
  return new Function(...names, `${stripTypeScriptTypes(source)}\nreturn $saveRequest`)(...names.map(name => runtime[name])) as runtime.RequestEncoder<Record<string, unknown>>
}

describe('generated parameter writers', () => {
  it('writes nested static parameters without per-element capturing callbacks', () => {
    const schema: LuxoSchema = { models: {}, types: { Input: { name: 'Input', fields: [
      { id: 1, name: 'text', type: 'String' },
      { id: 2, name: 'child', type: 'Model', typeName: 'Input', nullable: true },
    ] } }, apis: { save: { id: 7, name: 'save', module: 'test', params: [
      { id: 3, name: 'inputs', type: 'Model', typeName: 'Input', isList: true },
      { id: 4, name: 'input', type: 'Model', typeName: 'Input', nullable: true },
    ] } } }
    const source = genRequests(schema, name => name, true)
    expect(source).toContain('enc.writeDelimitedValue(item, write$Input)')
    expect(source).toContain('enc.writeDelimitedValue(value, write$Input)')
    expect(source).not.toContain('enc =>')
    const Previous = compile(schema)
    const codec = compileStatic(schema)
    const child = { text: '中文'.repeat(64), child: null }
    for (const values of [{ inputs: [], input: null }, { inputs: [child, { text: 'parent', child }], input: child }]) {
      const previous = new runtime.Encoder(1)
      const current = new runtime.Encoder(1)
      new Previous(values).writeParameters(previous)
      codec.writeParameters(current, values)
      expect(current.bytes()).toEqual(previous.bytes())
    }
  })
  it('keeps static scalar/list/null/omission encoding identical to generated wrappers', () => {
    const types = ['Int', 'Duration', 'Float', 'Boolean', 'String', 'Decimal', 'UUID', 'DateTime', 'Bytes', 'JSON']
    const values = [9007199254740993n, -7n, -0, true, '中文😀', '1.234',
      '00112233-4455-6677-8899-aabbccddeeff', '2026-01-01T00:00:00.123456789Z', new Uint8Array([0, 255]), { a: 1 }]
    for (const [index, type] of types.entries()) for (const isList of [false, true]) {
      const schema: LuxoSchema = { models: {}, apis: { save: { id: 7, name: 'save', module: 'test',
        params: [{ id: 3, name: 'value', type, isList, nullable: true }],
      } } }
      const Previous = compile(schema)
      const codec = compileStatic(schema)
      for (const value of [undefined, null, isList ? [values[index], values[index]] : values[index]]) {
        const input = Object.freeze(value === undefined ? {} : { value })
        const oldRequest = new Previous(input)
        expect(codec.initialCapacity(input)).toBe(oldRequest.initialCapacity)
        const previous = new runtime.Encoder()
        const current = new runtime.Encoder()
        oldRequest.writeParameters(previous)
        codec.writeParameters(current, input)
        expect(current.bytes()).toEqual(previous.bytes())
      }
    }
  })
  it('emits a reusable static encoder without capturing request values', () => {
    const schema: LuxoSchema = { models: {}, apis: { save: {
      id: 7, name: 'save', module: 'test', returnType: 'Int', params: [{ id: 3, name: 'value', type: 'Int' }],
    } } }
    const source = genRequests(schema, name => name, true)
    expect(source).not.toContain('constructor(')
    expect(source).not.toContain('this.values')
    const js = stripTypeScriptTypes(source.replace(/^import .*\n/gm, '').replace(/export const /g, 'const '))
    const codec = new Function(`${js}\nreturn $saveRequest`)() as {
      apiID: number
      initialCapacity(values: { value: bigint }): number
      writeParameters(enc: runtime.Encoder, values: { value: bigint }): void
    }
    expect(codec.apiID).toBe(7)
    for (const value of [0n, -9223372036854775808n, 9223372036854775807n]) {
      const input = Object.freeze({ value })
      const enc = new runtime.Encoder(codec.initialCapacity(input))
      codec.writeParameters(enc, input)
      const expected = new runtime.Encoder()
      expected.writeVarint(3)
      expected.writeSvarint(value)
      expect(enc.bytes()).toEqual(expected.bytes())
    }
  })
  it('sizes fixed scalar requests from wire bounds without shrinking selectable responses', () => {
    for (const id of [1, 2147483647]) for (const nullable of [false, true]) {
      const param = { id, name: 'value', type: 'Int', nullable }
      const api = { id, name: 'save', module: 'test', returnType: 'Int', params: [param] }
      const Request = compile({ models: {}, apis: { save: api } })
      const meta = { id, params: [{ ...param, fieldID: id }] }
      for (const value of [0n, -9223372036854775808n, 9223372036854775807n, ...(nullable ? [null] : [])]) {
        const request = new Request({ value })
        expect(request.initialCapacity).toBe(id === 1 ? 16 : 32)
        const body = encodeBinaryBody(meta, request)
        expect(body).toEqual(encodeBinaryBody(meta, { value }))
        expect(body.length).toBeLessThanOrEqual(request.initialCapacity)
      }
      const Selectable = compile({ models: { User: { name: 'User', fields: [] } }, apis: {
        save: { ...api, returnType: 'User' },
      } })
      expect(new Selectable({ value: 42n }).initialCapacity).toBe(64)
    }
  })

  it('bounds capacity hints without scanning nested inputs or trusting invalid Bytes', () => {
    const schema = (type: string): LuxoSchema => ({ models: {}, apis: { save: {
      id: 7, name: 'save', module: 'test', params: [{ id: 1, name: 'value', type }],
    } } })
    const Integer = compile(schema('Int'))
    expect(new Integer({ value: 42n }).initialCapacity).toBe(64)
    const Bytes = compile(schema('Bytes'))
    expect(new Bytes({ value: new Uint8Array(4096) }).initialCapacity).toBe(4160)
    expect(new Bytes({ value: new Uint8Array(70000) }).initialCapacity).toBe(65536)
    const invalid = { get byteLength(): number { throw new Error('untrusted size') } }
    expect(new Bytes({ value: invalid }).initialCapacity).toBe(64)
    expect(() => encodeBinaryBody({ id: 7 }, new Bytes({ value: invalid }))).toThrow()
    const StringRequest = compile(schema('String'))
    expect(new StringRequest({ value: 'text' }).initialCapacity).toBe(1024)
  })

  it('precomputes bounded capacity for wide static scalar requests', () => {
    for (const count of [5, 16, 128]) {
      const params = Array.from({ length: count }, (_, index) => ({
        id: index + 1, name: `v${index}`, type: 'Int',
      }))
      const schema: LuxoSchema = { models: {}, apis: { save: {
        id: 7, name: 'save', module: 'test', returnType: 'Int', params,
      } } }
      const codec = compileStatic(schema)
      const maximum = Object.fromEntries(params.map(param => [param.name, 9223372036854775807n]))
      const meta = { id: 7, params: params.map(param => ({ ...param, fieldID: param.id })) }
      const expectedCapacity = count === 5 ? 64 : count === 16 ? 256 : 1024
      expect(codec.initialCapacity(maximum)).toBe(expectedCapacity)
      for (const values of [maximum, {}]) {
        const encoded = runtime.encodeStaticRequestBody(meta, codec, values)
        expect(encoded).toEqual(encodeBinaryBody(meta, values))
        if (count === 128 && values === maximum) expect(encoded.byteLength).toBeGreaterThan(expectedCapacity)
        else expect(encoded.byteLength).toBeLessThanOrEqual(expectedCapacity)
      }
      expect(new (compile(schema))(maximum).initialCapacity).toBe(1024)
    }
  })

  it('preserves scalar, list, null and omitted wire values', () => {
    const types = ['Int', 'Duration', 'Float', 'Boolean', 'String', 'Decimal', 'UUID', 'DateTime', 'Bytes', 'JSON']
    const values = [9007199254740993n, -7n, -0, true, '中文😀', '1.234',
      '00112233-4455-6677-8899-aabbccddeeff', '2026-01-01T00:00:00.123456789Z', new Uint8Array([0, 255]), { a: 1 }]
    for (const [index, type] of types.entries()) for (const isList of [false, true]) {
      const params = [{ id: 3, name: 'value', type, isList, nullable: true }]
      const Request = compile({ models: {}, apis: { save: { id: 7, name: 'save', module: 'test', params } } })
      const meta = { id: 7, params: [{ ...params[0], fieldID: 3 }] }
      for (const value of [undefined, null, isList ? [values[index], values[index]] : values[index]]) {
        const input = value === undefined ? {} : { value }
        expect(encodeBinaryBody(meta, new Request(input))).toEqual(encodeBinaryBody(meta, input))
      }
    }
  })

  it('writes nested models directly with identical arena and delimiters', () => {
    const fields = [{ id: 1, name: 'text', type: 'String' }, { id: 2, name: 'id', type: 'Int' },
      { id: 3, name: 'child', type: 'Model', typeName: 'Input', nullable: true }]
    const params = [{ id: 5, name: 'inputs', type: 'Model', typeName: 'Input', isList: true }]
    const Request = compile({ models: {}, types: { Input: { name: 'Input', fields } },
      apis: { save: { id: 7, name: 'save', module: 'test', params } } })
    const meta = { id: 7, params: [{ ...params[0], fieldID: 5 }], types: { Input:
      Object.fromEntries(fields.map(field => [field.name, { ...field, fieldID: field.id }])) } }
    const input = { inputs: [{ text: '中文', id: -9223372036854775808n,
      child: { text: '😀', id: 9223372036854775807n, child: null } }] }
    expect(encodeBinaryBody(meta, new Request(input))).toEqual(encodeBinaryBody(meta, input))
    const generated = genRequests({ models: {}, types: { Input: { name: 'Input', fields } },
      apis: { save: { id: 7, name: 'save', module: 'test', params } } }, name => name)
    expect(generated).not.toMatch(/Object\.entries|encodeParam|JSON\.stringify/)
  })

  it('retains validation instead of sending malformed typed inputs', () => {
    for (const [type, invalid] of [['Int', 42], ['String', '\ud800'], ['Boolean', 'false'],
      ['Float', Infinity], ['Bytes', [1]], ['JSON', undefined]] as const) {
      const Request = compile({ models: {}, apis: { save: { id: 7, name: 'save', module: 'test',
        params: [{ id: 1, name: 'value', type }] } } })
      if (invalid !== undefined) expect(() => encodeBinaryBody({ id: 7 }, new Request({ value: invalid }))).toThrow()
      expect(() => encodeBinaryBody({ id: 7 }, new Request({ value: null }))).toThrow()
    }
  })
})
