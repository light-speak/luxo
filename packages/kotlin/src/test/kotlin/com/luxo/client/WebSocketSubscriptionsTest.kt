package com.luxo.client

import kotlinx.coroutines.*
import kotlinx.serialization.json.*
import java.util.Collections
import kotlin.test.*

class WebSocketSubscriptionsTest {
    @Test
    fun registrationOwnsMetadataAndInvalidReplacementKeepsSubscriptionsUsable() = runBlocking {
        for (mode in TransportMode.entries) withSocket { transport, socket ->
            transport.setMode(mode)
            val params = mutableListOf(ParamSchema(1, "value", "Int"))
            val source = mutableMapOf("watch" to APISchemaEntry(77, params))
            transport.setSchema(source)
            params[0] = ParamSchema(2, "value", "String")
            source.clear()
            assertFailsWith<IllegalArgumentException> {
                transport.setSchema(mapOf("watch" to APISchemaEntry(0)))
            }
            val ids = recordSubscriptions(socket, mode)
            val cancel = transport.subscribe("watch", { it }, mapOf("value" to 7L)) {}
            assertEquals(listOf(1uL), ids)
            assertEquals(0, socket.closes)
            cancel()
        }
    }

    @Test
    fun `queued binary subscribe and cancel retain original bytes`() = runBlocking {
        withSocket { transport, socket ->
            transport.setMode(TransportMode.BINARY)
            val schema = APISchemaEntry(77, listOf(ParamSchema(1, "data", "Bytes")))
            transport.setSchema(mapOf("watch" to schema))
            val data = ByteArray(1024) { it.toByte() }
            val params = mutableMapOf<String, Any?>("data" to data)
            val expected = LuxoBinaryProtocol.subscribeFrame(1, LuxoBinaryProtocol.encodeRequest(schema, params))
            val frames = mutableListOf<okio.ByteString>()
            socket.onBinary = { frames += it }
            val waiting = async(start = CoroutineStart.UNDISPATCHED) { transport.subscribe("watch", { it }, params) {} }
            data.fill(42)
            params.clear()
            assertContentEquals(expected, frames.single().toByteArray())
            ack(socket, TransportMode.BINARY, 1u)
            val cancel = waiting.await()
            cancel()
            assertContentEquals(expected, frames[0].toByteArray())
            assertContentEquals(LuxoBinaryProtocol.unsubscribeFrame(1), frames[1].toByteArray())
            cancel()
            assertEquals(2, frames.size)
        }
    }

    @Test
    fun `same API subscriptions use independent full width IDs and reverse ACKs`() = runBlocking {
        val starts = listOf(0uL, 9007199254740990uL, Long.MAX_VALUE.toULong() - 1u, ULong.MAX_VALUE - 2u)
        for (mode in TransportMode.entries) for (start in starts) withSocket(start) { transport, socket ->
            transport.setMode(mode)
            val ids = recordSubscriptions(socket)
            val values = mutableListOf<Int>()
            coroutineScope {
                val first = async(start = CoroutineStart.UNDISPATCHED) { transport.subscribe("watch", { it }) { values += value(it) } }
                val second = async(start = CoroutineStart.UNDISPATCHED) { transport.subscribe("watch", { it }) { values += value(it) + 10 } }
                assertEquals(listOf(start + 1u, start + 2u), ids)
                data(socket, mode, start + 1u, 9)
                assertTrue(values.isEmpty(), "data before ACK must not be delivered")
                ack(socket, mode, start + 2u)
                ack(socket, mode, start + 1u)
                val cancelFirst = first.await()
                val cancelSecond = second.await()
                data(socket, mode, start + 2u, 2)
                data(socket, mode, start + 1u, 1)
                assertEquals(listOf(12, 1), values)
                cancelFirst()
                cancelSecond()
            }
        }
    }

