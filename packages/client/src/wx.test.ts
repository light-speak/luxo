import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { WxTransport } from './wx'
import { WxTransport as PublicWxTransport } from './index'
import { Encoder } from './codec'

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
