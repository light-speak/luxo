package com.luxo.client

import kotlinx.coroutines.*
import kotlinx.serialization.json.*
import okhttp3.*
import okio.ByteString
import kotlin.test.*

class WebSocketPrecisionTest {
    @Test
    fun `queued binary call owns its payload after caller mutation`() = runBlocking {
        for (size in listOf(0, 1, 1024, 72 * 1024)) withSocket { transport, socket ->
            transport.setMode(TransportMode.BINARY)
            val schema = APISchemaEntry(1, listOf(ParamSchema(1, "data", "Bytes")))
            transport.setSchema(mapOf("echo" to schema))
            val data = ByteArray(size) { it.toByte() }
            val params = mutableMapOf<String, Any?>("data" to data)
            val expected = LuxoBinaryProtocol.callFrame(1, LuxoBinaryProtocol.encodeRequest(schema, params))
            val frames = mutableListOf<ByteString>()
            socket.onBinary = { frames += it }
            val pending = async(start = CoroutineStart.UNDISPATCHED) { transport.call("echo", params) }
            data.fill(42)
            params.clear()
            assertContentEquals(expected, frames.single().toByteArray())
            respond(socket, TransportMode.BINARY, 1u, 7)
            assertResult(7, pending.await())
        }
    }

    @Test
    fun `JSON and binary preserve unsigned IDs for out of order responses`() = runBlocking {
        val starts = listOf(0uL, 126uL, 9007199254740990uL, Long.MAX_VALUE.toULong() - 1u, ULong.MAX_VALUE - 2u)
        for (mode in TransportMode.entries) for (start in starts) {
            withSocket(start) { transport, socket ->
                transport.setMode(mode)
                transport.setSchema(mapOf("echo" to APISchemaEntry(1, listOf(ParamSchema(1, "value", "Int")))))
                val sent = captureRequestIDs(socket)
                coroutineScope {
                    val first = async(start = CoroutineStart.UNDISPATCHED) { transport.call("echo", mapOf("value" to Long.MIN_VALUE)) }
                    val second = async(start = CoroutineStart.UNDISPATCHED) { transport.call("echo", mapOf("value" to Long.MIN_VALUE)) }
                    assertEquals(listOf((start + 1u).toString(), (start + 2u).toString()), sent)
                    respond(socket, mode, start + 2u, 2)
                    respond(socket, mode, start + 1u, 1)
                    assertResult(1, first.await())
                    assertResult(2, second.await())
                }
            }
        }
    }

    @Test
    fun `exhaustion never wraps or overwrites the last pending call`() = runBlocking {
        for (mode in TransportMode.entries) withSocket(ULong.MAX_VALUE - 1u) { transport, socket ->
            transport.setMode(mode)
            transport.setSchema(mapOf("echo" to APISchemaEntry(1)))
            val sent = captureRequestIDs(socket)
            coroutineScope {
                val last = async(start = CoroutineStart.UNDISPATCHED) { transport.call("echo") }
                repeat(2) {
                    assertEquals("RequestIDExhausted", assertFailsWith<LuxoError> { transport.call("echo") }.error)
                }
                assertEquals(listOf(ULong.MAX_VALUE.toString()), sent)
                respond(socket, mode, ULong.MAX_VALUE, 7)
                assertResult(7, last.await())
            }
        }
    }

    @Test
    fun `unsigned maximum error ID still resolves the correct call`() = runBlocking {
        for (mode in TransportMode.entries) withSocket(ULong.MAX_VALUE - 1u) { transport, socket ->
            transport.setMode(mode)
            transport.setSchema(mapOf("echo" to APISchemaEntry(1)))
            socket.onText = {
                socket.receive("""{"${'$'}id":-1,"data":1}""")
                socket.receive("""{"${'$'}id":18446744073709551615,"error":"NotFound","code":404,"message":"missing"}""")
            }
            socket.onBinary = {
                val encoder = LuxoEncoder()
                encoder.writeVarint(LuxoBinaryProtocol.CALL_ERROR.toLong())
                encoder.writeVarint(-1L)
                encoder.writeFieldInt(1, 404)
                encoder.writeFieldString(2, "NotFound")
                encoder.writeFieldString(3, "missing")
                encoder.writeEnd()
                socket.receive(encoder.bytes())
            }
            assertEquals("NotFound", assertFailsWith<LuxoError> { transport.call("echo") }.error)
        }
    }

