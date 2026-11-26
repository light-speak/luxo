import { afterEach, describe, expect, it, vi } from 'vitest'
import { FetchTransport } from './transport'
import { Encoder } from './codec'
import { GeneratedRequest } from './request'
import type { RequestEncoder, RequestValues } from './request'
import type { TransportMode } from './transport'
import { createServer } from 'node:http'
import type { IncomingHttpHeaders } from 'node:http'

const utf8 = new TextEncoder()
afterEach(() => vi.unstubAllGlobals())

type ReplayScenario = 408 | 503 | 'lost' | 'refresh'
type ReplayCapture = { body: Uint8Array; token: string | undefined; port: number | undefined }
const replayEncoder: RequestEncoder<RequestValues> = {
  apiID: 1, initialCapacity: () => 8, writeParameters: () => {},
}

describe.each(['json', 'binary'] as const)('%s real HTTP POST replay boundary', mode => {
  for (const generated of [false, true]) for (const scenario of [408, 503, 'lost', 'refresh'] as const) {
    it(`${scenario} preserves explicit retry policy, generated=${generated}`, async () => {
      const requests: ReplayCapture[] = []
      const server = replayServer(requests, mode, scenario)
      await new Promise<void>((resolve, reject) => {
        server.once('error', reject)
        server.listen(0, '127.0.0.1', resolve)
      })
      try {
        const address = server.address()
        if (!address || typeof address === 'string') throw new Error('missing loopback port')
        const refresh = vi.fn(async () => 'renewed')
        const transport = new FetchTransport(`http://127.0.0.1:${address.port}`, {
          mode, token: 'original', onTokenExpired: refresh,
        })
        transport.setSchema({ echo: { id: 1 } })
        const call = () => generated ? transport.callGenerated('echo', replayEncoder) : transport.call('echo')
        await assertReplayPolicy(scenario, call, requests)
        expect(refresh).toHaveBeenCalledTimes(scenario === 'refresh' ? 1 : 0)
        const previous = requests.length
        await call()
        expect(requests).toHaveLength(previous + 1)
      } finally {
        server.closeAllConnections()
        await new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve()))
      }
    })
  }
})

async function assertReplayPolicy(scenario: ReplayScenario, call: () => Promise<unknown>, requests: ReplayCapture[]) {
  if (scenario === 'lost') {
    await call()
    await new Promise<void>(resolve => setImmediate(resolve))
    await expect(call()).rejects.toMatchObject({ error: 'NetworkError' })
    expect(requests).toHaveLength(2)
    expect(requests[1].port).toBe(requests[0].port)
  } else if (scenario === 'refresh') {
    await call()
    expect(requests).toHaveLength(2)
    expect(requests.map(request => request.token)).toEqual(['Bearer original', 'Bearer renewed'])
    expect(requests[1].body).toEqual(requests[0].body)
  } else {
    await expect(call()).rejects.toMatchObject({ error: 'Unavailable', code: scenario })
    expect(requests).toHaveLength(1)
  }
}

function replayServer(requests: ReplayCapture[], mode: TransportMode, scenario: ReplayScenario) {
  return createServer((request, reply) => {
    const chunks: Buffer[] = []
    request.on('data', (chunk: Buffer) => chunks.push(chunk))
    request.on('end', () => {
      requests.push({ body: Buffer.concat(chunks), token: request.headers.authorization, port: request.socket.remotePort })
      if (scenario === 'lost' && requests.length === 2) { request.socket.destroy(); return }
      const status = requests.length !== 1 || scenario === 'lost' ? 200 : scenario === 'refresh' ? 401 : scenario
      const name = status === 401 ? 'Unauthorized' : 'Unavailable'
      const body = status === 200
        ? mode === 'json' ? '{"data":42}' : Uint8Array.of(84)
        : mode === 'json' ? `{"error":"${name}","code":${status},"message":"retry later"}`
          : binaryError(BigInt(status), name, 'retry later')
      reply.writeHead(status, { 'Retry-After': '0' }).end(body)
    })
  })
}

