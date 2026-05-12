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
