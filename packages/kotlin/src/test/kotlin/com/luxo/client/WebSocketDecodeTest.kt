package com.luxo.client

import kotlinx.coroutines.*
import kotlinx.serialization.json.*
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.*

class WebSocketDecodeTest {
    @Test
    fun generatedUnaryEncodingCancellationDoesNotSend() = runBlocking {
        for (mode in TransportMode.entries) withSocket(mode) { transport, socket ->
            verifyEncodingCancellation(transport, socket, subscription = false)
        }
    }

    @Test
    fun generatedSubscriptionEncodingCancellationDoesNotSend() = runBlocking {
        for (mode in TransportMode.entries) withSocket(mode) { transport, socket ->
            verifyEncodingCancellation(transport, socket, subscription = true)
        }
    }

    private suspend fun verifyEncodingCancellation(transport: LuxoWebSocket, socket: TestSocket, subscription: Boolean) = coroutineScope {
        val sent = socket.sent
        val pending = async(start = CoroutineStart.UNDISPATCHED) {
            val context = currentCoroutineContext()
            val request = object : GeneratedRequest {
                override val apiID = 77
                override fun writeParameters(encoder: LuxoEncoder) { context.cancel() }
                override fun toJSONParameters(): Map<String, Any?> { context.cancel(); return emptyMap() }
            }
            if (subscription) transport.subscribe("watch", { it }, request) { fail("cancelled handler ran") }
            else transport.call("watch", request)
        }
        assertFailsWith<CancellationException> { pending.await() }
        assertEquals(sent, socket.sent, "request cancelled during encoding reached socket")
        assertEquals(7L, integer(transport.call("watch")))
        assertEquals(sent + 1, socket.sent, "cancelled encoding broke the next call")
        assertEquals(0, socket.closes)
    }

    @Test
    fun missingBinaryDataRetiresOnlyItsOperation() = runBlocking {
        withSocket(TransportMode.BINARY) { transport, socket ->
            val errors = mutableListOf<LuxoError>()
            val values = mutableListOf<Any>()
            val peers = mutableListOf<Any>()
            var done = 0
            var decoded = 0
            val decoder: (Any) -> Any = { decoded++; it }
            val stop = transport.subscribe("watch", decoder, observer = StreamObserver({ done++ }, { errors += it })) { values += it }
            val stopPeer = transport.subscribe("watch", decoder) { peers += it }
            socket.receive(frame(0x12, 1, byteArrayOf(0)))
            repeat(2) { socket.receive(frame(0x12, 1)) }
            finish(socket, TransportMode.BINARY, 1)
            socket.receive(frame(0x12, 2, byteArrayOf(0)))
            assertEquals(listOf(listOf<Byte>(0)), values.map { (it as ByteArray).toList() })
            assertEquals(listOf(listOf<Byte>(0)), peers.map { (it as ByteArray).toList() })
            assertEquals("ParseError", errors.single().error)
            assertEquals(0, done)
            assertEquals(2, decoded)
            val normal = socket.onBinary
            socket.onBinary = { bytes ->
                val dec = LuxoDecoder(bytes.toByteArray())
                if (dec.readVarint() == 1L) socket.receive(frame(2, dec.readVarint()))
            }
            assertEquals("ParseError", assertFailsWith<LuxoError> { transport.call("watch") }.error)
            socket.onBinary = normal
            assertEquals(7L, integer(transport.call("watch")))
            assertEquals(0, socket.closes)
            stop(); stopPeer()
        }
    }

