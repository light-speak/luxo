import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { FetchTransport, WsTransport, decodeBinaryError, encodeBinaryBody, encodeParam } from './transport'
import { Encoder } from './codec'
import { LuxoError } from './error'
import { decodeLuxoJSON } from './json'
import type { APISchema, TransportMode } from './transport'

const integerParamSchema: APISchema = {
  id: 1,
  params: [
    { fieldID: 1, name: 'count', type: 'Int' },
    { fieldID: 2, name: 'durations', type: 'Duration', isList: true, nullable: true },
    { fieldID: 3, name: 'input', type: 'Model', typeName: 'Input' },
    { fieldID: 4, name: 'score', type: 'Float' },
    { fieldID: 5, name: 'json', type: 'JSON' },
  ],
  types: { Input: {
    count: { fieldID: 1, type: 'Int', nullable: true },
    children: { fieldID: 2, type: 'Model', typeName: 'Input', isList: true },
  } },
}

// Mock global fetch
const mockFetch = vi.fn()

beforeEach(() => {
  mockFetch.mockReset()
  vi.stubGlobal('fetch', mockFetch)
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('binary param validation', () => {
	it('rejects misplaced UUID separators in scalar, list and model params', () => {
		const malformed = '----00112233445566778899aabbccddeeff'
		expect(() => encodeParam(new Encoder(), { fieldID: 1, type: 'UUID' }, malformed)).toThrow()
		expect(() => encodeParam(new Encoder(), { fieldID: 1, type: 'UUID', isList: true }, [malformed])).toThrow()
		expect(() => encodeParam(new Encoder(), { fieldID: 1, type: 'Model', typeName: 'Input' },
			{ id: malformed }, { Input: { id: { fieldID: 1, type: 'UUID' } } })).toThrow()
	})
	it('reports an oversized binary error code as a parse error', () => {
		const enc = new Encoder()
		enc.writeFieldInt(1, (1n << 63n) - 1n)
		enc.writeFieldString(2, 'Internal')
		enc.writeFieldString(3, 'failure')
		enc.writeEnd()
		expect(decodeBinaryError(enc.bytes(), 500).error).toBe('ParseError')
	})
	it('rejects non-canonical binary error envelopes', () => {
		for (const body of [
			new Uint8Array([1]),
			new Uint8Array([1, 0xa0, 0x06, 0]),
			new Uint8Array([1, 0xa0, 0x06, 2, 1, 69, 3, 1, 109]),
			new Uint8Array([1, 0xa0, 0x06, 2, 1, 69, 3, 1, 109, 0, 1]),
		]) {
			expect(decodeBinaryError(body, 400).error).toBe('ParseError')
		}
	})
	it('rejects unknown wire types', () => {
		expect(() => encodeParam(new Encoder(), { fieldID: 1, type: 'Unsupported' }, {})).toThrow('unsupported binary param type')
		expect(() => encodeParam(new Encoder(), { fieldID: 1, type: 'Unsupported', isList: true }, [])).toThrow('unsupported binary list param type')
	})

	it('encodes structured params as native length-delimited Luxo messages', () => {
		const meta = {
			id: 9,
			params: [{ fieldID: 7, name: 'input', type: 'Model', typeName: 'CreateInput' }],
			types: {
				CreateInput: {
					name: { fieldID: 1, type: 'String' },
					count: { fieldID: 2, type: 'Int' },
					child: { fieldID: 3, type: 'Model', typeName: 'Child', nullable: true },
					tags: { fieldID: 4, type: 'String', isList: true },
				},
				Child: { enabled: { fieldID: 1, type: 'Boolean' } },
			},
		}
		const body = encodeBinaryBody(meta, {
			input: { name: 'é', count: -2n, child: { enabled: true }, tags: ['a', 'bc'] },
		})
		expect(Array.from(body)).toEqual([
			9, 0, 7, 21,
			2, 1, 2, 0xc3, 0xa9, 2, 3, 3, 1,
			0, 1, 1, 0, 4, 2, 1, 97, 2, 98, 99, 0,
			0,
		])
	})

	it('encodes structured param lists and rejects incomplete model metadata', () => {
		const meta = {
			id: 3,
			params: [{ fieldID: 2, name: 'items', type: 'Model', typeName: 'Item', isList: true }],
			types: { Item: { value: { fieldID: 1, type: 'Int' } } },
		}
		expect(Array.from(encodeBinaryBody(meta, { items: [{ value: 1n }, { value: 2n }] }))).toEqual([
			3, 0, 2, 2, 4, 0, 1, 2, 0, 4, 0, 1, 4, 0, 0,
		])
		expect(() => encodeParam(new Encoder(), { fieldID: 1, type: 'Model', typeName: 'Missing' }, {}, {}))
			.toThrow('missing schema for structured type Missing')
	})

	it('accepts only RFC3339 strings for DateTime', () => {
		expect(() => encodeParam(new Encoder(), { fieldID: 1, type: 'DateTime' }, 0)).toThrow('RFC3339 string')
		expect(() => encodeParam(new Encoder(), { fieldID: 1, type: 'DateTime' }, 'not-a-date')).toThrow('RFC3339 string')
	})

	it('distinguishes nullable null, present, and absent params', () => {
		const meta = {
			id: 9,
			params: [
				{ fieldID: 1, name: 'nickname', type: 'String', nullable: true },
				{ fieldID: 2, name: 'age', type: 'Int', nullable: true },
			],
		}
		expect(Array.from(encodeBinaryBody(meta, { nickname: null, age: 42n }))).toEqual([9, 0, 1, 0, 2, 1, 84, 0])
		expect(Array.from(encodeBinaryBody(meta, {}))).toEqual([9, 0, 0])
	})

	it('encodes filters and sorters instead of dropping binary list controls', () => {
		const body = encodeBinaryBody({ id: 5 }, {
			$filters: [{ field: 'age', op: 'gte', value: 18 }],
			$sorters: [{ field: 'createdAt', order: 'desc' }],
		})
		expect(Array.from(body)).toEqual([
			5, 0,
			0xfe, 0xff, 0xff, 0xff, 0x07, 1, 3, 97, 103, 101, 4, 2, 49, 56,
			0xff, 0xff, 0xff, 0xff, 0x07, 1, 9, 99, 114, 101, 97, 116, 101, 100, 65, 116, 1,
			0,
		])
	})

	it('rejects malformed binary list controls', () => {
		expect(() => encodeBinaryBody({ id: 1 }, { $filters: [{ field: 'age', op: 'bad', value: 1 }] })).toThrow('invalid $filters')
		expect(() => encodeBinaryBody({ id: 1 }, { $sorters: [{ field: 'age', order: 'bad' }] })).toThrow('invalid $sorters')
	})
})

describe('JSON/Binary integer input parity', () => {
  it.each<TransportMode>(['json', 'binary'])('rejects invalid scalar/list/nested integers before HTTP dispatch in %s', async mode => {
    mockFetch.mockReset()
    mockFetch.mockImplementation(async () => new Response('{"data":true}'))
    const transport = new FetchTransport('http://localhost/api', { mode })
    transport.setSchema({ echo: integerParamSchema })
    for (const count of [1, 9007199254740992, '1', 1.5, 1n << 63n, -(1n << 63n) - 1n]) {
      for (const params of [{ count }, { durations: [count] }, { input: { children: [{ count }] } }]) {
        await expect(transport.call('echo', params)).rejects.toThrow(/bigint|overflow/)
      }
    }
    expect(mockFetch).not.toHaveBeenCalled()
  })

  it('does not constrain Float or opaque JSON numbers to signed int64', async () => {
    mockFetch.mockReset()
    mockFetch.mockImplementation(async () => new Response('{"data":true}'))
    const transport = new FetchTransport('http://localhost/api')
    transport.setSchema({ echo: integerParamSchema })
    await transport.call('echo', { count: (1n << 63n) - 1n, durations: [-9223372036854775808n],
      input: { count: null, children: [] }, score: 1.5, json: { count: 1n << 100n },
    })
    expect(mockFetch.mock.lastCall?.[1].body).toBe('{"count":9223372036854775807,"durations":[-9223372036854775808],"input":{"count":null,"children":[]},"score":1.5,"json":{"count":1267650600228229401496703205376},"$api":"echo"}')
    await expect(transport.call('echo', { durations: null })).resolves.toBe(true)
    await expect(transport.call('echo')).resolves.toBe(true)
  })

  it('replaces validation when the schema changes and leaves schema-free JSON untyped', async () => {
    mockFetch.mockImplementation(async () => new Response('{"data":true}'))
    const transport = new FetchTransport('http://localhost/api')
    transport.setSchema({ echo: integerParamSchema })
    await expect(transport.call('echo', { count: 1 })).rejects.toThrow('bigint')
    transport.setSchema({ echo: { id: 1, params: [{ fieldID: 1, name: 'count', type: 'Float' }] } })
    await expect(transport.call('echo', { count: 1.5 })).resolves.toBe(true)
    transport.setSchema({})
    await expect(transport.call('echo', { count: 1n << 100n })).resolves.toBe(true)
  })
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

    mockFetch.mockResolvedValueOnce(new Response('{"data":{"id":1}}'))

    await t.call('getUser', { id: 1 })

    const [, init] = mockFetch.mock.calls[0]
    expect(new Headers(init.headers).get('authorization')).toBe('Bearer new-token')
  })

  it('setToken clears authorization header on logout', async () => {
    const t = new FetchTransport('http://localhost:8080/api', { token: 'old-token' })
    t.setToken(null)

    mockFetch.mockResolvedValueOnce(new Response('{"data":true}'))

    await t.call('health')

    const [, init] = mockFetch.mock.calls[0]
    expect(new Headers(init.headers).get('authorization')).toBeNull()
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
    t.setSchema({ getUser: { id: 1, fields: { id: { fieldID: 1 }, name: { fieldID: 2 } } } })
    mockFetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      arrayBuffer: async () => new ArrayBuffer(0),
    })

    await t.call('getUser', { $select: 'name' })

    const body = new Uint8Array(mockFetch.mock.calls[0][1].body as ArrayBuffer)
		expect(Array.from(body)).toEqual([1, 2, 1, 2, 0])
  })

  it('encodes nested selections recursively and rejects invalid paths', () => {
    const meta = {
      id: 1,
      fields: {
        id: { fieldID: 1 },
        posts: { fieldID: 3, typeName: 'Post' },
      },
      types: {
        Post: {
          id: { fieldID: 1 },
          title: { fieldID: 2 },
        },
      },
    }
    expect(Array.from(encodeBinaryBody(meta, { $select: 'id,posts{title}' }))).toEqual([
      1, 6, 1, 5, 3, 2, 1, 2, 0,
    ])
    expect(() => encodeBinaryBody(meta, { $select: 'missing' })).toThrow('unknown selected field')
    expect(() => encodeBinaryBody(meta, { $select: 'id{name}' })).toThrow('does not support nested selection')
    expect(() => encodeBinaryBody(meta, { $select: 'id,id' })).toThrow('duplicate field')
  })

  it('requires $select for structured responses only', () => {
    const structured = { id: 1, fields: { id: { fieldID: 1 } } }
    expect(() => encodeBinaryBody(structured)).toThrow('$select is required')
    expect(() => encodeBinaryBody(structured, { $select: '   ' })).toThrow('$select is required')
    expect(Array.from(encodeBinaryBody({ id: 2 }))).toEqual([2, 0, 0])
  })

  it('refreshes an expired token once in binary mode', async () => {
    const refresh = vi.fn().mockResolvedValue('fresh-token')
    const t = new FetchTransport('http://localhost:8080/api', {
      mode: 'binary',
      token: 'expired-token',
      onTokenExpired: refresh,
    })
    t.setSchema({ health: { id: 1 } })
    const unauthorized = new Encoder()
    unauthorized.writeFieldInt(1, 401n)
    unauthorized.writeFieldString(2, 'Unauthorized')
    unauthorized.writeFieldString(3, 'expired')
    unauthorized.writeEnd()
    mockFetch
      .mockResolvedValueOnce(new Response(new Uint8Array(unauthorized.bytes()), { status: 401 }))
      .mockResolvedValueOnce({ ok: true, status: 200, arrayBuffer: async () => new ArrayBuffer(0) })

    await t.call('health')

    expect(refresh).toHaveBeenCalledTimes(1)
    expect(mockFetch).toHaveBeenCalledTimes(2)
    expect(new Headers(mockFetch.mock.calls[1][1].headers).get('authorization')).toBe('Bearer fresh-token')
  })

  it('decodes the canonical binary error envelope', async () => {
	const t = new FetchTransport('http://localhost:8080/api', { mode: 'binary' })
	t.setSchema({ createUser: { id: 1 } })
	const body = new Uint8Array([
	  1, 0xa0, 0x06,
	  2, 10, 66, 97, 100, 82, 101, 113, 117, 101, 115, 116,
	  3, 3, 98, 97, 100,
	  4, 1, 116,
	  5, 2, 123, 125,
	  6, 1, 99,
	  0,
	])
	mockFetch.mockResolvedValueOnce({
	  ok: false,
	  status: 400,
	  arrayBuffer: async () => body.buffer,
	})

	await expect(t.call('createUser')).rejects.toMatchObject({
	  error: 'BadRequest',
	  code: 400,
	  message: 'bad',
	  traceId: 't',
	  data: {},
	  cause: 'c',
	})
  })
})

