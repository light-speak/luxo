import { afterEach, describe, expect, it, vi } from 'vitest'
import { FetchTransport } from './transport'
import { Encoder } from './codec'
import type { TransportMode } from './transport'
import { createServer } from 'node:http'
import type { IncomingHttpHeaders } from 'node:http'

const utf8 = new TextEncoder()
afterEach(() => vi.unstubAllGlobals())

function binaryError(code = 401n, name = 'Unauthorized', message = 'expired'): Uint8Array<ArrayBuffer> {
  const encoder = new Encoder()
  encoder.writeFieldInt(1, code)
  encoder.writeFieldString(2, name)
  encoder.writeFieldString(3, message)
  encoder.writeEnd()
  return new Uint8Array(encoder.bytes())
}

function response(mode: TransportMode, status = 200): Response {
  if (status === 401) return new Response(mode === 'json'
    ? '{"error":"Unauthorized","code":401,"message":"expired"}' : binaryError(), { status })
  return new Response(mode === 'json' ? '{"data":true}' : Uint8Array.of(1))
}

describe.each(['json', 'binary'] as const)('%s HTTP request metadata', mode => {
  it('preserves cache overload without automatic retry or token refresh', async () => {
    const refresh = vi.fn(async () => 'must-not-refresh')
    const fetch = vi.fn(async () => new Response(mode === 'json'
      ? '{"error":"CacheOriginBusy","code":503,"message":"Cache origin capacity exhausted"}'
      : binaryError(503n, 'CacheOriginBusy', 'Cache origin capacity exhausted'), { status: 503 }))
    vi.stubGlobal('fetch', fetch)
    const transport = new FetchTransport('http://localhost/api', { mode, onTokenExpired: refresh })
    transport.setSchema({ echo: { id: 1 } })
    await expect(transport.call('echo')).rejects.toMatchObject({
      error: 'CacheOriginBusy', code: 503, message: 'Cache origin capacity exhausted',
    })
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(refresh).not.toHaveBeenCalled()
    fetch.mockImplementation(async () => response(mode))
    const next = await transport.call('echo')
    expect(next).toEqual(mode === 'json' ? true : Uint8Array.of(1))
    expect(fetch).toHaveBeenCalledTimes(2)
  })

  it('reserves case-insensitive protocol headers and snapshots caller-owned options', async () => {
    const requests: RequestInit[] = []
    vi.stubGlobal('fetch', vi.fn(async (_url: string, init: RequestInit) => {
      requests.push(init)
      return response(mode)
    }))
    const headers = { 'CONTENT-type': 'text/plain', 'x-LuXo-MoDe': 'wrong', 'X-Region': 'initial' }
    const transport = new FetchTransport('http://localhost/api', { mode, headers })
    transport.setSchema({ echo: { id: 1 } })
    headers['X-Region'] = 'changed'
    for (let index = 0; index < 2; index++) await transport.call('echo')
    expect(requests[0].headers).toBe(requests[1].headers)
    for (const request of requests) {
      const actual = new Headers(request.headers)
      expect(actual.get('content-type')).toBe(mode === 'json' ? 'application/json' : 'application/x-luxo')
      expect(actual.get('x-luxo-mode')).toBe(mode)
      expect(actual.get('x-region')).toBe('initial')
    }
  })

  it('updates and clears every authorization casing without changing already-dispatched headers', async () => {
    const requests: RequestInit[] = []
    vi.stubGlobal('fetch', vi.fn(async (_url: string, init: RequestInit) => {
      requests.push(init)
      return response(mode)
    }))
    const transport = new FetchTransport('http://localhost/api', {
      mode, token: 'initial', headers: { authorization: 'Bearer stale', Authorization: 'Bearer duplicate' },
    })
    transport.setSchema({ echo: { id: 1 } })
    await transport.call('echo')
    transport.setToken('renewed')
    await transport.call('echo')
    transport.setToken(null)
    await transport.call('echo')
    expect(requests.map(request => new Headers(request.headers).get('authorization')))
      .toEqual(['Bearer initial', 'Bearer renewed', null])
  })

  it('keeps the original request mode through token refresh and switches only future requests', async () => {
    const requests: RequestInit[] = []
    const other = mode === 'json' ? 'binary' : 'json'
    vi.stubGlobal('fetch', vi.fn(async (_url: string, init: RequestInit) => {
      requests.push(init)
      return response(requests.length === 3 ? other : mode, requests.length === 1 ? 401 : 200)
    }))
    const transport = new FetchTransport('http://localhost/api', {
      mode, headers: { 'CONTENT-TYPE': 'text/plain', 'X-Luxo-Mode': 'wrong', authorization: 'Bearer old' },
      onTokenExpired: async () => { transport.setMode(other); return 'renewed' },
    })
    transport.setSchema({ echo: { id: 1 } })
    await transport.call('echo')
    await transport.call('echo')
    expect(requests.map(request => new Headers(request.headers).get('x-luxo-mode'))).toEqual([mode, mode, other])
    expect(requests.map(request => new Headers(request.headers).get('authorization')))
      .toEqual(['Bearer old', 'Bearer renewed', 'Bearer renewed'])
    expect(requests[1].body).toEqual(requests[0].body)
  })
})

