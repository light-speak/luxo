package com.luxo.client

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import kotlin.test.assertFailsWith

class LuxoEncoderDecoderTest {

    // -- Varint round-trip --

    @Test
    fun `varint round-trip zero`() {
        val enc = LuxoEncoder()
        enc.writeVarint(0L)
        val dec = LuxoDecoder(enc.bytes())
        // Use nextField which reads a varint internally
        // fieldID=0 means end, so nextField returns false
        assertFalse(dec.nextField())
        assertEquals(0, dec.fieldID)
    }

    @Test
    fun `varint round-trip small value`() {
        val enc = LuxoEncoder()
        // Write fieldID=1, then an svarint value
        enc.writeFieldInt(1, 42)
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals(1, dec.fieldID)
        assertEquals(42L, dec.readInt())
        assertFalse(dec.nextField())
    }

    @Test
    fun `varint round-trip large value`() {
        val enc = LuxoEncoder()
        enc.writeFieldInt(127, Long.MAX_VALUE)
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals(127, dec.fieldID)
        assertEquals(Long.MAX_VALUE, dec.readInt())
        assertFalse(dec.nextField())
    }

    @Test
    fun `varint round-trip multi-byte fieldID`() {
        val enc = LuxoEncoder()
        enc.writeFieldInt(300, 100)
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals(300, dec.fieldID)
        assertEquals(100L, dec.readInt())
        assertFalse(dec.nextField())
    }

    // -- Svarint round-trip --

    @Test
    fun `svarint negative values`() {
        val enc = LuxoEncoder()
        enc.writeFieldInt(1, -1)
        enc.writeFieldInt(2, -128)
        enc.writeFieldInt(3, Long.MIN_VALUE)
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals(1, dec.fieldID)
        assertEquals(-1L, dec.readInt())

        assertTrue(dec.nextField())
        assertEquals(2, dec.fieldID)
        assertEquals(-128L, dec.readInt())

        assertTrue(dec.nextField())
        assertEquals(3, dec.fieldID)
        assertEquals(Long.MIN_VALUE, dec.readInt())

        assertFalse(dec.nextField())
    }

    @Test
    fun `svarint zero`() {
        val enc = LuxoEncoder()
        enc.writeFieldInt(1, 0)
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals(1, dec.fieldID)
        assertEquals(0L, dec.readInt())
        assertFalse(dec.nextField())
    }

    // -- Fixed64 round-trip --

    @Test
    fun `fixed64 common values`() {
        val values = listOf(0.0, 1.0, -1.0, 3.14159, Double.MAX_VALUE, Double.MIN_VALUE, Double.NaN)
        val enc = LuxoEncoder()
        for ((i, v) in values.withIndex()) {
            enc.writeFieldFloat(i + 1, v)
        }
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        for ((i, expected) in values.withIndex()) {
            assertTrue(dec.nextField())
            assertEquals(i + 1, dec.fieldID)
            val actual = dec.readFloat()
            if (expected.isNaN()) {
                assertTrue(actual.isNaN())
            } else {
                assertEquals(expected, actual)
            }
        }
        assertFalse(dec.nextField())
    }

    @Test
    fun `fixed64 infinity`() {
        val enc = LuxoEncoder()
        enc.writeFieldFloat(1, Double.POSITIVE_INFINITY)
        enc.writeFieldFloat(2, Double.NEGATIVE_INFINITY)
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals(Double.POSITIVE_INFINITY, dec.readFloat())
        assertTrue(dec.nextField())
        assertEquals(Double.NEGATIVE_INFINITY, dec.readFloat())
        assertFalse(dec.nextField())
    }

    // -- String round-trip --

    @Test
    fun `string empty`() {
        val enc = LuxoEncoder()
        enc.writeFieldString(1, "")
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals(1, dec.fieldID)
        assertEquals("", dec.readString())
        assertFalse(dec.nextField())
    }

    @Test
    fun `string ascii`() {
        val enc = LuxoEncoder()
        enc.writeFieldString(1, "hello world")
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals("hello world", dec.readString())
        assertFalse(dec.nextField())
    }

    @Test
    fun `string unicode`() {
        val text = "你好世界 \uD83D\uDE00"
        val enc = LuxoEncoder()
        enc.writeFieldString(1, text)
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals(text, dec.readString())
        assertFalse(dec.nextField())
    }

