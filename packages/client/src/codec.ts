// Luxo binary wire format — TypeScript implementation.
// Matches Go pkg/lux/codec/wire.go exactly.
//
// Wire format per field: [fieldID varint] [value]
// Types are known at compile time from schema — no type tags on wire.
//
// Encoding rules:
//   - Int: signed ZigZag varint; Boolean: one byte; Enum: string
//   - Float: fixed 8 bytes (little-endian float64)
//   - String/Bytes: length-prefixed (varint length + raw bytes)
//   - Nullable: 1-byte flag (0x00=null, 0x01=present) + value if present
//   - Message end: 0x00 terminator (fieldID 0 is reserved)

import { writeWireInt64 } from './int64'
import { formatDateTime, parseDateTime } from './datetime'
import { writeWireUint64 } from './uint64'
import { MAX_ARENA_SIZE, MAX_COLUMNAR_RECORDS, WireReader, decodeUTF8 } from './wire-reader'
import { decodeLuxoJSON } from './json'
import { assertUnicode } from './unicode'

const textEncoder = new TextEncoder()

function encodeUTF8(value: string): Uint8Array {
  assertUnicode(value)
  return textEncoder.encode(value)
}

const INITIAL_BUF_SIZE = 1024

// --- UUID helpers ---
// On the wire a UUID is a fixed 16-byte value (no length prefix). In JS it is
// represented as the canonical 36-char lowercase string "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx".

const HEX = '0123456789abcdef'

// Precomputed byte → 2-char hex lookup (avoids per-byte string ops on hot path).
const BYTE_HEX: string[] = new Array(256)
for (let i = 0; i < 256; i++) BYTE_HEX[i] = HEX[i >> 4] + HEX[i & 0x0f]

/** Format 16 bytes (read from `data` at `off`) as a canonical UUID string. */
export function formatUUID(data: Uint8Array, off: number): string {
  // Insert '-' after the 4th, 6th, 8th and 10th byte. Matches Go appendUUIDString.
  let s = ''
  for (let i = 0; i < 16; i++) {
    if (i === 4 || i === 6 || i === 8 || i === 10) s += '-'
    s += BYTE_HEX[data[off + i]]
  }
  return s
}

// --- DateTime helpers ---
// The wire carries floor Unix seconds and nanoseconds. Public DateTime values
// remain RFC3339 strings, so JavaScript's millisecond Date cannot erase precision.

/** Convert unix seconds to an RFC3339 string, matching Go's DateTime JSON output. */
export function unixSecondsToISO(seconds: number | bigint): string {
  return formatDateTime(seconds, 0)
}

/** Decode canonical base64 JSON representation into raw bytes. */
export function decodeBase64(value: string): Uint8Array {
  const binary = atob(value)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  return bytes
}

/** Decode a JSON value from its raw binary payload. */
export function decodeJSONValue(value: Uint8Array): unknown {
  return decodeLuxoJSON(decodeUTF8(value))
}

/** Decode a single hex digit, or -1 if not a hex char. */
function hexNibble(c: number): number {
  if (c >= 0x30 && c <= 0x39) return c - 0x30 // 0-9
  if (c >= 0x61 && c <= 0x66) return c - 0x61 + 10 // a-f
  if (c >= 0x41 && c <= 0x46) return c - 0x41 + 10 // A-F
  return -1
}

// Fixed hex-pair offsets in canonical UUID text; no per-value table allocation.
const UUID_HEX_OFFSETS = [0, 2, 4, 6, 9, 11, 14, 16, 19, 21, 24, 26, 28, 30, 32, 34]

/** Parse a canonical 36-char UUID string into 16 bytes. Returns null on malformed input. */
export function parseUUID(s: string): Uint8Array | null {
  if (s.length !== 36) return null
  if (s.charCodeAt(8) !== 0x2d || s.charCodeAt(13) !== 0x2d ||
      s.charCodeAt(18) !== 0x2d || s.charCodeAt(23) !== 0x2d) return null
  const out = new Uint8Array(16)
  for (let j = 0; j < 16; j++) {
    const i = UUID_HEX_OFFSETS[j]
    const hi = hexNibble(s.charCodeAt(i))
    const lo = hexNibble(s.charCodeAt(i + 1))
    if (hi < 0 || lo < 0) return null
    out[j] = (hi << 4) | lo
  }
  return out
}