describe('strict HTTP JSON text', () => {
  it('rejects isolated escapes in values, keys and overwritten fields before success or refresh', async () => {
    for (const status of [200, 401, 500]) {
      for (const data of ['"\\uD800"', '{"\\uDC00":1}', '{"x":"\\uD800","x":"valid"}']) {
        const body = status === 200 ? '{"data":' + data + '}'
          : '{"error":"Unauthorized","code":401,"message":"expired","data":' + data + '}'
        const refresh = vi.fn(async () => 'must-not-refresh')
        vi.stubGlobal('fetch', vi.fn(async () => new Response(body, { status })))
        await expect(new FetchTransport('http://localhost/api', { onTokenExpired: refresh }).call('echo'))
          .rejects.toMatchObject({ error: 'ParseError', code: status })
        expect(refresh).not.toHaveBeenCalled()
      }
    }
  })

  it.each(['json', 'binary'] as const)('does not send malformed %s strings or generic JSON', async mode => {
    const fetch = vi.fn(async () => new Response('{"data":true}'))
    vi.stubGlobal('fetch', fetch)
    const transport = new FetchTransport('http://localhost/api', { mode })
    transport.setSchema({ echo: { id: 1, params: [
      { fieldID: 1, name: 'text', type: 'String' }, { fieldID: 2, name: 'raw', type: 'JSON' },
    ] } })
    for (const params of [{ text: '\ud800' }, { raw: { ['\udc00']: 1 } }]) {
      await expect(transport.call('echo', params)).rejects.toThrow(/Unicode|surrogate/)
    }
    expect(fetch).not.toHaveBeenCalled()
  })

  it('rejects malformed UTF-8 before exposing success data or refreshing a token', async () => {
    for (const status of [200, 401, 500]) {
      for (const invalid of [[255], [192, 128], [237, 160, 128], [244, 144, 128, 128], [226, 130]]) {
        const prefix = status === 200 ? '{"data":"' : `{"error":"Unauthorized","code":${status},"message":"`
        const bytes = Uint8Array.from([...utf8.encode(prefix), ...invalid, ...utf8.encode('"}')])
        const refresh = vi.fn(async () => 'must-not-refresh')
        const fetch = vi.fn(async () => new Response(bytes, { status,
          headers: { 'Content-Type': 'application/json; charset=iso-8859-1' } }))
        vi.stubGlobal('fetch', fetch)
        const transport = new FetchTransport('http://localhost/api', { onTokenExpired: refresh })
        await expect(transport.call('echo')).rejects.toMatchObject({ error: 'ParseError', code: status })
        expect(refresh).not.toHaveBeenCalled()
        expect(fetch).toHaveBeenCalledTimes(1)
      }
    }
  })

  it('preserves valid text, original document BOM acceptance and exact integer tokens regardless of charset', async () => {
    for (const charset of ['utf-8', 'iso-8859-1', 'utf-16le']) {
      for (const bom of ['', '\ufeff']) {
        vi.stubGlobal('fetch', vi.fn(async () => new Response(
          utf8.encode(bom + '{"data":{"value":9223372036854775807,"text":"发布\\u0000�﻿"}}'),
          { headers: { 'Content-Type': `application/json; charset=${charset}` } },
        )))
        await expect(new FetchTransport('http://localhost/api').call('echo')).resolves.toEqual({
          value: 9223372036854775807n, text: '发布\u0000\ufffd\ufeff',
        })
      }
    }
  })

  it('does not allow a parameter to replace the selected API or change its own object', async () => {
    const fetch = vi.fn(async (_url: string, _init: RequestInit) => new Response('{"data":true}'))
    vi.stubGlobal('fetch', fetch)
    const params = Object.freeze({ $api: 'wrong', value: 9223372036854775807n })
    await new FetchTransport('http://localhost/api').call('echo', params)
    expect(JSON.parse(fetch.mock.calls[0][1].body as string)).toMatchObject({ $api: 'echo' })
    expect(params.$api).toBe('wrong')
    expect(fetch.mock.calls[0][1].body).toContain('9223372036854775807')
  })
})

