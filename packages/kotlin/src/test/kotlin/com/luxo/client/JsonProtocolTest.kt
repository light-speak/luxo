package com.luxo.client

import java.io.File
import kotlinx.serialization.json.*
import kotlin.test.*

class JsonProtocolTest {
    @Test
    fun `typed model parameters count objects independently from lists and JSON`() {
        val fields = mapOf("child" to SelectionFieldSchema(1, typeName = "Node", type = "Model", nullable = true),
            "children" to SelectionFieldSchema(2, typeName = "Node", type = "Model", isList = true),
            "payload" to SelectionFieldSchema(3, type = "JSON"))
        val meta = APISchemaEntry(1, listOf(ParamSchema(1, "node", "Model", typeName = "Node")), types = mapOf("Node" to fields))
        val generic = Json.parseToJsonElement("[".repeat(128) + "0" + "]".repeat(128))
        fun value(depth: Int, list: Boolean): JsonObject = buildJsonObject {
            put("child", if (depth > 1 && !list) value(depth - 1, list) else JsonNull)
            put("children", JsonArray(if (depth > 1 && list) listOf(value(depth - 1, list)) else emptyList()))
            put("payload", generic)
        }
        for (list in listOf(false, true)) for (depth in listOf(1, 63, 64, 65)) {
            val objectValue = value(depth, list)
            for (source in listOf(objectValue, objectValue.toMap())) {
                val params = mapOf("node" to source)
                if (depth <= 64) buildJsonObject { putLuxoParams(params, meta) }
                else assertContains(assertFailsWith<IllegalArgumentException> {
                    buildJsonObject { putLuxoParams(params, meta) }
                }.message.orEmpty(), "64")
            }
        }
    }

    @Test
    fun `scalar syntax fast path preserves whitespace and control character rules`() {
        for (value in listOf("0", "-0", "1.25e-3", "true", "false", "null", "\"x\"", "{}", "[]")) {
            val text = " \t\r\n$value\n\t "
            assertSame(text, requireJSONTokens(text))
            for (suffix in listOf("0", "null", "]", "}", ",", ":")) {
                assertFailsWith<IllegalArgumentException> { requireJSONTokens(text + suffix) }
            }
        }
        for (control in 0..31) {
            assertFailsWith<IllegalArgumentException> { requireJSONTokens("\"a${control.toChar()}b\"") }
            val escaped = "\"\\u${control.toString(16).padStart(4, '0')}\""
            assertSame(escaped, requireJSONTokens(escaped))
        }
        for (text in listOf("\"\\", "\"\\u", "\"\\u000", "\"\\\"", "[}", "{]")) {
            assertFailsWith<IllegalArgumentException> { requireJSONTokens(text) }
        }
    }

    @Test
    fun `syntax stack preserves mixed parents across storage boundaries`() {
        for (depth in listOf(1, 2, 31, 32, 63, 64, 65, 127, 128, 129, 1000)) {
            val opens = (0 until depth).joinToString("") { if (it % 2 == 0) "[" else "{\"x\":" }
            val closes = (depth - 1 downTo 0).joinToString("") { if (it % 2 == 0) "]" else "}" }
            for (leaf in listOf("0", "[]", "{}", "\"text\"")) {
                val text = opens + leaf + closes
                assertSame(text, requireJSONTokens(text))
                assertFailsWith<IllegalArgumentException> { requireJSONTokens(text.dropLast(1)) }
                assertFailsWith<IllegalArgumentException> { requireJSONTokens(text + "]") }
            }
            val wrongClose = if (closes.first() == ']') '}' else ']'
            assertFailsWith<IllegalArgumentException> { requireJSONTokens(opens + "0" + wrongClose + closes.drop(1)) }
            val sibling = "[$opens{}$closes,$opens[]$closes]"
            assertSame(sibling, requireJSONTokens(sibling))
        }
    }

    @OptIn(kotlinx.serialization.ExperimentalSerializationApi::class)
    @Test
    fun `raw fragments cannot inject siblings into a JSON container`() {
        val invalid = listOf(
            JsonArray(listOf(JsonUnquotedLiteral("0,1"))),
            JsonArray(listOf(JsonUnquotedLiteral(""))),
            JsonObject(mapOf("value" to JsonUnquotedLiteral("0,\"extra\":true"))),
            JsonObject(mapOf("value" to JsonUnquotedLiteral("0,\"value\":1"))),
        )
        for (value in invalid) {
            assertFailsWith<IllegalArgumentException>(value.toString()) { encodeLuxoJSON(value) }
            assertFailsWith<IllegalArgumentException> { decodeLuxoJsonValue(value) }
        }
        val literal = JsonUnquotedLiteral("{\"extra\":[1,2]}")
        assertEquals("[{\"extra\":[1,2]}]", encodeLuxoJSON(JsonArray(listOf(literal))))
    }