    @Test
    fun `late frames and repeated cancel cannot affect a newer subscription`() = runBlocking {
        for (mode in TransportMode.entries) withSocket { transport, socket ->
            transport.setMode(mode)
            autoAcknowledge(socket, mode)
            var received = 0
            val old = transport.subscribe("watch", { it }) { received = -1 }
            old()
            val current = transport.subscribe("watch", { it }) { received = value(it) }
            val sent = socket.sent
            old()
            assertEquals(sent, socket.sent)
            data(socket, mode, 1u, 9)
            failure(socket, mode, 1u)
            data(socket, mode, 2u, 2)
            assertEquals(2, received)
            current()
        }
    }

    @Test
    fun `subscription mode and numeric cancellation survive transport mode changes`() = runBlocking {
        for (mode in TransportMode.entries) withSocket(ULong.MAX_VALUE - 1u) { transport, socket ->
            transport.setMode(mode)
            autoAcknowledge(socket, mode)
            var received = 0
            val cancel = transport.subscribe("watch", { it }) { received = value(it) }
            transport.setMode(if (mode == TransportMode.JSON) TransportMode.BINARY else TransportMode.JSON)
            data(socket, transportModeOpposite(mode), ULong.MAX_VALUE, 9)
            assertEquals(0, received)
            data(socket, mode, ULong.MAX_VALUE, 2)
            assertEquals(2, received)
            socket.onText = { assertEquals("""{"${'$'}unsub":18446744073709551615}""", it) }
            socket.onBinary = { assertContentEquals(frame(0x11, ULong.MAX_VALUE), it.toByteArray()) }
            cancel()
            assertEquals("RequestIDExhausted", assertFailsWith<LuxoError> { transport.subscribe("watch", { it }) {} }.error)
        }
    }

    @Test
    fun `concurrent subscriptions enqueue in strictly increasing unsigned order`() = runBlocking {
        for (mode in TransportMode.entries) withSocket(Long.MAX_VALUE.toULong() - 64u) { transport, socket ->
            transport.setMode(mode)
            val ids = recordSubscriptions(socket, mode)
            val cancellations = (1..128).map {
                async(Dispatchers.Default) { transport.subscribe("watch", { it }) {} }
            }.awaitAll()
            assertEquals(128, ids.size)
            assertEquals(ids.sorted(), ids)
            assertEquals(128, ids.distinct().size)
            cancellations.forEach { it() }
        }
    }

    @Test
    fun `timeout and caller cancellation send one unsubscribe and permit retry`() = runBlocking {
        for (mode in TransportMode.entries) withSocket(timeoutMillis = 100) { transport, socket ->
            transport.setMode(mode)
            val cancellations = mutableListOf<ULong>()
            socket.onText = { text -> Json.parseToJsonElement(text).jsonObject["\$unsub"]?.let { cancellations += it.toString().toULong() } }
            socket.onBinary = { bytes ->
                val decoder = LuxoDecoder(bytes.toByteArray())
                if (decoder.readVarint() == 0x11L) cancellations += decoder.readVarint().toULong()
            }
            assertEquals("TimeoutError", assertFailsWith<LuxoError> { transport.subscribe("watch", { it }) {} }.error)
            val waiting = launch(start = CoroutineStart.UNDISPATCHED) { transport.subscribe("watch", { it }) {} }
            waiting.cancelAndJoin()
            assertEquals(listOf(1uL, 2uL), cancellations)
            autoAcknowledge(socket, mode)
            transport.subscribe("watch", { it }) {}.invoke()
        }
    }

    @Test
    fun `retired frames and malformed identifiers cannot acknowledge or deliver`() = runBlocking {
        for (mode in TransportMode.entries) withSocket { transport, socket ->
            transport.setMode(mode)
            var received = 0
            val waiting = async(start = CoroutineStart.UNDISPATCHED) { transport.subscribe("watch", { it }) { received++ } }
            for (id in listOf("\"1\"", "\"watch\"", "true", "null", "-0", "1.0", "1e0", "18446744073709551616", "[]", "{}")) {
                socket.receive("""{"${'$'}sub":$id,"ok":true}""")
                socket.receive("""{"${'$'}stream":$id,"data":1}""")
            }
            for (bytes in listOf(byteArrayOf(7, 77), byteArrayOf(6, 77, 1), frame(0x13, 1u, byteArrayOf(0)), byteArrayOf(0x93.toByte(), 0, 1))) {
                socket.receive(bytes)
            }
            ack(socket, transportModeOpposite(mode), 1u)
            yield()
            assertFalse(waiting.isCompleted)
            assertEquals(0, received)
            ack(socket, mode, 1u)
            val cancel = waiting.await()
            data(socket, mode, 1u, 1)
            assertEquals(1, received)
            cancel()
        }
    }

