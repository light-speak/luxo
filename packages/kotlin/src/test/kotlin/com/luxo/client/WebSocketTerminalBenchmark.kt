package com.luxo.client

import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.*
import okio.ByteString
import java.util.function.LongSupplier

/** Same UInt64 wire; the frozen baseline predates terminal and disconnect notifications. */
object WebSocketTerminalBenchmark {
    @JvmStatic
    fun main(args: Array<String>) {
        println("JDK ${System.getProperty("java.version")}; 12 warmup pairs, 20 alternating samples; ns/op and B/op")
        if (args.contentEquals(arrayOf("--disconnect-only"))) {
            for (mode in TransportMode.entries) for (count in listOf(1, 8, 64)) compareDisconnect(mode, count)
            println("sink=${PairedJvmBenchmark.sink}")
            return
        }
        for (mode in TransportMode.entries) {
            for (id in listOf(1uL, ULong.MAX_VALUE)) compareData(mode, id)
            compareLifecycle(mode, null)
            compareLifecycle(mode, StreamObserver({}, {}))
            compareUnary(mode)
            compareTerminalKinds(mode)
        }
        println("sink=${PairedJvmBenchmark.sink}")
    }

    private fun compareData(mode: TransportMode, id: ULong) = runBlocking {
        Fixture(false, mode, id - 1u).use { before ->
            Fixture(true, mode, id - 1u).use { after ->
                val cancelBefore = before.subscribe() { before.accept(it) }
                val cancelAfter = after.subscribe() { after.accept(it) }
                val message = before.dataFrame(id)
                before.deliver(message); after.deliver(message)
                check(before.received == 7L && after.received == 7L)
                PairedJvmBenchmark.compare("$mode data id=$id", LongSupplier {
                    before.deliver(message); before.received
                }, LongSupplier {
                    after.deliver(message); after.received
                }, iterations = 100_000)
                check(before.received == after.received)
                cancelBefore(); cancelAfter()
            }
        }
    }

    private fun compareLifecycle(mode: TransportMode, observer: StreamObserver?) {
        Fixture(false, mode).use { before ->
            Fixture(true, mode).use { after ->
                val handler: (Any) -> Unit = {}
                PairedJvmBenchmark.compare("$mode setup ACK cancel observer=${observer != null}", LongSupplier {
                    runBlocking { before.subscribe(observer = observer, handler = handler).invoke() }
                    before.client.socket.sent.toLong()
                }, LongSupplier {
                    runBlocking { after.subscribe(observer = observer, handler = handler).invoke() }
                    after.client.socket.sent.toLong()
                }, iterations = 20_000)
            }
        }
    }

    private fun compareUnary(mode: TransportMode) {
        Fixture(false, mode).use { before ->
            Fixture(true, mode).use { after ->
                PairedJvmBenchmark.compare("$mode unary fake roundtrip", LongSupplier {
                    runBlocking { before.accept(before.transport.call("watch")) }; before.received
                }, LongSupplier {
                    runBlocking { after.accept(after.transport.call("watch")) }; after.received
                }, iterations = 20_000)
                check(before.received == after.received)
            }
        }
    }

    private fun compareTerminalKinds(mode: TransportMode) {
        // Different operations, not a regression comparison; setup and encoding are included.
        Fixture(true, mode).use { complete ->
            Fixture(true, mode).use { failure ->
                var completed = 0L
                var rejected = 0L
                val doneObserver = StreamObserver(onComplete = { completed++ })
                val errorObserver = StreamObserver(onError = { check(it.error == "Rejected"); rejected++ })
                val handler: (Any) -> Unit = {}
                PairedJvmBenchmark.compare("$mode absolute setup+complete vs setup+error (NOT before/after)", LongSupplier {
                    runBlocking { complete.subscribe(observer = doneObserver, handler = handler) }
                    complete.finish(false); completed
                }, LongSupplier {
                    runBlocking { failure.subscribe(observer = errorObserver, handler = handler) }
                    failure.finish(true); rejected
                }, iterations = 20_000)
                check(completed > 0 && completed == rejected)
            }
        }
    }

