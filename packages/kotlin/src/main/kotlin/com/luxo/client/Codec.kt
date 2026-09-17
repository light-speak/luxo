package com.luxo.client

private fun finiteFloatFromBits(bits: Long): Double {
    if (bits and 0x7ff0000000000000L == 0x7ff0000000000000L) {
        throw LuxoCodecException("Float must be finite")
    }
    return Double.fromBits(bits)
}

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

    /** Write exact seconds and nanoseconds without a millisecond conversion. */
    fun writeDateTime(value: String) {
        val instant = DateTimeCodec.parse(value)
        writeSvarint(instant.epochSecond)
        writeVarint(instant.nano.toLong())
    }

    /** Write IEEE 754 float64 in little-endian byte order. */
    fun writeFixed64(v: Double) {
        if (!v.isFinite()) throw LuxoCodecException("Float must be finite")
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
        val bytes = requireLuxoUnicode(v).toByteArray(Charsets.UTF_8)
        writeBytes(bytes)
    }

    /** Write length-prefixed arbitrary bytes. */
    fun writeBytes(v: ByteArray) {
        writeVarint(v.size.toLong())
        writeRawBytes(v)
    }

	/** Write a length-delimited value directly into this encoder's buffer. */
	fun writeDelimited(writeValue: (LuxoEncoder) -> Unit) {
		val prefixPosition = pos
		ensure(1)
		pos++
		val valuePosition = pos
		try {
			writeValue(this)
		} catch (error: Throwable) {
			pos = prefixPosition
			throw error
		}
		val valueLength = pos - valuePosition
		val prefixLength = varintLength(valueLength)
		val shift = prefixLength - 1
		if (shift > 0) {
			ensure(shift)
			System.arraycopy(buf, valuePosition, buf, valuePosition + shift, valueLength)
			pos += shift
		}
		writeVarintAt(prefixPosition, valueLength)
	}

	private fun writeVarintAt(start: Int, input: Int) {
		var position = start
		var value = input
		while (value >= 0x80) {
			buf[position++] = ((value and 0x7f) or 0x80).toByte()
			value /= 128
		}
		buf[position] = value.toByte()
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
        val bytes = requireLuxoUnicode(v).toByteArray(Charsets.UTF_8)
        writeVarint(fieldID.toLong())
        writeBytes(bytes)
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
    inline fun <T> writeFieldArray(fieldID: Int, items: List<T>, crossinline encode: (T) -> Unit) {
        val start = arrayWritePosition()
        try {
            writeVarint(fieldID.toLong())
            writeVarint(items.size.toLong())
            for (item in items) encode(item)
        } catch (error: Throwable) {
            restoreArrayWritePosition(start)
            throw error
        }
    }

    // Part of the inline ABI: rebuild consumers if changed. Not a general checkpoint API.
    @PublishedApi
    internal fun arrayWritePosition(): Int = pos

    @PublishedApi
    internal fun restoreArrayWritePosition(position: Int) {
        pos = position
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

private fun varintLength(input: Int): Int {
	var value = input
	var length = 1
	while (value >= 0x80) {
		value /= 128
		length++
	}
	return length
}

/** Return the UTF-8 byte length without allocating an encoded copy. */
fun luxoUtf8Length(value: String): Int {
	var length = 0
	var index = 0
	while (index < value.length) {
		val code = value[index].code
		when {
			code < 0x80 -> length++
			code < 0x800 -> length += 2
			Character.isSurrogate(value[index]) -> {
				requireLuxoSurrogatePair(value, index)
				length += 4
				index++
			}
			else -> length += 3
		}
		index++
	}
	return length
}

/** Decode a UTF-8 field without copying its byte range or stripping a leading U+FEFF. */
fun decodeLuxoUtf8(bytes: ByteArray, startIndex: Int = 0, endIndex: Int = bytes.size): String {
    val text = String(bytes, startIndex, endIndex - startIndex, Charsets.UTF_8)
    // The platform replaces malformed sequences with U+FFFD. Only ambiguous text
    // needs the strict decoder; a literal U+FFFD is valid and must remain intact.
    if ('\uFFFD' !in text) return text
    return try {
        bytes.decodeToString(startIndex, endIndex, throwOnInvalidSequence = true)
    } catch (_: java.nio.charset.CharacterCodingException) {
        throw LuxoCodecException("invalid UTF-8 string at offset $startIndex")
    }
}

// Match the Go decoder's allocation limits; these do not narrow business Int64 values.
private const val MAX_ARRAY_ELEMENTS = 1_000_000
private const val MAX_COLUMNAR_RECORDS = 10_000_000
private const val MAX_ARENA_SIZE = 64 * 1024 * 1024

private fun checkedMetadata(value: Long, limit: Int, kind: String): Int {
    // Flip the sign bit for unsigned ordering without a platform-specific Long API.
    // Kotlin 2.4 also lowers ULong comparison to Long.compareUnsigned on JVM 1.8.
    if ((value xor Long.MIN_VALUE) > (limit.toLong() xor Long.MIN_VALUE)) {
        throw metadataError(value, limit, kind)
    }
    return value.toInt()
}

// Keep error formatting out of the small, inlineable successful check.
private fun metadataError(value: Long, limit: Int, kind: String): LuxoCodecException =
    LuxoCodecException("invalid $kind ${value.toULong()}, limit $limit")

class LuxoDecoder(private val buf: ByteArray) {

    private var off: Int = 0

    /** The field ID read by the most recent [nextField] call. */
    var fieldID: Int = 0
        private set

    // -- field iteration --------------------------------------------------

    /** Skip the arena header (totalStringLen varint) that prefixes each model's binary data. */
    fun skipArenaHeader() {
        checkedMetadata(readVarintRaw(), MAX_ARENA_SIZE, "arena size")
    }

    /**
     * Read the next field ID. Returns false at the end marker (0x00).
     * Exhaustion without an end marker is malformed input.
     */
    fun nextField(): Boolean {
        if (off >= buf.size) throw LuxoCodecException("missing row end marker")
        val id = readVarintRaw()
        fieldID = checkedMetadata(id, Int.MAX_VALUE, "field ID")
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
        return finiteFloatFromBits(bits)
    }

    /** Read a length-prefixed UTF-8 string. */
    fun readString(): String {
        val length = readVarintRaw()
        val len = checkedMetadata(length, buf.size - off, "string length")
        val result = decodeLuxoUtf8(buf, off, off + len)
        off += len
        return result
    }

    /** Read length-prefixed arbitrary bytes. */
    fun readBytes(): ByteArray {
        val length = readVarintRaw()
        val len = checkedMetadata(length, buf.size - off, "byte length")
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

    /** Validate a complete response or length-delimited child, never an input field. */
    fun checkComplete() {
        if (off != buf.size) throw LuxoCodecException("trailing binary data")
    }

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
     * Read a DateTime seconds/nanoseconds pair, returned as an
     * ISO-8601 string to match the JSON-mode representation (Kotlin `String`).
     */
    fun readDateTime(): String = DateTimeCodec.toIso(readInt(), readVarintRaw())

    fun readDateTimeArray(): List<String> {
        val count = checkedMetadata(readVarintRaw(), MAX_ARRAY_ELEMENTS, "array count")
        checkRemaining(count * 2)
        return List(count) { readDateTime() }
    }

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

    /** Read a nullable DateTime (0x00=null, else seconds/nanoseconds -> RFC3339). */
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
        val count = checkedMetadata(readVarintRaw(), MAX_ARRAY_ELEMENTS, "array count")
        // Every wire element has at least one byte, including null and empty values.
        checkRemaining(count)
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
            if (b and 0x80 == 0L) {
                if (shift == 63 && b > 1) throw LuxoCodecException("varint overflow")
                break
            }
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
        if (n > buf.size - off) {
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
        count = checkedMetadata(readVarintRaw(), MAX_COLUMNAR_RECORDS, "column count")
        arenaSize = checkedMetadata(readVarintRaw(), MAX_ARENA_SIZE, "arena size")
        if (count > 0) {
            if (count > buf.size - off - 2) throw LuxoCodecException("insufficient columnar element bytes")
            val start = off
            var id = buf[start].toLong() and 0xffL
            if (id >= 0x80) {
                id = readVarintRaw()
                if (count > buf.size - off - 1) throw LuxoCodecException("insufficient columnar element bytes")
                off = start
            }
            if (id == 0L) throw LuxoCodecException("nonempty list requires at least one column")
        }
    }

    /** Advance to the next column; exhaustion before its end marker is malformed. */
    fun nextColumn(): Boolean {
        if (off >= buf.size) throw LuxoCodecException("missing column end marker")
        val id = checkedMetadata(readVarintRaw(), Int.MAX_VALUE, "field ID")
        fieldID = id
        return id != 0
    }

    /** Read [count] zigzag-encoded signed int64 values. */
    fun readColumnInt(): List<Long> {
        checkRemaining(count)
        val result = LongArray(count)
        for (i in 0 until count) {
            val uv = readVarintRaw()
            result[i] = (uv ushr 1) xor -(uv and 1L)
        }
        return result.asList()
    }

    /**
     * Read [count] DateTime pairs,
     * converting each to its ISO-8601 string representation.
     */
    fun readColumnDateTime(): List<String> {
        checkRemaining(count * 2)
        val result = Array(count) { "" }
        for (i in 0 until count) {
            result[i] = readDateTime()
        }
        return result.asList()
    }

    /** Read [count] nullable DateTime pairs (0x00=null). */
    fun readColumnDateTimePtr(): List<String?> {
        checkRemaining(count)
        val result = arrayOfNulls<String>(count)
        for (i in 0 until count) {
            if (!readCanonicalMarker("nullable")) continue
            result[i] = readDateTime()
        }
        return result.asList()
    }

    private fun readDateTime(): String {
        val unsigned = readVarintRaw()
        val seconds = (unsigned ushr 1) xor -(unsigned and 1L)
        return DateTimeCodec.toIso(seconds, readVarintRaw())
    }

    /** Read [count] fixed64 (8-byte LE) float values. */
    fun readColumnFloat(): List<Double> {
        checkRemaining(count * 8)
        val result = DoubleArray(count)
        for (i in 0 until count) {
            var bits: Long = 0
            bits = bits or ((buf[off++].toLong() and 0xFF))
            bits = bits or ((buf[off++].toLong() and 0xFF) shl 8)
            bits = bits or ((buf[off++].toLong() and 0xFF) shl 16)
            bits = bits or ((buf[off++].toLong() and 0xFF) shl 24)
            bits = bits or ((buf[off++].toLong() and 0xFF) shl 32)
            bits = bits or ((buf[off++].toLong() and 0xFF) shl 40)
            bits = bits or ((buf[off++].toLong() and 0xFF) shl 48)
            bits = bits or ((buf[off++].toLong() and 0xFF) shl 56)
            result[i] = finiteFloatFromBits(bits)
        }
        return result.asList()
    }

    /** Read [count] length-prefixed UTF-8 string values. */
    fun readColumnString(): List<String> {
        checkRemaining(count)
        val result = Array(count) { "" }
        for (i in 0 until count) {
            val len = readByteLength()
            if (len == 0) { result[i] = ""; continue }
            result[i] = decodeLuxoUtf8(buf, off, off + len)
            off += len
        }
        return result.asList()
    }

    /** Read [count] boolean values (varint 0/1). */
    fun readColumnBool(): List<Boolean> {
        checkRemaining(count)
        val result = BooleanArray(count)
        for (i in 0 until count) {
            result[i] = readCanonicalMarker("bool")
        }
        return result.asList()
    }

    /** Read [count] nullable int values (0x00=null, 0x01+svarint). */
    fun readColumnIntPtr(): List<Long?> {
        checkRemaining(count)
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
        checkRemaining(count)
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
            result[i] = finiteFloatFromBits(bits)
        }
        return result.asList()
    }

    /** Read [count] nullable string values (0x00=null, 0x01+string). */
    fun readColumnStringPtr(): List<String?> {
        checkRemaining(count)
        val result = arrayOfNulls<String>(count)
        for (i in 0 until count) {
            if (!readCanonicalMarker("nullable")) continue
            val len = readByteLength()
            if (len == 0) { result[i] = ""; continue }
            result[i] = decodeLuxoUtf8(buf, off, off + len)
            off += len
        }
        return result.asList()
    }

    /** Read [count] nullable boolean values (0x00=null, 0x01+varint). */
    fun readColumnBoolPtr(): List<Boolean?> {
        checkRemaining(count)
        val result = arrayOfNulls<Boolean>(count)
        for (i in 0 until count) {
            if (!readCanonicalMarker("nullable")) continue
            result[i] = readCanonicalMarker("bool")
        }
        return result.asList()
    }

    /** Read [count] fixed 16-byte UUID values as canonical lowercase strings. */
    fun readColumnUuid(): List<String> {
        checkRemaining(count * 16)
        val result = Array(count) { "" }
        for (i in 0 until count) {
            result[i] = UuidCodec.format(buf, off)
            off += 16
        }
        return result.asList()
    }

    /** Read [count] nullable UUID values (0x00=null, 0x01+16 bytes). */
    fun readColumnUuidPtr(): List<String?> {
        checkRemaining(count)
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
        checkRemaining(count)
        val result = Array(count) { ByteArray(0) }
        for (i in 0 until count) {
            val len = readByteLength()
            result[i] = buf.copyOfRange(off, off + len)
            off += len
        }
        return result.asList()
    }

    /** Read [count] nullable byte blobs (0x00=null, 0x01+length+bytes). */
    fun readColumnBytesPtr(): List<ByteArray?> {
        checkRemaining(count)
        val result = arrayOfNulls<ByteArray>(count)
        for (i in 0 until count) {
            if (!readCanonicalMarker("nullable")) continue
            val len = readByteLength()
            result[i] = buf.copyOfRange(off, off + len)
            off += len
        }
        return result.asList()
    }

    /** Current read position (for reading pagination metadata after 0x00). */
    fun offset(): Int = off

    /** Validate the batch boundary after any pagination metadata has been read. */
    fun checkComplete() {
        if (off != buf.size) throw LuxoCodecException("trailing columnar data")
    }

    /** Read one signed zigzag-encoded varint at current position. */
    fun readSvarint(): Long {
        val uv = readVarintRaw()
        return (uv ushr 1) xor -(uv and 1L)
    }

    // -- internal ---------------------------------------------------------

    private fun readByteLength(): Int {
        val length = readVarintRaw()
        return checkedMetadata(length, buf.size - off, "byte length")
    }

    private fun readVarintRaw(): Long {
        var result: Long = 0
        var shift = 0
        while (true) {
            checkRemaining(1)
            val b = buf[off++].toLong() and 0xFF
            result = result or ((b and 0x7F) shl shift)
            if (b and 0x80 == 0L) {
                if (shift == 63 && b > 1) throw LuxoCodecException("varint overflow")
                break
            }
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
        if (n > buf.size - off) {
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
        if (s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-') {
            throw LuxoCodecException("invalid UUID separators: $s")
        }
        val out = ByteArray(16)
        var i = 0
        for (j in 0 until 16) {
            if (j == 4 || j == 6 || j == 8 || j == 10) i++
            val hi = nibble(s[i])
            val lo = nibble(s[i + 1])
            out[j] = ((hi shl 4) or lo).toByte()
            i += 2
        }
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
 * On the Luxo binary wire a DateTime is signed seconds plus unsigned nanos. JSON / API
 * surfaces use an ISO-8601 string. Both SDK modes expose DateTime as a Kotlin
 * `String`, so binary decoding converts seconds to the canonical ISO form.
 */
object DateTimeCodec {
    private val timestampPattern = Regex("""[0-9]{4}-[0-9]{2}-[0-9]{2}[Tt][0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,9})?([Zz]|[+-]([0-9]{2}):([0-9]{2}))""")

    fun parse(value: String): java.time.Instant {
        val match = timestampPattern.matchEntire(value) ?: throw LuxoCodecException("DateTime requires RFC3339 with at most 9 fractional digits")
        val zone = match.groups[1]!!
        val hours = match.groups[2]?.value?.toInt() ?: 0
        val minutes = match.groups[3]?.value?.toInt() ?: 0
        if (hours > 23 || minutes > 59) throw LuxoCodecException("invalid DateTime offset")
        val offset = (hours * 3600 + minutes * 60) * (if (zone.value[0] == '-') -1 else 1)
        val local = try {
            java.time.LocalDateTime.parse(value.substring(0, zone.range.first))
        } catch (_: java.time.DateTimeException) {
            throw LuxoCodecException("invalid DateTime calendar")
        }
        val seconds = local.toEpochSecond(java.time.ZoneOffset.UTC) - offset
        requireRange(seconds)
        return java.time.Instant.ofEpochSecond(seconds, local.nano.toLong())
    }

    fun validate(value: String): String {
        parse(value)
        return value
    }

    private fun requireRange(seconds: Long) {
        if (seconds < -62167219200L || seconds > 253402300799L) throw LuxoCodecException("DateTime out of range")
    }

    /** Format the fraction separately to match Go's trimmed RFC3339Nano output. */
    fun toIso(seconds: Long, nanoseconds: Long = 0): String {
        requireRange(seconds)
        if (nanoseconds !in 0..999999999) throw LuxoCodecException("invalid DateTime nanoseconds")
        val whole = java.time.Instant.ofEpochSecond(seconds).toString()
        if (nanoseconds == 0L) return whole
        return whole.dropLast(1) + "." + nanoseconds.toString().padStart(9, '0').trimEnd('0') + "Z"
    }
}

class LuxoCodecException(message: String) : RuntimeException(message)
