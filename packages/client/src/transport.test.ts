import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { FetchTransport, WsTransport, decodeBinaryError, encodeBinaryBody, encodeParam } from './transport'
import { GeneratedRequest } from './request'
import { Encoder } from './codec'
import { LuxoError } from './error'
import { decodeLuxoJSON } from './json'
import { JSONEnvelopeSource } from './json-resource'
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

const responseNode = (depth: number, leaf = '{}'): string => '{"next":'.repeat(depth - 1) + leaf + '}'.repeat(depth - 1)
const responseJSON = (depth: number): string => '['.repeat(depth) + '0' + ']'.repeat(depth)
const responseSchema: Record<string, APISchema> = { live: { id: 7,
  response: { type: 'Model', typeName: 'Node' }, types: { Node: {
    next: { fieldID: 1, type: 'Model', typeName: 'Node' }, json: { fieldID: 2, type: 'JSON' },
  } },
} }

beforeEach(() => {
  mockFetch.mockReset()
  vi.stubGlobal('fetch', mockFetch)
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('binary param validation', () => {
	it('keeps nullable JSON null canonical without conflating omission, strings or list elements', () => {
		const param = { fieldID: 1, name: 'value', type: 'JSON', nullable: true }
		const meta: APISchema = { id: 7, params: [param] }
		expect(encodeBinaryBody(meta, { value: null })).toEqual(new Uint8Array([7, 0, 1, 0, 0]))
		expect(encodeBinaryBody(meta, {})).toEqual(new Uint8Array([7, 0, 0]))
		expect(encodeBinaryBody(meta, { value: 'null' })).toEqual(new Uint8Array([7, 0, 1, 1, 6, 34, 110, 117, 108, 108, 34, 0]))
		expect(encodeBinaryBody({ ...meta, params: [{ ...param, isList: true }] }, { value: [null] }))
			.toEqual(new Uint8Array([7, 0, 1, 1, 1, 4, 110, 117, 108, 108, 0]))
		expect(() => encodeBinaryBody({ ...meta, params: [{ ...param, nullable: false }] }, { value: null }))
			.toThrow('not nullable')
	})

  it('writes structured Float, DateTime and Bytes using independent golden bytes', () => {
    const encoder = new Encoder()
    encodeParam(encoder, { fieldID: 1, type: 'Model', typeName: 'Input' }, {
      score: 1.5, created: '1970-01-01T00:00:00Z', payload: new Uint8Array([7, 9]),
    }, { Input: {
      score: { fieldID: 1, type: 'Float' }, created: { fieldID: 2, type: 'DateTime' },
      payload: { fieldID: 3, type: 'Bytes' },
    } })
    expect(Array.from(encoder.bytes())).toEqual([
      1, 18, 0, 1, 0, 0, 0, 0, 0, 0, 248, 63, 2, 0, 0, 3, 2, 7, 9, 0,
    ])
  })

  it('writes empty Float and Boolean legacy lists and rejects unsupported structured wire types', () => {
    const encoder = new Encoder()
    encodeParam(encoder, { fieldID: 1, type: 'Float', isList: true }, [])
    encodeParam(encoder, { fieldID: 2, type: 'Boolean', isList: true }, [false, true])
    expect(Array.from(encoder.bytes())).toEqual([1, 0, 2, 2, 0, 1])
    expect(() => encodeParam(new Encoder(), { fieldID: 1, type: 'Model', typeName: 'Input' },
      { value: 42 }, { Input: { value: { fieldID: 1, type: 'Unrecognized' } } })).toThrow('unsupported structured field type: Unrecognized')
  })
  it.each([
    { type: 'Int', isList: true, value: [0n, -1n, 1n], bytes: [1, 3, 0, 1, 2, 0] },
    { type: 'Float', isList: false, value: 2.5, bytes: [1, 0, 0, 0, 0, 0, 0, 4, 64, 0] },
    { type: 'Float', isList: true, value: [2.5], bytes: [1, 1, 0, 0, 0, 0, 0, 0, 4, 64, 0] },
    { type: 'Bytes', isList: true, value: [Uint8Array.of(1, 2), Uint8Array.of(255).buffer], bytes: [1, 2, 2, 1, 2, 1, 255, 0] },
    { type: 'Decimal', isList: true, value: ['1.5'], bytes: [1, 1, 3, 49, 46, 53, 0] },
    { type: 'UUID', isList: true, value: ['00112233-4455-6677-8899-aabbccddeeff'],
      bytes: [1, 1, 0, 17, 34, 51, 68, 85, 102, 119, 136, 153, 170, 187, 204, 221, 238, 255, 0] },
  ])('preserves golden legacy $type wire bytes (list=$isList)', ({ type, isList, value, bytes }) => {
    const encoder = new Encoder()
    encodeParam(encoder, { fieldID: 1, type, isList }, value)
    encoder.writeEnd()
    expect(encoder.bytes()).toEqual(Uint8Array.from(bytes))
  })

  it('rejects invalid legacy list and Bytes inputs explicitly', () => {
    expect(() => encodeParam(new Encoder(), { fieldID: 1, type: 'Int', isList: true }, 1n)).toThrow('must be an array')
    expect(() => encodeParam(new Encoder(), { fieldID: 1, type: 'Bytes' }, [1, 2])).toThrow('require Uint8Array or ArrayBuffer')
  })

  it('counts astral text bytes in the structured arena and preserves its golden encoding', () => {
    const encoder = new Encoder()
    encodeParam(encoder, { fieldID: 1, type: 'Model', typeName: 'Node' }, { label: '😀中' }, {
      Node: { label: { fieldID: 1, type: 'String' } },
    })
    encoder.writeEnd()
    expect(encoder.bytes()).toEqual(Uint8Array.of(1, 11, 7, 1, 7, 240, 159, 152, 128, 228, 184, 173, 0, 0))
  })

  it('rejects incomplete legacy model metadata and non-text arena fields explicitly', () => {
    const model = { fieldID: 1, type: 'Model', typeName: 'Node' }
    expect(() => encodeParam(new Encoder(), { fieldID: 1, type: 'Model' }, {})).toThrow('structured type <unknown>')
    expect(() => encodeParam(new Encoder(), model, { label: 42 }, {
      Node: { label: { fieldID: 1, type: 'String' } },
    })).toThrow('label must be a string')
    expect(() => encodeParam(new Encoder(), model, { label: 'text' }, {
      Node: { label: { fieldID: 1 } },
    })).toThrow('missing wire type for field label')
    expect(() => encodeParam(new Encoder(), model, { next: {} }, {
      Node: { next: { fieldID: 1, type: 'Model' } },
    })).toThrow('structured type <unknown>')
  })

	it.each(['false', 'true', 0, 1, -1, {}, [], new Boolean(false)])
	('rejects non-boolean runtime input instead of using truthiness: %s', (value: unknown) => {
		const types = { Input: { enabled: { fieldID: 1, type: 'Boolean' } } }
		for (const list of [false, true]) {
			const input = list ? [value] : value
			expect(() => encodeParam(new Encoder(), { fieldID: 1, type: 'Boolean', isList: list }, input)).toThrow()
			const fields = { Input: { enabled: { ...types.Input.enabled, isList: list } } }
			expect(() => encodeParam(new Encoder(), { fieldID: 1, type: 'Model', typeName: 'Input' },
				{ enabled: input }, fields)).toThrow()
		}
	})

	it('rejects null, undefined and holes within boolean lists', () => {
		for (const input of [[null], [undefined], new Array<unknown>(1)]) {
			expect(() => encodeParam(new Encoder(), { fieldID: 1, type: 'Boolean', isList: true }, input)).toThrow()
			expect(() => encodeParam(new Encoder(), { fieldID: 1, type: 'Model', typeName: 'Input' },
				{ flags: input }, { Input: { flags: { fieldID: 1, type: 'Boolean', isList: true } } })).toThrow()
		}
	})

	it('preserves false, true, nullable flags and omitted boolean parameters', () => {
		for (const nullable of [false, true]) {
			const meta = { id: 7, params: [{ fieldID: 1, name: 'value', type: 'Boolean', nullable }] }
			for (const value of [false, true]) {
				expect(encodeBinaryBody(meta, { value })).toEqual(Uint8Array.from([7, 0, 1,
					...(nullable ? [1] : []), value ? 1 : 0, 0]))
			}
			expect(encodeBinaryBody(meta, {})).toEqual(Uint8Array.from([7, 0, 0]))
			if (nullable) expect(encodeBinaryBody(meta, { value: null })).toEqual(Uint8Array.from([7, 0, 1, 0, 0]))
		}
	})

	it.each(['String', 'Decimal'])('rejects boxed and spoofed %s runtime values', (type) => {
		for (const value of [new String('12.5'), { isWellFormed: () => true, toString: () => '\ud800' }]) {
			expect(() => encodeParam(new Encoder(), { fieldID: 1, type }, value)).toThrow()
			expect(() => encodeParam(new Encoder(), { fieldID: 1, type, isList: true }, [value])).toThrow()
			expect(() => encodeParam(new Encoder(), { fieldID: 1, type: 'Model', typeName: 'Input' },
				{ values: [value] }, { Input: { values: { fieldID: 1, type, isList: true } } })).toThrow()
		}
	})

	it('checks the actual string value after a getter changed its type during arena sizing', () => {
		let reads = 0
		const spoof = { isWellFormed: () => true, toString: () => '\ud800' }
		const input = { get label(): unknown { return reads++ === 0 ? 'safe' : spoof } }
		expect(() => encodeParam(new Encoder(), { fieldID: 1, type: 'Model', typeName: 'Input' },
			input, { Input: { label: { fieldID: 1, type: 'String' } } })).toThrow('require a string')
	})

	it('rejects an array supplied as one model object while allowing an empty model list', () => {
		const types = { Input: { enabled: { fieldID: 1, type: 'Boolean', nullable: true } } }
		for (const value of [[], Object.assign([], { enabled: true })]) {
			expect(() => encodeParam(new Encoder(), { fieldID: 1, type: 'Model', typeName: 'Input' }, value, types)).toThrow()
			expect(() => encodeParam(new Encoder(), { fieldID: 1, type: 'Model', typeName: 'Input', isList: true }, [value], types)).toThrow()
		}
		const encoder = new Encoder()
		encodeParam(encoder, { fieldID: 1, type: 'Model', typeName: 'Input', isList: true }, [], types)
		expect(encoder.bytes()).toEqual(Uint8Array.from([1, 0]))
	})

	it.each([false, true])('writes dynamic JSON values without a temporary UTF-8 array (list=%s)', (list) => {
		const meta: APISchema = { id: 7, params: [{ fieldID: 1, name: 'value', type: 'JSON', isList: list }] }
		const value = { id: 9007199254740993n, text: '中文' }
		const reference = encodeBinaryBody(meta, { value: list ? [value] : value })
		const utf8 = vi.spyOn(TextEncoder.prototype, 'encode')
		const serialize = vi.spyOn(JSON, 'stringify')
		let body: Uint8Array, utf8Calls: number, jsonCalls: number
		try {
			body = encodeBinaryBody(meta, { value: list ? [value] : value })
			utf8Calls = utf8.mock.calls.length
			jsonCalls = serialize.mock.calls.length
		} finally {
			utf8.mockRestore()
			serialize.mockRestore()
		}
		expect(body).toEqual(reference)
		expect(utf8Calls).toBe(0)
		expect(jsonCalls).toBe(1)
	})

	it('writes structured JSON null directly without general JSON or temporary UTF-8 encoding', () => {
		const meta: APISchema = { id: 7, params: [{ fieldID: 1, name: 'value', type: 'Model', typeName: 'Input' }],
			types: { Input: { payload: { fieldID: 1, type: 'JSON' } } } }
		const serialize = vi.spyOn(JSON, 'stringify')
		const utf8 = vi.spyOn(TextEncoder.prototype, 'encode')
		let body: Uint8Array, jsonCalls: number, utf8Calls: number
		try {
			body = encodeBinaryBody(meta, { value: { payload: null } })
			jsonCalls = serialize.mock.calls.length
			utf8Calls = utf8.mock.calls.length
		} finally {
			serialize.mockRestore()
			utf8.mockRestore()
		}
		expect(body).toEqual(new Uint8Array([7, 0, 1, 8, 0, 1, 4, 110, 117, 108, 108, 0, 0]))
		expect(jsonCalls).toBe(0)
		expect(utf8Calls).toBe(0)
	})

	it('accepts JSON null inside a structured field while retaining top-level and list null checks', () => {
		const meta: APISchema = { id: 7, params: [{ fieldID: 1, name: 'value', type: 'Model', typeName: 'Input' }],
			types: { Input: { payload: { fieldID: 1, type: 'JSON' } } } }
		expect(encodeBinaryBody(meta, { value: { payload: null } })).toEqual(
			new Uint8Array([7, 0, 1, 8, 0, 1, 4, 110, 117, 108, 108, 0, 0]))
		expect(() => encodeBinaryBody({ id: 7, params: [{ fieldID: 1, name: 'value', type: 'JSON' }] },
			{ value: null })).toThrow('not nullable')
		for (const field of [{ fieldID: 1, type: 'String' }, { fieldID: 1, type: 'JSON', isList: true }]) {
			expect(() => encodeBinaryBody({ ...meta, types: { Input: { payload: field } } },
				{ value: { payload: null } })).toThrow('not nullable')
			if (field.isList) expect(() => encodeBinaryBody({ ...meta, types: { Input: { payload: field } } },
				{ value: { payload: {} } })).toThrow('must be an array')
		}
	})
	it.each([false, 0, '', 'null'])('keeps non-null JSON payload %j distinct from null', payload => {
		const meta: APISchema = { id: 7, params: [{ fieldID: 1, name: 'value', type: 'Model', typeName: 'Input' }],
			types: { Input: { payload: { fieldID: 1, type: 'JSON' } } } }
		const json = new TextEncoder().encode(JSON.stringify(payload))
		expect(encodeBinaryBody(meta, { value: { payload } })).toEqual(
			new Uint8Array([7, 0, 1, json.length + 4, 0, 1, json.length, ...json, 0, 0]))
	})

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

	it('ignores inherited structured field metadata', () => {
		const fields = { id: { fieldID: 1, type: 'Int' }, name: { fieldID: 2, type: 'String' } }
		const inherited = { ignored: { fieldID: 3, type: 'String' } }
		Object.defineProperty(inherited, 'trap', { enumerable: true, get() { throw new Error('inherited field read') } })
		Object.setPrototypeOf(fields, inherited)
		const meta: APISchema = {
			id: 7,
			params: [{ fieldID: 1, name: 'items', type: 'Model', typeName: 'Input', isList: true }],
			types: { Input: fields },
		}
		const input = { items: [{ id: -1n, name: '中文' }, { id: 42n, name: 'é' }] }
		const expected = encodeBinaryBody({ ...meta, types: { Input: { ...fields } } }, input)
		const actual = encodeBinaryBody(meta, input)
		expect(actual).toEqual(expected)
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

	it('treats undefined query controls as absent and still rejects null', () => {
		const meta = { id: 5 }
		const absent = encodeBinaryBody(meta, {})
		for (const params of [{ $filters: undefined }, { $sorters: undefined }, { $filters: undefined, $sorters: undefined }]) {
			expect(encodeBinaryBody(meta, params)).toEqual(absent)
		}
		expect(() => encodeBinaryBody(meta, { $filters: null })).toThrow('$filters must be an array')
		expect(() => encodeBinaryBody(meta, { $sorters: null })).toThrow('$sorters must be an array')
	})

	it('reads dynamic query-control getters once', () => {
		let reads = 0
		const params = {
			get $filters() { reads++; return [] },
			get $sorters() { reads++; return [] },
		}
		encodeBinaryBody({ id: 5 }, params)
		expect(reads).toBe(2)
	})

	it('rejects malformed binary list controls', () => {
		expect(() => encodeBinaryBody({ id: 1 }, { $filters: [{ field: 'age', op: 'bad', value: 1 }] })).toThrow('invalid $filters')
		expect(() => encodeBinaryBody({ id: 1 }, { $sorters: [{ field: 'age', order: 'bad' }] })).toThrow('invalid $sorters')
	})
})

describe('JSON/Binary integer input parity', () => {
  it('rejects generated Fetch subscriptions before invoking their request codec or network', async () => {
    const transport = new FetchTransport('http://localhost/api')
    const encode = vi.fn(() => { throw new Error('unexpected encoding') })
    const codec = { apiID: 7, initialCapacity: encode, writeParameters: encode }
    await expect(transport.subscribeGenerated('live', codec, undefined, value => value, () => {}))
      .rejects.toMatchObject({ error: 'ConfigError' })
    expect(encode).not.toHaveBeenCalled()
    expect(mockFetch).not.toHaveBeenCalled()
  })

  it.each(['model', 'json', 'envelope'])('rejects overwritten HTTP %s resources before native parse and retains the request snapshot', async kind => {
    const transport = new FetchTransport('http://localhost/api')
    transport.setSchema(responseSchema)
    const data = kind === 'model' ? '{"next":' + responseNode(64) + ',"next":null}'
      : kind === 'json' ? '{"json":' + responseJSON(129) + ',"json":0}' : responseNode(65)
    const text = '{"data":' + data + (kind === 'envelope' ? ',"data":null' : '') + '}'
    const response = new Response(text)
    mockFetch.mockImplementation(async () => {
      transport.setSchema({})
      return response
    })
    const parse = vi.spyOn(JSON, 'parse')
    try {
      await expect(transport.call('live')).rejects.toMatchObject({ error: 'ParseError' })
      expect(parse.mock.calls.some(([source]) => source === text)).toBe(false)
    } finally { parse.mockRestore() }
    mockFetch.mockResolvedValue(new Response('{"data":42}'))
    await expect(transport.call('live')).resolves.toBe(42)
  })

  it('preserves valid model64 plus JSON128 and legacy undeclared JSON responses', async () => {
    const transport = new FetchTransport('http://localhost/api')
    transport.setSchema(responseSchema)
    const text = '{"data":' + responseNode(64, '{"json":' + responseJSON(128) + '}') + '}'
    mockFetch.mockResolvedValueOnce(new Response(text))
    await expect(transport.call('live')).resolves.toBeTypeOf('object')
    transport.setSchema({})
    mockFetch.mockResolvedValueOnce(new Response('{"data":' + responseJSON(200) + '}'))
    await expect(transport.call('live')).resolves.toBeInstanceOf(Array)
  })

  it.each([false, true].flatMap(structured => [false, true].map(list => ({ structured, list }))))
  ('bounds serialized JSON parameters independently of typed wrappers ($structured, $list)', async ({ structured, list }) => {
    mockFetch.mockImplementation(async () => new Response('{"data":true}'))
    const transport = new FetchTransport('http://localhost/api')
    transport.setSchema({ echo: { id: 1, params: [{ fieldID: 1, name: 'value', type: structured ? 'Model' : 'JSON',
      typeName: structured ? 'Node' : undefined, isList: !structured && list }], types: { Node: {
      next: { fieldID: 1, type: 'Model', typeName: 'Node', nullable: true },
      data: { fieldID: 2, type: 'JSON', isList: list },
    } } } })
    for (const depth of [128, 129]) {
      let json: unknown = 0
      for (let i = 0; i < depth; i++) json = [json]
      let value: unknown = list ? [json] : json
      if (structured) {
        let node: Record<string, unknown> = { data: value }
        for (let i = 1; i < 64; i++) node = { next: node }
        value = node
      }
      let calls = 0
      const wrapped = structured ? value : { toJSON() { calls++; return json } }
      const params = { value: !structured && list ? [wrapped] : wrapped }
      const invoke = () => transport.call('echo', params)
      if (depth === 128) await expect(invoke()).resolves.toBe(true)
      else await expect(invoke()).rejects.toThrow(/JSON nesting exceeds 128 levels/)
      if (!structured) expect(calls).toBe(1)
    }
    expect(mockFetch).toHaveBeenCalledTimes(1)
  })

  it('checks model depth after serialization and invokes toJSON only once', async () => {
    mockFetch.mockImplementation(async () => new Response('{"data":true}'))
    const transport = new FetchTransport('http://localhost/api')
    transport.setSchema({ echo: { id: 1, params: [{ fieldID: 1, name: 'value', type: 'Model', typeName: 'Node' }],
      types: { Node: { next: { fieldID: 1, type: 'Model', typeName: 'Node', nullable: true } } } } })
    let value: Record<string, unknown> = {}
    for (let i = 1; i < 65; i++) value = { next: value }
    let calls = 0
    await expect(transport.call('echo', { value: { next: null, toJSON() { calls++; return value } } }))
      .rejects.toThrow(/model nesting exceeds 64 levels/)
    expect(calls).toBe(1)
    expect(mockFetch).not.toHaveBeenCalled()
  })

  it('preserves scalar toJSON overrides and JSON mode without declared parameter types', async () => {
    mockFetch.mockImplementation(async () => new Response('{"data":true}'))
    const transport = new FetchTransport('http://localhost/api')
    transport.setSchema({ echo: { id: 1, params: [{ fieldID: 1, name: 'value', type: 'JSON' }] } })
    let deep: unknown = 0
    for (let i = 0; i < 129; i++) deep = [deep]
    await expect(transport.call('echo', { value: { deep, toJSON: () => 42 } })).resolves.toBe(true)
    expect(mockFetch.mock.lastCall?.[1].body).toBe('{"value":42,"$api":"echo"}')
    transport.setSchema({})
    await expect(transport.call('echo', { unknownType: deep })).resolves.toBe(true)
    expect(mockFetch).toHaveBeenCalledTimes(2)
  })

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
  it('keeps the HTTP JSON plan while a generated request replaces Schema', async () => {
    const transport = new FetchTransport('http://localhost/luvia')
    transport.setSchema({ echo: { id: 7, params: [{ fieldID: 1, name: 'value', type: 'Int' }] } })
    const request = new class extends GeneratedRequest {
      readonly apiID = 7
      writeParameters() {}
      toJSONParameters() {
        transport.setSchema({ echo: { id: 7, params: [{ fieldID: 1, name: 'value', type: 'Float' }] } })
        return { value: 1.5 }
      }
    }()
    mockFetch.mockImplementation(async () => new Response('{"data":true}'))
    await expect(transport.call('echo', request)).rejects.toThrow(/bigint/)
    expect(mockFetch).not.toHaveBeenCalled()
    await transport.call('echo', { value: 1.5 })
    expect(mockFetch).toHaveBeenCalledOnce()
  })

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
  it('rejects missing binary HTTP data while preserving the null marker', async () => {
    const refresh = vi.fn(async () => 'unused')
    const transport = new FetchTransport('http://localhost/luvia', { mode: 'binary', onTokenExpired: refresh })
    transport.setSchema({ live: { id: 7 } })
    const codec = { apiID: 7, initialCapacity: () => 8, writeParameters() {} }
    for (const generated of [false, true]) {
      mockFetch.mockResolvedValue({ status: 200, arrayBuffer: async () => new ArrayBuffer(0) })
      const call = () => generated ? transport.callGenerated('live', codec) : transport.call('live')
      await expect(call()).rejects.toMatchObject({ error: 'ParseError', code: 200 })
      mockFetch.mockResolvedValue({ status: 200, arrayBuffer: async () => new Uint8Array([0]).buffer })
      await expect(call()).resolves.toEqual(new Uint8Array([0]))
    }
    expect(refresh).not.toHaveBeenCalled()
  })
  it('sends the owned encoded buffer without another copy or input aliasing', async () => {
    const encoded = vi.spyOn(Encoder.prototype, 'bytes')
    const t = new FetchTransport('http://localhost:8080/api', { mode: 'binary' })
    t.setSchema({ echo: { id: 1, params: [{ fieldID: 1, name: 'payload', type: 'Bytes' }] } })
    mockFetch.mockResolvedValue({ status: 200, arrayBuffer: async () => new Uint8Array([84]).buffer })
    const input = new Uint8Array([1, 2, 3])
    await t.call('echo', { payload: input })
    const first = mockFetch.mock.calls[0][1].body as ArrayBuffer
    expect(first).toBe(encoded.mock.results[0].value.buffer)
    const saved = new Uint8Array(first).slice()
    input.fill(255)
    await t.call('echo', { payload: input })
    expect(mockFetch.mock.calls[1][1].body).not.toBe(first)
    expect(new Uint8Array(first)).toEqual(saved)
  })

  it('encodes $select into the request field mask', async () => {
    const t = new FetchTransport('http://localhost:8080/api', { mode: 'binary' })
    t.setSchema({ getUser: { id: 1, fields: { id: { fieldID: 1 }, name: { fieldID: 2 } } } })
    mockFetch.mockResolvedValueOnce({
      ok: true,
      status: 200,
      arrayBuffer: async () => new Uint8Array([0]).buffer,
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
      .mockResolvedValueOnce({ ok: true, status: 200, arrayBuffer: async () => new Uint8Array([0]).buffer })

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
  it('rejects absent success data but preserves explicit null and empty values', async () => {
    const refresh = vi.fn()
    const transport = new FetchTransport('http://localhost/luvia', { onTokenExpired: refresh })
    for (const body of ['{}', '{"ok":true}', '{"value":42}']) {
      mockFetch.mockResolvedValueOnce(new Response(body, { status: 200 }))
      await expect(transport.call('echo')).rejects.toMatchObject({ error: 'ParseError', code: 200 })
    }
    for (const data of [null, false, 0, '', [], {}]) {
      mockFetch.mockResolvedValueOnce(new Response(JSON.stringify({ data }), { status: 200 }))
      await expect(transport.call('echo')).resolves.toEqual(data)
    }
    expect(refresh).not.toHaveBeenCalled()
    expect(mockFetch).toHaveBeenCalledTimes(9)
  })

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

  it.each(['json', 'binary'] as const)('cancels while reading a %s response body', async mode => {
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
    const t = new FetchTransport('http://localhost:8080/api', { mode })
    t.setSchema({ getUser: { id: 1 } })
    const request = t.call('getUser', undefined, { signal: controller.signal })
    await Promise.resolve()
    controller.abort()

    await expect(request).rejects.toMatchObject({ error: 'CancelledError' })
  })

  it.each([200, 503])('cleans up failed Binary body reads with HTTP %i', async status => {
    vi.useFakeTimers()
    try {
      const refresh = vi.fn()
      const t = new FetchTransport('http://localhost/api', { mode: 'binary', onTokenExpired: refresh })
      t.setSchema({ read: { id: 1 } })
      mockFetch.mockResolvedValueOnce({ status, arrayBuffer: async () => { throw new Error('broken body') } })
      await expect(t.call('read')).rejects.toMatchObject({ error: 'ParseError', code: status })
      expect(refresh).not.toHaveBeenCalled()
      expect(vi.getTimerCount()).toBe(0)
    } finally {
      vi.useRealTimers()
    }
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

  it('keeps a monotonic deadline when no observer is configured', async () => {
    const now = vi.spyOn(performance, 'now')
    const t = new FetchTransport('http://localhost:8080/api')
    mockFetch.mockResolvedValueOnce(new Response('{"data":true}'))

    await t.call('health')

    expect(now).toHaveBeenCalled()
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
  onmessage: ((event: { data: unknown }) => void) | null = null
  onclose: ((event?: { code?: number }) => void) | null = null

  constructor(readonly url: string) {
    FakeWebSocket.instances.push(this)
  }

  open() {
    this.readyState = FakeWebSocket.OPEN
    this.onopen?.()
  }

  message(data: unknown) {
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

function queuedTransportTimeouts(delay: number) {
  const schedule = globalThis.setTimeout.bind(globalThis)
  const original = Object.getOwnPropertyDescriptor(globalThis, 'setTimeout')
  if (!original) throw new Error('missing host setTimeout')
  const ready: Array<() => void> = []
  const timer = (callback: (...args: unknown[]) => void, wait?: number, ...args: unknown[]) => {
    const deliver = () => callback(...args)
    return schedule(wait === delay ? () => { ready.push(deliver) } : deliver, wait)
  }
  Object.defineProperty(globalThis, 'setTimeout', { ...original, value: timer })
  return {
    count: () => ready.length,
    flush: () => { for (const deliver of ready.splice(0)) deliver() },
    restore: () => Object.defineProperty(globalThis, 'setTimeout', original),
  }
}

function controlledDeadlineClock() {
  let elapsed = 0
  vi.spyOn(performance, 'now').mockImplementation(() => elapsed)
  const timers = vi.spyOn(globalThis, 'setTimeout')
  return {
    at: (value: number) => { elapsed = value },
    fire: () => {
      const callback = timers.mock.calls.at(-1)?.[0]
      if (typeof callback !== 'function') throw new Error('missing deadline callback')
      callback()
    },
  }
}

function waitForAbort(signal: AbortSignal): Promise<ArrayBuffer> {
  return new Promise((_, reject) => {
    const abort = () => reject(new DOMException('request aborted', 'AbortError'))
    if (signal.aborted) abort()
    else signal.addEventListener('abort', abort, { once: true })
  })
}

describe('transport monotonic deadlines', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket)
  })
  afterEach(() => { vi.clearAllTimers(); vi.restoreAllMocks(); vi.useRealTimers() })

  for (const mode of ['json', 'binary'] as const) for (const generated of [false, true]) {
    it.each(['headers', 'body', 'refresh'] as const)(`keeps the full HTTP ${mode} budget during %s, generated=${generated}`, async phase => {
      const clock = controlledDeadlineClock()
      const refresh = vi.fn(() => new Promise<string>(() => {}))
      const transport = new FetchTransport('http://localhost/api', { mode, timeout: 1.9, onTokenExpired: refresh })
      transport.setSchema({ slow: { id: 1 } })
      mockFetch.mockImplementation((_url: string, init: { signal: AbortSignal }) => {
        if (phase === 'headers') return waitForAbort(init.signal)
        if (phase === 'body') return Promise.resolve({ status: 200, arrayBuffer: () => waitForAbort(init.signal) })
        const error = new Encoder()
        error.writeFieldInt(1, 401n)
        error.writeFieldString(2, 'Unauthorized')
        error.writeFieldString(3, 'expired')
        error.writeEnd()
        return Promise.resolve(new Response(mode === 'json'
          ? '{"error":"Unauthorized","code":401,"message":"expired"}'
          : new Uint8Array(error.bytes()), { status: 401 }))
      })
      let settled: unknown
      const request = (generated ? transport.callGenerated('slow', {
        apiID: 1, initialCapacity: () => 8, writeParameters: () => {},
      }) : transport.call('slow')).catch((error: unknown) => { settled = error; return error })
      await vi.advanceTimersByTimeAsync(0)
      clock.at(1)
      clock.fire()
      await vi.advanceTimersByTimeAsync(0)
      expect(settled).toBeUndefined()
      expect(mockFetch).toHaveBeenCalledTimes(1)
      clock.at(1.9)
      clock.fire()
      expect(await request).toMatchObject({ error: 'TimeoutError' })
      expect(refresh).toHaveBeenCalledTimes(phase === 'refresh' ? 1 : 0)
      expect(mockFetch).toHaveBeenCalledTimes(1)
      expect(vi.getTimerCount()).toBe(0)
    })
  }

  for (const mode of ['json', 'binary'] as const) {
    it.each(['connect', 'call', 'subscribe', 'renewal'] as const)(`keeps the full WS ${mode} budget during %s`, async phase => {
      const clock = controlledDeadlineClock()
      const refresh = vi.fn(() => new Promise<string>(() => {}))
      const transport = new WsTransport('ws://localhost/api', { mode, timeout: 1.9, onTokenExpired: refresh })
      transport.setSchema({ slow: { id: 1 } })
      let settled: unknown
      try {
        const initial = phase === 'subscribe'
          ? transport.subscribe('slow', {}, value => value, () => {})
          : transport.call('slow')
        const first = initial.catch((error: unknown) => { settled = error; return error })
        const socket = FakeWebSocket.instances[0]
        if (phase !== 'connect') socket.open()
        await vi.advanceTimersByTimeAsync(0)
        let request: Promise<unknown> = first
        if (phase === 'renewal') {
          socket.onclose?.({ code: 4001 })
          expect(await first).toMatchObject({ error: 'Unauthorized' })
          settled = undefined
          request = transport.call('slow').catch((error: unknown) => { settled = error; return error })
          await vi.advanceTimersByTimeAsync(0)
        }
        clock.at(1)
        clock.fire()
        await vi.advanceTimersByTimeAsync(0)
        expect(settled).toBeUndefined()
        clock.at(1.9)
        clock.fire()
        expect(await request).toMatchObject({ error: phase === 'renewal' ? 'Unauthorized' : 'TimeoutError' })
        expect(refresh).toHaveBeenCalledTimes(phase === 'renewal' ? 1 : 0)
      } finally { transport.close() }
      expect(vi.getTimerCount()).toBe(0)
    })
  }
})

describe('WS timeout delivery and host reentry', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket)
  })
  afterEach(() => { vi.clearAllTimers(); vi.useRealTimers() })

  it.each(['after-open', 'during-clear'] as const)('keeps an open connection when its dequeued timeout arrives %s', async delivery => {
    const host = queuedTransportTimeouts(11)
    const transport = new WsTransport('ws://localhost/api', { timeout: 11 })
    let clear: PropertyDescriptor | undefined
    try {
      const pending = transport.call('live').catch((error: unknown) => error)
      const socket = FakeWebSocket.instances[0]
      await vi.advanceTimersByTimeAsync(11)
      expect(host.count()).toBe(1)
      if (delivery === 'during-clear') {
        const cancel = globalThis.clearTimeout.bind(globalThis)
        clear = Object.getOwnPropertyDescriptor(globalThis, 'clearTimeout')
        Object.defineProperty(globalThis, 'clearTimeout', { ...clear, value: (...args: Parameters<typeof globalThis.clearTimeout>) => { cancel(...args); host.flush() } })
      }
      socket.open()
      await Promise.resolve()
      host.flush()
      socket.message('{"$id":1,"data":42}')
      await expect(pending).resolves.toBe(42)
      expect(socket.readyState).toBe(FakeWebSocket.OPEN)
      expect(vi.getTimerCount()).toBe(0)
    } finally { if (clear) Object.defineProperty(globalThis, 'clearTimeout', clear); transport.close(); host.restore() }
  })

  it('preserves the replacement connection rejection when old timer cancellation reenters connect', async () => {
    const transport = new WsTransport('ws://localhost/api', { timeout: 11 })
    const controller = new AbortController()
    let next: Promise<unknown> | undefined, observed: unknown
    const cancel = globalThis.clearTimeout.bind(globalThis)
    let entered = false
    const original = Object.getOwnPropertyDescriptor(globalThis, 'clearTimeout')
    if (!original) throw new Error('missing host clearTimeout')
    const clear = (...args: Parameters<typeof globalThis.clearTimeout>) => {
      cancel(...args)
      if (entered) return
      entered = true
      next = transport.call('live', undefined, { signal: controller.signal }).catch((error: unknown) => error)
      void next.then(value => { observed = value })
    }
    Object.defineProperty(globalThis, 'clearTimeout', { ...original, value: clear })
    try {
      const first = transport.call('live').catch((error: unknown) => error)
      FakeWebSocket.instances[0].close()
      expect(await first).toMatchObject({ error: 'NetworkError' })
      const replacement = FakeWebSocket.instances[1]
      expect(replacement).toBeDefined()
      replacement.onerror?.()
      await vi.advanceTimersByTimeAsync(0)
      const result = observed
      controller.abort()
      await next
      expect(result).toMatchObject({ error: 'NetworkError' })
    } finally { controller.abort(); Object.defineProperty(globalThis, 'clearTimeout', original); transport.close() }
    expect(vi.getTimerCount()).toBe(0)
  })

  it('ignores a dequeued connection timeout from a retired socket', async () => {
    const host = queuedTransportTimeouts(11)
    const transport = new WsTransport('ws://localhost/api', { timeout: 11 })
    try {
      const first = transport.call('live').catch((error: unknown) => error)
      const old = FakeWebSocket.instances[0]
      await vi.advanceTimersByTimeAsync(11)
      old.close()
      expect(await first).toMatchObject({ error: 'NetworkError' })
      const next = transport.call('live')
      const socket = FakeWebSocket.instances[1]
      socket.open()
      await Promise.resolve()
      host.flush()
      socket.message('{"$id":1,"data":42}')
      await expect(next).resolves.toBe(42)
      expect(socket.readyState).toBe(FakeWebSocket.OPEN)
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close(); host.restore() }
  })

  it('ignores a dequeued completed-call timeout without repeating listener cleanup', async () => {
    const host = queuedTransportTimeouts(11)
    const transport = new WsTransport('ws://localhost/api', { timeout: 11 })
    const controller = new AbortController()
    const remove = vi.spyOn(controller.signal, 'removeEventListener')
    try {
      const first = transport.call('live', undefined, { signal: controller.signal })
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await vi.advanceTimersByTimeAsync(11)
      expect(host.count()).toBe(1)
      socket.message('{"$id":1,"data":42}')
      await expect(first).resolves.toBe(42)
      const cleaned = remove.mock.calls.length
      const next = transport.call('live').catch((error: unknown) => error)
      await Promise.resolve()
      host.flush()
      expect(remove).toHaveBeenCalledTimes(cleaned)
      socket.message('{"$id":2,"data":77}')
      await expect(next).resolves.toBe(77)
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close(); host.restore() }
  })

  it.each(['success', 'failure'] as const)('ignores a dequeued subscription deadline after ACK %s', async ack => {
    const host = queuedTransportTimeouts(11)
    const transport = new WsTransport('ws://localhost/api', { timeout: 11 })
    const onData = vi.fn()
    try {
      const pending = transport.subscribe('live', {}, value => value, onData).catch((error: unknown) => error)
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await vi.advanceTimersByTimeAsync(11)
      expect(host.count()).toBe(1)
      socket.message(ack === 'success' ? '{"$sub":1,"ok":true}' : '{"$sub":1,"error":"Denied","code":403,"message":"denied"}')
      const result = await pending
      host.flush()
      socket.message('{"$stream":1,"data":42}')
      if (ack === 'success') {
        expect(onData).toHaveBeenCalledExactlyOnceWith(42)
        expect(typeof result).toBe('function')
        if (typeof result === 'function') result()
      } else {
        expect(result).toMatchObject({ error: 'Denied' })
        expect(onData).not.toHaveBeenCalled()
      }
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close(); host.restore() }
  })

  it.each(['json', 'binary'] as const)('keeps the replacement socket after a reentrant %s payload getter retires its source', async mode => {
    const transport = new WsTransport('ws://localhost/api', { mode })
    transport.setSchema({ live: { id: 7 } })
    let next: Promise<unknown> | undefined
    try {
      const first = transport.call('live').catch((error: unknown) => error)
      const old = FakeWebSocket.instances[0]
      old.open()
      await Promise.resolve()
      let retired = false
      old.onmessage?.({ get data() {
        if (!retired) {
          retired = true
          old.close()
          next = transport.call('live')
          FakeWebSocket.instances[1].open()
        }
        return mode === 'json' ? '[' : Uint8Array.of(255).buffer
      } })
      expect(await first).toMatchObject({ error: 'NetworkError' })
      await Promise.resolve()
      const socket = FakeWebSocket.instances[1]
      socket.message(mode === 'json' ? '{"$id":2,"data":42}' : sequenceFrame(0x02, 2n, [42]).buffer)
      await expect(next).resolves.toEqual(mode === 'json' ? 42 : new Uint8Array([42]))
      expect(socket.readyState).toBe(FakeWebSocket.OPEN)
    } finally { transport.close() }
  })
})

async function withInheritedEnvelopeField<T>(key: string, value: unknown, run: () => Promise<T>): Promise<T> {
  const original = Object.getOwnPropertyDescriptor(Object.prototype, key)
  Object.defineProperty(Object.prototype, key, { configurable: true, writable: true, value })
  try { return await run() }
  finally {
    if (original) Object.defineProperty(Object.prototype, key, original)
    else delete (Object.prototype as Record<string, unknown>)[key]
  }
}

describe('own JSON envelope fields', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket)
  })
  afterEach(() => { vi.clearAllTimers(); vi.useRealTimers() })

  it.each(['call', 'subscribe'] as const)('keeps retired %s work detached when a native JSON wrapper closes the transport', async entry => {
    const transport = new WsTransport('ws://localhost/api')
    try {
      const pending = entry === 'call' ? transport.call('live') : transport.subscribe('live', {}, value => value, vi.fn())
      const rejected = expect(pending).rejects.toMatchObject({ error: 'NetworkError' })
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      const original = JSON.parse
      const parse = vi.spyOn(JSON, 'parse').mockImplementation((...args) => {
        const result = original(...args)
        transport.close()
        return result
      })
      try { socket.message(entry === 'call' ? '{"$id":1,"data":42}' : '{"$sub":1,"ok":true}') }
      finally { parse.mockRestore() }
      await rejected
      expect(socket.readyState).toBe(3)
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close() }
  })

  it('isolates a non-Error resource validator failure and keeps later calls usable', async () => {
    const transport = new WsTransport('ws://localhost/api')
    try {
      const pending = transport.call('live')
      const rejected = expect(pending).rejects.toMatchObject({ error: 'ParseError', message: 'invalid JSON response resources' })
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      const validate = vi.spyOn(JSONEnvelopeSource.prototype, 'validateData').mockImplementation(() => { throw 'unexpected validator failure' })
      try { socket.message('{"$id":1,"data":42}') }
      finally { validate.mockRestore() }
      await rejected
      const next = transport.call('live')
      await Promise.resolve()
      socket.message('{"$id":2,"data":42}')
      await expect(next).resolves.toBe(42)
      expect(socket.readyState).toBe(FakeWebSocket.OPEN)
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close() }
  })

  it('ignores an inherited HTTP error marker in a successful response', async () => {
    const transport = new FetchTransport('http://localhost/api')
    mockFetch.mockResolvedValueOnce(new Response('{"data":42}'))
    await withInheritedEnvelopeField('error', 'inherited', async () => {
      await expect(transport.call('live')).resolves.toBe(42)
    })
    expect(vi.getTimerCount()).toBe(0)
  })

  it.each(['error', 'code', 'message'] as const)('rejects a missing own HTTP %s before token renewal', async field => {
    const envelope = { error: 'Unauthorized', code: 401, message: 'expired' }
    const value = envelope[field]
    const body: Record<string, unknown> = { ...envelope }
    delete body[field]
    const refresh = vi.fn(async () => 'fresh')
    const transport = new FetchTransport('http://localhost/api', { onTokenExpired: refresh })
    mockFetch.mockResolvedValueOnce(new Response(JSON.stringify(body), { status: 401 }))
      .mockResolvedValueOnce(new Response('{"data":42}'))
    await withInheritedEnvelopeField(field, value, async () => {
      await expect(transport.call('live')).rejects.toMatchObject({ error: 'ParseError', code: 401 })
    })
    expect(refresh).not.toHaveBeenCalled()
    expect(mockFetch).toHaveBeenCalledTimes(1)
    expect(vi.getTimerCount()).toBe(0)
  })

  it.each(['$end', '$sub', '$stream'])('routes a call by its own ID despite inherited %s', async key => {
    const transport = new WsTransport('ws://localhost/api', { timeout: 10 })
    try {
      const pending = transport.call('live')
      const result = pending.catch((error: unknown) => error)
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      await withInheritedEnvelopeField(key, 99, async () => {
        socket.message('{"$id":1,"data":42}')
        await vi.advanceTimersByTimeAsync(10)
        await expect(result).resolves.toBe(42)
      })
    } finally { transport.close() }
  })

  it('ignores an inherited WS error marker for a successful call', async () => {
    const transport = new WsTransport('ws://localhost/api')
    try {
      const pending = transport.call('live')
      const result = expect(pending).resolves.toBe(42)
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      await withInheritedEnvelopeField('error', 'inherited', async () => {
        socket.message('{"$id":1,"data":42}')
        await result
      })
    } finally { transport.close() }
  })

  it.each(['$id', '$sub', '$stream'])('finishes a subscription despite inherited %s', async key => {
    const transport = new WsTransport('ws://localhost/api')
    const onComplete = vi.fn(), onError = vi.fn()
    try {
      const pending = transport.subscribe('live', {}, value => value, vi.fn(), { onComplete, onError })
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      socket.message('{"$sub":1,"ok":true}')
      const stop = await pending
      await withInheritedEnvelopeField(key, 99, async () => { socket.message('{"$end":1,"ok":true}') })
      expect(onComplete).toHaveBeenCalledTimes(1)
      expect(onError).not.toHaveBeenCalled()
      stop()
    } finally { transport.close() }
  })

  it('does not acknowledge a subscription with an inherited ok marker', async () => {
    const transport = new WsTransport('ws://localhost/api')
    let acknowledged = false
    try {
      const pending = transport.subscribe('live', {}, value => value, vi.fn()).then(stop => { acknowledged = true; return stop })
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      await withInheritedEnvelopeField('ok', true, async () => {
        socket.message('{"$sub":1}')
        await vi.advanceTimersByTimeAsync(0)
      })
      const premature = acknowledged
      socket.message('{"$sub":1,"ok":true}')
      const stop = await pending
      expect(premature).toBe(false)
      stop()
    } finally { transport.close() }
  })

  it.each(['error', 'data', 'ok'] as const)('uses own stream completion metadata despite inherited %s', async field => {
    const transport = new WsTransport('ws://localhost/api')
    const onComplete = vi.fn(), onError = vi.fn()
    try {
      const pending = transport.subscribe('live', {}, value => value, vi.fn(), { onComplete, onError })
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      socket.message('{"$sub":1,"ok":true}')
      const stop = await pending
      await withInheritedEnvelopeField(field, true, async () => {
        socket.message(field === 'ok' ? '{"$end":1}' : '{"$end":1,"ok":true}')
      })
      if (field === 'ok') {
        expect(onComplete).not.toHaveBeenCalled()
        expect(onError).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ error: 'ParseError' }))
      } else {
        expect(onComplete).toHaveBeenCalledTimes(1)
        expect(onError).not.toHaveBeenCalled()
      }
      stop()
    } finally { transport.close() }
  })
})

