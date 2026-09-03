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

const textEncoder = new TextEncoder()
const textDecoder = new TextDecoder()

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
// On the wire a DateTime is svarint(unix seconds). In JS it is represented as
// an RFC3339 string (e.g. "2026-05-28T12:00:00Z"), matching the Go JSON output
// which formats time.Unix(sec, 0).UTC() with RFC3339Nano. Since wire values
// carry only whole seconds, the fractional ".000" produced by Date.toISOString()
// is stripped so binary and JSON modes yield byte-identical strings.

/** Convert unix seconds to an RFC3339 string, matching Go's DateTime JSON output. */
export function unixSecondsToISO(seconds: number): string {
  // Date expects milliseconds; whole-second timestamps never have a fractional part.
  return new Date(seconds * 1000).toISOString().replace('.000Z', 'Z')
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
  return JSON.parse(textDecoder.decode(value)) as unknown
}

/** Decode a single hex digit, or -1 if not a hex char. */
function hexNibble(c: number): number {
  if (c >= 0x30 && c <= 0x39) return c - 0x30 // 0-9
  if (c >= 0x61 && c <= 0x66) return c - 0x61 + 10 // a-f
  if (c >= 0x41 && c <= 0x46) return c - 0x41 + 10 // A-F
  return -1
}

/** Parse a canonical 36-char UUID string into 16 bytes. Returns null on malformed input. */
export function parseUUID(s: string): Uint8Array | null {
  if (s.length !== 36) return null
  const out = new Uint8Array(16)
  let j = 0
  let i = 0
  while (i < s.length) {
    const c = s.charCodeAt(i)
    if (c === 0x2d) { i++; continue } // '-'
    if (i + 1 >= s.length || j >= 16) return null
    const hi = hexNibble(c)
    const lo = hexNibble(s.charCodeAt(i + 1))
    if (hi < 0 || lo < 0) return null
    out[j++] = (hi << 4) | lo
    i += 2
  }
  return j === 16 ? out : null
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
    let newLen = this.buf.length
    const needed = this.pos + n
    while (newLen < needed) newLen <<= 1
    const next = new Uint8Array(newLen)
    next.set(this.buf, 0)
    this.buf = next
    this.view = new DataView(this.buf.buffer)
  }

  /** Write unsigned LEB128 varint */
  writeVarint(v: number): void {
    // v must be a non-negative integer
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

  /** Write signed zigzag-encoded varint (matches Go: (v<<1) ^ (v>>63)) */
  writeSvarint(v: number): void {
    // Zigzag: map signed → unsigned. Safe for |v| < 2^52.
    // JS number precision: 53 bits → zigzag doubles the magnitude → safe up to 2^52.
    // API params rarely exceed this. For snowflake IDs > 2^52, use string type instead.
    if (v >= 0) {
      this.writeVarint(v * 2)
    } else {
      this.writeVarint(-v * 2 - 1)
    }
  }

  /** Write float64 as 8 bytes little-endian */
  writeFixed64(v: number): void {
    this.grow(8)
    this.view.setFloat64(this.pos, v, true)
    this.pos += 8
  }

  /** Write length-prefixed UTF-8 string */
  writeString(v: string): void {
    const encoded = textEncoder.encode(v)
    this.writeVarint(encoded.length)
    this.grow(encoded.length)
    this.buf.set(encoded, this.pos)
    this.pos += encoded.length
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

  /** Write a fixed 16-byte UUID (no length prefix). Accepts a canonical string or raw bytes. */
  writeUUID(v: string | Uint8Array): void {
    const bytes = typeof v === 'string' ? parseUUID(v) : v
    if (!bytes || bytes.length !== 16) throw new Error(`invalid UUID: ${typeof v === 'string' ? v : '[bytes]'}`)
    this.grow(16)
    this.buf.set(bytes, this.pos)
    this.pos += 16
  }

  // --- Field writers (fieldID + value) ---

  writeFieldInt(fieldID: number, v: number): void {
    this.writeVarint(fieldID)
    this.writeSvarint(v)
  }

  writeFieldFloat(fieldID: number, v: number): void {
    this.writeVarint(fieldID)
    this.writeFixed64(v)
  }

  writeFieldString(fieldID: number, v: string): void {
    this.writeVarint(fieldID)
    this.writeString(v)
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

  writeFieldIntArray(fieldID: number, v: number[]): void {
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
    this.writeVarint(fieldID)
    this.writeVarint(v.length)
    for (const item of v) this.writeString(item)
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

// --- Decoder ---

export class Decoder {
  private data: Uint8Array
  private view: DataView
  private off: number
  fieldID: number
  error: string | null

  constructor(data: Uint8Array) {
    this.data = data
    // zero-copy: use the buffer directly
    this.view = new DataView(data.buffer, data.byteOffset, data.byteLength)
    this.off = 0
    this.fieldID = 0
    this.error = null
  }

  /** Skip the arena header (totalStringLen varint) that prefixes each model's binary data. */
  skipArenaHeader(): void {
    this.readVarintRaw()
  }

  /** Read next field ID. Returns false at end marker (0x00) or EOF. */
  nextField(): boolean {
    if (this.error !== null) return false
    if (this.off >= this.data.length) {
      this.fieldID = 0
      return false
    }
    const id = this.readVarintRaw()
    this.fieldID = id
    return id !== 0
  }

  /** Read unsigned LEB128 varint */
  private readVarintRaw(): number {
    const start = this.off
    let v = 0
    let mul = 1
    while (this.off < this.data.length) {
      const b = this.data[this.off++]
      v += (b & 0x7F) * mul
      if (b < 0x80) return v
      mul *= 128
      if (mul > 562949953421312) {
        this.error ??= `varint overflow at offset ${start}`
        return 0
      }
    }
    this.error ??= `truncated varint at offset ${start}`
    return 0
  }

  /** Read signed zigzag-encoded int */
  readInt(): number {
    const uv = this.readVarintRaw()
    // zigzag decode: (uv >>> 1) ^ -(uv & 1)
    // For large JS numbers, use arithmetic instead of bitwise
    const half = Math.floor(uv / 2)
    return (uv & 1) ? -(half + 1) : half
  }

  /** Read float64 from 8 bytes little-endian */
  readFloat(): number {
    if (this.off + 8 > this.data.length) {
      this.error ??= `truncated fixed64 at offset ${this.off}`
      return 0
    }
    const v = this.view.getFloat64(this.off, true)
    this.off += 8
    return v
  }

  /** Read length-prefixed UTF-8 string */
  readString(): string {
    const len = this.readVarintRaw()
    if (this.error !== null) return ''
    if (len === 0) return ''
    if (this.off + len > this.data.length) {
      this.error ??= `truncated string at offset ${this.off}`
      return ''
    }
    const bytes = this.data.subarray(this.off, this.off + len)
    this.off += len
    return textDecoder.decode(bytes)
  }

  /** Read length-prefixed bytes as a zero-copy view. */
  readBytes(): Uint8Array {
    const len = this.readVarintRaw()
    if (this.error !== null) return new Uint8Array(0)
    if (len === 0) return new Uint8Array(0)
    if (this.off + len > this.data.length) {
      this.error ??= `truncated bytes at offset ${this.off}`
      return new Uint8Array(0)
    }
    const bytes = this.data.subarray(this.off, this.off + len)
    this.off += len
    return bytes
  }

  /** Read boolean (varint 0 or 1) */
  readBool(): boolean {
    return this.readCanonicalMarker('bool')
  }

  /** Read a fixed 16-byte UUID, returning the canonical 36-char string. */
  readUUID(): string {
    if (this.off + 16 > this.data.length) {
      this.error ??= `truncated uuid at offset ${this.off}`
      return '00000000-0000-0000-0000-000000000000'
    }
    const s = formatUUID(this.data, this.off)
    this.off += 16
    return s
  }

  // --- Scalar array readers (row mode: [count][item0][item1]...) ---
  // count is a plain varint; each item is encoded per element type.

  readIntArray(): number[] {
    const count = this.readVarintRaw()
    const out: number[] = new Array(count)
    for (let i = 0; i < count; i++) out[i] = this.readInt()
    return out
  }

  readFloatArray(): number[] {
    const count = this.readVarintRaw()
    const out: number[] = new Array(count)
    for (let i = 0; i < count; i++) out[i] = this.readFloat()
    return out
  }

  readStringArray(): string[] {
    const count = this.readVarintRaw()
    const out: string[] = new Array(count)
    for (let i = 0; i < count; i++) out[i] = this.readString()
    return out
  }

  readBoolArray(): boolean[] {
    const count = this.readVarintRaw()
    const out: boolean[] = new Array(count)
    for (let i = 0; i < count; i++) out[i] = this.readBool()
    return out
  }

  readUUIDArray(): string[] {
    const count = this.readVarintRaw()
    const out: string[] = new Array(count)
    for (let i = 0; i < count; i++) out[i] = this.readUUID()
    return out
  }

  readBytesArray(): Uint8Array[] {
    const count = this.readVarintRaw()
    const out: Uint8Array[] = new Array(count)
    for (let i = 0; i < count; i++) out[i] = this.readBytes()
    return out
  }

  // --- Nullable readers (1-byte flag + value if present) ---

  readIntPtr(): number | null {
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

  /** Read a DateTime: svarint(unix seconds) → RFC3339 string (matches Go JSON output). */
  readDateTime(): string {
    return unixSecondsToISO(this.readInt())
  }

  /** Read a nullable DateTime (0x00=null, else svarint seconds → RFC3339 string). */
  readDateTimePtr(): string | null {
    if (!this.readCanonicalMarker('nullable')) return null
    return unixSecondsToISO(this.readInt())
  }

  /** Read a nested model using a decoder function. */
  readNullable<T>(decode: () => T): T | null {
    if (!this.readCanonicalMarker('nullable')) return null
    return decode()
  }

  private readCanonicalMarker(kind: 'bool' | 'nullable'): boolean {
    if (this.off >= this.data.length) {
      this.error ??= `truncated ${kind} marker at offset ${this.off}`
      return false
    }
    const offset = this.off
    const marker = this.data[this.off++]
    if (marker === 0x00) return false
    if (marker === 0x01) return true
    this.error ??= `invalid ${kind} marker 0x${marker.toString(16).padStart(2, '0')} at offset ${offset}`
    return false
  }

  /** Read an array of items using a decoder function. */
  readArray<T>(decode: () => T): T[] {
    const count = this.readVarintRaw()
    const items: T[] = []
    for (let i = 0; i < count; i++) {
      items.push(decode())
    }
    return items
  }

  /** Skip remaining bytes (useful when encountering unknown fields) */
  get remaining(): number {
    return this.data.length - this.off
  }
}

// --- Columnar Decoder ---
// Reads columnar-encoded list data:
// [count varint][arena size varint][fieldID][vals...]...[0x00]

export class ColumnarDecoder {
  private buf: Uint8Array
  private view: DataView
  private off: number
  readonly count: number
  readonly arenaSize: number
  fieldID: number
  error: string | null

  constructor(data: Uint8Array) {
    this.buf = data
    this.view = new DataView(data.buffer, data.byteOffset, data.byteLength)
    this.off = 0
    this.fieldID = 0
    this.error = null
    this.count = this.readVarintRaw()
    this.arenaSize = this.readVarintRaw()
  }

  /** Advance to next column. Returns false at end marker (0x00) or EOF. */
  nextColumn(): boolean {
    if (this.error !== null) return false
    if (this.off >= this.buf.length) return false
    const id = this.readVarintRaw()
    this.fieldID = id
    if (id === 0) return false
    return true
  }

  /** Read count svarint values (zigzag-encoded int column). */
  readColumnInt(): number[] {
    const result: number[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      result[i] = this.readSvarint()
    }
    return result
  }

  /** Read count fixed64 float values. */
  readColumnFloat(): number[] {
    const result: number[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      result[i] = this.view.getFloat64(this.off, true)
      this.off += 8
    }
    return result
  }

  /** Read count length-prefixed string values. */
  readColumnString(): string[] {
    const result: string[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      const len = this.readVarintRaw()
      if (len === 0) { result[i] = ''; continue }
      result[i] = textDecoder.decode(this.buf.subarray(this.off, this.off + len))
      this.off += len
    }
    return result
  }

  /** Read count boolean values (varint 0/1). */
  readColumnBool(): boolean[] {
    const result: boolean[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      result[i] = this.readCanonicalMarker('bool')
    }
    return result
  }

  /** Read count fixed 16-byte UUID values (canonical strings). */
  readColumnUUID(): string[] {
    const result: string[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      result[i] = formatUUID(this.buf, this.off)
      this.off += 16
    }
    return result
  }

  /** Read count length-prefixed byte blobs. Each cell is a length-prefixed blob —
   *  used for scalar array columns ([T]), where each cell holds [count][items...]. */
  readColumnBytes(): Uint8Array[] {
    const result: Uint8Array[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      const len = this.readVarintRaw()
      result[i] = this.buf.subarray(this.off, this.off + len)
      this.off += len
    }
    return result
  }

  /** Read count nullable byte blobs (0x00=null, 0x01+length+bytes). */
  readColumnBytesPtr(): (Uint8Array | null)[] {
    const result: (Uint8Array | null)[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      if (!this.readCanonicalMarker('nullable')) { result[i] = null; continue }
      const len = this.readVarintRaw()
      result[i] = this.buf.subarray(this.off, this.off + len)
      this.off += len
    }
    return result
  }

  /** Read count nullable UUID values (0x00=null, 0x01+16 bytes). */
  readColumnUUIDPtr(): (string | null)[] {
    const result: (string | null)[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      if (!this.readCanonicalMarker('nullable')) { result[i] = null; continue }
      result[i] = formatUUID(this.buf, this.off)
      this.off += 16
    }
    return result
  }

  /** Read count nullable int values (0x00=null, 0x01+svarint). */
  readColumnIntPtr(): (number | null)[] {
    const result: (number | null)[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      if (!this.readCanonicalMarker('nullable')) { result[i] = null; continue }
      result[i] = this.readSvarint()
    }
    return result
  }

  /** Read count DateTime values (svarint seconds → RFC3339 strings). */
  readColumnDateTime(): string[] {
    const result: string[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      result[i] = unixSecondsToISO(this.readSvarint())
    }
    return result
  }

  /** Read count nullable DateTime values (0x00=null, 0x01+svarint → RFC3339 string). */
  readColumnDateTimePtr(): (string | null)[] {
    const result: (string | null)[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      if (!this.readCanonicalMarker('nullable')) { result[i] = null; continue }
      result[i] = unixSecondsToISO(this.readSvarint())
    }
    return result
  }

  /** Read count nullable float values (0x00=null, 0x01+fixed64). */
  readColumnFloatPtr(): (number | null)[] {
    const result: (number | null)[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      if (!this.readCanonicalMarker('nullable')) { result[i] = null; continue }
      result[i] = this.view.getFloat64(this.off, true)
      this.off += 8
    }
    return result
  }

  /** Read count nullable string values (0x00=null, 0x01+string). */
  readColumnStringPtr(): (string | null)[] {
    const result: (string | null)[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      if (!this.readCanonicalMarker('nullable')) { result[i] = null; continue }
      const len = this.readVarintRaw()
      if (len === 0) { result[i] = ''; continue }
      result[i] = textDecoder.decode(this.buf.subarray(this.off, this.off + len))
      this.off += len
    }
    return result
  }

  /** Read count nullable boolean values (0x00=null, 0x01+varint). */
  readColumnBoolPtr(): (boolean | null)[] {
    const result: (boolean | null)[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      if (!this.readCanonicalMarker('nullable')) { result[i] = null; continue }
      result[i] = this.readCanonicalMarker('bool')
    }
    return result
  }

  /** Current read position (for reading pagination metadata after 0x00). */
  offset(): number {
    return this.off
  }

  /** Read one signed zigzag-encoded varint at current position. */
  readSvarint(): number {
    const uv = this.readVarintRaw()
    const half = Math.floor(uv / 2)
    return (uv & 1) ? -(half + 1) : half
  }

  /** Read unsigned LEB128 varint. */
  private readVarintRaw(): number {
    let v = 0
    let mul = 1 // multiplier: 1, 128, 16384, ... — avoids 2**shift per iteration
    while (this.off < this.buf.length) {
      const b = this.buf[this.off++]
      v += (b & 0x7F) * mul
      if (b < 0x80) return v
      mul *= 128
      if (mul > 562949953421312) return 0 // 2^49 overflow guard
    }
    return 0
  }

  private readCanonicalMarker(kind: 'bool' | 'nullable'): boolean {
    if (this.off >= this.buf.length) {
      this.error ??= `truncated ${kind} marker at offset ${this.off}`
      return false
    }
    const offset = this.off
    const marker = this.buf[this.off++]
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
 *  are returned as raw numbers (nanoseconds); DateTime values are returned as RFC3339
 *  strings, matching the Go JSON output and the row/columnar scalar decoders. */
export function decodeScalarArray(cell: Uint8Array, type: FieldType): unknown[] {
  const dec = new Decoder(cell)
  switch (type) {
    case 'DateTime':
      return dec.readIntArray().map(unixSecondsToISO)
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
      return []
  }
}
