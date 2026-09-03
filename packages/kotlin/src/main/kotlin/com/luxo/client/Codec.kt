package com.luxo.client

/**
 * Luxo binary wire-format codec.
 *
 * Wire format:
 *   Varint   – unsigned LEB128 (7 bits/byte, high bit = continuation)
 *   Svarint  – zigzag then varint
 *   Fixed64  – 8 bytes little-endian IEEE 754
 *   String   – varint length + UTF-8 bytes
 *   Bool     – varint 0 or 1
 *   Nullable – 1 byte flag (0x00=null, 0x01=present) + value if present
 *   Field    – varint fieldID + value
 *   End      – 0x00 (fieldID 0)
 */

class LuxoEncoder(initialCapacity: Int = 1024) {

    private var buf: ByteArray = ByteArray(initialCapacity)
    private var pos: Int = 0

    // -- low-level writers ------------------------------------------------

    /** Encode unsigned LEB128.  [v] is treated as unsigned 64-bit. */
    fun writeVarint(v: Long) {
        var uv = v
        while (uv and 0x7FL.inv() != 0L) {
            ensure(1)
            buf[pos++] = ((uv and 0x7F) or 0x80).toByte()
            uv = uv ushr 7
        }
        ensure(1)
        buf[pos++] = (uv and 0x7F).toByte()
    }

    /** Zigzag-encode a signed 64-bit value, then write as varint. */
    fun writeSvarint(v: Long) {
        writeVarint((v shl 1) xor (v shr 63))
    }

    /** Write IEEE 754 float64 in little-endian byte order. */
    fun writeFixed64(v: Double) {
        ensure(8)
        val bits = java.lang.Double.doubleToRawLongBits(v)
        buf[pos++] = (bits).toByte()
        buf[pos++] = (bits ushr 8).toByte()
        buf[pos++] = (bits ushr 16).toByte()
        buf[pos++] = (bits ushr 24).toByte()
        buf[pos++] = (bits ushr 32).toByte()
        buf[pos++] = (bits ushr 40).toByte()
        buf[pos++] = (bits ushr 48).toByte()
        buf[pos++] = (bits ushr 56).toByte()
    }

    /** Write length-prefixed UTF-8 string. */
    fun writeString(v: String) {
        val bytes = v.toByteArray(Charsets.UTF_8)
        writeBytes(bytes)
    }

    /** Write length-prefixed arbitrary bytes. */
    fun writeBytes(v: ByteArray) {
        writeVarint(v.size.toLong())
        writeRawBytes(v)
    }

    /** Write raw bytes without a length prefix. */
    fun writeRawBytes(v: ByteArray) {
        ensure(v.size)
        System.arraycopy(v, 0, buf, pos, v.size)
        pos += v.size
    }

    /** Write boolean as varint 0/1. */
    fun writeBool(v: Boolean) {
        writeVarint(if (v) 1L else 0L)
    }

    /** Write a canonical UUID string as a fixed 16-byte value (no length prefix). */
    fun writeUuid(v: String) {
        val raw = UuidCodec.parse(v)
        ensure(16)
        System.arraycopy(raw, 0, buf, pos, 16)
        pos += 16
    }

    // -- field writers ----------------------------------------------------

    fun writeFieldInt(fieldID: Int, v: Long) {
        writeVarint(fieldID.toLong())
        writeSvarint(v)
    }

    fun writeFieldFloat(fieldID: Int, v: Double) {
        writeVarint(fieldID.toLong())
        writeFixed64(v)
    }

    fun writeFieldString(fieldID: Int, v: String) {
        writeVarint(fieldID.toLong())
        writeString(v)
    }

    fun writeFieldBool(fieldID: Int, v: Boolean) {
        writeVarint(fieldID.toLong())
        writeBool(v)
    }

    fun writeFieldUuid(fieldID: Int, v: String) {
        writeVarint(fieldID.toLong())
        writeUuid(v)
    }