    @Test
    fun invalidJSONTokensTerminateConnectionWithoutDeliveringOverwrittenData() = runBlocking {
        for (token in listOf("NaN", "01", "1e+", "[1,]", "\"\\q\"")) withSocket(TransportMode.JSON) { transport, socket ->
            val errors = mutableListOf<LuxoError>()
            var values = 0
            var completed = 0
            repeat(2) {
                transport.subscribe("watch", { it }, observer = StreamObserver({ completed++ }, { errors += it })) { values++ }
            }
            socket.onText = {}
            val pending = async(start = CoroutineStart.UNDISPATCHED) { runCatching { transport.call("watch") } }
            val message = """{"${'$'}stream":1,"data":$token,"data":7}"""
            socket.receive(message)
            val failure = withTimeout(1_000) { pending.await() }.exceptionOrNull()
            assertEquals("ParseError", assertIs<LuxoError>(failure).error, token)
            assertEquals(listOf("ParseError", "ParseError"), errors.map { it.error }, token)
            assertEquals(0, values, token)
            assertEquals(0, completed, token)
            assertEquals(1, socket.closes, token)
            socket.receive(message)
            assertEquals(2, errors.size, "late message notified twice: $token")
        }
    }

    @Test
    fun missingJSONDataRetiresOnlyItsOperation() = runBlocking {
        withSocket(TransportMode.JSON) { transport, socket ->
            val errors = mutableListOf<LuxoError>()
            val values = mutableListOf<Any>()
            val peers = mutableListOf<Any>()
            var done = 0
            val stop = transport.subscribe("watch", { it }, observer = StreamObserver({ done++ }, { errors += it })) { values += it }
            val stopPeer = transport.subscribe("watch", { it }) { peers += it }
            socket.receive("""{"${'$'}stream":1,"data":null}""")
            repeat(2) { socket.receive("""{"${'$'}stream":1}""") }
            finish(socket, TransportMode.JSON, 1)
            socket.receive("""{"${'$'}stream":2,"data":null}""")
            assertEquals(listOf<Any>(JsonNull), values)
            assertEquals(listOf<Any>(JsonNull), peers)
            assertEquals("ParseError", errors.single().error)
            assertEquals(0, done)
            val normal = socket.onText
            socket.onText = { text ->
                val id = Json.parseToJsonElement(text).jsonObject["\$id"]
                if (id != null) socket.receive("""{"${'$'}id":$id}""")
            }
            assertEquals("ParseError", assertFailsWith<LuxoError> { transport.call("watch") }.error)
            socket.onText = normal
            assertEquals(7L, integer(transport.call("watch")))
            assertEquals(0, socket.closes)
            stop(); stopPeer()
        }
    }

    @Test
    fun malformedPayloadRetiresOnlyItsSubscription() = runBlocking {
        for (mode in TransportMode.entries) withSocket(mode) { transport, socket ->
            val errors = mutableListOf<LuxoError>()
            val values = mutableListOf<Long>()
            var done = 0
            val stop = transport.subscribe("watch", ::integer, observer = StreamObserver({ done++ }, { errors += it })) { values += it }
            var peer = 0L
            val stopPeer = transport.subscribe("watch", ::integer) { peer = it }
            data(socket, mode, 1, valid = true)
            repeat(2) { data(socket, mode, 1, valid = false) }
            data(socket, mode, 1, valid = true)
            finish(socket, mode, 1)
            stop(); stop()
            data(socket, mode, 2, valid = true)
            assertEquals(listOf(7L), values)
            assertEquals("DecodeError", errors.single().error)
            assertEquals(0, done)
            assertEquals(7L, peer)
            assertEquals(7L, integer(transport.call("watch")))
            assertEquals(0, socket.closes)
            stopPeer()
        }
    }

    @Test
    fun businessExceptionsRemainOutsideDecoderCatch() = runBlocking {
        for (mode in TransportMode.entries) withSocket(mode) { transport, socket ->
            val expected = IllegalStateException("application")
            var completed = 0
            val stop = transport.subscribe("watch", ::integer, observer = StreamObserver({ completed++ }, { fail("not a decode error") })) {
                throw expected
            }
            repeat(2) { assertSame(expected, assertFailsWith<IllegalStateException> { data(socket, mode, 1, valid = true) }) }
            finish(socket, mode, 1)
            assertEquals(1, completed)
            stop()
        }
    }

    @Test
    fun missingDecoderMessageStillProducesOneDiagnostic() = runBlocking {
        for (mode in TransportMode.entries) withSocket(mode) { transport, socket ->
            val errors = mutableListOf<LuxoError>()
            val stop = transport.subscribe<Long>("watch", { throw IllegalArgumentException() },
                observer = StreamObserver(onError = { errors += it })) { fail("unexpected data") }
            repeat(2) { data(socket, mode, 1, valid = false) }
            assertEquals("DecodeError", errors.single().error)
            assertEquals("invalid subscription payload", errors.single().message)
            stop()
        }
    }