// --- Encoder ---

export class Encoder {
  private buf: Uint8Array
  private view: DataView
  private pos: number

  constructor(initialSize: number = INITIAL_BUF_SIZE) {
    this.buf = new Uint8Array(initialSize)
    this.view = new DataView(this.buf.buffer)
    this.pos = 0
  }

  /** Ensure at least `n` bytes available */
  private grow(n: number): void {
    if (this.pos + n <= this.buf.length) return
    let newLen = this.buf.length || 64
    const needed = this.pos + n
    while (newLen < needed) newLen *= 2
    const next = new Uint8Array(newLen)
    next.set(this.buf, 0)
    this.buf = next
    this.view = new DataView(this.buf.buffer)
  }

  /** Write unsigned LEB128 varint */
  writeVarint(v: number): void {
    if (!Number.isSafeInteger(v) || v < 0) throw new RangeError('invalid metadata integer')
    if (v < 0x80) {
      this.grow(1)
      this.buf[this.pos++] = v
      return
    }
    // up to 10 bytes for 64-bit, but JS numbers max at 53 bits (8 bytes)
    this.grow(8)
    while (v >= 0x80) {
      this.buf[this.pos++] = (v & 0x7F) | 0x80
      v = Math.floor(v / 128) // avoid >>> which is 32-bit
    }
    this.buf[this.pos++] = v
  }

  /** Write a full-width unsigned ID without widening field-ID and length metadata. */
  writeUint64(value: number | bigint): void {
    if (typeof value === 'number') {
      this.writeVarint(value)
      return
    }
    this.grow(10)
    this.pos = writeWireUint64(this.buf, this.pos, value)
  }

  /** Write signed zigzag-encoded varint (matches Go: (v<<1) ^ (v>>63)) */
  writeSvarint(v: bigint): void {
    this.grow(10)
    this.pos = writeWireInt64(this.buf, this.pos, v)
  }

  /** Write an RFC3339 timestamp with its exact nanosecond fraction. */
  writeDateTime(value: string): void {
    const timestamp = parseDateTime(value)
    this.writeSvarint(timestamp.seconds)
    this.writeVarint(timestamp.nanoseconds)
  }

  /** Write float64 as 8 bytes little-endian */
  writeFixed64(v: number): void {
    if (!Number.isFinite(v)) throw new RangeError('Float must be finite')
    this.grow(8)
    this.view.setFloat64(this.pos, v, true)
    this.pos += 8
  }

  /** Write length-prefixed UTF-8 string */
  writeString(v: string): void {
    this.writeBytes(encodeUTF8(v))
  }

  /** Write boolean as varint 0 or 1 */
  writeBool(v: boolean): void {
    this.grow(1)
    this.buf[this.pos++] = v ? 1 : 0
  }

  /** Write bytes without a length prefix. */
  writeRawBytes(v: Uint8Array): void {
    this.grow(v.length)
    this.buf.set(v, this.pos)
    this.pos += v.length
  }

  /** Write length-prefixed bytes. */
  writeBytes(v: Uint8Array): void {
    this.writeVarint(v.length)
    this.writeRawBytes(v)
  }

  /** Write a length-delimited value directly into this encoder's buffer. */
  writeDelimited(writeValue: (encoder: Encoder) => void): void {
    const prefixPosition = this.pos
    this.grow(1)
    this.pos++
    const valuePosition = this.pos
    try {
      writeValue(this)
    } catch (error) {
      this.pos = prefixPosition
      throw error
    }
    const valueLength = this.pos - valuePosition
    const prefixLength = varintLength(valueLength)
    const shift = prefixLength - 1
    if (shift > 0) {
      this.grow(shift)
      this.buf.copyWithin(valuePosition + shift, valuePosition, this.pos)
      this.pos += shift
    }
    this.writeVarintAt(prefixPosition, valueLength)
  }

  private writeVarintAt(position: number, value: number): void {
    while (value >= 0x80) {
      this.buf[position++] = (value & 0x7f) | 0x80
      value = Math.floor(value / 128)
    }
    this.buf[position] = value
  }

