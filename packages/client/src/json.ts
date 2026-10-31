import { assertInt64 } from './int64'
import { assertJSONUnicode, assertUnicode } from './unicode'

// Stage-4 JSON source access is runtime-checked; TS standard-library declarations
// do not yet expose these APIs in every supported compiler library target.
export interface LuxoJSONNumber { readonly rawJSON: string }
const nativeJSON = JSON as JSON & {
  rawJSON(text: string): LuxoJSONNumber
  isRawJSON(value: unknown): value is LuxoJSONNumber
}
let negativeZero: LuxoJSONNumber | undefined
const fractionalNumberSyntax = /[.eE]/

/** JSON mode requires native source access; never fall back to rounded numbers. */
function requireLosslessJSON(): void {
  if (typeof nativeJSON.rawJSON !== 'function' || typeof nativeJSON.isRawJSON !== 'function') {
    throw new Error('Luxo JSON requires JSON.rawJSON and JSON.parse source access; upgrade the JavaScript runtime')
  }
}

/** Construct a validated raw number, never a quoted string or unchecked JSON fragment. */
export function jsonNumber(literal: string): LuxoJSONNumber {
  requireLosslessJSON()
  const value = nativeJSON.rawJSON(literal)
  if (!isJSONNumber(value)) throw new TypeError('expected a JSON number token')
  return value
}

export function isJSONNumber(value: unknown): value is LuxoJSONNumber {
  if (!nativeJSON.isRawJSON(value)) return false
  const first = value.rawJSON.charCodeAt(0)
  return first === 45 || (first >= 48 && first <= 57)
}

/** Both legacy modes now preserve fractional/exponent syntax and negative zero. */
export function decodeLuxoJSON(text: string, _preserveNumberSyntax = false): unknown {
  requireLosslessJSON()
  // Validate source text, not the reviver's final object: duplicate keys can
  // otherwise hide an earlier malformed string or key.
  assertJSONUnicode(text)
  return JSON.parse(text, reviveJSONNumber) as unknown
}

function reviveJSONNumber(_key: string, value: unknown, context?: { source?: string }): unknown {
  if (typeof value !== 'number') return value
  const source = context?.source
  if (source === undefined) throw new Error('Luxo JSON requires JSON.parse source access')
  if (source !== '-0' && !fractionalNumberSyntax.test(source)) {
    return Number.isSafeInteger(value) ? value : BigInt(source)
  }
  return nativeJSON.rawJSON(source)
}

/** Envelopes must be objects; preserve number syntax for generated typed decoders. */
export function decodeJSONEnvelope(text: string): Record<string, unknown> {
  const value = decodeLuxoJSON(text, true)
  if (value === null || typeof value !== 'object' || Array.isArray(value) || nativeJSON.isRawJSON(value)) {
    throw new TypeError('expected a JSON response object')
  }
  return value as Record<string, unknown>
}

export function encodeLuxoJSON(value: unknown, space?: number): string {
  requireLosslessJSON()
  const encoded = JSON.stringify(value, (_key: string, item: unknown): unknown => {
    if (typeof item === 'bigint') return nativeJSON.rawJSON(item.toString())
    if (typeof item === 'number') {
      if (!Number.isFinite(item)) throw new RangeError('non-finite JSON number')
      // JSON.stringify erases the sign; share one immutable native token lazily.
      if (Object.is(item, -0)) return negativeZero ??= nativeJSON.rawJSON('-0')
    }
    return item
  }, space)
  if (encoded === undefined) throw new TypeError('value is not JSON encodable')
  // Also covers boxed strings, toJSON results and native raw JSON fragments.
  assertJSONUnicode(encoded)
  return encoded
}

export function int64FromJSON(value: unknown): bigint {
  if (nativeJSON.isRawJSON(value)) {
    if (!/^-?(0|[1-9][0-9]*)$/.test(value.rawJSON)) throw new TypeError('expected an int64 JSON integer token')
    value = BigInt(value.rawJSON)
  }
  if (typeof value === 'number' && Number.isSafeInteger(value)) return BigInt(value)
  if (typeof value !== 'bigint') throw new TypeError('expected an int64 JSON integer token')
  assertInt64(value)
  return value
}

export function floatFromJSON(value: unknown): number {
  const literal = nativeJSON.isRawJSON(value) ? value.rawJSON : undefined
  const numeric = literal !== undefined ? Number(literal)
    : typeof value === 'bigint' ? Number(value) : value
  if (typeof numeric !== 'number' || !Number.isFinite(numeric)) throw new TypeError('expected a finite JSON Float')
  if (numeric === 0 && literal !== undefined && nonzeroSignificand(literal)) throw new RangeError('JSON Float underflow')
  return numeric
}

export function stringFromJSON(value: unknown): string {
  if (typeof value !== 'string') throw new TypeError('expected a JSON string')
  assertUnicode(value)
  return value
}

export function booleanFromJSON(value: unknown): boolean {
  if (typeof value !== 'boolean') throw new TypeError('expected a JSON boolean')
  return value
}

function nonzeroSignificand(literal: string): boolean {
  for (const char of literal) {
    if (char === 'e' || char === 'E') return false
    if (char >= '1' && char <= '9') return true
  }
  return false
}

/** Validate generic JSON depth and text without converting or copying its numbers. */
export function normalizeLuxoJSON(value: unknown, depth = 0): unknown {
  if (depth > 128) throw new RangeError('JSON nesting exceeds 128 levels')
  if (nativeJSON.isRawJSON(value)) {
    if (value.rawJSON.charCodeAt(0) === 34) assertJSONUnicode(value.rawJSON)
    return value
  }
  if (typeof value === 'string') assertUnicode(value)
  if (Array.isArray(value)) {
    for (let index = 0; index < value.length; index++) normalizeLuxoJSON(value[index], depth + 1)
  } else if (value !== null && typeof value === 'object') {
    const record = value as Record<string, unknown>
    for (const key of Object.keys(record)) {
      assertUnicode(key)
      normalizeLuxoJSON(record[key], depth + 1)
    }
  }
  return value
}