    @Test
    fun unreadableDecoderMessageStillRetiresSubscription() = runBlocking {
        for (mode in TransportMode.entries) withSocket(mode) { transport, socket ->
            val errors = mutableListOf<LuxoError>()
            val cause = object : Exception() {
                override val message: String get() = throw IllegalStateException("message accessor failed")
            }
            var decodes = 0
            var completed = 0
            val stop = transport.subscribe<Long>("watch", { decodes++; throw cause },
                observer = StreamObserver({ completed++ }, { errors += it })) { fail("unexpected data") }
            var peer = 0L
            val stopPeer = transport.subscribe("watch", ::integer) { peer = it }
            repeat(2) { data(socket, mode, 1, valid = false) }
            finish(socket, mode, 1)
            stop(); stop()
            data(socket, mode, 2, valid = true)
            assertEquals(1, decodes)
            assertEquals("DecodeError", errors.single().error)
            assertEquals("invalid subscription payload", errors.single().message)
            assertEquals(0, completed)
            assertEquals(7L, peer)
            assertEquals(7L, integer(transport.call("watch")))
            assertEquals(0, socket.closes)
            stopPeer()
        }
    }

    @Test
    fun fatalJvmErrorsAreNotConvertedIntoProtocolErrors() = runBlocking {
        for (mode in TransportMode.entries) withSocket(mode) { transport, socket ->
            val fatal = AssertionError("fatal decoder failure")
            val stop = transport.subscribe<Long>("watch", { throw fatal },
                observer = StreamObserver(onError = { fail("not a recoverable decoder error") })) { fail("unexpected data") }
            assertSame(fatal, assertFailsWith<AssertionError> { data(socket, mode, 1, valid = true) })
            stop()
        }
    }

    @Test
    fun decodingFailureWithoutObserverStillCancels() = runBlocking {
        for (mode in TransportMode.entries) withSocket(mode) { transport, socket ->
            var decodes = 0
            val stop = transport.subscribe<Long>("watch", { decodes++; throw IllegalArgumentException() }) { fail("unexpected data") }
            val sent = socket.sent
            repeat(2) { data(socket, mode, 1, valid = false) }
            stop()
            assertEquals(1, decodes)
            assertEquals(sent + 1, socket.sent)
        }
    }

    @Test
    fun failedRemoteCancellationDoesNotPreventLocalFailure() = runBlocking {
        for (mode in TransportMode.entries) for (throwOnSend in listOf(false, true)) withSocket(mode) { transport, socket ->
            val errors = mutableListOf<LuxoError>()
            val stop = transport.subscribe("watch", ::integer, observer = StreamObserver(onError = { errors += it })) { fail("unexpected data") }
            val text = socket.onText
            val binary = socket.onBinary
            socket.acceptSend = false
            if (throwOnSend) {
                socket.onText = { throw IllegalStateException("socket write") }
                socket.onBinary = { throw IllegalStateException("socket write") }
            }
            repeat(2) { data(socket, mode, 1, valid = false) }
            stop()
            assertEquals("DecodeError", errors.single().error)
            socket.acceptSend = true
            socket.onText = text
            socket.onBinary = binary
            assertEquals(7L, integer(transport.call("watch")))
        }
    }

    @Test
    fun observerCanReenterOutsideLifecycleLockAndThrow() = runBlocking {
        for (mode in TransportMode.entries) withSocket(mode) { transport, socket ->
            val expected = IllegalStateException("observer")
            var notified = 0
            transport.subscribe<Long>("watch", { throw LuxoError("RangeError", 0, "bad integer") }, observer = StreamObserver(onError = {
                assertEquals("DecodeError", it.error)
                assertEquals("bad integer", it.message)
                notified++
                runBlocking { withTimeout(1_000) { withContext(Dispatchers.Default) { transport.close() } } }
                throw expected
            })) { fail("unexpected data") }
            assertSame(expected, assertFailsWith<IllegalStateException> { data(socket, mode, 1, valid = false) })
            data(socket, mode, 1, valid = false)
            assertEquals(1, notified)
        }
    }

