package com.luxo.client

import kotlinx.coroutines.*
import kotlinx.serialization.json.*
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.*

class WebSocketDecodeTest {
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
