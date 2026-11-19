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
  // Validate source text, not the reviver's final object: duplicate keys can
  // otherwise hide an earlier malformed string or key.
  assertJSONUnicode(text)
  return parseLuxoJSON(text)
}

function parseLuxoJSON(text: string): unknown {
  requireLosslessJSON()
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
export function decodeJSONEnvelope(text: string, sourceValidated = false): Record<string, unknown> {
  const value = sourceValidated ? parseLuxoJSON(text) : decodeLuxoJSON(text, true)
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
  return validateJSONValue(value, depth)
}

/** Check the source before duplicate names or native parsing can hide deep values. */
export function decodeBoundedLuxoJSON(text: string): unknown {
  assertJSONDepth(text)
  return decodeLuxoJSON(text)
}

/** Generic values own their budget, independently of model/list/wire wrappers. */
export function encodeBoundedLuxoJSON(value: unknown): string {
  const text = encodeLuxoJSON(value)
  assertJSONDepth(text)
  return text
}

// This scanner checks only depth. Native parsing/serialization validates syntax,
// and the existing Unicode check still covers every key and string token.
function assertJSONDepth(text: string): void {
  if (text.length < 258) return
  // A valid string root has no containers; syntax is still checked by the caller.
  if (text.charCodeAt(0) === 34) return
  // Quoted brackets can only overestimate the number of possible containers.
  let potential = jsonOpeningCount(text, '[') + jsonOpeningCount(text, '{')
  if (potential <= 128) return
  let depth = 0
  for (let index = 0; index < text.length; index++) {
    const code = text.charCodeAt(index)
    if (code === 34) index = jsonStringEnd(text, index)
    else if (code === 91 || code === 123) {
      if (++depth > 128) throw new RangeError('JSON nesting exceeds 128 levels')
    } else if (code === 93 || code === 125) {
      depth--
      if (--potential <= 128) return
    }
  }
}

function jsonOpeningCount(text: string, delimiter: string): number {
  let count = 0
  for (let index = text.indexOf(delimiter); index >= 0; index = text.indexOf(delimiter, index + 1)) count++
  return count
}

function jsonStringEnd(text: string, start: number): number {
  for (let index = start + 1; index < text.length;) {
    const end = text.indexOf('"', index)
    if (end < 0) return text.length
    let escapes = 0
    for (let back = end - 1; back > start && text.charCodeAt(back) === 92; back--) escapes++
    if (escapes % 2 === 0) return end
    index = end + 1
  }
  return text.length
}

function validateJSONValue(value: unknown, depth: number): unknown {
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) throw new TypeError('Non-finite JSON number')
    return value
  }
  if (typeof value === 'string') assertUnicode(value)
  if (Array.isArray(value)) {
    if (depth >= 128) throw new RangeError('JSON nesting exceeds 128 levels')
    for (let index = 0; index < value.length; index++) validateJSONValue(value[index], depth + 1)
  } else if (value !== null && typeof value === 'object') {
    if (nativeJSON.isRawJSON(value)) {
      if (value.rawJSON.charCodeAt(0) === 34) assertJSONUnicode(value.rawJSON)
      return value
    }
    if (depth >= 128) throw new RangeError('JSON nesting exceeds 128 levels')
    const record = value as Record<string, unknown>
    for (const key of Object.keys(record)) {
      assertUnicode(key)
      validateJSONValue(record[key], depth + 1)
    }
  }
  return value
}