    @Test
    fun `string long`() {
        val text = "a".repeat(10_000)
        val enc = LuxoEncoder()
        enc.writeFieldString(1, text)
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals(text, dec.readString())
        assertFalse(dec.nextField())
    }

    // -- Bool round-trip --

    @Test
    fun `bool true and false`() {
        val enc = LuxoEncoder()
        enc.writeFieldBool(1, true)
        enc.writeFieldBool(2, false)
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals(1, dec.fieldID)
        assertTrue(dec.readBool())

        assertTrue(dec.nextField())
        assertEquals(2, dec.fieldID)
        assertFalse(dec.readBool())

        assertFalse(dec.nextField())
    }

    // -- Edge cases --

    @Test
    fun `empty buffer returns false on nextField`() {
        val dec = LuxoDecoder(ByteArray(0))
        assertFalse(dec.nextField())
        assertEquals(0, dec.fieldID)
    }

    @Test
    fun `decoder throws on truncated buffer`() {
        // A single byte 0x81 means continuation bit set but no next byte for the string length
        val enc = LuxoEncoder()
        enc.writeFieldString(1, "hello")
        val full = enc.bytes()
        // Truncate: keep fieldID varint but cut the string data
        val truncated = full.copyOf(2)

        val dec = LuxoDecoder(truncated)
        assertTrue(dec.nextField()) // reads fieldID=1
        assertFailsWith<LuxoCodecException> {
            dec.readString() // should fail — not enough bytes
        }
    }

    @Test
    fun `multiple fields interleaved types`() {
        val enc = LuxoEncoder()
        enc.writeFieldInt(1, 999)
        enc.writeFieldString(2, "test")
        enc.writeFieldBool(3, true)
        enc.writeFieldFloat(4, 2.718)
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())

        assertTrue(dec.nextField())
        assertEquals(1, dec.fieldID)
        assertEquals(999L, dec.readInt())

        assertTrue(dec.nextField())
        assertEquals(2, dec.fieldID)
        assertEquals("test", dec.readString())

        assertTrue(dec.nextField())
        assertEquals(3, dec.fieldID)
        assertTrue(dec.readBool())

        assertTrue(dec.nextField())
        assertEquals(4, dec.fieldID)
        assertEquals(2.718, dec.readFloat())

        assertFalse(dec.nextField())
    }

    @Test
    fun `encoder grows buffer beyond initial capacity`() {
        val enc = LuxoEncoder(initialCapacity = 4)
        enc.writeFieldString(1, "this string is longer than 4 bytes")
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals("this string is longer than 4 bytes", dec.readString())
        assertFalse(dec.nextField())
    }
}

class ColumnarDecoderTest {

    /** Helper: write unsigned varint to a list. */
    private fun MutableList<Byte>.writeVarint(v: Long) {
        var uv = v
        while (uv and 0x7FL.inv() != 0L) {
            add(((uv and 0x7F) or 0x80).toByte())
            uv = uv ushr 7
        }
        add((uv and 0x7F).toByte())
    }

    /** Helper: write zigzag svarint to a list. */
    private fun MutableList<Byte>.writeSvarint(v: Long) {
        writeVarint((v shl 1) xor (v shr 63))
    }

    /** Helper: write fixed64 (LE float64) to a list. */
    private fun MutableList<Byte>.writeFixed64(v: Double) {
        val bits = java.lang.Double.doubleToRawLongBits(v)
        for (i in 0 until 8) {
            add((bits ushr (i * 8)).toByte())
        }
    }

    /** Helper: write length-prefixed string to a list. */
    private fun MutableList<Byte>.writeString(v: String) {
        val bytes = v.toByteArray(Charsets.UTF_8)
        writeVarint(bytes.size.toLong())
        bytes.forEach { add(it) }
    }