    private fun compareDisconnect(mode: TransportMode, count: Int) {
        // Both arms use the current transport, not a before/after fix comparison.
        Fixture(true, mode).use { silent ->
            Fixture(true, mode).use { observed ->
                var notified = 0L
                val observer = StreamObserver(onError = { check(it.error == "NetworkError"); notified++ })
                PairedJvmBenchmark.compare("$mode absolute setup[$count]+disconnect+reconnect no observer / observer", LongSupplier {
                    silent.cycleDisconnect(count, null)
                }, LongSupplier {
                    observed.cycleDisconnect(count, observer)
                }, iterations = if (count == 64) 1_000 else 2_000)
                check(notified == observed.disconnects * count)
                check(observed.disconnects == silent.disconnects)
            }
        }
    }

    private class Fixture(current: Boolean, val mode: TransportMode, start: ULong = 0u) : AutoCloseable {
        val client = TestSocketClient()
        private val modern = if (current) LuxoWebSocket("ws://localhost/luvia", client, start, 30_000) else null
        private val baseline = if (current) null else PreTerminalWebSocketBaseline("ws://localhost/luvia", client, start, 30_000)
        val transport: Transport = modern ?: requireNotNull(baseline)
        // No decoder adapter is added inside the frozen implementation.
        suspend fun subscribe(observer: StreamObserver? = null, handler: (Any) -> Unit): () -> Unit = if (modern != null) {
            modern.subscribe("watch", { it }, observer = observer, handler = handler)
        } else requireNotNull(baseline).subscribeRaw("watch", emptyMap(), observer, handler)
        var received = 0L
            private set
        var disconnects = 0L
            private set
        private var subscriptionID = 0L
        private val errorPayload = LuxoEncoder().apply {
            writeFieldInt(1, 409); writeFieldString(2, "Rejected"); writeFieldString(3, "no"); writeEnd()
        }.bytes()

        init {
            transport.setMode(mode)
            transport.setSchema(mapOf("watch" to APISchemaEntry(77)))
            open()
        }

        private fun open() {
            modern?.connect() ?: baseline?.connect()
            client.socket.open()
            client.socket.onText = { text ->
                val json = Json.parseToJsonElement(text).jsonObject
                if (json.containsKey("\$sub")) {
                    subscriptionID = requireNotNull(webSocketJSONRequestID(json["\$id"]))
                    client.socket.receive("""{"${'$'}sub":${json.getValue("\$id")},"ok":true}""")
                } else if (json.containsKey("\$api")) {
                    client.socket.receive("""{"${'$'}id":${json.getValue("\$id")},"data":7}""")
                }
            }
            client.socket.onBinary = { bytes ->
                val decoder = LuxoDecoder(bytes.toByteArray())
                when (decoder.readVarint()) {
                    0x10L -> {
                        subscriptionID = decoder.readVarint()
                        client.socket.receive(frame(0x13, subscriptionID))
                    }
                    0x01L -> client.socket.receive(frame(2, decoder.readVarint(), byteArrayOf(7)))
                }
            }
        }

        fun cycleDisconnect(count: Int, observer: StreamObserver?): Long = runBlocking {
            repeat(count) { subscribe(observer = observer) {} }
            val previous = client.socket
            previous.disconnected()
            open()
            check(client.socket !== previous && client.socket.sent == 0)
            ++disconnects
        }

        fun accept(value: Any) { received += if (value is JsonPrimitive) value.long else (value as ByteArray)[0].toLong() }

        fun dataFrame(id: ULong): Any = if (mode == TransportMode.JSON) {
            """{"${'$'}stream":$id,"data":7}"""
        } else frame(0x12, id.toLong(), byteArrayOf(7))

        fun finish(failure: Boolean) {
            if (mode == TransportMode.BINARY) {
                client.socket.receive(frame(if (failure) 0x16 else 0x15, subscriptionID, if (failure) errorPayload else byteArrayOf()))
            } else if (failure) {
                client.socket.receive("""{"${'$'}end":${subscriptionID.toULong()},"error":"Rejected","code":409,"message":"no"}""")
            } else {
                client.socket.receive("""{"${'$'}end":${subscriptionID.toULong()},"ok":true}""")
            }
        }

        fun deliver(frame: Any) {
            if (frame is String) client.socket.receive(frame) else client.socket.receive(frame as ByteString)
        }

        private fun frame(type: Long, id: Long, payload: ByteArray = byteArrayOf()): ByteString {
            val encoder = LuxoEncoder()
            encoder.writeVarint(type)
            encoder.writeVarint(id)
            encoder.writeRawBytes(payload)
            return ByteString.of(*encoder.bytes())
        }

        override fun close() {
            modern?.close() ?: baseline?.close()
            client.dispatcher.executorService.shutdownNow()
            client.connectionPool.evictAll()
        }
    }
}
