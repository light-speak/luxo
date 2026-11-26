package com.luxo.client

import com.sun.management.ThreadMXBean
import java.lang.management.ManagementFactory
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.ThreadPoolExecutor
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.JsonPrimitive
import okhttp3.Dispatcher
import okhttp3.Interceptor
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import okio.Buffer

private const val fixtureAPI = "sample"
private const val readyToken = "fixture-ready"
private const val expiredToken = "fixture-expired"

private enum class Scenario(val label: String) {
    NO_CALLBACK("no-callback"), CALLBACK_READY("callback-ready"), REFRESH_ONCE("refresh-once"),
}

private data class WireCounts(val requests: Int, val unauthorized: Int, val successful: Int, val refreshes: Int) {
    operator fun minus(other: WireCounts) = WireCounts(
        requests - other.requests, unauthorized - other.unauthorized, successful - other.successful,
        refreshes - other.refreshes,
    )
}

private class FixtureWire(mode: TransportMode) : Interceptor {
    private val binary = mode == TransportMode.BINARY
    private val modeHeader = if (binary) "binary" else "json"
    private val media = if (binary) "application/x-luxo".toMediaType() else "application/json; charset=utf-8".toMediaType()
    private val input = if (binary) byteArrayOf(7, 0, 0) else "{\"${'$'}api\":\"$fixtureAPI\"}".toByteArray()
    private val success = if (binary) byteArrayOf(14) else "{\"data\":7}".toByteArray()
    private val rejection = if (binary) LuxoEncoder().apply {
        writeFieldInt(1, 401)
        writeFieldString(2, "Unauthorized")
        writeFieldString(3, "expired")
        writeEnd()
    }.bytes() else "{\"code\":401,\"error\":\"Unauthorized\",\"message\":\"expired\"}".toByteArray()
    private val requestBody = Buffer()
    @Volatile private var retryStart = -1
    @Volatile var requests = 0
        private set
    @Volatile var unauthorized = 0
        private set
    @Volatile var successful = 0
        private set

    fun startBatch(retry: Boolean) {
        retryStart = if (retry) requests else -1
    }

    override fun intercept(chain: Interceptor.Chain): Response {
        val request = chain.request()
        check(request.method == "POST" && request.url.encodedPath == "/luvia")
        check(request.header("X-Luxo-Mode") == modeHeader && request.header("Content-Type") == media.toString())
        checkNotNull(request.body).writeTo(requestBody)
        try {
            check(requestBody.size == input.size.toLong() && input.indices.all { requestBody[it.toLong()] == input[it] }) {
                "unexpected $modeHeader request payload"
            }
        } finally {
            requestBody.clear()
        }
        val start = retryStart
        val expired = start >= 0 && (requests - start) % 2 == 0
        check(request.header("Authorization") == if (expired) "Bearer $expiredToken" else "Bearer $readyToken")
        requests++
        if (expired) unauthorized++ else successful++
        return Response.Builder().request(request).protocol(Protocol.HTTP_1_1)
            .code(if (expired) 401 else 200)
            .message(if (expired) "Unauthorized" else "OK")
            .body((if (expired) rejection else success).toResponseBody(media)).build()
    }
}

private class HttpBenchmarkCase(mode: TransportMode, private val scenario: Scenario, size: Int) : AutoCloseable {
    private val caller = Thread.currentThread().threadId()
    private val ioThread = AtomicReference<Thread>()
    private val executor = ThreadPoolExecutor(
        1, 1, 0, TimeUnit.SECONDS, LinkedBlockingQueue(),
        { task -> Thread(task, "http-benchmark-okhttp").also { check(ioThread.compareAndSet(null, it)) } },
    )
    private val wire = FixtureWire(mode)
    private val client = OkHttpClient.Builder().followRedirects(false).followSslRedirects(false)
        .dispatcher(Dispatcher(executor)).addInterceptor(wire).build()
    private var refreshCalls = 0
    private val callback: (suspend () -> String?)? = if (scenario == Scenario.NO_CALLBACK) null else suspend {
        check(Thread.currentThread().threadId() == caller) { "refresh work crossed the measured caller thread" }
        refreshCalls++
        readyToken
    }
    private val params = emptyMap<String, Any?>()
    private val binary = mode == TransportMode.BINARY
    private val expectedBinary = byteArrayOf(14)
    private val expectedJSON = JsonPrimitive(7)
    private val schema = mapOf(fixtureAPI to APISchemaEntry(id = 7, responseType = null))
    private val transports = Array(if (scenario == Scenario.REFRESH_ONCE) size else 1) {
        OkHttpTransport("http://benchmark.invalid/luvia", token = readyToken, client = client,
            mode = mode, schema = schema, onTokenExpired = callback, timeoutSeconds = 0)
    }

