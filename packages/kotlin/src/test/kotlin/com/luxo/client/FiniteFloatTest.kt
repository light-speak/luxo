package com.luxo.client

import java.nio.ByteBuffer
import java.nio.ByteOrder
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

class FiniteFloatTest {
    private fun wire(value: Double): ByteArray = ByteBuffer.allocate(8).order(ByteOrder.LITTLE_ENDIAN).putDouble(value).array()

    @Test
    fun `non-finite Float is rejected on both sides`() {
        for (value in listOf(Double.NaN, Double.POSITIVE_INFINITY, Double.NEGATIVE_INFINITY)) {
            assertFailsWith<LuxoCodecException> { LuxoEncoder().writeFixed64(value) }
            assertFailsWith<LuxoCodecException> { LuxoDecoder(wire(value)).readFloat() }
            for (nullable in listOf(false, true)) {
                val column = ColumnarDecoder(byteArrayOf(1, 0, 1) + (if (nullable) byteArrayOf(1) else byteArrayOf()) + wire(value) + byteArrayOf(0))
                column.nextColumn()
                assertFailsWith<LuxoCodecException> {
                    if (nullable) column.readColumnFloatPtr() else column.readColumnFloat()
                }
            }
        }
    }

    @Test
    fun `finite edge bits survive round trip`() {
        for (value in listOf(0.0, -0.0, Double.MIN_VALUE, -Double.MIN_VALUE, Double.MAX_VALUE, -Double.MAX_VALUE)) {
            val encoder = LuxoEncoder()
            encoder.writeFixed64(value)
            assertContentEquals(wire(value), encoder.bytes())
            assertEquals(value.toRawBits(), LuxoDecoder(encoder.bytes()).readFloat().toRawBits())
        }
    }
}