    @Test
    fun `correlated setup errors preserve raw JSON data and allow retry`() = runBlocking {
        for (mode in TransportMode.entries) withSocket { transport, socket ->
            transport.setMode(mode)
            socket.onText = { failure(socket, mode, 1u) }
            socket.onBinary = { failure(socket, mode, 1u) }
            val error = assertFailsWith<LuxoError> { transport.subscribe("watch", { it }) {} }
            assertEquals("NotFound", error.error)
            assertEquals("123456789012345678901234567890", error.data?.jsonObject?.get("n").toString())
            autoAcknowledge(socket, mode)
            transport.subscribe("watch", { it }) {}.invoke()
        }
    }

    @Test
    fun `subscription params cannot replace reserved JSON metadata`() = runBlocking {
        withSocket { transport, socket ->
            socket.onText = { text ->
                val json = Json.parseToJsonElement(text).jsonObject
                assertEquals("1", json["\$id"].toString())
                assertEquals(JsonPrimitive("watch"), json["\$sub"])
                ack(socket, TransportMode.JSON, 1u)
            }
            transport.subscribe("watch", { it }, mapOf("\$id" to 42L, "\$sub" to "wrong")) {}
        }
    }

    @Test
    fun `send failure and closing fail pending setup without leaking registrations`() = runBlocking {
        for (mode in TransportMode.entries) withSocket { transport, socket ->
            transport.setMode(mode)
            socket.acceptSend = false
            assertEquals("ConnectionError", assertFailsWith<LuxoError> { transport.subscribe("watch", { it }) {} }.error)
            socket.acceptSend = true
            supervisorScope {
                val pending = async(start = CoroutineStart.UNDISPATCHED) { transport.subscribe("watch", { it }) {} }
                transport.close()
                assertEquals("ConnectionError", assertFailsWith<LuxoError> { pending.await() }.error)
            }
            assertEquals("ConnectionError", assertFailsWith<LuxoError> { transport.subscribe("watch", { it }) {} }.error)
        }
    }

    @Test
    fun `disconnect terminates subscriptions once and reconnect requires explicit new IDs`() = runBlocking {
        val start = Long.MAX_VALUE.toULong() - 1u
        for (mode in TransportMode.entries) withClient(start) { transport, client ->
            transport.setMode(mode)
            val oldSocket = client.socket
            autoAcknowledge(oldSocket, mode)
            var received = 0
            val errors = mutableListOf<LuxoError>()
            val observer = StreamObserver(onComplete = { fail("disconnect completed normally") }, onError = { errors += it })
            val first = transport.subscribe("watch", { it }, observer = observer) { received = value(it) }
            val second = transport.subscribe("watch", { it }, observer = observer) {}
            oldSocket.disconnected()
            oldSocket.failed()
            assertEquals(listOf("NetworkError", "NetworkError"), errors.map { it.error })
            transport.connect()
            val newSocket = client.socket
            assertNotSame(oldSocket, newSocket)
            val ids = recordSubscriptions(newSocket, mode)
            coroutineScope {
                val next = async(start = CoroutineStart.UNDISPATCHED) { transport.subscribe("watch", { it }) { received = value(it) } }
                newSocket.open()
                val third = next.await()
                assertEquals(listOf(start + 3u), ids)
                oldSocket.open()
                assertEquals(1, oldSocket.closes)
                oldSocket.failed()
                oldSocket.disconnected()
                data(oldSocket, mode, start + 1u, 9)
                assertEquals(0, received)
                data(newSocket, mode, start + 1u, 2)
                assertEquals(0, received)
                val sent = newSocket.sent
                first(); second()
                assertEquals(sent, newSocket.sent, "old cancel must not send on the new connection")
                data(newSocket, mode, start + 3u, 3)
                assertEquals(3, received)
                assertEquals(2, errors.size)
                first(); second(); third()
            }
        }
    }