    fun writeFieldBytes(fieldID: Int, v: ByteArray) {
        writeVarint(fieldID.toLong())
        writeBytes(v)
    }

    /**
     * Write a scalar-array field: [fieldID][count][item0][item1]...
     * [encode] writes one element per item using the element type's encoding.
     */
    fun <T> writeFieldArray(fieldID: Int, items: List<T>, encode: (T) -> Unit) {
        writeVarint(fieldID.toLong())
        writeVarint(items.size.toLong())
        for (item in items) encode(item)
    }

    /** Write end-of-struct marker (fieldID 0). */
    fun writeEnd() {
        ensure(1)
        buf[pos++] = 0x00
    }

    /** Return the encoded bytes (trimmed copy). */
    fun bytes(): ByteArray = buf.copyOf(pos)

    // -- internal ---------------------------------------------------------

    private fun ensure(n: Int) {
        if (pos + n <= buf.size) return
        var newCap = buf.size
        while (newCap < pos + n) newCap = newCap shl 1
        buf = buf.copyOf(newCap)
    }
}

class LuxoDecoder(private val buf: ByteArray) {

    private var off: Int = 0

    /** The field ID read by the most recent [nextField] call. */
    var fieldID: Int = 0
        private set

    // -- field iteration --------------------------------------------------

    /** Skip the arena header (totalStringLen varint) that prefixes each model's binary data. */
    fun skipArenaHeader() {
        readVarintRaw()
    }

    /**
     * Read the next field ID.  Returns false when end marker (0x00) is
     * reached or the buffer is exhausted.
     */
    fun nextField(): Boolean {
        if (off >= buf.size) {
            fieldID = 0
            return false
        }
        val id = readVarintRaw()
        fieldID = id.toInt()
        return fieldID != 0
    }

    // -- value readers ----------------------------------------------------

    /** Read a zigzag-encoded signed 64-bit integer. */
    fun readInt(): Long {
        val uv = readVarintRaw()
        return (uv ushr 1) xor -(uv and 1L)
    }

    /** Read an 8-byte little-endian IEEE 754 float64. */
    fun readFloat(): Double {
        checkRemaining(8)
        var bits: Long = 0
        bits = bits or ((buf[off++].toLong() and 0xFF))
        bits = bits or ((buf[off++].toLong() and 0xFF) shl 8)
        bits = bits or ((buf[off++].toLong() and 0xFF) shl 16)
        bits = bits or ((buf[off++].toLong() and 0xFF) shl 24)
        bits = bits or ((buf[off++].toLong() and 0xFF) shl 32)
        bits = bits or ((buf[off++].toLong() and 0xFF) shl 40)
        bits = bits or ((buf[off++].toLong() and 0xFF) shl 48)
        bits = bits or ((buf[off++].toLong() and 0xFF) shl 56)
        return java.lang.Double.longBitsToDouble(bits)
    }

    /** Read a length-prefixed UTF-8 string. */
    fun readString(): String {
        return readBytes().toString(Charsets.UTF_8)
    }

    /** Read length-prefixed arbitrary bytes. */
    fun readBytes(): ByteArray {
        val len = readVarintRaw().toInt()
        checkRemaining(len)
        val result = buf.copyOfRange(off, off + len)
        off += len
        return result
    }

    /** Read an unsigned LEB128 value from the current offset. */
    fun readVarint(): Long = readVarintRaw()

    /** Current read position. */
    fun offset(): Int = off

    /** Number of unread bytes. */
    fun remaining(): Int = buf.size - off

    /** Return all unread bytes. */
    fun readRemainingBytes(): ByteArray {
        val result = buf.copyOfRange(off, buf.size)
        off = buf.size
        return result
    }

    /** Read a canonical boolean marker (0x00=false, 0x01=true). */
    fun readBool(): Boolean = readCanonicalMarker("bool")

