package com.luxo.client

import java.nio.ByteBuffer
import java.nio.charset.CharacterCodingException
import kotlin.random.Random
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

class CodecTextTest {
    private val invalid = listOf("80", "ff", "c080", "c1bf", "e08080", "eda080", "f0808080",
        "f4908080", "f5808080", "c2", "e282", "f09f98").map { it.hexToByteArray() }
    private val valid = listOf("", "plain", "发布检查 café", "\u0000", "\ufefftext", "\ufffd", "\udbff\udfff")

    @Test
    fun `row nullable array and column strings preserve valid Unicode and exact boundaries`() {
        for (value in valid) {
            val bytes = value.encodeToByteArray()
            for (read in readers(bytes)) assertEquals(value, read())
            val row = LuxoDecoder(frame(bytes) + byteArrayOf(1))
            assertEquals(value, row.readString())
            assertEquals(true, row.readBool())
            row.checkComplete()
        }
    }

    @Test
    fun `all string readers reject malformed UTF8 instead of returning replacement text`() {
        for (bytes in invalid) {
            for (read in readers(bytes)) assertFailsWith<LuxoCodecException>(bytes.toHexString()) { read() }
        }
    }

    @Test
    fun `invalid array text never returns a partial list or decodes the next value`() {
        val decoder = LuxoDecoder(byteArrayOf(3) + frame("ok".encodeToByteArray()) + frame(byteArrayOf(-1)) + frame(byteArrayOf(65)))
        var reads = 0
        assertFailsWith<LuxoCodecException> { decoder.readArray { reads++; decoder.readString() } }
        assertEquals(2, reads)
    }

    @Test
    fun `raw bytes are not constrained by text encoding`() {
        for (bytes in invalid) {
            assertContentEquals(bytes, LuxoDecoder(frame(bytes)).readBytes())
            assertContentEquals(bytes, column(bytes).readColumnBytes().single())
        }
    }

    @Test
    fun `string length truncation cannot borrow bytes beyond the payload`() {
        for (value in valid.filter { it.isNotEmpty() }) {
            val wire = frame(value.encodeToByteArray())
            for (end in wire.indices) {
                assertFailsWith<LuxoCodecException> { LuxoDecoder(wire.copyOf(end)).readString() }
            }
        }
    }

    @Test
    fun `optimized string reading agrees with a strict platform decoder on 4352 byte sequences`() {
        val random = Random(741)
        val oracle = Charsets.UTF_8.newDecoder()
        for (index in 0 until 4352) {
            val bytes = if (index < 256) byteArrayOf(index.toByte()) else random.nextBytes(index % 17)
            val expected = try { oracle.reset().decode(ByteBuffer.wrap(bytes)).toString() }
                catch (_: CharacterCodingException) { null }
            val decoder = LuxoDecoder(frame(bytes))
            if (expected == null) assertFailsWith<LuxoCodecException> { decoder.readString() }
            else { assertEquals(expected, decoder.readString()); decoder.checkComplete() }
        }
    }

    private fun readers(bytes: ByteArray): List<() -> String?> {
        val field = frame(bytes)
        return listOf(
            { LuxoDecoder(field).readString() },
            { LuxoDecoder(byteArrayOf(1) + field).readStringPtr() },
            { LuxoDecoder(byteArrayOf(1) + field).let { it.readArray { it.readString() }.single() } },
            { column(bytes).readColumnString().single() },
            { column(bytes, true).readColumnStringPtr().single() },
        )
    }

    private fun column(bytes: ByteArray, nullable: Boolean = false): ColumnarDecoder {
        val present = if (nullable) byteArrayOf(1) else byteArrayOf()
        val decoder = ColumnarDecoder(byteArrayOf(1, 0, 1) + present + frame(bytes) + byteArrayOf(0))
        assertEquals(true, decoder.nextColumn())
        return decoder
    }

    private fun frame(bytes: ByteArray): ByteArray {
        require(bytes.size < 128)
        return byteArrayOf(bytes.size.toByte()) + bytes
    }
}