    @Test
    fun `reconnect never sends old modes or params without a new subscription`() = runBlocking {
        withClient { transport, client ->
            val oldSocket = client.socket
            autoAcknowledge(oldSocket, TransportMode.JSON)
            val params = mutableMapOf<String, Any?>("value" to 7L)
            var errors = 0
            val cancel = transport.subscribe("watch", { it }, params, StreamObserver(onError = { errors++ })) {}
            params["value"] = 9L
            transport.setMode(TransportMode.BINARY)
            oldSocket.disconnected()
            transport.connect()
            val restored = client.socket
            restored.onText = { fail("reconnected socket replayed a JSON subscription") }
            restored.onBinary = { fail("reconnected socket replayed a Binary subscription") }
            restored.open()
            assertEquals(1, errors)
            cancel()
            assertEquals(0, restored.sent)
        }
    }

    @Test
    fun `a failed new subscription after reconnect permits another attempt without replay`() = runBlocking {
        withClient { transport, client ->
            autoAcknowledge(client.socket, TransportMode.JSON)
            val cancel = transport.subscribe("watch", { it }) {}
            client.socket.disconnected()
            transport.connect()
            val failed = client.socket
            failed.acceptSend = false
            supervisorScope {
                val waiting = async(start = CoroutineStart.UNDISPATCHED) { transport.subscribe("watch", { it }) {} }
                failed.open()
                assertEquals("ConnectionError", assertFailsWith<LuxoError> { waiting.await() }.error)
                assertEquals(0, failed.closes)
            }
            failed.acceptSend = true
            val ids = recordSubscriptions(failed, TransportMode.JSON)
            cancel()
            transport.subscribe("watch", { it }) {}.invoke()
            assertEquals(listOf(3uL), ids)
        }
    }

    @Test
    fun `disconnect fails pending ACK but a later connection can retry`() = runBlocking {
        for (failure in listOf(false, true)) withClient { transport, client ->
            supervisorScope {
                val waiting = async(start = CoroutineStart.UNDISPATCHED) { transport.subscribe("watch", { it }) {} }
                if (failure) client.socket.failed() else client.socket.disconnected()
                assertEquals("NetworkError", assertFailsWith<LuxoError> { waiting.await() }.error)
            }
            transport.connect()
            val ids = recordSubscriptions(client.socket, TransportMode.JSON)
            client.socket.open()
            assertTrue(ids.isEmpty(), "unacknowledged subscriptions are not silently retried")
            transport.subscribe("watch", { it }) {}.invoke()
            assertEquals(listOf(2uL), ids)
        }
    }

    @Test
    fun `disconnect separates unary failure pending ACK and established observer`() = runBlocking {
        for (mode in TransportMode.entries) for (parse in listOf(false, true)) withClient { transport, client ->
            transport.setMode(mode)
            val socket = client.socket
            autoAcknowledge(socket, mode)
            val errors = mutableListOf<String>()
            transport.subscribe("watch", { it }, observer = StreamObserver(onError = { errors += it.error })) {}
            socket.onText = {}; socket.onBinary = {}
            supervisorScope {
                val call = async(start = CoroutineStart.UNDISPATCHED) { transport.call("watch") }
                val pending = async(start = CoroutineStart.UNDISPATCHED) {
                    transport.subscribe("watch", { it }, observer = StreamObserver(onError = { fail("unacknowledged observer notified") })) {}
                }
                if (parse) socket.receive("{\"\$id\":2,\"data\":") else socket.failed()
                assertEquals(if (parse) "ParseError" else "ConnectionError", assertFailsWith<LuxoError> { call.await() }.error)
                assertEquals(if (parse) "ParseError" else "NetworkError", assertFailsWith<LuxoError> { pending.await() }.error)
            }
            assertEquals(listOf(if (parse) "ParseError" else "NetworkError"), errors)
            if (parse) assertEquals(1, socket.closes)
            socket.failed(); socket.disconnected()
            assertEquals(1, errors.size)
        }
    }