    @Test
    fun decoderRetirementDoesNotNotifyTwice() = runBlocking {
        for (mode in TransportMode.entries) for (close in listOf(false, true)) withSocket(mode) { transport, socket ->
            lateinit var stop: () -> Unit
            stop = transport.subscribe<Long>("watch", {
                if (close) transport.close() else stop()
                throw IllegalArgumentException("retired during decoding")
            }, observer = StreamObserver({ fail("retired") }, { fail("retired") })) { fail("unexpected data") }
            repeat(2) { data(socket, mode, 1, valid = false) }
            stop()
        }
    }

    @Test
    fun concurrentDecodeCompletionAndCancellationNotifyAtMostOnce() = runBlocking {
        for (mode in TransportMode.entries) withSocket(mode) { transport, socket ->
            repeat(32) { index ->
                val notifications = AtomicInteger()
                val stop = transport.subscribe("watch", ::integer, observer = StreamObserver(
                    { notifications.incrementAndGet() }, { notifications.incrementAndGet() },
                )) { fail("unexpected data") }
                coroutineScope {
                    repeat(4) { launch(Dispatchers.Default) { data(socket, mode, index + 1L, valid = false) } }
                    launch(Dispatchers.Default) { finish(socket, mode, index + 1L) }
                    launch(Dispatchers.Default) { stop() }
                }
                assertTrue(notifications.get() <= 1)
            }
        }
    }

    private fun integer(data: Any): Long = if (data is ByteArray) {
        val decoder = LuxoDecoder(data)
        decoder.readInt().also { decoder.checkComplete() }
    } else decodeLuxoJsonInt(data as JsonElement)

    private fun data(socket: TestSocket, mode: TransportMode, id: Long, valid: Boolean) {
        if (mode == TransportMode.JSON) socket.receive("""{"${'$'}stream":$id,"data":${if (valid) "7" else "true"}}""")
        else socket.receive(frame(0x12, id, if (valid) byteArrayOf(14) else byteArrayOf(0x80.toByte())))
    }

    private fun finish(socket: TestSocket, mode: TransportMode, id: Long) {
        if (mode == TransportMode.JSON) socket.receive("""{"${'$'}end":$id,"ok":true}""")
        else socket.receive(frame(0x15, id))
    }

    private fun frame(tag: Long, id: Long, data: ByteArray = byteArrayOf()): ByteArray = LuxoEncoder().apply {
        writeVarint(tag); writeVarint(id); writeRawBytes(data)
    }.bytes()

    private suspend fun withSocket(mode: TransportMode, block: suspend (LuxoWebSocket, TestSocket) -> Unit) {
        val client = TestSocketClient()
        val transport = LuxoWebSocket("ws://localhost/luvia", client, 0u, 2_000)
        transport.setMode(mode)
        transport.setSchema(mapOf("watch" to APISchemaEntry(77)))
        try {
            transport.connect()
            val socket = client.socket
            socket.open()
            socket.onText = { text ->
                val json = Json.parseToJsonElement(text).jsonObject
                if (json.containsKey("\$sub")) socket.receive("""{"${'$'}sub":${json.getValue("\$id")},"ok":true}""")
                else if (json.containsKey("\$api")) socket.receive("""{"${'$'}id":${json.getValue("\$id")},"data":7}""")
            }
            socket.onBinary = { bytes ->
                val decoder = LuxoDecoder(bytes.toByteArray())
                when (decoder.readVarint()) {
                    0x10L -> socket.receive(frame(0x13, decoder.readVarint()))
                    0x01L -> socket.receive(frame(2, decoder.readVarint(), byteArrayOf(14)))
                }
            }
            block(transport, socket)
        } finally {
            transport.close()
            client.dispatcher.executorService.shutdownNow()
            client.connectionPool.evictAll()
        }
    }
}