type CapturedRequest = { headers: IncomingHttpHeaders; body: string }

function loopbackServer(requests: CapturedRequest[]) {
  const invalid = Uint8Array.from([...utf8.encode('{"error":"Unauthorized","code":401,"message":"'), 255, 34, 125])
  return createServer((request, response) => {
    const chunks: Buffer[] = []
    request.on('data', (chunk: Buffer) => chunks.push(chunk))
    request.on('end', () => {
      requests.push({ headers: request.headers, body: Buffer.concat(chunks).toString() })
      response.setHeader('Content-Type', 'application/json; charset=iso-8859-1')
      if (request.url === '/invalid') response.writeHead(401).end(invalid)
      else if (request.headers['x-luxo-mode'] === 'binary') response.end(Uint8Array.of(1))
      else {
        const bytes = utf8.encode('{"data":{"value":9223372036854775807,"text":"发布"}}')
        response.write(bytes.subarray(0, bytes.length - 4))
        response.end(bytes.subarray(bytes.length - 4))
      }
    })
  })
}

it('enforces headers and raw UTF-8 through an actual loopback HTTP connection', async () => {
  const requests: CapturedRequest[] = []
  const server = loopbackServer(requests)
  await new Promise<void>((resolve, reject) => {
    server.once('error', reject)
    server.listen(0, '127.0.0.1', resolve)
  })
  try {
    const address = server.address()
    if (!address || typeof address === 'string') throw new Error('Expected loopback TCP address')
    const endpoint = `http://127.0.0.1:${address.port}`
    for (const mode of ['json', 'binary'] as const) {
      const transport = new FetchTransport(endpoint, { mode, headers: { 'content-TYPE': 'wrong', 'x-luxo-MODE': 'wrong' } })
      transport.setSchema({ echo: { id: 1 } })
      expect(await transport.call('echo', { $api: 'wrong' })).toEqual(mode === 'json'
        ? { value: 9223372036854775807n, text: '发布' } : Uint8Array.of(1))
    }
    const refresh = vi.fn(async () => 'must-not-refresh')
    await expect(new FetchTransport(endpoint + '/invalid', { onTokenExpired: refresh }).call('echo'))
      .rejects.toMatchObject({ error: 'ParseError', code: 401 })
    expect(refresh).not.toHaveBeenCalled()
    expect(requests.map(request => request.headers['x-luxo-mode'])).toEqual(['json', 'binary', 'json'])
    expect(requests.map(request => request.headers['content-type']))
      .toEqual(['application/json', 'application/x-luxo', 'application/json'])
    expect(JSON.parse(requests[0].body)).toMatchObject({ $api: 'echo' })
  } finally {
    server.closeAllConnections()
    await new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve()))
  }
})
