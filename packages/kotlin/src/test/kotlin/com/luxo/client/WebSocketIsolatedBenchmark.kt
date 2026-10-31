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
        Fixture(mode, payloadSize).use { fixture ->
            check(fixture.call() == 7L)
            val iterations = if (payloadSize > 1024) 2_000 else 50_000
            repeat(15) { measure(fixture, iterations) }
            val samples = List(25) { measure(fixture, iterations) }
            fun median(values: List<Double>) = values.sorted()[12]
            println(String.format(Locale.ROOT, "%s bytes=%d unary %.3f ns/op; %.3f B/op; sent=%d sink=%d", mode, payloadSize,
                median(samples.map { it.first }), median(samples.map { it.second }), fixture.sent, sink))
            println(samples.joinToString("\n") { "sample ${it.first} ${it.second}" })
            check(fixture.sent == 1 + iterations * 40)
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

    private class Fixture(mode: TransportMode, payloadSize: Int) : AutoCloseable {
        private val client = TestSocketClient()
        private val transport = LuxoWebSocket("ws://localhost/luvia", client, 0u, 30_000)
        private val params: Map<String, Any?> = if (payloadSize == 0) emptyMap() else mapOf("data" to ByteArray(payloadSize) { it.toByte() })
        val sent get() = client.socket.sent

        init {
            transport.setMode(mode)
            val params = if (payloadSize == 0) emptyList() else listOf(ParamSchema(1, "data", "Bytes"))
            transport.setSchema(mapOf("watch" to APISchemaEntry(77, params)))
            transport.connect()
            client.socket.open()
            client.socket.onText = { text ->
                val json = Json.parseToJsonElement(text).jsonObject
                check(json["\$api"]?.jsonPrimitive?.content == "watch")
                client.socket.receive("""{"${'$'}id":${json.getValue("\$id")},"data":7}""")
            }
            client.socket.onBinary = { bytes ->
                val decoder = LuxoDecoder(bytes.toByteArray())
                check(decoder.readVarint() == 1L)
                val id = decoder.readVarint()
                client.socket.receive(LuxoEncoder().apply { writeVarint(2); writeVarint(id); writeSvarint(7) }.bytes())
            }
        }

        fun call(): Long = runBlocking {
            when (val data = transport.call("watch", params)) {
                is ByteArray -> LuxoDecoder(data).run { readInt().also { checkComplete() } }
                else -> decodeLuxoJsonInt(data as JsonElement)
            }
        }

        override fun close() {
            transport.close()
            client.dispatcher.executorService.shutdownNow()
            client.connectionPool.evictAll()
        }
    }
}
