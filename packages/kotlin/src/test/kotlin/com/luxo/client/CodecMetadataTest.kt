package com.luxo.client

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class CodecMetadataTest {
    @Test
    fun `nonempty lists require column payload before allocation`() {
        for (count in listOf(1L, 128L, 10_000_000L)) {
            for (payload in listOf(
                byteArrayOf(), byteArrayOf(0), byteArrayOf(1, 0), byteArrayOf(0, 1, 2, 3, 4),
                byteArrayOf(0x80.toByte(), 0, 1, 2), byteArrayOf(0x80.toByte(), 0x80.toByte(), 0, 1),
                byteArrayOf(0x80.toByte(), 1, 0),
            )) {
                assertFailsWith<LuxoCodecException> { ColumnarDecoder(metadata(count, 0) + payload) }
            }
        }
    }

    @Test
    fun `empty lists and nullable columns with multibyte field IDs remain valid`() {
        for (count in listOf(0, 1, 2)) {
            for (id in if (count == 0) listOf(0) else listOf(1, 128, 16384)) {
                val payload = if (count == 0) byteArrayOf(0) else metadata(id.toLong()) + ByteArray(count) + byteArrayOf(0)
                val decoder = ColumnarDecoder(metadata(count.toLong(), 0) + payload)
                if (count > 0) {
                    assertTrue(decoder.nextColumn())
                    assertEquals(id, decoder.fieldID)
                    assertEquals(List<Long?>(count) { null }, decoder.readColumnIntPtr())
                }
                assertFalse(decoder.nextColumn())
            }
        }
    }

    private val oversized = listOf(4294967296L, 4294967297L, 2147483648L, Long.MAX_VALUE, Long.MIN_VALUE, -1L)

    @Test
    fun `field IDs cannot alias a valid field or the end marker`() {
        for (id in oversized) {
            val wire = metadata(id)
            assertFailsWith<LuxoCodecException>("row field=$id") { LuxoDecoder(wire).nextField() }
            assertFailsWith<LuxoCodecException>("column field=$id") {
                ColumnarDecoder(byteArrayOf(0, 0) + wire).nextColumn()
            }
        }
    }

    @Test
    fun `invalid metadata reports its original unsigned wire value`() {
        val error = assertFailsWith<LuxoCodecException> { LuxoDecoder(metadata(-1L)).nextField() }
        assertTrue(error.message!!.contains("18446744073709551615"))
    }

    @Test
    fun `all representable field ID boundaries remain exact`() {
        for (id in listOf(0, 1, 127, 128, 16384, Int.MAX_VALUE)) {
            val wire = metadata(id.toLong())
            val row = LuxoDecoder(wire)
            val column = ColumnarDecoder(byteArrayOf(0, 0) + wire)
            assertEquals(id != 0, row.nextField())
            assertEquals(id, row.fieldID)
            assertEquals(id != 0, column.nextColumn())
            assertEquals(id, column.fieldID)
            assertFailsWith<LuxoCodecException> { row.nextField() }
            assertFailsWith<LuxoCodecException> { column.nextColumn() }
        }
    }

    @Test
    fun `array counts fail before invoking the element decoder`() {
        for (count in oversized + listOf(1_000_001L, 1L)) {
            var calls = 0
            assertFailsWith<LuxoCodecException>("array count=$count") {
                LuxoDecoder(metadata(count)).readArray { calls++; Unit }
            }
            assertEquals(0, calls)
        }
    }

    @Test
    fun `array limits include zero and the maximum supported size`() {
        for (count in listOf(0, 1, 128, 1_000_000)) {
            val decoder = LuxoDecoder(metadata(count.toLong()) + ByteArray(count))
            val result = decoder.readArray { decoder.readBool() }
            assertEquals(count, result.size)
            assertTrue(result.all { !it })
            assertEquals(0, decoder.remaining())
        }
    }

    @Test
    fun `column counts obey the Go reader limit before narrowing`() {
        for (count in oversized + 10_000_001L) {
            assertFailsWith<LuxoCodecException>("column count=$count") { ColumnarDecoder(metadata(count, 0)) }
        }
        for (count in listOf(0, 1, 10_000_000)) {
            val payload = if (count == 0) byteArrayOf(0) else byteArrayOf(1) + ByteArray(count + 1)
            assertEquals(count, ColumnarDecoder(metadata(count.toLong(), 0) + payload).count)
        }
    }

    @Test
    fun `arena headers obey the Go allocation limit before narrowing`() {
        for (size in oversized + 67_108_865L) {
            assertFailsWith<LuxoCodecException>("row arena=$size") { LuxoDecoder(metadata(size)).skipArenaHeader() }
            assertFailsWith<LuxoCodecException>("column arena=$size") { ColumnarDecoder(metadata(0, size)) }
        }
        for (size in listOf(0, 1, 67_108_864)) {
            val row = LuxoDecoder(metadata(size.toLong()))
            row.skipArenaHeader()
            assertEquals(0, row.remaining())
            assertEquals(size, ColumnarDecoder(metadata(0, size.toLong())).arenaSize)
        }
    }

    @Test
    fun `column byte lengths cannot wrap or consume following fields`() {
        for (nullable in listOf(false, true)) {
            for (size in oversized + 2L) {
                val wire = metadata(1, 0, 1) + (if (nullable) byteArrayOf(1) else byteArrayOf()) + metadata(size) + byteArrayOf(65)
                for (bytes in listOf(false, true)) {
                    val decoder = ColumnarDecoder(wire)
                    assertTrue(decoder.nextColumn())
                    assertFailsWith<LuxoCodecException>("size=$size, nullable=$nullable, bytes=$bytes") {
                        if (bytes) {
                            if (nullable) decoder.readColumnBytesPtr() else decoder.readColumnBytes()
                        } else {
                            if (nullable) decoder.readColumnStringPtr() else decoder.readColumnString()
                        }
                    }
                }
            }
        }
    }

    @Test
    fun `empty and present column cells keep the exact cursor`() {
        val wire = metadata(3, 0, 1, 0, 1) + byteArrayOf(65) + metadata(0, 0)
        val strings = ColumnarDecoder(wire)
        assertTrue(strings.nextColumn())
        assertEquals(listOf("", "A", ""), strings.readColumnString())
        assertFalse(strings.nextColumn())
        assertEquals(wire.size, strings.offset())
        val bytes = ColumnarDecoder(wire)
        assertTrue(bytes.nextColumn())
        assertEquals(listOf(emptyList(), listOf(65.toByte()), emptyList()), bytes.readColumnBytes().map { it.toList() })
        assertFalse(bytes.nextColumn())
        assertEquals(wire.size, bytes.offset())
    }

    @Test
    fun `truncated column payloads are rejected across every scalar type`() {
        for (column in columns) {
            assertFailsWith<LuxoCodecException>(column.name) {
                val decoder = ColumnarDecoder(metadata(2, 0, 1) + ByteArray(column.width * 2 - 1))
                assertTrue(decoder.nextColumn())
                column.read(decoder)
            }
        }
    }

    @Test
    fun `empty columns consume no values across every scalar type`() {
        for (column in columns) {
            val decoder = ColumnarDecoder(metadata(0, 0, 1, 0))
            assertTrue(decoder.nextColumn())
            column.read(decoder)
            assertEquals(3, decoder.offset(), column.name)
            assertFalse(decoder.nextColumn())
        }
    }

    @Test
    fun `metadata varints must terminate within uint64`() {
        val invalid = listOf(byteArrayOf(0x80.toByte()), ByteArray(10) { 0xff.toByte() },
            ByteArray(9) { 0xff.toByte() } + byteArrayOf(2))
        for (wire in invalid) {
            assertFailsWith<LuxoCodecException> { LuxoDecoder(wire).nextField() }
            assertFailsWith<LuxoCodecException> { LuxoDecoder(wire).skipArenaHeader() }
            assertFailsWith<LuxoCodecException> { LuxoDecoder(wire).readArray { Unit } }
            assertFailsWith<LuxoCodecException> { ColumnarDecoder(wire) }
            assertFailsWith<LuxoCodecException> { ColumnarDecoder(byteArrayOf(0) + wire) }
            assertFailsWith<LuxoCodecException> { ColumnarDecoder(byteArrayOf(0, 0) + wire).nextColumn() }
        }
    }

    private fun metadata(vararg values: Long): ByteArray = LuxoEncoder().apply {
        for (value in values) writeVarint(value)
    }.bytes()

    private data class Column(val name: String, val width: Int, val read: (ColumnarDecoder) -> Unit)

    private val columns = listOf(
        Column("Int", 1) { it.readColumnInt() }, Column("Int?", 1) { it.readColumnIntPtr() },
        Column("Float", 8) { it.readColumnFloat() }, Column("Float?", 1) { it.readColumnFloatPtr() },
        Column("String", 1) { it.readColumnString() }, Column("String?", 1) { it.readColumnStringPtr() },
        Column("Boolean", 1) { it.readColumnBool() }, Column("Boolean?", 1) { it.readColumnBoolPtr() },
        Column("UUID", 16) { it.readColumnUuid() }, Column("UUID?", 1) { it.readColumnUuidPtr() },
        Column("DateTime", 1) { it.readColumnDateTime() }, Column("DateTime?", 1) { it.readColumnDateTimePtr() },
        Column("Bytes", 1) { it.readColumnBytes() }, Column("Bytes?", 1) { it.readColumnBytesPtr() },
    )
}
