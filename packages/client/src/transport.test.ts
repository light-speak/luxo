import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { FetchTransport, WsTransport } from './transport'
import { LuxoError } from './error'

// Mock global fetch
const mockFetch = vi.fn()

beforeEach(() => {
  vi.stubGlobal('fetch', mockFetch)
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('FetchTransport constructor', () => {
  it('creates with endpoint only', () => {
    const t = new FetchTransport('http://localhost:8080/api')
    expect(t).toBeInstanceOf(FetchTransport)
  })

  it('creates with options', () => {
    const t = new FetchTransport('http://localhost:8080/api', {
      token: 'abc123',
      mode: 'json',
      timeout: 5000,
      headers: { 'X-Custom': 'test' },
    })
    expect(t).toBeInstanceOf(FetchTransport)
  })
})

describe('FetchTransport setToken / setMode / setSchema', () => {
  it('setToken updates authorization header', async () => {
    const t = new FetchTransport('http://localhost:8080/api')
    t.setToken('new-token')

    mockFetch.mockResolvedValueOnce({
      status: 200,
      json: async () => ({ data: { id: 1 } }),
    })

    await t.call('getUser', { id: 1 })

    const [, init] = mockFetch.mock.calls[0]
    expect(init.headers['Authorization']).toBe('Bearer new-token')
  })

  it('setToken clears authorization header on logout', async () => {
    const t = new FetchTransport('http://localhost:8080/api', { token: 'old-token' })
    t.setToken(null)

    mockFetch.mockResolvedValueOnce({
      status: 200,
      json: async () => ({ data: true }),
    })

    await t.call('health')

    const [, init] = mockFetch.mock.calls[0]
    expect(init.headers['Authorization']).toBeUndefined()
  })

  it('setMode changes transport mode', () => {
    const t = new FetchTransport('http://localhost:8080/api')
    // Should not throw
    t.setMode('binary')
    t.setMode('json')
  })

  it('setSchema stores API schemas', () => {
    const t = new FetchTransport('http://localhost:8080/api')
    t.setSchema({
      getUser: { id: 1, params: [{ fieldID: 1, name: 'id', type: 'Int' }] },
    })
    // No assertion needed — just verifying no throw
  })
})

describe('FetchTransport binary field selection', () => {
  it('encodes $select into the request field mask', async () => {
    const t = new FetchTransport('http://localhost:8080/api', { mode: 'binary' })
    t.setSchema({ getUser: { id: 1, fields: { id: 1, name: 2 } } })
    mockFetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      arrayBuffer: async () => new ArrayBuffer(0),
    })

    await t.call('getUser', { $select: 'name' })

    const body = new Uint8Array(mockFetch.mock.calls[0][1].body as ArrayBuffer)
    expect(Array.from(body)).toEqual([1, 1, 4, 0])
  })

  it('refreshes an expired token once in binary mode', async () => {
    const refresh = vi.fn().mockResolvedValue('fresh-token')
    const t = new FetchTransport('http://localhost:8080/api', {
      mode: 'binary',
      token: 'expired-token',
      onTokenExpired: refresh,
    })
    t.setSchema({ health: { id: 1 } })
    mockFetch
      .mockResolvedValueOnce({ ok: false, status: 401 })
      .mockResolvedValueOnce({ ok: true, status: 200, arrayBuffer: async () => new ArrayBuffer(0) })

    await t.call('health')

    expect(refresh).toHaveBeenCalledTimes(1)
    expect(mockFetch).toHaveBeenCalledTimes(2)
    expect(mockFetch.mock.calls[1][1].headers.Authorization).toBe('Bearer fresh-token')
  })
})

describe('FetchTransport jsonCall', () => {
  it('sends correct request format', async () => {
    const t = new FetchTransport('http://localhost:8080/api')

    mockFetch.mockResolvedValueOnce({
      status: 200,
      json: async () => ({ data: { id: 1, name: 'Alice' } }),
    })

    const result = await t.call('getUser', { id: 1 })

    expect(mockFetch).toHaveBeenCalledTimes(1)
    const [url, init] = mockFetch.mock.calls[0]
    expect(url).toBe('http://localhost:8080/api')
    expect(init.method).toBe('POST')
    expect(init.headers['Content-Type']).toBe('application/json')

    const body = JSON.parse(init.body)
    expect(body.$api).toBe('getUser')
    expect(body.id).toBe(1)

    expect(result).toEqual({ id: 1, name: 'Alice' })
  })

  it('sends request without params', async () => {
    const t = new FetchTransport('http://localhost:8080/api')

    mockFetch.mockResolvedValueOnce({
      status: 200,
      json: async () => ({ data: [] }),
    })

    const result = await t.call('listUsers')

    const body = JSON.parse(mockFetch.mock.calls[0][1].body)
    expect(body.$api).toBe('listUsers')
    expect(result).toEqual([])
  })
})

