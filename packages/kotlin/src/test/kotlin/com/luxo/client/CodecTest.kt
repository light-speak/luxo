package com.luxo.client

import java.io.File
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.long
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import kotlin.test.assertFailsWith

class LuxoEncoderDecoderTest {

    @Test
    fun `row decoder requires an end marker`() {
        val row = LuxoDecoder(byteArrayOf(0, 1, 84))
        row.skipArenaHeader()
        assertTrue(row.nextField())
        assertEquals(42L, row.readInt())
        assertFailsWith<LuxoCodecException> { row.nextField() }
    }

    @Test
    fun `column decoder requires an end marker`() {
        val column = ColumnarDecoder(byteArrayOf(1, 0, 1, 0x80.toByte(), 1))
        assertTrue(column.nextColumn())
        assertEquals(listOf(64L), column.readColumnInt())
        assertFailsWith<LuxoCodecException> { column.nextColumn() }
    }

    @Test
    fun `response completion rejects trailing data and respects pagination`() {
        for (extra in listOf(byteArrayOf(), byteArrayOf(99))) {
            val row = LuxoDecoder(byteArrayOf(0, 0) + extra)
            row.skipArenaHeader()
            assertFalse(row.nextField())
            val column = ColumnarDecoder(byteArrayOf(0, 0, 0) + extra)
            assertFalse(column.nextColumn())
            if (extra.isEmpty()) {
                row.checkComplete()
                column.checkComplete()
            } else {
                assertFailsWith<LuxoCodecException> { row.checkComplete() }
                assertFailsWith<LuxoCodecException> { column.checkComplete() }
            }
        }
        val page = ColumnarDecoder(byteArrayOf(0, 0, 0, 0, 2, 40))
        assertFalse(page.nextColumn())
        assertFailsWith<LuxoCodecException> { page.checkComplete() }
        assertEquals(0L, page.readSvarint())
        assertEquals(1L, page.readSvarint())
        assertEquals(20L, page.readSvarint())
        page.checkComplete()
    }

    @Test
    fun `int64 matches canonical wire and JSON values`() {
        val fixture = Json.parseToJsonElement(
            File("../../pkg/lux/codec/testdata/protocol-v1.json").readText()
        ).jsonObject
        for (entry in fixture.getValue("int64Cases").jsonArray) {
            val item = entry.jsonObject
            val value = item.getValue("value").jsonPrimitive.content.toLong()
            val wire = fixtureBytes(item.getValue("wire").jsonPrimitive.content)
            val encoder = LuxoEncoder()
            encoder.writeSvarint(value)
            assertTrue(wire.contentEquals(encoder.bytes()), "wire for $value")
            assertEquals(value, LuxoDecoder(wire).readInt())
            val containerWire = byteArrayOf(1) + wire
            assertEquals(value, LuxoDecoder(containerWire).readIntPtr())
            val array = LuxoDecoder(containerWire)
            assertEquals(listOf(value), array.readArray { array.readInt() })
            val column = ColumnarDecoder(byteArrayOf(1, 0, 1) + wire + byteArrayOf(0))
            assertTrue(column.nextColumn())
            assertEquals(listOf(value), column.readColumnInt())
            val nullableColumn = ColumnarDecoder(byteArrayOf(1, 0, 1) + containerWire + byteArrayOf(0))
            assertTrue(nullableColumn.nextColumn())
            assertEquals(listOf(value), nullableColumn.readColumnIntPtr())
            assertEquals(value, Json.parseToJsonElement(value.toString()).jsonPrimitive.long)
        }
    }

    @Test
    fun `reject invalid int64 in row and column encodings`() {
        val fixture = Json.parseToJsonElement(
            File("../../pkg/lux/codec/testdata/protocol-v1.json").readText()
        ).jsonObject
        for (entry in fixture.getValue("invalidInt64").jsonArray) {
            val wire = fixtureBytes(entry.jsonPrimitive.content)
            assertFailsWith<LuxoCodecException>(entry.toString()) { LuxoDecoder(wire).readInt() }
            assertFailsWith<LuxoCodecException>(entry.toString()) {
                val column = ColumnarDecoder(byteArrayOf(1, 0, 1) + wire)
                assertTrue(column.nextColumn())
                column.readColumnInt()
            }
        }
    }

    private fun fixtureBytes(hex: String): ByteArray =
        hex.chunked(2).map { it.toInt(16).toByte() }.toByteArray()

