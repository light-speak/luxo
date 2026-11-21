import { measure } from './measure.bench'
import { encodeLuxoJSON } from './json'

type JSONModule = typeof import('./json')
const beforeNativeJSON = JSON as JSON & { rawJSON(text: string): unknown; isRawJSON(value: unknown): boolean }

function beforeRequireLosslessJSON(): void {
  if (typeof beforeNativeJSON.rawJSON !== 'function' || typeof beforeNativeJSON.isRawJSON !== 'function') {
    throw new Error('Luxo JSON requires JSON.rawJSON and JSON.parse source access; upgrade the JavaScript runtime')
  }
}

let beforeIndentNegativeZero: unknown

/** Frozen compact writer before adding optional diagnostic indentation. */
function beforeJSONFormatting(value: unknown): string {
  beforeRequireLosslessJSON()
  const encoded = JSON.stringify(value, (_key: string, item: unknown): unknown => {
    if (typeof item === 'bigint') return beforeNativeJSON.rawJSON(item.toString())
    if (typeof item === 'number') {
      if (!Number.isFinite(item)) throw new RangeError('non-finite JSON number')
      if (Object.is(item, -0)) return beforeIndentNegativeZero ??= beforeNativeJSON.rawJSON('-0')
    }
    return item
  })
  if (encoded === undefined) throw new TypeError('value is not JSON encodable')
  return encoded
}

export function measureJSONFormatting(): void {
  const row = { id: 9223372036854775807n, value: 1.25, label: 'task' }
  for (const [name, value] of Object.entries({
    integer: { value: 42n }, fraction: { value: 1.25 }, negativeZero: { value: -0 },
    nested16: Array.from({ length: 16 }, () => row),
    text72KiB: { value: '任务'.repeat(12_288) },
  })) measure(`compact JSON with optional indentation ${name}`, () => beforeJSONFormatting(value), () => encodeLuxoJSON(value))
}

/** Frozen lossless reader before reusing the reviver and its numeric-syntax test. */
export function beforeSharedJSONReviver(text: string): unknown {
  beforeRequireLosslessJSON()
  return JSON.parse(text, (_key: string, value: unknown, context?: { source?: string }): unknown => {
    if (typeof value !== 'number') return value
    const source = context?.source
    if (source === undefined) throw new Error('Luxo JSON requires JSON.parse source access')
    if (source !== '-0' && !/[.eE]/.test(source)) {
      return Number.isSafeInteger(value) ? value : BigInt(source)
    }
    return beforeNativeJSON.rawJSON(source)
  }) as unknown
}

export function measureSharedJSONReviver(after: JSONModule): void {
  const row = '{"id":9223372036854775807,"amount":1.25,"nested":{"ratio":0.125}}'
  const fixtures = {
    integer: '42', fraction: '1.25', negativeZero: '-0', exponent: '1E+03',
    largeInteger: '9223372036854775807', strings: '{"label":"task","ready":true,"empty":null}',
    smallFraction: '{"amount":1.25}', nested16: `[${Array.from({ length: 16 }, () => row).join(',')}]`,
  }
  for (const [name, text] of Object.entries(fixtures)) {
    if (encodeLuxoJSON(beforeSharedJSONReviver(text)) !== encodeLuxoJSON(after.decodeLuxoJSON(text))) {
      throw new Error(`${name}: unequal numeric tokens`)
    }
    measure(`shared reviver ${name}`, () => beforeSharedJSONReviver(text), () => after.decodeLuxoJSON(text))
  }
}

/** Compare snapshots compiled by the same TS compiler and executed by native Node. */
export function measureJSONNumbers(before: JSONModule, after: JSONModule): void {
  const row = '{"id":9223372036854775807,"amount":1.25,"nested":{"ratio":0.125}}'
  const fixtures = {
    smallInteger: '{"id":9223372036854775807}',
    smallFraction: '{"amount":1.25}',
    nested16: `[${Array.from({ length: 16 }, () => row).join(',')}]`,
  }
  for (const [name, text] of Object.entries(fixtures)) {
    measure(`generic read ${name}`, () => before.decodeLuxoJSON(text), () => after.decodeLuxoJSON(text))
    measure(`envelope generic field ${name}`,
      () => before.normalizeLuxoJSON(before.decodeLuxoJSON(text, true)),
      () => after.normalizeLuxoJSON(after.decodeLuxoJSON(text, true)))
    measure(`forward ${name}`, () => before.encodeLuxoJSON(before.decodeLuxoJSON(text)),
      () => after.encodeLuxoJSON(after.decodeLuxoJSON(text)))
  }
  const readTyped = (sdk: JSONModule) => {
    const value = sdk.decodeLuxoJSON(row, true) as Record<string, unknown>
    return { id: sdk.int64FromJSON(value.id), amount: sdk.floatFromJSON(value.amount) }
  }
  measure('typed Int and Float response', () => readTyped(before), () => readTyped(after))
}

/** Frozen pre-signed-zero writer; compare only inputs it can encode correctly. */
function beforeSignedZero(value: unknown): string {
  const nativeJSON = JSON as JSON & { rawJSON(text: string): unknown; isRawJSON(value: unknown): boolean }
  if (typeof nativeJSON.rawJSON !== 'function' || typeof nativeJSON.isRawJSON !== 'function') {
    throw new Error('Luxo JSON requires JSON.rawJSON and JSON.parse source access; upgrade the JavaScript runtime')
  }
  const encoded = JSON.stringify(value, (_key: string, item: unknown): unknown => {
    if (typeof item === 'bigint') return nativeJSON.rawJSON(item.toString())
    if (typeof item === 'number' && !Number.isFinite(item)) throw new RangeError('non-finite JSON number')
    return item
  })
  if (encoded === undefined) throw new TypeError('value is not JSON encodable')
  return encoded
}

export function measureJSONSignedZeroEncoding(): void {
  const rows = Array.from({ length: 16 }, (_, index) => ({ id: BigInt(index), amount: index + 0.25, ratio: 0.125 }))
  for (const [name, value] of Object.entries({
    integer: { value: 42n }, positiveZero: { value: 0 }, fraction: { value: 1.25 },
    floatArray128: Array.from({ length: 128 }, (_, index) => index + 0.25), nested16: rows,
  })) {
    measure(`signed zero guard ${name}`, () => beforeSignedZero(value), () => encodeLuxoJSON(value))
  }
  // The old writer's incorrect zero output is not an equivalent baseline.
  if (encodeLuxoJSON({ value: -0 }) !== '{"value":-0}') throw new Error('negative zero was lost')
}
