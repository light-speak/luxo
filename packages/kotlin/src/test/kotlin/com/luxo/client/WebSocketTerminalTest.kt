package com.luxo.client

import kotlinx.coroutines.*
import kotlinx.serialization.json.*
import kotlin.test.*

class WebSocketTerminalTest {
    @Test
    fun completionFollowsDataOnceAndOnlyRemovesItsOwnSubscription() = runBlocking {
        for (mode in TransportMode.entries) withSocket(mode) { transport, socket ->
            val events = mutableListOf<Int>()
            val cancel = transport.subscribe("watch", { it }, observer = StreamObserver(onComplete = { events += 64 })) {
                events += if (it is JsonPrimitive) it.int else (it as ByteArray)[0].toInt()
            }
            var other = 0
            val cancelOther = transport.subscribe("watch", { it }) { other++ }
            repeat(64) { data(socket, mode, 1u, it) }
            finish(socket, mode, 1u)
            finish(socket, mode, 1u)
            data(socket, mode, 1u, 99)
            data(socket, mode, 2u, 7)
            assertEquals((0..64).toList(), events)
            assertEquals(1, other)
            val sent = socket.sent
            cancel()
            assertEquals(sent, socket.sent)
            assertEquals(0, socket.closes)
            cancelOther()
        }
    }

    @Test
    fun businessFailurePreservesRawNumbersAndDoesNotComplete() = runBlocking {
        for (mode in TransportMode.entries) withSocket(mode, start = ULong.MAX_VALUE - 1u) { transport, socket ->
            val errors = mutableListOf<LuxoError>()
            var completed = 0
            transport.subscribe("watch", { it }, observer = StreamObserver({ completed++ }, { errors += it })) {}
            fail(socket, mode, ULong.MAX_VALUE)
            fail(socket, mode, ULong.MAX_VALUE)
            finish(socket, mode, ULong.MAX_VALUE)
            val error = errors.single()
            assertEquals("Rejected", error.error)
            assertEquals(409, error.code)
            assertEquals(precisionData, error.data.toString())
            assertEquals("trace", error.traceId)
            assertEquals("details", error.developmentCause)
            assertEquals(0, completed)
            assertEquals(0, socket.closes)
        }
    }

    @Test
    fun completionSupportsUnsignedMaximumAndModeChanges() = runBlocking {
        for (mode in TransportMode.entries) withSocket(mode, start = ULong.MAX_VALUE - 1u) { transport, socket ->
            var completed = 0
            transport.subscribe("watch", { it }, observer = StreamObserver(onComplete = { completed++ })) {}
            transport.setMode(opposite(mode))
            finish(socket, opposite(mode), ULong.MAX_VALUE)
            assertEquals(0, completed)
            finish(socket, mode, ULong.MAX_VALUE)
            assertEquals(1, completed)
        }
    }

    @Test
    fun invalidOrAmbiguousJSONIdentifiersCannotTerminateSubscription() = runBlocking {
        withSocket(TransportMode.JSON) { transport, socket ->
            var completed = 0
            var errors = 0
            transport.subscribe("watch", { it }, observer = StreamObserver({ completed++ }, { errors++ })) {}
            for (id in listOf("0", "2", "-1", "1.0", "true", "null", "\"1\"", "18446744073709551616")) {
                socket.receive("""{"${'$'}end":$id,"ok":true}""")
            }
            for (other in listOf("sub", "stream", "id")) {
                socket.receive("""{"${'$'}end":1,"${'$'}$other":1,"ok":true}""")
            }
            assertEquals(0, errors)
            assertEquals(0, completed)
            finish(socket, TransportMode.JSON, 1u)
            assertEquals(1, completed)
        }
    }

    @Test
    fun malformedJSONTerminalFailsOnlyCorrelatedSubscription() = runBlocking {
        val suffixes = listOf("", ",\"ok\":false", ",\"ok\":\"true\"", ",\"ok\":true,\"data\":null",
            ",\"error\":\"Rejected\"", ",\"ok\":true,\"error\":\"Rejected\",\"code\":409,\"message\":\"no\"")
        for (suffix in suffixes) withSocket(TransportMode.JSON) { transport, socket ->
            val errors = mutableListOf<LuxoError>()
            transport.subscribe("watch", { it }, observer = StreamObserver(onError = { errors += it })) {}
            socket.receive("""{"${'$'}end":1$suffix}""")
            assertEquals("ParseError", errors.single().error)
            finish(socket, TransportMode.JSON, 1u)
            assertEquals(1, errors.size)
        }
    }