    /** Read a fixed 16-byte UUID and format it as a canonical lowercase string. */
    fun readUuid(): String {
        checkRemaining(16)
        val s = UuidCodec.format(buf, off)
        off += 16
        return s
    }

    /**
     * Read a DateTime: svarint(unix seconds) on the wire, returned as an
     * ISO-8601 string to match the JSON-mode representation (Kotlin `String`).
     */
    fun readDateTime(): String = DateTimeCodec.toIso(readInt())

    // -- nullable readers -------------------------------------------------

    fun readIntPtr(): Long? {
        if (!readNullFlag()) return null
        return readInt()
    }

    fun readFloatPtr(): Double? {
        if (!readNullFlag()) return null
        return readFloat()
    }

    fun readStringPtr(): String? {
        if (!readNullFlag()) return null
        return readString()
    }

    fun readBoolPtr(): Boolean? {
        if (!readNullFlag()) return null
        return readBool()
    }

    fun readUuidPtr(): String? {
        if (!readNullFlag()) return null
        return readUuid()
    }

    /** Read a nullable DateTime (0x00=null, else svarint seconds -> ISO string). */
    fun readDateTimePtr(): String? {
        if (!readNullFlag()) return null
        return readDateTime()
    }

    /** Read a nullable nested model using a decoder lambda. */
    fun <T> readNullable(decode: () -> T): T? {
        if (!readNullFlag()) return null
        return decode()
    }

    /** Read an array of items using a decoder lambda. */
    fun <T> readArray(decode: () -> T): List<T> {
        val count = readVarintRaw().toInt()
        return List(count) { decode() }
    }

    // -- internal ---------------------------------------------------------

    /** Read unsigned LEB128 varint. */
    private fun readVarintRaw(): Long {
        var result: Long = 0
        var shift = 0
        while (true) {
            checkRemaining(1)
            val b = buf[off++].toLong() and 0xFF
            result = result or ((b and 0x7F) shl shift)
            if (b and 0x80 == 0L) break
            shift += 7
            if (shift >= 64) throw LuxoCodecException("varint overflow")
        }
        return result
    }

    /** Read nullable flag byte. Returns true if value is present. */
    private fun readNullFlag(): Boolean = readCanonicalMarker("nullable")

    private fun readCanonicalMarker(kind: String): Boolean {
        checkRemaining(1)
        val markerOffset = off
        return when (buf[off++].toInt() and 0xFF) {
            0x00 -> false
            0x01 -> true
            else -> throw LuxoCodecException("invalid $kind marker at offset $markerOffset")
        }
    }

    private fun checkRemaining(n: Int) {
        if (off + n > buf.size) {
            throw LuxoCodecException("unexpected end of buffer at offset $off, need $n bytes")
        }
    }
}

/** Field-mask helpers for sparse field selection. */
object FieldMask {

    /**
     * Set the bit for [fieldID] in [mask], growing the array if needed.
     * Returns the (possibly new) mask array.
     */
    fun set(mask: ByteArray, fieldID: Int): ByteArray {
        if (fieldID <= 0) return mask
        val bit = fieldID - 1
        val byteIdx = bit ushr 3
        val m = if (byteIdx >= mask.size) mask.copyOf(byteIdx + 1) else mask
        m[byteIdx] = (m[byteIdx].toInt() or (1 shl (bit and 7))).toByte()
        return m
    }

    /** Check whether [fieldID] is present in [mask]. */
    fun has(mask: ByteArray, fieldID: Int): Boolean {
        if (fieldID <= 0) return false
        val bit = fieldID - 1
        val byteIdx = bit ushr 3
        if (byteIdx >= mask.size) return false
        return (mask[byteIdx].toInt() and (1 shl (bit and 7))) != 0
    }
}