it('keeps HTTP status and error presence authoritative over JSON data', async () => {
  for (const [status, body, name] of [
    [200, '{"data":true,"error":null,"code":401,"message":"bad"}', 'ParseError'],
    [200, '{"data":true,"error":false,"code":401,"message":"bad"}', 'ParseError'],
    [200, '{"data":true,"error":"Unauthorized","code":401,"message":"bad"}', 'Unauthorized'],
    [201, '{"data":true}', 'ParseError'],
    [403, '{"data":true}', 'ParseError'],
    [500, '{"data":true}', 'ParseError'],
  ] as const) {
    const refresh = vi.fn(async () => 'unused')
    const fetch = vi.fn(async () => new Response(body, { status }))
    vi.stubGlobal('fetch', fetch)
    const transport = new FetchTransport('http://localhost/api', { mode: 'json', onTokenExpired: refresh })
    await expect(transport.call('echo')).rejects.toMatchObject({ error: name, code: name === 'Unauthorized' ? 401 : status })
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(refresh).not.toHaveBeenCalled()
    fetch.mockImplementation(async () => new Response('{"data":true}'))
    await expect(transport.call('echo')).resolves.toBe(true)
    expect(fetch).toHaveBeenCalledTimes(2)
  }
})

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

describe.each(['json', 'binary'] as const)('%s HTTP call refresh policy', mode => {
  it.each(['direct', 'generated'] as const)('preserves the original 401 when a %s call disables refresh', async entry => {
    const refresh = vi.fn(async () => 'renewed')
    const fetch = vi.fn(async (_url: string, _init: RequestInit) => response(mode))
      .mockImplementationOnce(async () => response(mode, 401))
    vi.stubGlobal('fetch', fetch)
    const transport = new FetchTransport('http://localhost/api', { mode, token: 'original', onTokenExpired: refresh })
    transport.setSchema({ echo: { id: 1 } })
    const options = { signal: new AbortController().signal, refreshOnUnauthorized: false }
    const request = entry === 'direct'
      ? transport.call('echo', undefined, options)
      : transport.callGenerated('echo', replayEncoder, undefined, options)
    await expect(request).rejects.toMatchObject({ code: 401, error: 'Unauthorized', message: 'expired' })
    expect(refresh).not.toHaveBeenCalled()
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(new Headers(fetch.mock.calls[0][1].headers).get('authorization')).toBe('Bearer original')
  })

  it.each(['direct', 'generated'] as const)('keeps a same-client %s refresh call separate from other requests', async entry => {
    vi.useFakeTimers()
    const timeout = 12_345
    const schedule = vi.spyOn(globalThis, 'setTimeout')
    const cancel = vi.spyOn(globalThis, 'clearTimeout')
    const controller = new AbortController()
    const options = { signal: controller.signal }
    const refreshOptions = { ...options, refreshOnUnauthorized: false }
    const renewResponse = Promise.withResolvers<Response>()
    const renewEncoder = { ...replayEncoder, apiID: 2 }
    const refresh = vi.fn(async () => {
      const renew = entry === 'direct'
        ? transport.call('renew', undefined, refreshOptions)
        : transport.callGenerated('renew', renewEncoder, undefined, refreshOptions)
      await expect(renew).rejects.toMatchObject({ code: 401, error: 'Unauthorized', message: 'expired' })
      return 'renewed'
    })
    const transport = new FetchTransport('http://localhost/api', { mode, timeout, token: 'original', onTokenExpired: refresh })
    transport.setSchema({ echo: { id: 1 }, renew: { id: 2 } })
    const authorization: (string | null)[] = []
    vi.stubGlobal('fetch', vi.fn(async (_url: string, init: RequestInit) => {
      const token = new Headers(init.headers).get('authorization')
      authorization.push(token)
      if (authorization.length === 4) return renewResponse.promise
      return response(mode, token === 'Bearer renewed' ? 200 : 401)
    }))
    const calls = Promise.allSettled([
      transport.call('echo', undefined, options),
      transport.callGenerated('echo', replayEncoder, undefined, options),
      transport.call('echo', undefined, options),
    ])
    let outcomes: PromiseSettledResult<unknown>[] | undefined
    void calls.then(results => { outcomes = results })
    try {
      await vi.advanceTimersByTimeAsync(0)
      expect(refresh).toHaveBeenCalledOnce()
      expect(authorization).toEqual(Array.from({ length: 4 }, () => 'Bearer original'))
      renewResponse.resolve(response(mode, 401))
      await vi.advanceTimersByTimeAsync(0)
      expect(outcomes).toEqual(Array.from({ length: 3 }, () => ({
        status: 'fulfilled', value: mode === 'json' ? true : Uint8Array.of(1),
      })))
      expect(refresh).toHaveBeenCalledOnce()
      expect(authorization.slice(4)).toEqual(Array.from({ length: 3 }, () => 'Bearer renewed'))
      const deadlines = schedule.mock.results.filter((_, i) => schedule.mock.calls[i][1] === timeout)
      expect(deadlines).toHaveLength(4)
      for (const deadline of deadlines) expect(cancel).toHaveBeenCalledWith(deadline.value)
    } finally {
      controller.abort()
      renewResponse.resolve(response(mode, 401))
      await calls
      schedule.mockRestore()
      cancel.mockRestore()
      vi.clearAllTimers()
      vi.useRealTimers()
    }
  })
})