  /** Write a fixed 16-byte UUID (no length prefix). Accepts a canonical string or raw bytes. */
  writeUUID(v: string | Uint8Array): void {
    const bytes = typeof v === 'string' ? parseUUID(v) : v
    if (!bytes || bytes.length !== 16) throw new Error(`invalid UUID: ${typeof v === 'string' ? v : '[bytes]'}`)
    this.grow(16)
    this.buf.set(bytes, this.pos)
    this.pos += 16
  }

  // --- Field writers (fieldID + value) ---

  writeFieldInt(fieldID: number, v: bigint): void {
    this.writeVarint(fieldID)
    this.writeSvarint(v)
  }

  writeFieldFloat(fieldID: number, v: number): void {
    this.writeVarint(fieldID)
    this.writeFixed64(v)
  }

  writeFieldString(fieldID: number, v: string): void {
    const encoded = encodeUTF8(v)
    this.writeVarint(fieldID)
    this.writeBytes(encoded)
  }

  writeFieldBool(fieldID: number, v: boolean): void {
    this.writeVarint(fieldID)
    this.writeBool(v)
  }

  writeFieldBytes(fieldID: number, v: Uint8Array): void {
    this.writeVarint(fieldID)
    this.writeBytes(v)
  }

  writeFieldUUID(fieldID: number, v: string | Uint8Array): void {
    this.writeVarint(fieldID)
    this.writeUUID(v)
  }

  // --- Array field writers (fieldID + [count][items...]) ---
  // Array count is a plain varint (not zigzag); each item is encoded per element type.

  writeFieldIntArray(fieldID: number, v: bigint[]): void {
    this.writeVarint(fieldID)
    this.writeVarint(v.length)
    for (const item of v) this.writeSvarint(item)
  }

  writeFieldFloatArray(fieldID: number, v: number[]): void {
    this.writeVarint(fieldID)
    this.writeVarint(v.length)
    for (const item of v) this.writeFixed64(item)
  }

  writeFieldStringArray(fieldID: number, v: string[]): void {
    const start = this.pos
    try {
      this.writeVarint(fieldID)
      this.writeVarint(v.length)
      for (const item of v) this.writeString(item)
    } catch (error) {
      this.pos = start
      throw error
    }
  }

  writeFieldBoolArray(fieldID: number, v: boolean[]): void {
    this.writeVarint(fieldID)
    this.writeVarint(v.length)
    for (const item of v) this.writeBool(item)
  }

  writeFieldUUIDArray(fieldID: number, v: (string | Uint8Array)[]): void {
    this.writeVarint(fieldID)
    this.writeVarint(v.length)
    for (const item of v) this.writeUUID(item)
  }

  writeFieldBytesArray(fieldID: number, v: Uint8Array[]): void {
    this.writeVarint(fieldID)
    this.writeVarint(v.length)
    for (const item of v) this.writeBytes(item)
  }

  /** Write end marker (fieldID 0) */
  writeEnd(): void {
    this.grow(1)
    this.buf[this.pos++] = 0x00
  }

  /** Return a trimmed copy of the encoded data (safe for fetch body) */
  bytes(): Uint8Array {
    return this.buf.slice(0, this.pos)
  }

  /** Reset encoder for reuse without reallocating */
  reset(): void {
    this.pos = 0
  }
}

function varintLength(value: number): number {
  let length = 1
  while (value >= 0x80) {
    value = Math.floor(value / 128)
    length++
  }
  return length
}

// --- Decoder ---

export class Decoder extends WireReader {
  private view: DataView
  fieldID: number

  constructor(data: Uint8Array) {
    super(data)
    // zero-copy: use the buffer directly
    this.view = new DataView(data.buffer, data.byteOffset, data.byteLength)
    this.fieldID = 0
  }

  /** Skip the arena header (totalStringLen varint) that prefixes each model's binary data. */
  skipArenaHeader(): void {
    this.readBoundedUnsigned(MAX_ARENA_SIZE, 'arena size')
  }

  /** Read next field ID. EOF without an end marker is a protocol error. */
  nextField(): boolean {
    if (this.error !== null) return false
    if (this.wireOffset >= this.wireBytes.length) {
      this.error = 'missing row end marker'
      this.fieldID = 0
      return false
    }
    const id = this.readVarintRaw()
    this.fieldID = id
    return id !== 0
  }