    @OptIn(kotlinx.serialization.ExperimentalSerializationApi::class)
    @Test
    fun `raw invalid literals cannot enter generated inputs or direct error data`() {
        for (token in listOf("undefined", "NaN", "+1", "01", "1e+", "\uFEFFnull")) {
            val raw = JsonUnquotedLiteral(token)
            for (value in listOf(raw, JsonArray(listOf(raw)), JsonObject(mapOf("value" to raw)))) {
                assertFails(token) { encodeLuxoJSON(value) }
                assertFails(token) { decodeLuxoJsonValue(value) }
            }
        }
    }

    @OptIn(kotlinx.serialization.ExperimentalSerializationApi::class)
    @Test
    fun `raw JSON fragments must contain exactly one complete value`() {
        val invalid = listOf("", " ", "1 2", "[1,]", "{\"x\":}", "[1", "{", "true false",
            "[1 2]", "{\"x\" 1}", "{\"x\":1,}", "\"\\q\"", "\"\\u12zz\"", "\"line\nfeed\"")
        for (text in invalid) {
            val raw = JsonUnquotedLiteral(text)
            assertFails(text) { encodeLuxoJSON(raw) }
            assertFails(text) { decodeLuxoJsonValue(raw) }
            assertFails(text) { decodeLuxoJSON(text) }
        }
        for (text in listOf("{}", "[]", "[1,{\"x\":[true,null]}]", "\"escaped\\\"quote\"", "\"\\u0061\"")) {
            val raw = JsonUnquotedLiteral(text)
            assertEquals(text, encodeLuxoJSON(raw))
            assertSame(raw, decodeLuxoJsonValue(raw))
            assertEquals(Json.parseToJsonElement(text), decodeLuxoJSON(text))
        }
    }

    @Test
    fun `invalid JSON literals cannot hide in opaque values or overwritten keys`() {
        val invalid = listOf("undefined", "NaN", "Infinity", "+1", "01", "-01", "1.", ".1", "1e", "1e+", "--1", "TRUE", "nul", "\uFEFFnull")
        val accepted = invalid.flatMap { listOf(it, "[$it]", "{\"data\":$it,\"data\":0}") }
            .filter { runCatching { decodeLuxoJSON(it) }.isSuccess }
        assertEquals(emptyList(), accepted, "invalid tokens must be rejected before duplicate keys discard them")
        for (valid in listOf("0", "-0", "1.2300", "1e9999", "-1e-9999", "123456789012345678901234567890", "true", "false", "null")) {
            assertEquals(valid, decodeLuxoJSON(valid).toString())
        }
        assertEquals("NaN", decodeLuxoJSON("\"NaN\"").jsonPrimitive.content)
    }

    @Test
    fun `Binary JSON rejects document BOM prefixes`() {
        for (text in listOf("null", "42", "{}", "[]", "\"value\"")) {
            assertFails { decodeLuxoJSON("\uFEFF$text") }
            val encoder = LuxoEncoder()
            encoder.writeFieldInt(1, 400)
            encoder.writeFieldString(2, "Custom")
            encoder.writeFieldString(3, "failed")
            encoder.writeFieldString(5, "\uFEFF$text")
            encoder.writeEnd()
            assertEquals("ParseError", LuxoBinaryProtocol.decodeError(encoder.bytes(), 502).error)
        }
        assertEquals("\uFEFFvalue", decodeLuxoJSON("\"\uFEFFvalue\"").jsonPrimitive.content)
        assertTrue(decodeHTTPJSON("\uFEFF{\"data\":null}".encodeToByteArray()).containsKey("data"))
    }

    @Test
    fun `prototype named error data keys survive both encodings`() {
        val text = """{"__proto__":{"marker":"value"},"constructor":{"prototype":{"marker":"value"}},"hasOwnProperty":false,"toJSON":"literal","n":9007199254740993,"precise":1.2300}"""
        val envelope = decodeHTTPJSON("""{"error":"Custom","code":400,"message":"failed","data":$text}""".encodeToByteArray())
        val binary = LuxoEncoder()
        binary.writeFieldInt(1, 400)
        binary.writeFieldString(2, "Custom")
        binary.writeFieldString(3, "failed")
        binary.writeFieldString(5, text)
        binary.writeEnd()
        for (error in listOf(decodeLuxoJsonError(envelope, 502), LuxoBinaryProtocol.decodeError(binary.bytes(), 502))) {
            assertNotNull(error)
            assertEquals("Custom", error.error)
            val data = assertIs<JsonObject>(error.data)
            assertTrue(data.containsKey("__proto__"))
            assertEquals("literal", data["toJSON"]?.jsonPrimitive?.content)
            assertEquals(text, data.toString())
        }
    }