    @Test
    fun `malformed JSON closes socket without replaying subscriptions or damaging a new connection`() = runBlocking {
        val invalid = listOf("", "{", "[]", "[1]", "null", "1", "{\"\$id\":1} trailing", "{\"data\":\"\\uD800\"}")
        for (body in invalid) withClient { transport, client ->
            val old = client.socket
            autoAcknowledge(old, TransportMode.JSON)
            val errors = mutableListOf<String>()
            val stop = transport.subscribe("watch", { it }, observer = StreamObserver(onError = { errors += it.error })) {}
            old.receive(body)
            assertEquals(listOf("ParseError"), errors, body)
            assertEquals(1, old.closes)
            transport.connect()
            val next = client.socket
            assertNotSame(old, next)
            val ids = recordSubscriptions(next, TransportMode.JSON)
            next.open()
            assertTrue(ids.isEmpty())
            old.receive("{"); old.failed(); stop()
            transport.subscribe("watch", { it }) {}.invoke()
            assertEquals(listOf(2uL), ids)
            assertEquals(0, next.closes)
            assertEquals(1, errors.size)
        }
    }

    @Test
    fun `disconnect notifies all observers outside lock even when one throws`() = runBlocking {
        for (parse in listOf(false, true)) withClient { transport, client ->
            val socket = client.socket
            autoAcknowledge(socket, TransportMode.JSON)
            val expected = IllegalStateException("observer failure")
            var notified = 0
            repeat(3) {
                transport.subscribe("watch", { it }, observer = StreamObserver(onError = {
                    assertEquals(if (parse) "ParseError" else "NetworkError", it.error)
                    notified++
                    runBlocking { withTimeout(1_000) { withContext(Dispatchers.Default) { transport.close() } } }
                    throw expected
                })) {}
            }
            assertSame(expected, assertFailsWith<IllegalStateException> {
                if (parse) socket.receive("{") else socket.failed()
            })
            if (parse) assertEquals(1, socket.closes)
            assertEquals(3, notified)
            socket.disconnected()
            assertEquals(3, notified)
        }
    }

    @Test
    fun `disconnect preserves distinct callback failures without skipping observers`() = runBlocking {
        withClient { transport, client ->
            autoAcknowledge(client.socket, TransportMode.JSON)
            val thrown = mutableListOf<IllegalStateException>()
            repeat(3) {
                transport.subscribe("watch", { it }, observer = StreamObserver(onError = {
                    val failure = IllegalStateException("observer ${thrown.size}")
                    thrown += failure
                    throw failure
                })) {}
            }
            val failure = assertFailsWith<IllegalStateException> { client.socket.disconnected() }
            assertEquals(3, thrown.size)
            assertSame(thrown.first(), failure)
            assertEquals(thrown.drop(1), failure.suppressed.toList())
            client.socket.failed()
            assertEquals(3, thrown.size)
        }
    }

    @Test
    fun `explicit close silently retires established subscriptions`() = runBlocking {
        for (mode in TransportMode.entries) withSocket { transport, socket ->
            transport.setMode(mode)
            autoAcknowledge(socket, mode)
            val observer = StreamObserver(
                onComplete = { fail("explicit close completed a subscription") },
                onError = { fail("explicit close failed a subscription") },
            )
            val cancel = transport.subscribe("watch", { it }, observer = observer) { fail("data after close") }
            transport.close()
            socket.failed()
            socket.disconnected()
            data(socket, mode, 1u, 1)
            cancel()
            assertEquals(1, socket.sent)
        }
    }

