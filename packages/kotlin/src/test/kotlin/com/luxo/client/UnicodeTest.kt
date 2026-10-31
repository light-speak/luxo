package com.luxo.client

import java.io.File
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.*
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import kotlin.test.*

class UnicodeTest {
    private val malformed = listOf("\uD800", "\uDBFF", "\uDC00", "\uDFFF", "\uD800x", "\uDC00\uD800")
    private val valid = listOf("", "ascii", "東京", "\uD800\uDC00", "\uDBFF\uDFFF", "\uFFFD",
        "\u0000\uFEFF", "\uD7FF\uE000\uFFFF", "\\uD800", "e\u0301")

    @Test
    fun `every BMP code unit agrees with strict platform encoding`() {
        for (code in 0..0xffff) {
            val text = code.toChar().toString()
            val expected = runCatching { text.encodeToByteArray(throwOnInvalidSequence = true) }.isSuccess
            assertEquals(expected, isLuxoUnicode(text), "code unit $code")
        }
        for (offset in listOf(0, 1, 15, 16, 31, 32, 63, 64, 255, 256, 1023, 1024)) {
            val prefix = "文".repeat(offset)
            for (text in malformed + valid) {
                val value = prefix + text + prefix
                val expected = runCatching { value.encodeToByteArray(throwOnInvalidSequence = true) }.isSuccess
                assertEquals(expected, isLuxoUnicode(value), "offset $offset")
            }
        }
    }

    @Test
    fun `shared corpus rejects invalid characters before duplicate keys discard them`() {
        val corpus = Json.parseToJsonElement(File("../../pkg/lux/codec/testdata/protocol-v1.json").readText())
            .jsonObject.getValue("jsonUnicodeCases").jsonArray
        assertEquals(23, corpus.size)
        for (item in corpus) {
            val row = item.jsonObject
            val raw = row.getValue("raw").jsonPrimitive.content
            val bytes = "{\"data\":$raw}".encodeToByteArray()
            if (row.getValue("valid").jsonPrimitive.boolean) {
                assertEquals(Json.parseToJsonElement(raw), decodeHTTPJSON(bytes)["data"], row.toString())
            } else assertFails(row.toString()) { decodeHTTPJSON(bytes) }
        }
    }

    @Test
    fun `typed strings and error values reject incomplete host characters`() {
        val error = buildJsonObject { put("error", "Rejected"); put("code", 400); put("message", "no") }
        for (text in malformed) {
            assertEquals("ParseError", assertFailsWith<LuxoError> { decodeLuxoJsonString(JsonPrimitive(text)) }.error)
            for (name in listOf("error", "message", "traceId", "cause", "data")) {
                assertEquals("ParseError", decodeLuxoJsonError(JsonObject(error + (name to JsonPrimitive(text))), 502)?.error)
            }
            val data = buildJsonObject { put(text, JsonArray(listOf(JsonPrimitive(text)))) }
            assertEquals("ParseError", decodeLuxoJsonError(JsonObject(error + ("data" to data)), 502)?.error)
        }
    }

    @Test
    fun `Binary writes and arena sizes reject bad strings without partial output`() {
        for (text in malformed) {
            assertFails { luxoUtf8Length(text) }
            val writes = listOf<(LuxoEncoder) -> Unit>(
                { it.writeString(text) }, { it.writeFieldString(2, text) },
                { encoder -> encoder.writeFieldArray(3, listOf("valid", text), encoder::writeString) },
            )
            for (write in writes) {
                val encoder = LuxoEncoder().apply { writeFieldInt(1, 42) }
                val prefix = encoder.bytes()
                assertFails { write(encoder) }
                assertContentEquals(prefix, encoder.bytes())
                encoder.writeString("next")
                assertTrue(encoder.bytes().size > prefix.size)
            }
        }
    }

    @Test
    fun `array callbacks preserve their original failure and allow encoder reuse`() {
        val encoder = LuxoEncoder(8).apply { writeFieldInt(1, 42) }
        val prefix = encoder.bytes()
        val failure = IllegalStateException("callback failed after growing the buffer")
        assertSame(failure, assertFailsWith<IllegalStateException> {
            encoder.writeFieldArray(2, listOf(1L, 2L)) {
                encoder.writeString("valid".repeat(50))
                if (it == 2L) throw failure
            }
        })
        assertContentEquals(prefix, encoder.bytes())
        encoder.writeFieldArray(2, listOf(3L), encoder::writeSvarint)
        val expected = LuxoEncoder().apply {
            writeFieldInt(1, 42); writeFieldArray(2, listOf(3L), ::writeSvarint)
        }
        assertContentEquals(expected.bytes(), encoder.bytes())
    }

    @Test
    fun `array writes preserve sequential list traversal and rollback`() {
        val values = listOf("東京", "\uD800\uDC00", "valid")
        val sequential = object : java.util.LinkedList<String>(values) {
            override fun get(index: Int): String = error("must not index a sequential list")
        }
        val indexed = LuxoEncoder().apply { writeFieldArray(1, values, ::writeString) }.bytes()
        val encoder = LuxoEncoder().apply { writeFieldArray(1, sequential, ::writeString) }
        assertContentEquals(indexed, encoder.bytes())
        sequential.add("\uD800")
        assertFails { encoder.writeFieldArray(2, sequential, encoder::writeString) }
        assertContentEquals(indexed, encoder.bytes())
    }

