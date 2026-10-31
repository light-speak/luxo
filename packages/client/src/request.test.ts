import { describe, expect, it, vi } from 'vitest'
import { Encoder } from './codec'
import { GeneratedRequest, requestJSON } from './request'
import type { RequestEncoder, RequestValues } from './request'
import { FetchTransport, encodeBinaryBody, encodeStaticRequestBody } from './transport'
import type { APISchema } from './transport'
import type { Filter, Sorter } from './types'

class GetUserRequest extends GeneratedRequest {
  readonly apiID = 7
  constructor(private readonly id: bigint) { super() }
  writeParameters(encoder: Encoder): void {
    encoder.writeVarint(3)
    encoder.writeSvarint(this.id)
  }
  toJSONParameters(): Record<string, unknown> { return { id: this.id } }
}

describe('static encoder with original parameters', () => {
  type Values = RequestValues & { id: bigint }
  const schema: APISchema = { id: 7, params: [{ name: 'id', type: 'Int', fieldID: 3 }] }
  const codec: RequestEncoder<Values> = {
    apiID: 7,
    initialCapacity: () => 16,
    writeParameters(encoder, values) {
      if (values === undefined) throw new TypeError('missing parameters')
      encoder.writeVarint(3)
      encoder.writeSvarint(values.id)
    },
  }
  it('passes the original object without schema traversal or JSON conversion', () => {
    const values: Values = Object.freeze({ id: 9007199254740993n })
    const guarded: APISchema = { id: 7, get params(): APISchema['params'] { throw Error('schema scanned') } }
    const checking: RequestEncoder<Values> = { ...codec, writeParameters(encoder, actual) {
      expect(actual).toBe(values)
      codec.writeParameters(encoder, actual)
    } }
    expect(encodeStaticRequestBody(guarded, checking, values)).toEqual(encodeBinaryBody(schema, values))
  })
  it('preserves exact integers, controls and independent output ownership', () => {
    for (const id of [0n, -(1n << 63n), (1n << 63n) - 1n]) {
      const values: Values = { id, $filters: [{ field: 'id', op: 'eq', value: id }], $sorters: [{ field: 'id', order: 'desc' }] }
      const bytes = encodeStaticRequestBody(schema, codec, values)
      expect(bytes).toEqual(encodeBinaryBody(schema, values))
      const copy = bytes.slice()
      values.id = 1n
      encodeStaticRequestBody(schema, codec, values)
      expect(bytes).toEqual(copy)
    }
  })
  it('rejects mismatched IDs and invalid values without fallback', () => {
    expect(() => encodeStaticRequestBody({ id: 8 }, codec, { id: 1n })).toThrow(/ID/)
    expect(() => encodeStaticRequestBody(schema, codec, { id: 1n << 63n })).toThrow()
    expect(() => encodeStaticRequestBody(schema, codec, { id: 1n, $filters: [{ field: 'x', op: 'eq', value: NaN }] })).toThrow()
  })
  it('sends original parameters through the dedicated HTTP entry in either mode', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch')
    try {
      for (const mode of ['binary', 'json'] as const) {
        const events: string[] = []
        const transport = new FetchTransport('http://test.invalid', { mode, observer: event => events.push(event.status) })
        transport.setSchema({ getUser: schema })
        const values = Object.freeze({ id: 9007199254740993n })
        fetchMock.mockResolvedValueOnce(new Response(mode === 'binary' ? new Uint8Array([84]) : '{"data":42}'))
        await transport.callGenerated('getUser', codec, values)
        const body = fetchMock.mock.lastCall?.[1]?.body
        if (mode === 'binary') expect(new Uint8Array(body as ArrayBuffer)).toEqual(encodeBinaryBody(schema, values))
        else expect(body).toBe('{"id":9007199254740993,"$api":"getUser"}')
        expect(events).toEqual(['success'])
      }
    } finally { fetchMock.mockRestore() }
  })
  it('rejects cancelled or mismatched HTTP requests before sending', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch')
    try {
      const transport = new FetchTransport('http://test.invalid', { mode: 'binary' })
      transport.setSchema({ getUser: schema })
      await expect(transport.callGenerated('getUser', codec, { id: 1n }, { signal: AbortSignal.abort() })).rejects.toMatchObject({ error: 'CancelledError' })
      await expect(transport.callGenerated('getUser', { ...codec, apiID: 8 }, { id: 1n })).rejects.toMatchObject({ error: 'ConfigError' })
      await expect(transport.callGenerated('missing', codec, { id: 1n })).rejects.toMatchObject({ error: 'ConfigError' })
      expect(fetchMock).not.toHaveBeenCalled()
    } finally { fetchMock.mockRestore() }
  })
  it('retains binary request bytes and mode during a refresh retry', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch')
    try {
      const values = { id: 42n }
      let writes = 0
      const counting: RequestEncoder<typeof values> = { ...codec, writeParameters(enc, params) {
        writes++
        codec.writeParameters(enc, params)
      } }
      const transport = new FetchTransport('http://test.invalid', { mode: 'binary', onTokenExpired: async () => {
        values.id = 99n
        transport.setMode('json')
        return 'new-token'
      } })
      transport.setSchema({ getUser: schema })
      const error = new Encoder()
      error.writeFieldInt(1, 401n)
      error.writeFieldString(2, 'Unauthorized')
      error.writeFieldString(3, 'expired')
      error.writeEnd()
      fetchMock.mockResolvedValueOnce(new Response(error.bytes(), { status: 401 }))
        .mockResolvedValueOnce(new Response(new Uint8Array([84])))
      expect(await transport.callGenerated('getUser', counting, values)).toEqual(new Uint8Array([84]))
      expect(writes).toBe(1)
      expect(fetchMock.mock.calls[1][1]?.body).toBe(fetchMock.mock.calls[0][1]?.body)
    } finally { fetchMock.mockRestore() }
  })
  it('preserves structured field selection and rejects omission', () => {
    const selected: APISchema = { ...schema, fields: { id: { fieldID: 1, type: 'Int' } } }
    const values: Values = { id: 42n, $select: 'id' }
    expect(encodeStaticRequestBody(selected, codec, values)).toEqual(encodeBinaryBody(selected, values))
    expect(() => encodeStaticRequestBody(selected, codec, { id: 42n })).toThrow(/\$select/)
  })
  it('keeps distinct call-site selections isolated through the same static encoder', () => {
    const fields = {
      id: { fieldID: 1, type: 'Int' },
      name: { fieldID: 2, type: 'String' },
      child: { fieldID: 3, type: 'Model', typeName: 'Result' },
    }
    const selected: APISchema = { ...schema, fields, types: { Result: fields } }
    const selections = ['id', 'name', 'id,name', 'id,child { name }']
    const outputs = selections.map($select => {
      const values: Values = { id: 42n, $select }
      const bytes = encodeStaticRequestBody(selected, codec, values)
      expect(bytes).toEqual(encodeBinaryBody(selected, values))
      return bytes
    })
    for (let index = 1; index < outputs.length; index++) {
      expect(outputs[index]).not.toEqual(outputs[index - 1])
    }
    expect(encodeStaticRequestBody(selected, codec, { id: 42n, $select: 'id' })).toEqual(outputs[0])
    for (const $select of ['', 'unknown', 'child { unknown }']) {
      expect(() => encodeStaticRequestBody(selected, codec, { id: 42n, $select })).toThrow()
    }
  })
})

