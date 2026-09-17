package com.luxo.client

import java.io.File
import java.math.BigDecimal
import kotlinx.serialization.json.*
import kotlin.random.Random
import kotlin.test.*

class JsonNumbersTest {
    @Test
    fun `float conversion matches JSON grammar across short adversarial tokens`() {
        val alphabet = "019-+.eEfDx _\u0000"
        val digits = CharArray(4)
        for (length in 1..digits.size) {
            var count = 1
            repeat(length) { count *= alphabet.length }
            repeat(count) { ordinal ->
                var remaining = ordinal
                repeat(length) { index ->
                    digits[index] = alphabet[remaining % alphabet.length]
                    remaining /= alphabet.length
                }
                assertFloatToken(digits.concatToString(0, length))
            }
        }
    }

    @Test
    fun `float grammar rejects Java extensions and interior corruption without leaking exceptions`() {
        for (token in listOf("1.e2", "-0.e-2", "1e2.3", "1..2", "1e+e2", "0x1p0", "-0X1.Ap2",
            "1D", "1F", "1e2f", "1_0", "1\u00002", "1\t2", "1\n2", "1\u00a02", "١.٢", "１.２")) {
            assertFloatToken(token)
        }
        for (character in '\u0000'..' ') {
            assertFloatToken("${character}1.25")
            assertFloatToken("1.25$character")
        }
        val random = Random(0x4c55584f)
        repeat(4096) {
            val number = Double.fromBits(random.nextLong())
            if (number.isFinite()) {
                val token = number.toString()
                assertEquals(number.toRawBits(), decodeLuxoJsonFloat(unquoted(token)).toRawBits(), token)
                assertEquals(number.toRawBits(), singleScanFloatCandidate(unquoted(token)).toRawBits(), token)
                val position = random.nextInt(token.length + 1)
                assertFloatToken(token.substring(0, position) + "x" + token.substring(position))
            }
        }
    }

    // Independent declarative grammar and decimal zero check: no production scanner reuse.
    private val floatGrammar = Regex("-?(0|[1-9][0-9]*)(\\.[0-9]+)?([eE][+-]?[0-9]+)?")

    @Test
    fun `float grammar handles long mantissas and misplaced punctuation`() {
        for (token in listOf("0." + "1234567890".repeat(256), "1e" + "9".repeat(1024), "-0.000e-12345")) {
            assertFloatToken(token)
            for (position in listOf(0, 1, token.length / 2, token.length - 1, token.length)) {
                for (character in ".eE+-_\u0000") {
                    assertFloatToken(token.substring(0, position) + character + token.substring(position))
                }
            }
        }
    }

    private fun assertFloatToken(token: String) {
        val expected = if (floatGrammar.matches(token)) token.toDouble() else null
        val valid = expected != null && expected.isFinite() &&
            (expected != 0.0 || BigDecimal(token).signum() == 0)
        val value = unquoted(token)
        for (read in listOf(::decodeLuxoJsonFloat, ::singleScanFloatCandidate)) {
            if (valid) assertEquals(expected.toRawBits(), read(value).toRawBits(), token)
            else assertEquals("ParseError", assertFailsWith<LuxoError>(token) { read(value) }.error)
        }
    }

    @Test
    fun `explicit conversions match the canonical numeric corpus without changing tokens`() {
        val fixture = Json.parseToJsonElement(File("../../pkg/lux/codec/testdata/protocol-v1.json").readText()).jsonObject
        val cases = fixture.getValue("jsonNumberConversions").jsonArray
        assertTrue(cases.isNotEmpty())
        for (item in cases.map { it.jsonObject }) {
            val token = item.getValue("token").jsonPrimitive.content
            val integer = item.getValue("int64").jsonPrimitive.contentOrNull
            val bits = item.getValue("float64Bits").jsonPrimitive.contentOrNull
            for (value in listOf(Json.parseToJsonElement(token), unquoted(token))) {
                if (integer == null) assertFailsWith<LuxoError>(token) { decodeLuxoJsonInt(value) }
                else assertEquals(integer, decodeLuxoJsonInt(value).toString(), token)
                for (read in listOf(::decodeLuxoJsonFloat, ::singleScanFloatCandidate)) {
                    if (bits == null) assertFailsWith<LuxoError>(token) { read(value) }
                    else assertEquals(bits, read(value).toRawBits().toULong().toString(16).padStart(16, '0'), token)
                }
                assertEquals(token, value.toString())
            }
        }
    }

    @Test
    fun `integer responses and raw parameters use exact signed tokens`() {
        val fixture = Json.parseToJsonElement(File("../../pkg/lux/codec/testdata/protocol-v1.json").readText()).jsonObject
        val tokens = fixture.getValue("int64Cases").jsonArray.map { it.jsonObject.getValue("value").jsonPrimitive.content } + "-0"
        for (token in tokens) {
            val value = Json.parseToJsonElement(token)
            assertEquals(token.toLong(), decodeLuxoJsonInt(value))
            assertEquals(token.toLong(), requireLuxoInt64(value))
        }
        for (token in listOf("", "-", "+1", "00", "01", "-01", "1.0", "1e0", "1e-400", "9223372036854775808",
            "-9223372036854775809", "9999999999999999999", "99999999999999999999", " 1", "1 ", "١", "NaN")) {
            assertNull(parseLuxoIntToken(token), token)
            val value = unquoted(token)
            assertEquals("ParseError", assertFailsWith<LuxoError>(token) { decodeLuxoJsonInt(value) }.error)
            assertEquals("ConfigError", assertFailsWith<LuxoError>(token) { requireLuxoInt64(value) }.error)
        }
        for (value in listOf(JsonNull, JsonPrimitive("1"), JsonPrimitive(true), JsonArray(emptyList()), JsonObject(emptyMap()))) {
            assertFailsWith<LuxoError> { decodeLuxoJsonInt(value) }
        }
    }

    @Test
    fun `float conversion preserves finite values and distinguishes signed zero from underflow`() {
        for (token in listOf("0", "-0", "0.00", "-0.00E-99999", "0e99999", "1E+03", "5e-324", "-5e-324",
            "2.4703282292062328e-324", "1.7976931348623157e308", "0.12345678901234567890123456789", "9223372036854775808")) {
            assertEquals(token.toDouble().toBits(), decodeLuxoJsonFloat(Json.parseToJsonElement(token)).toBits(), token)
        }
        for (token in listOf("", "-", "+1", "01", "-01", ".1", "1.", "1e", "1e+", "1e-", "1f", "0x1.0p0",
            "NaN", "Infinity", "1e400", "-1e400", "1e-400", "-1e-400", "2.4703282292062327e-324", " 1", "1 ", "١")) {
            assertEquals("ParseError", assertFailsWith<LuxoError>(token) { decodeLuxoJsonFloat(unquoted(token)) }.error)
        }
        for (value in listOf(JsonNull, JsonPrimitive("1"), JsonPrimitive(true), JsonArray(emptyList()), JsonObject(emptyMap()))) {
            assertFailsWith<LuxoError> { decodeLuxoJsonFloat(value) }
        }
    }

    @OptIn(kotlinx.serialization.ExperimentalSerializationApi::class)
    private fun unquoted(token: String): JsonElement = JsonUnquotedLiteral(token)
}