  /** Read unsigned LEB128 varint */
  private readVarintRaw(): number {
    return this.readUnsigned()
  }

  /** Read signed zigzag-encoded int */
  readInt(): bigint {
    return this.readSigned()
  }

  /** Decode a complete scalar response and reject malformed or trailing bytes. */
  readIntChecked(): bigint {
    const value = this.readInt()
    checkComplete(this)
    return value
  }

  readIntArrayChecked(): bigint[] {
    const values = this.readIntArray()
    checkComplete(this)
    return values
  }

  /** Read float64 from 8 bytes little-endian */
  readFloat(): number {
    if (!this.requireBytes(8, 'fixed64')) return 0
    const v = this.view.getFloat64(this.wireOffset, true)
    if (!this.acceptFloat(v)) return 0
    this.wireOffset += 8
    return v
  }

  /** Read length-prefixed UTF-8 string */
  readString(): string {
    const len = this.readLength('string')
    if (len <= 0) return ''
    return this.readStringBytes(len)
  }

  /** Read length-prefixed bytes as a zero-copy view. */
  readBytes(): Uint8Array {
    const len = this.readVarintRaw()
    if (this.error !== null) return new Uint8Array(0)
    if (len === 0) return new Uint8Array(0)
    if (this.wireOffset + len > this.wireBytes.length) {
      this.error ??= `truncated bytes at offset ${this.wireOffset}`
      return new Uint8Array(0)
    }
    const bytes = this.wireBytes.subarray(this.wireOffset, this.wireOffset + len)
    this.wireOffset += len
    return bytes
  }

  /** Read boolean (varint 0 or 1) */
  readBool(): boolean {
    return this.readCanonicalMarker('bool')
  }

  /** Read a fixed 16-byte UUID, returning the canonical 36-char string. */
  readUUID(): string {
    if (this.wireOffset + 16 > this.wireBytes.length) {
      this.error ??= `truncated uuid at offset ${this.wireOffset}`
      return '00000000-0000-0000-0000-000000000000'
    }
    const s = formatUUID(this.wireBytes, this.wireOffset)
    this.wireOffset += 16
    return s
  }

  // --- Scalar array readers (row mode: [count][item0][item1]...) ---
  // count is a plain varint; each item is encoded per element type.

  readIntArray(): bigint[] {
    const count = this.readArrayCount()
    const out: bigint[] = new Array(count)
    for (let i = 0; i < count; i++) out[i] = this.readInt()
    return out
  }

  readFloatArray(): number[] {
    const count = this.readArrayCount(8)
    const out: number[] = new Array(count)
    for (let i = 0; i < count; i++) out[i] = this.readFloat()
    return out
  }

  readStringArray(): string[] {
    const count = this.readArrayCount()
    const out: string[] = new Array(count)
    for (let i = 0; i < count; i++) {
      out[i] = this.readString()
      if (this.error !== null) return []
    }
    return out
  }

  readBoolArray(): boolean[] {
    const count = this.readArrayCount()
    const out: boolean[] = new Array(count)
    for (let i = 0; i < count; i++) out[i] = this.readBool()
    return out
  }

  readUUIDArray(): string[] {
    const count = this.readArrayCount(16)
    const out: string[] = new Array(count)
    for (let i = 0; i < count; i++) out[i] = this.readUUID()
    return out
  }

  readBytesArray(): Uint8Array[] {
    const count = this.readArrayCount()
    const out: Uint8Array[] = new Array(count)
    for (let i = 0; i < count; i++) out[i] = this.readBytes()
    return out
  }

  // --- Nullable readers (1-byte flag + value if present) ---

  readIntPtr(): bigint | null {
    if (!this.readCanonicalMarker('nullable')) return null
    return this.readInt()
  }

  readFloatPtr(): number | null {
    if (!this.readCanonicalMarker('nullable')) return null
    return this.readFloat()
  }

  readStringPtr(): string | null {
    if (!this.readCanonicalMarker('nullable')) return null
    return this.readString()
  }

  readBoolPtr(): boolean | null {
    if (!this.readCanonicalMarker('nullable')) return null
    return this.readBool()
  }

  readUUIDPtr(): string | null {
    if (!this.readCanonicalMarker('nullable')) return null
    return this.readUUID()
  }

