package com.luxo.client

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertContentEquals
import kotlin.test.assertFailsWith
import kotlinx.serialization.json.*
import java.io.File

class DateTimeTest {
    @Test
    fun `shared DateTime boundaries reject truncation and normalization`() {
        val fixture = Json.parseToJsonElement(File("../../pkg/lux/codec/testdata/protocol-v1.json").readText()).jsonObject
        for (entry in fixture.getValue("dateTimeBoundaryCases").jsonArray) {
            val item = entry.jsonObject
            val input = item.getValue("input").jsonPrimitive.content
            val encoder = LuxoEncoder()
            if (item["canonical"] == JsonNull) {
                assertFailsWith<LuxoCodecException>(input) { encoder.writeDateTime(input) }
                assertEquals(0, encoder.bytes().size)
            } else {
                encoder.writeDateTime(input)
                assertEquals(item.getValue("canonical").jsonPrimitive.content, LuxoDecoder(encoder.bytes()).readDateTime(), input)
            }
        }
    }
    @Test
    fun `nanoseconds survive request and response boundaries`() {
        for (input in listOf(
            "2026-09-17T12:00:00.123456789Z",
            "1969-12-31T23:59:59.999999999Z",
            "9999-12-31T23:59:59.999999999Z",
        )) {
            val value = java.time.Instant.parse(input)
            val wire = LuxoEncoder()
            wire.writeSvarint(value.epochSecond)
            wire.writeVarint(value.nano.toLong())
            assertEquals(input, LuxoDecoder(wire.bytes()).readDateTime())
            val request = LuxoBinaryProtocol.encodeRequest(
                APISchemaEntry(1, listOf(ParamSchema(1, "value", "DateTime"))),
                mapOf("value" to input),
            )
            assertContentEquals(byteArrayOf(1, 0, 1) + wire.bytes() + byteArrayOf(0), request)
        }
    }

    @Test
    fun `invalid nanoseconds are not normalized`() {
        for (nanos in listOf(1000000000L, Long.MAX_VALUE, -1L)) {
            val wire = LuxoEncoder()
            wire.writeSvarint(0)
            wire.writeVarint(nanos)
            assertFailsWith<LuxoCodecException> { LuxoDecoder(wire.bytes()).readDateTime() }
        }
        assertFailsWith<LuxoCodecException> { LuxoDecoder(byteArrayOf(0)).readDateTime() }
    }
}