describe('FetchTransport error handling', () => {
  it('throws LuxoError on network error', async () => {
    const t = new FetchTransport('http://localhost:8080/api')

    mockFetch.mockRejectedValueOnce(new TypeError('Failed to fetch'))

    await expect(t.call('getUser')).rejects.toThrow(LuxoError)
    await expect(
      t.call('getUser').catch((e: LuxoError) => {
        expect(e.error).toBe('NetworkError')
        expect(e.code).toBe(0)
        throw e
      }),
    ).rejects.toThrow()
  })

  it('throws LuxoError on invalid JSON response', async () => {
    const t = new FetchTransport('http://localhost:8080/api')

    mockFetch.mockResolvedValueOnce({
      status: 200,
      json: async () => { throw new SyntaxError('Unexpected token') },
    })

    await expect(t.call('getUser')).rejects.toThrow(LuxoError)
    try {
      await t.call('getUser')
    } catch (e) {
      // Second call also throws NetworkError since fetch is not mocked again
    }
  })

  it('throws LuxoError on API error response', async () => {
    const t = new FetchTransport('http://localhost:8080/api')

    mockFetch.mockResolvedValueOnce({
      status: 404,
      json: async () => ({
        error: 'NotFound',
        code: 404,
        message: 'User not found',
        traceId: 'trace-123',
      }),
    })

    try {
      await t.call('getUser', { id: 999 })
      expect.fail('should have thrown')
    } catch (e) {
      expect(e).toBeInstanceOf(LuxoError)
      const err = e as LuxoError
      expect(err.error).toBe('NotFound')
      expect(err.code).toBe(404)
      expect(err.message).toBe('User not found')
      expect(err.traceId).toBe('trace-123')
    }
  })

  it('throws LuxoError on timeout', async () => {
    const t = new FetchTransport('http://localhost:8080/api', { timeout: 100 })

    mockFetch.mockImplementationOnce((_url: string, init: { signal: AbortSignal }) => {
      return new Promise((_, reject) => {
        init.signal.addEventListener('abort', () => {
          const err = new DOMException('The operation was aborted.', 'AbortError')
          reject(err)
        })
      })
    })

    try {
      await t.call('slowApi')
      expect.fail('should have thrown')
    } catch (e) {
      expect(e).toBeInstanceOf(LuxoError)
      const err = e as LuxoError
      expect(err.error).toBe('TimeoutError')
    }
  })

  it('cancels an in-flight request through an external AbortSignal', async () => {
    const controller = new AbortController()
    const t = new FetchTransport('http://localhost:8080/api')
    mockFetch.mockImplementationOnce((_url: string, init: { signal: AbortSignal }) => {
      return new Promise((_, reject) => {
        init.signal.addEventListener('abort', () => {
          reject(new DOMException('The operation was aborted.', 'AbortError'))
        })
      })
    })

    const request = t.call('getUser', undefined, { signal: controller.signal })
    controller.abort()

    await expect(request).rejects.toMatchObject({ error: 'CancelledError' })
  })

  it('does not dispatch a request for a pre-cancelled signal', async () => {
    const controller = new AbortController()
    controller.abort()
    const t = new FetchTransport('http://localhost:8080/api')

    await expect(t.call('getUser', undefined, { signal: controller.signal })).rejects.toMatchObject({
      error: 'CancelledError',
    })
    expect(mockFetch).not.toHaveBeenCalled()
  })

  it('cancels while reading a JSON response body', async () => {
    const controller = new AbortController()
    mockFetch.mockImplementationOnce((_url: string, init: { signal: AbortSignal }) => Promise.resolve({
      status: 200,
      json: () => new Promise((_, reject) => {
        if (init.signal.aborted) {
          reject(new DOMException('The operation was aborted.', 'AbortError'))
          return
        }
        init.signal.addEventListener('abort', () => {
          reject(new DOMException('The operation was aborted.', 'AbortError'))
        })
      }),
    }))
    const t = new FetchTransport('http://localhost:8080/api')
    const request = t.call('getUser', undefined, { signal: controller.signal })
    await Promise.resolve()
    controller.abort()

    await expect(request).rejects.toMatchObject({ error: 'CancelledError' })
  })

  it('times out while reading a Binary response body', async () => {
    mockFetch.mockImplementationOnce((_url: string, init: { signal: AbortSignal }) => Promise.resolve({
      ok: true,
      status: 200,
      arrayBuffer: () => new Promise((_, reject) => {
        init.signal.addEventListener('abort', () => {
          reject(new DOMException('The operation was aborted.', 'AbortError'))
        })
      }),
    }))
    const t = new FetchTransport('http://localhost:8080/api', { mode: 'binary', timeout: 20 })
    t.setSchema({ getUser: { id: 1 } })

    await expect(t.call('getUser')).rejects.toMatchObject({ error: 'TimeoutError' })
  })
})

