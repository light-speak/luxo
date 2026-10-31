package com.luxo.client

import java.math.BigDecimal
import java.math.BigInteger
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.long
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class TransportPrecisionTest {
    private val invalidIntegers = listOf(
        1.5, 1.0, Double.NaN, Double.POSITIVE_INFINITY, 1.5f,
        BigInteger("9223372036854775808"), BigDecimal("1.5"), "1", true,
    )

    @Test
    fun `binary Int and Duration never coerce arbitrary Numbers`() {
        for (type in listOf("Int", "Duration")) {
            for (value in invalidIntegers) {
                for (list in listOf(false, true)) {
                    val meta = APISchemaEntry(1, listOf(ParamSchema(1, "value", type, isList = list)))
                    val error = assertFailsWith<LuxoError>("$type, list=$list, value=$value") {
                        LuxoBinaryProtocol.encodeRequest(meta, mapOf("value" to if (list) listOf(value) else value))
                    }
                    assertEquals("ConfigError", error.error)
                }
            }
        }
    }

    @Test
    fun `binary integer input keeps all signed JVM widths exact`() {
        val meta = APISchemaEntry(1, listOf(ParamSchema(1, "value", "Int")))
        for (value in listOf<Number>(1.toByte(), 1.toShort(), 1, 1L, Long.MIN_VALUE, Long.MAX_VALUE)) {
            val request = LuxoDecoder(LuxoBinaryProtocol.encodeRequest(meta, mapOf("value" to value)))
            assertEquals(1L, request.readVarint())
            assertEquals(0L, request.readVarint())
            assertTrue(request.nextField())
            assertEquals(value.toLong(), request.readInt())
        }
    }

    @Test
    fun `binary error codes outside Int range are rejected instead of truncated`() {
        for (code in listOf(Int.MAX_VALUE.toLong() + 1, Int.MIN_VALUE.toLong() - 1, 4294967696L, Long.MAX_VALUE)) {
            val encoder = LuxoEncoder()
            encoder.writeFieldInt(1, code)
            encoder.writeFieldString(2, "Internal")
            encoder.writeFieldString(3, "failure")
            encoder.writeEnd()
            val error = LuxoBinaryProtocol.decodeError(encoder.bytes(), 500)
            assertEquals("ParseError", error.error)
            assertEquals(500, error.code)
        }
    }

    @Test
    fun `binary errors reject duplicate and aliased field identifiers`() {
        for (duplicate in 1..6) {
            val encoder = binaryErrorFields()
            if (duplicate == 1) encoder.writeFieldInt(duplicate, 401)
            else encoder.writeFieldString(duplicate, if (duplicate == 5) "null" else "duplicate")
            encoder.writeEnd()
            assertEquals("ParseError", LuxoBinaryProtocol.decodeError(encoder.bytes(), 502).error)
        }
        for (tag in listOf(7L, 4294967296L, 4294967297L, Long.MIN_VALUE, -1L)) {
            val encoder = binaryErrorFields()
            encoder.writeVarint(tag)
            if (tag != 4294967296L) { encoder.writeSvarint(401); encoder.writeEnd() }
            assertEquals("ParseError", LuxoBinaryProtocol.decodeError(encoder.bytes(), 502).error, "field=$tag")
        }
    }

    @Test
    fun `binary error strings and JSON require valid UTF-8`() {
        for (invalidField in listOf(2, 3, 4, 5, 6)) {
            val encoder = LuxoEncoder()
            for (field in 1..6) {
                if (field == invalidField) encoder.writeFieldBytes(field,
                    if (field == 5) byteArrayOf(34, -1, 34) else byteArrayOf(-1))
                else writeErrorField(encoder, field)
            }
            encoder.writeEnd()
            assertEquals("ParseError", LuxoBinaryProtocol.decodeError(encoder.bytes(), 502).error)
        }
    }

    @Test
    fun `error UTF-8 validation agrees with strict decoding including replacement characters`() {
        val random = kotlin.random.Random(8461)
        val samples = listOf("", "ascii", "中文", "\uFFFD", "中\uFFFD文", "\uD834\uDD1E")
            .map { it.toByteArray(Charsets.UTF_8) } + (0..255).map { byteArrayOf(it.toByte()) } +
            List(10000) { random.nextBytes(random.nextInt(1, 24)) }
        for (bytes in samples) {
            val expected = runCatching { bytes.decodeToString(throwOnInvalidSequence = true) }
            val encoder = LuxoEncoder().apply {
                writeFieldInt(1, 401)
                writeFieldBytes(2, bytes)
                writeFieldString(3, "message")
                writeEnd()
            }
            val error = LuxoBinaryProtocol.decodeError(encoder.bytes(), 502)
            if (expected.isSuccess) assertEquals(expected.getOrThrow(), error.error)
            else assertEquals("ParseError", error.error)
        }
    }

    @Test
    fun `binary error parsing preserves reordered fields and rejects every truncation`() {
        val encoder = LuxoEncoder()
        for (field in listOf(6, 3, 5, 1, 4, 2)) writeErrorField(encoder, field)
        encoder.writeEnd()
        val bytes = encoder.bytes()
        val error = LuxoBinaryProtocol.decodeError(bytes, 502)
        assertEquals("Unauthorized", error.error)
        assertEquals(401, error.code)
        assertEquals("trace", error.traceId)
        assertEquals("原因", error.developmentCause)
        for (length in 0 until bytes.size) {
            assertEquals("ParseError", LuxoBinaryProtocol.decodeError(bytes.copyOf(length), 502).error)
        }
        assertEquals("ParseError", LuxoBinaryProtocol.decodeError(bytes + byteArrayOf(0), 502).error)
    }

    private fun binaryErrorFields() = LuxoEncoder().also { encoder ->
        for (field in 1..6) writeErrorField(encoder, field)
    }

    private fun writeErrorField(encoder: LuxoEncoder, field: Int) {
        when (field) {
            1 -> encoder.writeFieldInt(1, 401)
            2 -> encoder.writeFieldString(2, "Unauthorized")
            3 -> encoder.writeFieldString(3, "expired")
            4 -> encoder.writeFieldString(4, "trace")
            5 -> encoder.writeFieldString(5, "{\"id\":9223372036854775807}")
            6 -> encoder.writeFieldString(6, "原因")
        }
    }

    @Test
    fun `JSON invalid integer params fail before any request is sent`() = runBlocking {
        val sent = AtomicInteger()
        withResponse("""{"data":0}""", sent) { client ->
            val meta = APISchemaEntry(1, listOf(ParamSchema(1, "value", "Int")))
            val transport = OkHttpTransport("http://localhost/luvia", client = client, schema = mapOf("echo" to meta))
            for (value in invalidIntegers) {
                val error = assertFailsWith<LuxoError> { transport.call("echo", mapOf("value" to value)) }
                assertEquals("ConfigError", error.error)
            }
            assertEquals(0, sent.get())
        }
    }

    @Test
    fun `JSON nested and recursive integer params reject lossy values`() = runBlocking {
        withResponse("""{"data":0}""") { client ->
            val fields = mapOf(
                "value" to SelectionFieldSchema(1, type = "Int", nullable = true),
                "children" to SelectionFieldSchema(2, "Input", type = "Model", isList = true),
            )
            val meta = APISchemaEntry(1, listOf(ParamSchema(1, "input", "Model", typeName = "Input")), types = mapOf("Input" to fields))
            val transport = OkHttpTransport("http://localhost/luvia", client = client, schema = mapOf("echo" to meta))
            val input = mapOf("value" to null, "children" to listOf(mapOf("value" to 1.5)))
            assertEquals("ConfigError", assertFailsWith<LuxoError> { transport.call("echo", mapOf("input" to input)) }.error)
            transport.call("echo", mapOf("input" to mapOf("children" to listOf(mapOf("value" to Long.MAX_VALUE)))))
        }
    }

    @Test
    fun `JSON Float and opaque JSON keep their own numeric semantics`() = runBlocking {
        withResponse("""{"data":9223372036854775807}""") { client ->
            val meta = APISchemaEntry(1, listOf(ParamSchema(1, "ratio", "Float"), ParamSchema(2, "raw", "JSON")))
            val transport = OkHttpTransport("http://localhost/luvia", client = client, schema = mapOf("echo" to meta))
            val response = transport.call("echo", mapOf("ratio" to 1.5, "raw" to BigInteger("9223372036854775808")))
            assertEquals(Long.MAX_VALUE, (response as JsonPrimitive).long)
        }
    }

    @Test
    fun `malformed JSON error codes produce Luxo parse errors`() = runBlocking {
        for (code in listOf("2147483648", "4294967696", "9223372036854775807", "1.5", "\"400\"", "null")) {
            withResponse("""{"error":"Internal","code":$code,"message":"failure"}""") { client ->
                val transport = OkHttpTransport("http://localhost/luvia", client = client)
                assertEquals("ParseError", assertFailsWith<LuxoError> { transport.call("echo") }.error)
            }
        }
    }

    private suspend fun withResponse(
        body: String,
        sent: AtomicInteger = AtomicInteger(),
        block: suspend (OkHttpClient) -> Unit,
    ) {
        val client = OkHttpClient.Builder().addInterceptor { chain ->
            sent.incrementAndGet()
            Response.Builder().request(chain.request()).protocol(Protocol.HTTP_1_1).code(200).message("fixture")
                .body(body.toResponseBody("application/json".toMediaType())).build()
        }.build()
        try {
            block(client)
        } finally {
            client.dispatcher.executorService.shutdownNow()
            client.connectionPool.evictAll()
        }
    }
}