describe('FetchTransport jsonCall', () => {
  it('preserves full-width integers through raw HTTP request and response text', async () => {
    const value = 9223372036854775807n
    mockFetch.mockResolvedValueOnce(new Response(`{"data":{"value":${value}}}`))
    const transport = new FetchTransport('http://localhost/luvia')
    await expect(transport.call('echo', { value })).resolves.toEqual({ value })
    expect(mockFetch.mock.lastCall?.[1].body).toBe(`{"value":${value},"$api":"echo"}`)
  })

  it('preserves arbitrary error data without rounding numeric tokens', async () => {
    mockFetch.mockResolvedValueOnce(new Response(
      '{"error":"Conflict","code":409,"message":"bad","data":{"id":9223372036854775807,"ratio":1.25}}',
      { status: 409 },
    ))
    const transport = new FetchTransport('http://localhost/luvia')
    await expect(transport.call('echo')).rejects.toMatchObject({
      error: 'Conflict', code: 409, data: { id: 9223372036854775807n, ratio: { rawJSON: '1.25' } },
    })
  })

  it('rejects non-object JSON envelopes as protocol errors', async () => {
    const transport = new FetchTransport('http://localhost/luvia')
    for (const body of ['null', '[]', '1', '1.0', '"text"']) {
      mockFetch.mockResolvedValueOnce(new Response(body))
      await expect(transport.call('echo')).rejects.toMatchObject({ error: 'ParseError' })
    }
  })

  it('sends correct request format', async () => {
    const t = new FetchTransport('http://localhost:8080/api')

    mockFetch.mockResolvedValueOnce(new Response('{"data":{"id":1,"name":"Alice"}}'))

    const result = await t.call('getUser', { id: 1 })

    expect(mockFetch).toHaveBeenCalledTimes(1)
    const [url, init] = mockFetch.mock.calls[0]
    expect(url).toBe('http://localhost:8080/api')
    expect(init.method).toBe('POST')
    expect(new Headers(init.headers).get('content-type')).toBe('application/json')

    const body = JSON.parse(init.body)
    expect(body.$api).toBe('getUser')
    expect(body.id).toBe(1)

    expect(result).toEqual({ id: 1, name: 'Alice' })
  })

  it('sends request without params', async () => {
    const t = new FetchTransport('http://localhost:8080/api')

    mockFetch.mockResolvedValueOnce(new Response('{"data":[]}'))

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

    const request = t.call('getUser')
    await expect(request).rejects.toThrow(LuxoError)
    await expect(request).rejects.toMatchObject({ error: 'NetworkError', code: 0 })
  })

  it('throws LuxoError on invalid JSON response', async () => {
    const t = new FetchTransport('http://localhost:8080/api')

    mockFetch.mockResolvedValueOnce(new Response('{'))

    await expect(t.call('getUser')).rejects.toThrow(LuxoError)
    try {
      await t.call('getUser')
    } catch (e) {
      // Second call also throws NetworkError since fetch is not mocked again
    }
  })

  it('throws LuxoError on API error response', async () => {
    const t = new FetchTransport('http://localhost:8080/api')

    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({
        error: 'NotFound',
        code: 404,
        message: 'User not found',
        traceId: 'trace-123',
      }), { status: 404 }))

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
      arrayBuffer: () => new Promise((_, reject) => {
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
    mockFetch.mockResolvedValueOnce(new Response('{"data":true}'))

    await t.call('health')

    expect(now).not.toHaveBeenCalled()
  })

  it('emits safe metadata without JSON parameter values', async () => {
    const observer = vi.fn()
    const t = new FetchTransport('http://localhost:8080/api', { observer })
    mockFetch.mockResolvedValueOnce(new Response('{"data":true}'))

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
    const body = new Uint8Array([
      1, 0xa2, 0x06,
      2, 12, 85, 110, 97, 117, 116, 104, 111, 114, 105, 122, 101, 100,
      3, 19, 105, 110, 118, 97, 108, 105, 100, 32, 99, 114, 101, 100, 101, 110, 116, 105, 97, 108, 115,
      4, 10, 116, 114, 97, 99, 101, 45, 115, 97, 102, 101,
      0,
    ])
    mockFetch.mockResolvedValueOnce({
      ok: false,
      status: 401,
      arrayBuffer: async () => body.buffer,
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

  it('carries structured data and development cause', () => {
    const err = new LuxoError('BadRequest', 400, 'bad', 'trace', { param: 'email' }, 'validation failed')
    expect(err.data).toEqual({ param: 'email' })
    expect(err.cause).toBe('validation failed')
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

  it.each(['json', 'binary'] as const)('isolates %s decoder failures from peers, unary calls and repeated frames', async mode => {
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    transport.setSchema({ live: { id: 71 }, health: { id: 72 } })
    const onData = vi.fn(), onError = vi.fn(), onComplete = vi.fn(), peer = vi.fn()
    const decode = (value: unknown): number => {
      const number = value instanceof Uint8Array ? value[0] : value
      if (number !== 7) throw new Error('invalid sample')
      return number
    }
    try {
      const first = transport.subscribe('live', {}, decode, onData, { onError, onComplete })
      const second = transport.subscribe('live', {}, decode, peer)
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      for (const id of [1, 2]) socket.message(mode === 'json'
        ? `{"$sub":${id},"ok":true}` : sequenceFrame(0x13, BigInt(id), []).buffer)
      const [cancel, cancelPeer] = await Promise.all([first, second])
      const send = (id: number, value: number) => socket.message(mode === 'json'
        ? `{"$stream":${id},"data":${value}}` : sequenceFrame(0x12, BigInt(id), [value]).buffer)
      send(1, 7)
      expect(onData).toHaveBeenCalledExactlyOnceWith(7)
      expect(() => send(1, 9)).not.toThrow()
      expect(onError).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ error: 'DecodeError', code: 0, message: 'invalid sample' }))
      const sent = socket.sent.length
      expect(socket.sent.at(-1)).toEqual(mode === 'json' ? '{"$unsub":1}' : sequenceFrame(0x11, 1n, []).buffer)
      send(1, 9); send(1, 7); cancel()
      socket.message(mode === 'json' ? '{"$end":1,"ok":true}' : sequenceFrame(0x15, 1n, []).buffer)
      expect(socket.sent).toHaveLength(sent)
      expect(onData).toHaveBeenCalledTimes(1)
      expect(onError).toHaveBeenCalledTimes(1)
      expect(onComplete).not.toHaveBeenCalled()
      send(2, 7)
      expect(peer).toHaveBeenCalledExactlyOnceWith(7)
      const call = transport.call('health')
      await Promise.resolve()
      socket.message(mode === 'json' ? '{"$id":3,"data":true}' : sequenceFrame(2, 3n, [1]).buffer)
      await expect(call).resolves.toEqual(mode === 'json' ? true : new Uint8Array([1]))
      cancelPeer()
    } finally { transport.close() }
  })

  it.each(['json', 'binary'] as const)('does not mistake a %s business callback exception for a decoding error', async mode => {
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    transport.setSchema({ live: { id: 71 } })
    const failure = new Error('application failed'), onError = vi.fn(), onComplete = vi.fn()
    const onData = vi.fn(() => { throw failure })
    try {
      const pending = transport.subscribe('live', {}, value => value, onData, { onError, onComplete })
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      socket.message(mode === 'json' ? '{"$sub":1,"ok":true}' : sequenceFrame(0x13, 1n, []).buffer)
      const cancel = await pending
      const data = mode === 'json' ? '{"$stream":1,"data":7}' : sequenceFrame(0x12, 1n, [7]).buffer
      expect(() => socket.message(data)).toThrow(failure)
      expect(() => socket.message(data)).toThrow(failure)
      expect(onData).toHaveBeenCalledTimes(2)
      expect(onError).not.toHaveBeenCalled()
      expect(socket.sent).toHaveLength(1)
      socket.message(mode === 'json' ? '{"$end":1,"ok":true}' : sequenceFrame(0x15, 1n, []).buffer)
      expect(onComplete).toHaveBeenCalledOnce()
      cancel()
    } finally { transport.close() }
  })

  it.each(['json', 'binary'] as const)('retires %s decoding errors before reentrant or throwing observers', async mode => {
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    transport.setSchema({ live: { id: 71 } })
    const failure = new Error('observer failed'), onData = vi.fn(), decode = vi.fn(() => { throw 'invalid value' })
    const onError = vi.fn(() => { cancel(); throw failure })
    let cancel = () => {}
    try {
      const pending = transport.subscribe('live', {}, decode, onData, { onError })
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      const data = mode === 'json' ? '{"$stream":1,"data":7}' : sequenceFrame(0x12, 1n, [7]).buffer
      socket.message(data)
      expect(decode).not.toHaveBeenCalled()
      socket.message(mode === 'json' ? '{"$sub":1,"ok":true}' : sequenceFrame(0x13, 1n, []).buffer)
      cancel = await pending
      expect(() => socket.message(data)).toThrow(failure)
      expect(onError).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ error: 'DecodeError', message: 'invalid subscription response' }))
      expect(() => socket.message(data)).not.toThrow()
      expect(decode).toHaveBeenCalledTimes(1)
      expect(onData).not.toHaveBeenCalled()
      expect(socket.sent).toHaveLength(2)
    } finally { transport.close() }
  })

  it.each(['json', 'binary'] as const)('keeps %s local decode retirement when cancellation sending fails or no observer exists', async mode => {
    for (const observe of [false, true]) {
      const transport = new WsTransport('ws://localhost/luvia', { mode })
      transport.setSchema({ live: { id: 71 } })
      const onError = vi.fn(), decode = vi.fn(() => { throw new LuxoError('ParseError', 99, 'bad model') })
      try {
        const pending = transport.subscribe('live', {}, decode, vi.fn(), observe ? { onError } : undefined)
        const socket = FakeWebSocket.instances.at(-1)!
        socket.open()
        await Promise.resolve()
        socket.message(mode === 'json' ? '{"$sub":1,"ok":true}' : sequenceFrame(0x13, 1n, []).buffer)
        const cancel = await pending
        socket.send = () => { throw new Error('send failed') }
        const data = mode === 'json' ? '{"$stream":1,"data":7}' : sequenceFrame(0x12, 1n, [7]).buffer
        expect(() => socket.message(data)).not.toThrow()
        expect(() => socket.message(data)).not.toThrow()
        expect(decode).toHaveBeenCalledTimes(1)
        expect(onError).toHaveBeenCalledTimes(observe ? 1 : 0)
        if (observe) expect(onError).toHaveBeenCalledWith(expect.objectContaining({ error: 'DecodeError', code: 0, message: 'bad model' }))
        cancel()
      } finally { transport.close() }
    }
  })

  it.each(['json', 'binary'] as const)('does not double-notify after a %s decoder closes its connection', async mode => {
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    transport.setSchema({ live: { id: 71 } })
    const onError = vi.fn()
    try {
      const pending = transport.subscribe('live', {}, () => { transport.close(); throw new Error('closed') }, vi.fn(), { onError })
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      socket.message(mode === 'json' ? '{"$sub":1,"ok":true}' : sequenceFrame(0x13, 1n, []).buffer)
      const cancel = await pending
      socket.message(mode === 'json' ? '{"$stream":1,"data":7}' : sequenceFrame(0x12, 1n, [7]).buffer)
      expect(onError).not.toHaveBeenCalled()
      cancel()
    } finally { transport.close() }
  })

  it.each(['json', 'binary'] as const)('notifies %s completion once and removes only its subscription', async mode => {
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    transport.setSchema({ live: { id: 71 } })
    const seen: string[] = [], peer = vi.fn(), onError = vi.fn()
    try {
      const first = transport.subscribe('live', {}, (value: unknown) => value, () => seen.push('data'), {
        onComplete: () => seen.push('complete'), onError,
      })
      const second = transport.subscribe('live', {}, (value: unknown) => value, peer)
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      for (const id of [1, 2]) socket.message(mode === 'json'
        ? `{"$sub":${id},"ok":true}` : sequenceFrame(0x13, BigInt(id), []).buffer)
      const [cancel, cancelPeer] = await Promise.all([first, second])
      const data = mode === 'json' ? '{"$stream":1,"data":7}' : sequenceFrame(0x12, 1n, [14]).buffer
      const end = mode === 'json' ? '{"$end":1,"ok":true}' : sequenceFrame(0x15, 1n, []).buffer
      socket.message(data)
      socket.message(end)
      socket.message(end)
      socket.message(data)
      const sent = socket.sent.length
      cancel()
      expect(socket.sent).toHaveLength(sent)
      expect(seen).toEqual(['data', 'complete'])
      expect(onError).not.toHaveBeenCalled()
      socket.message(mode === 'json' ? '{"$stream":2,"data":8}' : sequenceFrame(0x12, 2n, [16]).buffer)
      expect(peer).toHaveBeenCalledOnce()
      cancelPeer()
    } finally { transport.close() }
  })

  it.each(['json', 'binary'] as const)('delivers %s terminal error data without losing numbers', async mode => {
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    transport.setSchema({ live: { id: 71 } })
    const onData = vi.fn(), onError = vi.fn(), onComplete = vi.fn()
    const payload = '{"large":123456789012345678901234567890,"fraction":0.1234567890123456789,"zero":-0}'
    try {
      const pending = transport.subscribe('live', {}, (value: unknown) => value, onData, { onError, onComplete })
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      socket.message(mode === 'json' ? '{"$sub":1,"ok":true}' : sequenceFrame(0x13, 1n, []).buffer)
      const cancel = await pending
      const encoder = new Encoder()
      encoder.writeFieldInt(1, 422n)
      encoder.writeFieldString(2, 'Rejected')
      encoder.writeFieldString(3, 'failed')
      encoder.writeFieldBytes(5, new TextEncoder().encode(payload))
      encoder.writeEnd()
      const end = mode === 'json'
        ? `{"$end":1,"error":"Rejected","code":422,"message":"failed","data":${payload}}`
        : sequenceFrame(0x16, 1n, [...encoder.bytes()]).buffer
      socket.message(end)
      socket.message(end)
      expect(onError).toHaveBeenCalledOnce()
      const error: unknown = onError.mock.calls[0][0]
      expect(error).toBeInstanceOf(LuxoError)
      expect(error).toMatchObject({ error: 'Rejected', code: 422, data: decodeLuxoJSON(payload) })
      expect(onData).not.toHaveBeenCalled()
      expect(onComplete).not.toHaveBeenCalled()
      const sent = socket.sent.length
      cancel()
      expect(socket.sent).toHaveLength(sent)
    } finally { transport.close() }
  })

  it.each(['json', 'binary'] as const)('rejects %s terminal frames arriving before acknowledgement', async mode => {
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    transport.setSchema({ live: { id: 71 } })
    const onComplete = vi.fn(), onError = vi.fn()
    try {
      const pending = transport.subscribe('live', {}, (value: unknown) => value, () => {}, { onComplete, onError })
      const rejected = expect(pending).rejects.toMatchObject({ error: 'ParseError' })
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      socket.message(mode === 'json' ? '{"$end":1,"ok":true}' : sequenceFrame(0x15, 1n, []).buffer)
      await rejected
      expect(onComplete).not.toHaveBeenCalled()
      expect(onError).not.toHaveBeenCalled()
    } finally { transport.close() }
  })

  it.each(['json', 'binary'] as const)('rejects malformed %s completion without notifying success', async mode => {
    const frames = mode === 'json'
      ? ['{"$end":1,"ok":false}', '{"$end":1,"ok":true,"data":null}', '{"$end":1,"error":"bad"}']
      : [sequenceFrame(0x15, 1n, [0]).buffer, sequenceFrame(0x16, 1n, []).buffer]
    for (const frame of frames) {
      const transport = new WsTransport('ws://localhost/luvia', { mode })
      transport.setSchema({ live: { id: 71 } })
      const onError = vi.fn(), onComplete = vi.fn()
      try {
        const pending = transport.subscribe('live', {}, (value: unknown) => value, () => {}, { onError, onComplete })
        const socket = FakeWebSocket.instances.at(-1)!
        socket.open()
        await Promise.resolve()
        socket.message(mode === 'json' ? '{"$sub":1,"ok":true}' : sequenceFrame(0x13, 1n, []).buffer)
        await pending
        socket.message(frame)
        expect(onComplete).not.toHaveBeenCalled()
        expect(onError).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ error: 'ParseError' }))
      } finally { transport.close() }
    }
  })

  it.each(['json', 'binary'] as const)('preserves full-width %s terminal identity and ignores mismatched frames', async mode => {
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    transport.setSchema({ live: { id: 71 } })
    Reflect.set(transport, 'seq', (1n << 64n) - 2n)
    const onComplete = vi.fn()
    try {
      const pending = transport.subscribe('live', {}, (value: unknown) => value, () => {}, { onComplete })
      const socket = FakeWebSocket.instances.at(-1)!
      socket.open()
      await Promise.resolve()
      const max = (1n << 64n) - 1n
      socket.message(mode === 'json' ? `{"$sub":${max},"ok":true}` : sequenceFrame(0x13, max, []).buffer)
      await pending
      socket.message(mode === 'json' ? sequenceFrame(0x15, max, []).buffer : `{"$end":${max},"ok":true}`)
      socket.message(mode === 'json' ? '{"$end":-0,"ok":true}' : new Uint8Array([0x15, 0x80]).buffer)
      socket.message(mode === 'json' ? '{"$end":0,"ok":true}' : sequenceFrame(0x15, 0n, []).buffer)
      expect(onComplete).not.toHaveBeenCalled()
      socket.message(mode === 'json' ? `{"$end":${max},"ok":true}` : sequenceFrame(0x15, max, []).buffer)
      expect(onComplete).toHaveBeenCalledOnce()
    } finally { transport.close() }
  })

  it.each(['json', 'binary'] as const)('cleans %s subscriptions before invoking a throwing terminal callback', async mode => {
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    transport.setSchema({ live: { id: 71 } })
    const failure = new Error('callback failure')
    try {
      const pending = transport.subscribe('live', {}, (value: unknown) => value, () => {}, { onComplete: () => { throw failure } })
      const socket = FakeWebSocket.instances.at(-1)!
      socket.open()
      await Promise.resolve()
      socket.message(mode === 'json' ? '{"$sub":1,"ok":true}' : sequenceFrame(0x13, 1n, []).buffer)
      const cancel = await pending
      const end = mode === 'json' ? '{"$end":1,"ok":true}' : sequenceFrame(0x15, 1n, []).buffer
      expect(() => socket.message(end)).toThrow(failure)
      expect(() => socket.message(end)).not.toThrow()
      const sent = socket.sent.length
      cancel()
      expect(socket.sent).toHaveLength(sent)
    } finally { transport.close() }
  })

  it.each(['json', 'binary'] as const)('isolates concurrent %s subscriptions to the same API', async mode => {
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    transport.setSchema({ live: { id: 71 } })
    const first = vi.fn(), second = vi.fn()
    try {
      const a = transport.subscribe('live', {}, (value: unknown) => value, first)
      const b = transport.subscribe('live', {}, (value: unknown) => value, second)
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      if (mode === 'json') {
        expect(socket.sent.map(value => JSON.parse(value as string))).toEqual([
          { $id: 1, $sub: 'live' }, { $id: 2, $sub: 'live' },
        ])
        socket.message('{"$sub":2,"ok":true}')
        socket.message('{"$sub":1,"ok":true}')
      } else {
        expect(socket.sent.map(value => [...new Uint8Array(value as ArrayBuffer)])).toEqual([
          [0x10, 1, 71, 0, 0], [0x10, 2, 71, 0, 0],
        ])
        socket.message(sequenceFrame(0x13, 2n, []).buffer)
        socket.message(sequenceFrame(0x13, 1n, []).buffer)
      }
      const [cancelA, cancelB] = await Promise.all([a, b])
      cancelA()
      const sent = socket.sent.length
      cancelA()
      expect(socket.sent).toHaveLength(sent)
      if (mode === 'json') {
        socket.message('{"$stream":1,"data":11}')
        socket.message('{"$stream":2,"data":22}')
      } else {
        socket.message(sequenceFrame(0x12, 1n, [11]).buffer)
        socket.message(sequenceFrame(0x12, 2n, [22]).buffer)
      }
      expect(first).not.toHaveBeenCalled()
      expect(second).toHaveBeenCalledExactlyOnceWith(mode === 'json' ? 22 : new Uint8Array([22]))
      cancelB()
    } finally { transport.close() }
  })

  it.each(['json', 'binary'] as const)('preserves the full %s subscription ID and refuses wraparound', async mode => {
    const max = (1n << 64n) - 1n
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    transport.setSchema({ live: { id: 71 } })
    Reflect.set(transport, 'seq', max - 1n)
    const listener = vi.fn()
    try {
      const pending = transport.subscribe('live', {}, (value: unknown) => value, listener)
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      if (mode === 'json') {
        expect(socket.sent[0]).toBe(`{"$id":${max},"$sub":"live"}`)
        socket.message(`{"$sub":${max},"ok":true}`)
      } else {
        expect(new Uint8Array(socket.sent[0] as ArrayBuffer)).toEqual(sequenceFrame(0x10, max, [71, 0, 0]))
        socket.message(sequenceFrame(0x13, max, []).buffer)
      }
      const cancel = await pending
      await expect(transport.subscribe('live', {}, (value: unknown) => value, vi.fn())).rejects.toMatchObject({ error: 'RequestIDExhausted' })
      expect(socket.sent).toHaveLength(1)
      if (mode === 'json') socket.message(`{"$stream":${max},"data":42}`)
      else socket.message(sequenceFrame(0x12, max, [42]).buffer)
      expect(listener).toHaveBeenCalledOnce()
      cancel()
      if (mode === 'json') expect(socket.sent[1]).toBe(`{"$unsub":${max}}`)
      else expect(new Uint8Array(socket.sent[1] as ArrayBuffer)).toEqual(sequenceFrame(0x11, max, []))
    } finally { transport.close() }
  })

  it.each(['json', 'binary'] as const)('ignores malformed and retired %s subscription identities', async mode => {
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    transport.setSchema({ live: { id: 71 } })
    const listener = vi.fn()
    let acknowledged = false
    try {
      const pending = (async () => {
        const cancel = await transport.subscribe('live', {}, (value: unknown) => value, listener)
        acknowledged = true
        return cancel
      })()
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      if (mode === 'json') {
        for (const id of ['"live"', 'true', 'null', '-1', '-0', '1.0', '1e0', '18446744073709551616', '[]', '{}']) {
          socket.message(`{"$sub":${id},"ok":true}`)
          socket.message(`{"$stream":${id},"data":42}`)
        }
        socket.message('{"$stream":1,"data":42}') // Data cannot precede ACK.
      } else {
        for (const kind of [0x12, 0x13]) {
          for (const tail of [[], [128], [...Array<number>(9).fill(128), 2]]) {
            socket.message(new Uint8Array([kind, ...tail]).buffer)
          }
        }
        socket.message(new Uint8Array([0x07, 1]).buffer)
        socket.message(new Uint8Array([0x06, 1, 42]).buffer)
        socket.message(new Uint8Array([0x12, 1, 42]).buffer)
      }
      await Promise.resolve()
      expect(acknowledged).toBe(false)
      expect(listener).not.toHaveBeenCalled()
      if (mode === 'json') socket.message('{"$sub":1,"ok":true}')
      else socket.message(new Uint8Array([0x13, 1]).buffer)
      const cancel = await pending
      cancel()
    } finally { transport.close() }
  })

  it('isolates replacement connections from late callbacks on the old socket', async () => {
    vi.useFakeTimers()
    const transport = new WsTransport('ws://localhost/luvia')
    const listener = vi.fn()
    try {
      const onError = vi.fn()
      const pending = transport.subscribe('live', {}, (value: unknown) => value, listener, { onError })
      const first = FakeWebSocket.instances[0]
      first.open()
      await Promise.resolve()
      first.message('{"$sub":1,"ok":true}')
      const cancel = await pending
      first.close()
      await vi.advanceTimersByTimeAsync(1000)
      const second = FakeWebSocket.instances[1]
      second.open()
      expect(second.sent).toEqual([])
      expect(onError).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ error: 'NetworkError' }))
      first.message('{"$stream":1,"data":11}')
      first.close()
      second.message('{"$stream":1,"data":22}')
      expect(listener).not.toHaveBeenCalled()
      cancel()
      expect(second.sent).toEqual([])
      const next = transport.subscribe('live', {}, (value: unknown) => value, listener)
      await Promise.resolve()
      second.message('{"$sub":2,"ok":true}')
      const cancelNext = await next
      second.message('{"$stream":2,"data":33}')
      expect(listener).toHaveBeenCalledExactlyOnceWith(33)
      cancelNext()
    } finally {
      transport.close()
      vi.clearAllTimers()
      vi.useRealTimers()
    }
  })

  it.each(['json', 'binary'] as const)('does not reject a new %s subscription with a retired error', async mode => {
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    transport.setSchema({ live: { id: 71 } })
    try {
      const first = transport.subscribe('live', {}, (value: unknown) => value, vi.fn())
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      if (mode === 'json') socket.message('{"$sub":1,"ok":true}')
      else socket.message(sequenceFrame(0x13, 1n, []).buffer)
      const oldCancel = await first
      oldCancel()
      const second = transport.subscribe('live', {}, (value: unknown) => value, vi.fn())
      await Promise.resolve()
      if (mode === 'json') socket.message('{"$sub":1,"error":"Old","code":400,"message":"old"}')
      else {
        const enc = new Encoder()
        enc.writeFieldInt(1, 400n)
        enc.writeFieldString(2, 'Old')
        enc.writeFieldString(3, 'old')
        enc.writeEnd()
        socket.message(sequenceFrame(0x14, 1n, [...enc.bytes()]).buffer)
      }
      oldCancel()
      if (mode === 'json') socket.message('{"$sub":2,"ok":true}')
      else socket.message(sequenceFrame(0x13, 2n, []).buffer)
      const cancel = await second
      cancel()
    } finally { transport.close() }
  })

  it('rejects a subscription closed while its socket is still connecting', async () => {
    const transport = new WsTransport('ws://localhost/luvia')
    const pending = transport.subscribe('live', {}, (value: unknown) => value, vi.fn())
    transport.close()
    await expect(pending).rejects.toMatchObject({ error: 'NetworkError' })
    await expect(transport.subscribe('live', {}, (value: unknown) => value, vi.fn())).rejects.toMatchObject({ error: 'NetworkError' })
    expect(FakeWebSocket.instances).toHaveLength(1)
  })

  it.each(['json', 'binary'] as const)('keeps %s subscription mode after transport configuration changes', async mode => {
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    transport.setSchema({ live: { id: 71 } })
    const listener = vi.fn()
    try {
      const pending = transport.subscribe('live', {}, (value: unknown) => value, listener)
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      transport.setMode(mode === 'json' ? 'binary' : 'json')
      if (mode === 'json') socket.message('{"$sub":1,"ok":true}')
      else socket.message(new Uint8Array([0x13, 1]).buffer)
      const cancel = await pending
      if (mode === 'json') socket.message('{"$stream":1,"data":22}')
      else socket.message(new Uint8Array([0x12, 1, 22]).buffer)
      expect(listener).toHaveBeenCalledOnce()
      cancel()
      if (mode === 'json') expect(socket.sent[1]).toBe('{"$unsub":1}')
      else expect(new Uint8Array(socket.sent[1] as ArrayBuffer)).toEqual(new Uint8Array([0x11, 1]))
    } finally { transport.close() }
  })

  it.each(['json', 'binary'] as const)('reports malformed %s call/subscription errors without timing out', async mode => {
    for (const subscription of [false, true]) {
      for (const invalid of [false, null, [], '']) {
        const transport = new WsTransport('ws://localhost/luvia', { mode, timeout: 50 })
        transport.setSchema({ echo: { id: 7 } })
        try {
          const request = subscription ? transport.subscribe('echo', {}, (value: unknown) => value, () => {}) : transport.call('echo')
          const socket = FakeWebSocket.instances.at(-1)!
          socket.open()
          await Promise.resolve()
          if (mode === 'json') {
            socket.message(JSON.stringify({ ...(subscription ? { $sub: 1 } : { $id: 1 }),
              error: invalid, code: false, message: 'bad' }))
          } else {
            const encoder = new Encoder()
            encoder.writeFieldInt(1, 400n)
            encoder.writeFieldBytes(2, new Uint8Array([255]))
            encoder.writeFieldString(3, 'bad')
            encoder.writeEnd()
            socket.message(new Uint8Array([
              subscription ? 0x14 : 3, 1, ...encoder.bytes(),
            ]).buffer)
          }
          await expect(request).rejects.toMatchObject({ error: 'ParseError', code: 0 })
        } finally {
          transport.close()
        }
      }
    }
  })

  it('rejects invalid JSON call and subscription integers and cleans pending state', async () => {
    const transport = new WsTransport('ws://localhost/luvia', { timeout: 100 })
    transport.setSchema({ echo: integerParamSchema })
    try {
      const first = transport.call('echo', { count: 1 })
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await expect(first).rejects.toThrow('bigint')
      for (const count of [1, 1n << 63n]) {
        await expect(transport.call('echo', { input: { count } })).rejects.toThrow(/bigint|overflow/)
        await expect(transport.subscribe('echo', { durations: [count] }, (value: unknown) => value, () => {})).rejects.toThrow(/bigint|overflow/)
      }
      expect(socket.sent).toEqual([])
      const subscribed = transport.subscribe('echo', { count: 9223372036854775807n }, (value: unknown) => value, () => {})
      await Promise.resolve()
      expect(socket.sent[0]).toBe('{"count":9223372036854775807,"$id":6,"$sub":"echo"}')
      socket.message('{"$sub":6,"ok":true}')
      const unsubscribe = await subscribed
      unsubscribe()
    } finally {
      transport.close()
    }
  })

  it('subscribes, dispatches JSON stream data, and unsubscribes', async () => {
    const transport = new WsTransport('ws://localhost/luvia', { token: 'secret' })
    const listener = vi.fn()
    const subscribed = transport.subscribe('liveAlerts', { projectId: 7 }, (value: unknown) => value, listener)
    const socket = FakeWebSocket.instances[0]

    expect(socket.url).toBe('ws://localhost/luvia?token=secret')
    socket.open()
    await Promise.resolve()
    socket.message(JSON.stringify({ $sub: 1, ok: true }))
    const unsubscribe = await subscribed

    expect(JSON.parse(socket.sent[0] as string)).toEqual({ $id: 1, $sub: 'liveAlerts', projectId: 7 })
    socket.message(JSON.stringify({ $stream: 1, data: { id: 9 } }))
    expect(listener).toHaveBeenCalledWith({ id: 9 })

    unsubscribe()
    expect(JSON.parse(socket.sent[1] as string)).toEqual({ $unsub: 1 })
    transport.close()
  })

  it('preserves full-width JSON stream parameters and event values', async () => {
    const value = -9223372036854775808n
    const transport = new WsTransport('ws://localhost/luvia')
    const listener = vi.fn()
    try {
      const pending = transport.subscribe('live', { value }, (value: unknown) => value, listener)
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      socket.message('{"$sub":1,"ok":true}')
      const unsubscribe = await pending
      expect(socket.sent[0]).toBe(`{"value":${value},"$id":1,"$sub":"live"}`)
      socket.message(`{"$stream":1,"data":{"value":${value}}}`)
      expect(listener).toHaveBeenCalledWith({ value })
      unsubscribe()
    } finally {
      transport.close()
    }
  })

  it('subscribes and dispatches binary stream payloads', async () => {
    const transport = new WsTransport('ws://localhost/luvia', { mode: 'binary' })
    transport.setSchema({ liveTraces: { id: 12, params: [{ fieldID: 1, name: 'projectId', type: 'Int' }] } })
    const listener = vi.fn()
    const subscribed = transport.subscribe('liveTraces', { projectId: 7n }, (value: unknown) => value, listener)
    const socket = FakeWebSocket.instances[0]
    socket.open()
    await Promise.resolve()
    socket.message(new Uint8Array([0x13, 1]).buffer)
    const unsubscribe = await subscribed

	expect(Array.from(new Uint8Array(socket.sent[0] as ArrayBuffer))).toEqual([0x10, 1, 12, 0, 1, 14, 0])
	socket.message(new Uint8Array([0x12, 1, 9, 0]).buffer)
    expect(Array.from(listener.mock.calls[0][0] as Uint8Array)).toEqual([9, 0])

    unsubscribe()
	expect(Array.from(new Uint8Array(socket.sent[1] as ArrayBuffer))).toEqual([0x11, 1])
    transport.close()
  })

  it('rejects a JSON subscription when the server rejects it', async () => {
    const transport = new WsTransport('ws://localhost/luvia')
    const subscribed = transport.subscribe('getUser', {}, (value: unknown) => value, vi.fn())
    const socket = FakeWebSocket.instances[0]
    socket.open()
    await Promise.resolve()
    socket.message(JSON.stringify({
      $sub: 1,
      error: 'BadRequest',
      code: 400,
      message: 'getUser is not a stream API',
    }))

    await expect(subscribed).rejects.toMatchObject({ error: 'BadRequest', code: 400 })
    transport.close()
  })

  it('rejects a binary subscription when the server rejects it', async () => {
    const transport = new WsTransport('ws://localhost/luvia', { mode: 'binary' })
    transport.setSchema({ getUser: { id: 10 } })
    const subscribed = transport.subscribe('getUser', {}, (value: unknown) => value, vi.fn())
    const socket = FakeWebSocket.instances[0]
    socket.open()
    await Promise.resolve()
    socket.message(new Uint8Array([
      0x14, 1,
      1, 0xa0, 0x06,
      2, 10, 66, 97, 100, 82, 101, 113, 117, 101, 115, 116,
      3, 3, 98, 97, 100,
      0,
    ]).buffer)

    await expect(subscribed).rejects.toMatchObject({ error: 'BadRequest', code: 400 })
    transport.close()
  })

  it('rejects subscriptions on a non-WebSocket transport', async () => {
    const transport = new FetchTransport('http://localhost/luvia')
    await expect(transport.subscribe('liveAlerts', {}, (value: unknown) => value, vi.fn())).rejects.toMatchObject({
      error: 'ConfigError',
    })
  })

  it('times out when a subscription acknowledgement never arrives', async () => {
    const transport = new WsTransport('ws://localhost/luvia', { timeout: 5 })
    const subscribed = transport.subscribe('liveAlerts', {}, (value: unknown) => value, vi.fn())
    const socket = FakeWebSocket.instances[0]
    socket.open()

    await expect(subscribed).rejects.toMatchObject({ error: 'TimeoutError' })
    transport.close()
  })
})