describe('FetchTransport observability', () => {
  const secret = 'DO_NOT_LOG_THIS_SECRET'

  it('does not measure time when no observer is configured', async () => {
    const now = vi.spyOn(performance, 'now')
    const t = new FetchTransport('http://localhost:8080/api')
    mockFetch.mockResolvedValueOnce({
      status: 200,
      json: async () => ({ data: true }),
    })

    await t.call('health')

    expect(now).not.toHaveBeenCalled()
  })

  it('emits safe metadata without JSON parameter values', async () => {
    const observer = vi.fn()
    const t = new FetchTransport('http://localhost:8080/api', { observer })
    mockFetch.mockResolvedValueOnce({
      status: 200,
      json: async () => ({ data: true }),
    })

    await t.call('login', { username: 'admin', password: secret })

    expect(observer).toHaveBeenCalledOnce()
    expect(observer.mock.calls[0][0]).toMatchObject({
      api: 'login',
      mode: 'json',
      status: 'success',
    })
    expect(JSON.stringify(observer.mock.calls)).not.toContain(secret)
  })

  it('emits safe Luxo error metadata without Binary parameter values', async () => {
    const observer = vi.fn()
    const t = new FetchTransport('http://localhost:8080/api', { mode: 'binary', observer })
    t.setSchema({
      login: {
        id: 1,
        params: [
          { fieldID: 1, name: 'username', type: 'String' },
          { fieldID: 2, name: 'password', type: 'String' },
        ],
      },
    })
    mockFetch.mockResolvedValueOnce({
      ok: false,
      status: 401,
      json: async () => ({ error: 'Unauthorized', code: 401, message: 'invalid credentials', traceId: 'trace-safe' }),
    })

    await expect(t.call('login', { username: 'admin', password: secret })).rejects.toBeInstanceOf(LuxoError)

    expect(observer).toHaveBeenCalledOnce()
    expect(observer.mock.calls[0][0]).toMatchObject({
      api: 'login',
      mode: 'binary',
      status: 'error',
      errorCode: 'Unauthorized',
      traceId: 'trace-safe',
    })
    expect(JSON.stringify(observer.mock.calls)).not.toContain(secret)
  })
})

describe('LuxoError', () => {
  it('has correct properties', () => {
    const err = new LuxoError('NotFound', 404, 'User not found', 'trace-abc')
    expect(err.name).toBe('LuxoError')
    expect(err.error).toBe('NotFound')
    expect(err.code).toBe(404)
    expect(err.message).toBe('User not found')
    expect(err.traceId).toBe('trace-abc')
    expect(err).toBeInstanceOf(Error)
  })

  it('works without traceId', () => {
    const err = new LuxoError('NetworkError', 0, 'connection refused')
    expect(err.traceId).toBeUndefined()
  })
})

class FakeWebSocket {
  static readonly OPEN = 1
  static instances: FakeWebSocket[] = []

  readyState = 0
  binaryType = ''
  sent: Array<string | ArrayBuffer> = []
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((event: { data: string | ArrayBuffer }) => void) | null = null
  onclose: (() => void) | null = null

  constructor(readonly url: string) {
    FakeWebSocket.instances.push(this)
  }

  open() {
    this.readyState = FakeWebSocket.OPEN
    this.onopen?.()
  }

  message(data: string | ArrayBuffer) {
    this.onmessage?.({ data })
  }

  send(data: string | ArrayBuffer) {
    this.sent.push(data)
  }

  close() {
    this.readyState = 3
    this.onclose?.()
  }
}

