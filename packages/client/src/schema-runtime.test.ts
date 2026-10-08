import { afterEach, describe, expect, it, vi } from 'vitest'
import { FetchTransport, WsTransport } from './transport'
import type { APISchema } from './transport'
import { WxTransport } from './wx'
import { snapshotAPISchema } from './schema-runtime'

afterEach(() => vi.unstubAllGlobals())

function fixture(): Record<string, APISchema> {
  return { read: { id: 1, params: [{ fieldID: 1, name: 'value', type: 'Int' }],
    fields: { id: { fieldID: 1, type: 'Int' } },
    types: { Input: { id: { fieldID: 1, type: 'Int' } } },
  } }
}

describe('runtime Schema registration', () => {
  for (const TransportClass of [FetchTransport, WsTransport, WxTransport]) {
    it(`${TransportClass.name} rejects illegal IDs in every directory`, () => {
      const transport = new TransportClass('http://localhost/luxo')
      for (const target of ['api', 'param', 'field', 'type'] as const) {
        for (const id of [0, -1, 1.5, NaN, Infinity, target === 'api' ? 2147483648 : target === 'param' ? 2147483646 : 65536]) {
          const schema = fixture()
          if (target === 'api') schema.read.id = id
          if (target === 'param') schema.read.params![0].fieldID = id
          if (target === 'field') schema.read.fields!.id.fieldID = id
          if (target === 'type') schema.read.types!.Input.id.fieldID = id
          expect(() => transport.setSchema(schema)).toThrow(/ID/)
        }
      }
    })

    it(`${TransportClass.name} rejects duplicate route, parameter and field IDs`, () => {
      const transport = new TransportClass('http://localhost/luxo')
      const routes = fixture()
      routes.other = { id: 1 }
      expect(() => transport.setSchema(routes)).toThrow(/duplicate.*ID/)
      const params = fixture()
      params.read.params!.push({ fieldID: 1, name: 'other', type: 'Int' })
      expect(() => transport.setSchema(params)).toThrow(/duplicate.*ID/)
      const fields = fixture()
      fields.read.fields!.other = { fieldID: 1 }
      expect(() => transport.setSchema(fields)).toThrow(/duplicate.*ID/)
      const types = fixture()
      types.read.types!.Input.other = { fieldID: 1 }
      expect(() => transport.setSchema(types)).toThrow(/duplicate.*ID/)
    })

    it(`${TransportClass.name} keeps legal sparse maxima and independent namespaces`, () => {
      const transport = new TransportClass('http://localhost/luxo')
      const schema = fixture()
      schema.read.id = 2147483647
      schema.read.params![0].fieldID = 2147483645
      schema.read.fields!.id.fieldID = 65535
      schema.read.types!.Input.id.fieldID = 65535
      schema.other = { id: 1, params: schema.read.params, fields: schema.read.fields, types: schema.read.types }
      expect(() => transport.setSchema(schema)).not.toThrow()
      expect(() => transport.setSchema({})).not.toThrow()
    })
  }

  it('validates WeChat constructor metadata as well as setSchema', () => {
    expect(() => new WxTransport('http://localhost/luxo', { schema: { read: { id: 0 } } })).toThrow(/ID/)
  })

  it('owns metadata and preserves old registration after a rejected update', async () => {
    const request = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(new Uint8Array([0])))
    vi.stubGlobal('fetch', request)
    const transport = new FetchTransport('http://localhost/luxo', { mode: 'binary' })
    const schema = fixture()
    transport.setSchema(schema)
    schema.read.id = 9
    schema.read.params![0].fieldID = 7
    schema.read.params![0].type = 'String'
    schema.read.fields!.id.fieldID = 2
    expect(() => transport.setSchema({ read: { id: 0 } })).toThrow(/ID/)
    await transport.call('read', { value: 7n, $select: 'id' })
    const body = request.mock.calls[0]?.[1]?.body
    expect(body).toBeInstanceOf(ArrayBuffer)
    if (!(body instanceof ArrayBuffer)) throw new Error('expected request bytes')
    expect(Array.from(new Uint8Array(body))).toEqual([1, 2, 1, 1, 1, 14, 0])
  })

  it('does not let caller mutations change the Binary parameter plan', async () => {
    const request = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(new Uint8Array([0])))
    vi.stubGlobal('fetch', request)
    const transport = new FetchTransport('http://localhost/luxo', { mode: 'binary' })
    const schema = fixture()
    transport.setSchema(schema)
    schema.read.params![0].type = 'String'
    await expect(transport.call('read', { value: 7n, $select: 'id' })).resolves.toBeDefined()
  })

  it('copies nested metadata while preserving sharing within one registration', () => {
    const schema = fixture()
    schema.other = { id: 2, fields: schema.read.fields, types: schema.read.types }
    const snapshot = snapshotAPISchema(schema)
    expect(snapshot.read.fields).toBe(snapshot.other.fields)
    expect(snapshot.read.types).toBe(snapshot.other.types)
    expect(snapshot.read.fields).not.toBe(schema.read.fields)
    expect(snapshot.read.types).not.toBe(schema.read.types)
    schema.read.types!.Input.id.type = 'String'
    schema.read.types!.Input.id.fieldID = 2
    schema.read.params!.push({ name: 'other', type: 'Int', fieldID: 2 })
    expect(snapshot.read.types!.Input.id).toMatchObject({ fieldID: 1, type: 'Int' })
    expect(snapshot.read.params).toHaveLength(1)
    const updated = snapshotAPISchema(schema)
    expect(updated.read.types!.Input.id).toMatchObject({ fieldID: 2, type: 'String' })
    expect(updated.read.types).not.toBe(snapshot.read.types)
  })

  it('keeps the old JSON plan when a replacement is rejected', async () => {
    const request = vi.fn(async () => new Response('{"data":true}'))
    vi.stubGlobal('fetch', request)
    const transport = new FetchTransport('http://localhost/luxo')
    transport.setSchema(fixture())
    const changed = fixture()
    changed.read.params![0].type = 'String'
    changed.read.fields!.id.fieldID = 0
    expect(() => transport.setSchema(changed)).toThrow(/ID/)
    await expect(transport.call('read', { value: 7n })).resolves.toBe(true)
    await expect(transport.call('read', { value: '7' })).rejects.toThrow(/bigint/)
    expect(request).toHaveBeenCalledTimes(1)
  })

  it('keeps metadata flags, recursive type names and prototype-shaped names', () => {
    const fields = Object.fromEntries([['__proto__', {
      fieldID: 1, type: 'Model', typeName: '__proto__', isList: true, nullable: true,
    }]])
    const schema = Object.fromEntries([['__proto__', { id: 1, fields,
      params: [{ fieldID: 1, name: 'input', type: 'Model', typeName: '__proto__', isList: true, nullable: true }],
      types: Object.fromEntries([['__proto__', fields]]),
    }]])
    const snapshot = snapshotAPISchema(schema)
    expect(snapshot).toEqual(schema)
    expect(Object.hasOwn(snapshot, '__proto__')).toBe(true)
    expect(Object.hasOwn(snapshot.__proto__.fields!, '__proto__')).toBe(true)
    expect(snapshot.__proto__.fields).toBe(snapshot.__proto__.types!.__proto__)
    expect(snapshot.__proto__.params![0]).not.toBe(schema.__proto__.params[0])
  })

  it('validates exactly the IDs retained in the snapshot, reading each once', () => {
    let apiReads = 0, paramReads = 0, fieldReads = 0
    const snapshot = snapshotAPISchema({ read: {
      get id() { return ++apiReads === 1 ? 1 : 0 },
      params: [{ get fieldID() { return ++paramReads === 1 ? 1 : 0 }, name: 'value', type: 'Int' }],
      fields: { id: { get fieldID() { return ++fieldReads === 1 ? 1 : 0 }, type: 'Int' } },
    } })
    expect(snapshot.read.id).toBe(1)
    expect(snapshot.read.params![0].fieldID).toBe(1)
    expect(snapshot.read.fields!.id.fieldID).toBe(1)
    expect([apiReads, paramReads, fieldReads]).toEqual([1, 1, 1])
  })
})
