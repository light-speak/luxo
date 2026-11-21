import { afterEach, describe, expect, it, vi } from 'vitest'
import { Encoder, decodeJSONValue } from './codec'
import { LuxoError } from './error'
import { decodeJSONEnvelope, encodeLuxoJSON, jsonNumber } from './json'
import { decodeBinaryError, FetchTransport, jsonResponseError } from './transport'
import type { TransportMode } from './transport'

afterEach(() => vi.unstubAllGlobals())

const jsonError = { error: 'Unauthorized', code: 401, message: 'expired' }

describe('strict error envelopes', () => {
  it.each(['error', 'code', 'message'] as const)('rejects inherited required JSON error metadata (%s)', field => {
    const json: Record<string, unknown> = Object.create({ [field]: jsonError[field] })
    Object.assign(json, jsonError)
    delete json[field]
    expect(jsonResponseError(json, 502)).toMatchObject({ error: 'ParseError', code: 502 })
  })

  it.each(['traceId', 'cause', 'data'] as const)('ignores inherited optional JSON error metadata without reading getters (%s)', field => {
    let reads = 0
    const prototype = Object.defineProperty({}, field, { get() { reads++; return 'inherited secret' } })
    const json: Record<string, unknown> = Object.assign(Object.create(prototype), jsonError)
    const error = jsonResponseError(json, 502)
    expect(error).toMatchObject(jsonError)
    expect(error[field]).toBeUndefined()
    expect(reads).toBe(0)
  })

  it('normalizes non-finite direct error data to ParseError', () => {
    for (const number of [NaN, Infinity, -Infinity]) {
      for (const data of [number, [number], { value: number }]) {
        expect(jsonResponseError({ ...jsonError, data }, 401).error).toBe('ParseError')
      }
    }
    const data = jsonNumber('1e400')
    expect(jsonResponseError({ ...jsonError, data }, 401).data).toBe(data)
  })

  it('rejects document BOM prefixes in Binary JSON values and error data', () => {
    for (const text of ['null', '42', '{}', '[]', '"value"']) {
      const raw = '\ufeff' + text
      expect(() => decodeJSONValue(new TextEncoder().encode(raw))).toThrow()
      const encoder = new Encoder()
      for (const field of [1, 2, 3]) writeErrorField(encoder, field)
      encoder.writeFieldString(5, raw)
      encoder.writeEnd()
      expect(decodeBinaryError(encoder.bytes(), 502).error).toBe('ParseError')
    }
    expect(decodeJSONValue(new TextEncoder().encode('"\ufeffvalue"'))).toBe('\ufeffvalue')
  })

  it('applies the existing JSON error data depth guard to Binary errors', () => {
    for (const depth of [1, 127, 128, 129, 130]) {
      for (const [open, close] of [['[', ']'], ['{"child":', '}']]) {
        const data = open.repeat(depth) + '0' + close.repeat(depth)
        const encoder = new Encoder()
        for (const field of [1, 2, 3]) writeErrorField(encoder, field)
        encoder.writeFieldString(5, data)
        encoder.writeEnd()
        const json = jsonResponseError(decodeJSONEnvelope(
          `{"error":"Unauthorized","code":401,"message":"expired","data":${data}}`,
        ), 502)
        const binary = decodeBinaryError(encoder.bytes(), 502)
        const expected = depth > 128 ? 'ParseError' : 'Unauthorized'
        expect(json.error).toBe(expected)
        expect(binary.error).toBe(expected)
        if (depth <= 128) expect(binary.data).toEqual(json.data)
      }
    }
  })

  it('preserves prototype-named opaque data keys in both encodings', () => {
    const data = '{"__proto__":{"luxoAuditMarker":"value"},"constructor":{"prototype":{"luxoAuditMarker":"value"}},"hasOwnProperty":false,"toJSON":"literal","n":9007199254740993,"precise":1.2300}'
    const encoder = new Encoder()
    for (const field of [1, 2, 3]) writeErrorField(encoder, field)
    encoder.writeFieldString(5, data)
    encoder.writeEnd()
    const errors = [
      jsonResponseError(decodeJSONEnvelope(`{"error":"Unauthorized","code":401,"message":"expired","data":${data}}`), 502),
      decodeBinaryError(encoder.bytes(), 502),
    ]
    for (const error of errors) {
      expect(error.error).not.toBe('ParseError')
      const value = error.data as Record<string, unknown>
      expect(Object.hasOwn(value, '__proto__')).toBe(true)
      expect(Object.hasOwn(value, 'constructor')).toBe(true)
      expect(Object.getPrototypeOf(value)).toBe(Object.prototype)
      expect(encodeLuxoJSON(value)).toBe(data)
    }
    expect(Object.hasOwn(Object.prototype, 'luxoAuditMarker')).toBe(false)
  })

  it('rejects non-error status codes in both encodings', () => {
    for (const code of [Number.MIN_SAFE_INTEGER, -1, 0, 200, 399, 600, 1000, Number.MAX_SAFE_INTEGER]) {
      expect(jsonResponseError({ ...jsonError, code }, 502).error).toBe('ParseError')
      expect(decodeBinaryError(binaryError(BigInt(code)), 502).error).toBe('ParseError')
    }
  })

  it('rejects invalid Unicode supplied directly in error fields or opaque data', () => {
    for (const field of ['error', 'message', 'traceId', 'cause']) {
      expect(jsonResponseError({ ...jsonError, [field]: '\ud800' }, 502))
        .toMatchObject({ error: 'ParseError', code: 502 })
    }
    for (const data of ['\udc00', { ['\ud800']: 1 }]) {
      expect(jsonResponseError({ ...jsonError, data }, 502)).toMatchObject({ error: 'ParseError', code: 502 })
    }
    for (const data of ['"\\uD800"', '{"x":"\\uDC00","x":"valid"}']) {
      const encoder = new Encoder()
      for (const field of [1, 2, 3]) writeErrorField(encoder, field)
      encoder.writeFieldString(5, data)
      encoder.writeEnd()
      expect(decodeBinaryError(encoder.bytes(), 502)).toMatchObject({ error: 'ParseError', code: 502 })
    }
  })

  it('does not replace malformed JSON fields with defaults', () => {
    for (const field of ['error', 'code', 'message']) {
      const missing: Record<string, unknown> = { ...jsonError }
      delete missing[field]
      expect(jsonResponseError(missing, 502)).toMatchObject({ error: 'ParseError', code: 502 })
    }
    for (const patch of [
      { error: null }, { error: false }, { error: 0 }, { error: {} },
      { code: null }, { code: true }, { code: '401' }, { code: 1.5 },
      { code: NaN }, { code: Infinity }, { code: -Infinity },
      { code: Number.MAX_SAFE_INTEGER + 1 }, { code: -(Number.MAX_SAFE_INTEGER + 1) },
      { code: 401n }, { message: false }, { traceId: [] }, { cause: 0 },
    ]) {
      expect(jsonResponseError({ ...jsonError, ...patch }, 502))
        .toMatchObject({ error: 'ParseError', code: 502 })
    }
    for (const code of ['401.0', '401e0', '9223372036854775807']) {
      const json = decodeJSONEnvelope(`{"error":"Unauthorized","code":${code},"message":"expired"}`)
      expect(jsonResponseError(json, 401)).toMatchObject({ error: 'ParseError', code: 401 })
    }
  })

  it('preserves valid error fields and the HTTP error status range', () => {
    for (const code of [400, 401, 500, 599]) {
      const error = jsonResponseError({ ...jsonError, code, traceId: null, cause: null }, 502)
      expect(error).toMatchObject({ ...jsonError, code })
      expect(error.traceId).toBeUndefined()
      expect(error.cause).toBeUndefined()
    }
    const error = jsonResponseError(decodeJSONEnvelope(
      '{"error":"","code":400,"message":"","traceId":"trace","cause":"原因","data":{"id":9223372036854775807,"ratio":1.5}}',
    ), 502)
    expect(error).toMatchObject({ error: '', code: 400, message: '', traceId: 'trace', cause: '原因',
      data: { id: 9223372036854775807n, ratio: jsonNumber('1.5') } })
    expect(jsonResponseError(decodeJSONEnvelope('{"error":"Custom","code":-0,"message":"failure"}')).error).toBe('ParseError')
  })

  it('turns malformed nested error data into a parse error without throwing', () => {
    let value: unknown = null
    for (let depth = 0; depth < 130; depth++) value = { child: value }
    expect(jsonResponseError({ ...jsonError, data: value }, 502))
      .toMatchObject({ error: 'ParseError', code: 502 })
  })

  it('rejects every duplicate binary field, including optional fields', () => {
    for (let field = 1; field <= 6; field++) {
      const encoder = binaryFields()
      writeErrorField(encoder, field)
      encoder.writeEnd()
      expect(decodeBinaryError(encoder.bytes(), 502)).toMatchObject({ error: 'ParseError', code: 502 })
    }
  })

  it('rejects malformed UTF-8 in every binary text or JSON field', () => {
    for (const field of [2, 3, 4, 5, 6]) {
      const encoder = new Encoder()
      for (let current = 1; current <= 6; current++) {
        if (current === field) {
          encoder.writeFieldBytes(field, field === 5 ? new Uint8Array([34, 255, 34]) : new Uint8Array([255]))
        } else writeErrorField(encoder, current)
      }
      encoder.writeEnd()
      expect(decodeBinaryError(encoder.bytes(), 502)).toMatchObject({ error: 'ParseError', code: 502 })
    }
  })

  it('preserves a leading U+FEFF in binary error strings as data, not an encoding marker', () => {
    const encoder = new Encoder()
    encoder.writeFieldInt(1, 409n)
    for (const field of [2, 3, 4, 6]) encoder.writeFieldString(field, '\ufeffvalue')
    encoder.writeEnd()
    expect(decodeBinaryError(encoder.bytes(), 502)).toMatchObject({
      error: '\ufeffvalue', message: '\ufeffvalue', traceId: '\ufeffvalue', cause: '\ufeffvalue', code: 409,
    })
  })

  it('rejects truncation, unknown fields, trailing bytes and overflowing codes', () => {
    const encoder = binaryFields()
    encoder.writeEnd()
    const bytes = encoder.bytes()
    for (let length = 0; length < bytes.length; length++) {
      expect(decodeBinaryError(bytes.subarray(0, length), 502).error).toBe('ParseError')
    }
    expect(decodeBinaryError(new Uint8Array([...bytes, 0]), 502).error).toBe('ParseError')
    for (const field of [7, 32, 2147483648]) {
      const invalid = binaryFields()
      invalid.writeFieldInt(field, 0n)
      invalid.writeEnd()
      expect(decodeBinaryError(invalid.bytes(), 502).error).toBe('ParseError')
    }
    for (const code of [-9007199254740992n, 9007199254740992n]) {
      expect(decodeBinaryError(binaryError(code), 502).error).toBe('ParseError')
    }
  })

  it('preserves valid binary fields in arbitrary order and full-width data', () => {
    const encoder = new Encoder()
    for (const field of [6, 3, 5, 1, 4, 2]) writeErrorField(encoder, field)
    encoder.writeEnd()
    expect(decodeBinaryError(encoder.bytes(), 502)).toMatchObject({ ...jsonError,
      traceId: 'trace', cause: '原因', data: { id: 9223372036854775807n, ratio: jsonNumber('1.5') } })
    expect(decodeBinaryError(binaryError(400n), 502)).toMatchObject({ ...jsonError, code: 400 })
  })

  it('rejects invalid binary JSON syntax but preserves empty optional strings', () => {
    for (const data of ['{', '', 'undefined', '{"id":1} trailing']) {
      const encoder = new Encoder()
      for (const field of [1, 2, 3]) writeErrorField(encoder, field)
      encoder.writeFieldString(5, data)
      encoder.writeEnd()
      expect(decodeBinaryError(encoder.bytes(), 502).error).toBe('ParseError')
    }
    const encoder = new Encoder()
    encoder.writeFieldInt(1, 400n)
    for (const field of [2, 3, 4, 6]) encoder.writeFieldString(field, '')
    encoder.writeFieldString(5, 'null')
    encoder.writeEnd()
    expect(decodeBinaryError(encoder.bytes(), 502)).toMatchObject({
      error: '', code: 400, message: '', traceId: '', cause: '', data: null,
    })
    expect(jsonResponseError({ ...jsonError, code: false }).code).toBe(0)
  })
})

