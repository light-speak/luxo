import { describe, expect, it } from 'vitest'
import { JSONParamValidator } from './json-params'
import type { APISchema } from './transport'

function validator(meta: APISchema): JSONParamValidator {
  const result = new JSONParamValidator()
  result.setSchema({ echo: meta })
  return result
}

describe('compiled JSON integer paths', () => {
  it('validates raw sealed cases before duplicate folding and keeps JSON depth independent', () => {
    const check = validator({ id: 1, params: [{ name: 'value', fieldID: 1, type: 'Sealed', typeName: 'Outcome' }], types: {
      '@sealed:Outcome': { Pending: { fieldID: 1, type: 'Model', typeName: 'Outcome.Pending' }, Next: { fieldID: 2, type: 'Model', typeName: 'Outcome.Next' } },
      'Outcome.Pending': {}, 'Outcome.Next': {
        child: { fieldID: 1, type: 'Sealed', typeName: 'Outcome', nullable: true }, note: { fieldID: 2, type: 'JSON' },
      },
    } })
    expect(check.validateEncoded('echo', '{"value":{"Pending":{}}}')).toBe('{"value":{"Pending":{}}}')
    for (const value of ['{}', '{"Future":{}}', '{"Pending":null}', '{"Pending":{},"Pending":{}}', '{"Pending":{},"Next":{}}', '{"Pending":{"extra":0}}']) {
      expect(() => check.validateEncoded('echo', '{"value":' + value + '}')).toThrow()
    }
    const nested = (count: number): unknown => {
      let value: unknown = { Pending: {} }
      for (let i = 1; i < count; i++) value = { Next: { child: value } }
      return value
    }
    expect(() => check.validateEncoded('echo', JSON.stringify({ value: nested(32) }))).not.toThrow()
    expect(() => check.validateEncoded('echo', JSON.stringify({ value: nested(33) }))).toThrow(/64/)
    const json = '['.repeat(128) + '0' + ']'.repeat(128)
    expect(() => check.validateEncoded('echo', '{"value":{"Next":{"note":' + json + '}}}')).not.toThrow()
    expect(() => check.validateEncoded('echo', '{"value":{"Next":{"note":[' + json + '],"note":0}}}')).toThrow(/128/)
    expect(() => check.validateEncoded('echo', '{"value":{"Next":{"note":1,"note":2}}}')).not.toThrow()
  })
  it('rejects a model getter that changes shape before Bytes projection', () => {
    const check = validator({ id: 1, params: [{ fieldID: 1, name: 'node', type: 'Model', typeName: 'Node' }],
      types: { Node: { bytes: { fieldID: 1, type: 'Bytes' } } } })
    let reads = 0
    const input = { get node(): unknown { return ++reads === 1 ? {} : 42 } }
    expect(() => check.prepare('echo', input)).toThrow('node must be an object')
    expect(reads).toBe(2)
  })

  it('projects plain Enum strings when legacy metadata omits the enum name', () => {
    const check = validator({ id: 1, params: [{ fieldID: 1, name: 'state', type: 'Enum' }] })
    expect(check.prepare('echo', { state: 'READY' })).toEqual({ state: 'READY' })
  })

  it.each([false, true])('counts only model objects toward the 64-layer limit (list=%s)', list => {
    const check = validator({ id: 1, params: [{ name: 'node', fieldID: 1, type: 'Model', typeName: 'Node' }], types: { Node: {
      next: { fieldID: 1, type: 'Model', typeName: 'Node', isList: list, nullable: true },
      json: { fieldID: 2, type: 'JSON' }, bytes: { fieldID: 3, type: 'Bytes' },
    } } })
    let json: unknown = {}
    for (let i = 1; i < 128; i++) json = { child: json }
    const input = (layers: number): Record<string, unknown> => {
      let node: Record<string, unknown> = { next: list ? [] : null, json }
      for (let i = 1; i < layers; i++) node = { next: list ? [node] : node }
      return { node }
    }
    const valid = input(64)
    expect(() => check.validate('echo', valid)).not.toThrow()
    expect(check.prepare('echo', valid)).toBe(valid)
    for (const operation of ['validate', 'prepare'] as const) {
      expect(() => check[operation]('echo', input(65))).toThrow(/model nesting exceeds 64 levels/)
      const cyclic: Record<string, unknown> = {}
      cyclic.next = list ? [cyclic] : cyclic
      expect(() => check[operation]('echo', { node: cyclic })).toThrow(/model nesting exceeds 64 levels/)
    }
    expect(check.prepare('echo', { node: { bytes: new Uint8Array([255]) } })).toEqual({ node: { bytes: '/w==' } })
  })

  it('projects scalar and model-list Bytes with safe property names', () => {
    const check = validator({ id: 1, params: [
      { name: 'data', fieldID: 1, type: 'Bytes', nullable: true },
      { name: 'nodes', fieldID: 2, type: 'Model', typeName: 'Node', isList: true },
    ], types: { Node: JSON.parse('{"__proto__":{"fieldID":1,"type":"Bytes"}}') as NonNullable<APISchema['types']>[string] } })
    const node = { ['__proto__']: Uint8Array.from([255]) }
    const params = { data: new Uint8Array(), nodes: [node] }
    expect(check.prepare('echo', params)).toEqual({ data: '', nodes: [{ ['__proto__']: '/w==' }] })
    expect(Object.getPrototypeOf((check.prepare('echo', params)?.nodes as object[])[0])).toBe(Object.prototype)
    expect(node.__proto__).toBeInstanceOf(Uint8Array)
    const unchanged = { data: null, nodes: [] }
    expect(check.prepare('echo', unchanged)).toBe(unchanged)
  })

  it('requires native Base64 only when a Bytes value is sent', () => {
    const check = validator({ id: 1, params: [{ name: 'data', fieldID: 1, type: 'Bytes', nullable: true }] })
    const original = Object.getOwnPropertyDescriptor(Uint8Array.prototype, 'toBase64')!
    try {
      Object.defineProperty(Uint8Array.prototype, 'toBase64', { ...original, value: undefined })
      expect(() => check.prepare('echo', { data: new Uint8Array() })).toThrow('native Uint8Array.toBase64')
      expect(check.prepare('echo', { data: null })).toEqual({ data: null })
      const integers = validator({ id: 1, params: [{ name: 'value', fieldID: 1, type: 'Int' }] })
      const params = { value: 1n }
      expect(integers.prepare('echo', params)).toBe(params)
    } finally {
      Object.defineProperty(Uint8Array.prototype, 'toBase64', original)
    }
  })

  it('bounds model projection when a getter changes its value after validation', () => {
    const check = validator({ id: 1, params: [{ name: 'node', fieldID: 1, type: 'Model', typeName: 'Node' }], types: { Node: {
      next: { fieldID: 1, type: 'Model', typeName: 'Node' }, bytes: { fieldID: 2, type: 'Bytes' },
    } } })
    const cyclic: Record<string, unknown> = {}
    cyclic.next = cyclic
    let reads = 0
    const params = { get node(): Record<string, unknown> { return ++reads === 1 ? {} : cyclic } }
    expect(() => check.prepare('echo', params)).toThrow(/model nesting exceeds 64 levels/)
    expect(reads).toBe(2)
  })

  it('projects only declared Bytes paths without mutating caller-owned data', () => {
    const check = validator({ id: 1, params: [{ name: 'input', fieldID: 1, type: 'Model', typeName: 'Node' }],
      types: { Node: {
        value: { fieldID: 1, type: 'Int' }, bytes: { fieldID: 2, type: 'Bytes', isList: true, nullable: true },
        next: { fieldID: 3, type: 'Model', typeName: 'Node', nullable: true }, json: { fieldID: 4, type: 'JSON' },
      } },
    })
    const storage = Uint8Array.from([99, 0, 127, 128, 255, 99])
    const json = { bytes: storage }
    const next = Object.freeze({ value: 2n, bytes: null })
    const input = Object.freeze({ value: 1n, bytes: Object.freeze([storage.subarray(1, 5), new Uint8Array()]), next, json })
    const params = Object.freeze({ input })
    const output = check.prepare('echo', params)
    expect(output).toEqual({ input: { value: 1n, bytes: ['AH+A/w==', ''], next, json } })
    expect(output).not.toBe(params)
    expect(input.bytes[0]).toBeInstanceOf(Uint8Array)
    expect((output?.input as typeof input).next).toBe(next)
    expect((output?.input as typeof input).json).toBe(json)
    for (const unchanged of [{ input: next }, { input: { value: 1n, bytes: [] } }, { input: { value: 1n } }]) {
      expect(check.prepare('echo', unchanged)).toBe(unchanged)
    }
    expect(() => check.prepare('echo', { input: { value: 1 } })).toThrow('bigint')
    for (const bytes of ['AA==', {}, [null], [new Int8Array(1)]]) {
      expect(() => check.prepare('echo', { input: { value: 1n, bytes } })).toThrow()
    }
    expect(check.prepare('unknown', params)).toBe(params)
    expect(check.prepare('echo')).toBeUndefined()
  })

  it('handles all signed boundaries, absent parameters and nullable containers', () => {
    const check = validator({ id: 1, params: [
      { name: 'value', fieldID: 1, type: 'Int', nullable: true },
      { name: 'values', fieldID: 2, type: 'Duration', isList: true, nullable: true },
    ] })
    for (const value of [0n, -1n, 9007199254740993n, -(1n << 63n), (1n << 63n) - 1n]) {
      expect(() => check.validate('echo', { value, values: [value] })).not.toThrow()
    }
    check.validate('echo')
    check.validate('echo', {})
    check.validate('echo', { value: null, values: null })
    for (const values of [1n, {}, [null], [undefined], new Array(1)]) {
      expect(() => check.validate('echo', { values })).toThrow()
    }
  })

  it('shares recursive type plans without including JSON or Float fields', () => {
    const types = { Node: {
      value: { fieldID: 1, type: 'Int' },
      next: { fieldID: 2, type: 'Model', typeName: 'Node', nullable: true },
      json: { fieldID: 3, type: 'JSON' },
      float: { fieldID: 4, type: 'Float' },
    } }
    const meta = { id: 1, types, params: [{ name: 'node', fieldID: 1, type: 'Model', typeName: 'Node' }] }
    const check = new JSONParamValidator()
    check.setSchema({ first: meta, second: { ...meta, id: 2 } })
    for (const api of ['first', 'second']) {
      check.validate(api, { node: { value: 1n, next: { value: 2n, next: null }, json: 1n << 100n, float: 1.5 } })
      expect(() => check.validate(api, { node: { next: { value: 1 } } })).toThrow('bigint')
      for (const node of [null, [], 'node', 1]) expect(() => check.validate(api, { node })).toThrow('object')
    }
  })

  it('never reads schema metadata again on the request path', () => {
    const field = { fieldID: 1, type: 'Int' }
    const types = { Input: { value: field } }
    const params = [{ name: 'input', fieldID: 1, type: 'Model', typeName: 'Input' }]
    const check = validator({ id: 1, types, params })
    Object.defineProperty(field, 'type', { get() { throw new Error('hot schema read') } })
    Object.defineProperty(params[0], 'type', { get() { throw new Error('hot schema read') } })
    check.validate('echo', { input: { value: 1n } })
    expect(() => check.validate('echo', { input: { value: 1 } })).toThrow('bigint')
  })

  it('requires structured type metadata only when the parameter is present', () => {
    for (const typeName of [undefined, '', 'Missing', '__proto__', 'constructor']) {
      const check = validator({ id: 1, params: [{ name: 'input', fieldID: 1, type: 'Model', typeName }] })
      check.validate('echo', {})
      expect(() => check.validate('echo', { input: {} })).toThrow('missing schema')
    }
  })

  it('bounds data recursion while supporting cyclic schema definitions', () => {
    const check = validator({ id: 1,
      params: [{ name: 'node', fieldID: 1, type: 'Model', typeName: 'Node' }],
      types: { Node: { next: { fieldID: 1, type: 'Model', typeName: 'Node' } } },
    })
    let node: Record<string, unknown> = {}
    for (let depth = 1; depth < 64; depth++) node = { next: node }
    check.validate('echo', { node })
    expect(() => check.validate('echo', { node: { next: node } })).toThrow('64')
    const cycle: Record<string, unknown> = {}
    cycle.next = cycle
    expect(() => check.validate('echo', { node: cycle })).toThrow('64')
  })

  it('does not confuse API names with Object prototype members', () => {
    const check = new JSONParamValidator()
    check.setSchema(JSON.parse('{"__proto__":{"id":1,"params":[{"name":"value","fieldID":1,"type":"Int"}]}}'))
    expect(() => check.validate('__proto__', { value: 1 })).toThrow('bigint')
    check.validate('constructor', { value: 1 })
    check.setSchema({ empty: { id: 2, params: [] }, float: { id: 3, params: [{ name: 'value', fieldID: 1, type: 'Float' }] } })
    check.validate('__proto__', { value: 1 })
    check.validate('float', { value: 1.5 })
  })
})