    @Test
    fun `typed strings and booleans reject other JSON kinds`() {
        for (text in listOf("", "false", "9007199254740993", "发布 café", "\uFEFFtext")) {
            val value = JsonPrimitive(text)
            assertSame(text, decodeLuxoJsonString(value))
            assertEquals("ParseError", assertFailsWith<LuxoError> { decodeLuxoJsonBoolean(value) }.error)
        }
        for (boolean in listOf(true, false)) {
            val value = JsonPrimitive(boolean)
            assertEquals(boolean, decodeLuxoJsonBoolean(value))
            assertEquals("ParseError", assertFailsWith<LuxoError> { decodeLuxoJsonString(value) }.error)
        }
        for (text in listOf("0", "1", "9007199254740993", "1.0", "null", "[]", "{}")) {
            val value = Json.parseToJsonElement(text)
            assertEquals("ParseError", assertFailsWith<LuxoError> { decodeLuxoJsonString(value) }.error)
            assertEquals("ParseError", assertFailsWith<LuxoError> { decodeLuxoJsonBoolean(value) }.error)
        }
    }

    @Test
    fun `opaque numeric tokens remain exact in parameters and both error encodings`() {
        val fixture = Json.parseToJsonElement(File("../../pkg/lux/codec/testdata/protocol-v1.json").readText()).jsonObject
        val meta = APISchemaEntry(1, listOf(ParamSchema(1, "value", "JSON")))
        for (number in fixture.getValue("jsonNumberTokens").jsonArray) {
            val token = number.jsonPrimitive.content
            val text = """{"n":$token,"items":[$token,null],"nested":{"n":$token}}"""
            val value = Json.parseToJsonElement(text)
            assertSame(value, encode(meta, value)["value"])
            assertEquals(text, encode(meta, value)["value"].toString())
            val json = decodeHTTPJSON("""{"error":"Custom","code":400,"message":"failure","data":$text}""".encodeToByteArray())
            assertEquals(text, decodeLuxoJsonError(json, 400)?.data.toString())
            val binary = LuxoEncoder()
            binary.writeFieldInt(1, 400)
            binary.writeFieldString(2, "Custom")
            binary.writeFieldString(3, "failure")
            binary.writeFieldString(5, text)
            binary.writeEnd()
            assertEquals(text, LuxoBinaryProtocol.decodeError(binary.bytes(), 400).data.toString())
        }
    }

    @Test
    fun `generated JSON floats preserve finite values and reject nonfinite values`() {
        for (value in listOf(0.0, -0.0, 1.25, Double.MIN_VALUE, Double.MAX_VALUE)) {
            assertEquals(value.toBits(), luxoJsonFloat(value).double.toBits())
        }
        for (value in listOf(Double.NaN, Double.POSITIVE_INFINITY, Double.NEGATIVE_INFINITY)) {
            assertEquals("ConfigError", assertFailsWith<LuxoError> { luxoJsonFloat(value) }.error)
        }
    }

    @Test
    fun `integer JSON tokens must be integral unquoted and in range`() {
        for (value in listOf(JsonPrimitive("1"), JsonPrimitive(1.5), JsonNull, JsonPrimitive(true), Json.parseToJsonElement("9223372036854775808"))) {
            assertEquals("ConfigError", assertFailsWith<LuxoError> { requireLuxoInt64(value) }.error)
        }
        for (value in listOf(Long.MIN_VALUE, 0L, Long.MAX_VALUE)) {
            assertEquals(value, requireLuxoInt64(JsonPrimitive(value)))
        }
    }

    @Test
    fun `typed integer arrays and nullable values stay exact`() {
        for (type in listOf("Int", "Duration")) {
            val meta = APISchemaEntry(1, listOf(ParamSchema(1, "value", type, isList = true, nullable = true)))
            assertEquals(JsonNull, encode(meta, JsonNull)["value"])
            for (value in listOf(listOf(Long.MIN_VALUE, Long.MAX_VALUE), arrayOf(Long.MIN_VALUE, Long.MAX_VALUE))) {
                assertEquals("[-9223372036854775808,9223372036854775807]", encode(meta, value)["value"].toString())
            }
            for (value in listOf(listOf(null), listOf(1.5), 1L)) {
                assertEquals("ConfigError", assertFailsWith<LuxoError> { encode(meta, value) }.error)
            }
            assertEquals(emptyMap(), buildJsonObject { putLuxoParams(emptyMap(), meta) })
        }
    }