describe.each(['json', 'binary'] as const)('%s HTTP request metadata', mode => {
  it('does not refresh a late unauthorized response after cancellation', async () => {
    const abort = new AbortController()
    const refresh = vi.fn(async () => 'must-not-install')
    const fetch = vi.fn(async () => { abort.abort(); return response(mode, 401) })
    vi.stubGlobal('fetch', fetch)
    const transport = new FetchTransport('http://localhost/api', { mode, onTokenExpired: refresh })
    transport.setSchema({ echo: { id: 1 } })
    await expect(transport.call('echo', undefined, { signal: abort.signal }))
      .rejects.toMatchObject({ error: 'CancelledError' })
    expect(refresh).not.toHaveBeenCalled()
    expect(fetch).toHaveBeenCalledTimes(1)
    fetch.mockImplementation(async () => response(mode))
    await expect(transport.call('echo')).resolves.toEqual(mode === 'json' ? true : Uint8Array.of(1))
    expect(fetch).toHaveBeenCalledTimes(2)
  })

  it('does not send when generated parameter encoding aborts the call', async () => {
    const abort = new AbortController()
    const fetch = vi.fn(async () => response(mode))
    vi.stubGlobal('fetch', fetch)
    class CancellingRequest extends GeneratedRequest {
      readonly apiID = 1
      writeParameters(): void { abort.abort() }
      toJSONParameters(): Record<string, unknown> { abort.abort(); return {} }
    }
    const transport = new FetchTransport('http://localhost/api', { mode })
    transport.setSchema({ echo: { id: 1 } })
    await expect(transport.call('echo', new CancellingRequest(), { signal: abort.signal }))
      .rejects.toMatchObject({ error: 'CancelledError' })
    expect(fetch).not.toHaveBeenCalled()
    await expect(transport.call('echo')).resolves.toEqual(mode === 'json' ? true : Uint8Array.of(1))
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('rechecks cancellation after the static request input is encoded', async () => {
    const abort = new AbortController()
    const fetch = vi.fn(async () => response(mode))
    vi.stubGlobal('fetch', fetch)
    const codec: RequestEncoder<RequestValues> = {
      apiID: 1,
      initialCapacity: () => 16,
      writeParameters: () => { abort.abort() },
    }
    const params = { get value(): bigint { abort.abort(); return 42n } }
    const transport = new FetchTransport('http://localhost/api', { mode })
    transport.setSchema({ echo: { id: 1, params: [{ name: 'value', fieldID: 1, type: 'Int' }] } })
    await expect(transport.callGenerated('echo', codec, params, { signal: abort.signal }))
      .rejects.toMatchObject({ error: 'CancelledError' })
    expect(fetch).not.toHaveBeenCalled()
  })

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

it('rejects scalar and array envelopes without refresh, preserving explicit null data', async () => {
  for (const status of [200, 401, 500]) {
    for (const body of ['[]', '[{"data":true}]', 'null', 'true', '42', '9223372036854775807', '1.234567890123456789', '"data"', '{}']) {
      const refresh = vi.fn(async () => 'must-not-refresh')
      const fetch = vi.fn(async () => new Response(body, { status }))
      vi.stubGlobal('fetch', fetch)
      const transport = new FetchTransport('http://localhost/api', { mode: 'json', onTokenExpired: refresh })
      await expect(transport.call('echo')).rejects.toMatchObject({ error: 'ParseError', code: status })
      expect(refresh).not.toHaveBeenCalled()
      expect(fetch).toHaveBeenCalledTimes(1)
      fetch.mockImplementation(async () => new Response('{"data":null}', { status: 200 }))
      await expect(transport.call('echo')).resolves.toBeNull()
      expect(fetch).toHaveBeenCalledTimes(2)
    }
  }
})

describe.each(['json', 'binary'] as const)('%s refresh deadline', mode => {
  it('expires while refresh is pending and never applies its late token', async () => {
    vi.useFakeTimers()
    try {
      let finish!: (token: string) => void
      const refresh = vi.fn(() => new Promise<string>(resolve => { finish = resolve }))
      const fetch = vi.fn(async () => response(mode, 401))
      vi.stubGlobal('fetch', fetch)
      const transport = new FetchTransport('http://localhost/api', { mode, timeout: 100, token: 'original', onTokenExpired: refresh })
      transport.setSchema({ echo: { id: 1 } })
      const outcome = transport.call('echo').then(() => 'success', (error: unknown) => error)
      await vi.advanceTimersByTimeAsync(0)
      expect(refresh).toHaveBeenCalledTimes(1)
      await vi.advanceTimersByTimeAsync(100)
      let settled = false
      void outcome.then(() => { settled = true })
      await Promise.resolve()
      expect(settled).toBe(true)
      expect(await outcome).toMatchObject({ error: 'TimeoutError' })
      finish('late-token')
      await vi.advanceTimersByTimeAsync(0)
      expect(fetch).toHaveBeenCalledTimes(1)
      expect(vi.getTimerCount()).toBe(0)
      let authorization: string | null = null
      vi.stubGlobal('fetch', vi.fn(async (_url: string, init: RequestInit) => {
        authorization = new Headers(init.headers).get('authorization')
        return response(mode)
      }))
      await transport.call('echo')
      expect(authorization).toBe('Bearer original')
    } finally {
      vi.useRealTimers()
    }
  })
})

describe.each(['json', 'binary'] as const)('%s refresh retry budget', mode => {
  it('retains the original deadline and abort signal for the retry', async () => {
    vi.useFakeTimers()
    try {
      let finish!: (token: string) => void
      const signals: AbortSignal[] = []
      const fetch = vi.fn(async (_url: string, init: RequestInit) => {
        signals.push(init.signal as AbortSignal)
        if (signals.length === 1) return response(mode, 401)
        return new Promise<Response>((_resolve, reject) => {
          init.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')), { once: true })
        })
      })
      vi.stubGlobal('fetch', fetch)
      const transport = new FetchTransport('http://localhost/api', {
        mode, timeout: 100, onTokenExpired: () => new Promise<string>(resolve => { finish = resolve }),
      })
      transport.setSchema({ echo: { id: 1 } })
      const outcome = transport.call('echo').then(() => 'success', (error: unknown) => error)
      await vi.advanceTimersByTimeAsync(70)
      finish('renewed')
      await vi.advanceTimersByTimeAsync(0)
      expect(fetch).toHaveBeenCalledTimes(2)
      expect(signals[1]).toBe(signals[0])
      await vi.advanceTimersByTimeAsync(30)
      expect(await outcome).toMatchObject({ error: 'TimeoutError' })
      expect(vi.getTimerCount()).toBe(0)
    } finally { vi.useRealTimers() }
  })
})

describe.each(['json', 'binary'] as const)('%s HTTP retry request snapshot', mode => {
  it('reuses the original encoded request after parameters and schema change', async () => {
    const params = { count: 42n }
    const requests: (string | number[])[] = []
    vi.stubGlobal('fetch', vi.fn(async (_url: string, init: RequestInit) => {
      requests.push(typeof init.body === 'string' ? init.body : Array.from(new Uint8Array(init.body as ArrayBuffer)))
      return response(mode, requests.length === 1 ? 401 : 200)
    }))
    const transport = new FetchTransport('http://localhost/api', {
      mode, onTokenExpired: async () => {
        params.count = 99n
        transport.setSchema({ echo: { id: 2, params: [{ fieldID: 3, name: 'count', type: 'Int' }] } })
        return 'renewed'
      },
    })
    transport.setSchema({ echo: { id: 1, params: [{ fieldID: 1, name: 'count', type: 'Int' }] } })
    await transport.call('echo', params)
    expect(requests).toHaveLength(2)
    expect(requests[1]).toEqual(requests[0])
    await transport.call('echo', params)
    expect(requests[2]).not.toEqual(requests[0])
  })
})

describe.each(['json', 'binary'] as const)('%s refresh settlement race', mode => {
  it('does not apply a token when the deadline expires before the awaiting call resumes', async () => {
    vi.useFakeTimers()
    try {
      let finish!: (token: string) => void
      const refresh = vi.fn(() => new Promise<string>(resolve => { finish = resolve }))
      const fetch = vi.fn(async () => response(mode, 401))
      vi.stubGlobal('fetch', fetch)
      const transport = new FetchTransport('http://localhost/api', {
        mode, timeout: 100, token: 'original', onTokenExpired: refresh,
      })
      transport.setSchema({ echo: { id: 1 } })
      const outcome = transport.call('echo').catch((error: unknown) => error)
      await vi.advanceTimersByTimeAsync(0)
      expect(refresh).toHaveBeenCalledTimes(1)
      finish('expired-result')
      // Resolve the refresh waiter, then expire before its awaiting call resumes.
      await Promise.resolve()
      vi.advanceTimersByTime(100)
      expect(await outcome).toMatchObject({ error: 'TimeoutError' })
      expect(fetch).toHaveBeenCalledTimes(1)
      let authorization: string | null = null
      vi.stubGlobal('fetch', vi.fn(async (_url: string, init: RequestInit) => {
        authorization = new Headers(init.headers).get('authorization')
        return response(mode)
      }))
      await transport.call('echo')
      expect(authorization).toBe('Bearer original')
      expect(vi.getTimerCount()).toBe(0)
    } finally { vi.useRealTimers() }
  })
})

it('rejects same-origin and cross-origin business redirects without forwarding credentials or bodies', async () => {
  let targetRequests = 0
  const target = createServer((_request, reply) => { targetRequests++; reply.end('{"data":true}') })
  let location = '', status = 302, requireRefresh = false
  const source = createServer((request, reply) => {
    request.resume()
    if (requireRefresh && request.headers.authorization !== 'Bearer renewed') {
      reply.writeHead(401).end(request.headers['x-luxo-mode'] === 'binary'
        ? binaryError() : '{"error":"Unauthorized","code":401,"message":"expired"}')
      return
    }
    if (request.url === '/capture') { targetRequests++; reply.end('{"data":true}'); return }
    reply.writeHead(status, { Location: location }).end()
  })
  try {
    await new Promise<void>(resolve => target.listen(0, '127.0.0.1', resolve))
    await new Promise<void>(resolve => source.listen(0, '127.0.0.1', resolve))
    const sourceAddress = source.address(), targetAddress = target.address()
    if (!sourceAddress || typeof sourceAddress === 'string' || !targetAddress || typeof targetAddress === 'string') throw new Error('missing port')
    const endpoint = `http://127.0.0.1:${sourceAddress.port}`
    for (const mode of ['json', 'binary'] as const) {
      requireRefresh = false
      const refresh = vi.fn(async () => 'renewed')
      const transport = new FetchTransport(endpoint + '/api', {
        mode, headers: { 'X-Project-Key': 'test-only-key' }, onTokenExpired: refresh,
      })
      transport.setSchema({ echo: { id: 1, params: [{ fieldID: 1, name: 'value', type: 'String' }] } })
      for (status of [301, 302, 303, 307, 308]) {
        for (location of ['/capture', `http://127.0.0.1:${targetAddress.port}/capture`]) {
          await expect(transport.call('echo', { value: 'private-input' })).rejects.toMatchObject({ error: 'NetworkError' })
          expect(targetRequests).toBe(0)
        }
      }
      expect(refresh).not.toHaveBeenCalled()
      requireRefresh = true
      await expect(transport.call('echo', { value: 'retry-input' })).rejects.toMatchObject({ error: 'NetworkError' })
      expect(refresh).toHaveBeenCalledTimes(1)
      expect(targetRequests).toBe(0)
    }
  } finally {
    source.closeAllConnections(); target.closeAllConnections()
    await Promise.all([source, target].map(server => new Promise<void>((resolve, reject) => {
      server.close(error => error ? reject(error) : resolve())
    })))
  }
})
