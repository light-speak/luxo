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
    // zigzag: map signed to unsigned
    // For JS numbers (53-bit safe): (v * 2) ^ (v < 0 ? -1 : 0)
    // This correctly handles the full 53-bit signed range
    const zz = v < 0 ? (-v * 2 - 1) : (v * 2)
    this.writeVarint(zz)
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

  /** Return a trimmed view of the encoded data (no copy if possible) */
  bytes(): Uint8Array {
    return this.buf.subarray(0, this.pos)
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
    let shift = 0
    while (this.off < this.data.length) {
      const b = this.data[this.off++]
      v += (b & 0x7F) * (2 ** shift) // use multiply to avoid 32-bit limit of <<
      if (b < 0x80) return v
      shift += 7
      if (shift >= 56) return 0 // overflow guard (beyond 53-bit safe range)
    }
    return 0 // truncated
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

  /** Skip remaining bytes (useful when encountering unknown fields) */
  get remaining(): number {
    return this.data.length - this.off
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