  /** Read a nullable seconds/nanoseconds pair. */
  readDateTimePtr(): string | null {
    if (!this.readCanonicalMarker('nullable')) return null
    const value = this.readDateTime()
    return this.error === null ? value : null
  }

  readDateTimeArray(): string[] {
    const count = this.readArrayCount(2)
    if (this.error !== null) return []
    const values: string[] = new Array(count)
    for (let i = 0; i < count; i++) {
      values[i] = this.readDateTime()
      if (this.error !== null) return []
    }
    return values
  }

  /** Read a nested model using a decoder function. */
  readNullable<T>(decode: () => T): T | null {
    if (!this.readCanonicalMarker('nullable')) return null
    return decode()
  }

  private readCanonicalMarker(kind: 'bool' | 'nullable'): boolean {
    if (this.wireOffset >= this.wireBytes.length) {
      this.error ??= `truncated ${kind} marker at offset ${this.wireOffset}`
      return false
    }
    const offset = this.wireOffset
    const marker = this.wireBytes[this.wireOffset++]
    if (marker === 0x00) return false
    if (marker === 0x01) return true
    this.error ??= `invalid ${kind} marker 0x${marker.toString(16).padStart(2, '0')} at offset ${offset}`
    return false
  }

  /** Read an array of items using a decoder function. */
  readArray<T>(decode: () => T): T[] {
    const count = this.readArrayCount()
    const items: T[] = []
    for (let i = 0; i < count && this.error === null; i++) {
      items.push(decode())
    }
    return items
  }

  /** Skip remaining bytes (useful when encountering unknown fields) */
  get remaining(): number {
    return this.wireBytes.length - this.wireOffset
  }
}

// --- Columnar Decoder ---
// Reads columnar-encoded list data:
// [count varint][arena size varint][fieldID][vals...]...[0x00]

export class ColumnarDecoder extends WireReader {
  private view: DataView
  readonly count: number
  readonly arenaSize: number
  fieldID: number

  constructor(data: Uint8Array) {
    super(data)
    this.view = new DataView(data.buffer, data.byteOffset, data.byteLength)
    this.fieldID = 0
    this.count = this.readBoundedUnsigned(MAX_COLUMNAR_RECORDS, 'column count')
    this.arenaSize = this.readBoundedUnsigned(MAX_ARENA_SIZE, 'arena size')
    if (this.error === null && this.count > 0) {
      if (this.count > this.wireBytes.length - this.wireOffset - 2) {
        this.error = 'insufficient columnar element bytes'
      } else {
        const start = this.wireOffset
        let id = this.wireBytes[start]
        if (id >= 0x80) {
          id = this.readVarintRaw()
          if (this.count > this.wireBytes.length - this.wireOffset - 1) {
            this.error ??= 'insufficient columnar element bytes'
          }
          this.wireOffset = start
        }
        if (id === 0 && this.error === null) {
          this.error = 'nonempty list requires at least one column'
        }
      }
    }
  }

  /** Advance to next column. EOF without an end marker is a protocol error. */
  nextColumn(): boolean {
    if (this.error !== null) return false
    if (this.wireOffset >= this.wireBytes.length) {
      this.error = 'missing column end marker'
      return false
    }
    const id = this.readVarintRaw()
    this.fieldID = id
    if (id === 0) return false
    return true
  }

  /** Read count svarint values (zigzag-encoded int column). */
  readColumnInt(): bigint[] {
    if (!this.requireBytes(this.count, 'int64 column')) return []
    const result: bigint[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      result[i] = this.readSvarint()
    }
    return result
  }

  /** Read count fixed64 float values. */
  readColumnFloat(): number[] {
    if (!this.requireBytes(this.count * 8, 'float column')) return []
    const result: number[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      result[i] = this.view.getFloat64(this.wireOffset, true)
      if (!this.acceptFloat(result[i])) return []
      this.wireOffset += 8
    }
    return result
  }

  /** Read count length-prefixed string values. */
  readColumnString(): string[] {
    if (!this.requireBytes(this.count, 'string column')) return []
    const result: string[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      const len = this.readLength('string')
      if (len < 0) return []
      if (len === 0) { result[i] = ''; continue }
      result[i] = this.readStringBytes(len)
      if (this.error !== null) return []
    }
    return result
  }