    @Test
    fun `parameters cannot override JSON call metadata`() = runBlocking {
        withSocket { transport, socket ->
            socket.onText = { text ->
                val request = Json.parseToJsonElement(text).jsonObject
                assertEquals("echo", request.getValue("\$api").jsonPrimitive.content)
                assertEquals("1", request.getValue("\$id").jsonPrimitive.content)
                socket.receive("""{"${'$'}id":1,"data":1}""")
            }
            assertResult(1, transport.call("echo", mapOf("\$id" to 42L, "\$api" to "wrong")))
        }
    }

    @Test
    fun `malformed JSON IDs never consume a pending call`() = runBlocking {
        val invalid = listOf("true", "false", "null", "-1", "-0", "1.0", "1e0", "\"1\"", "[]", "{}", "18446744073709551616", "+1", "01", "0١")
        for (id in invalid) withSocket { transport, socket ->
            socket.onText = {
                socket.receive("""{"${'$'}id":$id,"data":1}""")
                socket.receive("""{"data":1}""")
                socket.receive("""{"${'$'}id":1,"data":2}""")
            }
            assertResult(2, transport.call("echo"))
        }
    }

    @Test
    fun `binary frame tags and IDs reject malformed encodings`() = runBlocking {
        val invalid = listOf(
            byteArrayOf(), byteArrayOf(2), byteArrayOf(2, 0x81.toByte()),
            byteArrayOf(0x82.toByte(), 0, 1, 1),
            byteArrayOf(2) + ByteArray(9) { 0x80.toByte() } + byteArrayOf(2, 1),
            byteArrayOf(2) + ByteArray(10) { 0x80.toByte() } + byteArrayOf(0, 1),
        )
        for (bytes in invalid) withSocket { transport, socket ->
            transport.setMode(TransportMode.BINARY)
            transport.setSchema(mapOf("echo" to APISchemaEntry(1)))
            socket.onBinary = {
                socket.receive(bytes)
                respond(socket, TransportMode.BINARY, 1u, 2)
            }
            assertResult(2, transport.call("echo"))
        }
    }

    @Test
    fun `binary control tags never consume a pending unary call`() = runBlocking {
        for (start in listOf(0uL, ULong.MAX_VALUE - 1u)) withSocket(start) { transport, socket ->
            transport.setMode(TransportMode.BINARY)
            transport.setSchema(mapOf("echo" to APISchemaEntry(1)))
            val pending = async(start = CoroutineStart.UNDISPATCHED) { transport.call("echo") }
            for (tag in 0..255) {
                if (tag == LuxoBinaryProtocol.CALL_SUCCESS || tag == LuxoBinaryProtocol.CALL_ERROR) continue
                val frame = LuxoEncoder().apply {
                    writeRawBytes(byteArrayOf(tag.toByte()))
                    writeVarint((start + 1u).toLong())
                }.bytes()
                socket.receive(frame)
                yield()
                assertFalse(pending.isCompleted, "control tag $tag consumed unary call")
            }
            respond(socket, TransportMode.BINARY, start + 1u, 7)
            assertResult(7, pending.await())
        }
    }

    @Test
    fun `subscription success requires an exact acknowledgement`() = runBlocking {
        for (mode in TransportMode.entries) withSocket { transport, socket ->
            transport.setMode(mode)
            transport.setSchema(mapOf("watch" to APISchemaEntry(1)))
            coroutineScope {
                val subscription = async(start = CoroutineStart.UNDISPATCHED) { transport.subscribe("watch", { it }) {} }
                if (mode == TransportMode.JSON) {
                    for (ok in listOf("false", "1", "\"true\"", "null", "{}")) {
                        socket.receive("""{"${'$'}sub":1,"ok":$ok}""")
                    }
                    socket.receive("""{"${'$'}sub":1}""")
                } else {
                    socket.receive(byteArrayOf(0x13, 1, 0))
                    socket.receive(byteArrayOf(0x93.toByte(), 0, 1))
                }
                yield()
                assertFalse(subscription.isCompleted)
                if (mode == TransportMode.JSON) socket.receive("""{"${'$'}sub":1,"ok":true}""")
                else socket.receive(byteArrayOf(0x13, 1))
                subscription.await().invoke()
            }
        }
    }