describe('WsTransport disconnect contract', () => {
  beforeEach(() => {
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket)
    vi.useFakeTimers()
  })
  afterEach(() => { vi.clearAllTimers(); vi.useRealTimers() })

  function create(mode: TransportMode = 'json'): WsTransport {
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    transport.setSchema({ live: { id: 71 } })
    return transport
  }

  function ack(socket: FakeWebSocket, mode: TransportMode, id: number): void {
    socket.message(mode === 'json' ? `{"$sub":${id},"ok":true}` : sequenceFrame(0x13, BigInt(id), []).buffer)
  }

  it.each(['json', 'binary'] as const)('rejects %s pending work and notifies established subscriptions exactly once', async mode => {
    for (const event of ['error', 'close', 'parse'] as const) {
      const transport = create(mode)
      const onError = vi.fn(), onPendingError = vi.fn(), onComplete = vi.fn()
      try {
        const first = transport.subscribe('live', {}, (value: unknown) => value, vi.fn(), { onError, onComplete })
        const second = transport.subscribe('live', {}, (value: unknown) => value, vi.fn(), { onError, onComplete })
        const socket = FakeWebSocket.instances.at(-1)!
        socket.open()
        await Promise.resolve()
        ack(socket, mode, 1); ack(socket, mode, 2)
        const cancels = await Promise.all([first, second])
        const results: unknown[] = []
        const request = transport.call('live').catch(error => { results.push(error) })
        const setup = transport.subscribe('live', {}, (value: unknown) => value, vi.fn(), { onError: onPendingError }).catch(error => { results.push(error) })
        await Promise.resolve()
        if (event === 'error') socket.onerror?.()
        else if (event === 'parse') socket.message('{"$id":3,"data":')
        else socket.close()
        await vi.advanceTimersByTimeAsync(0)
        expect(results).toHaveLength(2)
        await Promise.all([request, setup])
        const error = event === 'parse' ? 'ParseError' : 'NetworkError'
        expect(results).toEqual([expect.objectContaining({ error }), expect.objectContaining({ error })])
        expect(onError).toHaveBeenCalledTimes(2)
        expect(onError).toHaveBeenCalledWith(expect.objectContaining({ error }))
        expect(onPendingError).not.toHaveBeenCalled()
        expect(onComplete).not.toHaveBeenCalled()
        socket.onerror?.(); socket.close()
        cancels.forEach(cancel => cancel())
        expect(onError).toHaveBeenCalledTimes(2)
        expect(vi.getTimerCount()).toBe(1)
      } finally { transport.close() }
      expect(vi.getTimerCount()).toBe(0)
    }
  })

  it.each(['json', 'binary'] as const)('closes %s pending requests even when the browser close event is delayed', async mode => {
    const transport = create(mode)
    const onError = vi.fn(), onComplete = vi.fn()
    try {
      const established = transport.subscribe('live', {}, (value: unknown) => value, vi.fn(), { onError, onComplete })
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      ack(socket, mode, 1)
      await established
      const results: unknown[] = []
      const call = transport.call('live').catch(error => { results.push(error) })
      const pending = transport.subscribe('live', {}, (value: unknown) => value, vi.fn(), { onError }).catch(error => { results.push(error) })
      await Promise.resolve()
      vi.spyOn(socket, 'close').mockImplementation(() => { socket.readyState = 3 })
      transport.close()
      await vi.advanceTimersByTimeAsync(0)
      expect(results).toHaveLength(2)
      await Promise.all([call, pending])
      expect(results.every(error => error instanceof LuxoError && error.error === 'NetworkError')).toBe(true)
      socket.onclose?.()
      expect(onError).not.toHaveBeenCalled()
      expect(onComplete).not.toHaveBeenCalled()
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close() }
  })

  it('rejects a connecting request on explicit close without waiting for a browser event', async () => {
    const transport = create()
    let failure: unknown
    const pending = transport.subscribe('live', {}, (value: unknown) => value, vi.fn()).catch(error => { failure = error })
    const socket = FakeWebSocket.instances[0]
    vi.spyOn(socket, 'close').mockImplementation(() => { socket.readyState = 3 })
    transport.close()
    await vi.advanceTimersByTimeAsync(0)
    expect(failure).toMatchObject({ error: 'NetworkError' })
    await pending
    socket.open()
    expect(socket.readyState).toBe(3)
    expect(vi.getTimerCount()).toBe(0)
  })

  it.each(['close', 'parse'])('notifies all retired subscriptions after %s even if callbacks close the transport or throw', async event => {
    const transport = create()
    const failures = [undefined, new Error('second callback')]
    const seen: number[] = []
    try {
      const subscriptions = [0, 1, 2].map(index => transport.subscribe('live', {}, (value: unknown) => value, vi.fn(), {
        onError: error => {
          expect(error.error).toBe(event === 'parse' ? 'ParseError' : 'NetworkError')
          seen.push(index)
          transport.close()
          if (index < failures.length) throw failures[index]
        },
      }))
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      for (const id of [1, 2, 3]) ack(socket, 'json', id)
      await Promise.all(subscriptions)
      let thrown: unknown
      try {
        if (event === 'parse') socket.message('{')
        else socket.close()
      } catch (error) { thrown = error }
      expect(socket.readyState).toBe(3)
      expect(seen).toEqual([0, 1, 2])
      expect(thrown).toBeInstanceOf(AggregateError)
      expect((thrown as AggregateError).errors).toEqual(failures)
      expect(vi.getTimerCount()).toBe(0)
      socket.onclose?.()
      expect(seen).toEqual([0, 1, 2])
    } finally { transport.close() }
  })

  it.each(['close', 'parse'])('preserves a throwing %s callback and permits an explicit subscription from another callback', async event => {
    const transport = create()
    const failure = new Error('observer failure')
    let replacement: Promise<() => void> | undefined
    try {
      const first = transport.subscribe('live', {}, (value: unknown) => value, vi.fn(), { onError: () => { throw failure } })
      const second = transport.subscribe('live', {}, (value: unknown) => value, vi.fn(), {
        onError: () => { replacement = transport.subscribe('live', {}, (value: unknown) => value, vi.fn()) },
      })
      const old = FakeWebSocket.instances[0]
      old.open()
      await Promise.resolve()
      ack(old, 'json', 1); ack(old, 'json', 2)
      const cancels = await Promise.all([first, second])
      expect(() => event === 'parse' ? old.message('{') : old.close()).toThrow(failure)
      const next = FakeWebSocket.instances[1]
      next.open()
      await Promise.resolve()
      expect(next.sent).toEqual(['{"$id":3,"$sub":"live"}'])
      old.message('{'); old.close()
      expect(next.readyState).toBe(1)
      cancels.forEach(cancel => cancel())
      ack(next, 'json', 3)
      expect(replacement).toBeDefined()
      ;(await replacement!)()
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close() }
  })

  it.each(['', '[1]', 'null', '1', '{"$id":1} trailing', '{"$id":1,"data":"\\uD800"}'])('closes malformed JSON %j and reconnects without replay', async body => {
    const transport = create(), onError = vi.fn()
    try {
      const setup = transport.subscribe('live', {}, (value: unknown) => value, vi.fn(), { onError })
      const old = FakeWebSocket.instances.at(-1)!
      old.open()
      await Promise.resolve()
      ack(old, 'json', 1)
      const cancel = await setup
      old.message(body)
      expect(old.readyState).toBe(3)
      expect(onError).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ error: 'ParseError' }))
      await vi.advanceTimersByTimeAsync(1000)
      const next = FakeWebSocket.instances.at(-1)!
      expect(next).not.toBe(old)
      next.open()
      await Promise.resolve()
      expect(next.sent).toEqual([])
      cancel(); old.message('{'); old.close()
      const call = transport.call('live')
      await Promise.resolve()
      next.message('{"$id":2,"data":true}')
      await expect(call).resolves.toBe(true)
      expect(onError).toHaveBeenCalledOnce()
    } finally { transport.close() }
  })

  it('does not keep a rejected connection promise after socket construction fails', async () => {
    const transport = create()
    vi.stubGlobal('WebSocket', class {
      static readonly OPEN = 1
      constructor() { throw new Error('construct failed') }
    })
    try {
      await expect(transport.subscribe('live', {}, (value: unknown) => value, vi.fn())).rejects.toThrow('construct failed')
      vi.stubGlobal('WebSocket', FakeWebSocket)
      const retry = transport.subscribe('live', {}, (value: unknown) => value, vi.fn())
      void retry.catch(() => {})
      expect(FakeWebSocket.instances).toHaveLength(1)
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      ack(socket, 'json', 1)
      ;(await retry)()
    } finally { transport.close() }
  })

  it('continues bounded reconnect after constructor failure and cancels its timer on close', async () => {
    const transport = create()
    const onError = vi.fn()
    try {
      const pending = transport.subscribe('live', {}, (value: unknown) => value, vi.fn(), { onError })
      const first = FakeWebSocket.instances[0]
      first.open()
      await Promise.resolve()
      ack(first, 'json', 1)
      await pending
      first.close()
      let attempts = 0
      vi.stubGlobal('WebSocket', class {
        static readonly OPEN = 1
        constructor() { attempts++; throw new Error('construction failed') }
      })
      await vi.advanceTimersByTimeAsync(1000)
      expect(attempts).toBe(1)
      expect(vi.getTimerCount()).toBe(1)
      await vi.advanceTimersByTimeAsync(1999)
      expect(attempts).toBe(1)
      await vi.advanceTimersByTimeAsync(1)
      expect(attempts).toBe(2)
      await vi.runAllTimersAsync()
      expect(attempts).toBe(10)
      expect(vi.getTimerCount()).toBe(0)
      expect(onError).toHaveBeenCalledOnce()
    } finally { transport.close() }
  })

  it.each(['json', 'binary'] as const)('does not send %s requests while the socket is closing', async mode => {
    const transport = create(mode)
    try {
      const call = transport.call('live')
      const socket = FakeWebSocket.instances[0]
      socket.open()
      socket.readyState = 2
      await expect(call).rejects.toMatchObject({ error: 'NetworkError' })
      expect(socket.sent).toEqual([])
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close() }
  })

  it.each(['json', 'binary'] as const)('retires the closing %s socket before a new call replaces it', async mode => {
    const transport = create(mode), onError = vi.fn()
    try {
      const setup = transport.subscribe('live', {}, (value: unknown) => value, vi.fn(), { onError })
      const old = FakeWebSocket.instances[0]
      old.open()
      await Promise.resolve()
      ack(old, mode, 1)
      const cancel = await setup
      const pending = transport.call('live')
      void pending.catch(() => {})
      await Promise.resolve()
      old.readyState = 2
      const next = transport.call('live')
      void next.catch(() => {})
      expect(onError).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ error: 'NetworkError' }))
      await expect(pending).rejects.toMatchObject({ error: 'NetworkError' })
      const socket = FakeWebSocket.instances[1]
      socket.open()
      await Promise.resolve()
      old.onclose?.(); old.onerror?.(); cancel()
      expect(socket.sent).toHaveLength(1)
      socket.message(mode === 'json' ? '{"$id":3,"data":7}' : sequenceFrame(2, 3n, [14]).buffer)
      await expect(next).resolves.toEqual(mode === 'json' ? 7 : new Uint8Array([14]))
      expect(onError).toHaveBeenCalledOnce()
    } finally { transport.close() }
  })

  it.each(['json', 'binary'] as const)('reports %s close between connection readiness and registration as NetworkError', async mode => {
    const transport = create(mode)
    try {
      const subscription = transport.subscribe('live', {}, (value: unknown) => value, vi.fn())
      const call = transport.call('live')
      void call.catch(() => {})
      const socket = FakeWebSocket.instances[0]
      socket.open()
      transport.close()
      await expect(subscription).rejects.toMatchObject({ error: 'NetworkError' })
      await expect(call).rejects.toMatchObject({ error: 'NetworkError' })
      expect(socket.sent).toEqual([])
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close() }
  })
})