/**
 * Columnar binary decoder for Luxo list responses.
 *
 * Columnar format:
 *   [count varint]
 *   [arena size varint]
 *   [fieldID varint][val0][val1]...[valN]  // column 1
 *   [fieldID varint][val0][val1]...[valN]  // column 2
 *   ...
 *   [0x00]  // end marker
 */
class ColumnarDecoder(private val buf: ByteArray) {

    private var off: Int = 0

    /** Number of rows in this columnar batch. */
    val count: Int

    /** Total UTF-8 string bytes advertised for arena allocation. */
    val arenaSize: Int

    /** The current column's field ID after calling [nextColumn]. */
    var fieldID: Int = 0
        private set

    init {
        count = readVarintRaw().toInt()
        arenaSize = readVarintRaw().toInt()
    }

    /** Advance to next column. Returns false at end marker (0x00) or EOF. */
    fun nextColumn(): Boolean {
        if (off >= buf.size) return false
        val id = readVarintRaw().toInt()
        fieldID = id
        return id != 0
    }

    /** Read [count] zigzag-encoded signed int64 values. */
    fun readColumnInt(): List<Long> {
        val result = LongArray(count)
        for (i in 0 until count) {
            val uv = readVarintRaw()
            result[i] = (uv ushr 1) xor -(uv and 1L)
        }
        return result.asList()
    }

    /**
     * Read [count] DateTime values from an Int column (svarint unix seconds),
     * converting each to its ISO-8601 string representation.
     */
    fun readColumnDateTime(): List<String> {
        val result = Array(count) { "" }
        for (i in 0 until count) {
            val uv = readVarintRaw()
            val sec = (uv ushr 1) xor -(uv and 1L)
            result[i] = DateTimeCodec.toIso(sec)
        }
        return result.asList()
    }

    /** Read [count] nullable DateTime values (0x00=null, else svarint seconds). */
    fun readColumnDateTimePtr(): List<String?> {
        val result = arrayOfNulls<String>(count)
        for (i in 0 until count) {
            if (!readCanonicalMarker("nullable")) continue
            val uv = readVarintRaw()
            val sec = (uv ushr 1) xor -(uv and 1L)
            result[i] = DateTimeCodec.toIso(sec)
        }
        return result.asList()
    }

    /** Read [count] fixed64 (8-byte LE) float values. */
    fun readColumnFloat(): List<Double> {
        val result = DoubleArray(count)
        for (i in 0 until count) {
            checkRemaining(8)
            var bits: Long = 0
            bits = bits or ((buf[off++].toLong() and 0xFF))
            bits = bits or ((buf[off++].toLong() and 0xFF) shl 8)
            bits = bits or ((buf[off++].toLong() and 0xFF) shl 16)
            bits = bits or ((buf[off++].toLong() and 0xFF) shl 24)
            bits = bits or ((buf[off++].toLong() and 0xFF) shl 32)
            bits = bits or ((buf[off++].toLong() and 0xFF) shl 40)
            bits = bits or ((buf[off++].toLong() and 0xFF) shl 48)
            bits = bits or ((buf[off++].toLong() and 0xFF) shl 56)
            result[i] = java.lang.Double.longBitsToDouble(bits)
        }
        return result.asList()
    }

    /** Read [count] length-prefixed UTF-8 string values. */
    fun readColumnString(): List<String> {
        val result = Array(count) { "" }
        for (i in 0 until count) {
            val len = readVarintRaw().toInt()
            if (len == 0) { result[i] = ""; continue }
            checkRemaining(len)
            result[i] = String(buf, off, len, Charsets.UTF_8)
            off += len
        }
        return result.asList()
    }

    /** Read [count] boolean values (varint 0/1). */
    fun readColumnBool(): List<Boolean> {
        val result = BooleanArray(count)
        for (i in 0 until count) {
            result[i] = readCanonicalMarker("bool")
        }
        return result.asList()
    }