    init { check(executor.prestartAllCoreThreads() == 1) }

    private fun snapshot() = WireCounts(wire.requests, wire.unauthorized, wire.successful, refreshCalls)

    private suspend fun call(transport: OkHttpTransport) {
        val value = transport.call(fixtureAPI, params)
        check(if (binary) value is ByteArray && value.contentEquals(expectedBinary) else value == expectedJSON)
    }

    suspend fun warmEndpoints() {
        for (transport in transports) call(transport)
        awaitOkHttp()
        check(snapshot() == WireCounts(transports.size, 0, transports.size, 0))
    }

    suspend fun runBatch(size: Int, bean: ThreadMXBean?): String {
        val retry = scenario == Scenario.REFRESH_ONCE
        if (retry) {
            check(size == transports.size)
            transports.forEach { it.setToken(expiredToken) }
        }
        val before = snapshot()
        wire.startBatch(retry)
        val ioID = checkNotNull(ioThread.get()).threadId()
        val startCallerCPU = bean?.getThreadCpuTime(caller)
        val startIoCPU = bean?.getThreadCpuTime(ioID)
        val startCallerBytes = bean?.getThreadAllocatedBytes(caller)
        val startIoBytes = bean?.getThreadAllocatedBytes(ioID)
        val startTime = if (bean == null) 0L else System.nanoTime()
        repeat(size) { call(transports[if (retry) it else 0]) }
        awaitOkHttp()
        val endTime = if (bean == null) 0L else System.nanoTime()
        val endCallerCPU = bean?.getThreadCpuTime(caller)
        val endIoCPU = bean?.getThreadCpuTime(ioID)
        val endCallerBytes = bean?.getThreadAllocatedBytes(caller)
        val endIoBytes = bean?.getThreadAllocatedBytes(ioID)
        check(Thread.currentThread().threadId() == caller)
        val counts = snapshot() - before
        val expected = if (retry) WireCounts(size * 2, size, size, size) else WireCounts(size, 0, size, 0)
        check(counts == expected) { "$scenario: got $counts, expected $expected" }
        val summary = "$size\t${counts.requests}\t${counts.unauthorized}\t${counts.successful}\t${counts.refreshes}"
        if (bean == null) return summary
        fun perRequest(start: Long?, end: Long?): Double {
            val first = checkNotNull(start)
            val last = checkNotNull(end)
            check(first >= 0 && last >= first)
            return (last - first).toDouble() / size
        }
        return "$summary\t${(endTime - startTime).toDouble() / size}" +
            "\t${perRequest(startCallerCPU, endCallerCPU)}\t${perRequest(startIoCPU, endIoCPU)}" +
            "\t${perRequest(startCallerBytes, endCallerBytes)}\t${perRequest(startIoBytes, endIoBytes)}"
    }

    // One bounded barrier per batch drains dispatcher work after resuming the caller.
    private fun awaitOkHttp() {
        executor.submit {}.get(5, TimeUnit.SECONDS)
    }

    override fun close() {
        client.dispatcher.cancelAll()
        client.connectionPool.evictAll()
        executor.shutdownNow()
        check(executor.awaitTermination(5, TimeUnit.SECONDS)) { "benchmark dispatcher did not terminate" }
    }
}

private fun measurementBean(): ThreadMXBean =
    (ManagementFactory.getThreadMXBean() as ThreadMXBean).apply {
        check(isThreadCpuTimeSupported && isThreadAllocatedMemorySupported)
        isThreadCpuTimeEnabled = true
        isThreadAllocatedMemoryEnabled = true
    }

object HttpSingleFlightBenchmark {
    @JvmStatic
    fun main(args: Array<String>) = runBlocking {
        require(args.size == 3)
        val measure = when (args[0]) {
            "smoke" -> false
            "sample" -> true
            else -> error("expected smoke or sample")
        }
        val size = args[1].toInt()
        require(size > 0)
        val bean = measurementBean()
        val leaves = listOf(TransportMode.JSON, TransportMode.BINARY).flatMap { mode ->
            Scenario.entries.map { mode to it }
        }
        val rotation = args[2].toInt()
        require(rotation in leaves.indices)
        for (index in leaves.indices) {
            val (mode, scenario) = leaves[(index + rotation) % leaves.size]
            val prefix = "${if (measure) "sample" else "smoke"}\t${mode.name}\t${scenario.label}"
            HttpBenchmarkCase(mode, scenario, size).use { fixture ->
                fixture.warmEndpoints()
                if (measure) repeat(12) { fixture.runBatch(size, null) }
                println("$prefix\t${fixture.runBatch(size, if (measure) bean else null)}")
            }
        }
    }
}