describe('WsTransport binary RPC frames', () => {
  beforeEach(() => {
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket)
  })

  it('uses an explicit frame type at the old sequence collision boundary', async () => {
    const transport = new WsTransport('ws://localhost/luvia', { mode: 'binary' })
    transport.setSchema({ health: { id: 7 } })
    ;(transport as unknown as { seq: number }).seq = 252
    const request = transport.call('health')
    const socket = FakeWebSocket.instances[0]
    socket.open()
    await Promise.resolve()

    expect(Array.from(new Uint8Array(socket.sent[0] as ArrayBuffer))).toEqual([0x01, 0xfd, 0x01, 7, 0, 0])
    socket.message(new Uint8Array([0x02, 0xfd, 0x01, 9, 0]).buffer)
    await expect(request).resolves.toEqual(new Uint8Array([9, 0]))
    transport.close()
  })

  it('times out when a call response never arrives', async () => {
    const transport = new WsTransport('ws://localhost/luvia', { timeout: 5 })
    const request = transport.call('health')
    const socket = FakeWebSocket.instances[0]
    socket.open()

    await expect(request).rejects.toMatchObject({ error: 'TimeoutError' })
    transport.close()
  })

  it('rejects a call from a canonical binary error frame', async () => {
    const transport = new WsTransport('ws://localhost/luvia', { mode: 'binary' })
    transport.setSchema({ health: { id: 7 } })
    const request = transport.call('health')
    const socket = FakeWebSocket.instances[0]
    socket.open()
    await Promise.resolve()
    socket.message(new Uint8Array([
      0x03, 1,
      1, 0xa0, 0x06,
      2, 10, 66, 97, 100, 82, 101, 113, 117, 101, 115, 116,
      3, 3, 98, 97, 100,
      0,
    ]).buffer)

    await expect(request).rejects.toMatchObject({ error: 'BadRequest', code: 400, message: 'bad' })
    transport.close()
  })
})

