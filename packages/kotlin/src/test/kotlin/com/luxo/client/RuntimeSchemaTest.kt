package com.luxo.client

import kotlinx.coroutines.runBlocking
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import okio.Buffer
import kotlin.test.*

class RuntimeSchemaTest {
    @Test
    fun parameterCopiesDoNotBorrowSourceStorageAtAnySupportedListShape() {
        for (count in listOf(0, 1, 16, 128)) {
            val params = MutableList(count) { ParamSchema(it + 1, "value$it", "Int") }
            val copy = snapshotAPISchema(mapOf("read" to APISchemaEntry(1, params))).getValue("read")
            for (index in params.indices) params[index] = params[index].copy(type = "String")
            assertEquals(count, copy.params.size)
            for (param in copy.params) {
                assertEquals("Int", param.type)
                assertEquals(param, copy.paramsByName[param.name])
            }
            params.clear()
            assertEquals(count, copy.params.size)
        }
    }

    @Test
    fun snapshotOwnsContainersAndPreservesSharedDirectories() {
        val fields = mutableMapOf("value" to SelectionFieldSchema(1, "Input", "Model", true, true))
        val types = mutableMapOf<String, Map<String, SelectionFieldSchema>>("Input" to fields)
        val params = mutableListOf(ParamSchema(1, "value", "Int", isList = true, nullable = true))
        val source = mutableMapOf("read" to APISchemaEntry(1, params, fields, types),
            "other" to APISchemaEntry(2, params, fields, types))
        val snapshot = snapshotAPISchema(source)
        val read = snapshot.getValue("read")
        assertSame(read.fields, snapshot.getValue("other").fields)
        assertSame(read.types, snapshot.getValue("other").types)
        assertSame(read.fields, read.types.getValue("Input"))
        assertEquals(SelectionFieldSchema(1, "Input", "Model", true, true), read.fields["value"])
        assertTrue(read.params.single().isList && read.params.single().nullable)
        params.clear()
        fields["value"] = SelectionFieldSchema(2, type = "String")
        assertEquals(1, read.fields.getValue("value").fieldID)
        assertEquals("Int", read.paramsByName.getValue("value").type)
        assertEquals(1, read.params.size)
        assertNotSame(fields, read.fields)
        assertNotSame(params, read.params)
        assertNotSame(types, read.types)
        assertNotSame(source, snapshot)
        val updated = snapshotAPISchema(source).getValue("read")
        assertEquals(2, updated.fields.getValue("value").fieldID)
        assertTrue(updated.paramsByName.isEmpty())
    }

    @Test
    fun registrationValidatesAllIdentifierSpaces() {
        val socket = LuxoWebSocket("ws://localhost/luxo", autoReconnect = false)
        try {
            for (transport in listOf<Transport>(OkHttpTransport("http://localhost/luxo"), socket)) {
                for (target in listOf("api", "param", "field", "type")) {
                    val maximum = when (target) {
                        "api" -> WireIDLimits.API
                        "param" -> WireIDLimits.PARAM
                        else -> WireIDLimits.SELECTION_FIELD
                    }
                    val invalid = listOf(0, -1, Int.MIN_VALUE) +
                        if (maximum < Int.MAX_VALUE) listOf(maximum + 1) else emptyList()
                    for (id in invalid) assertFailsWith<IllegalArgumentException>("$target=$id") {
                        transport.setSchema(schema(target, id))
                    }
                    assertFailsWith<IllegalArgumentException>("duplicate $target") {
                        transport.setSchema(schema(target, 1, duplicate = true))
                    }
                    transport.setSchema(schema(target, maximum))
                }
                transport.setSchema(emptyMap())
            }
        } finally { socket.close() }
    }

    @Test
    fun httpConstructorValidatesBeforeAcceptingMetadata() {
        for (target in listOf("api", "param", "field", "type")) {
            assertFailsWith<IllegalArgumentException>(target) {
                OkHttpTransport("http://localhost/luxo", schema = schema(target, 0))
            }
        }
    }

    @Test
    fun externalMutationCannotChangeRegisteredHttpParameters() = runBlocking {
        for (mode in TransportMode.entries) for (constructor in listOf(false, true)) {
            val fixture = Requests(mode)
            val params = mutableListOf(ParamSchema(1, "value", "Int"))
            val source = mutableMapOf("read" to APISchemaEntry(1, params))
            val transport = OkHttpTransport("http://localhost/luxo", client = fixture.client, mode = mode,
                schema = if (constructor) source else emptyMap())
            if (!constructor) transport.setSchema(source)
            params[0] = ParamSchema(2, "value", "String")
            transport.call("read", mapOf("value" to 7L))
            source.clear()
            transport.call("read", mapOf("value" to 7L))
            assertEquals(2, fixture.bodies.size)
            fixture.assertBody()
        }
    }

    @Test
    fun rejectedReplacementPreservesOldHttpSchema() = runBlocking {
        for (mode in TransportMode.entries) {
            val fixture = Requests(mode)
            val transport = OkHttpTransport("http://localhost/luxo", client = fixture.client, mode = mode,
                schema = mapOf("read" to APISchemaEntry(1, listOf(ParamSchema(1, "value", "Int")))))
            assertFailsWith<IllegalArgumentException> {
                transport.setSchema(linkedMapOf(
                    "read" to APISchemaEntry(9, listOf(ParamSchema(2, "value", "String"))),
                    "invalid" to APISchemaEntry(0),
                ))
            }
            transport.call("read", mapOf("value" to 7L))
            fixture.assertBody()
            assertFailsWith<LuxoError> { transport.call("read", mapOf("value" to "7")) }
            assertEquals(1, fixture.bodies.size)
        }
    }

    private fun schema(target: String, id: Int, duplicate: Boolean = false): Map<String, APISchemaEntry> {
        val fields = buildMap {
            put("value", SelectionFieldSchema(id, type = "Int"))
            if (duplicate) put("other", SelectionFieldSchema(id, type = "Int"))
        }
        val params = if (target == "param") buildList {
            add(ParamSchema(id, "value", "Int"))
            if (duplicate) add(ParamSchema(id, "other", "Int"))
        } else emptyList()
        val entry = APISchemaEntry(if (target == "api") id else 1, params,
            if (target == "field") fields else emptyMap(),
            if (target == "type") mapOf("Input" to fields) else emptyMap())
        return buildMap {
            put("read", entry)
            if (target == "api" && duplicate) put("other", entry)
        }
    }

    private class Requests(private val mode: TransportMode) {
        val bodies = mutableListOf<ByteArray>()
        val client = OkHttpClient.Builder().addInterceptor { chain ->
            val request = chain.request()
            bodies += Buffer().also { request.body!!.writeTo(it) }.readByteArray()
            val data = if (mode == TransportMode.JSON) "{\"data\":true}".toByteArray() else byteArrayOf(1)
            Response.Builder().request(request).protocol(Protocol.HTTP_1_1).code(200).message("OK")
                .body(data.toResponseBody("application/octet-stream".toMediaType())).build()
        }.build()

        fun assertBody() {
            for (body in bodies) {
                if (mode == TransportMode.BINARY) assertContentEquals(byteArrayOf(1, 0, 1, 14, 0), body)
                else assertEquals("{\"\$api\":\"read\",\"value\":7}", body.decodeToString())
            }
        }
    }
}
