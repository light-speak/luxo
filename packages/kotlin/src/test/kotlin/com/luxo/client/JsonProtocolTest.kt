package com.luxo.client

import java.io.File
import kotlinx.serialization.json.*
import kotlin.test.*

class JsonProtocolTest {
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
        for (code in listOf(Int.MIN_VALUE, 0, 400, Int.MAX_VALUE)) {
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
