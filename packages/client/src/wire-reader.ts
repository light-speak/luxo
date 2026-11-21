// Match Go codec limits; these bound metadata, not business Int64 values.
import { formatDateTime } from './datetime'

export const MAX_ARRAY_ELEMENTS = 1_000_000
export const MAX_COLUMNAR_RECORDS = 10_000_000
export const MAX_ARENA_SIZE = 64 * 1024 * 1024

// Each field is UTF-8 data, not a document with an optional BOM to strip.
const textDecoder = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true })

export function decodeUTF8(bytes: Uint8Array): string {
  return textDecoder.decode(bytes)
}

/** Shared cursor; row and column decoders do not allocate an additional state object. */
export class WireReader {
  protected wireOffset = 0
  error: string | null = null

  constructor(protected readonly wireBytes: Uint8Array) {}

  /** Read floor seconds and nanoseconds; a missing fraction is not legacy seconds. */
  readDateTime(): string {
    if (this.error !== null) return ''
    const seconds = this.readSigned()
    const nanos = this.readBoundedUnsigned(999999999, 'DateTime nanoseconds')
    if (this.error !== null) return ''
    return formatDateTime(seconds, nanos)
  }

  protected acceptFloat(value: number): boolean {
    if (Number.isFinite(value)) return true
    this.error ??= `non-finite Float at offset ${this.wireOffset}`
    return false
  }

  protected readBoundedUnsigned(limit: number, kind: string): number {
    if (this.error !== null) return 0
    const value = this.readUnsigned()
    if (value <= limit) return value
    this.error ??= `${kind} ${value} exceeds limit ${limit}`
    return 0
  }

  protected requireBytes(length: number, kind: string): boolean {
    if (this.error !== null) return false
    if (length <= this.wireBytes.length - this.wireOffset) return true
    this.error = `truncated ${kind} at offset ${this.wireOffset}`
    return false
  }

  protected readLength(kind: string): number {
    if (this.error !== null) return -1
    const length = this.readUnsigned()
    return this.requireBytes(length, kind) ? length : -1
  }

  protected readArrayCount(minimumWidth = 1): number {
    const count = this.readBoundedUnsigned(MAX_ARRAY_ELEMENTS, 'array count')
    return this.requireBytes(count * minimumWidth, 'array') ? count : 0
  }

  /** Decode a byte range after its caller has checked the length. */
  protected readStringBytes(length: number): string {
    const start = this.wireOffset
    this.wireOffset += length
    try {
      return textDecoder.decode(this.wireBytes.subarray(start, this.wireOffset))
    } catch {
      this.error ??= `invalid UTF-8 at offset ${start}`
      return ''
    }
  }

  protected readUnsigned(): number {
    const start = this.wireOffset
    if (this.wireOffset < this.wireBytes.length) {
      const first = this.wireBytes[this.wireOffset++]
      if (first < 128) return first
      let value = first & 127
      let factor = 128
      for (let i = 1; i < 8 && this.wireOffset < this.wireBytes.length; i++) {
        const byte = this.wireBytes[this.wireOffset++]
        if (i === 7 && byte > 15) {
          this.error ??= `metadata varint overflow at offset ${start}`
          return 0
        }
        value += (byte & 127) * factor
        if (byte < 128) return value
        factor *= 128
      }
    }
    this.error ??= `truncated varint at offset ${start}`
    return 0
  }

  protected readSigned(): bigint {
    const start = this.wireOffset
    let lo = 0
    let hi = 0
    for (let i = 0; i < 10 && this.wireOffset < this.wireBytes.length; i++) {
      const byte = this.wireBytes[this.wireOffset++]
      if (i === 9 && byte > 1) {
        this.error ??= `int64 varint overflow at offset ${start}`
        return 0n
      }
      const part = byte & 127
      const shift = i * 7
      if (shift < 32) {
        lo |= part << shift
        if (shift > 25) hi |= part >>> (32 - shift)
      } else {
        hi |= part << (shift - 32)
      }
      if (byte < 128) {
        const unsigned = (BigInt(hi >>> 0) << 32n) | BigInt(lo >>> 0)
        return (unsigned >> 1n) ^ -BigInt(lo & 1)
      }
    }
    this.error ??= `truncated int64 varint at offset ${start}`
    return 0n
  }
}