    @Test
    fun `raw JSON objects and generated input JSON are validated without copying`() {
        val fields = mapOf(
            "value" to SelectionFieldSchema(1, type = "Int"),
            "children" to SelectionFieldSchema(2, "Input", type = "Model", isList = true),
        )
        val meta = APISchemaEntry(1, listOf(ParamSchema(1, "value", "Model", typeName = "Input")), types = mapOf("Input" to fields))
        val raw = Json.parseToJsonElement("""{"value":9223372036854775807,"children":[{"value":-9223372036854775808}]}""")
        assertSame(raw, encode(meta, raw)["value"])
        var conversions = 0
        val generated = object : LuxoBinaryEncodable {
            override fun writeLuxo(encoder: LuxoEncoder) = Unit
            override fun toLuxoJson(): JsonElement { conversions++; return raw }
        }
        assertSame(raw, encode(meta, generated)["value"])
        assertEquals(1, conversions)
        val invalid = Json.parseToJsonElement("""{"children":[{"value":1.5}]}""")
        assertEquals("ConfigError", assertFailsWith<LuxoError> { encode(meta, invalid) }.error)
        assertEquals("ConfigError", assertFailsWith<LuxoError> { encode(meta, mapOf("value" to null)) }.error)
        assertEquals("ConfigError", assertFailsWith<LuxoError> { encode(meta, mapOf(1 to 2)) }.error)
        assertEquals("ConfigError", assertFailsWith<LuxoError> { encode(meta, 1) }.error)
        assertEquals("ConfigError", assertFailsWith<LuxoError> { encode(meta.copy(types = emptyMap()), raw) }.error)
    }

    @Test
    fun `opaque JSON and unknown fields retain existing representation`() {
        val raw = Json.parseToJsonElement("""{"n":9223372036854775808,"f":1.5,"nil":null}""")
        val meta = APISchemaEntry(1, listOf(ParamSchema(1, "value", "JSON")))
        assertSame(raw, encode(meta, raw)["value"])
        assertSame(JsonNull, encode(meta, JsonNull)["value"])
        assertSame(raw, encode(null, raw)["value"])
        assertSame(raw, encode(APISchemaEntry(1), raw)["value"])
        val shape = APISchemaEntry(1, listOf(ParamSchema(1, "value", "Model", typeName = "Input")), types = mapOf("Input" to emptyMap()))
        assertEquals(raw, encode(shape, mapOf("n" to raw.jsonObject["n"], "f" to 1.5, "nil" to null))["value"])
    }

    @Test
    fun `valid error codes preserve optional fields and opaque full width data`() {
        for (code in listOf(400, 401, 500, 599)) {
            val json = Json.parseToJsonElement("""{"error":"Custom","code":$code,"message":"failure","data":{"id":9223372036854775807},"traceId":"trace","cause":"reason"}""").jsonObject
            val error = assertNotNull(decodeLuxoJsonError(json, 500))
            assertEquals(code, error.code)
            assertEquals("Custom", error.error)
            assertEquals("trace", error.traceId)
            assertEquals("reason", error.developmentCause)
            assertEquals(Long.MAX_VALUE, error.data!!.jsonObject.getValue("id").jsonPrimitive.long)
        }
        assertNull(decodeLuxoJsonError(buildJsonObject { put("data", 1) }, 200))
        val json = Json.parseToJsonElement("""{"error":"Custom","code":400,"message":"failure","traceId":null,"cause":null}""").jsonObject
        assertNull(decodeLuxoJsonError(json, 500)!!.traceId)
        assertNull(decodeLuxoJsonError(json, 500)!!.developmentCause)
    }

    @Test
    fun `malformed error fields always return a parse error`() {
        val valid = Json.parseToJsonElement("""{"error":"Custom","code":400,"message":"failure"}""").jsonObject
        for (name in listOf("error", "code", "message", "traceId", "cause")) {
            val invalidValues = if (name == "code") listOf(JsonNull, JsonPrimitive("400"), JsonPrimitive(1.5)) else listOf(JsonPrimitive(1), JsonObject(emptyMap()))
            for (value in invalidValues) {
                val error = decodeLuxoJsonError(JsonObject(valid + (name to value)), 502)
                assertEquals("ParseError", error?.error)
                assertEquals(502, error?.code)
            }
        }
        for (name in listOf("code", "message")) {
            assertEquals("ParseError", decodeLuxoJsonError(JsonObject(valid - name), 500)?.error)
        }
        for (token in listOf("4e2", "0e-400", "+400", "0400", "400.0")) {
            val json = JsonObject(valid + ("code" to Json.parseToJsonElement(token)))
            assertEquals("ParseError", decodeLuxoJsonError(json, 500)?.error, token)
        }
    }

    private fun encode(meta: APISchemaEntry?, value: Any?): JsonObject =
        buildJsonObject { putLuxoParams(mapOf("value" to value), meta) }
}
