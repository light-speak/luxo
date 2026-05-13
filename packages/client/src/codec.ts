// Luxo binary wire format — TypeScript implementation.
// Matches Go pkg/lux/codec/wire.go exactly.
//
// Wire format per field: [fieldID varint] [value]
// Types are known at compile time from schema — no type tags on wire.
//
// Encoding rules:
//   - Int/Boolean/Enum: varint (LEB128)
//   - Float: fixed 8 bytes (little-endian float64)
//   - String/Bytes: length-prefixed (varint length + raw bytes)
//   - Nullable: 1-byte flag (0x00=null, 0x01=present) + value if present
//   - Message end: 0x00 terminator (fieldID 0 is reserved)

const textEncoder = new TextEncoder()
const textDecoder = new TextDecoder()

const INITIAL_BUF_SIZE = 1024

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

  constructor(data: Uint8Array) {
    this.data = data
    // zero-copy: use the buffer directly
    this.view = new DataView(data.buffer, data.byteOffset, data.byteLength)
    this.off = 0
    this.fieldID = 0
  }

  /** Read next field ID. Returns false at end marker (0x00) or EOF. */
  nextField(): boolean {
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
    let v = 0
    let mul = 1
    while (this.off < this.data.length) {
      const b = this.data[this.off++]
      v += (b & 0x7F) * mul
      if (b < 0x80) return v
      mul *= 128
      if (mul > 562949953421312) return 0 // 2^49 overflow guard
    }
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
    if (this.off + 8 > this.data.length) return 0
    const v = this.view.getFloat64(this.off, true)
    this.off += 8
    return v
  }

  /** Read length-prefixed UTF-8 string */
  readString(): string {
    const len = this.readVarintRaw()
    if (len === 0) return ''
    if (this.off + len > this.data.length) return ''
    const bytes = this.data.subarray(this.off, this.off + len)
    this.off += len
    return textDecoder.decode(bytes)
  }

  /** Read boolean (varint 0 or 1) */
  readBool(): boolean {
    return this.readVarintRaw() !== 0
  }

  // --- Nullable readers (1-byte flag + value if present) ---

  readIntPtr(): number | null {
    if (this.off >= this.data.length) return null
    if (this.data[this.off++] === 0x00) return null
    return this.readInt()
  }

  readFloatPtr(): number | null {
    if (this.off >= this.data.length) return null
    if (this.data[this.off++] === 0x00) return null
    return this.readFloat()
  }

  readStringPtr(): string | null {
    if (this.off >= this.data.length) return null
    if (this.data[this.off++] === 0x00) return null
    return this.readString()
  }

  readBoolPtr(): boolean | null {
    if (this.off >= this.data.length) return null
    if (this.data[this.off++] === 0x00) return null
    return this.readBool()
  }

  /** Read a nested model using a decoder function. */
  readNullable<T>(decode: () => T): T | null {
    const present = this.readVarint()
    if (present === 0n) return null
    return decode()
  }

  /** Read an array of items using a decoder function. */
  readArray<T>(decode: () => T): T[] {
    const count = Number(this.readVarint())
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
// Reads columnar-encoded list data: [count varint][fieldID][vals...][fieldID][vals...]...[0x00]

export class ColumnarDecoder {
  private buf: Uint8Array
  private view: DataView
  private off: number
  readonly count: number
  fieldID: number

  constructor(data: Uint8Array) {
    this.buf = data
    this.view = new DataView(data.buffer, data.byteOffset, data.byteLength)
    this.off = 0
    this.fieldID = 0
    this.count = this.readVarintRaw()
  }

  /** Advance to next column. Returns false at end marker (0x00) or EOF. */
  nextColumn(): boolean {
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
      result[i] = this.readVarintRaw() !== 0
    }
    return result
  }

  /** Read count nullable int values (0x00=null, 0x01+svarint). */
  readColumnIntPtr(): (number | null)[] {
    const result: (number | null)[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      if (this.buf[this.off++] === 0x00) { result[i] = null; continue }
      result[i] = this.readSvarint()
    }
    return result
  }

  /** Read count nullable float values (0x00=null, 0x01+fixed64). */
  readColumnFloatPtr(): (number | null)[] {
    const result: (number | null)[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      if (this.buf[this.off++] === 0x00) { result[i] = null; continue }
      result[i] = this.view.getFloat64(this.off, true)
      this.off += 8
    }
    return result
  }

  /** Read count nullable string values (0x00=null, 0x01+string). */
  readColumnStringPtr(): (string | null)[] {
    const result: (string | null)[] = new Array(this.count)
    for (let i = 0; i < this.count; i++) {
      if (this.buf[this.off++] === 0x00) { result[i] = null; continue }
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
      if (this.buf[this.off++] === 0x00) { result[i] = null; continue }
      result[i] = this.readVarintRaw() !== 0
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
}

// --- FieldMask helpers ---

/** Set a field bit in the mask, growing the mask if necessary. Returns the (possibly new) mask. */
export function fieldMaskSet(mask: Uint8Array, fieldID: number): Uint8Array {
  const byteIdx = fieldID >>> 3
  const bitIdx = fieldID & 7
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
  const byteIdx = fieldID >>> 3
  if (byteIdx >= mask.length) return false
  return (mask[byteIdx] & (1 << (fieldID & 7))) !== 0
}