describe('transport public boundary regressions', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket)
  })
  afterEach(() => { vi.clearAllTimers(); vi.useRealTimers() })

  const codec = { apiID: 7, initialCapacity: () => 8, writeParameters() {} }

  it('rejects an unregistered legacy binary HTTP call before network dispatch', async () => {
    const transport = new FetchTransport('http://localhost/api', { mode: 'binary' })
    await expect(transport.call('missing')).rejects.toMatchObject({ error: 'ConfigError' })
    expect(mockFetch).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
  })

  it.each(['http', 'ws'] as const)('rejects a mismatched generated JSON API ID over %s', async kind => {
    const transport = kind === 'http' ? new FetchTransport('http://localhost/api') : new WsTransport('ws://localhost/api')
    transport.setSchema({ live: { id: 8 } })
    const failed = expect(transport.callGenerated('live', codec)).rejects.toMatchObject({ error: 'ConfigError' })
    try {
      if (transport instanceof WsTransport) FakeWebSocket.instances[0].open()
      await failed
      expect(mockFetch).not.toHaveBeenCalled()
      expect(FakeWebSocket.instances[0]?.sent ?? []).toHaveLength(0)
      expect(vi.getTimerCount()).toBe(0)
    } finally { if (transport instanceof WsTransport) transport.close() }
  })

  it.each(['json', 'binary'] as const)('wraps a non-Error %s HTTP executor rejection', async mode => {
    const transport = new FetchTransport('http://localhost/api', { mode })
    transport.setSchema({ live: { id: 7 } })
    mockFetch.mockRejectedValueOnce('executor unavailable')
    await expect(transport.call('live')).rejects.toMatchObject({ error: 'NetworkError', message: 'executor unavailable' })
    expect(vi.getTimerCount()).toBe(0)
  })

  it.each(['http', 'ws'] as const)('keeps a generated %s codec failure when its observer throws', async kind => {
    const observer = vi.fn(() => { throw new Error('observer failed') })
    const transport = kind === 'http'
      ? new FetchTransport('http://localhost/api', { mode: 'binary', observer })
      : new WsTransport('ws://localhost/api', { mode: 'binary', observer })
    transport.setSchema({ live: { id: 7 } })
    const broken = { ...codec, writeParameters() { throw 'codec failed' } }
    const failed = expect(transport.callGenerated('live', broken)).rejects.toBe('codec failed')
    try {
      if (transport instanceof WsTransport) FakeWebSocket.instances[0].open()
      await failed
      expect(observer).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ status: 'error', errorCode: undefined }))
      expect(mockFetch).not.toHaveBeenCalled()
      expect(FakeWebSocket.instances[0]?.sent ?? []).toHaveLength(0)
      expect(vi.getTimerCount()).toBe(0)
    } finally { if (transport instanceof WsTransport) transport.close() }
  })

  it('keeps a generated binary WS success when its observer throws', async () => {
    const observer = vi.fn(() => { throw new Error('observer failed') })
    const transport = new WsTransport('ws://localhost/api', { mode: 'binary', observer })
    transport.setSchema({ live: { id: 7 } })
    try {
      const pending = transport.callGenerated('live', codec)
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      socket.message(sequenceFrame(0x02, 1n, [42, 7]).buffer)
      await expect(pending).resolves.toEqual(new Uint8Array([42, 7]))
      expect(observer).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ status: 'success', responseBytes: 2 }))
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close() }
  })

  it.each(['call', 'generated'] as const)('rejects a pre-cancelled WS %s before constructing a socket', async entry => {
    const controller = new AbortController()
    controller.abort()
    const transport = new WsTransport('ws://localhost/api')
    transport.setSchema({ live: { id: 7 } })
    try {
      const pending = entry === 'call' ? transport.call('live', undefined, { signal: controller.signal })
        : transport.callGenerated('live', codec, undefined, { signal: controller.signal })
      await expect(pending).rejects.toMatchObject({ error: 'CancelledError' })
      expect(FakeWebSocket.instances).toHaveLength(0)
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close() }
  })

  it.each(['call', 'generated'] as const)('rechecks cancellation when a WS %s signal getter aborts on connection lookup', async entry => {
    const controller = new AbortController()
    let reads = 0
    const options = { get signal() { if (++reads === 2) controller.abort(); return controller.signal } }
    const transport = new WsTransport('ws://localhost/api')
    transport.setSchema({ live: { id: 7 } })
    try {
      const pending = entry === 'call' ? transport.call('live', undefined, options)
        : transport.callGenerated('live', codec, undefined, options)
      await expect(pending).rejects.toMatchObject({ error: 'CancelledError' })
      expect(reads).toBe(2)
      expect(FakeWebSocket.instances).toHaveLength(0)
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close() }
  })

  it('retires known JSON operations when native parsing refuses a deeply nested opaque subtree', async () => {
    const transport = new WsTransport('ws://localhost/api')
    transport.setSchema(responseSchema)
    const onError = vi.fn(), onData = vi.fn()
    try {
      const subscription = transport.subscribe('live', {}, value => value, onData, { onError })
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await vi.advanceTimersByTimeAsync(0)
      socket.message('{"$sub":1,"ok":true}')
      const stop = await subscription
      const pending = transport.call('live')
      const failed = expect(pending).rejects.toMatchObject({ error: 'ParseError' })
      await Promise.resolve()
      const parse = vi.spyOn(JSON, 'parse')
      try {
        socket.message('{"$id":2,"data":{"future":' + responseJSON(20000) + '}}')
        await failed
        expect(parse).toHaveBeenCalledTimes(1)
      } finally { parse.mockRestore() }
      expect(onError).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ error: 'ParseError' }))
      expect(onData).not.toHaveBeenCalled()
      expect(socket.readyState).toBe(3)
      stop()
    } finally { transport.close() }
    expect(vi.getTimerCount()).toBe(0)
  })

  it('rejects an unexpected host message payload and settles its pending call', async () => {
    const transport = new WsTransport('ws://localhost/api')
    try {
      const pending = transport.call('live')
      const failed = expect(pending).rejects.toMatchObject({ error: 'ParseError' })
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      socket.message(new Blob(['unexpected host payload']))
      await failed
      expect(socket.readyState).toBe(3)
    } finally { transport.close() }
    expect(vi.getTimerCount()).toBe(0)
  })

  it('observes a signal wrapper aborting during connection listener registration', async () => {
    const controller = new AbortController()
    const register = controller.signal.addEventListener.bind(controller.signal)
    const remove = vi.spyOn(controller.signal, 'removeEventListener')
    vi.spyOn(controller.signal, 'addEventListener').mockImplementation((...args) => {
      controller.abort()
      register(...args)
    })
    const transport = new WsTransport('ws://localhost/api')
    try {
      const pending = transport.call('live', undefined, { signal: controller.signal })
      await expect(pending).rejects.toMatchObject({ error: 'CancelledError' })
      const socket = FakeWebSocket.instances[0]
      expect(socket.sent).toHaveLength(0)
      expect(remove).toHaveBeenCalledTimes(1)
      socket.open()
      await vi.advanceTimersByTimeAsync(0)
      expect(socket.sent).toHaveLength(0)
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close() }
  })

  it('settles cancellation triggered by a pending-call listener wrapper before request registration', async () => {
    const transport = new WsTransport('ws://localhost/api')
    try {
      const first = transport.call('live')
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      socket.message('{"$id":1,"data":42}')
      await first
      const controller = new AbortController()
      const register = controller.signal.addEventListener.bind(controller.signal)
      const remove = vi.spyOn(controller.signal, 'removeEventListener')
      vi.spyOn(controller.signal, 'addEventListener').mockImplementation((...args) => {
        register(...args)
        controller.abort()
      })
      await expect(transport.call('live', undefined, { signal: controller.signal })).rejects.toMatchObject({ error: 'CancelledError' })
      expect(socket.sent).toHaveLength(1)
      expect(remove).toHaveBeenCalledTimes(1)
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close() }
  })

  it.each(['call', 'generated'] as const)('cancels WS %s after an open connection resolves without sending', async entry => {
    const transport = new WsTransport('ws://localhost/api')
    transport.setSchema({ live: { id: 7 } })
    try {
      const first = transport.call('live')
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      socket.message('{"$id":1,"data":42}')
      await first
      const controller = new AbortController()
      const pending = entry === 'call' ? transport.call('live', undefined, { signal: controller.signal })
        : transport.callGenerated('live', codec, undefined, { signal: controller.signal })
      controller.abort()
      await expect(pending).rejects.toMatchObject({ error: 'CancelledError' })
      expect(socket.sent).toHaveLength(1)
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close() }
  })

  it.each(['call', 'subscribe'] as const)('wraps non-Error WS %s send failures and clears the timeout', async entry => {
    const transport = new WsTransport('ws://localhost/api')
    try {
      const pending = entry === 'call' ? transport.call('live') : transport.subscribe('live', {}, value => value, vi.fn())
      const failed = expect(pending).rejects.toThrow('send unavailable')
      const socket = FakeWebSocket.instances[0]
      socket.send = () => { throw 'send unavailable' }
      socket.open()
      await failed
      expect(vi.getTimerCount()).toBe(0)
      socket.message(entry === 'call' ? '{"$id":1,"data":42}' : '{"$sub":1,"ok":true}')
      expect(socket.readyState).toBe(FakeWebSocket.OPEN)
    } finally { transport.close() }
  })

  it('ignores duplicate subscription acknowledgements and isolates a later rejection', async () => {
    const transport = new WsTransport('ws://localhost/api')
    const onError = vi.fn(), onData = vi.fn(), siblingData = vi.fn()
    try {
      const first = transport.subscribe('live', {}, value => value, onData, { onError })
      const second = transport.subscribe('live', {}, value => value, siblingData)
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await vi.advanceTimersByTimeAsync(0)
      socket.message('{"$sub":1,"ok":true}')
      socket.message('{"$sub":2,"ok":true}')
      const stop = await first, stopSibling = await second
      socket.message('{"$sub":1,"ok":true}')
      socket.message('{"$sub":1,"error":"Gone","code":410,"message":"retired"}')
      expect(onError).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ error: 'Gone', code: 410 }))
      socket.message('{"$stream":1,"data":1}')
      socket.message('{"$stream":2,"data":2}')
      expect(onData).not.toHaveBeenCalled()
      expect(siblingData).toHaveBeenCalledExactlyOnceWith(2)
      stop()
      expect(socket.sent).toHaveLength(2)
      stopSibling()
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close() }
  })

  it('retires a subscription during socket closing without sending an advisory cancellation', async () => {
    const transport = new WsTransport('ws://localhost/api')
    const onData = vi.fn()
    try {
      const pending = transport.subscribe('live', {}, value => value, onData)
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      socket.message('{"$sub":1,"ok":true}')
      const stop = await pending
      socket.readyState = 2
      stop()
      socket.message('{"$stream":1,"data":42}')
      expect(onData).not.toHaveBeenCalled()
      expect(socket.sent).toHaveLength(1)
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close() }
  })
})

