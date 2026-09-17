export const INT64_MIN = -(1n << 63n)
export const INT64_MAX = (1n << 63n) - 1n

/** Validate the schema Int/Duration representation without coercion or wrapping. */
export function assertInt64(value: bigint): void {
  if (typeof value !== 'bigint') throw new TypeError('Luxo Int/Duration requires bigint')
  if (value < INT64_MIN || value > INT64_MAX) throw new RangeError('int64 overflow')
}

/** Exact conversion for metadata and DateTime boundaries, never schema integers. */
export function int64ToSafeNumber(value: bigint): number {
  const result = Number(value)
  if (!Number.isSafeInteger(result)) throw new RangeError('integer exceeds the safe number range')
  return result
}

/** Zigzag uses two words; the per-byte loop contains no BigInt arithmetic. */
export function writeWireInt64(bytes: Uint8Array, offset: number, value: bigint): number {
  assertInt64(value)
  const low = Number(BigInt.asUintN(32, value))
  const high = Number(BigInt.asIntN(32, value >> 32n))
  const sign = high >> 31
  return writeWireWords(bytes, offset, ((low << 1) ^ sign) >>> 0, (((high << 1) | (low >>> 31)) ^ sign) >>> 0)
}

/** Shared unsigned two-word loop for schema zigzag values and transport IDs. */
export function writeWireWords(bytes: Uint8Array, offset: number, lo: number, hi: number): number {
  while (hi !== 0 || lo >= 128) {
    bytes[offset++] = (lo & 127) | 128
    lo = ((lo >>> 7) | (hi << 25)) >>> 0
    hi >>>= 7
  }
  bytes[offset++] = lo
  return offset
}
