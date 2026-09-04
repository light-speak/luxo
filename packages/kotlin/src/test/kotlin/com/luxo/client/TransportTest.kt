package com.luxo.client

import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.add
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue
import kotlin.test.assertFailsWith

class TransportProtocolTest {

    @Test
    fun `binary request includes field mask and typed params`() {
        val meta = APISchemaEntry(
            id = 7,
            params = listOf(ParamSchema(1, "id", "Int")),
            fields = mapOf("id" to SelectionFieldSchema(1), "name" to SelectionFieldSchema(2)),
        )

        val body = LuxoBinaryProtocol.encodeRequest(
            meta,
            mapOf("id" to 3, "\$select" to "name"),
        )

        assertTrue(byteArrayOf(7, 2, 1, 2, 1, 6, 0).contentEquals(body))
    }

    @Test
    fun `binary request encodes nested selections recursively`() {
        val meta = APISchemaEntry(
            id = 1,
            fields = mapOf(
                "id" to SelectionFieldSchema(1),
                "posts" to SelectionFieldSchema(3, "Post"),
            ),
            types = mapOf(
                "Post" to mapOf(
                    "id" to SelectionFieldSchema(1),
                    "title" to SelectionFieldSchema(2),
                ),
            ),
        )
        val body = LuxoBinaryProtocol.encodeRequest(meta, mapOf("\$select" to "id,posts{title}"))
        assertTrue(byteArrayOf(1, 6, 1, 5, 3, 2, 1, 2, 0).contentEquals(body))
    }

    @Test
    fun `binary request encodes filters and sorters`() {
        val body = LuxoBinaryProtocol.encodeRequest(
            APISchemaEntry(5),
            mapOf(
                "\$filters" to listOf(LuxoFilter("age", "gte", 18)),
                "\$sorters" to listOf(LuxoSorter("createdAt", "desc")),
            ),
        )
        assertTrue(byteArrayOf(
            5, 0,
            0xfe.toByte(), 0xff.toByte(), 0xff.toByte(), 0xff.toByte(), 0x07,
            1, 3, 97, 103, 101, 4, 2, 49, 56,
            0xff.toByte(), 0xff.toByte(), 0xff.toByte(), 0xff.toByte(), 0x07,
            1, 9, 99, 114, 101, 97, 116, 101, 100, 65, 116, 1,
            0,
        ).contentEquals(body))
    }

    @Test
    fun `binary request supports Bytes and JSON params`() {
        val meta = APISchemaEntry(
            id = 1,
            params = listOf(
                ParamSchema(1, "blob", "Bytes"),
                ParamSchema(2, "metadata", "JSON"),
            ),
        )

        val body = LuxoBinaryProtocol.encodeRequest(
            meta,
            mapOf(
                "blob" to byteArrayOf(0, 0xff.toByte()),
                "metadata" to mapOf("ok" to true),
            ),
        )

        assertTrue(
            byteArrayOf(
                1, 0,
                1, 2, 0, 0xff.toByte(),
                2, 11, 123, 34, 111, 107, 34, 58, 116, 114, 117, 101, 125,
                0,
            ).contentEquals(body),
        )
    }

    @Test
    fun `canonical binary error envelope preserves every field`() {
        val body = byteArrayOf(
            1, 0xa0.toByte(), 0x06,
            2, 10, 66, 97, 100, 82, 101, 113, 117, 101, 115, 116,
            3, 3, 98, 97, 100,
            4, 1, 116,
            5, 2, 123, 125,
            6, 1, 99,
            0,
        )

        val error = LuxoBinaryProtocol.decodeError(body, 400)

        assertEquals("BadRequest", error.error)
        assertEquals(400, error.code)
        assertEquals("bad", error.message)
        assertEquals("t", error.traceId)
        assertEquals(buildJsonObject {}, error.data)
        assertEquals("c", error.developmentCause)
    }

    @Test
    fun `non-canonical binary error envelopes are rejected`() {
        val invalid = listOf(
            byteArrayOf(1),
            byteArrayOf(1, 0xa0.toByte(), 0x06, 0),
            byteArrayOf(1, 0xa0.toByte(), 0x06, 2, 1, 69, 3, 1, 109),
            byteArrayOf(1, 0xa0.toByte(), 0x06, 2, 1, 69, 3, 1, 109, 0, 1),
        )
        for (body in invalid) {
            assertEquals("ParseError", LuxoBinaryProtocol.decodeError(body, 400).error)
        }
    }

    @Test
    fun `binary call frame keeps multi-byte sequence IDs unambiguous`() {
        val frame = LuxoBinaryProtocol.callFrame(253, byteArrayOf(7, 0, 0))
        assertTrue(byteArrayOf(1, 0xfd.toByte(), 0x01, 7, 0, 0).contentEquals(frame))
    }

    @Test
    fun `JSON params preserve nested objects arrays and null`() {
        val value = luxoJsonValue(
            mapOf(
                "filter" to mapOf("active" to true),
                "ids" to listOf(1, 2),
                "optional" to null,
            ),
        )

        assertEquals(
            buildJsonObject {
                put("filter", buildJsonObject { put("active", true) })
                put("ids", buildJsonArray { add(1); add(2) })
                put("optional", JsonNull)
            },
            value,
        )
    }

    @Test
    fun `unknown param types and non-string DateTime are rejected`() {
        assertFailsWith<LuxoError> {
            LuxoBinaryProtocol.encodeRequest(
                APISchemaEntry(1, listOf(ParamSchema(1, "input", "Model"))),
                mapOf("input" to emptyMap<String, Any?>()),
            )
        }
        assertFailsWith<LuxoError> {
            LuxoBinaryProtocol.encodeRequest(
                APISchemaEntry(1, listOf(ParamSchema(1, "at", "DateTime"))),
                mapOf("at" to 0),
            )
        }
        assertFailsWith<LuxoError> {
            LuxoBinaryProtocol.encodeRequest(
                APISchemaEntry(1, listOf(ParamSchema(1, "at", "DateTime", isList = true))),
                mapOf("at" to listOf(null)),
            )
        }
    }

    @Test
    fun `nullable params encode null present and absent distinctly`() {
        val meta = APISchemaEntry(
            9,
            listOf(
                ParamSchema(1, "nickname", "String", nullable = true),
                ParamSchema(2, "age", "Int", nullable = true),
            ),
        )
        assertTrue(
            byteArrayOf(9, 0, 1, 0, 2, 1, 84, 0).contentEquals(
                LuxoBinaryProtocol.encodeRequest(meta, mapOf("nickname" to null, "age" to 42)),
            ),
        )
        assertTrue(byteArrayOf(9, 0, 0).contentEquals(LuxoBinaryProtocol.encodeRequest(meta, emptyMap())))
    }
}
