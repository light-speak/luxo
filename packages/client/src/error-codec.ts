import { Decoder } from './codec'
import { LuxoError } from './error'
import { int64ToSafeNumber } from './int64'
import { decodeBoundedLuxoJSON, normalizeLuxoJSON } from './json'
import { decodeUTF8 } from './wire-reader'
import { assertUnicode } from './unicode'

export function missingBinaryResponse(code = 0): LuxoError {
  return new LuxoError('ParseError', code, 'missing binary response data')
}

function ownJSONField(json: Record<string, unknown>, key: string): unknown {
  return Object.hasOwn(json, key) ? json[key] : undefined
}

/** Strict wire errors; invalid fields must not become defaults or trigger refresh. */
export function jsonResponseError(json: Record<string, unknown>, status = 0): LuxoError {
  try {
    const name = ownJSONField(json, 'error'), code = ownJSONField(json, 'code'), message = ownJSONField(json, 'message')
    if (typeof name !== 'string' || typeof message !== 'string' || typeof code !== 'number' ||
      !Number.isSafeInteger(code) || code < 400 || code > 599) {
      return parseError(status, 'JSON', 'missing or invalid fields')
    }
    const traceId = ownJSONField(json, 'traceId'), cause = ownJSONField(json, 'cause')
    if ((traceId != null && typeof traceId !== 'string') || (cause != null && typeof cause !== 'string')) {
      return parseError(status, 'JSON', 'missing or invalid fields')
    }
    assertUnicode(name)
    assertUnicode(message)
    if (traceId != null) assertUnicode(traceId)
    if (cause != null) assertUnicode(cause)
    return new LuxoError(name, code, message, traceId ?? undefined,
      normalizeLuxoJSON(ownJSONField(json, 'data')), cause ?? undefined)
  } catch {
    return parseError(status, 'JSON', 'invalid error text or data')
  }
}

export function decodeBinaryError(data: Uint8Array, status: number): LuxoError {
  try {
    const dec = new Decoder(data)
    let code = status
    let name = 'Error', message = '', traceId: string | undefined, cause: string | undefined
    let errorData: unknown
    let seen = 0, ended = false
    while (dec.remaining > 0) {
      if (!dec.nextField()) {
        ended = dec.error === null
        break
      }
      const field = dec.fieldID
      if (field < 1 || field > 6) return parseError(status, 'binary', `unknown field ${field}`)
      const bit = 1 << (field - 1)
      if (seen & bit) return parseError(status, 'binary', `duplicate field ${field}`)
      seen |= bit
      switch (field) {
        case 1: code = int64ToSafeNumber(dec.readInt()); break
        case 2: name = decodeUTF8(dec.readBytes()); break
        case 3: message = decodeUTF8(dec.readBytes()); break
        case 4: traceId = decodeUTF8(dec.readBytes()); break
        case 5: errorData = decodeBoundedLuxoJSON(decodeUTF8(dec.readBytes())); break
        case 6: cause = decodeUTF8(dec.readBytes()); break
      }
    }
    if (dec.error !== null) return parseError(status, 'binary', dec.error)
    if (!ended) return parseError(status, 'binary', 'missing end marker')
    if (dec.remaining !== 0) return parseError(status, 'binary', 'trailing bytes')
    if ((seen & 7) !== 7) return parseError(status, 'binary', 'missing required fields')
    if (code < 400 || code > 599) return parseError(status, 'binary', 'invalid error status')
    return new LuxoError(name, code, message, traceId, errorData, cause)
  } catch {
    return parseError(status, 'binary', 'invalid integer, UTF-8 or JSON value')
  }
}

function parseError(status: number, format: string, detail: string): LuxoError {
  return new LuxoError('ParseError', status, `invalid ${format} error response: ${detail}`)
}