describe('WsTransport request ID precision', () => {
  beforeEach(() => {
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket)
  })

  it.each(['json', 'binary'] as const)('preserves adjacent full-width IDs in %s', async mode => {
    for (const first of [9007199254740991n, 9223372036854775807n, 18446744073709551614n]) {
      const transport = new WsTransport('ws://localhost/luvia', { mode })
      transport.setSchema({ health: { id: 7 } })
      const previous = first - 1n
      ;(transport as unknown as { seq: number | bigint }).seq = previous <= BigInt(Number.MAX_SAFE_INTEGER) ? Number(previous) : previous
      const one = transport.call('health')
      const two = transport.call('health')
      const socket = FakeWebSocket.instances.at(-1)!
      socket.open()
      await Promise.resolve()
      expect(socket.sent).toHaveLength(2)
      for (const [index, id] of [first, first + 1n].entries()) {
        if (mode === 'json') expect(socket.sent[index]).toContain(`"$id":${id}`)
        else expect(new Uint8Array(socket.sent[index] as ArrayBuffer)).toEqual(sequenceFrame(1, id, [7, 0, 0]))
      }
      if (mode === 'json') {
        socket.message(`{"$id":${first + 1n},"data":2}`)
        socket.message(`{"$id":${first},"data":1}`)
      } else {
        socket.message(sequenceFrame(2, first + 1n, [2]).buffer as ArrayBuffer)
        socket.message(sequenceFrame(2, first, [1]).buffer as ArrayBuffer)
      }
      await expect(one).resolves.toEqual(mode === 'json' ? 1 : new Uint8Array([1]))
      await expect(two).resolves.toEqual(mode === 'json' ? 2 : new Uint8Array([2]))
      transport.close()
    }
  })

  it('does not match malformed JSON IDs to pending calls', async () => {
    const transport = new WsTransport('ws://localhost/luvia')
    const request = transport.call('health')
    const socket = FakeWebSocket.instances[0]
    socket.open()
    await Promise.resolve()
    for (const token of ['1.0', '1e0', 'true', '"1"', '-1', 'null', '[]', '{}', '18446744073709551616']) {
      socket.message(`{"$id":${token},"data":"wrong"}`)
    }
    socket.message('{"$id":1,"data":"correct"}')
    await expect(request).resolves.toBe('correct')
    transport.close()
  })

  it.each(['json', 'binary'] as const)('fails on %s counter exhaustion without replacing a pending call', async mode => {
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    transport.setSchema({ health: { id: 7 } })
    ;(transport as unknown as { seq: bigint }).seq = 18446744073709551614n
    const request = transport.call('health')
    const socket = FakeWebSocket.instances[0]
    socket.open()
    await Promise.resolve()
    await expect(transport.call('health')).rejects.toMatchObject({ error: 'RequestIDExhausted' })
    await expect(transport.call('health')).rejects.toMatchObject({ error: 'RequestIDExhausted' })
    expect(socket.sent).toHaveLength(1)
    if (mode === 'json') socket.message('{"$id":18446744073709551615,"data":true}')
    else socket.message(sequenceFrame(2, 18446744073709551615n, [1]).buffer as ArrayBuffer)
    await expect(request).resolves.toEqual(mode === 'json' ? true : new Uint8Array([1]))
    transport.close()
  })

  it('keeps API and ID metadata authoritative', async () => {
    const transport = new WsTransport('ws://localhost/luvia')
    const request = transport.call('health', { $api: 'wrong', $id: 99, value: 9223372036854775807n })
    const socket = FakeWebSocket.instances[0]
    socket.open()
    await Promise.resolve()
    expect(JSON.parse(socket.sent[0] as string)).toMatchObject({ $api: 'health', $id: 1 })
    expect(socket.sent[0]).toContain('9223372036854775807')
    socket.message('{"$id":1,"data":true}')
    await expect(request).resolves.toBe(true)
    transport.close()
  })

  it.each(['json', 'binary'] as const)('correlates full-width %s error responses exactly', async mode => {
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    transport.setSchema({ health: { id: 7 } })
    ;(transport as unknown as { seq: bigint }).seq = 18446744073709551614n
    const request = transport.call('health')
    const socket = FakeWebSocket.instances[0]
    socket.open()
    await Promise.resolve()
    if (mode === 'json') {
      socket.message('{"$id":18446744073709551614,"data":"wrong"}')
      socket.message('{"$id":18446744073709551615,"error":"BadRequest","code":400,"message":"bad"}')
    } else {
      socket.message(sequenceFrame(2, 18446744073709551614n, [42]).buffer as ArrayBuffer)
      socket.message(sequenceFrame(3, 18446744073709551615n, [
        1, 0xa0, 0x06, 2, 10, ...new TextEncoder().encode('BadRequest'), 3, 3, 98, 97, 100, 0,
      ]).buffer as ArrayBuffer)
    }
    await expect(request).rejects.toMatchObject({ error: 'BadRequest', code: 400, message: 'bad' })
    transport.close()
  })

  it('drops truncated and overflowing binary headers without consuming a pending call', async () => {
    const transport = new WsTransport('ws://localhost/luvia', { mode: 'binary' })
    transport.setSchema({ health: { id: 7 } })
    const request = transport.call('health')
    const socket = FakeWebSocket.instances[0]
    socket.open()
    await Promise.resolve()
    for (const bytes of [[2], [2, 0x81], [3, 0x81], [2, ...Array(9).fill(128), 2], [0x82, 0, 1, 0]]) {
      expect(() => socket.message(Uint8Array.from(bytes).buffer)).not.toThrow()
    }
    socket.message(sequenceFrame(2, 1n, [42]).buffer as ArrayBuffer)
    await expect(request).resolves.toEqual(new Uint8Array([42]))
    transport.close()
  })

  it.each(['json', 'binary'] as const)('does not complete %s subscriptions with malformed acknowledgements', async mode => {
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    transport.setSchema({ live: { id: 7 } })
    let completed = false
    const subscribed = (async () => {
      const unsubscribe = await transport.subscribe('live', {}, (value: unknown) => value, () => {})
      completed = true
      return unsubscribe
    })()
    const socket = FakeWebSocket.instances[0]
    socket.open()
    await Promise.resolve()
    if (mode === 'json') {
      for (const ok of ['1', '1.0', 'false', '"true"', 'null']) socket.message(`{"$sub":1,"ok":${ok}}`)
      socket.message('{"$sub":1}')
    } else {
      for (const bytes of [[0x13, 0x87], [0x13, 1, 42], [0x13, ...Array(9).fill(128), 2]]) {
        socket.message(Uint8Array.from(bytes).buffer)
      }
    }
    await new Promise<void>(resolve => setTimeout(resolve, 0))
    expect(completed).toBe(false)
    if (mode === 'json') socket.message('{"$sub":1,"ok":true}')
    else socket.message(new Uint8Array([0x13, 1]).buffer)
    const unsubscribe = await subscribed
    unsubscribe()
    transport.close()
  })
})

// Independent BigInt reference, not the production header writer.
function sequenceFrame(type: number, id: bigint, payload: number[]): Uint8Array<ArrayBuffer> {
  const bytes = [type]
  while (id >= 128n) {
    bytes.push(Number(id & 127n) | 128)
    id >>= 7n
  }
  return Uint8Array.from([...bytes, Number(id), ...payload])
}

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
