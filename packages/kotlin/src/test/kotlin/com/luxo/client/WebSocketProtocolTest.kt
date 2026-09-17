package com.luxo.client

import kotlinx.serialization.json.*
import kotlinx.serialization.ExperimentalSerializationApi
import java.math.BigInteger
import java.util.concurrent.Callable
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicLong
import kotlin.random.Random
import kotlin.test.*

class WebSocketProtocolTest {
    @Test
    fun `JSON metadata covers the full uint64 range without signed conversion`() {
        val values = listOf(0uL, 1uL, 127uL, 128uL, 9007199254740993uL, Long.MAX_VALUE.toULong(), 1uL shl 63, 9999999999999999999uL, 10000000000000000000uL, ULong.MAX_VALUE)
        for (value in values) {
            val encoded = webSocketJSONID(value.toLong())
            assertFalse(encoded.isString)
            assertEquals(value.toString(), encoded.toString())
            assertEquals(value.toLong(), webSocketJSONRequestID(Json.parseToJsonElement(encoded.toString())))
        }
    }

    @OptIn(ExperimentalSerializationApi::class)
    @Test
    fun `noncanonical ID representations are rejected`() {
        val invalid = listOf("null", "true", "false", "-0", "-1", "+1", "01", "1.0", "1e0", "1E0", "18446744073709551616", "99999999999999999999999", "1١", "١", "[]", "{}", "\"1\"", "\"\"")
        assertNull(webSocketJSONRequestID(null))
        assertNull(webSocketJSONRequestID(JsonUnquotedLiteral("")))
        for (token in invalid) {
            val element = runCatching { Json.parseToJsonElement(token) }.getOrNull()
            assertNull(webSocketJSONRequestID(element), token)
        }
    }

    @Test
    fun `unsigned JSON and binary match an independent BigInteger reference`() {
        val random = Random(314159)
        repeat(10_000) {
            val bits = random.nextLong()
            val reference = BigInteger(java.lang.Long.toUnsignedString(bits))
            val encoder = LuxoEncoder()
            encoder.writeVarint(bits)
            var remaining = reference
            val expected = mutableListOf<Byte>()
            do {
                var byte = remaining.and(BigInteger.valueOf(127)).toInt()
                remaining = remaining.shiftRight(7)
                if (remaining != BigInteger.ZERO) byte = byte or 128
                expected += byte.toByte()
            } while (remaining != BigInteger.ZERO)
            assertContentEquals(expected.toByteArray(), encoder.bytes())
            assertEquals(bits, LuxoDecoder(encoder.bytes()).readVarint())
            assertEquals(reference.toString(), webSocketJSONID(bits).toString())
            assertEquals(bits, webSocketJSONRequestID(Json.parseToJsonElement(reference.toString())))
        }
    }

    @OptIn(ExperimentalSerializationApi::class)
    @Test
    fun `decimal validation agrees with an independent canonical uint64 reference`() {
        val random = Random(271828)
        val maximum = BigInteger.ONE.shiftLeft(64) - BigInteger.ONE
        val alphabet = "0123456789+-eE.١ "
        repeat(10_000) {
            val token = buildString { repeat(random.nextInt(0, 24)) { append(alphabet[random.nextInt(alphabet.length)]) } }
            val reference = if (token.isNotEmpty() && token.all { it in '0'..'9' } && (token.length == 1 || token[0] != '0')) {
                BigInteger(token).takeIf { it <= maximum }?.toLong()
            } else null
            assertEquals(reference, webSocketJSONRequestID(JsonUnquotedLiteral(token)), token)
        }
    }

    @Test
    fun `counter crosses signed boundaries but never wraps unsigned maximum`() {
        val counter = AtomicLong(Long.MAX_VALUE - 1)
        assertEquals(Long.MAX_VALUE, nextWebSocketRequestID(counter))
        assertEquals(Long.MIN_VALUE, nextWebSocketRequestID(counter))
        assertEquals(Long.MIN_VALUE + 1, nextWebSocketRequestID(counter))
        counter.set(-2)
        assertEquals(-1, nextWebSocketRequestID(counter))
        repeat(2) { assertEquals("RequestIDExhausted", assertFailsWith<LuxoError> { nextWebSocketRequestID(counter) }.error) }
        assertEquals(-1, counter.get())
    }

    @Test
    fun `concurrent counter allocation is unique including the final ID`() {
        val pool = Executors.newFixedThreadPool(8)
        try {
            val counter = AtomicLong(Long.MAX_VALUE - 4_000)
            val allocations = pool.invokeAll(List(8) { Callable { List(1_000) { nextWebSocketRequestID(counter) } } })
            val ids = allocations.flatMap { it.get() }
            assertEquals(8_000, ids.toSet().size)
            assertEquals(Long.MIN_VALUE + 3_999, counter.get())
            counter.set(-2)
            val finalIDs = pool.invokeAll(List(8) { Callable {
                try { nextWebSocketRequestID(counter) }
                catch (error: LuxoError) { assertEquals("RequestIDExhausted", error.error); null }
            } }).mapNotNull { it.get() }
            assertEquals(listOf(-1L), finalIDs)
            assertEquals(-1, counter.get())
        } finally {
            pool.shutdownNow()
        }
    }
}
