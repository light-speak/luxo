package com.luxo.client

import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.*
import okio.ByteString
import java.util.function.LongSupplier

/** Paired real transport dispatch/setup with a fake socket; no network or reflection. */
object WebSocketSubscriptionBenchmark {
    @JvmStatic
    fun main(args: Array<String>) {
        println("JDK ${System.getProperty("java.version")}; 12 warmup pairs, 20 alternating samples; ns/op and B/op")
        for (id in listOf(1uL, 129uL, ULong.MAX_VALUE)) {
            compareDispatch(TransportMode.JSON, 1, id)
            for (apis in listOf(1, 32, 128)) compareDispatch(TransportMode.BINARY, apis, id)
        }
        for (mode in TransportMode.entries) {
            compareLifecycle(mode)
            compareUnary(mode)
        }
        println("sink=${PairedJvmBenchmark.sink}")
    }

    private fun compareDispatch(mode: TransportMode, apis: Int, id: ULong) = runBlocking {
        Fixture(false, mode, apis, 0u).use { before ->
            Fixture(true, mode, apis, id - 1u).use { after ->
                val cancelBefore = before.subscribe() { before.accept(it) }
                val cancelAfter = after.subscribe() { after.accept(it) }
                val oldFrame = before.dataFrame(apis.toULong())
                val newFrame = after.dataFrame(id)
                before.deliver(oldFrame); after.deliver(newFrame)
                check(before.received == 7L && after.received == 7L)
                PairedJvmBenchmark.compare("$mode dispatch apis=$apis id=$id", LongSupplier {
                    before.deliver(oldFrame)
                    before.received
                }, LongSupplier {
                    after.deliver(newFrame)
                    after.received
                }, iterations = 100_000)
                check(before.received == after.received)
                cancelBefore(); cancelAfter()
            }
        }
    }

    private fun compareLifecycle(mode: TransportMode) {
        Fixture(false, mode, 1, 0u).use { before ->
            Fixture(true, mode, 1, 0u).use { after ->
                val handler: (Any) -> Unit = {}
                PairedJvmBenchmark.compare("$mode subscribe ACK cancel", LongSupplier {
                    runBlocking { before.subscribe(handler = handler).invoke() }
                    before.client.socket.sent.toLong()
                }, LongSupplier {
                    runBlocking { after.subscribe(handler = handler).invoke() }
                    after.client.socket.sent.toLong()
                }, iterations = 20_000)
            }
        }
    }

    private fun compareUnary(mode: TransportMode) {
        // Fresh transports keep both unary counters (and frame lengths) identical.
        Fixture(false, mode, 1, 0u).use { before ->
            Fixture(true, mode, 1, 0u).use { after ->
                PairedJvmBenchmark.compare("$mode unary fake roundtrip", LongSupplier {
                    runBlocking { before.accept(before.transport.call("watch")) }
                    before.received
                }, LongSupplier {
                    runBlocking { after.accept(after.transport.call("watch")) }
                    after.received
                }, iterations = 20_000)
                check(before.received == after.received)
            }
        }
    }

    private class Fixture(val current: Boolean, val mode: TransportMode, apis: Int, start: ULong) : AutoCloseable {
        val client = TestSocketClient()
        private val modern = if (current) LuxoWebSocket("ws://localhost/luvia", client, start, 30_000) else null
        private val legacy = if (current) null else LegacyWebSocketSubscriptionBaseline("ws://localhost/luvia", client, autoReconnect = false)
        val transport: Transport = modern ?: requireNotNull(legacy)
        // The frozen baseline retains its original raw subscription entry point.
        suspend fun subscribe(handler: (Any) -> Unit): () -> Unit = if (modern != null) {
            modern.subscribe("watch", { it }, handler = handler)
        } else requireNotNull(legacy).subscribeRaw("watch", emptyMap(), null, handler)
        var received = 0L
            private set

        init {
            transport.setMode(mode)
            transport.setSchema(buildMap {
                for (id in 1 until apis) put("unused$id", APISchemaEntry(id))
                put("watch", APISchemaEntry(apis))
            })
            modern?.connect() ?: legacy?.connect()
            client.socket.open()
            client.socket.onText = { text ->
                val json = Json.parseToJsonElement(text).jsonObject
                if (json.containsKey("\$sub")) {
                    val id = if (current) json.getValue("\$id").toString() else "\"watch\""
                    client.socket.receive("""{"${'$'}sub":$id,"ok":true}""")
                } else if (json.containsKey("\$api")) {
                    client.socket.receive("""{"${'$'}id":${json.getValue("\$id")},"data":7}""")
                }
            }
            client.socket.onBinary = { bytes ->
                val decoder = LuxoDecoder(bytes.toByteArray())
                when (decoder.readVarint()) {
                    0x04L, 0x10L -> client.socket.receive(frame(if (current) 0x13 else 7, decoder.readVarint(), false))
                    0x01L -> client.socket.receive(frame(2, decoder.readVarint(), true))
                }
            }
        }

        fun accept(value: Any) { received += if (value is JsonPrimitive) value.long else (value as ByteArray)[0].toLong() }

        fun dataFrame(id: ULong): Any = if (mode == TransportMode.JSON) {
            val key = if (current) id.toString() else "\"watch\""
            """{"${'$'}stream":$key,"data":7}"""
        } else {
            frame(if (current) 0x12 else 6, id.toLong(), true)
        }

        fun deliver(frame: Any) {
            if (frame is String) client.socket.receive(frame) else client.socket.receive(frame as ByteString)
        }

        private fun frame(type: Long, id: Long, data: Boolean): ByteString {
            val encoder = LuxoEncoder()
            encoder.writeVarint(type)
            encoder.writeVarint(id)
            if (data) encoder.writeRawBytes(byteArrayOf(7))
            return ByteString.of(*encoder.bytes())
        }

        override fun close() {
            modern?.close() ?: legacy?.close()
            client.dispatcher.executorService.shutdownNow()
            client.connectionPool.evictAll()
        }
    }
}
