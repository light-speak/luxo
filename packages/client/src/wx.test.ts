import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { WxTransport } from './wx'
import { WxTransport as PublicWxTransport } from './index'
import { Encoder } from './codec'
import { UnknownEnumValue } from './enum'
import { GeneratedRequest } from './request'
import type { RequestEncoder, RequestValues } from './request'

interface RequestOptions {
  data: string | ArrayBuffer
  dataType?: string
  responseType?: string
  header: Record<string, string>
  success: (response: { statusCode: number; data: unknown }) => void
  fail: (error: { errMsg: string }) => void
}

const request = vi.fn<(options: RequestOptions) => void>()

beforeEach(() => {
  request.mockReset()
  vi.stubGlobal('wx', { request })
})
afterEach(() => vi.unstubAllGlobals())

describe('WxTransport precision', () => {
  it('sends sealed static JSON through the public WeChat request entry', async () => {
    const transport = new WxTransport('https://example.test/api')
    transport.setSchema({ echo: { id: 1, params: [{ fieldID: 1, name: 'value', type: 'Sealed', typeName: 'Outcome' }],
      types: { '@sealed:Outcome': { Pending: { fieldID: 1, type: 'Model', typeName: 'Outcome.Pending' } }, 'Outcome.Pending': {} },
      response: { type: 'Sealed', typeName: 'Outcome' } } })
    const codec: RequestEncoder<RequestValues> = { apiID: 1, initialCapacity: () => 8, writeParameters() { throw Error('unexpected Binary') },
      writeJSONParameters(writer) { writer.writeRaw(',"value":{"Pending":{}}') } }
    request.mockImplementation(options => options.success({ statusCode: 200, data: '{"data":{"Pending":{}}}' }))
    await expect(transport.callGenerated('echo', codec)).resolves.toEqual({ Pending: {} })
    expect(request.mock.lastCall?.[0].data).toBe('{"$api":"echo","value":{"Pending":{}}}')
  })
  it('retains the JSON plan while a static writer replaces Schema', async () => {
    const transport = new WxTransport('https://example.test/api', {
      schema: { echo: { id: 1, params: [{ fieldID: 1, name: 'value', type: 'Sealed', typeName: 'Outcome' }], types: { '@sealed:Outcome': { Success: { fieldID: 1, type: 'Model', typeName: 'Outcome.Success' } }, 'Outcome.Success': {} } } },
    })
    const codec: RequestEncoder<RequestValues> = {
      apiID: 1, initialCapacity: () => 8, writeParameters() {},
      writeJSONParameters(writer) {
        transport.setSchema({ echo: { id: 1, params: [{ fieldID: 1, name: 'value', type: 'JSON' }] } })
        writer.writeRaw(',"value":{"Future":{}}')
      },
    }
    request.mockImplementation(options => options.success({ statusCode: 200, data: '{"data":true}' }))
    await expect(transport.callGenerated('echo', codec)).rejects.toThrow(/unknown/)
    expect(request).not.toHaveBeenCalled()
    await expect(transport.call('echo', { value: { Future: {} } })).resolves.toBe(true)
  })
  it('ignores an inherited error marker in a successful raw JSON response', async () => {
    const original = Object.getOwnPropertyDescriptor(Object.prototype, 'error')
    const transport = new WxTransport('https://example.test/api')
    request.mockImplementation(options => options.success({ statusCode: 200, data: '{"data":42}' }))
    Object.defineProperty(Object.prototype, 'error', { configurable: true, writable: true, value: 'inherited' })
    try { await expect(transport.call('live')).resolves.toBe(42) }
    finally {
      if (original) Object.defineProperty(Object.prototype, 'error', original)
      else delete (Object.prototype as Record<string, unknown>).error
    }
  })

  it('rejects generated subscriptions before invoking the codec or WeChat network', async () => {
    const transport = new WxTransport('https://example.test/api')
    const encode = vi.fn(() => { throw new Error('unexpected encoding') })
    const codec = { apiID: 7, initialCapacity: encode, writeParameters: encode }
    await expect(transport.subscribeGenerated('live', codec, undefined, value => value, () => {}))
      .rejects.toMatchObject({ error: 'ConfigError' })
    expect(encode).not.toHaveBeenCalled()
    expect(request).not.toHaveBeenCalled()
  })

  it('rejects overwritten response JSON before parsing and retains its declaration while waiting', async () => {
    const transport = new WxTransport('https://example.test/api', { schema: { echo: { id: 7, response: { type: 'JSON' } } } })
    let finish: RequestOptions['success'] | undefined
    request.mockImplementation(options => { finish = options.success })
    const pending = transport.call('echo')
    transport.setSchema({})
    const parse = vi.spyOn(JSON, 'parse')
    try {
      const rejected = expect(pending).rejects.toMatchObject({ error: 'ParseError' })
      finish?.({ statusCode: 200, data: '{"data":' + '['.repeat(129) + '0' + ']'.repeat(129) + ',"data":0}' })
      await rejected
      expect(parse).not.toHaveBeenCalled()
    } finally { parse.mockRestore() }
  })

  it.each([false, true])('rejects serialized JSON129 before WeChat dispatch (generated=%s)', async generated => {
    const schema = { echo: { id: 7, params: [{ fieldID: 1, name: 'value', type: 'JSON' }] } }
    const transport = new WxTransport('https://example.test/api', { schema })
    let value: unknown = 0
    for (let i = 0; i < 129; i++) value = [value]
    let calls = 0
    const params = { value: { toJSON() { calls++; transport.setSchema({}); return value } } }
    const codec = { apiID: 7, initialCapacity: () => 8, writeParameters() {} }
    await expect(generated ? transport.callGenerated('echo', codec, params) : transport.call('echo', params))
      .rejects.toThrow(/JSON nesting exceeds 128 levels/)
    expect(calls).toBe(1)
    expect(request).not.toHaveBeenCalled()
    transport.setSchema(schema)
    request.mockImplementation(options => options.success({ statusCode: 200, data: '{"data":42}' }))
    await expect(transport.call('echo', { value: 0 })).resolves.toBe(42)
  })

  for (const phase of ['capacity', 'parameters', 'legacy'] as const) {
    it.each(['remove', 'replace', 'mode'] as const)(`keeps the Binary snapshot when ${phase} changes %s`, async action => {
      const transport = new WxTransport('https://example.test/api', {
        mode: 'binary', schema: { echo: { id: 7, params: [{ fieldID: 1, name: 'value', type: 'Int' }] } },
      })
      const mutate = () => {
        if (action === 'mode') transport.setMode('json')
        else transport.setSchema(action === 'remove' ? {} : {
          echo: { id: 9, params: [{ fieldID: 2, name: 'value', type: 'Int' }] },
        })
      }
      const write = (enc: Encoder) => { enc.writeVarint(1); enc.writeSvarint(7n) }
      const codec = {
        apiID: 7,
        initialCapacity: () => { if (phase === 'capacity') mutate(); return 16 },
        writeParameters(enc: Encoder) { if (phase === 'parameters') mutate(); write(enc) },
      }
      const legacy = new class extends GeneratedRequest {
        readonly apiID = 7
        writeParameters(enc: Encoder) { mutate(); write(enc) }
        toJSONParameters() { return { value: 7n } }
      }()
      request.mockImplementation(options => options.success({ statusCode: 200, data:
        options.responseType === 'arraybuffer' ? new Uint8Array([14]).buffer : '{"data":true}',
      }))
      const result = phase === 'legacy' ? transport.call('echo', legacy) : transport.callGenerated('echo', codec, { value: 7n })
      await expect(result).resolves.toEqual(new Uint8Array([14]))
      expect(request).toHaveBeenCalledOnce()
      expect(request.mock.lastCall?.[0].data).toEqual(new Uint8Array([7, 0, 1, 14, 0]).buffer)
      expect(request.mock.lastCall?.[0].header['X-Luxo-Mode']).toBe('binary')
      if (action === 'remove') {
        await expect(transport.call('echo', { value: 7n })).rejects.toMatchObject({ error: 'ConfigError' })
        expect(request).toHaveBeenCalledOnce()
      } else {
        await expect(transport.call('echo', { value: 7n })).resolves.toEqual(action === 'mode' ? true : new Uint8Array([14]))
        expect(request).toHaveBeenCalledTimes(2)
        expect(request.mock.lastCall?.[0].data).toEqual(action === 'mode'
          ? '{"$api":"echo","value":7}' : new Uint8Array([9, 0, 2, 14, 0]).buffer)
      }
    })
  }

  it('keeps the JSON plan while a generated request replaces Schema', async () => {
    const transport = new WxTransport('https://example.test/api', {
      schema: { echo: { id: 7, params: [{ fieldID: 1, name: 'value', type: 'Int' }] } },
    })
    const input = new class extends GeneratedRequest {
      readonly apiID = 7
      writeParameters() {}
      toJSONParameters() {
        transport.setSchema({ echo: { id: 7, params: [{ fieldID: 1, name: 'value', type: 'Float' }] } })
        return { value: 1.5 }
      }
    }()
    request.mockImplementation(options => options.success({ statusCode: 200, data: '{"data":true}' }))
    const call = async () => transport.call('echo', input)
    await expect(call()).rejects.toThrow(/bigint/)
    expect(request).not.toHaveBeenCalled()
    await transport.call('echo', { value: 1.5 })
    expect(request).toHaveBeenCalledOnce()
  })

  it.each([false, true])('requires Binary response data while preserving its null marker (generated=%s)', async generated => {
    const transport = new WxTransport('https://example.test/api', { mode: 'binary', schema: { echo: { id: 7 } } })
    const codec = { apiID: 7, initialCapacity: () => 16, writeParameters() {} }
    const call = () => generated ? transport.callGenerated('echo', codec) : transport.call('echo')
    request.mockImplementation(options => options.success({ statusCode: 200, data: new ArrayBuffer(0) }))
    await expect(call()).rejects.toMatchObject({ error: 'ParseError', code: 200 })
    request.mockImplementation(options => options.success({ statusCode: 200, data: new Uint8Array([0]).buffer }))
    await expect(call()).resolves.toEqual(new Uint8Array([0]))
  })
  it('preserves own parameter keys without invoking prototype setters', async () => {
    const transport = new WxTransport('https://example.test/api')
    request.mockImplementation(options => options.success({ statusCode: 200, data: '{"data":true}' }))
    const params = JSON.parse('{"__proto__":{"value":7},"constructor":"input"}') as Record<string, unknown>
    await transport.call('echo', params)
    const sent = JSON.parse(request.mock.lastCall![0].data as string) as Record<string, unknown>
    expect(Object.hasOwn(sent, '__proto__')).toBe(true)
    expect(sent.__proto__).toEqual({ value: 7 })
    expect(sent.constructor).toBe('input')
    expect(Object.getPrototypeOf(params)).toBe(Object.prototype)
  })
  it('projects typed JSON Bytes and unknown enum members before sending', async () => {
    const transport = new WxTransport('https://example.test/api', { schema: { echo: { id: 7, params: [
      { fieldID: 1, name: 'bytes', type: 'Bytes' },
      { fieldID: 2, name: 'state', type: 'Enum', typeName: 'State' },
    ] } } })
    request.mockImplementation(options => options.success({ statusCode: 200, data: '{"data":true}' }))
    await transport.call('echo', { bytes: new Uint8Array([1, 255]), state: new UnknownEnumValue('State', 'NEW') })
    expect(JSON.parse(request.mock.lastCall![0].data as string)).toEqual({ $api: 'echo', bytes: 'Af8=', state: 'NEW' })
  })
  it('keeps the requested API authoritative over parameter metadata', async () => {
    const transport = new WxTransport('https://example.test/api', { schema: { echo: { id: 7 } } })
    request.mockImplementation(options => options.success({ statusCode: 200, data: '{"data":true}' }))
    await transport.call('echo', { $api: 'other' })
    expect(JSON.parse(request.mock.lastCall![0].data as string).$api).toBe('echo')
  })
  it.each([false, true])('rejects missing response data but preserves explicit null (generated=%s)', async generated => {
    const transport = new WxTransport('https://example.test/api', { schema: { echo: { id: 7 } } })
    const codec = { apiID: 7, initialCapacity: () => 16, writeParameters() {} }
    const call = () => generated ? transport.callGenerated('echo', codec) : transport.call('echo')
    for (const data of ['{}', '{"traceId":"request"}']) {
      request.mockImplementation(options => options.success({ statusCode: 200, data }))
      await expect(call()).rejects.toMatchObject({ error: 'ParseError', code: 200 })
    }
    for (const [data, value] of [['{"data":null}', null], ['{"data":false}', false], ['{"data":0}', 0]] as const) {
      request.mockImplementation(options => options.success({ statusCode: 200, data }))
      await expect(call()).resolves.toBe(value)
    }
  })
  it.each(['json', 'binary'] as const)('supports original typed parameters in the dedicated %s entry', async mode => {
    const schema = { echo: { id: 7, params: [{ fieldID: 1, name: 'value', type: 'Int' }] } }
    const transport = new WxTransport('https://example.test/api', { mode, schema })
    const values = Object.freeze({ value: 9007199254740993n })
    let writes = 0
    const codec = { apiID: 7, initialCapacity: () => 16, writeParameters(enc: Encoder, actual: typeof values | undefined) {
      expect(actual).toBe(values)
      writes++
      enc.writeVarint(1)
      enc.writeSvarint(actual!.value)
    } }
    request.mockImplementation(options => options.success({ statusCode: 200,
      data: mode === 'json' ? '{"data":true}' : new Uint8Array([1]).buffer }))
    await expect(transport.callGenerated('echo', codec, values)).resolves.toEqual(mode === 'json' ? true : new Uint8Array([1]))
    expect(writes).toBe(mode === 'binary' ? 1 : 0)
    await expect(transport.callGenerated('echo', { ...codec, apiID: 9 }, values)).rejects.toMatchObject({ error: 'ConfigError' })
    expect(request).toHaveBeenCalledTimes(1)
  })
  it.each(['json', 'binary'] as const)('owns constructor metadata and keeps valid %s registration after failure', async mode => {
    const schema = { echo: { id: 7, params: [{ fieldID: 1, name: 'value', type: 'Int' }] } }
    const transport = new WxTransport('https://example.test/api', { mode, schema })
    schema.echo.id = 9
    schema.echo.params[0].type = 'String'
    expect(() => transport.setSchema({ echo: { id: 0 } })).toThrow(/ID/)
    request.mockImplementation(options => options.success({ statusCode: 200,
      data: mode === 'json' ? '{"data":true}' : new Uint8Array([1]).buffer }))
    await expect(transport.call('echo', { value: 7n })).resolves.toEqual(mode === 'json' ? true : new Uint8Array([1]))
    expect(request.mock.lastCall?.[0].data).toEqual(mode === 'json'
      ? '{"$api":"echo","value":7}' : new Uint8Array([7, 0, 1, 14, 0]).buffer)
    await expect(transport.call('echo', { value: 'invalid' })).rejects.toThrow(/bigint/)
    expect(request).toHaveBeenCalledTimes(1)
  })

  it('exports the shared implementation from the public package entry', () => {
    expect(PublicWxTransport).toBe(WxTransport)
  })

  it('rejects malformed error fields and non-success data envelopes', async () => {
    const transport = new WxTransport('https://example.test/api')
    for (const [statusCode, data] of [
      [200, '{"error":false,"code":400,"message":"bad"}'],
      [401, '{"error":"Unauthorized","code":true,"message":"bad"}'],
      [403, '{"data":42}'],
      [401, '{"error":"Unauthorized","message":"bad"}'],
    ] as const) {
      request.mockImplementation(options => options.success({ statusCode, data }))
      await expect(transport.call('echo')).rejects.toMatchObject({ error: 'ParseError', code: statusCode })
    }
  })

  it('validates schema integers before issuing a JSON request', async () => {
    request.mockImplementation(options => options.success({ statusCode: 200, data: '{"data":true}' }))
    const schema = { echo: { id: 1, params: [{ fieldID: 1, name: 'value', type: 'Int' }] } }
    const transport = new WxTransport('https://example.test/api', { schema })
    for (const value of [1, '1', 1n << 63n]) {
      await expect(transport.call('echo', { value })).rejects.toThrow(/bigint|overflow/)
    }
    expect(request).not.toHaveBeenCalled()
    transport.setSchema({})
    await expect(transport.call('echo', { value: 1 })).resolves.toBe(true)
    transport.setSchema(schema)
    await expect(transport.call('echo', { value: 1 })).rejects.toThrow('bigint')
  })

  it('requests raw text and preserves signed int64 values in both directions', async () => {
    request.mockImplementation(options => options.success({ statusCode: 200, data: '{"data":9223372036854775807}' }))
    const transport = new WxTransport('https://example.test/api')
    await expect(transport.call('echo', { value: -9223372036854775808n })).resolves.toBe(9223372036854775807n)
    expect(request.mock.lastCall?.[0]).toMatchObject({
      dataType: 'text', responseType: 'text', data: '{"$api":"echo","value":-9223372036854775808}',
    })
  })

  it('rejects pre-parsed, malformed and non-object response envelopes', async () => {
    const transport = new WxTransport('https://example.test/api')
    for (const data of [{ data: 9223372036854775807 }, '[1]', 'null', '{', undefined]) {
      request.mockImplementation(options => options.success({ statusCode: 200, data }))
      await expect(transport.call('echo')).rejects.toMatchObject({ error: 'ParseError' })
    }
  })

  it('preserves full-width error data and raw JSON numbers', async () => {
    request.mockImplementation(options => options.success({ statusCode: 409,
      data: '{"error":"Conflict","code":409,"message":"bad","traceId":"trace","cause":"reason","data":{"id":9223372036854775807,"ratio":1.25}}',
    }))
    const transport = new WxTransport('https://example.test/api')
    await expect(transport.call('echo')).rejects.toMatchObject({
      error: 'Conflict', code: 409, traceId: 'trace', cause: 'reason',
      data: { id: 9223372036854775807n, ratio: { rawJSON: '1.25' } },
    })
  })

  it('rejects malformed Unicode in host JSON text, including overwritten values', async () => {
    const transport = new WxTransport('https://example.test/api')
    for (const statusCode of [200, 401, 500]) {
      for (const data of ['{"data":"\\ud800"}', '{"data":{"\\udfff":1}}',
        '{"data":"\\ud800","data":"valid"}']) {
        request.mockImplementation(options => options.success({ statusCode, data }))
        await expect(transport.call('echo')).rejects.toMatchObject({ error: 'ParseError', code: statusCode })
      }
    }
  })

  it.each(['json', 'binary'] as const)('rejects malformed strings before wx.request in %s mode', async mode => {
    const transport = new WxTransport('https://example.test/api', { mode,
      schema: { echo: { id: 1, params: [{ fieldID: 1, name: 'value', type: 'String' }] } },
    })
    await expect(transport.call('echo', { value: '\ud800' })).rejects.toThrow(/Unicode|surrogate/)
    expect(request).not.toHaveBeenCalled()
  })

  it('preserves full-width binary payloads and validates response type', async () => {
    const enc = new Encoder()
    enc.writeSvarint(9223372036854775807n)
    const payload = new Uint8Array(enc.bytes()).buffer
    request.mockImplementation(options => options.success({ statusCode: 200, data: payload }))
    const transport = new WxTransport('https://example.test/api', { mode: 'binary',
      schema: { echo: { id: 1, params: [{ fieldID: 1, name: 'value', type: 'Int' }] } },
    })
    await expect(transport.call('echo', { value: 9223372036854775807n })).resolves.toEqual(new Uint8Array(payload))
    expect(request.mock.lastCall?.[0].responseType).toBe('arraybuffer')
    request.mockImplementation(options => options.success({ statusCode: 200, data: 'not binary' }))
    await expect(transport.call('echo')).rejects.toMatchObject({ error: 'ParseError' })
    await expect(transport.call('missing')).rejects.toMatchObject({ error: 'ConfigError' })
  })

  it('decodes binary protocol errors', async () => {
    const enc = new Encoder()
    enc.writeFieldInt(1, 404n)
    enc.writeFieldString(2, 'NotFound')
    enc.writeFieldString(3, 'missing')
    enc.writeEnd()
    request.mockImplementation(options => options.success({ statusCode: 404, data: new Uint8Array(enc.bytes()).buffer }))
    const transport = new WxTransport('https://example.test/api', { mode: 'binary', schema: { echo: { id: 1 } } })
    await expect(transport.call('echo')).rejects.toMatchObject({ error: 'NotFound', code: 404 })
  })

  it('keeps token changes and network failures explicit in both modes', async () => {
    const transport = new WxTransport('https://example.test/api', { token: 'first' })
    request.mockImplementation(options => options.success({ statusCode: 200, data: '{"data":true}' }))
    await transport.call('echo')
    expect(request.mock.lastCall?.[0].header.Authorization).toBe('Bearer first')
    transport.setToken('second')
    await transport.call('echo')
    expect(request.mock.lastCall?.[0].header.Authorization).toBe('Bearer second')
    transport.setToken(null)
    await transport.call('echo')
    expect(request.mock.lastCall?.[0].header.Authorization).toBeUndefined()
    request.mockImplementation(options => options.fail({ errMsg: 'offline' }))
    await expect(transport.call('echo')).rejects.toMatchObject({ error: 'NetworkError' })
    transport.setMode('binary')
    transport.setSchema({ echo: { id: 1 } })
    await expect(transport.call('echo')).rejects.toMatchObject({ error: 'NetworkError' })
    await expect(transport.subscribe('echo', {}, (value: unknown) => value, () => {})).rejects.toMatchObject({ error: 'ConfigError' })
  })
})
