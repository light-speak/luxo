import { describe, expect, it, vi } from 'vitest'
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
  it.each([false, true].flatMap(staticRequest => [false, true].map(list => ({ staticRequest, list }))))
  ('bounds generated model inputs at 64 object layers ($staticRequest, $list)', ({ staticRequest, list }) => {
    const params = [{ id: 1, name: 'value', type: 'Model', typeName: 'Input' }]
    const schema: LuxoSchema = { models: {}, types: { Input: { name: 'Input', fields: [
      { id: 1, name: 'next', type: 'Model', typeName: 'Input', isList: list, nullable: true },
      { id: 2, name: 'json', type: 'JSON' },
    ] } }, apis: { save: { id: 7, name: 'save', module: 'test', returnType: 'Int', params } } }
    const Request = compile(schema), codec = compileStatic(schema)
    const meta = { id: 7, params: [{ ...params[0], fieldID: 1 }] }
    let json: unknown = {}
    for (let i = 1; i < 128; i++) json = { child: json }
    const input = (layers: number): Record<string, unknown> => {
      let value: Record<string, unknown> = { next: list ? [] : null, json }
      for (let i = 1; i < layers; i++) value = { next: list ? [value] : value }
      return value
    }
    const encode = (value: Record<string, unknown>): Uint8Array => staticRequest
      ? runtime.encodeStaticRequestBody(meta, codec, { value }) : encodeBinaryBody(meta, new Request({ value }))
    expect(encode(input(64)).byteLength).toBeGreaterThan(128)
    expect(() => encode(input(65))).toThrow(/model nesting exceeds 64 levels/)
    const cyclic: Record<string, unknown> = {}
    cyclic.next = list ? [cyclic] : cyclic
    expect(() => encode(cyclic)).toThrow(/model nesting exceeds 64 levels/)
    expect(encode(input(1)).byteLength).toBeGreaterThan(128)
  })

  it.each([false, true])('rejects oversized generated lists at top level and nested inputs (static=%s)', staticRequest => {
    for (const nested of [false, true]) {
      const params = [{ id: 1, name: 'value', type: nested ? 'Model' : 'Boolean', typeName: nested ? 'Input' : undefined, isList: !nested }]
      const schema: LuxoSchema = { models: {}, types: { Input: { name: 'Input', fields: [
        { id: 1, name: 'flags', type: 'Boolean', isList: true },
      ] } }, apis: { save: { id: 7, name: 'save', module: 'test', returnType: 'Int', params } } }
      const meta = { id: 7, params: params.map(p => ({ ...p, fieldID: p.id })) }
      const Request = compile(schema), codec = compileStatic(schema)
      const list = Array<boolean>(1_000_001)
      const values = { value: nested ? { flags: list } : list }
      expect(() => staticRequest ? runtime.encodeStaticRequestBody(meta, codec, values)
        : encodeBinaryBody(meta, new Request(values))).toThrow(RangeError)
    }
  })

  it.each([false, true])('rejects arrays supplied as one generated input (static=%s)', (staticRequest) => {
    const params = [{ id: 1, name: 'value', type: 'Model', typeName: 'Input' }]
    const schema: LuxoSchema = { models: {}, types: { Input: { name: 'Input', fields: [
      { id: 1, name: 'enabled', type: 'Boolean', nullable: true },
    ] } }, apis: { save: { id: 7, name: 'save', module: 'test', returnType: 'Int', params } } }
    const meta = { id: 7, params: [{ ...params[0], fieldID: 1 }] }
    const Request = compile(schema), codec = compileStatic(schema)
    for (const value of [[], Object.assign([], { enabled: true })]) {
      expect(() => staticRequest ? runtime.encodeStaticRequestBody(meta, codec, { value })
        : encodeBinaryBody(meta, new Request({ value }))).toThrow()
    }
  })

  it.each([false, true].flatMap(staticRequest => [false, 0, '', 'null'].map(payload => ({ staticRequest, payload }))))
  ('keeps non-null JSON payload distinct from null ($staticRequest, $payload)', ({ staticRequest, payload }) => {
    const params = [{ id: 1, name: 'value', type: 'Model', typeName: 'Input' }]
    const schema: LuxoSchema = { models: {}, types: { Input: { name: 'Input', fields: [
      { id: 1, name: 'payload', type: 'JSON' },
    ] } }, apis: { save: { id: 7, name: 'save', module: 'test', returnType: 'Int', params } } }
    const meta = { id: 7, params: [{ ...params[0], fieldID: 1 }] }
    const Request = compile(schema), codec = compileStatic(schema)
    const values = { value: { payload } }
    const utf8 = vi.spyOn(TextEncoder.prototype, 'encode')
    let body: Uint8Array, utf8Calls: number
    try {
      body = staticRequest ? runtime.encodeStaticRequestBody(meta, codec, values) : encodeBinaryBody(meta, new Request(values))
      utf8Calls = utf8.mock.calls.length
    } finally {
      utf8.mockRestore()
    }
    expect(utf8Calls).toBe(0)
    const json = new TextEncoder().encode(JSON.stringify(payload))
    expect(body).toEqual(new Uint8Array([7, 0, 1, json.length + 4, 0, 1, json.length, ...json, 0, 0]))
  })

  it.each([[false, false], [true, false], [false, true], [true, true]])
  ('writes JSON null directly (static=%s, list=%s)', (staticRequest, list) => {
    const params = [{ id: 1, name: 'value', type: 'Model', typeName: 'Input' }]
    const schema: LuxoSchema = { models: {}, types: { Input: { name: 'Input', fields: [
      { id: 1, name: 'payload', type: 'JSON', isList: list },
    ] } }, apis: { save: { id: 7, name: 'save', module: 'test', returnType: 'Int', params } } }
    const meta = { id: 7, params: [{ ...params[0], fieldID: 1 }] }
    const Request = compile(schema), codec = compileStatic(schema)
    const values = { value: { payload: list ? [null] : null } }
    const request = new Request(values)
    const serialize = vi.spyOn(JSON, 'stringify'), utf8 = vi.spyOn(TextEncoder.prototype, 'encode')
    let body: Uint8Array, jsonCalls: number, utf8Calls: number
    try {
      body = staticRequest ? runtime.encodeStaticRequestBody(meta, codec, values) : encodeBinaryBody(meta, request)
      jsonCalls = serialize.mock.calls.length
      utf8Calls = utf8.mock.calls.length
    } finally {
      serialize.mockRestore()
      utf8.mockRestore()
    }
    expect(body).toEqual(new Uint8Array([7, 0, 1, list ? 9 : 8, 0, 1,
      ...(list ? [1] : []), 4, 110, 117, 108, 108, 0, 0]))
    expect(jsonCalls).toBe(0)
    expect(utf8Calls).toBe(0)
  })

  it.each([[false, false], [true, false], [false, true], [true, true]])
  ('preserves JSON null within structured inputs (static=%s, list=%s)', (staticRequest, list) => {
    const params = [{ id: 1, name: 'value', type: 'Model', typeName: 'Input' }]
    const fields = [{ id: 1, name: 'payload', type: 'JSON', isList: list }]
    const schema: LuxoSchema = { models: {}, types: { Input: { name: 'Input', fields } },
      apis: { save: { id: 7, name: 'save', module: 'test', returnType: 'Int', params } } }
    const meta = { id: 7, params: [{ ...params[0], fieldID: 1 }], types: {
      Input: { payload: { fieldID: 1, type: 'JSON', isList: list } },
    } }
    const values = { value: { payload: list ? [null] : null } }
    const expected = new Uint8Array([7, 0, 1, list ? 9 : 8, 0, 1,
      ...(list ? [1] : []), 4, 110, 117, 108, 108, 0, 0])
    const Request = compile(schema), codec = compileStatic(schema)
    const encode = (input: Record<string, unknown>) => staticRequest
      ? runtime.encodeStaticRequestBody(meta, codec, input) : encodeBinaryBody(meta, new Request(input))
    expect(encode(values)).toEqual(expected)
    expect(() => encode({ value: null })).toThrow()
    if (list) expect(() => encode({ value: { payload: null } })).toThrow()
  })

  it('preserves JSON null in the dynamic structured writer with the same exact wire', () => {
    const meta = { id: 7, params: [{ fieldID: 1, name: 'value', type: 'Model', typeName: 'Input' }],
      types: { Input: { payload: { fieldID: 1, type: 'JSON' } } } }
    const expected = new Uint8Array([7, 0, 1, 8, 0, 1, 4, 110, 117, 108, 108, 0, 0])
    expect(encodeBinaryBody(meta, { value: { payload: null } })).toEqual(expected)
    expect(() => encodeBinaryBody({ id: 7, params: [{ fieldID: 1, name: 'value', type: 'JSON' }] },
      { value: null })).toThrow()
  })

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