    /** Read [count] nullable int values (0x00=null, 0x01+svarint). */
    fun readColumnIntPtr(): List<Long?> {
        val result = arrayOfNulls<Long>(count)
        for (i in 0 until count) {
            if (!readCanonicalMarker("nullable")) continue
            val uv = readVarintRaw()
            result[i] = (uv ushr 1) xor -(uv and 1L)
        }
        return result.asList()
    }

    /** Read [count] nullable float values (0x00=null, 0x01+fixed64). */
    fun readColumnFloatPtr(): List<Double?> {
        val result = arrayOfNulls<Double>(count)
        for (i in 0 until count) {
            if (!readCanonicalMarker("nullable")) continue
            checkRemaining(8)
            var bits: Long = 0
            bits = bits or ((buf[off++].toLong() and 0xFF))
            bits = bits or ((buf[off++].toLong() and 0xFF) shl 8)
            bits = bits or ((buf[off++].toLong() and 0xFF) shl 16)
            bits = bits or ((buf[off++].toLong() and 0xFF) shl 24)
            bits = bits or ((buf[off++].toLong() and 0xFF) shl 32)
            bits = bits or ((buf[off++].toLong() and 0xFF) shl 40)
            bits = bits or ((buf[off++].toLong() and 0xFF) shl 48)
            bits = bits or ((buf[off++].toLong() and 0xFF) shl 56)
            result[i] = java.lang.Double.longBitsToDouble(bits)
        }
        return result.asList()
    }

    /** Read [count] nullable string values (0x00=null, 0x01+string). */
    fun readColumnStringPtr(): List<String?> {
        val result = arrayOfNulls<String>(count)
        for (i in 0 until count) {
            if (!readCanonicalMarker("nullable")) continue
            val len = readVarintRaw().toInt()
            if (len == 0) { result[i] = ""; continue }
            checkRemaining(len)
            result[i] = String(buf, off, len, Charsets.UTF_8)
            off += len
        }
        return result.asList()
    }

    /** Read [count] nullable boolean values (0x00=null, 0x01+varint). */
    fun readColumnBoolPtr(): List<Boolean?> {
        val result = arrayOfNulls<Boolean>(count)
        for (i in 0 until count) {
            if (!readCanonicalMarker("nullable")) continue
            result[i] = readCanonicalMarker("bool")
        }
        return result.asList()
    }

    /** Read [count] fixed 16-byte UUID values as canonical lowercase strings. */
    fun readColumnUuid(): List<String> {
        val result = Array(count) { "" }
        for (i in 0 until count) {
            checkRemaining(16)
            result[i] = UuidCodec.format(buf, off)
            off += 16
        }
        return result.asList()
    }

    /** Read [count] nullable UUID values (0x00=null, 0x01+16 bytes). */
    fun readColumnUuidPtr(): List<String?> {
        val result = arrayOfNulls<String>(count)
        for (i in 0 until count) {
            if (!readCanonicalMarker("nullable")) continue
            checkRemaining(16)
            result[i] = UuidCodec.format(buf, off)
            off += 16
        }
        return result.asList()
    }

    /**
     * Read [count] length-prefixed byte blobs. Each cell holds an inline
     * scalar-array encoding ([count][items...]) or a nested-model blob. Used
     * for list columns (scalar arrays and relation lists) in columnar form.
     */
    fun readColumnBytes(): List<ByteArray> {
        val result = Array(count) { ByteArray(0) }
        for (i in 0 until count) {
            val len = readVarintRaw().toInt()
            checkRemaining(len)
            result[i] = buf.copyOfRange(off, off + len)
            off += len
        }
        return result.asList()
    }

    /** Read [count] nullable byte blobs (0x00=null, 0x01+length+bytes). */
    fun readColumnBytesPtr(): List<ByteArray?> {
        val result = arrayOfNulls<ByteArray>(count)
        for (i in 0 until count) {
            if (!readCanonicalMarker("nullable")) continue
            val len = readVarintRaw().toInt()
            checkRemaining(len)
            result[i] = buf.copyOfRange(off, off + len)
            off += len
        }
        return result.asList()
    }