  /** Read count boolean values (varint 0/1). */
  readColumnBool(): boolean[] {
    if (!this.requireBytes(this.count, 'bool column')) return []
    const result: boolean[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      result[i] = this.readCanonicalMarker('bool')
    }
    return result
  }

  /** Read count fixed 16-byte UUID values (canonical strings). */
  readColumnUUID(): string[] {
    if (!this.requireBytes(this.count * 16, 'uuid column')) return []
    const result: string[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      result[i] = formatUUID(this.wireBytes, this.wireOffset)
      this.wireOffset += 16
    }
    return result
  }

  /** Read count length-prefixed byte blobs. Each cell is a length-prefixed blob —
   *  used for scalar array columns ([T]), where each cell holds [count][items...]. */
  readColumnBytes(): Uint8Array[] {
    if (!this.requireBytes(this.count, 'bytes column')) return []
    const result: Uint8Array[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      const len = this.readLength('bytes')
      if (len < 0) return []
      result[i] = this.wireBytes.subarray(this.wireOffset, this.wireOffset + len)
      this.wireOffset += len
    }
    return result
  }

  /** Read count nullable byte blobs (0x00=null, 0x01+length+bytes). */
  readColumnBytesPtr(): (Uint8Array | null)[] {
    if (!this.requireBytes(this.count, 'nullable bytes column')) return []
    const result: (Uint8Array | null)[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      if (!this.readCanonicalMarker('nullable')) { result[i] = null; continue }
      const len = this.readLength('bytes')
      if (len < 0) return []
      result[i] = this.wireBytes.subarray(this.wireOffset, this.wireOffset + len)
      this.wireOffset += len
    }
    return result
  }

  /** Read count nullable UUID values (0x00=null, 0x01+16 bytes). */
  readColumnUUIDPtr(): (string | null)[] {
    if (!this.requireBytes(this.count, 'nullable uuid column')) return []
    const result: (string | null)[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      if (!this.readCanonicalMarker('nullable')) { result[i] = null; continue }
      if (!this.requireBytes(16, 'uuid')) return []
      result[i] = formatUUID(this.wireBytes, this.wireOffset)
      this.wireOffset += 16
    }
    return result
  }

  /** Read count nullable int values (0x00=null, 0x01+svarint). */
  readColumnIntPtr(): (bigint | null)[] {
    if (!this.requireBytes(this.count, 'nullable int64 column')) return []
    const result: (bigint | null)[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      if (!this.readCanonicalMarker('nullable')) { result[i] = null; continue }
      result[i] = this.readSvarint()
    }
    return result
  }

  /** Read count exact DateTime pairs. */
  readColumnDateTime(): string[] {
    if (!this.requireBytes(this.count * 2, 'datetime column')) return []
    const result: string[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      result[i] = this.readDateTime()
      if (this.error !== null) return []
    }
    return result
  }

  /** Read count nullable DateTime pairs. */
  readColumnDateTimePtr(): (string | null)[] {
    if (!this.requireBytes(this.count, 'nullable datetime column')) return []
    const result: (string | null)[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      if (!this.readCanonicalMarker('nullable')) { result[i] = null; continue }
      result[i] = this.readDateTime()
      if (this.error !== null) return []
    }
    return result
  }

  /** Read count nullable float values (0x00=null, 0x01+fixed64). */
  readColumnFloatPtr(): (number | null)[] {
    if (!this.requireBytes(this.count, 'nullable float column')) return []
    const result: (number | null)[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      if (!this.readCanonicalMarker('nullable')) { result[i] = null; continue }
      if (!this.requireBytes(8, 'float')) return []
      const value = this.view.getFloat64(this.wireOffset, true)
      if (!this.acceptFloat(value)) return []
      result[i] = value
      this.wireOffset += 8
    }
    return result
  }

  /** Read count nullable string values (0x00=null, 0x01+string). */
  readColumnStringPtr(): (string | null)[] {
    if (!this.requireBytes(this.count, 'nullable string column')) return []
    const result: (string | null)[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      if (!this.readCanonicalMarker('nullable')) { result[i] = null; continue }
      const len = this.readLength('string')
      if (len < 0) return []
      if (len === 0) { result[i] = ''; continue }
      result[i] = this.readStringBytes(len)
      if (this.error !== null) return []
    }
    return result
  }