describe('WsTransport subscriptions', () => {
  beforeEach(() => {
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket)
  })

  it.each(['call', 'generated-call', 'subscribe', 'generated-subscribe'].flatMap(kind =>
    (['json', 'binary'] as const).map(mode => ({ kind, mode }))))
  ('retains the $mode operation across connection-time reconfiguration ($kind)', async ({ kind, mode }) => {
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    transport.setSchema(responseSchema)
    const onError = vi.fn(), onData = vi.fn()
    const codec = { apiID: 7, initialCapacity: () => 8, writeParameters() {} }
    const stream = kind.includes('subscribe')
    const pending = kind === 'call' ? transport.call('live')
      : kind === 'generated-call' ? transport.callGenerated('live', codec)
        : kind === 'subscribe' ? transport.subscribe('live', {}, data => data, onData, { onError })
          : transport.subscribeGenerated('live', codec, undefined, data => data, onData, { onError })
    const rejected = !stream && mode === 'json' ? expect(pending).rejects.toMatchObject({ error: 'ParseError' }) : undefined
    transport.setMode(mode === 'json' ? 'binary' : 'json')
    transport.setSchema({})
    const socket = FakeWebSocket.instances[0]
    socket.open()
    await Promise.resolve()
    await Promise.resolve()
    try {
      expect(typeof socket.sent[0]).toBe(mode === 'json' ? 'string' : 'object')
      if (stream) {
        socket.message(mode === 'json' ? '{"$sub":1,"ok":true}' : Uint8Array.of(0x13, 1).buffer)
        await pending
      }
      const route = stream ? '$stream' : '$id'
      if (mode === 'binary') {
        const parse = vi.spyOn(JSON, 'parse')
        try {
          socket.message('{"' + route + '":1,"data":' + responseNode(65) + '}')
          expect(parse).not.toHaveBeenCalled()
        } finally { parse.mockRestore() }
        socket.message(Uint8Array.of(stream ? 0x12 : 2, 1, 42).buffer)
        if (stream) expect(onData).toHaveBeenCalledWith(Uint8Array.of(42))
        else await expect(pending).resolves.toEqual(Uint8Array.of(42))
      } else {
        socket.message('{"' + route + '":1,"data":' + responseNode(65) + ',"data":null}')
        if (stream) expect(onError).toHaveBeenCalledTimes(1)
        else await rejected
      }
      expect(socket.readyState).toBe(FakeWebSocket.OPEN)
    } finally { transport.close() }
  })

  it.each(['$sub', '$end', '$id'])('isolates overlimit error data routed through %s', async route => {
    const transport = new WsTransport('ws://localhost/luvia')
    const onError = vi.fn()
    const stream = route !== '$id'
    const pending = stream ? transport.subscribe('live', {}, data => data, () => {}, { onError }) : transport.call('live')
    const rejected = route !== '$end' ? expect(pending).rejects.toMatchObject({ error: 'ParseError' }) : undefined
    const socket = FakeWebSocket.instances[0]
    socket.open()
    await Promise.resolve()
    await Promise.resolve()
    try {
      if (route === '$end') { socket.message('{"$sub":1,"ok":true}'); await pending }
      socket.message('{"' + route + '":1,"data":' + responseJSON(129) + ',"data":0,"error":"bad","code":400,"message":"bad"}')
      if (route === '$end') expect(onError).toHaveBeenCalledTimes(1)
      else await rejected
      expect(socket.readyState).toBe(FakeWebSocket.OPEN)
    } finally { transport.close() }
  })

  it('isolates overwritten source limits and keeps the response declaration captured before connecting', async () => {
    const transport = new WsTransport('ws://localhost/luvia')
    transport.setSchema(responseSchema)
    const first = transport.call('live')
    const failed = expect(first).rejects.toMatchObject({ error: 'ParseError' })
    transport.setSchema({})
    const second = transport.call('live')
    const socket = FakeWebSocket.instances[0]
    socket.open()
    await Promise.resolve()
    await Promise.resolve()
    const parse = vi.spyOn(JSON, 'parse')
    try {
      socket.message('{"$id":1,"data":{"next":' + responseNode(64) + ',"next":null}}')
      await failed
      expect(parse).not.toHaveBeenCalled()
      socket.message('{"$id":2,"data":42}')
      await expect(second).resolves.toBe(42)
      expect(socket.readyState).toBe(FakeWebSocket.OPEN)
    } finally { parse.mockRestore(); transport.close() }
  })

  it('rejects a bad stream source once while an active sibling continues', async () => {
    const transport = new WsTransport('ws://localhost/luvia')
    transport.setSchema(responseSchema)
    const onError = vi.fn(), onData = vi.fn(), siblingData = vi.fn()
    const first = transport.subscribe('live', {}, data => data, onData, { onError })
    const second = transport.subscribe('live', {}, data => data, siblingData)
    const socket = FakeWebSocket.instances[0]
    socket.open()
    await Promise.resolve()
    await Promise.resolve()
    socket.message('{"$sub":1,"ok":true}')
    socket.message('{"$sub":2,"ok":true}')
    await first
    const stop = await second
    const parse = vi.spyOn(JSON, 'parse')
    try {
      socket.message('{"$stream":1,"data":{"json":' + responseJSON(129) + ',"json":0}}')
      expect(parse).not.toHaveBeenCalled()
      expect(onError).toHaveBeenCalledTimes(1)
      expect(onError.mock.calls[0][0]).toMatchObject({ error: 'ParseError' })
      expect(onData).not.toHaveBeenCalled()
      socket.message('{"$stream":1,"data":' + responseJSON(200) + '}')
      expect(parse).not.toHaveBeenCalled()
      socket.message('{"$stream":2,"data":{}}')
      expect(siblingData).toHaveBeenCalledWith({})
      expect(socket.readyState).toBe(FakeWebSocket.OPEN)
      stop()
    } finally { parse.mockRestore(); transport.close() }
  })

  it('checks syntax for unknown IDs without parsing values and retires malformed messages', async () => {
    const transport = new WsTransport('ws://localhost/luvia')
    const pending = transport.call('live')
    const failed = expect(pending).rejects.toMatchObject({ error: 'ParseError' })
    const socket = FakeWebSocket.instances[0]
    socket.open()
    await Promise.resolve()
    await Promise.resolve()
    const parse = vi.spyOn(JSON, 'parse')
    try {
      socket.message('{"$id":99,"data":' + responseJSON(200) + '}')
      expect(parse).not.toHaveBeenCalled()
      socket.message('{"$id":99,"data":' + responseJSON(200) + ',"x":[0,]}')
      await failed
      expect(parse).not.toHaveBeenCalled()
      expect(socket.readyState).toBe(3)
    } finally { parse.mockRestore(); transport.close() }
  })

  it.each(['call', 'generated-call', 'subscribe', 'generated-subscribe'])
  ('rejects serialized JSON129 before %s sends and retains its schema snapshot', async kind => {
    const schema = { live: { id: 7, params: [{ fieldID: 1, name: 'value', type: 'JSON' }] } }
    const transport = new WsTransport('ws://localhost/luvia')
    transport.setSchema(schema)
    let value: unknown = 0
    for (let i = 0; i < 129; i++) value = [value]
    let calls = 0
    const params = { value: { toJSON() { calls++; transport.setSchema({}); return value } } }
    const codec = { apiID: 7, initialCapacity: () => 8, writeParameters() {} }
    try {
      const pending = kind === 'call' ? transport.call('live', params)
        : kind === 'generated-call' ? transport.callGenerated('live', codec, params)
          : kind === 'subscribe' ? transport.subscribe('live', params, data => data, () => {})
            : transport.subscribeGenerated('live', codec, params, data => data, () => {})
      const rejected = expect(pending).rejects.toThrow(/JSON nesting exceeds 128 levels/)
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await rejected
      expect(calls).toBe(1)
      expect(socket.sent).toEqual([])
      transport.setSchema(schema)
      const recovery = transport.call('live', { value: 0 })
      await Promise.resolve()
      await Promise.resolve()
      socket.message('{"$id":2,"data":42}')
      await expect(recovery).resolves.toBe(42)
    } finally { transport.close() }
  })

  it.each(['json', 'binary'] as const)('bounds a stalled %s handshake for every waiter', async mode => {
    vi.useFakeTimers()
    const transport = new WsTransport('ws://localhost/luvia', { mode, timeout: 50 })
    transport.setSchema({ live: { id: 7 } })
    const codec = { apiID: 7, initialCapacity: () => 8, writeParameters() {} }
    const failures: unknown[] = []
    const observe = (promise: Promise<unknown>) => promise.catch(error => { failures.push(error) })
    try {
      const waiting = [
        observe(transport.call('live')),
        observe(transport.callGenerated('live', codec)),
        observe(transport.subscribe('live', {}, value => value, () => {})),
        observe(transport.subscribeGenerated('live', codec, undefined, value => value, () => {})),
      ]
      const stale = FakeWebSocket.instances[0]
      await vi.advanceTimersByTimeAsync(50)
      expect(failures).toHaveLength(4)
      expect(failures).toEqual(Array.from({ length: 4 }, () => expect.objectContaining({ error: 'TimeoutError' })))
      await Promise.all(waiting)
      expect(stale.readyState).toBe(3)
      expect(stale.sent).toEqual([])
      const retried = transport.call('live')
      const socket = FakeWebSocket.instances[1]
      stale.open()
      expect(stale.readyState).toBe(3)
      socket.open()
      await Promise.resolve()
      socket.message(mode === 'json' ? '{"$id":1,"data":true}' : sequenceFrame(2, 1n, [1]).buffer)
      await expect(retried).resolves.toBeDefined()
      await vi.advanceTimersByTimeAsync(50)
      expect(socket.readyState).toBe(FakeWebSocket.OPEN)
      expect(FakeWebSocket.instances).toHaveLength(2)
    } finally {
      transport.close()
      vi.clearAllTimers()
      vi.useRealTimers()
    }
  })

  it.each(['json', 'binary'] as const)('isolates invalid %s error statuses from peer work', async mode => {
    const refresh = vi.fn(async () => 'renewed')
    const transport = new WsTransport('ws://localhost/luvia', { mode, onTokenExpired: refresh })
    transport.setSchema({ live: { id: 7 } })
    const onError = vi.fn(), onComplete = vi.fn(), onData = vi.fn()
    try {
      const call = transport.call('live')
      const rejected = expect(call).rejects.toMatchObject({ error: 'ParseError' })
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      const pending = transport.subscribe('live', {}, value => value, onData, { onError, onComplete })
      await Promise.resolve()
      socket.message(mode === 'json' ? '{"$sub":2,"ok":true}' : sequenceFrame(0x13, 2n, []).buffer)
      const cancel = await pending
      const peer = transport.call('live')
      await Promise.resolve()
      const enc = new Encoder()
      enc.writeFieldInt(1, 0n)
      enc.writeFieldString(2, 'Unauthorized')
      enc.writeFieldString(3, 'invalid')
      enc.writeEnd()
      socket.message(mode === 'json' ? '{"$id":1,"error":"Unauthorized","code":0,"message":"invalid"}'
        : sequenceFrame(0x03, 1n, [...enc.bytes()]).buffer)
      const end = mode === 'json' ? '{"$end":2,"error":"Unauthorized","code":0,"message":"invalid"}'
        : sequenceFrame(0x16, 2n, [...enc.bytes()]).buffer
      socket.message(end)
      socket.message(end)
      await rejected
      expect(onError).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ error: 'ParseError' }))
      expect(onComplete).not.toHaveBeenCalled()
      expect(onData).not.toHaveBeenCalled()
      expect(refresh).not.toHaveBeenCalled()
      expect(socket.readyState).toBe(FakeWebSocket.OPEN)
      socket.message(mode === 'json' ? '{"$id":3,"data":42}' : sequenceFrame(0x02, 3n, [84]).buffer)
      await expect(peer).resolves.toBeDefined()
      const count = socket.sent.length
      cancel()
      expect(socket.sent).toHaveLength(count)
    } finally { transport.close() }
  })

  it.each(['json', 'binary'] as const)('uses a static encoder for %s calls and subscriptions', async mode => {
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    transport.setSchema({ live: { id: 7, params: [{ fieldID: 1, name: 'value', type: 'Int' }] } })
    const values = Object.freeze({ value: 9007199254740993n })
    let writes = 0
    const codec = { apiID: 7, initialCapacity: () => 16, writeParameters(enc: Encoder, actual: typeof values | undefined) {
      expect(actual).toBe(values)
      writes++
      enc.writeVarint(1)
      enc.writeSvarint(actual!.value)
    } }
    try {
      const pending = transport.callGenerated('live', codec, values)
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      const body = encodeBinaryBody({ id: 7, params: [{ fieldID: 1, name: 'value', type: 'Int' }] }, values)
      expect(socket.sent[0]).toEqual(mode === 'binary' ? sequenceFrame(1, 1n, [...body]).buffer : '{"value":9007199254740993,"$id":1,"$api":"live"}')
      socket.message(mode === 'binary' ? sequenceFrame(2, 1n, [1]).buffer : '{"$id":1,"data":true}')
      await pending
      const subscribed = transport.subscribeGenerated('live', codec, values, data => data, () => {})
      await Promise.resolve()
      expect(socket.sent[1]).toEqual(mode === 'binary' ? sequenceFrame(0x10, 2n, [...body]).buffer : '{"value":9007199254740993,"$id":2,"$sub":"live"}')
      socket.message(mode === 'binary' ? sequenceFrame(0x13, 2n, []).buffer : '{"$sub":2,"ok":true}')
      const cancel = await subscribed
      cancel()
      expect(writes).toBe(mode === 'binary' ? 2 : 0)
    } finally { transport.close() }
  })

  it('sends sealed static JSON calls and subscriptions without materializing cases', async () => {
    const transport = new WsTransport('ws://localhost/luvia')
    transport.setSchema({ live: { id: 7, params: [{ fieldID: 1, name: 'value', type: 'Sealed', typeName: 'Outcome' }],
      fields: { Pending: { fieldID: 1, type: 'Model', typeName: 'Outcome.Pending' } },
      types: { '@sealed:Outcome': { Pending: { fieldID: 1, type: 'Model', typeName: 'Outcome.Pending' } }, 'Outcome.Pending': {} },
      response: { type: 'Sealed', typeName: 'Outcome' } } })
    const values = { value: { $case: 'Pending', toJSON() { throw Error('business case projected') } }, $select: 'Pending' }
    let writes = 0
    const codec = { apiID: 7, initialCapacity: () => 16, writeParameters() { throw Error('unexpected Binary') },
      writeJSONParameters(writer: import('./request').JSONRequestWriter, actual: typeof values | undefined) {
        expect(actual).toBe(values); writes++; writer.writeRaw(',"value":{"Pending":{}}')
      } }
    try {
      const pending = transport.callGenerated('live', codec, values)
      const socket = FakeWebSocket.instances[0]; socket.open(); await Promise.resolve()
      expect(JSON.parse(socket.sent[0] as string)).toEqual({ $id: 1, $api: 'live', value: { Pending: {} }, $select: 'Pending' })
      socket.message('{"$id":1,"data":{"Pending":{}}}')
      await expect(pending).resolves.toEqual({ Pending: {} })
      const received = vi.fn()
      const subscribed = transport.subscribeGenerated('live', codec, values, value => value, received)
      await Promise.resolve()
      expect(JSON.parse(socket.sent[1] as string)).toEqual({ $id: 2, $sub: 'live', value: { Pending: {} }, $select: 'Pending' })
      socket.message('{"$sub":2,"ok":true}')
      const cancel = await subscribed
      socket.message('{"$stream":2,"data":{"Pending":{}}}')
      expect(received).toHaveBeenCalledExactlyOnceWith({ Pending: {} })
      cancel(); expect(writes).toBe(2)
    } finally { transport.close() }
  })

  it('sends owned call, subscription and cancellation buffers without recopying', async () => {
    const encoded = vi.spyOn(Encoder.prototype, 'bytes')
    const transport = new WsTransport('ws://localhost/luvia', { mode: 'binary' })
    transport.setSchema({ live: { id: 7 } })
    try {
      const pending = transport.call('live')
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      expect(socket.sent[0]).toBe(encoded.mock.results.at(-1)?.value.buffer)
      const saved = new Uint8Array(socket.sent[0] as ArrayBuffer).slice()
      socket.message(sequenceFrame(2, 1n, [1]).buffer)
      await pending
      const subscribed = transport.subscribe('live', {}, value => value, () => {})
      await Promise.resolve()
      expect(socket.sent.at(-1)).toBe(encoded.mock.results.at(-1)?.value.buffer)
      socket.message(sequenceFrame(0x13, 2n, []).buffer)
      const cancel = await subscribed
      cancel()
      expect(socket.sent.at(-1)).toBe(encoded.mock.results.at(-1)?.value.buffer)
      expect(socket.sent.at(-1)).not.toBe(socket.sent[0])
      expect(new Uint8Array(socket.sent[0] as ArrayBuffer)).toEqual(saved)
    } finally {
      transport.close()
    }
  })

  it.each(['json', 'binary'] as const)('retains its %s registration after caller mutation or invalid updates', async mode => {
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    const schema = { live: { id: 7, params: [{ fieldID: 1, name: 'value', type: 'Int' }] } }
    transport.setSchema(schema)
    schema.live.id = 9
    schema.live.params[0].type = 'String'
    expect(() => transport.setSchema({ live: { id: 0 } })).toThrow(/ID/)
    try {
      const pending = transport.call('live', { value: 7n })
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      const sent = socket.sent[0]
      socket.message(mode === 'json' ? '{"$id":1,"data":true}' : sequenceFrame(2, 1n, [1]).buffer)
      await expect(pending).resolves.toEqual(mode === 'json' ? true : new Uint8Array([1]))
      expect(sent).toEqual(mode === 'json'
        ? '{"value":7,"$id":1,"$api":"live"}' : sequenceFrame(1, 1n, [7, 0, 1, 14, 0]).buffer)
      const onData = vi.fn()
      const subscribed = transport.subscribe('live', { value: 7n }, value => value, onData)
      await Promise.resolve()
      socket.message(mode === 'json' ? '{"$sub":2,"ok":true}' : sequenceFrame(0x13, 2n, []).buffer)
      const cancel = await subscribed
      socket.message(mode === 'json' ? '{"$stream":2,"data":true}' : sequenceFrame(0x12, 2n, [1]).buffer)
      expect(onData).toHaveBeenCalledExactlyOnceWith(mode === 'json' ? true : new Uint8Array([1]))
      cancel()
    } finally { transport.close() }
  })

  it.each(['json', 'binary'] as const)('isolates missing %s data without mistaking explicit null for completion', async mode => {
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    transport.setSchema({ live: { id: 7 }, echo: { id: 8 } })
    const onData = vi.fn(), onError = vi.fn(), onComplete = vi.fn(), peer = vi.fn()
    const decode = vi.fn((value: unknown) => value)
    const expected = mode === 'json' ? null : new Uint8Array([0])
    const response = (socket: FakeWebSocket, stream: boolean, id: number, present: boolean) => {
      socket.message(mode === 'json' ? JSON.stringify({ [stream ? '$stream' : '$id']: id, ...(present ? { data: null } : {}) })
        : sequenceFrame(stream ? 0x12 : 2, BigInt(id), present ? [0] : []).buffer)
    }
    try {
      const first = transport.subscribe('live', {}, decode, onData, { onError, onComplete })
      const second = transport.subscribe('live', {}, decode, peer)
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      for (const id of [1, 2]) socket.message(mode === 'json' ? `{"$sub":${id},"ok":true}` : sequenceFrame(0x13, BigInt(id), []).buffer)
      const [cancel, cancelPeer] = await Promise.all([first, second])
      response(socket, true, 1, true)
      expect(onData).toHaveBeenCalledExactlyOnceWith(expected)
      response(socket, true, 1, false)
      expect(onError).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ error: 'ParseError', code: 0 }))
      expect(socket.sent.at(-1)).toEqual(mode === 'json' ? '{"$unsub":1}' : sequenceFrame(0x11, 1n, []).buffer)
      response(socket, true, 1, false)
      socket.message(mode === 'json' ? '{"$end":1,"ok":true}' : sequenceFrame(0x15, 1n, []).buffer)
      expect(onError).toHaveBeenCalledTimes(1)
      expect(onComplete).not.toHaveBeenCalled()
      expect(decode).toHaveBeenCalledTimes(1)
      response(socket, true, 2, true)
      expect(peer).toHaveBeenCalledExactlyOnceWith(expected)
      const missing = transport.call('echo')
      const failure = expect(missing).rejects.toMatchObject({ error: 'ParseError', code: 0 })
      await Promise.resolve()
      response(socket, false, 3, false)
      await failure
      const valid = transport.call('echo')
      await Promise.resolve()
      response(socket, false, 4, true)
      await expect(valid).resolves.toEqual(expected)
      cancel(); cancelPeer()
    } finally { transport.close() }
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

  it.each(['json', 'binary'] as const)('retires %s decoding failures whose message getter throws', async mode => {
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    transport.setSchema({ live: { id: 71 } })
    const failure = new Error(), onError = vi.fn(), onData = vi.fn(), onComplete = vi.fn(), peer = vi.fn()
    Object.defineProperty(failure, 'message', { get() { throw new Error('message accessor failed') } })
    const decode = vi.fn(() => { throw failure })
    try {
      const first = transport.subscribe('live', {}, decode, onData, { onError, onComplete })
      const second = transport.subscribe('live', {}, value => value, peer)
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      for (const id of [1, 2]) socket.message(mode === 'json'
        ? `{"$sub":${id},"ok":true}` : sequenceFrame(0x13, BigInt(id), []).buffer)
      const [cancel, cancelPeer] = await Promise.all([first, second])
      const send = (id: number) => socket.message(mode === 'json'
        ? `{"$stream":${id},"data":7}` : sequenceFrame(0x12, BigInt(id), [7]).buffer)
      expect(() => send(1)).not.toThrow()
      send(1); cancel()
      socket.message(mode === 'json' ? '{"$end":1,"ok":true}' : sequenceFrame(0x15, 1n, []).buffer)
      expect(decode).toHaveBeenCalledOnce()
      expect(onData).not.toHaveBeenCalled()
      expect(onComplete).not.toHaveBeenCalled()
      expect(onError).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ error: 'DecodeError', message: 'invalid subscription response' }))
      send(2)
      expect(peer).toHaveBeenCalledExactlyOnceWith(mode === 'json' ? 7 : new Uint8Array([7]))
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
      socket.message(mode === 'json' ? '{"$end":-0,"ok":true}' : sequenceFrame(0x15, max - 1n, []).buffer)
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
          for (const id of [0n, 999n, 18446744073709551615n]) {
            socket.message(sequenceFrame(kind, id, kind === 0x12 ? [42] : []).buffer)
          }
        }
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

  it.each(['json', 'binary'] as const)('isolates replacement connections from late callbacks on the old socket (%s)', async mode => {
    vi.useFakeTimers()
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    transport.setSchema({ live: { id: 71 } })
    const ack = (id: number) => mode === 'json' ? `{"$sub":${id},"ok":true}` : sequenceFrame(0x13, BigInt(id), []).buffer
    const stream = (id: number, value: number) => mode === 'json' ? `{"$stream":${id},"data":${value}}` : sequenceFrame(0x12, BigInt(id), [value]).buffer
    const listener = vi.fn()
    try {
      const onError = vi.fn()
      const pending = transport.subscribe('live', {}, (value: unknown) => value, listener, { onError })
      const first = FakeWebSocket.instances[0]
      first.open()
      await Promise.resolve()
      first.message(ack(1))
      const cancel = await pending
      first.close()
      await vi.advanceTimersByTimeAsync(1000)
      const second = FakeWebSocket.instances[1]
      second.open()
      expect(second.sent).toEqual([])
      expect(onError).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ error: 'NetworkError' }))
      first.message(stream(1, 11))
      first.close()
      second.message(stream(1, 22))
      expect(listener).not.toHaveBeenCalled()
      cancel()
      expect(second.sent).toEqual([])
      const next = transport.subscribe('live', {}, (value: unknown) => value, listener)
      await Promise.resolve()
      second.message(ack(2))
      const cancelNext = await next
      first.message(stream(2, 99))
      first.message(mode === 'json' ? '{"$end":2}' : sequenceFrame(0x15, 2n, []).buffer)
      first.message('{')
      expect(onError).toHaveBeenCalledTimes(1)
      expect(second.readyState).toBe(FakeWebSocket.OPEN)
      second.message(stream(2, 33))
      expect(listener).toHaveBeenCalledExactlyOnceWith(mode === 'json' ? 33 : Uint8Array.of(33))
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

  it.each(['json', 'binary'] as const)('settles %s subscription timeout even when cancellation send throws', async mode => {
    vi.useFakeTimers()
    const transport = new WsTransport('ws://localhost/luvia', { mode, timeout: 5 })
    transport.setSchema({ live: { id: 71 } })
    try {
      const onData = vi.fn()
      const pending = transport.subscribe('live', {}, value => value, onData)
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      const send = socket.send.bind(socket)
      socket.send = () => { throw new Error('cancellation send failed') }
      let failure: unknown
      const rejected = (async () => { try { await pending } catch (error) { failure = error } })()
      await vi.advanceTimersByTimeAsync(5)
      await rejected
      expect(failure).toMatchObject({ error: 'TimeoutError' })
      socket.send = send
      socket.message(mode === 'json' ? '{"$sub":1,"ok":true}' : sequenceFrame(0x13, 1n, []).buffer)
      socket.message(mode === 'json' ? '{"$stream":1,"data":7}' : sequenceFrame(0x12, 1n, [7]).buffer)
      expect(onData).not.toHaveBeenCalled()
      const retry = transport.subscribe('live', {}, value => value, onData)
      await Promise.resolve()
      socket.message(mode === 'json' ? '{"$sub":2,"ok":true}' : sequenceFrame(0x13, 2n, []).buffer)
      const cancel = await retry
      cancel()
    } finally {
      transport.close()
      vi.clearAllTimers()
      vi.useRealTimers()
    }
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
    for (const event of ['error', 'close', 'parse', 'binary-parse'] as const) {
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
        else if (event === 'binary-parse') socket.message(new Uint8Array([2, 128]).buffer)
        else socket.close()
        await vi.advanceTimersByTimeAsync(0)
        expect(results).toHaveLength(2)
        await Promise.all([request, setup])
        const error = event === 'parse' || event === 'binary-parse' ? 'ParseError' : 'NetworkError'
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

  it.each([
    [], [2], [2, 0x81], [3, 0x81], [2, ...Array<number>(9).fill(128), 2],
    [2, ...Array<number>(10).fill(128), 0], [0x82, 0, 1, 0], [0xff, 1], [0x10, 1], [0x11, 1], [0x15, 0x80],
  ])('rejects malformed Binary header %j with ParseError', async (...bytes) => {
    const transport = new WsTransport('ws://localhost/luvia', { mode: 'binary', timeout: 10 })
    transport.setSchema({ health: { id: 7 } })
    try {
      const request = transport.call('health')
      const failed = expect(request).rejects.toMatchObject({ error: 'ParseError' })
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      socket.message(Uint8Array.from(bytes).buffer)
      await failed
      expect(socket.readyState).toBe(3)
    } finally { transport.close() }
  })

  it('rejects only a correlated Binary ACK with trailing bytes', async () => {
    const transport = new WsTransport('ws://localhost/luvia', { mode: 'binary', timeout: 10 })
    transport.setSchema({ live: { id: 7 } })
    const listener = vi.fn()
    try {
      const broken = transport.subscribe('live', {}, (value: unknown) => value, vi.fn())
      const failed = expect(broken).rejects.toMatchObject({ error: 'ParseError' })
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      const peer = transport.subscribe('live', {}, (value: unknown) => value, listener)
      const call = transport.call('live')
      await Promise.resolve()
      socket.message(sequenceFrame(0x13, 999n, [42]).buffer)
      socket.message(sequenceFrame(0x13, 1n, [42]).buffer)
      socket.message(sequenceFrame(0x13, 2n, []).buffer)
      socket.message(sequenceFrame(2, 3n, [42]).buffer)
      await failed
      const cancel = await peer
      socket.message(sequenceFrame(0x12, 2n, [7]).buffer)
      expect(listener).toHaveBeenCalledExactlyOnceWith(new Uint8Array([7]))
      await expect(call).resolves.toEqual(new Uint8Array([42]))
      expect(socket.readyState).toBe(1)
      cancel()
    } finally { transport.close() }
  })

  it('does not complete JSON subscriptions with malformed acknowledgements', async () => {
    const transport = new WsTransport('ws://localhost/luvia')
    let completed = false
    try {
      const subscribed = (async () => {
        const unsubscribe = await transport.subscribe('live', {}, (value: unknown) => value, () => {})
        completed = true
        return unsubscribe
      })()
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      for (const ok of ['1', '1.0', 'false', '"true"', 'null']) socket.message(`{"$sub":1,"ok":${ok}}`)
      socket.message('{"$sub":1}')
      await new Promise<void>(resolve => setTimeout(resolve, 0))
      expect(completed).toBe(false)
      socket.message('{"$sub":1,"ok":true}')
      ;(await subscribed)()
    } finally { transport.close() }
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

async function assertUpdatedWSConfig(transport: WsTransport, socket: FakeWebSocket, change: 'remove' | 'replace' | 'mode'): Promise<void> {
  const next = transport.call('health')
  if (change === 'remove') {
    await expect(next).rejects.toMatchObject({ error: 'ConfigError' })
    return
  }
  await Promise.resolve()
  if (change === 'mode') {
    expect(socket.sent.at(-1)).toBe('{"$id":2,"$api":"health"}')
    socket.message('{"$id":2,"data":true}')
  } else {
    expect(new Uint8Array(socket.sent.at(-1) as ArrayBuffer)).toEqual(new Uint8Array([1, 2, 9, 0, 0]))
    socket.message(new Uint8Array([2, 2, 14]).buffer)
  }
  await next
}

describe('WsTransport observability', () => {
  const secret = 'DO_NOT_LOG_THIS_SECRET'

  beforeEach(() => {
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket)
  })

  it('keeps monotonic deadlines when no observer is configured', async () => {
    const now = vi.spyOn(performance, 'now')
    const transport = new WsTransport('ws://localhost/luvia')
    const request = transport.call('health')
    const socket = FakeWebSocket.instances[0]
    socket.open()
    await Promise.resolve()
    socket.message(JSON.stringify({ $id: 1, data: true }))

    await expect(request).resolves.toBe(true)
    expect(now).toHaveBeenCalled()
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

describe('WsTransport retired call isolation', () => {
  beforeEach(() => {
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket)
    vi.useFakeTimers()
  })

  afterEach(() => { vi.clearAllTimers(); vi.useRealTimers() })

  it.each([false, true])('keeps the WS JSON plan when encoding replaces Schema (subscription=%s)', async subscription => {
    const transport = new WsTransport('ws://localhost/luvia')
    transport.setSchema({ echo: { id: 7, params: [{ fieldID: 1, name: 'value', type: 'Int' }] } })
    const request = new class extends GeneratedRequest {
      readonly apiID = 7
      writeParameters() {}
      toJSONParameters() {
        transport.setSchema({ echo: { id: 7, params: [{ fieldID: 1, name: 'value', type: 'Float' }] } })
        return { value: 1.5 }
      }
    }()
    try {
      const pending = subscription ? transport.subscribe('echo', request, (value: unknown) => value, vi.fn())
        : transport.call('echo', request)
      const rejected = expect(pending).rejects.toThrow(/bigint/)
      const socket = FakeWebSocket.instances[0]
      const send = socket.send.bind(socket)
      socket.send = data => {
        send(data)
        const message = JSON.parse(data as string) as { $id: number; $sub?: string }
        socket.message(message.$sub ? `{"$sub":${message.$id},"ok":true}` : `{"$id":${message.$id},"data":true}`)
      }
      socket.open()
      await rejected
      expect(socket.sent).toHaveLength(0)
      await transport.call('echo', { value: 1.5 })
      expect(socket.sent).toHaveLength(1)
    } finally { transport.close() }
  })

  it.each([
    ['json', 'static'], ['binary', 'static'], ['json', 'generated'], ['binary', 'generated'],
    ['json', 'dynamic'], ['binary', 'dynamic'],
  ] as const)('does not send a %s %s subscription closed while encoding', async (mode, entry) => {
    const transport = new WsTransport('ws://localhost/luvia', { mode })
    transport.setSchema({ health: { id: 7, params: [{ fieldID: 1, name: 'value', type: 'Int' }] } })
    const codec = {
      apiID: 7, initialCapacity: () => 16,
      writeParameters(encoder: Encoder) { transport.close(); encoder.writeFieldInt(1, 1n) },
    }
    const values = { get value() { transport.close(); return 1n } }
    const request = new class extends GeneratedRequest {
      readonly apiID = 7
      writeParameters(encoder: Encoder) { codec.writeParameters(encoder) }
      toJSONParameters() { transport.close(); return { value: 1n } }
    }()
    try {
      const decode = (value: unknown) => value
      const pending = entry === 'static' ? transport.subscribeGenerated('health', codec, values, decode, vi.fn())
        : transport.subscribe('health', entry === 'dynamic' ? values : request, decode, vi.fn())
      const rejected = expect(pending).rejects.toMatchObject({ error: 'NetworkError' })
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await rejected
      expect(socket.sent).toHaveLength(0)
      await expect(transport.subscribe('health', {}, decode, vi.fn())).rejects.toMatchObject({ error: 'NetworkError' })
      expect(FakeWebSocket.instances).toHaveLength(1)
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close() }
  })

  it.each([
    ['call', 'remove'], ['call', 'replace'], ['call', 'mode'],
    ['subscription', 'remove'], ['subscription', 'replace'], ['subscription', 'mode'],
  ] as const)('keeps a static %s snapshot when its encoder changes %s', async (entry, change) => {
    const transport = new WsTransport('ws://localhost/luvia', { mode: 'binary' })
    transport.setSchema({ health: { id: 7, params: [{ fieldID: 1, name: 'value', type: 'Int' }] } })
    const codec = {
      apiID: 7, initialCapacity: () => 16,
      writeParameters(encoder: Encoder) {
        if (change === 'mode') transport.setMode('json')
        else transport.setSchema(change === 'remove' ? {} : { health: { id: 9 } })
        encoder.writeFieldInt(1, 42n)
      },
    }
    const listener = vi.fn()
    const pending = entry === 'call' ? transport.callGenerated('health', codec)
      : transport.subscribeGenerated('health', codec, undefined, (value: unknown) => value, listener)
    void pending.catch(() => {})
    try {
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      expect(socket.sent).toHaveLength(1)
      expect(socket.sent[0]).toBeInstanceOf(ArrayBuffer)
      expect(new Uint8Array(socket.sent[0] as ArrayBuffer)).toEqual(
        new Uint8Array([entry === 'call' ? 1 : 0x10, 1, 7, 0, 1, 84, 0]),
      )
      if (entry === 'call') {
        socket.message(new Uint8Array([2, 1, 14]).buffer)
        await expect(pending).resolves.toEqual(new Uint8Array([14]))
      } else {
        socket.message(new Uint8Array([0x13, 1]).buffer)
        const cancel = await pending
        socket.message(new Uint8Array([0x12, 1, 14]).buffer)
        expect(listener).toHaveBeenCalledExactlyOnceWith(new Uint8Array([14]))
        expect(typeof cancel).toBe('function')
        if (typeof cancel === 'function') cancel()
        expect(new Uint8Array(socket.sent.at(-1) as ArrayBuffer)).toEqual(new Uint8Array([0x11, 1]))
      }
      await assertUpdatedWSConfig(transport, socket, change)
    } finally { transport.close() }
  })

  it.each([
    ['json', 'static'], ['binary', 'static'], ['json', 'generated'], ['binary', 'generated'],
  ] as const)('does not send a %s %s request aborted while encoding', async (mode, entry) => {
    const transport = new WsTransport('ws://localhost/luvia', { mode, timeout: 10 })
    transport.setSchema({ health: { id: 7, params: [{ fieldID: 1, name: 'value', type: 'Int' }] } })
    const controller = new AbortController()
    const codec = { apiID: 7, initialCapacity: () => 16, writeParameters(encoder: Encoder) { controller.abort(); encoder.writeFieldInt(1, 1n) } }
    const values = { get value() { controller.abort(); return 1n } }
    const request = new class extends GeneratedRequest {
      readonly apiID = 7
      writeParameters(encoder: Encoder) { codec.writeParameters(encoder) }
      toJSONParameters() { controller.abort(); return { value: 1n } }
    }()
    try {
      const options = { signal: controller.signal }
      const pending = entry === 'static' ? transport.callGenerated('health', codec, values, options)
        : transport.call('health', request, options)
      const rejected = expect(pending).rejects.toMatchObject({ error: 'CancelledError' })
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await rejected
      expect(socket.sent).toHaveLength(0)
      expect(vi.getTimerCount()).toBe(0)
      const next = transport.call('health', { value: 1n })
      await Promise.resolve()
      socket.message(mode === 'json' ? '{"$id":2,"data":42}' : sequenceFrame(0x02, 2n, [42]).buffer)
      await expect(next).resolves.toEqual(mode === 'json' ? 42 : new Uint8Array([42]))
      expect(socket.sent).toHaveLength(1)
    } finally { transport.close() }
  })

  it.each(['json', 'binary'] as const)('cleans up failed %s abort registration before sending', async mode => {
    const transport = new WsTransport('ws://localhost/luvia', { mode, timeout: 10 })
    transport.setSchema({ health: { id: 7 } })
    try {
      const connected = transport.call('health')
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      socket.message(mode === 'json' ? '{"$id":1,"data":42}' : sequenceFrame(0x02, 1n, [42]).buffer)
      await connected
      const controller = new AbortController()
      const failure = new Error('injected registration failure')
      const register = vi.spyOn(controller.signal, 'addEventListener').mockImplementation(() => { throw failure })
      const cleanup = vi.spyOn(controller.signal, 'removeEventListener')
      await expect(transport.call('health', undefined, { signal: controller.signal })).rejects.toBe(failure)
      expect(socket.sent).toHaveLength(1)
      expect(cleanup).toHaveBeenCalledTimes(1)
      expect(vi.getTimerCount()).toBe(0)
      register.mockRestore()
      const next = transport.call('health')
      await Promise.resolve()
      socket.message(mode === 'json' ? '{"$id":3,"data":42}' : sequenceFrame(0x02, 3n, [42]).buffer)
      await expect(next).resolves.toEqual(mode === 'json' ? 42 : new Uint8Array([42]))
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close() }
  })

  it.each(['json', 'binary'] as const)('cleans up a throwing %s send without affecting the next call', async mode => {
    const transport = new WsTransport('ws://localhost/luvia', { mode, timeout: 10 })
    transport.setSchema({ health: { id: 7 } })
    const controller = new AbortController()
    const cleanup = vi.spyOn(controller.signal, 'removeEventListener')
    const registered = vi.spyOn(controller.signal, 'addEventListener')
    const failure = new Error('injected send failure')
    try {
      const first = transport.call('health', undefined, { signal: controller.signal })
      const rejected = expect(first).rejects.toBe(failure)
      const socket = FakeWebSocket.instances[0]
      const send = socket.send.bind(socket)
      socket.send = () => { throw failure }
      socket.open()
      await rejected
      expect(cleanup).toHaveBeenCalledTimes(2)
      expect(cleanup.mock.calls.map(call => call[1])).toEqual(registered.mock.calls.map(call => call[1]))
      expect(vi.getTimerCount()).toBe(0)
      socket.send = send
      const next = transport.call('health')
      await Promise.resolve()
      controller.abort()
      socket.message(mode === 'json' ? '{"$id":1,"error":"Gone","code":400,"message":"late"}' : sequenceFrame(0x03, 1n, [255]).buffer)
      socket.message(mode === 'json' ? '{"$id":2,"data":42}' : sequenceFrame(0x02, 2n, [42]).buffer)
      await expect(next).resolves.toEqual(mode === 'json' ? 42 : new Uint8Array([42]))
      expect(cleanup).toHaveBeenCalledTimes(2)
      expect(vi.getTimerCount()).toBe(0)
      expect(socket.readyState).toBe(FakeWebSocket.OPEN)
      expect(FakeWebSocket.instances).toHaveLength(1)
    } finally { transport.close() }
  })

  it.each([
    ['json', 'cancel'], ['binary', 'cancel'],
    ['json', 'timeout'], ['binary', 'timeout'],
  ] as const)('isolates late %s responses after %s from sibling and future calls', async (mode, retirement) => {
    const transport = new WsTransport('ws://localhost/luvia', { mode, timeout: 10 })
    transport.setSchema({ health: { id: 7 } })
    const firstController = new AbortController()
    const secondController = new AbortController()
    const firstCleanup = vi.spyOn(firstController.signal, 'removeEventListener')
    const firstRegistered = vi.spyOn(firstController.signal, 'addEventListener')
    const secondCleanup = vi.spyOn(secondController.signal, 'removeEventListener')
    try {
      const first = transport.call('health', undefined, { signal: firstController.signal })
      const failed = expect(first).rejects.toMatchObject({ error: retirement === 'cancel' ? 'CancelledError' : 'TimeoutError' })
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await Promise.resolve()
      await vi.advanceTimersByTimeAsync(5)
      const second = transport.call('health', undefined, { signal: secondController.signal })
      await Promise.resolve()
      if (retirement === 'cancel') firstController.abort()
      else await vi.advanceTimersByTimeAsync(5)
      await failed
      socket.message(mode === 'json' ? '{"$id":1,"data":999}' : sequenceFrame(0x02, 1n, [99]).buffer)
      socket.message(mode === 'json' ? '{"$id":1,"error":"Gone","code":400,"message":"late"}' : sequenceFrame(0x03, 1n, [255]).buffer)
      socket.message(mode === 'json' ? '{"$id":2,"data":42}' : sequenceFrame(0x02, 2n, [42]).buffer)
      await expect(second).resolves.toEqual(mode === 'json' ? 42 : new Uint8Array([42]))
      secondController.abort()
      expect(firstCleanup).toHaveBeenCalledTimes(2)
      expect(firstCleanup.mock.calls.map(call => call[1])).toEqual(firstRegistered.mock.calls.map(call => call[1]))
      expect(secondCleanup).toHaveBeenCalledTimes(1)
      const third = transport.call('health')
      await Promise.resolve()
      socket.message(mode === 'json' ? '{"$id":3,"data":84}' : sequenceFrame(0x02, 3n, [84]).buffer)
      await expect(third).resolves.toEqual(mode === 'json' ? 84 : new Uint8Array([84]))
      expect(FakeWebSocket.instances).toHaveLength(1)
      expect(socket.readyState).toBe(FakeWebSocket.OPEN)
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close() }
  })
})

describe('FetchTransport refresh cancellation', () => {
  function unauthorized(mode: 'json' | 'binary'): Response {
    const encoder = new Encoder()
    encoder.writeFieldInt(1, 401n)
    encoder.writeFieldString(2, 'Unauthorized')
    encoder.writeFieldString(3, 'expired')
    encoder.writeEnd()
    return new Response(mode === 'json'
      ? '{"code":401,"error":"Unauthorized","message":"expired"}'
      : new Uint8Array(encoder.bytes()), { status: 401 })
  }

  it.each(['json', 'binary'] as const)('wraps a non-Error %s retry failure without refreshing twice', async mode => {
    vi.useFakeTimers()
    const refresh = vi.fn(async () => 'fresh')
    const transport = new FetchTransport('http://localhost/api', { mode, onTokenExpired: refresh })
    transport.setSchema({ live: { id: 7 } })
    mockFetch.mockResolvedValueOnce(unauthorized(mode)).mockRejectedValueOnce('retry unavailable')
    try {
      await expect(transport.call('live')).rejects.toMatchObject({ error: 'NetworkError', message: 'retry unavailable' })
      expect(refresh).toHaveBeenCalledTimes(1)
      expect(mockFetch).toHaveBeenCalledTimes(2)
      expect(vi.getTimerCount()).toBe(0)
    } finally { vi.clearAllTimers(); vi.useRealTimers() }
  })

  it.each(['json', 'binary'] as const)('shares one refresh and retries all three expired %s calls', async mode => {
    vi.useFakeTimers()
    try {
      const renewal = Promise.withResolvers<string | null>()
      const refresh = vi.fn(() => renewal.promise)
      const transport = new FetchTransport('http://localhost/api', { mode, token: 'expired', onTokenExpired: refresh })
      transport.setSchema({ health: { id: 1 } })
      mockFetch.mockImplementation(async (_url, init) => {
        if (new Headers(init.headers).get('authorization') === 'Bearer expired') return unauthorized(mode)
        return new Response(mode === 'json' ? '{"data":42}' : new Uint8Array([84]), { status: 200 })
      })
      const calls = Promise.allSettled([transport.call('health'), transport.call('health'), transport.call('health')])
      await vi.advanceTimersByTimeAsync(0)
      const observedRefreshes = refresh.mock.calls.length
      renewal.resolve('fresh')
      const outcomes = (await calls).map(result => result.status === 'fulfilled' ? result.value : (result.reason as LuxoError).code)
      expect({ observedRefreshes, outcomes, sends: mockFetch.mock.calls.length }).toEqual({
        observedRefreshes: 1,
        outcomes: Array.from({ length: 3 }, () => mode === 'json' ? 42 : new Uint8Array([84])),
        sends: 6,
      })
    } finally { vi.useRealTimers() }
  })

  it.each(['json', 'binary'] as const)('keeps %s refresh retry order after the first of two waiters cancels', async mode => {
    vi.useFakeTimers()
    const renewal = Promise.withResolvers<string | null>()
    try {
      const refresh = vi.fn(() => renewal.promise)
      const transport = new FetchTransport('http://localhost/api', { mode, token: 'expired', onTokenExpired: refresh })
      transport.setSchema({ first: { id: 1 }, second: { id: 2 }, third: { id: 3 } })
      const sent: Array<RequestInit['body']> = []
      const retried: Array<RequestInit['body']> = []
      mockFetch.mockImplementation(async (_url, init) => {
        if (new Headers(init?.headers).get('authorization') === 'Bearer expired') {
          sent.push(init?.body)
          return unauthorized(mode)
        }
        retried.push(init?.body)
        return new Response(mode === 'json' ? '{"data":42}' : new Uint8Array([84]))
      })
      const controller = new AbortController()
      const cancelled = expect(transport.call('first', undefined, { signal: controller.signal })).rejects
        .toMatchObject({ error: 'CancelledError' })
      await vi.advanceTimersByTimeAsync(0)
      const second = transport.call('second')
      await vi.advanceTimersByTimeAsync(0)
      expect(sent).toHaveLength(2)
      controller.abort()
      await cancelled
      const third = transport.call('third')
      await vi.advanceTimersByTimeAsync(0)
      expect(sent).toHaveLength(3)
      expect(refresh).toHaveBeenCalledOnce()
      renewal.resolve('fresh')
      const value = mode === 'json' ? 42 : new Uint8Array([84])
      expect(await Promise.all([second, third])).toEqual([value, value])
      expect(retried).toHaveLength(2)
      expect(retried[0]).toBe(sent[1])
      expect(retried[1]).toBe(sent[2])
      expect(vi.getTimerCount()).toBe(0)
    } finally {
      renewal.resolve(null)
      vi.clearAllTimers()
      vi.useRealTimers()
    }
  })

  it.each(['json', 'binary'] as const)('retries a late %s 401 with the renewed token once', async mode => {
    const late = Promise.withResolvers<Response>()
    const refresh = vi.fn(async () => 'renewed')
    const transport = new FetchTransport('http://localhost/api', { mode, token: 'expired', onTokenExpired: refresh })
    transport.setSchema({ health: { id: 1 } })
    mockFetch.mockReturnValueOnce(late.promise)
      .mockResolvedValueOnce(unauthorized(mode))
      .mockResolvedValueOnce(new Response(mode === 'json' ? '{"data":42}' : new Uint8Array([84])))
      .mockResolvedValueOnce(unauthorized(mode))
    const lateResult = expect(transport.call('health')).rejects.toMatchObject({ error: 'Unauthorized', code: 401 })
    await transport.call('health')
    late.resolve(unauthorized(mode))
    await lateResult
    expect(refresh).toHaveBeenCalledOnce()
    expect(mockFetch.mock.calls.map(([, init]) => new Headers(init.headers).get('authorization'))).toEqual([
      'Bearer expired', 'Bearer expired', 'Bearer renewed', 'Bearer renewed',
    ])
  })

  it.each(['json', 'binary'] as const)('shares a null %s refresh with old responses and allows a later attempt', async mode => {
    const late = Promise.withResolvers<Response>()
    const refresh = vi.fn<() => Promise<string | null>>().mockResolvedValueOnce(null).mockResolvedValueOnce('fresh')
    const transport = new FetchTransport('http://localhost/api', { mode, token: 'expired', onTokenExpired: refresh })
    transport.setSchema({ health: { id: 1 } })
    mockFetch.mockReturnValueOnce(late.promise)
      .mockResolvedValueOnce(unauthorized(mode))
      .mockResolvedValueOnce(unauthorized(mode))
      .mockResolvedValueOnce(new Response(mode === 'json' ? '{"data":42}' : new Uint8Array([84])))
    const lateResult = expect(transport.call('health')).rejects.toMatchObject({ error: 'Unauthorized', code: 401 })
    await expect(transport.call('health')).rejects.toMatchObject({ error: 'Unauthorized', code: 401 })
    late.resolve(unauthorized(mode))
    await lateResult
    expect(refresh).toHaveBeenCalledOnce()
    await expect(transport.call('health')).resolves.toEqual(mode === 'json' ? 42 : new Uint8Array([84]))
    expect(refresh).toHaveBeenCalledTimes(2)
  })

  it.each([
    ['json', 'null'], ['binary', 'null'], ['json', 'reject'], ['binary', 'reject'],
  ] as const)('preserves a late %s refresh %s after the next generation succeeds', async (mode, outcome) => {
    const late = Promise.withResolvers<Response>()
    const failure = new Error('refresh rejected')
    const refresh = vi.fn<() => Promise<string | null>>()
    if (outcome === 'reject') refresh.mockRejectedValueOnce(failure)
    else refresh.mockResolvedValueOnce(null)
    refresh.mockResolvedValueOnce('fresh')
    const transport = new FetchTransport('http://localhost/api', { mode, token: 'expired', onTokenExpired: refresh })
    transport.setSchema({ health: { id: 1 } })
    const success = () => new Response(mode === 'json' ? '{"data":42}' : new Uint8Array([84]))
    mockFetch.mockReturnValueOnce(late.promise)
      .mockResolvedValueOnce(unauthorized(mode))
      .mockResolvedValueOnce(unauthorized(mode))
      .mockImplementation(async () => success())

    const observed = transport.call('health').catch((error: unknown) => error)
    const first = expect(transport.call('health')).rejects
    if (outcome === 'reject') await first.toBe(failure)
    else await first.toMatchObject({ error: 'Unauthorized', code: 401 })
    await expect(transport.call('health')).resolves.toEqual(mode === 'json' ? 42 : new Uint8Array([84]))
    late.resolve(unauthorized(mode))
    if (outcome === 'reject') expect(await observed).toBe(failure)
    else expect(await observed).toMatchObject({ error: 'Unauthorized', code: 401 })
    expect(refresh).toHaveBeenCalledTimes(2)
    expect(mockFetch).toHaveBeenCalledTimes(4)
  })

  it.each(['json', 'binary'] as const)('lets a late %s 401 refresh after its sole peer cancelled', async mode => {
    const late = Promise.withResolvers<Response>()
    const entered = Promise.withResolvers<void>()
    const obsolete = Promise.withResolvers<string | null>()
    const controller = new AbortController()
    const refresh = vi.fn<() => Promise<string | null>>()
      .mockImplementationOnce(() => { entered.resolve(); return obsolete.promise })
      .mockResolvedValueOnce('renewed')
    const transport = new FetchTransport('http://localhost/api', { mode, token: 'expired', onTokenExpired: refresh })
    transport.setSchema({ health: { id: 1 } })
    mockFetch.mockReturnValueOnce(late.promise)
      .mockResolvedValueOnce(unauthorized(mode))
      .mockResolvedValueOnce(new Response(mode === 'json' ? '{"data":42}' : new Uint8Array([84])))

    const pending = transport.call('health')
    const cancelled = expect(transport.call('health', undefined, { signal: controller.signal })).rejects
      .toMatchObject({ error: 'CancelledError' })
    await entered.promise
    controller.abort()
    await cancelled
    obsolete.resolve('obsolete')
    late.resolve(unauthorized(mode))
    await expect(pending).resolves.toEqual(mode === 'json' ? 42 : new Uint8Array([84]))
    expect(refresh).toHaveBeenCalledTimes(2)
    expect(mockFetch.mock.calls.map(([, init]) => new Headers(init.headers).get('authorization'))).toEqual([
      'Bearer expired', 'Bearer expired', 'Bearer renewed',
    ])
  })

  it.each([
    ['json', null], ['binary', null], ['json', 'different-user'], ['binary', 'different-user'],
  ] as const)('invalidates all waiting %s calls when the session changes to %s', async (mode, replacement) => {
    vi.useFakeTimers()
    try {
      const renewal = Promise.withResolvers<string | null>()
      const refresh = vi.fn(() => renewal.promise)
      const transport = new FetchTransport('http://localhost/api', { mode, token: 'expired', onTokenExpired: refresh })
      transport.setSchema({ health: { id: 1 } })
      mockFetch.mockImplementation(async (_url, init) => {
        if (new Headers(init?.headers).get('authorization') === 'Bearer expired') return unauthorized(mode)
        return new Response(mode === 'json' ? '{"data":42}' : new Uint8Array([84]))
      })
      const calls = Promise.allSettled([transport.call('health'), transport.call('health'), transport.call('health')])
      await vi.advanceTimersByTimeAsync(0)
      transport.setToken(replacement)
      expect(await calls).toEqual(Array.from({ length: 3 }, () => ({
        status: 'rejected', reason: expect.objectContaining({ code: 401 }),
      })))
      renewal.resolve('stale')
      expect(refresh).toHaveBeenCalledOnce()
      expect(mockFetch).toHaveBeenCalledTimes(3)
      await transport.call('health')
      expect(new Headers(mockFetch.mock.calls[3][1]?.headers).get('authorization'))
        .toBe(replacement ? `Bearer ${replacement}` : null)
    } finally { vi.useRealTimers() }
  })

  it.each(['json', 'binary'] as const)('cancels %s when the refresh callback synchronously aborts its caller', async mode => {
    vi.useFakeTimers()
    const controller = new AbortController()
    const refresh = vi.fn(() => { controller.abort(); return Promise.resolve('stale') })
    const transport = new FetchTransport('http://localhost/api', { mode, onTokenExpired: refresh })
    transport.setSchema({ live: { id: 7 } })
    mockFetch.mockResolvedValueOnce(unauthorized(mode))
    try {
      await expect(transport.call('live', undefined, { signal: controller.signal })).rejects.toMatchObject({ error: 'CancelledError' })
      expect(refresh).toHaveBeenCalledTimes(1)
      expect(mockFetch).toHaveBeenCalledTimes(1)
      expect(vi.getTimerCount()).toBe(0)
    } finally { vi.clearAllTimers(); vi.useRealTimers() }
  })

  it.each(['json', 'binary'] as const)('checks the parent abort again after a reentrant %s token update', async mode => {
    vi.useFakeTimers()
    const controller = new AbortController()
    const transport = new class extends FetchTransport {
      override setToken(token: string | null): void { super.setToken(token); controller.abort() }
    }('http://localhost/api', { mode, onTokenExpired: async () => 'fresh' })
    transport.setSchema({ live: { id: 7 } })
    mockFetch.mockResolvedValueOnce(unauthorized(mode))
    try {
      await expect(transport.call('live', undefined, { signal: controller.signal })).rejects.toMatchObject({ error: 'CancelledError' })
      expect(mockFetch).toHaveBeenCalledTimes(1)
      expect(vi.getTimerCount()).toBe(0)
    } finally { vi.clearAllTimers(); vi.useRealTimers() }
  })

  it.each([
    ['json', null], ['binary', null], ['json', 'new-session'], ['binary', 'new-session'],
  ] as const)('does not retry after a reentrant %s setter replaces the refresh token with %s', async (mode, replacement) => {
    const transport = new class extends FetchTransport {
      override setToken(_token: string | null): void { super.setToken(replacement) }
    }('http://localhost/api', { mode, token: 'expired', onTokenExpired: async () => 'fresh' })
    transport.setSchema({ live: { id: 7 } })
    mockFetch
      .mockResolvedValueOnce(unauthorized(mode))
      .mockResolvedValueOnce(new Response(mode === 'json' ? '{"data":42}' : new Uint8Array([84])))

    await expect(transport.call('live')).rejects.toMatchObject({ code: 401 })
    expect(mockFetch).toHaveBeenCalledTimes(1)
    await transport.call('live')
    expect(new Headers(mockFetch.mock.calls[1][1]?.headers).get('authorization'))
      .toBe(replacement ? `Bearer ${replacement}` : null)
  })

  it.each(['json', 'binary'] as const)('does not retry after a reentrant %s setter advances the session twice', async mode => {
    const refresh = vi.fn(async () => 'fresh')
    const transport = new class extends FetchTransport {
      override setToken(token: string | null): void {
        super.setToken(token)
        super.setToken(token)
      }
    }('http://localhost/api', { mode, token: 'expired', onTokenExpired: refresh })
    transport.setSchema({ live: { id: 7 } })
    mockFetch
      .mockResolvedValueOnce(unauthorized(mode))
      .mockResolvedValueOnce(new Response(mode === 'json' ? '{"data":42}' : new Uint8Array([84])))

    await expect(transport.call('live')).rejects.toMatchObject({ code: 401 })
    expect(refresh).toHaveBeenCalledOnce()
    expect(mockFetch).toHaveBeenCalledTimes(1)
    await transport.call('live')
    expect(new Headers(mockFetch.mock.calls[1][1]?.headers).get('authorization')).toBe('Bearer fresh')
  })

  it.each([
    ['json', 'none'], ['binary', 'none'], ['json', 'pending'], ['binary', 'pending'], ['json', 'throws'], ['binary', 'throws'],
  ] as const)('preserves the settled %s refresh for a late 401 during %s replacement', async (mode, replacement) => {
    const delayed = Promise.withResolvers<Response>()
    const entered = Promise.withResolvers<void>()
    const next = Promise.withResolvers<string | null>()
    const token = 'fresh-'.padEnd(1990, 'a')
    const setterFailure = new Error('setter failed')
    const wantsNewer = replacement !== 'none'
    const refresh = vi.fn<() => Promise<string | null>>()
      .mockResolvedValueOnce(token)
      .mockImplementationOnce(() => { entered.resolve(); return next.promise })
    let setterCalls = 0
    const transport = new class extends FetchTransport {
      override setToken(value: string | null): void {
        setterCalls++
        if (setterCalls === 2 && replacement === 'throws') throw setterFailure
        if (setterCalls > 1) super.setToken(value)
      }
    }('http://localhost/api', { mode, token: 'expired', onTokenExpired: refresh })
    transport.setSchema({ live: { id: 7 } })
    mockFetch
      .mockReturnValueOnce(delayed.promise)
      .mockResolvedValueOnce(unauthorized(mode))
    const late = transport.call('live')
    await expect(transport.call('live')).rejects.toMatchObject({ code: 401 })

    let waiting: Promise<unknown> | undefined
    if (wantsNewer) {
      mockFetch.mockResolvedValueOnce(unauthorized(mode))
      waiting = transport.call('live').catch((error: unknown) => error)
      await entered.promise
    }
    mockFetch.mockResolvedValueOnce(new Response(mode === 'json' ? '{"data":42}' : new Uint8Array([84])))
    delayed.resolve(unauthorized(mode))
    const expectedResult = mode === 'json' ? 42 : new Uint8Array([84])
    let expectedToken = token
    try {
      if (replacement === 'throws') {
        await expect(late).rejects.toBe(setterFailure)
        expectedToken = 'newer'
        next.resolve(expectedToken)
        expect(await waiting).toEqual(expectedResult)
      } else {
        await expect(late).resolves.toEqual(expectedResult)
        if (waiting) expect(await waiting).toMatchObject({ code: 401 })
      }
      expect(setterCalls).toBe(replacement === 'throws' ? 3 : 2)
      expect(refresh).toHaveBeenCalledTimes(wantsNewer ? 2 : 1)
      expect(mockFetch).toHaveBeenCalledTimes(wantsNewer ? 4 : 3)
      expect(new Headers(mockFetch.mock.lastCall?.[1]?.headers).get('authorization')).toBe(`Bearer ${expectedToken}`)
    } finally { next.resolve('discarded') }
  })

  it.each(['json', 'binary'] as const)('does not enter refresh after its %s transport lifecycle ends', async mode => {
    vi.useFakeTimers()
    const lifecycle = new AbortController(), refresh = vi.fn(async () => 'stale')
    const transport = new class extends FetchTransport {
      constructor() {
        super('http://localhost/api', { mode, onTokenExpired: refresh })
        this.refreshAbortSignal = lifecycle.signal
      }
    }()
    transport.setSchema({ live: { id: 7 } })
    mockFetch.mockResolvedValueOnce(unauthorized(mode))
    try {
      const pending = transport.call('live')
      lifecycle.abort()
      await expect(pending).rejects.toMatchObject({ error: 'CancelledError' })
      expect(refresh).not.toHaveBeenCalled()
      expect(mockFetch).toHaveBeenCalledTimes(1)
      expect(vi.getTimerCount()).toBe(0)
    } finally { vi.clearAllTimers(); vi.useRealTimers() }
  })

  it.each(['json', 'binary'] as const)('checks %s lifecycle cancellation after refresh settlement', async mode => {
    vi.useFakeTimers()
    const entered = Promise.withResolvers<void>(), refresh = Promise.withResolvers<string | null>()
    const controller = new AbortController(), lifecycle = new AbortController()
    const transport = new class extends FetchTransport {
      constructor() {
        super('http://localhost/api', { mode, onTokenExpired: () => { entered.resolve(); return refresh.promise } })
        this.refreshAbortSignal = lifecycle.signal
      }
    }()
    transport.setSchema({ live: { id: 7 } })
    mockFetch.mockResolvedValueOnce(unauthorized(mode))
    try {
      const pending = transport.call('live', undefined, { signal: controller.signal })
      const failed = expect(pending).rejects.toMatchObject({ error: 'CancelledError' })
      await entered.promise
      refresh.resolve('stale')
      queueMicrotask(() => lifecycle.abort())
      await failed
      expect(mockFetch).toHaveBeenCalledTimes(1)
      expect(vi.getTimerCount()).toBe(0)
    } finally { vi.clearAllTimers(); vi.useRealTimers() }
  })

  it.each([
    ['json', null], ['binary', null],
    ['json', 'different-user'], ['binary', 'different-user'],
    ['json', 'original-token'], ['binary', 'original-token'],
  ] as const)('does not restore stale %s credentials after explicitly setting %s', async (mode, replacement) => {
    const entered = Promise.withResolvers<void>()
    const refresh = Promise.withResolvers<string | null>()
    const transport = new FetchTransport('http://localhost/api', {
      mode, token: 'original-token',
      onTokenExpired: () => { entered.resolve(); return refresh.promise },
    })
    transport.setSchema({ health: { id: 1 } })
    mockFetch.mockResolvedValueOnce(unauthorized(mode))
    mockFetch.mockImplementation(async () => new Response(mode === 'json' ? '{"data":42}' : new Uint8Array([84]), { status: 200 }))
    const result = transport.call('health').catch((error: unknown) => error)
    await entered.promise
    transport.setToken(replacement)
    refresh.resolve('old-user-refreshed')
    expect(await result).toMatchObject({ error: 'Unauthorized', code: 401 })
    const callsBefore = mockFetch.mock.calls.length
    await transport.call('health')
    expect(new Headers(mockFetch.mock.calls[callsBefore][1].headers).get('authorization')).toBe(replacement === null ? null : `Bearer ${replacement}`)
    expect(callsBefore).toBe(1)
  })

  it.each(['json', 'binary'] as const)('does not refresh a stale %s response after credentials change', async mode => {
    const response = Promise.withResolvers<Response>()
    const refresh = vi.fn(async () => 'old-user-refreshed')
    const transport = new FetchTransport('http://localhost/api', {
      mode, token: 'original-token', onTokenExpired: refresh,
    })
    transport.setSchema({ health: { id: 1 } })
    mockFetch.mockReturnValueOnce(response.promise)
    const failure = expect(transport.call('health')).rejects.toMatchObject({ error: 'Unauthorized', code: 401 })
    transport.setToken('different-user')
    response.resolve(unauthorized(mode))
    await failure
    expect(refresh).not.toHaveBeenCalled()
    expect(mockFetch).toHaveBeenCalledTimes(1)
  })

  for (const mode of ['json', 'binary'] as const) {
    it.each(['null', 'reject', 'throw'] as const)(`cleans up ${mode} refresh %s without changing credentials`, async completion => {
      vi.useFakeTimers()
      const controller = new AbortController()
      const added = vi.spyOn(controller.signal, 'addEventListener')
      const removed = vi.spyOn(controller.signal, 'removeEventListener')
      const callbackError = new Error('refresh unavailable')
      const refresh = vi.fn((): Promise<string | null> => {
        if (completion === 'throw') throw callbackError
        return completion === 'null' ? Promise.resolve(null) : Promise.reject(callbackError)
      })
      const transport = new FetchTransport('http://localhost/api', {
        mode, token: 'original-token', onTokenExpired: refresh,
      })
      transport.setSchema({ health: { id: 1 } })
      mockFetch.mockResolvedValueOnce(unauthorized(mode))
      try {
        const call = transport.call('health', undefined, { signal: controller.signal })
        if (completion === 'null') await expect(call).rejects.toMatchObject({ error: 'Unauthorized', code: 401 })
        else await expect(call).rejects.toBe(callbackError)
        expect(refresh).toHaveBeenCalledTimes(1)
        expect(mockFetch).toHaveBeenCalledTimes(1)
        expect(vi.getTimerCount()).toBe(0)
        for (const [type, listener] of added.mock.calls) {
          expect(removed.mock.calls.some(([removedType, removedListener]) => removedType === type && removedListener === listener)).toBe(true)
        }
        controller.abort()
        mockFetch.mockResolvedValueOnce(new Response(mode === 'json' ? '{"data":42}' : new Uint8Array([84]), { status: 200 }))
        await transport.call('health')
        expect(new Headers(mockFetch.mock.calls[1][1].headers).get('authorization')).toBe('Bearer original-token')
        expect(refresh).toHaveBeenCalledTimes(1)
        expect(vi.getTimerCount()).toBe(0)
      } finally {
        vi.useRealTimers()
      }
    })
  }

  it.each(['json', 'binary'] as const)('keeps credentials when %s is cancelled between refresh settlement and retry', async mode => {
    const entered = Promise.withResolvers<void>()
    const refresh = Promise.withResolvers<string | null>()
    const controller = new AbortController()
    const transport = new FetchTransport('http://localhost/api', {
      mode, token: 'original-token',
      onTokenExpired: () => { entered.resolve(); return refresh.promise },
    })
    transport.setSchema({ health: { id: 1 } })
    mockFetch.mockResolvedValueOnce(unauthorized(mode))
    const result = transport.call('health', undefined, { signal: controller.signal })
    const failure = expect(result).rejects.toMatchObject({ error: 'CancelledError' })
    await entered.promise
    refresh.resolve('late-token')
    queueMicrotask(() => controller.abort())
    await failure
    expect(mockFetch).toHaveBeenCalledTimes(1)
    mockFetch.mockResolvedValueOnce(new Response(mode === 'json' ? '{"data":42}' : new Uint8Array([84]), { status: 200 }))
    await transport.call('health')
    expect(new Headers(mockFetch.mock.calls[1][1].headers).get('authorization')).toBe('Bearer original-token')
  })

  it.each([['json', 'resolve'], ['json', 'reject'], ['binary', 'resolve'], ['binary', 'reject']] as const)('cancels %s with late refresh %s', async (mode, completion) => {
    vi.useFakeTimers()
    const entered = Promise.withResolvers<void>()
    const refresh = Promise.withResolvers<string | null>()
    const controller = new AbortController()
    const transport = new FetchTransport('http://localhost/api', {
      mode,
      token: 'original-token',
      onTokenExpired: () => { entered.resolve(); return refresh.promise },
    })
    transport.setSchema({ health: { id: 1 } })
    mockFetch.mockResolvedValueOnce(unauthorized(mode))
    let finished = false
    let failure: unknown
    const observed = transport.call('health', undefined, { signal: controller.signal }).then(
      () => { finished = true },
      error => { finished = true; failure = error },
    )
    try {
      await entered.promise
      controller.abort()
      await vi.advanceTimersByTimeAsync(0)
      expect(finished).toBe(true)
      expect(failure).toMatchObject({ error: 'CancelledError' })
      expect(mockFetch).toHaveBeenCalledTimes(1)
    } finally {
      if (completion === 'resolve') refresh.resolve('late-token')
      else refresh.reject(new Error('late refresh failure'))
      await observed
      vi.useRealTimers()
    }
    expect(mockFetch).toHaveBeenCalledTimes(1)
    mockFetch.mockResolvedValueOnce(new Response(mode === 'json' ? '{"data":42}' : new Uint8Array([84]), { status: 200 }))
    await transport.call('health')
    expect(new Headers(mockFetch.mock.calls[1][1].headers).get('authorization')).toBe('Bearer original-token')
  })
})


describe('WsTransport access token renewal', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket)
  })
  afterEach(() => { vi.clearAllTimers(); vi.useRealTimers() })

  function expire(socket: FakeWebSocket): void {
    socket.readyState = 3
    socket.onclose?.({ code: 4001 })
  }

  it.each(['close', 'clear-token'] as const)('honors %s before the deferred renewal callback starts', async action => {
    const onTokenExpired = vi.fn(async () => 'stale')
    const transport = new WsTransport('ws://localhost/api?existing=1', { token: 'old', onTokenExpired })
    try {
      const pending = transport.call('live').catch((error: unknown) => error)
      const socket = FakeWebSocket.instances[0]
      expect(socket.url).toBe('ws://localhost/api?existing=1&token=old')
      socket.open()
      await vi.advanceTimersByTimeAsync(0)
      expire(socket)
      if (action === 'close') transport.close()
      else transport.setToken(null)
      expect(await pending).toMatchObject({ error: 'Unauthorized' })
      await vi.advanceTimersByTimeAsync(0)
      expect(onTokenExpired).not.toHaveBeenCalled()
      if (action === 'clear-token') {
        const next = transport.call('live')
        await vi.advanceTimersByTimeAsync(0)
        const replacement = FakeWebSocket.instances[1]
        expect(replacement.url).toBe('ws://localhost/api?existing=1')
        replacement.open()
        await vi.advanceTimersByTimeAsync(0)
        replacement.message('{"$id":2,"data":42}')
        await expect(next).resolves.toBe(42)
      }
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close() }
  })

  it.each(['json', 'binary'] as const)('renews %s once without replaying expired work', async mode => {
    let renew!: (token: string | null) => void
    const onTokenExpired = vi.fn(() => new Promise<string | null>(resolve => { renew = resolve }))
    const transport = new WsTransport('ws://localhost/luvia', { mode, token: 'old', onTokenExpired })
    transport.setSchema({ live: { id: 71 } })
    try {
      const oldCall = transport.call('live').catch((error: unknown) => error)
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await vi.advanceTimersByTimeAsync(0)
      expire(socket)
      expect(await oldCall).toMatchObject({ error: 'Unauthorized', code: 401 })
      const nextCall = transport.call('live')
      await vi.advanceTimersByTimeAsync(0)
      expect(onTokenExpired).toHaveBeenCalledTimes(1)
      expect(FakeWebSocket.instances).toHaveLength(1)
      renew('new')
      await vi.advanceTimersByTimeAsync(0)
      const next = FakeWebSocket.instances[1]
      expect(next.url).toContain('token=new')
      next.open()
      await vi.advanceTimersByTimeAsync(0)
      expect(next.sent).toHaveLength(1)
      next.message(mode === 'json' ? '{"$id":2,"data":42}' : sequenceFrame(0x02, 2n, [84]).buffer)
      await nextCall
      socket.onclose?.({ code: 4001 })
      expect(onTokenExpired).toHaveBeenCalledTimes(1)
    } finally { transport.close() }
  })

  it.each(['json', 'binary'] as const)('isolates cancellation between %s renewal waiters', async mode => {
    const renewal = Promise.withResolvers<string | null>()
    const callback = vi.fn(() => renewal.promise)
    const transport = new WsTransport('ws://localhost/luvia', { mode, token: 'old', onTokenExpired: callback })
    transport.setSchema({ live: { id: 71 } })
    try {
      const old = transport.call('live').catch((error: unknown) => error)
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await vi.advanceTimersByTimeAsync(0)
      expire(socket)
      expect(await old).toMatchObject({ code: 401 })
      const controller = new AbortController()
      const cancelled = transport.call('live', undefined, { signal: controller.signal }).catch((error: unknown) => error)
      const surviving = transport.call('live')
      controller.abort()
      await vi.advanceTimersByTimeAsync(0)
      expect(await cancelled).toMatchObject({ error: 'CancelledError' })
      expect(callback).toHaveBeenCalledTimes(1)
      renewal.resolve('new')
      await vi.advanceTimersByTimeAsync(0)
      const next = FakeWebSocket.instances[1]
      next.open()
      await vi.advanceTimersByTimeAsync(0)
      expect(next.sent).toHaveLength(1)
      next.message(mode === 'json' ? '{"$id":2,"data":42}' : sequenceFrame(0x02, 2n, [84]).buffer)
      await surviving
      expect(callback).toHaveBeenCalledTimes(1)
    } finally { transport.close() }
    expect(vi.getTimerCount()).toBe(0)
  })

  it.each(['null', 'throw'] as const)('settles every concurrent renewal waiter on %s', async outcome => {
    const renewal = Promise.withResolvers<string | null>()
    const callback = vi.fn(() => renewal.promise)
    const transport = new WsTransport('ws://localhost/luvia', { token: 'old', onTokenExpired: callback })
    try {
      const old = transport.call('live').catch((error: unknown) => error)
      FakeWebSocket.instances[0].open()
      await vi.advanceTimersByTimeAsync(0)
      expire(FakeWebSocket.instances[0])
      await old
      const waiters = Array.from({ length: 8 }, () => transport.call('live').catch((error: unknown) => error))
      await vi.advanceTimersByTimeAsync(0)
      if (outcome === 'null') renewal.resolve(null)
      else renewal.reject(new Error('refresh rejected'))
      await vi.advanceTimersByTimeAsync(0)
      for (const error of await Promise.all(waiters)) expect(error).toMatchObject({ code: 401 })
      expect(callback).toHaveBeenCalledTimes(1)
      expect(FakeWebSocket.instances).toHaveLength(1)
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close() }
  })

  it.each(['null', 'throw', 'missing'] as const)('stops automatic retries when renewal is %s', async failure => {
    const onTokenExpired = failure === 'missing' ? undefined : async () => {
      if (failure === 'throw') throw new Error('refresh rejected')
      return null
    }
    const transport = new WsTransport('ws://localhost/luvia', { token: 'old', onTokenExpired })
    try {
      const pending = transport.call('live').catch((error: unknown) => error)
      const socket = FakeWebSocket.instances[0]
      socket.open()
      await vi.advanceTimersByTimeAsync(0)
      expire(socket)
      expect(await pending).toMatchObject({ code: 401 })
      await vi.advanceTimersByTimeAsync(60000)
      await expect(transport.call('live')).rejects.toMatchObject({ code: 401 })
      expect(FakeWebSocket.instances).toHaveLength(1)
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close() }
  })

  it('settles refresh waiters on close and ignores a late token', async () => {
    let renew!: (token: string | null) => void
    const transport = new WsTransport('ws://localhost/luvia', {
      token: 'old', onTokenExpired: () => new Promise(resolve => { renew = resolve }),
    })
    const oldCall = transport.call('live').catch((error: unknown) => error)
    FakeWebSocket.instances[0].open()
    await vi.advanceTimersByTimeAsync(0)
    expire(FakeWebSocket.instances[0])
    expect(await oldCall).toMatchObject({ code: 401 })
    const waiting = transport.call('live').catch((error: unknown) => error)
    await vi.advanceTimersByTimeAsync(0)
    transport.close()
    expect(await waiting).toMatchObject({ error: 'NetworkError' })
    renew('late')
    await vi.advanceTimersByTimeAsync(60000)
    expect(FakeWebSocket.instances).toHaveLength(1)
    expect(vi.getTimerCount()).toBe(0)
  })

  it.each(['close', 'replace-token'] as const)('honors %s between WS renewal settlement and credential adoption', async action => {
    const renewal = Promise.withResolvers<string | null>(), entered = Promise.withResolvers<void>()
    const onTokenExpired = vi.fn(() => { entered.resolve(); return renewal.promise })
    const transport = new WsTransport('ws://localhost/api', { token: 'old', onTokenExpired })
    try {
      const old = transport.call('live').catch((error: unknown) => error)
      FakeWebSocket.instances[0].open()
      await vi.advanceTimersByTimeAsync(0)
      expire(FakeWebSocket.instances[0])
      await entered.promise
      await vi.advanceTimersByTimeAsync(0)
      renewal.resolve('stale')
      queueMicrotask(() => { if (action === 'close') transport.close(); else transport.setToken('manual') })
      expect(await old).toMatchObject({ error: 'Unauthorized' })
      await vi.advanceTimersByTimeAsync(0)
      expect(onTokenExpired).toHaveBeenCalledTimes(1)
      expect(FakeWebSocket.instances).toHaveLength(1)
      if (action === 'replace-token') {
        const pending = transport.call('live')
        await vi.advanceTimersByTimeAsync(0)
        const socket = FakeWebSocket.instances[1]
        expect(socket.url).toContain('token=manual')
        socket.open()
        await vi.advanceTimersByTimeAsync(0)
        socket.message('{"$id":2,"data":42}')
        await expect(pending).resolves.toBe(42)
      }
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close() }
  })

  it('preserves a manually replaced token over a pending renewal', async () => {
    let renew!: (token: string | null) => void
    const transport = new WsTransport('ws://localhost/luvia', { token: 'old',
      onTokenExpired: () => new Promise(resolve => { renew = resolve }),
    })
    try {
      const old = transport.call('live').catch((error: unknown) => error)
      FakeWebSocket.instances[0].open()
      await vi.advanceTimersByTimeAsync(0)
      expire(FakeWebSocket.instances[0])
      await old
      await vi.advanceTimersByTimeAsync(0)
      transport.setToken('manual')
      const nextCall = transport.call('live')
      await vi.advanceTimersByTimeAsync(0)
      renew('stale')
      await vi.advanceTimersByTimeAsync(0)
      const next = FakeWebSocket.instances[1]
      expect(next.url).toContain('token=manual')
      next.open()
      await vi.advanceTimersByTimeAsync(0)
      next.message('{"$id":2,"data":42}')
      await nextCall
    } finally { transport.close() }
  })

  it('bounds a stalled renewal and discards its late result', async () => {
    let renew!: (token: string | null) => void
    const transport = new WsTransport('ws://localhost/luvia', { token: 'old', timeout: 100,
      onTokenExpired: () => new Promise(resolve => { renew = resolve }),
    })
    try {
      const old = transport.call('live').catch((error: unknown) => error)
      FakeWebSocket.instances[0].open()
      await vi.advanceTimersByTimeAsync(0)
      expire(FakeWebSocket.instances[0])
      await old
      const next = transport.call('live').catch((error: unknown) => error)
      await vi.advanceTimersByTimeAsync(100)
      expect(await next).toMatchObject({ error: 'Unauthorized', code: 401 })
      renew('late')
      await vi.advanceTimersByTimeAsync(60000)
      expect(FakeWebSocket.instances).toHaveLength(1)
      expect(vi.getTimerCount()).toBe(0)
    } finally { transport.close() }
  })

  it.each(['json', 'binary'] as const)('terminates %s subscriptions without resubscribing after renewal', async mode => {
    const onError = vi.fn(), onComplete = vi.fn(), onTokenExpired = vi.fn(async () => 'new')
    const transport = new WsTransport('ws://localhost/luvia', { mode, token: 'old', onTokenExpired })
    transport.setSchema({ live: { id: 71 } })
    try {
      const setup = transport.subscribe('live', {}, value => value, vi.fn(), { onError, onComplete })
      const old = FakeWebSocket.instances[0]
      old.open()
      await vi.advanceTimersByTimeAsync(0)
      old.message(mode === 'json' ? '{"$sub":1,"ok":true}' : sequenceFrame(0x13, 1n, []).buffer)
      const unsubscribe = await setup
      expire(old)
      expect(onError).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ code: 401 }))
      expect(onComplete).not.toHaveBeenCalled()
      await vi.advanceTimersByTimeAsync(1000)
      expect(FakeWebSocket.instances).toHaveLength(2)
      const next = FakeWebSocket.instances[1]
      expect(next.url).toContain('token=new')
      next.open()
      await vi.advanceTimersByTimeAsync(0)
      unsubscribe()
      expect(next.sent).toHaveLength(0)
      expect(onTokenExpired).toHaveBeenCalledTimes(1)
    } finally { transport.close() }
  })


  it('cancels one refresh waiter without cancelling shared renewal', async () => {
    let renew!: (token: string | null) => void
    const transport = new WsTransport('ws://localhost/luvia', { token: 'old',
      onTokenExpired: () => new Promise(resolve => { renew = resolve }),
    })
    try {
      const old = transport.call('live').catch((error: unknown) => error)
      FakeWebSocket.instances[0].open()
      await vi.advanceTimersByTimeAsync(0)
      expire(FakeWebSocket.instances[0])
      await old
      const controller = new AbortController()
      const cancelled = transport.call('live', undefined, { signal: controller.signal }).catch((error: unknown) => error)
      const live = transport.call('live')
      await vi.advanceTimersByTimeAsync(0)
      controller.abort()
      expect(await cancelled).toMatchObject({ error: 'CancelledError' })
      renew('new')
      await vi.advanceTimersByTimeAsync(0)
      const next = FakeWebSocket.instances[1]
      next.open()
      await vi.advanceTimersByTimeAsync(0)
      expect(next.sent).toHaveLength(1)
      next.message('{"$id":2,"data":42}')
      await live
    } finally { transport.close() }
  })

})