    @Test
    fun `correlated JSON errors fail promptly without discarding the next call`() = runBlocking {
        withSocket { transport, socket ->
            for (code in listOf("2147483648", "4294967696", "1.5", "\"400\"", "null", "{}")) {
                socket.onText = { request ->
                    val id = Json.parseToJsonElement(request).jsonObject.getValue("\$id")
                    socket.receive("""{"${'$'}id":$id,"error":"Internal","code":$code,"message":"failure"}""")
                }
                assertEquals("ParseError", assertFailsWith<LuxoError> { transport.call("echo") }.error)
            }
            socket.onText = { request ->
                val id = Json.parseToJsonElement(request).jsonObject.getValue("\$id")
                socket.receive("""{"${'$'}id":$id,"data":9223372036854775807}""")
            }
            assertEquals(Long.MAX_VALUE, (transport.call("echo") as JsonPrimitive).long)
        }
    }

    @Test
    fun `JSON subscription errors allow retry and stream full width integers`() = runBlocking {
        withSocket { transport, socket ->
            socket.onText = {
                socket.receive("""{"${'$'}sub":1,"error":"Internal","code":4294967696,"message":"failure"}""")
            }
            assertEquals("ParseError", assertFailsWith<LuxoError> { transport.subscribe("watch", { it }) {} }.error)
            socket.onText = { socket.receive("""{"${'$'}sub":2,"ok":true}""") }
            var received: Any? = null
            val unsubscribe = transport.subscribe("watch", { it }) { received = it }
            socket.receive("""{"${'$'}stream":2,"data":-9223372036854775808}""")
            assertEquals(Long.MIN_VALUE, (received as JsonPrimitive).long)
            unsubscribe()
        }
    }

    @Test
    fun `both WebSocket modes reject fractional integers before sending calls or subscriptions`() = runBlocking {
        withSocket { transport, socket ->
            transport.setSchema(mapOf("echo" to APISchemaEntry(1, listOf(ParamSchema(1, "value", "Int")))))
            for (mode in TransportMode.entries) {
                transport.setMode(mode)
                assertEquals("ConfigError", assertFailsWith<LuxoError> { transport.call("echo", mapOf("value" to 1.5)) }.error)
                assertEquals("ConfigError", assertFailsWith<LuxoError> { transport.subscribe("echo", { it }, mapOf("value" to 1.5)) {} }.error)
            }
            assertEquals(0, socket.sent)
        }
    }

    @Test
    fun `binary WebSocket errors never wrap large codes`() = runBlocking {
        withSocket { transport, socket ->
            transport.setMode(TransportMode.BINARY)
            transport.setSchema(mapOf("echo" to APISchemaEntry(1)))
            socket.onBinary = { request ->
                val decoder = LuxoDecoder(request.toByteArray())
                decoder.readVarint()
                val encoder = LuxoEncoder()
                encoder.writeVarint(LuxoBinaryProtocol.CALL_ERROR.toLong())
                encoder.writeVarint(decoder.readVarint())
                encoder.writeFieldInt(1, 4294967696L)
                encoder.writeFieldString(2, "Internal")
                encoder.writeFieldString(3, "failure")
                encoder.writeEnd()
                socket.receive(encoder.bytes())
            }
            assertEquals("ParseError", assertFailsWith<LuxoError> { transport.call("echo") }.error)
        }
    }

    @Test
    fun `wide binary frame types are not mistaken for successful calls`() = runBlocking {
        withSocket { transport, socket ->
            transport.setMode(TransportMode.BINARY)
            transport.setSchema(mapOf("echo" to APISchemaEntry(1)))
            socket.onBinary = { request ->
                val decoder = LuxoDecoder(request.toByteArray())
                decoder.readVarint()
                val id = decoder.readVarint()
                for (type in listOf(4294967298L, LuxoBinaryProtocol.CALL_SUCCESS.toLong())) {
                    val encoder = LuxoEncoder()
                    encoder.writeVarint(type)
                    encoder.writeVarint(id)
                    encoder.writeRawBytes(byteArrayOf(if (type > 255) 1 else 2))
                    socket.receive(encoder.bytes())
                }
            }
            assertContentEquals(byteArrayOf(2), transport.call("echo") as ByteArray)
        }
    }