    @Test
    fun malformedBinaryTerminalFailsCorrelatedSubscription() = runBlocking {
        for (tag in listOf(0x15, 0x16)) withSocket(TransportMode.BINARY) { transport, socket ->
            val errors = mutableListOf<LuxoError>()
            transport.subscribe("watch", { it }, observer = StreamObserver(onError = { errors += it })) {}
            socket.receive(frame(tag, 1u, byteArrayOf(1)))
            assertEquals("ParseError", errors.single().error)
        }
    }

    @Test
    fun uncorrelatableBinaryTerminalDoesNotRemoveSubscription() = runBlocking {
        withSocket(TransportMode.BINARY) { transport, socket ->
            var completed = 0
            var errors = 0
            transport.subscribe("watch", { it }, observer = StreamObserver({ completed++ }, { errors++ })) {}
            for (frame in listOf(byteArrayOf(), byteArrayOf(0x15), byteArrayOf(0x95.toByte(), 0, 1),
                byteArrayOf(0x15) + ByteArray(10) { 0xff.toByte() }, frame(0x15, 2u), frame(0x17, 1u))) socket.receive(frame)
            assertEquals(0, completed)
            assertEquals(0, errors)
            finish(socket, TransportMode.BINARY, 1u)
            assertEquals(1, completed)
        }
    }

    @Test
    fun terminalBeforeAcknowledgementRejectsWithoutObserverNotification() = runBlocking {
        for (mode in TransportMode.entries) withSocket(mode, acknowledge = false) { transport, socket ->
            var notified = 0
            val pending = async { runCatching {
                transport.subscribe("watch", { it }, observer = StreamObserver({ notified++ }, { notified++ })) {}
            } }
            yield()
            fail(socket, mode, 1u)
            assertEquals("ParseError", assertIs<LuxoError>(pending.await().exceptionOrNull()).error)
            assertEquals(0, notified)
        }
    }

    @Test
    fun acknowledgementFailureAfterEstablishmentNotifiesObserver() = runBlocking {
        for (mode in TransportMode.entries) withSocket(mode) { transport, socket ->
            val errors = mutableListOf<LuxoError>()
            transport.subscribe("watch", { it }, observer = StreamObserver(onError = { errors += it })) {}
            fail(socket, mode, 1u, acknowledgement = true)
            fail(socket, mode, 1u, acknowledgement = true)
            assertEquals("Rejected", errors.single().error)
        }
    }

    @Test
    fun cancellationAndAbsentObserversStaySilent() = runBlocking {
        for (mode in TransportMode.entries) withSocket(mode) { transport, socket ->
            var notified = 0
            val cancel = transport.subscribe("watch", { it }, observer = StreamObserver({ notified++ }, { notified++ })) {}
            cancel()
            finish(socket, mode, 1u)
            fail(socket, mode, 1u)
            transport.subscribe("watch", { it }) {}
            finish(socket, mode, 2u)
            transport.subscribe("watch", { it }) {}
            fail(socket, mode, 3u)
            assertEquals(0, notified)
        }
    }

    @Test
    fun observerExceptionPropagatesAfterRemovalAndDoesNotHoldLifecycleLock() = runBlocking {
        for (mode in TransportMode.entries) for (failure in listOf(false, true)) withSocket(mode) { transport, socket ->
            val expected = IllegalStateException("observer")
            var notifications = 0
            val callback: () -> Unit = {
                notifications++
                runBlocking { withTimeout(1_000) { withContext(Dispatchers.Default) { transport.close() } } }
                throw expected
            }
            val cancel = transport.subscribe("watch", { it }, observer = StreamObserver(callback, { callback() })) {}
            assertSame(expected, assertFailsWith<IllegalStateException> {
                if (failure) fail(socket, mode, 1u) else finish(socket, mode, 1u)
            })
            cancel()
            finish(socket, mode, 1u)
            assertEquals(1, notifications)
        }
    }

