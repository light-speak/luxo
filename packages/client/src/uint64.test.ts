import { describe, expect, it } from 'vitest'
import { Encoder } from './codec'
import { decodeJSONEnvelope } from './json'
import { jsonUint64, readWireUint64, UINT64_MAX } from './uint64'

function reference(value: bigint): Uint8Array {
  const bytes: number[] = []
  while (value >= 128n) {
    bytes.push(Number(value & 127n) | 128)
    value >>= 7n
  }
  return Uint8Array.from([...bytes, Number(value)])
}

describe('uint64 transport metadata', () => {
  it('keeps small IDs as numbers and full-width IDs as bigint without rounding', () => {
    const values = [0n, 1n, 127n, 128n, 253n, 1n << 32n, (1n << 53n) - 1n, 1n << 53n, (1n << 53n) + 1n, (1n << 63n) - 1n, 1n << 63n, UINT64_MAX]
    for (const value of values) {
      const expected = value <= BigInt(Number.MAX_SAFE_INTEGER) ? Number(value) : value
      expect(jsonUint64(decodeJSONEnvelope(`{"$id":${value}}`).$id)).toBe(expected)
      expect(jsonUint64(value)).toBe(expected)
      const encoder = new Encoder(0)
      encoder.writeUint64(expected)
      expect(encoder.bytes()).toEqual(reference(value))
      const framed = Uint8Array.from([2, ...encoder.bytes(), 42])
      expect(readWireUint64(framed, 1)).toEqual({ value: expected, offset: framed.length - 1 })
    }
  })

  it('rejects noninteger JSON tokens and out-of-range metadata', () => {
    for (const token of ['-0', '-1', 'true', 'false', 'null', '"1"', '[]', '{}', '1.0', '1e0', '1.5', '18446744073709551616']) {
      expect(jsonUint64(decodeJSONEnvelope(`{"$id":${token}}`).$id), token).toBeUndefined()
    }
    for (const value of [undefined, NaN, Infinity, -Infinity, 1.5, -1, 9007199254740992, -1n, UINT64_MAX + 1n]) {
      expect(jsonUint64(value)).toBeUndefined()
    }
    for (const value of [-1, 1.5, Infinity, 9007199254740992, -1n, UINT64_MAX + 1n]) {
      expect(() => new Encoder().writeUint64(value)).toThrow()
    }
    expect(() => new Encoder().writeUint64('1' as unknown as bigint)).toThrow(/bigint/)
  })

  it('rejects truncated and overflowing unsigned varints', () => {
    for (const bytes of [[], [128], [...Array(9).fill(128), 2], [...Array(10).fill(128), 0]]) {
      expect(readWireUint64(Uint8Array.from(bytes), 0)).toBeUndefined()
    }
    // Go's unsigned LEB128 reader accepts redundant zero groups too.
    expect(readWireUint64(Uint8Array.from([128, 0]), 0)).toEqual({ value: 0, offset: 2 })
  })

  it('matches an independent BigInt reference over 10,000 full-width values', () => {
    let value = 0x123456789abcdef0n
    const encoder = new Encoder()
    for (let index = 0; index < 10000; index++) {
      value = BigInt.asUintN(64, value * 6364136223846793005n + 1442695040888963407n)
      encoder.reset()
      encoder.writeUint64(value)
      expect(encoder.bytes()).toEqual(reference(value))
      const decoded = readWireUint64(encoder.bytes(), 0)
      expect(BigInt(decoded!.value)).toBe(value)
      expect(decoded!.offset).toBe(encoder.bytes().length)
    }
  })
})
