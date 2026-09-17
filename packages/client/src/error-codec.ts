import { Decoder } from './codec'
import { LuxoError } from './error'
import { int64ToSafeNumber } from './int64'
import { decodeLuxoJSON, isJSONNumber, normalizeLuxoJSON } from './json'
import { decodeUTF8 } from './wire-reader'
import { assertUnicode } from './unicode'

/** Strict wire errors; invalid fields must not become defaults or trigger refresh. */
export function jsonResponseError(json: Record<string, unknown>, status = 0): LuxoError {
  const code = typeof json.code === 'number' ? json.code
    : isJSONNumber(json.code) && json.code.rawJSON === '-0' ? -0 : undefined
  if (typeof json.error !== 'string' || typeof json.message !== 'string' ||
    code === undefined || !Number.isSafeInteger(code) ||
    (json.traceId != null && typeof json.traceId !== 'string') ||
    (json.cause != null && typeof json.cause !== 'string')) {
    return parseError(status, 'JSON', 'missing or invalid fields')
  }
  try {
    assertUnicode(json.error)
    assertUnicode(json.message)
    if (json.traceId != null) assertUnicode(json.traceId)
    if (json.cause != null) assertUnicode(json.cause)
    return new LuxoError(json.error, code, json.message, json.traceId ?? undefined,
      normalizeLuxoJSON(json.data), json.cause ?? undefined)
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
        case 5: errorData = decodeLuxoJSON(decodeUTF8(dec.readBytes())); break
        case 6: cause = decodeUTF8(dec.readBytes()); break
      }
    }
    if (dec.error !== null) return parseError(status, 'binary', dec.error)
    if (!ended) return parseError(status, 'binary', 'missing end marker')
    if (dec.remaining !== 0) return parseError(status, 'binary', 'trailing bytes')
    if ((seen & 7) !== 7) return parseError(status, 'binary', 'missing required fields')
    return new LuxoError(name, code, message, traceId, errorData, cause)
  } catch {
    return parseError(status, 'binary', 'invalid integer, UTF-8 or JSON value')
  }
}

function parseError(status: number, format: string, detail: string): LuxoError {
  return new LuxoError('ParseError', status, `invalid ${format} error response: ${detail}`)
}