    @Test
    fun concurrentDuplicateTerminalAndCancellationNotifyAtMostOnce() = runBlocking {
        for (mode in TransportMode.entries) withSocket(mode) { transport, socket ->
            repeat(32) { index ->
                val notified = java.util.concurrent.atomic.AtomicInteger()
                val cancel = transport.subscribe("watch", { it }, observer = StreamObserver(onComplete = { notified.incrementAndGet() })) {}
                coroutineScope {
                    repeat(8) { launch(Dispatchers.Default) { finish(socket, mode, (index + 1).toULong()) } }
                    launch(Dispatchers.Default) { cancel() }
                }
                assertTrue(notified.get() <= 1)
            }
        }
    }

    @Test
    fun completionBeforeAcknowledgementRejectsEstablishment() = runBlocking {
        for (mode in TransportMode.entries) withSocket(mode, acknowledge = false) { transport, socket ->
            val pending = async { runCatching { transport.subscribe("watch", { it }) {} } }
            yield()
            finish(socket, mode, 1u)
            val error = assertIs<LuxoError>(withTimeout(1_000) { pending.await() }.exceptionOrNull())
            assertEquals("ParseError", error.error)
        }
    }

    @Test
    fun userDataExceptionsAreNotSwallowedAsMalformedFrames() = runBlocking {
        for (mode in TransportMode.entries) withSocket(mode) { transport, socket ->
            val expected = IllegalStateException("application callback")
            val cancel = transport.subscribe("watch", { it }) { throw expected }
            assertSame(expected, assertFailsWith<IllegalStateException> { data(socket, mode, 1u, 7) })
            cancel()
        }
    }

    private suspend fun withSocket(
        mode: TransportMode,
        acknowledge: Boolean = true,
        start: ULong = 0u,
        block: suspend (LuxoWebSocket, TestSocket) -> Unit,
    ) {
        val client = TestSocketClient()
        val transport = LuxoWebSocket("ws://localhost/luvia", client, start, 2_000)
        transport.setMode(mode)
        transport.setSchema(mapOf("watch" to APISchemaEntry(77)))
        try {
            transport.connect()
            val socket = client.socket
            socket.open()
            socket.onText = { text ->
                val json = Json.parseToJsonElement(text).jsonObject
                if (acknowledge && json.containsKey("\$sub")) {
                    socket.receive("""{"${'$'}sub":${json.getValue("\$id")},"ok":true}""")
                }
            }
            socket.onBinary = { bytes ->
                val decoder = LuxoDecoder(bytes.toByteArray())
                if (decoder.readVarint() == 0x10L && acknowledge) {
                    socket.receive(frame(0x13, decoder.readVarint().toULong()))
                }
            }
            block(transport, socket)
        } finally {
            transport.close()
            client.dispatcher.executorService.shutdownNow()
            client.connectionPool.evictAll()
        }
    }

    private fun data(socket: TestSocket, mode: TransportMode, id: ULong, value: Int) {
        if (mode == TransportMode.JSON) socket.receive("""{"${'$'}stream":$id,"data":$value}""")
        else socket.receive(frame(0x12, id, byteArrayOf(value.toByte())))
    }

    private fun finish(socket: TestSocket, mode: TransportMode, id: ULong) {
        if (mode == TransportMode.JSON) socket.receive("""{"${'$'}end":$id,"ok":true}""")
        else socket.receive(frame(0x15, id))
    }

    private fun opposite(mode: TransportMode) = if (mode == TransportMode.JSON) TransportMode.BINARY else TransportMode.JSON

    private val precisionData = """{"n":123456789012345678901234567890,"f":1.2345678901234567890123456789,"zero":-0}"""

    private fun fail(socket: TestSocket, mode: TransportMode, id: ULong, acknowledgement: Boolean = false) {
        if (mode == TransportMode.JSON) {
            val key = if (acknowledgement) "sub" else "end"
            socket.receive("""{"${'$'}$key":$id,"error":"Rejected","code":409,"message":"no","traceId":"trace","data":$precisionData,"cause":"details"}""")
        } else {
            val encoder = LuxoEncoder()
            encoder.writeFieldInt(1, 409)
            encoder.writeFieldString(2, "Rejected")
            encoder.writeFieldString(3, "no")
            encoder.writeFieldString(4, "trace")
            encoder.writeFieldString(5, precisionData)
            encoder.writeFieldString(6, "details")
            encoder.writeEnd()
            socket.receive(frame(if (acknowledgement) 0x14 else 0x16, id, encoder.bytes()))
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