describe('transport deadline ownership guards', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket)
  })
  afterEach(() => { vi.clearAllTimers(); vi.restoreAllMocks(); vi.useRealTimers() })

  function reenterNextTimerCancellation(action: () => void) {
    const descriptor = Object.getOwnPropertyDescriptor(globalThis, 'clearTimeout')
    if (!descriptor) throw new Error('missing host clearTimeout')
    const clear = globalThis.clearTimeout.bind(globalThis)
    let calls = 0
    Object.defineProperty(globalThis, 'clearTimeout', { ...descriptor,
      value: (...args: Parameters<typeof globalThis.clearTimeout>) => {
        clear(...args)
        if (calls++ === 0) action()
      },
    })
    return { count: () => calls, restore: () => Object.defineProperty(globalThis, 'clearTimeout', descriptor) }
  }

  it('rejects HTTP cancellation observed after the initial public signal check', async () => {
    const controller = new AbortController()
    let reads = 0
    const options = { get signal() { if (++reads === 2) controller.abort(); return controller.signal } }
    const transport = new FetchTransport('http://localhost/api')
    await expect(transport.call('live', undefined, options)).rejects.toMatchObject({ error: 'CancelledError' })
    expect(reads).toBe(2)
    expect(mockFetch).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('maps a host HTTP AbortError without local cancellation or timeout', async () => {
    const controller = new AbortController()
    const transport = new FetchTransport('http://localhost/api', { timeout: 11 })
    mockFetch.mockRejectedValueOnce(new DOMException('host aborted the response', 'AbortError'))
    await expect(transport.call('live', undefined, { signal: controller.signal })).rejects.toMatchObject({ error: 'CancelledError' })
    expect(controller.signal.aborted).toBe(false)
    expect(mockFetch).toHaveBeenCalledTimes(1)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('keeps a WS connection opened while its expiring host timer is cleared', async () => {
    const clock = controlledDeadlineClock()
    const transport = new WsTransport('ws://localhost/api', { timeout: 11 })
    const pending = transport.call('live').catch((error: unknown) => error)
    const socket = FakeWebSocket.instances[0]
    const host = reenterNextTimerCancellation(() => socket.open())
    try {
      clock.at(11)
      clock.fire()
      await vi.advanceTimersByTimeAsync(0)
      expect(host.count()).toBe(1)
      expect(socket.readyState).toBe(FakeWebSocket.OPEN)
      socket.message('{"$id":1,"data":42}')
      await expect(pending).resolves.toBe(42)
      expect(vi.getTimerCount()).toBe(0)
    } finally { host.restore(); transport.close() }
  })

  it('keeps a WS response received while its expiring host timer is cleared', async () => {
    const clock = controlledDeadlineClock()
    const controller = new AbortController()
    const remove = vi.spyOn(controller.signal, 'removeEventListener')
    const transport = new WsTransport('ws://localhost/api', { timeout: 11 })
    const pending = transport.call('live', undefined, { signal: controller.signal }).catch((error: unknown) => error)
    const socket = FakeWebSocket.instances[0]
    socket.open()
    await vi.advanceTimersByTimeAsync(0)
    const removedBefore = remove.mock.calls.length
    const host = reenterNextTimerCancellation(() => socket.message('{"$id":1,"data":42}'))
    try {
      clock.at(11)
      clock.fire()
      await expect(pending).resolves.toBe(42)
      expect(host.count()).toBe(1)
      expect(remove).toHaveBeenCalledTimes(removedBefore + 1)
      expect(vi.getTimerCount()).toBe(0)
    } finally { host.restore(); transport.close() }
  })

  it('keeps a WS subscription acknowledged while its expiring host timer is cleared', async () => {
    const clock = controlledDeadlineClock()
    const onData = vi.fn(), onError = vi.fn()
    const transport = new WsTransport('ws://localhost/api', { timeout: 11 })
    const pending = transport.subscribe('live', {}, value => value, onData, { onError }).catch((error: unknown) => error)
    const socket = FakeWebSocket.instances[0]
    socket.open()
    await vi.advanceTimersByTimeAsync(0)
    const host = reenterNextTimerCancellation(() => socket.message('{"$sub":1,"ok":true}'))
    try {
      clock.at(11)
      clock.fire()
      const stop = await pending
      expect(host.count()).toBe(1)
      expect(typeof stop).toBe('function')
      if (typeof stop !== 'function') throw new Error('subscription did not acknowledge')
      socket.message('{"$stream":1,"data":42}')
      expect(onData).toHaveBeenCalledExactlyOnceWith(42)
      expect(onError).not.toHaveBeenCalled()
      stop()
      expect(vi.getTimerCount()).toBe(0)
    } finally { host.restore(); transport.close() }
  })
})
