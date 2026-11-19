package com.luxo.client

import com.sun.management.ThreadMXBean
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.*
import java.lang.management.ManagementFactory
import java.util.Locale

/** One implementation and one call site per JVM; no renamed transport classes. */
object WebSocketIsolatedBenchmark {
    @Volatile private var sink = 0L
    private val allocations = (ManagementFactory.getThreadMXBean() as ThreadMXBean).apply {
        isThreadAllocatedMemoryEnabled = true
    }

    @JvmStatic
    fun main(args: Array<String>) {
        val mode = TransportMode.valueOf(args[0])
        val payloadSize = args[1].toInt()
        val entry = args.getOrNull(2) ?: "dynamic"
        require(entry in listOf("dynamic", "generated", "subscription", "generated-subscription"))
        Fixture(mode, payloadSize, entry).use { fixture ->
            check(fixture.call() == 7L)
            val iterations = args.getOrNull(3)?.toInt() ?: if (payloadSize > 1024) 2_000 else 50_000
            require(iterations > 0)
            repeat(15) { measure(fixture, iterations) }
            val samples = List(25) { measure(fixture, iterations) }
            fun median(values: List<Double>) = values.sorted()[12]
            println(String.format(Locale.ROOT, "%s bytes=%d %s %.3f ns/op; %.3f B/op; sent=%d sink=%d", mode, payloadSize, entry,
                median(samples.map { it.first }), median(samples.map { it.second }), fixture.sent, sink))
            println(samples.joinToString("\n") { "sample ${it.first} ${it.second}" })
            check(fixture.sent == (if (entry.endsWith("subscription")) 2 else 1) * (1 + iterations * 40))
        }
    }

    private fun measure(fixture: Fixture, iterations: Int): Pair<Double, Double> {
        val thread = Thread.currentThread().threadId()
        val bytes = allocations.getThreadAllocatedBytes(thread)
        val start = System.nanoTime()
        var result = 0L
        repeat(iterations) { result = result xor fixture.call() }
        val elapsed = System.nanoTime() - start
        val allocated = allocations.getThreadAllocatedBytes(thread) - bytes
        sink = result
        return elapsed.toDouble() / iterations to allocated.toDouble() / iterations
    }

    private class Fixture(mode: TransportMode, payloadSize: Int, private val entry: String) : AutoCloseable {
        private val client = TestSocketClient()
        private val transport = LuxoWebSocket("ws://localhost/luvia", client, 0u, 30_000)
        private val data = if (payloadSize == 0) null else ByteArray(payloadSize) { it.toByte() }
        private val params: Map<String, Any?> = if (data == null) emptyMap() else mapOf("data" to data)
        private val request = object : GeneratedRequest {
            override val apiID = 77
            override fun writeParameters(encoder: LuxoEncoder) { data?.let { encoder.writeFieldBytes(1, it) } }
            override fun toJSONParameters(): Map<String, Any?> = params
        }
        val sent get() = client.socket.sent

        init {
            transport.setMode(mode)
            val params = if (payloadSize == 0) emptyList() else listOf(ParamSchema(1, "data", "Bytes"))
            val schema = APISchemaEntry(77, params)
            check(LuxoBinaryProtocol.encodeRequest(schema, this.params).contentEquals(LuxoBinaryProtocol.encodeRequest(schema, request)))
            transport.setSchema(mapOf("watch" to schema))
            transport.connect()
            client.socket.open()
            client.socket.onText = { text ->
                val json = Json.parseToJsonElement(text).jsonObject
                when {
                    json.containsKey("\$sub") -> client.socket.receive("""{"${'$'}sub":${json.getValue("\$id")},"ok":true}""")
                    json.containsKey("\$api") -> {
                        check(json["\$api"]?.jsonPrimitive?.content == "watch")
                        client.socket.receive("""{"${'$'}id":${json.getValue("\$id")},"data":7}""")
                    }
                    else -> check(json.containsKey("\$unsub"))
                }
            }
            client.socket.onBinary = { bytes ->
                val decoder = LuxoDecoder(bytes.toByteArray())
                val type = decoder.readVarint()
                val id = decoder.readVarint()
                when (type) {
                    0x01L -> client.socket.receive(LuxoEncoder().apply { writeVarint(2); writeVarint(id); writeSvarint(7) }.bytes())
                    0x10L -> client.socket.receive(LuxoEncoder().apply { writeVarint(0x13); writeVarint(id) }.bytes())
                    else -> check(type == 0x11L)
                }
            }
        }

        fun call(): Long = runBlocking {
            if (entry.endsWith("subscription")) {
                val stop = if (entry.startsWith("generated")) transport.subscribe("watch", { it }, request) {}
                    else transport.subscribe("watch", { it }, params = params) {}
                stop()
                return@runBlocking 7L
            }
            val response = if (entry == "generated") transport.call("watch", request) else transport.call("watch", params)
            when (response) {
                is ByteArray -> LuxoDecoder(response).run { readInt().also { checkComplete() } }
                else -> decodeLuxoJsonInt(response as JsonElement)
            }
        }

        override fun close() {
            transport.close()
            client.dispatcher.executorService.shutdownNow()
            client.connectionPool.evictAll()
        }
    }
}