	@Test
	fun `length-delimited values are written in place`() {
		val enc = LuxoEncoder(4)
		enc.writeDelimited { value ->
			value.writeVarint(7L)
			value.writeString("go")
		}
		assertTrue(byteArrayOf(4, 7, 2, 103, 111).contentEquals(enc.bytes()))

		val large = LuxoEncoder(4)
		large.writeDelimited { it.writeRawBytes(ByteArray(130) { 9 }) }
		val bytes = large.bytes()
		assertEquals(132, bytes.size)
		assertTrue(byteArrayOf(0x82.toByte(), 1).contentEquals(bytes.copyOfRange(0, 2)))
		assertTrue(bytes.copyOfRange(2, bytes.size).all { it == 9.toByte() })
	}

    @Test
    fun `bytes round-trip preserves arbitrary binary data`() {
        val expected = byteArrayOf(0, 1, 127, 0x80.toByte(), 0xff.toByte())
        val enc = LuxoEncoder()
        enc.writeFieldBytes(1, expected)
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals(1, dec.fieldID)
        assertTrue(expected.contentEquals(dec.readBytes()))
        assertFalse(dec.nextField())
    }

    @Test
    fun `byte lengths cannot wrap to a smaller Int`() {
        for (length in listOf(2L, 2147483648L, 4294967296L, 4294967297L, Long.MAX_VALUE, Long.MIN_VALUE, -1L)) {
            val encoder = LuxoEncoder()
            encoder.writeVarint(length)
            encoder.writeRawBytes(byteArrayOf(65))
            assertFailsWith<LuxoCodecException>("length=$length") { LuxoDecoder(encoder.bytes()).readBytes() }
            assertFailsWith<LuxoCodecException>("string length=$length") { LuxoDecoder(encoder.bytes()).readString() }
        }
    }

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
        val values = listOf(0.0, -0.0, 1.0, -1.0, 3.14159, Double.MAX_VALUE, Double.MIN_VALUE)
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
            assertEquals(expected.toRawBits(), actual.toRawBits())
        }
        assertFalse(dec.nextField())
    }

    @Test
    fun `fixed64 rejects non-finite values`() {
        for (value in listOf(Double.NaN, Double.POSITIVE_INFINITY, Double.NEGATIVE_INFINITY)) {
            assertFailsWith<LuxoCodecException> { LuxoEncoder().writeFieldFloat(1, value) }
        }
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
    fun `empty buffer cannot stand in for an end marker`() {
        val dec = LuxoDecoder(ByteArray(0))
        assertFailsWith<LuxoCodecException> { dec.nextField() }
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

    // -- UUID round-trip (16-byte fixed) --

    @Test
    fun `uuid rejects every corrupted position and non-canonical length`() {
        val valid = "00112233-4455-6677-8899-aabbccddeeff"
        for (index in valid.indices) {
            val replacements = if (valid[index] == '-') listOf('0', '_', '−') else listOf('g', '-', '\u0000', 'é')
            for (replacement in replacements) {
                val malformed = valid.replaceRange(index, index + 1, replacement.toString())
                assertFailsWith<LuxoCodecException>(malformed) { UuidCodec.parse(malformed) }
            }
        }
        for (input in listOf("", valid.drop(1), valid + "0", valid.replace("-", ""))) {
            assertFailsWith<LuxoCodecException>(input) { UuidCodec.parse(input) }
        }
    }

    @Test
    fun `uuid canonical shared cases reject misplaced separators`() {
        val fixture = Json.parseToJsonElement(File("../../pkg/lux/codec/testdata/protocol-v1.json").readText()).jsonObject
        val cases = fixture.getValue("uuidCanonicalCases").jsonArray
        assertEquals(11, cases.size)
        for (entry in cases) {
            val item = entry.jsonObject
            val input = item.getValue("input").jsonPrimitive.content
            val canonical = item.getValue("canonical")
            val encoder = LuxoEncoder()
            if (canonical == kotlinx.serialization.json.JsonNull) {
                assertFailsWith<LuxoCodecException>(input) { UuidCodec.parse(input) }
                assertFailsWith<LuxoCodecException>(input) { encoder.writeUuid(input) }
                assertEquals(0, encoder.bytes().size, input)
            } else {
                encoder.writeUuid(input)
                assertEquals(canonical.jsonPrimitive.content, LuxoDecoder(encoder.bytes()).readUuid(), input)
            }
        }
    }

    @Test
    fun `uuid round-trip canonical string`() {
        val uuid = "550e8400-e29b-41d4-a716-446655440000"
        val enc = LuxoEncoder()
        enc.writeFieldUuid(1, uuid)
        enc.writeEnd()

        // Wire size: fieldID(1) + 16 bytes + end(1) = 18 bytes
        assertEquals(18, enc.bytes().size)

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals(1, dec.fieldID)
        assertEquals(uuid, dec.readUuid())
        assertFalse(dec.nextField())
    }

    @Test
    fun `uuid uppercase input formats to lowercase`() {
        val enc = LuxoEncoder()
        enc.writeFieldUuid(1, "AABBCCDD-EEFF-0011-2233-445566778899")
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals("aabbccdd-eeff-0011-2233-445566778899", dec.readUuid())
    }

    @Test
    fun `uuid nullable present and null`() {
        val uuid = "00112233-4455-6677-8899-aabbccddeeff"
        val enc = LuxoEncoder()
        enc.writeVarint(1)            // fieldID 1
        enc.writeBool(true)           // present flag
        enc.writeUuid(uuid)
        enc.writeVarint(2)            // fieldID 2
        enc.writeBool(false)          // null flag
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals(uuid, dec.readUuidPtr())
        assertTrue(dec.nextField())
        assertEquals(null, dec.readUuidPtr())
        assertFalse(dec.nextField())
    }

    @Test
    fun `uuid parse rejects bad input`() {
        assertFailsWith<LuxoCodecException> { UuidCodec.parse("too-short") }
        assertFailsWith<LuxoCodecException> { UuidCodec.parse("zz0e8400-e29b-41d4-a716-446655440000") }
    }

    @Test
    fun `uuid format matches Go canonical layout`() {
        // bytes 0..15 -> dashes after byte index 3,5,7,9 (the 4/6/8/10 positions)
        val raw = ByteArray(16) { it.toByte() }
        val s = UuidCodec.format(raw, 0)
        assertEquals("00010203-0405-0607-0809-0a0b0c0d0e0f", s)
    }

    // -- Scalar array fields (row form: [count][items]) --

    @Test
    fun `scalar string array round-trip`() {
        val tags = listOf("a", "bb", "ccc")
        val enc = LuxoEncoder()
        enc.writeFieldArray(1, tags) { enc.writeString(it) }
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals(tags, dec.readArray { dec.readString() })
        assertFalse(dec.nextField())
    }

    @Test
    fun `scalar int array round-trip`() {
        val nums = listOf(1L, -2L, 300L)
        val enc = LuxoEncoder()
        enc.writeFieldArray(1, nums) { enc.writeSvarint(it) }
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals(nums, dec.readArray { dec.readInt() })
    }

    @Test
    fun `scalar uuid array round-trip`() {
        val ids = listOf(
            "550e8400-e29b-41d4-a716-446655440000",
            "00112233-4455-6677-8899-aabbccddeeff",
        )
        val enc = LuxoEncoder()
        enc.writeFieldArray(1, ids) { enc.writeUuid(it) }
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals(ids, dec.readArray { dec.readUuid() })
    }

    @Test
    fun `empty scalar array`() {
        val enc = LuxoEncoder()
        enc.writeFieldArray(1, emptyList<String>()) { enc.writeString(it) }
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals(emptyList(), dec.readArray { dec.readString() })
    }

    // -- DateTime (floor Unix seconds + nanoseconds -> ISO string) --

    @Test
    fun `datetime decodes svarint seconds to ISO string`() {
        // Go wire: svarint(seconds) + varint(nanos). 1748424000 == 2025-05-28T09:20:00Z.
        val enc = LuxoEncoder()
        enc.writeVarint(1)
        enc.writeDateTime("2025-05-28T09:20:00Z")
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals(1, dec.fieldID)
        assertEquals("2025-05-28T09:20:00Z", dec.readDateTime())
        assertFalse(dec.nextField())
    }

    @Test
    fun `datetime epoch zero`() {
        val enc = LuxoEncoder()
        enc.writeVarint(1)
        enc.writeDateTime("1970-01-01T00:00:00Z")
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals("1970-01-01T00:00:00Z", dec.readDateTime())
    }

    @Test
    fun `datetime negative seconds (pre-epoch)`() {
        val enc = LuxoEncoder()
        enc.writeVarint(1)
        enc.writeDateTime("1969-12-31T23:59:59Z")
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals("1969-12-31T23:59:59Z", dec.readDateTime())
    }

    @Test
    fun `nullable datetime present`() {
        val enc = LuxoEncoder()
        enc.writeVarint(1) // fieldID
        enc.writeBool(true) // null flag = present
        enc.writeDateTime("2025-05-28T09:20:00Z")
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals("2025-05-28T09:20:00Z", dec.readDateTimePtr())
    }

    @Test
    fun `nullable datetime null`() {
        val enc = LuxoEncoder()
        enc.writeVarint(1) // fieldID
        enc.writeBool(false) // null flag = absent
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals(null, dec.readDateTimePtr())
    }

    @Test
    fun `datetime scalar array round-trip`() {
        val secs = listOf(0L, 1748424000L, -1L)
        val enc = LuxoEncoder()
        enc.writeFieldArray(1, secs) { enc.writeSvarint(it); enc.writeVarint(0) }
        enc.writeEnd()

        val dec = LuxoDecoder(enc.bytes())
        assertTrue(dec.nextField())
        assertEquals(
            listOf("1970-01-01T00:00:00Z", "2025-05-28T09:20:00Z", "1969-12-31T23:59:59Z"),
            dec.readDateTimeArray(),
        )
    }

    @Test
    fun `datetime codec toIso`() {
        assertEquals("1970-01-01T00:00:00Z", DateTimeCodec.toIso(0L))
        assertEquals("2025-05-28T09:20:00Z", DateTimeCodec.toIso(1748424000L))
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
        buf.writeVarint(10) // arena size
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
        assertEquals(10, dec.arenaSize)

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
        buf.writeVarint(0) // arena size
        buf.add(0x00) // end marker

        val dec = ColumnarDecoder(buf.toByteArray())
        assertEquals(0, dec.count)
        assertFalse(dec.nextColumn())
    }

    @Test
    fun `nullable columns`() {
        val buf = mutableListOf<Byte>()
        buf.writeVarint(3) // count=3
        buf.writeVarint(2) // arena size
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
    fun `datetime column (int svarint seconds to ISO)`() {
        val buf = mutableListOf<Byte>()
        buf.writeVarint(3) // count=3
        buf.writeVarint(0) // arena size
        buf.writeVarint(1) // fieldID=1
        buf.writeSvarint(0); buf.writeVarint(0)
        buf.writeSvarint(1748424000); buf.writeVarint(0)
        buf.writeSvarint(-1); buf.writeVarint(0)
        buf.add(0x00)

        val dec = ColumnarDecoder(buf.toByteArray())
        assertEquals(3, dec.count)
        assertTrue(dec.nextColumn())
        assertEquals(1, dec.fieldID)
        assertEquals(
            listOf("1970-01-01T00:00:00Z", "2025-05-28T09:20:00Z", "1969-12-31T23:59:59Z"),
            dec.readColumnDateTime(),
        )
        assertFalse(dec.nextColumn())
    }

    @Test
    fun `nullable datetime column`() {
        val buf = mutableListOf<Byte>()
        buf.writeVarint(3) // count=3
        buf.writeVarint(0) // arena size
        buf.writeVarint(1) // fieldID=1
        buf.add(0x00) // null
        buf.add(0x01); buf.writeSvarint(1748424000); buf.writeVarint(0) // present
        buf.add(0x00) // null
        buf.add(0x00) // end marker

        val dec = ColumnarDecoder(buf.toByteArray())
        assertEquals(3, dec.count)
        assertTrue(dec.nextColumn())
        assertEquals(1, dec.fieldID)
        assertEquals(listOf(null, "2025-05-28T09:20:00Z", null), dec.readColumnDateTimePtr())
        assertFalse(dec.nextColumn())
    }

    @Test
    fun `bool column`() {
        val buf = mutableListOf<Byte>()
        buf.writeVarint(3) // count=3
        buf.writeVarint(0) // arena size
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
    fun `uuid column (16 bytes each)`() {
        val u1 = UuidCodec.parse("550e8400-e29b-41d4-a716-446655440000")
        val u2 = UuidCodec.parse("00112233-4455-6677-8899-aabbccddeeff")
        val buf = mutableListOf<Byte>()
        buf.writeVarint(2) // count=2
        buf.writeVarint(0) // arena size
        buf.writeVarint(1) // fieldID=1
        u1.forEach { buf.add(it) }
        u2.forEach { buf.add(it) }
        buf.add(0x00)

        val dec = ColumnarDecoder(buf.toByteArray())
        assertEquals(2, dec.count)
        assertTrue(dec.nextColumn())
        assertEquals(1, dec.fieldID)
        assertEquals(
            listOf(
                "550e8400-e29b-41d4-a716-446655440000",
                "00112233-4455-6677-8899-aabbccddeeff",
            ),
            dec.readColumnUuid(),
        )
        assertFalse(dec.nextColumn())
    }

    @Test
    fun `nullable uuid column`() {
        val u = UuidCodec.parse("00112233-4455-6677-8899-aabbccddeeff")
        val buf = mutableListOf<Byte>()
        buf.writeVarint(2) // count=2
        buf.writeVarint(0) // arena size
        buf.writeVarint(1) // fieldID=1
        buf.add(0x00)               // record 0: null
        buf.add(0x01); u.forEach { buf.add(it) } // record 1: present
        buf.add(0x00)

        val dec = ColumnarDecoder(buf.toByteArray())
        assertTrue(dec.nextColumn())
        assertEquals(
            listOf(null, "00112233-4455-6677-8899-aabbccddeeff"),
            dec.readColumnUuidPtr(),
        )
        assertFalse(dec.nextColumn())
    }

    @Test
    fun `scalar array column (bytes cells holding inline arrays)`() {
        // Each cell is a length-prefixed blob containing [count][items...].
        // Build two cells: ["x","yy"] and [] (empty).
        fun cell(items: List<String>): ByteArray {
            val inner = mutableListOf<Byte>()
            inner.writeVarint(items.size.toLong())
            items.forEach { inner.writeString(it) }
            return inner.toByteArray()
        }
        val cell0 = cell(listOf("x", "yy"))
        val cell1 = cell(emptyList())

        val buf = mutableListOf<Byte>()
        buf.writeVarint(2) // count=2
        buf.writeVarint(0) // arena size
        buf.writeVarint(1) // fieldID=1
        // cell0 length-prefixed
        buf.writeVarint(cell0.size.toLong()); cell0.forEach { buf.add(it) }
        buf.writeVarint(cell1.size.toLong()); cell1.forEach { buf.add(it) }
        buf.add(0x00)

        val dec = ColumnarDecoder(buf.toByteArray())
        assertTrue(dec.nextColumn())
        val cells = dec.readColumnBytes()
        assertEquals(2, cells.size)

        // Decode each cell as an inline string array.
        val d0 = LuxoDecoder(cells[0])
        assertEquals(listOf("x", "yy"), d0.readArray { d0.readString() })
        val d1 = LuxoDecoder(cells[1])
        assertEquals(emptyList(), d1.readArray { d1.readString() })

        assertFalse(dec.nextColumn())
    }

    @Test
    fun `offset and readSvarint`() {
        val buf = mutableListOf<Byte>()
        buf.writeVarint(0) // count=0
        buf.writeVarint(0) // arena size
        buf.add(0x00) // end marker
        buf.writeSvarint(-42) // pagination metadata

        val dec = ColumnarDecoder(buf.toByteArray())
        assertFalse(dec.nextColumn())
        assertEquals(-42L, dec.readSvarint())
    }

    @Test
    fun `rejects non-canonical boolean and nullable markers`() {
        assertFailsWith<LuxoCodecException> {
            LuxoDecoder(byteArrayOf(0x02)).readBool()
        }
        assertFailsWith<LuxoCodecException> {
            LuxoDecoder(byteArrayOf(0x02)).readIntPtr()
        }
    }
}

class FieldMaskTest {

    @Test
    fun `set and has basic`() {
        var mask = ByteArray(0)
        mask = FieldMask.set(mask, 1)
        mask = FieldMask.set(mask, 8)
        mask = FieldMask.set(mask, 9)
        mask = FieldMask.set(mask, 16)

        assertTrue(FieldMask.has(mask, 1))
        assertTrue(FieldMask.has(mask, 8))
        assertTrue(FieldMask.has(mask, 9))
        assertTrue(FieldMask.has(mask, 16))
        assertFalse(FieldMask.has(mask, 0))
        assertFalse(FieldMask.has(mask, 2))
        assertFalse(FieldMask.has(mask, 10))
        assertFalse(FieldMask.has(mask, 17))
    }

    @Test
    fun `has returns false for out of range`() {
        val mask = ByteArray(1)
        assertFalse(FieldMask.has(mask, 100))
    }

    @Test
    fun `set grows mask array`() {
        var mask = ByteArray(1)
        mask = FieldMask.set(mask, 65)
        assertTrue(FieldMask.has(mask, 65))
        assertEquals(9, mask.size)
    }
}
