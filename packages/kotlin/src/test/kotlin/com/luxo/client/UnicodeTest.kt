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
    @OptIn(kotlinx.serialization.ExperimentalSerializationApi::class)
    @Test
    fun `JSON writers retain parsed numbers and complete raw fragments exactly`() {
        for (raw in listOf("-0", "1.00", "-1.2300", "1e+03", "1e-003", "1E9", "1e400", "1e-9999",
            "9223372036854775807", "-9223372036854775808", "18446744073709551616", "9007199254740993",
            "0.123456789012345678901234567890", "true", "false", "null", "\"raw text\"", "[]", "{}")) {
            val values = if (raw == "null") listOf(JsonNull) else listOf(decodeLuxoJSON(raw), JsonUnquotedLiteral(raw))
            for (value in values) {
                for (encode in listOf(::encodeLuxoJSON, ::encodeBoundedLuxoJSON)) {
                    assertEquals(raw, encode(value), raw)
                    assertEquals("{\"value\":$raw}", encode(buildJsonObject { put("value", value) }), raw)
                    assertEquals("[$raw]", encode(JsonArray(listOf(value))), raw)
                }
            }
        }
    }

    @Test
    fun `JSON writers preserve every BMP string escape and supplementary boundaries`() {
        for (code in 0..65535) {
            if (code in 0xd800..0xdfff) continue
            val value = JsonPrimitive(code.toChar().toString())
            // JsonElement text is the existing wire compatibility oracle.
            val expected = value.toString()
            assertEquals(expected, encodeLuxoJSON(value), "code=$code")
            assertEquals(expected, encodeBoundedLuxoJSON(value), "code=$code")
        }
        for (code in listOf(0x10000, 0x20000, 0x10ffff)) {
            val text = String(Character.toChars(code))
            val value = buildJsonObject { put(text, buildJsonArray { add(text); add(true); add(JsonNull) }) }
            assertEquals(value.toString(), encodeLuxoJSON(value))
            assertEquals(value.toString(), encodeBoundedLuxoJSON(value))
        }
    }

    @OptIn(kotlinx.serialization.ExperimentalSerializationApi::class)
    @Test
    fun `numeric siblings never bypass later raw Unicode or container checks`() {
        for (raw in listOf("", "01", "+1", "1e", "1 2", "1,\"injected\":true", "true,false", "NaN",
            "Infinity", "{\"v\":\"\\ud800\"}", "[0", "{}[]")) {
            val bad = JsonUnquotedLiteral(raw)
            for (encode in listOf(::encodeLuxoJSON, ::encodeBoundedLuxoJSON)) {
                for (value in listOf(bad, JsonArray(listOf(JsonPrimitive(0), bad)),
                    buildJsonObject { put("first", 0); put("bad", bad) })) {
                    assertFails("raw=$raw") { encode(value) }
                }
            }
        }
        for (text in listOf("\uD800", "\uDC00", "valid\uD800tail")) {
            for (encode in listOf(::encodeLuxoJSON, ::encodeBoundedLuxoJSON)) {
                assertFails { encode(JsonPrimitive(text)) }
                assertFails { encode(buildJsonObject { put("first", 0); put(text, text) }) }
                assertFails { encode(JsonArray(listOf(JsonPrimitive(0), JsonPrimitive(text)))) }
            }
        }
        for (objects in listOf(false, true)) for (depth in 127..129) {
            var nested: JsonElement = JsonNull
            repeat(depth - 1) { nested = if (objects) buildJsonObject { put("v", nested) } else JsonArray(listOf(nested)) }
            val value = if (objects) buildJsonObject { put("first", 0); put("nested", nested) }
                else JsonArray(listOf(JsonPrimitive(0), nested))
            if (depth <= 128) assertEquals(value.toString(), encodeBoundedLuxoJSON(value))
            else assertFailsWith<IllegalArgumentException> { encodeBoundedLuxoJSON(value) }
        }
    }

    @Test
    fun `generic JSON counts empty containers but not scalar leaves`() {
        for (objects in listOf(false, true)) for (depth in 127..129) {
            for (leaf in listOf("0", "[]", "{}", "1e400")) {
                val containers = depth - if (leaf == "[]" || leaf == "{}") 1 else 0
                val raw = (if (objects) "{\"v\":" else "[").repeat(containers) + leaf +
                    (if (objects) "}" else "]").repeat(containers)
                val value = Json.parseToJsonElement(raw)
                if (depth <= 128) assertSame(value, decodeLuxoJsonValue(value))
                else assertFailsWith<IllegalArgumentException>("depth=$depth leaf=$leaf") {
                    decodeLuxoJsonValue(value)
                }
            }
        }
    }

    @Test
    fun `binary error JSON rejects deep source even when duplicate keys overwrite it`() {
        for (depth in 128..129) {
            val nested = "[".repeat(depth - 1) + "0" + "]".repeat(depth - 1)
            val raw = "{\"v\":$nested,\"v\":0}"
            val encoder = LuxoEncoder().apply {
                writeFieldInt(1, 400); writeFieldString(2, "Rejected"); writeFieldString(3, "no")
                writeFieldString(5, raw); writeEnd()
            }
            assertEquals(if (depth == 128) "Rejected" else "ParseError",
                LuxoBinaryProtocol.decodeError(encoder.bytes(), 502).error)
        }
    }

    @Test
    fun `binary JSON parameters have an independent container budget per list item`() {
        for (list in listOf(false, true)) for (depth in 128..129) {
            val value = Json.parseToJsonElement("[".repeat(depth) + "0" + "]".repeat(depth))
            val meta = APISchemaEntry(id = 1, params = listOf(ParamSchema(1, "value", "JSON", isList = list)))
            val params = mapOf("value" to if (list) listOf(value) else value)
            if (depth == 128) assertTrue(LuxoBinaryProtocol.encodeRequest(meta, params).isNotEmpty())
            else assertFailsWith<IllegalArgumentException> { LuxoBinaryProtocol.encodeRequest(meta, params) }
        }
    }

    @OptIn(kotlinx.serialization.ExperimentalSerializationApi::class)
    @Test
    fun `bounded JSON validates source fragments before parsing or writing`() {
        for (objects in listOf(false, true)) for (depth in 127..129) for (leaf in listOf("0", "[]", "{}", "1e400")) {
            val count = depth - if (leaf == "[]" || leaf == "{}") 1 else 0
            val raw = (if (objects) "{\"v\":" else "[").repeat(count) + leaf +
                (if (objects) "}" else "]").repeat(count)
            val value = Json.parseToJsonElement(raw)
            if (depth <= 128) {
                assertEquals(value, decodeBoundedLuxoJSON(raw))
                assertEquals(raw, encodeBoundedLuxoJSON(value))
                assertEquals(raw, encodeBoundedLuxoJSON(JsonUnquotedLiteral(raw)))
            } else {
                assertFailsWith<IllegalArgumentException> { decodeBoundedLuxoJSON(raw) }
                assertFailsWith<IllegalArgumentException> { encodeBoundedLuxoJSON(value) }
                assertFailsWith<IllegalArgumentException> { decodeLuxoJsonValue(JsonUnquotedLiteral(raw)) }
                assertFailsWith<IllegalArgumentException> { encodeBoundedLuxoJSON(JsonUnquotedLiteral(raw)) }
            }
        }
        val fragment = JsonUnquotedLiteral("[".repeat(128) + "0" + "]".repeat(128))
        for (outer in listOf(JsonArray(listOf(fragment)), buildJsonObject { put("v", fragment) })) {
            assertFailsWith<IllegalArgumentException> { encodeBoundedLuxoJSON(outer) }
            assertFailsWith<IllegalArgumentException> { decodeLuxoJsonValue(outer) }
        }
        val quoted = JsonPrimitive("[{}]\\\"".repeat(200)).toString()
        assertEquals(Json.parseToJsonElement(quoted), decodeBoundedLuxoJSON(quoted))
        for (bad in listOf("[0,]", "{\"v\":0,}", "[", "true false", "\"\\ud800\"")) {
            assertFailsWith<IllegalArgumentException> { decodeBoundedLuxoJSON(bad) }
        }
        assertFailsWith<IllegalArgumentException> { decodeBoundedLuxoJSON("[".repeat(4000) + "0" + "]".repeat(4000)) }
    }

    @Test
    fun `typed JSON parameters reset budgets inside models and stop recursive host collections`() {
        val fields = mapOf("value" to SelectionFieldSchema(1, type = "JSON"),
            "values" to SelectionFieldSchema(2, type = "JSON", isList = true))
        val meta = APISchemaEntry(1, listOf(ParamSchema(1, "input", "Model", typeName = "Packet")),
            types = mapOf("Packet" to fields))
        for (depth in 128..129) {
            val value = Json.parseToJsonElement("[".repeat(depth) + "0" + "]".repeat(depth))
            val params = mapOf("input" to mapOf("value" to value, "values" to listOf(value)))
            if (depth == 128) buildJsonObject { putLuxoParams(params, meta) }
            else assertFailsWith<IllegalArgumentException> { buildJsonObject { putLuxoParams(params, meta) } }
        }
        for (kind in 0..2) {
            var value: Any = 0
            repeat(4000) { value = when (kind) { 0 -> listOf(value); 1 -> arrayOf(value); else -> mapOf("v" to value) } }
            assertFailsWith<IllegalArgumentException> { luxoGenericJSONValue(value) }
        }
    }

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
