import { writeWireWords } from './int64'

export type UInt64 = number | bigint

export const UINT64_MAX = 0xffffffffffffffffn
const SAFE_MAX = 0x1fffffffffffffn

/** Preserve the number fast path without narrowing the unsigned wire domain. */
export function jsonUint64(value: unknown): UInt64 | undefined {
  if (typeof value === 'number') {
    return Number.isSafeInteger(value) && value >= 0 && !Object.is(value, -0) ? value : undefined
  }
  if (typeof value !== 'bigint' || value < 0n || value > UINT64_MAX) return undefined
  return value <= SAFE_MAX ? Number(value) : value
}

/** Full-width LEB128 writer; per-byte operations use two numeric words, not BigInt. */
export function writeWireUint64(bytes: Uint8Array, offset: number, value: bigint): number {
  if (typeof value !== 'bigint') throw new TypeError('uint64 requires bigint')
  if (value < 0n || value > UINT64_MAX) throw new RangeError('uint64 overflow')
  return writeWireWords(bytes, offset, Number(BigInt.asUintN(32, value)), Number(value >> 32n))
}

/** Read at a caller-owned byte offset; malformed IDs never become zero. */
export function readWireUint64(bytes: Uint8Array, start: number): { value: UInt64; offset: number } | undefined {
  let low = 0
  let high = 0
  let offset = start
  for (let index = 0; index < 10 && offset < bytes.length; index++) {
    const byte = bytes[offset++]
    if (index === 9 && byte > 1) return undefined
    const part = byte & 127
    const shift = index * 7
    if (shift < 32) {
      low |= part << shift
      if (shift > 25) high |= part >>> (32 - shift)
    } else {
      high |= part << (shift - 32)
    }
    if (byte < 128) {
      high >>>= 0
      const value = high <= 0x1fffff ? high * 0x100000000 + (low >>> 0)
        : (BigInt(high) << 32n) | BigInt(low >>> 0)
      return { value, offset }
    }
  }
  return undefined
}
