import { int64ToSafeNumber } from './int64'

const minSeconds = -62167219200
const maxSeconds = 253402300799
const timestampPattern = /^([0-9]{4})-([0-9]{2})-([0-9]{2})[Tt]([0-9]{2}):([0-9]{2}):([0-9]{2})(?:\.([0-9]{1,9}))?([Zz]|[+-]([0-9]{2}):([0-9]{2}))$/

/** Format an exact timestamp without passing nanoseconds through a JS Date. */
export function formatDateTime(seconds: number | bigint, nanoseconds: number): string {
  const exact = typeof seconds === 'bigint' ? int64ToSafeNumber(seconds) : seconds
  if (!Number.isSafeInteger(exact) || exact < minSeconds || exact > maxSeconds) throw new RangeError('DateTime out of range')
  if (!Number.isInteger(nanoseconds) || nanoseconds < 0 || nanoseconds >= 1e9) throw new RangeError('invalid DateTime nanoseconds')
  const whole = new Date(exact * 1000).toISOString().replace('.000Z', '')
  if (nanoseconds === 0) return whole + 'Z'
  return whole + '.' + String(nanoseconds).padStart(9, '0').replace(/0+$/, '') + 'Z'
}

/** Parse seconds and the decimal fraction separately; Date only sees whole seconds. */
export function parseDateTime(value: string): { seconds: bigint; nanoseconds: number } {
  const parts = timestampPattern.exec(value)
  if (!parts || parts[0].length !== value.length) throw new RangeError('DateTime requires an RFC3339 string with at most 9 fractional digits')
  const year = Number(parts[1]), month = Number(parts[2]), day = Number(parts[3])
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0)
  const days = month === 2 ? (leap ? 29 : 28) : (month === 4 || month === 6 || month === 9 || month === 11 ? 30 : 31)
  if (month < 1 || month > 12 || day < 1 || day > days || Number(parts[4]) > 23 || Number(parts[5]) > 59 || Number(parts[6]) > 59 || Number(parts[9] ?? 0) > 23 || Number(parts[10] ?? 0) > 59) {
    throw new RangeError('invalid DateTime calendar or offset')
  }
  const whole = value.slice(0, 19).replace('t', 'T') + parts[8]!.replace('z', 'Z')
  const seconds = Date.parse(whole) / 1000
  if (!Number.isSafeInteger(seconds) || seconds < minSeconds || seconds > maxSeconds) throw new RangeError('DateTime out of range')
  const fraction = parts[7] ?? ''
  return { seconds: BigInt(seconds), nanoseconds: fraction === '' ? 0 : Number(fraction) * 10 ** (9 - fraction.length) }
}

/** Validate typed JSON dates without reparsing or re-encoding a JSON document. */
export function dateTimeFromJSON(value: unknown): string {
  if (typeof value !== 'string') throw new TypeError('expected a DateTime JSON string')
  parseDateTime(value)
  return value
}