    @Test
    fun `quoted JSON request IDs cannot resolve a numeric request`() = runBlocking {
        withSocket { transport, socket ->
            socket.onText = { request ->
                val id = Json.parseToJsonElement(request).jsonObject.getValue("\$id")
                socket.receive("""{"${'$'}id":"$id","data":1}""")
                socket.receive("""{"${'$'}id":$id,"data":2}""")
            }
            assertEquals(2L, (transport.call("echo") as JsonPrimitive).long)
        }
    }

    private fun captureRequestIDs(socket: TestSocket): MutableList<String> {
        val ids = mutableListOf<String>()
        socket.onText = { text ->
            val request = Json.parseToJsonElement(text).jsonObject
            val id = request.getValue("\$id").jsonPrimitive
            assertFalse(id.isString)
            ids += id.content
            request["value"]?.let { assertEquals(Long.MIN_VALUE, it.jsonPrimitive.long) }
        }
        socket.onBinary = { bytes ->
            val decoder = LuxoDecoder(bytes.toByteArray())
            assertEquals(LuxoBinaryProtocol.CALL_REQUEST.toLong(), decoder.readVarint())
            ids += decoder.readVarint().toULong().toString()
        }
        return ids
    }

    private fun respond(socket: TestSocket, mode: TransportMode, id: ULong, value: Int) {
        if (mode == TransportMode.JSON) {
            socket.receive("""{"${'$'}id":$id,"data":$value}""")
        } else {
            val encoder = LuxoEncoder()
            encoder.writeVarint(LuxoBinaryProtocol.CALL_SUCCESS.toLong())
            encoder.writeVarint(id.toLong())
            encoder.writeRawBytes(byteArrayOf(value.toByte()))
            socket.receive(encoder.bytes())
        }
    }

    private fun assertResult(expected: Int, actual: Any) {
        when (actual) {
            is JsonPrimitive -> assertEquals(expected.toLong(), actual.long)
            is ByteArray -> assertContentEquals(byteArrayOf(expected.toByte()), actual)
            else -> fail("unexpected result: $actual")
        }
    }

    private suspend fun withSocket(initialSequence: ULong = 0uL, block: suspend (LuxoWebSocket, TestSocket) -> Unit) {
        val client = TestSocketClient()
        val transport = LuxoWebSocket("ws://localhost/luvia", client, initialSequence, timeoutMillis = 500)
        try {
            transport.connect()
            client.socket.open()
            block(transport, client.socket)
        } finally {
            transport.close()
            client.dispatcher.executorService.shutdownNow()
            client.connectionPool.evictAll()
        }
    }
}

internal class TestSocketClient : OkHttpClient() {
    @Volatile lateinit var socket: TestSocket
    override fun newWebSocket(request: Request, listener: WebSocketListener): WebSocket =
        TestSocket(request, listener).also { socket = it }
}

/** Deterministic transport boundary: exercise public calls, not private reflection. */
internal class TestSocket(private val request: Request, private val listener: WebSocketListener) : WebSocket {
    var onText: (String) -> Unit = {}
    var onBinary: (ByteString) -> Unit = {}
    var sent = 0
    var acceptSend = true
    var closes = 0

    fun open() = listener.onOpen(this, Response.Builder().request(request).protocol(Protocol.HTTP_1_1).code(101).message("upgrade").build())
    fun receive(text: String) = listener.onMessage(this, text)
    fun receive(bytes: ByteArray) = listener.onMessage(this, ByteString.of(*bytes))
    fun receive(bytes: ByteString) = listener.onMessage(this, bytes)
    fun disconnected() = listener.onClosed(this, 1006, "lost")
    fun failed() = listener.onFailure(this, java.io.IOException("lost"), null)
    override fun request(): Request = request
    override fun queueSize(): Long = 0
    override fun send(text: String): Boolean { sent++; onText(text); return acceptSend }
    override fun send(bytes: ByteString): Boolean { sent++; onBinary(bytes); return acceptSend }
    override fun close(code: Int, reason: String?): Boolean { closes++; return true }
    override fun cancel() {}
}