describe('generated request boundary', () => {
  const schema: APISchema = { id: 7, params: [{ name: 'id', type: 'Int', fieldID: 3 }] }
  it('writes the same wire without reading parameter schema or materializing JSON', () => {
    const request = new GetUserRequest(9007199254740993n)
    const expected = encodeBinaryBody(schema, { id: 9007199254740993n })
    const noParameters: APISchema = { id: 7, get params(): APISchema['params'] { throw new Error('parameter schema scanned') } }
    request.toJSONParameters = () => { throw new Error('JSON materialized in Binary') }
    expect(encodeBinaryBody(noParameters, request)).toEqual(expected)
  })
  it('does not fall back on schema mismatch or encoding failure', () => {
    expect(() => encodeBinaryBody({ id: 8 }, new GetUserRequest(42n))).toThrow(/ID/)
    expect(() => encodeBinaryBody(schema, new GetUserRequest(1n << 63n))).toThrow()
  })
  it('only materializes JSON when explicitly requested', () => {
    expect(requestJSON(new GetUserRequest(42n))).toEqual({ id: 42n })
    const dynamic = { id: 42n }
    expect(requestJSON(dynamic)).toBe(dynamic)
    expect(requestJSON(undefined)).toBeUndefined()
  })
  it('preserves control wire and validation on the typed request path', () => {
    class Controls extends GetUserRequest {
      override get filters(): Filter[] { return filters }
      override get sorters(): Sorter[] { return sorters }
    }
    const filters: Filter[] = [
      { field: 'id', op: 'eq', value: 9007199254740993n },
      { field: 'name', op: 'contains', value: '中文' },
    ]
    const sorters: Sorter[] = [{ field: 'id', order: 'desc' }]
    const request = new Controls(42n)
    expect(encodeBinaryBody(schema, request)).toEqual(encodeBinaryBody(schema,
      { id: 42n, $filters: filters, $sorters: sorters }))
    filters[0] = { field: 'score', op: 'eq', value: NaN }
    expect(() => encodeBinaryBody(schema, request)).toThrow()
    filters.length = 1001
    expect(() => encodeBinaryBody(schema, request)).toThrow()
    filters.length = 0
    sorters.length = 101
    expect(() => encodeBinaryBody(schema, request)).toThrow()
  })
})