    @Test
    fun `concurrent completion and disconnect publish only one terminal result`() = runBlocking {
        for (mode in TransportMode.entries) repeat(32) { withSocket { transport, socket ->
            transport.setMode(mode)
            autoAcknowledge(socket, mode)
            val terminal = java.util.concurrent.atomic.AtomicInteger()
            transport.subscribe("watch", { it }, observer = StreamObserver(
                onComplete = { terminal.incrementAndGet() },
                onError = { assertEquals("NetworkError", it.error); terminal.incrementAndGet() },
            )) {}
            coroutineScope {
                launch(Dispatchers.Default) { socket.failed() }
                launch(Dispatchers.Default) {
                    if (mode == TransportMode.JSON) socket.receive("""{"${'$'}end":1,"ok":true}""")
                    else socket.receive(frame(0x15, 1u))
                }
            }
            assertEquals(1, terminal.get())
        } }
    }

    @Test
    fun `concurrent disconnect and cancellation notify each subscription at most once`() = runBlocking {
        repeat(32) { withClient { transport, client ->
            val socket = client.socket
            autoAcknowledge(socket, TransportMode.JSON)
            val notified = java.util.concurrent.atomic.AtomicInteger()
            val cancel = transport.subscribe("watch", { it }, observer = StreamObserver(onError = {
                assertEquals("NetworkError", it.error); notified.incrementAndGet()
            })) {}
            coroutineScope {
                repeat(8) { launch(Dispatchers.Default) { socket.failed() } }
                launch(Dispatchers.Default) { cancel() }
            }
            assertTrue(notified.get() <= 1)
            socket.disconnected()
            assertTrue(notified.get() <= 1)
        } }
    }

    @Test
    fun `close while connecting rejects setup and closes a late socket`() = runBlocking {
        val client = TestSocketClient()
        val transport = LuxoWebSocket("ws://localhost/luvia", client, 0u, 2_000)
        try {
            supervisorScope {
                val waiting = async(start = CoroutineStart.UNDISPATCHED) { transport.subscribe("watch", { it }) {} }
                transport.close()
                client.socket.open()
                assertEquals("ConnectionError", assertFailsWith<LuxoError> { waiting.await() }.error)
                assertEquals(2, client.socket.closes)
            }
        } finally {
            transport.close()
            client.dispatcher.executorService.shutdownNow()
            client.connectionPool.evictAll()
        }
    }

    @Test
    fun `automatic reconnect restores only the connection and not subscriptions`() = runBlocking {
        val client = TestSocketClient()
        val transport = LuxoWebSocket("ws://localhost/luvia", client, autoReconnect = true, maxReconnectDelayMs = 1)
        try {
            transport.connect()
            val first = client.socket
            first.open()
            autoAcknowledge(first, TransportMode.JSON)
            var errors = 0
            val cancel = transport.subscribe("watch", { it }, observer = StreamObserver(onError = {
                assertEquals("NetworkError", it.error); errors++
            })) {}
            first.failed()
            withTimeout(2_000) { while (client.socket === first) delay(1) }
            val second = client.socket
            val ids = recordSubscriptions(second, TransportMode.JSON)
            second.open()
            assertTrue(ids.isEmpty())
            assertEquals(1, errors)
            cancel()
            assertEquals(0, second.sent)
            transport.subscribe("watch", { it }) {}.invoke()
            assertEquals(listOf(2uL), ids)
        } finally {
            transport.close()
            client.dispatcher.executorService.shutdownNow()
            client.connectionPool.evictAll()
        }
    }

    @Test
    fun `missing schema rejects binary setup before any send`() = runBlocking {
        withSocket { transport, socket ->
            transport.setMode(TransportMode.BINARY)
            transport.setSchema(emptyMap())
            assertEquals("ConfigError", assertFailsWith<LuxoError> { transport.subscribe("watch", { it }) {} }.error)
            assertEquals(0, socket.sent)
            transport.setSchema(mapOf("watch" to APISchemaEntry(77)))
            autoAcknowledge(socket, TransportMode.BINARY)
            transport.subscribe("watch", { it }) {}.invoke()
        }
    }

