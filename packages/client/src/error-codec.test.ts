import { afterEach, describe, expect, it, vi } from 'vitest'
import { Encoder } from './codec'
import { LuxoError } from './error'
import { decodeJSONEnvelope, encodeLuxoJSON, jsonNumber } from './json'
import { decodeBinaryError, FetchTransport, jsonResponseError } from './transport'
import type { TransportMode } from './transport'

afterEach(() => vi.unstubAllGlobals())

const jsonError = { error: 'Unauthorized', code: 401, message: 'expired' }

describe('strict error envelopes', () => {
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

  it('preserves valid error fields and the existing safe-int code range', () => {
    for (const code of [0, -1, 401, Number.MIN_SAFE_INTEGER, Number.MAX_SAFE_INTEGER]) {
      const error = jsonResponseError({ ...jsonError, code, traceId: null, cause: null }, 502)
      expect(error).toMatchObject({ ...jsonError, code })
      expect(error.traceId).toBeUndefined()
      expect(error.cause).toBeUndefined()
    }
    const error = jsonResponseError(decodeJSONEnvelope(
      '{"error":"","code":0,"message":"","traceId":"trace","cause":"原因","data":{"id":9223372036854775807,"ratio":1.5}}',
    ), 502)
    expect(error).toMatchObject({ error: '', code: 0, message: '', traceId: 'trace', cause: '原因',
      data: { id: 9223372036854775807n, ratio: jsonNumber('1.5') } })
    expect(jsonResponseError(decodeJSONEnvelope('{"error":"Custom","code":-0,"message":"failure"}')).code).toBe(-0)
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
    expect(decodeBinaryError(binaryError(0n), 502)).toMatchObject({ ...jsonError, code: 0 })
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
    encoder.writeFieldInt(1, 0n)
    for (const field of [2, 3, 4, 6]) encoder.writeFieldString(field, '')
    encoder.writeFieldString(5, 'null')
    encoder.writeEnd()
    expect(decodeBinaryError(encoder.bytes(), 502)).toMatchObject({
      error: '', code: 0, message: '', traceId: '', cause: '', data: null,
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
      await expect(transport.call('echo')).rejects.toMatchObject({ error: 'Unauthorized', code })
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