  /** Read count nullable boolean values (0x00=null, 0x01+varint). */
  readColumnBoolPtr(): (boolean | null)[] {
    if (!this.requireBytes(this.count, 'nullable bool column')) return []
    const result: (boolean | null)[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      if (!this.readCanonicalMarker('nullable')) { result[i] = null; continue }
      result[i] = this.readCanonicalMarker('bool')
    }
    return result
  }

  /** Current read position (for reading pagination metadata after 0x00). */
  offset(): number {
    return this.wireOffset
  }

  /** Read one signed zigzag-encoded varint at current position. */
  readSvarint(): bigint {
    return this.readSigned()
  }

  /** Read unsigned LEB128 varint. */
  private readVarintRaw(): number {
    return this.readUnsigned()
  }

  private readCanonicalMarker(kind: 'bool' | 'nullable'): boolean {
    if (this.wireOffset >= this.wireBytes.length) {
      this.error ??= `truncated ${kind} marker at offset ${this.wireOffset}`
      return false
    }
    const offset = this.wireOffset
    const marker = this.wireBytes[this.wireOffset++]
    if (marker === 0x00) return false
    if (marker === 0x01) return true
    this.error ??= `invalid ${kind} marker 0x${marker.toString(16).padStart(2, '0')} at offset ${offset}`
    return false
  }
}

// --- FieldMask helpers ---

/** Set a field bit in the mask, growing the mask if necessary. Returns the (possibly new) mask. */
export function fieldMaskSet(mask: Uint8Array, fieldID: number): Uint8Array {
  if (!Number.isInteger(fieldID) || fieldID <= 0) return mask
  const bit = fieldID - 1
  const byteIdx = bit >>> 3
  const bitIdx = bit & 7
  if (byteIdx >= mask.length) {
    const next = new Uint8Array(byteIdx + 1)
    next.set(mask, 0)
    next[byteIdx] = 1 << bitIdx
    return next
  }
  mask[byteIdx] |= 1 << bitIdx
  return mask
}

/** Check if a field bit is set in the mask. */
export function fieldMaskHas(mask: Uint8Array, fieldID: number): boolean {
  if (!Number.isInteger(fieldID) || fieldID <= 0) return false
  const bit = fieldID - 1
  const byteIdx = bit >>> 3
  if (byteIdx >= mask.length) return false
  return (mask[byteIdx] & (1 << (bit & 7))) !== 0
}

// --- Scalar array helpers ---

/** Luxo wire field type names (matches Go schema.FieldType.String()). */
export type FieldType =
  | 'Int' | 'Float' | 'String' | 'Boolean' | 'DateTime'
  | 'Duration' | 'Bytes' | 'Enum' | 'Model' | 'UUID' | 'Decimal' | 'JSON'

/** Decode a scalar array cell ([count][items...]) into a JS array, by element type.
 *  Used both for row-mode list fields and for columnar list cells (each cell is a
 *  length-prefixed blob read via ColumnarDecoder.readColumnBytes). Duration values
 *  are returned as bigint nanoseconds; DateTime values are returned as RFC3339
 *  strings, matching the Go JSON output and the row/columnar scalar decoders. */
export function decodeScalarArray(cell: Uint8Array, type: FieldType): unknown[] {
  const dec = new Decoder(cell)
  const values = readScalarArray(dec, type)
  checkComplete(dec)
  return values
}

function checkComplete(dec: Decoder): void {
  if (dec.error) throw new Error(dec.error)
  if (dec.remaining !== 0) throw new Error('unexpected trailing response bytes')
}

function readScalarArray(dec: Decoder, type: FieldType): unknown[] {
  switch (type) {
    case 'DateTime':
      return dec.readDateTimeArray()
    case 'Int': case 'Duration':
      return dec.readIntArray()
    case 'Float':
      return dec.readFloatArray()
    case 'String': case 'Enum': case 'Decimal':
      return dec.readStringArray()
    case 'Boolean':
      return dec.readBoolArray()
    case 'UUID':
      return dec.readUUIDArray()
    case 'Bytes':
      return dec.readBytesArray()
    case 'JSON':
      return dec.readBytesArray().map(decodeJSONValue)
    default:
      throw new TypeError(`unsupported scalar array type ${type}`)
  }
}