describe.each(['json', 'binary'] as const)('%s HTTP error boundaries', mode => {
  it('never treats body-read failures as an authenticated error envelope', async () => {
    const refresh = vi.fn(async () => 'must-not-be-used')
    const fail = async () => { throw new LuxoError('Unauthorized', 401, 'body failed') }
    const fetch = vi.fn(async () => ({ status: 401, ok: false, text: fail, arrayBuffer: fail }))
    vi.stubGlobal('fetch', fetch)
    const transport = new FetchTransport('http://localhost/luvia', { mode, onTokenExpired: refresh })
    transport.setSchema({ echo: { id: 1 } })
    await expect(transport.call('echo')).rejects.toMatchObject({ error: 'ParseError', code: 401 })
    expect(refresh).not.toHaveBeenCalled()
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('does not refresh or resend malformed unauthorized responses', async () => {
    for (const body of mode === 'json' ? ['{}', '{"error":false,"code":401,"message":"bad"}', '{']
      : [new Uint8Array([0]), new Uint8Array([1, 128]), new TextEncoder().encode(encodeLuxoJSON(jsonError))]) {
      const refresh = vi.fn(async () => 'must-not-be-used')
      const fetch = vi.fn(async () => new Response(body, { status: 401 }))
      vi.stubGlobal('fetch', fetch)
      const transport = new FetchTransport('http://localhost/luvia', { mode, onTokenExpired: refresh })
      transport.setSchema({ echo: { id: 1 } })
      await expect(transport.call('echo')).rejects.toMatchObject({ error: 'ParseError', code: 401 })
      expect(refresh).not.toHaveBeenCalled()
      expect(fetch).toHaveBeenCalledTimes(1)
    }
  })

  it('only refreshes valid matching status/code 401, at most once', async () => {
    for (const [status, code] of [[401, 401], [403, 401], [401, 403], [401, 0]]) {
      const refresh = vi.fn(async () => 'renewed')
      const fetch = vi.fn(async () => errorResponse(mode, status, code))
      vi.stubGlobal('fetch', fetch)
      const transport = new FetchTransport('http://localhost/luvia', { mode, onTokenExpired: refresh })
      transport.setSchema({ echo: { id: 1 } })
      await expect(transport.call('echo')).rejects.toMatchObject(code === 0
        ? { error: 'ParseError', code: status } : { error: 'Unauthorized', code })
      expect(refresh).toHaveBeenCalledTimes(status === 401 && code === 401 ? 1 : 0)
      expect(fetch).toHaveBeenCalledTimes(status === 401 && code === 401 ? 2 : 1)
    }
  })

  it('preserves refresh failure and decline without retrying', async () => {
    for (const shouldThrow of [false, true]) {
      const failure = new Error('refresh failed')
      const refresh = vi.fn(async () => { if (shouldThrow) throw failure; return null })
      const fetch = vi.fn(async () => errorResponse(mode, 401, 401))
      vi.stubGlobal('fetch', fetch)
      const transport = new FetchTransport('http://localhost/luvia', { mode, onTokenExpired: refresh })
      transport.setSchema({ echo: { id: 1 } })
      if (shouldThrow) await expect(transport.call('echo')).rejects.toBe(failure)
      else await expect(transport.call('echo')).rejects.toMatchObject(jsonError)
      expect(refresh).toHaveBeenCalledTimes(1)
      expect(fetch).toHaveBeenCalledTimes(1)
    }
  })

  it('keeps the original mode when the refresh callback changes future calls', async () => {
    const requests: RequestInit[] = []
    vi.stubGlobal('fetch', vi.fn(async (_url: string, init: RequestInit) => {
      requests.push(init)
      if (requests.length === 1) return errorResponse(mode, 401, 401)
      return new Response(mode === 'json' ? '{"data":true}' : new Uint8Array([1]), { status: 200 })
    }))
    const transport = new FetchTransport('http://localhost/luvia', {
      mode, token: 'expired', onTokenExpired: async () => {
        transport.setMode(mode === 'json' ? 'binary' : 'json')
        return 'renewed'
      },
    })
    transport.setSchema({ echo: { id: 1 } })
    await expect(transport.call('echo')).resolves.toEqual(mode === 'json' ? true : new Uint8Array([1]))
    expect(requests).toHaveLength(2)
    expect(requests[1].body).toEqual(requests[0].body)
    expect(new Headers(requests[1].headers).get('content-type'))
      .toBe(mode === 'json' ? 'application/json' : 'application/x-luxo')
    expect(new Headers(requests[1].headers).get('authorization')).toBe('Bearer renewed')
  })

  it('reports a malformed retry without attempting another refresh', async () => {
    const refresh = vi.fn(async () => 'renewed')
    const fetch = vi.fn().mockResolvedValueOnce(errorResponse(mode, 401, 401))
      .mockResolvedValueOnce(new Response(mode === 'json' ? '{}' : new Uint8Array([0]), { status: 401 }))
    vi.stubGlobal('fetch', fetch)
    const transport = new FetchTransport('http://localhost/luvia', { mode, onTokenExpired: refresh })
    transport.setSchema({ echo: { id: 1 } })
    await expect(transport.call('echo')).rejects.toMatchObject({ error: 'ParseError', code: 401 })
    expect(refresh).toHaveBeenCalledTimes(1)
    expect(fetch).toHaveBeenCalledTimes(2)
  })
})

function writeErrorField(encoder: Encoder, field: number): void {
  if (field === 1) encoder.writeFieldInt(1, 401n)
  else if (field === 5) encoder.writeFieldString(5, '{"id":9223372036854775807,"ratio":1.5}')
  else encoder.writeFieldString(field, ({ 2: 'Unauthorized', 3: 'expired', 4: 'trace', 6: '原因' } as Record<number, string>)[field])
}

function binaryFields(): Encoder {
  const encoder = new Encoder()
  for (let field = 1; field <= 6; field++) writeErrorField(encoder, field)
  return encoder
}

function binaryError(code: bigint): Uint8Array<ArrayBuffer> {
  const encoder = new Encoder()
  encoder.writeFieldInt(1, code)
  encoder.writeFieldString(2, 'Unauthorized')
  encoder.writeFieldString(3, 'expired')
  encoder.writeEnd()
  return new Uint8Array(encoder.bytes())
}

function errorResponse(mode: TransportMode, status: number, code: number): Response {
  return new Response(mode === 'json' ? encodeLuxoJSON({ ...jsonError, code }) : binaryError(BigInt(code)), { status })
}