    @Test
    fun `decode 2 records with int, string, float columns`() {
        val buf = mutableListOf<Byte>()
        buf.writeVarint(2) // count=2
        // Column 1: fieldID=1, int values [42, -7]
        buf.writeVarint(1)
        buf.writeSvarint(42)
        buf.writeSvarint(-7)
        // Column 2: fieldID=2, string values ["hello", "world"]
        buf.writeVarint(2)
        buf.writeString("hello")
        buf.writeString("world")
        // Column 3: fieldID=3, float values [3.14, 2.718]
        buf.writeVarint(3)
        buf.writeFixed64(3.14)
        buf.writeFixed64(2.718)
        // End marker
        buf.add(0x00)

        val dec = ColumnarDecoder(buf.toByteArray())
        assertEquals(2, dec.count)

        assertTrue(dec.nextColumn())
        assertEquals(1, dec.fieldID)
        assertEquals(listOf(42L, -7L), dec.readColumnInt())

        assertTrue(dec.nextColumn())
        assertEquals(2, dec.fieldID)
        assertEquals(listOf("hello", "world"), dec.readColumnString())

        assertTrue(dec.nextColumn())
        assertEquals(3, dec.fieldID)
        val floats = dec.readColumnFloat()
        assertEquals(3.14, floats[0], 0.0001)
        assertEquals(2.718, floats[1], 0.0001)

        assertFalse(dec.nextColumn())
    }

    @Test
    fun `empty list (count=0)`() {
        val buf = mutableListOf<Byte>()
        buf.writeVarint(0) // count=0
        buf.add(0x00) // end marker

        val dec = ColumnarDecoder(buf.toByteArray())
        assertEquals(0, dec.count)
        assertFalse(dec.nextColumn())
    }

    @Test
    fun `nullable columns`() {
        val buf = mutableListOf<Byte>()
        buf.writeVarint(3) // count=3
        // Column 1: fieldID=1, nullable int [null, 99, null]
        buf.writeVarint(1)
        buf.add(0x00) // null
        buf.add(0x01); buf.writeSvarint(99) // present
        buf.add(0x00) // null
        // Column 2: fieldID=2, nullable string [null, "hi", ""]
        buf.writeVarint(2)
        buf.add(0x00) // null
        buf.add(0x01); buf.writeString("hi") // present
        buf.add(0x01); buf.writeString("") // present empty
        // End marker
        buf.add(0x00)

        val dec = ColumnarDecoder(buf.toByteArray())
        assertEquals(3, dec.count)

        assertTrue(dec.nextColumn())
        assertEquals(1, dec.fieldID)
        assertEquals(listOf(null, 99L, null), dec.readColumnIntPtr())

        assertTrue(dec.nextColumn())
        assertEquals(2, dec.fieldID)
        assertEquals(listOf(null, "hi", ""), dec.readColumnStringPtr())

        assertFalse(dec.nextColumn())
    }

    @Test
    fun `bool column`() {
        val buf = mutableListOf<Byte>()
        buf.writeVarint(3) // count=3
        buf.writeVarint(1) // fieldID=1
        buf.writeVarint(1) // true
        buf.writeVarint(0) // false
        buf.writeVarint(1) // true
        buf.add(0x00)

        val dec = ColumnarDecoder(buf.toByteArray())
        assertEquals(3, dec.count)
        assertTrue(dec.nextColumn())
        assertEquals(listOf(true, false, true), dec.readColumnBool())
        assertFalse(dec.nextColumn())
    }

    @Test
    fun `offset and readSvarint`() {
        val buf = mutableListOf<Byte>()
        buf.writeVarint(0) // count=0
        buf.add(0x00) // end marker
        buf.writeSvarint(-42) // pagination metadata

        val dec = ColumnarDecoder(buf.toByteArray())
        assertFalse(dec.nextColumn())
        assertEquals(-42L, dec.readSvarint())
    }
}

class FieldMaskTest {

    @Test
    fun `set and has basic`() {
        var mask = ByteArray(0)
        mask = FieldMask.set(mask, 0)
        mask = FieldMask.set(mask, 7)
        mask = FieldMask.set(mask, 8)
        mask = FieldMask.set(mask, 15)

        assertTrue(FieldMask.has(mask, 0))
        assertTrue(FieldMask.has(mask, 7))
        assertTrue(FieldMask.has(mask, 8))
        assertTrue(FieldMask.has(mask, 15))
        assertFalse(FieldMask.has(mask, 1))
        assertFalse(FieldMask.has(mask, 9))
        assertFalse(FieldMask.has(mask, 16))
    }

    @Test
    fun `has returns false for out of range`() {
        val mask = ByteArray(1)
        assertFalse(FieldMask.has(mask, 100))
    }

    @Test
    fun `set grows mask array`() {
        var mask = ByteArray(1)
        mask = FieldMask.set(mask, 64)
        assertTrue(FieldMask.has(mask, 64))
        assertEquals(9, mask.size) // 64/8 + 1 = 9
    }
}
