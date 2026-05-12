import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { FetchTransport } from './transport'
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
