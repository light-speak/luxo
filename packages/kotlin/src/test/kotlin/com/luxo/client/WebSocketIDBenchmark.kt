package com.luxo.client

import com.sun.management.ThreadMXBean
import kotlinx.serialization.json.*
import java.lang.management.ManagementFactory
import java.util.Locale
import java.util.concurrent.atomic.AtomicLong
import java.util.function.LongSupplier

/** Dependency-free local comparison, not a substitute for full-request or device performance gates. */
object WebSocketIDBenchmark {
    @Volatile private var jsonSink: JsonPrimitive? = null

    @JvmStatic
    fun main(args: Array<String>) {
        println("JDK ${System.getProperty("java.version")}; 12 warmup pairs, 20 alternating samples; ns/op and B/op")
        compareParsing("small IDs", Array(1024) { JsonPrimitive((it + 128).toLong()) })
        compareParsing("large signed IDs", Array(1024) { JsonPrimitive(Long.MAX_VALUE - it) })
        val previous = AtomicLong(0)
        val current = AtomicLong(0)
        PairedJvmBenchmark.compare("counter", LongSupplier { previous.incrementAndGet() }, LongSupplier { nextWebSocketRequestID(current) })
        var oldID = 1024L
        var newID = 1024L
        PairedJvmBenchmark.compare("JSON number construction", LongSupplier {
            jsonSink = JsonPrimitive(oldID++)
            oldID
        }, LongSupplier {
            jsonSink = webSocketJSONID(newID++)
            newID
        })
        val wide = Json.parseToJsonElement("18446744073709551615")
        check(webSocketJSONRequestID(wide) == -1L)
        check(webSocketJSONID(-1L).toString() == "18446744073709551615")
        println("UInt64.max verified; no valid signed baseline exists for it. sink=${PairedJvmBenchmark.sink}")
    }

    private fun compareParsing(name: String, values: Array<JsonPrimitive>) {
        for (value in values) check(previousJSONRequestID(value) == webSocketJSONRequestID(value))
        var oldIndex = 0
        var newIndex = 0
        PairedJvmBenchmark.compare(name, LongSupplier {
            previousJSONRequestID(values[oldIndex++ and 1023])!!
        }, LongSupplier {
            webSocketJSONRequestID(values[newIndex++ and 1023])!!
        })
    }

    private fun previousJSONRequestID(element: JsonElement?): Long? {
        val sequence = element as? JsonPrimitive ?: return null
        if (sequence.isString) return null
        return sequence.longOrNull
    }
}

/** Shared paired harness; all fixtures and semantic checks belong outside timed operations. */
internal object PairedJvmBenchmark {
    @Volatile var sink = 0L
        private set
    private val allocations = (ManagementFactory.getThreadMXBean() as ThreadMXBean).apply {
        isThreadAllocatedMemoryEnabled = true
    }

    private data class Sample(val nanos: Double, val bytes: Double)

    private fun measure(operation: LongSupplier, iterations: Int): Sample {
        val thread = Thread.currentThread().threadId()
        val bytes = allocations.getThreadAllocatedBytes(thread)
        val start = System.nanoTime()
        var result = 0L
        repeat(iterations) { result = result xor operation.asLong }
        val elapsed = System.nanoTime() - start
        val allocated = allocations.getThreadAllocatedBytes(thread) - bytes
        sink = result
        return Sample(elapsed.toDouble() / iterations, allocated.toDouble() / iterations)
    }

    fun compare(name: String, before: LongSupplier, after: LongSupplier, iterations: Int = 500_000) {
        val warmup = minOf(iterations, 100_000)
        repeat(12) { measure(before, warmup); measure(after, warmup) }
        val previous = mutableListOf<Sample>()
        val current = mutableListOf<Sample>()
        repeat(20) { index ->
            if (index % 2 == 0) {
                previous += measure(before, iterations)
                current += measure(after, iterations)
            } else {
                current += measure(after, iterations)
                previous += measure(before, iterations)
            }
        }
        fun median(samples: List<Sample>, value: (Sample) -> Double): Double =
            samples.map(value).sorted().let { (it[9] + it[10]) / 2 }
        println(String.format(Locale.ROOT, "%s: %.3f -> %.3f ns/op; %.3f -> %.3f B/op", name,
            median(previous) { it.nanos }, median(current) { it.nanos },
            median(previous) { it.bytes }, median(current) { it.bytes }))
    }

    /** One implementation per JVM; callers compile both versions with identical class names. */
    fun isolated(name: String, operation: LongSupplier, iterations: Int, warmupIterations: Int = iterations): Int {
        require(iterations > 0 && warmupIterations > 0)
        val warmup = List(12) { measure(operation, warmupIterations) }
        val samples = List(20) { measure(operation, iterations) }
        reportIsolated(name, warmup, samples)
        return warmupIterations * 12 + iterations * 20
    }

    /** The caller owns one coroutine scope; immediate test callbacks must stay on its thread. */
    suspend fun isolatedSuspending(
        name: String, iterations: Int, warmupIterations: Int, operation: suspend () -> Long,
    ): Int {
        require(iterations > 0 && warmupIterations > 0)
        val warmup = List(12) { measureSuspending(operation, warmupIterations) }
        val samples = List(20) { measureSuspending(operation, iterations) }
        reportIsolated(name, warmup, samples)
        return warmupIterations * 12 + iterations * 20
    }

    private suspend fun measureSuspending(operation: suspend () -> Long, iterations: Int): Sample {
        val thread = Thread.currentThread().threadId()
        val bytes = allocations.getThreadAllocatedBytes(thread)
        val start = System.nanoTime()
        var result = 0L
        repeat(iterations) { result = result xor operation() }
        val elapsed = System.nanoTime() - start
        val allocated = allocations.getThreadAllocatedBytes(thread) - bytes
        check(Thread.currentThread().threadId() == thread) { "allocation measurement crossed threads" }
        sink = result
        return Sample(elapsed.toDouble() / iterations, allocated.toDouble() / iterations)
    }

    private fun reportIsolated(name: String, warmup: List<Sample>, samples: List<Sample>) {
        fun median(value: (Sample) -> Double): Double =
            samples.map(value).sorted().let { (it[9] + it[10]) / 2 }
        println(String.format(Locale.ROOT, "%s: %.3f ns/op; %.3f B/op; sink=%d", name,
            median { it.nanos }, median { it.bytes }, sink))
        warmup.forEach { println("warmup $name ${it.nanos} ${it.bytes}") }
        samples.forEach { println("sample $name ${it.nanos} ${it.bytes}") }
    }
}