    @Test
    fun `closing concurrently with registrations resolves every waiter`() = runBlocking {
        repeat(50) {
            withSocket { transport, socket ->
                autoAcknowledge(socket, TransportMode.JSON)
                val outcomes = (1..16).map {
                    async(Dispatchers.Default) {
                        try {
                            transport.subscribe("watch", { it }) {}.invoke()
                            "success"
                        } catch (error: LuxoError) {
                            error.error
                        }
                    }
                }
                withContext(Dispatchers.Default) { transport.close() }
                assertTrue(outcomes.awaitAll().all { it == "success" || it == "ConnectionError" })
            }
        }
    }

    private suspend fun withSocket(
        start: ULong = 0uL,
        timeoutMillis: Long = 2_000,
        block: suspend (LuxoWebSocket, TestSocket) -> Unit,
    ) = withClient(start, timeoutMillis) { transport, client -> block(transport, client.socket) }

    private suspend fun withClient(
        start: ULong = 0uL,
        timeoutMillis: Long = 2_000,
        block: suspend (LuxoWebSocket, TestSocketClient) -> Unit,
    ) {
        val client = TestSocketClient()
        val transport = LuxoWebSocket("ws://localhost/luvia", client, start, timeoutMillis)
        transport.setSchema(mapOf("watch" to APISchemaEntry(77)))
        try {
            transport.connect()
            client.socket.open()
            block(transport, client)
        } finally {
            transport.close()
            client.dispatcher.executorService.shutdownNow()
            client.connectionPool.evictAll()
        }
    }

    private fun recordSubscriptions(socket: TestSocket, acknowledge: TransportMode? = null): MutableList<ULong> {
        val ids = Collections.synchronizedList(mutableListOf<ULong>())
        socket.onText = { text ->
            val json = Json.parseToJsonElement(text).jsonObject
            if (json.containsKey("\$sub")) {
                assertEquals(JsonPrimitive("watch"), json["\$sub"])
                val id = json.getValue("\$id").toString().toULong()
                ids += id
                acknowledge?.let { ack(socket, it, id) }
            }
        }
        socket.onBinary = { bytes ->
            val decoder = LuxoDecoder(bytes.toByteArray())
            if (decoder.readVarint() == 0x10L) {
                val id = decoder.readVarint().toULong()
                assertEquals(77L, decoder.readVarint())
                ids += id
                acknowledge?.let { ack(socket, it, id) }
            }
        }
        return ids
    }

    private fun autoAcknowledge(socket: TestSocket, mode: TransportMode) { recordSubscriptions(socket, mode) }
    private fun transportModeOpposite(mode: TransportMode) = if (mode == TransportMode.JSON) TransportMode.BINARY else TransportMode.JSON
    private fun value(data: Any) = if (data is JsonPrimitive) data.int else (data as ByteArray)[0].toInt()
    private fun ack(socket: TestSocket, mode: TransportMode, id: ULong) {
        if (mode == TransportMode.JSON) socket.receive("""{"${'$'}sub":$id,"ok":true}""")
        else socket.receive(frame(0x13, id))
    }
    private fun data(socket: TestSocket, mode: TransportMode, id: ULong, value: Int) {
        if (mode == TransportMode.JSON) socket.receive("""{"${'$'}stream":$id,"data":$value}""")
        else socket.receive(frame(0x12, id, byteArrayOf(value.toByte())))
    }
    private fun failure(socket: TestSocket, mode: TransportMode, id: ULong) {
        if (mode == TransportMode.JSON) socket.receive("""{"${'$'}sub":$id,"error":"NotFound","code":404,"message":"missing","data":{"n":123456789012345678901234567890}}""")
        else {
            val error = LuxoEncoder()
            error.writeFieldInt(1, 404)
            error.writeFieldString(2, "NotFound")
            error.writeFieldString(3, "missing")
            error.writeFieldString(5, """{"n":123456789012345678901234567890}""")
            error.writeEnd()
            socket.receive(frame(0x14, id, error.bytes()))
        }
    }
    private fun frame(type: Int, id: ULong, payload: ByteArray = byteArrayOf()): ByteArray {
        val encoder = LuxoEncoder()
        encoder.writeVarint(type.toLong())
        encoder.writeVarint(id.toLong())
        encoder.writeRawBytes(payload)
        return encoder.bytes()
    }
}