describe('WsTransport subscriptions', () => {
  beforeEach(() => {
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket)
  })

  it('subscribes, dispatches JSON stream data, and unsubscribes', async () => {
    const transport = new WsTransport('ws://localhost/luvia', { token: 'secret' })
    const listener = vi.fn()
    const subscribed = transport.subscribe('liveAlerts', { projectId: 7 }, listener)
    const socket = FakeWebSocket.instances[0]

    expect(socket.url).toBe('ws://localhost/luvia?token=secret')
    socket.open()
    const unsubscribe = await subscribed

    expect(JSON.parse(socket.sent[0] as string)).toEqual({ $sub: 'liveAlerts', projectId: 7 })
    socket.message(JSON.stringify({ $stream: 'liveAlerts', data: { id: 9 } }))
    expect(listener).toHaveBeenCalledWith({ id: 9 })

    unsubscribe()
    expect(JSON.parse(socket.sent[1] as string)).toEqual({ $unsub: 'liveAlerts' })
    transport.close()
  })

  it('subscribes and dispatches binary stream payloads', async () => {
    const transport = new WsTransport('ws://localhost/luvia', { mode: 'binary' })
    transport.setSchema({ liveTraces: { id: 12, params: [{ fieldID: 1, name: 'projectId', type: 'Int' }] } })
    const listener = vi.fn()
    const subscribed = transport.subscribe('liveTraces', { projectId: 7 }, listener)
    const socket = FakeWebSocket.instances[0]
    socket.open()
    const unsubscribe = await subscribed

    expect(Array.from(new Uint8Array(socket.sent[0] as ArrayBuffer))).toEqual([0xFE, 12, 0, 1, 14, 0])
    socket.message(new Uint8Array([0xFD, 12, 9, 0]).buffer)
    expect(Array.from(listener.mock.calls[0][0] as Uint8Array)).toEqual([9, 0])

    unsubscribe()
    expect(Array.from(new Uint8Array(socket.sent[1] as ArrayBuffer))).toEqual([0xFF, 12])
    transport.close()
  })

  it('rejects subscriptions on a non-WebSocket transport', async () => {
    const transport = new FetchTransport('http://localhost/luvia')
    await expect(transport.subscribe('liveAlerts', {}, vi.fn())).rejects.toMatchObject({
      error: 'ConfigError',
    })
  })
})

describe('WsTransport observability', () => {
  const secret = 'DO_NOT_LOG_THIS_SECRET'

  beforeEach(() => {
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket)
  })

  it('does not measure time when no observer is configured', async () => {
    const now = vi.spyOn(performance, 'now')
    const transport = new WsTransport('ws://localhost/luvia')
    const request = transport.call('health')
    const socket = FakeWebSocket.instances[0]
    socket.open()
    await Promise.resolve()
    socket.message(JSON.stringify({ $id: 1, data: true }))

    await expect(request).resolves.toBe(true)
    expect(now).not.toHaveBeenCalled()
    transport.close()
  })

  it('emits safe metadata for successful and failed calls', async () => {
    const observer = vi.fn()
    const transport = new WsTransport('ws://localhost/luvia', { observer })

    const success = transport.call('login', { password: secret })
    const socket = FakeWebSocket.instances[0]
    socket.open()
    await Promise.resolve()
    socket.message(JSON.stringify({ $id: 1, data: true }))
    await expect(success).resolves.toBe(true)

    const failure = transport.call('login', { password: secret })
    await Promise.resolve()
    socket.message(JSON.stringify({
      $id: 2,
      error: 'Unauthorized',
      code: 401,
      message: 'invalid credentials',
      traceId: 'trace-safe',
    }))
    await expect(failure).rejects.toBeInstanceOf(LuxoError)

    expect(observer).toHaveBeenCalledTimes(2)
    expect(observer.mock.calls[0][0]).toMatchObject({ api: 'login', mode: 'json', status: 'success' })
    expect(observer.mock.calls[1][0]).toMatchObject({
      api: 'login',
      mode: 'json',
      status: 'error',
      statusCode: 401,
      errorCode: 'Unauthorized',
      traceId: 'trace-safe',
    })
    expect(JSON.stringify(observer.mock.calls)).not.toContain(secret)
    transport.close()
  })

  it('cancels a pending call and ignores its late response', async () => {
    const controller = new AbortController()
    const transport = new WsTransport('ws://localhost/luvia')
    const request = transport.call('health', undefined, { signal: controller.signal })
    const socket = FakeWebSocket.instances[0]
    socket.open()
    await Promise.resolve()

    controller.abort()
    await expect(request).rejects.toMatchObject({ error: 'CancelledError' })
    socket.message(JSON.stringify({ $id: 1, data: true }))
    transport.close()
  })
})