    /** Current read position (for reading pagination metadata after 0x00). */
    fun offset(): Int = off

    /** Read one signed zigzag-encoded varint at current position. */
    fun readSvarint(): Long {
        val uv = readVarintRaw()
        return (uv ushr 1) xor -(uv and 1L)
    }

    // -- internal ---------------------------------------------------------

    private fun readVarintRaw(): Long {
        var result: Long = 0
        var shift = 0
        while (true) {
            checkRemaining(1)
            val b = buf[off++].toLong() and 0xFF
            result = result or ((b and 0x7F) shl shift)
            if (b and 0x80 == 0L) break
            shift += 7
            if (shift >= 64) throw LuxoCodecException("varint overflow")
        }
        return result
    }

    private fun readCanonicalMarker(kind: String): Boolean {
        checkRemaining(1)
        val markerOffset = off
        return when (buf[off++].toInt() and 0xFF) {
            0x00 -> false
            0x01 -> true
            else -> throw LuxoCodecException("invalid $kind marker at offset $markerOffset")
        }
    }

    private fun checkRemaining(n: Int) {
        if (off + n > buf.size) {
            throw LuxoCodecException("unexpected end of buffer at offset $off, need $n bytes")
        }
    }
}

/**
 * UUID wire-format helpers.
 *
 * On the Luxo binary wire a UUID is a fixed 16-byte value with no length
 * prefix. JSON / API surfaces use the canonical 36-char lowercase string
 * "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx".
 */
object UuidCodec {

    private val HEX = "0123456789abcdef".toCharArray()

    /** Format 16 bytes of [buf] starting at [off] as a canonical lowercase UUID. */
    fun format(buf: ByteArray, off: Int): String {
        val out = CharArray(36)
        var j = 0
        for (i in 0 until 16) {
            if (i == 4 || i == 6 || i == 8 || i == 10) {
                out[j++] = '-'
            }
            val b = buf[off + i].toInt() and 0xFF
            out[j++] = HEX[b ushr 4]
            out[j++] = HEX[b and 0x0F]
        }
        return String(out)
    }

    /**
     * Parse a canonical 36-char UUID string into 16 bytes.
     * Throws [LuxoCodecException] if the string is not a valid UUID.
     */
    fun parse(s: String): ByteArray {
        if (s.length != 36) throw LuxoCodecException("invalid UUID length: ${s.length}")
        val out = ByteArray(16)
        var j = 0
        var i = 0
        while (i < s.length) {
            val c = s[i]
            if (c == '-') { i++; continue }
            if (i + 1 >= s.length || j >= 16) throw LuxoCodecException("invalid UUID: $s")
            val hi = nibble(s[i])
            val lo = nibble(s[i + 1])
            out[j++] = ((hi shl 4) or lo).toByte()
            i += 2
        }
        if (j != 16) throw LuxoCodecException("invalid UUID: $s")
        return out
    }

    private fun nibble(c: Char): Int = when (c) {
        in '0'..'9' -> c - '0'
        in 'a'..'f' -> c - 'a' + 10
        in 'A'..'F' -> c - 'A' + 10
        else -> throw LuxoCodecException("invalid hex digit: $c")
    }
}

/**
 * DateTime wire-format helpers.
 *
 * On the Luxo binary wire a DateTime is svarint(unix seconds). JSON / API
 * surfaces use an ISO-8601 string. Both SDK modes expose DateTime as a Kotlin
 * `String`, so binary decoding converts seconds to the canonical ISO form.
 */
object DateTimeCodec {

    /** Convert Unix seconds to an ISO-8601 (UTC) string, e.g. "2026-05-28T10:00:00Z". */
    fun toIso(seconds: Long): String = java.time.Instant.ofEpochSecond(seconds).toString()
}

class LuxoCodecException(message: String) : RuntimeException(message)