    @Test
    fun `valid characters and opaque Bytes retain their original meaning`() {
        for (text in valid) {
            assertSame(text, decodeLuxoJsonString(JsonPrimitive(text)))
            val encoder = LuxoEncoder().apply { writeString(text) }
            assertEquals(text, LuxoDecoder(encoder.bytes()).readString())
            assertEquals(text.encodeToByteArray().size, luxoUtf8Length(text))
            val value = buildJsonObject { put(text, text) }
            assertEquals(value, decodeHTTPJSON(value.toString().encodeToByteArray()))
            assertSame(value, decodeLuxoJsonValue(value))
            assertEquals(value, decodeLuxoJSON(encodeLuxoJSON(value)))
        }
        val bytes = byteArrayOf(-19, -96, -128, -1)
        assertContentEquals(bytes, LuxoDecoder(LuxoEncoder().apply { writeBytes(bytes) }.bytes()).readBytes())
    }

    @Test
    fun `outgoing JSON and Binary never send malformed string or opaque JSON parameters`() = runBlocking {
        var sent = 0
        val client = OkHttpClient.Builder().addInterceptor { chain ->
            sent++
            Response.Builder().request(chain.request()).protocol(Protocol.HTTP_1_1).code(200).message("OK")
                .body("{\"data\":true}".toResponseBody()).build()
        }.build()
        try {
            for (mode in TransportMode.entries) for (type in listOf("String", "JSON")) {
                val schema = mapOf("echo" to APISchemaEntry(1, listOf(ParamSchema(1, "value", type))))
                val transport = OkHttpTransport("http://localhost/luvia", client = client, mode = mode, schema = schema)
                for (text in malformed) {
                    val value: Any = if (type == "String") text else buildJsonObject { put(text, text) }
                    assertFails { transport.call("echo", mapOf("value" to value)) }
                    assertEquals(0, sent)
                    if (mode == TransportMode.JSON) {
                        assertFails { transport.call(text) }
                        assertFails { transport.call("echo", mapOf(text to 1)) }
                        assertEquals(0, sent)
                    }
                }
            }
        } finally {
            client.dispatcher.executorService.shutdownNow()
            client.connectionPool.evictAll()
        }
    }

    @Test
    fun `Binary error JSON rejects escaped and overwritten bad characters`() {
        for (text in listOf("{\"value\":\"\\ud800\"}", "{\"value\":\"\\ud800\",\"value\":\"valid\"}")) {
            val encoder = LuxoEncoder().apply {
                writeFieldInt(1, 400); writeFieldString(2, "Rejected"); writeFieldString(3, "no")
                writeFieldString(5, text); writeEnd()
            }
            assertEquals("ParseError", LuxoBinaryProtocol.decodeError(encoder.bytes(), 502).error)
        }
    }

    @Test
    fun `direct JSON boundaries reject literal mixed and raw escaped bad text`() {
        for (text in malformed) {
            for (value in listOf(JsonPrimitive(text), buildJsonObject { put(text, text) }, JsonArray(listOf(JsonPrimitive(text))))) {
                assertFails { encodeLuxoJSON(value) }
                assertFails { decodeLuxoJsonValue(value) }
            }
            assertFails { decodeLuxoJSON("\"$text\"") }
        }
        for (text in listOf("\"\\uD800\uDC00\"", "\"\uD800\\uDC00\"", "\"\\uD800\\uDC00\\uD800\"")) {
            assertFails { decodeLuxoJSON(text) }
        }
        for (text in listOf("\"\\uD\"", "\"\\uD8\"", "\"\\uD8xx\\uDC00\"", "\"\\")) {
            assertFails { decodeLuxoJSON(text) }
        }
        var deep: JsonElement = JsonNull
        repeat(130) { deep = JsonArray(listOf(deep)) }
        assertFails { decodeLuxoJsonValue(deep) }
    }

    @OptIn(kotlinx.serialization.ExperimentalSerializationApi::class)
    @Test
    fun `raw JSON literals retain numeric text but cannot hide incomplete characters`() {
        for (text in listOf("123456789012345678901234567890", "1e-9999", "0.123456789012345678901234567890")) {
            val value = JsonUnquotedLiteral(text)
            assertEquals(text, encodeLuxoJSON(value))
            assertSame(value, decodeLuxoJsonValue(value))
        }
        for (text in listOf("\"\\uD800\"", "{\"\\uDC00\":1}", "\"\uD800\"")) {
            assertFails { encodeLuxoJSON(JsonUnquotedLiteral(text)) }
            assertFails { decodeLuxoJsonValue(JsonUnquotedLiteral(text)) }
        }
    }

    @Test
    fun `deterministic mixtures agree with platform JSON parsing and strict UTF8 encoding`() {
        val fragments = listOf("a", "\\\\", "\\\"", "\\n", "\\u0000", "\\uD800", "\\uDC00", "\\udbff",
            "\\udfff", "\\u0061", "\\uD7FF", "\\uE000", "東京", "\uD800", "\uDC00", "\uD800\uDC00")
        var state = 0x51a7c0deL
        repeat(4096) { sample ->
            val token = buildString {
                append('"')
                repeat(1 + sample % 8) {
                    state = (state * 1664525 + 1013904223) and 0xffffffffL
                    append(fragments[(state ushr 28).toInt()])
                }
                append('"')
            }
            val valid = runCatching {
                token.encodeToByteArray(throwOnInvalidSequence = true)
                Json.parseToJsonElement(token).jsonPrimitive.content.encodeToByteArray(throwOnInvalidSequence = true)
            }.isSuccess
            for (raw in listOf(token, "{$token:1}", "{\"value\":$token,\"value\":\"valid\"}")) {
                assertEquals(valid, runCatching { decodeLuxoJSON(raw) }.isSuccess, raw)
            }
        }
    }
}
