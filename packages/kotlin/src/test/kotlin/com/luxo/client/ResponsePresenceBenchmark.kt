package com.luxo.client

import java.util.function.LongSupplier
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.*
import okhttp3.*
import okhttp3.ResponseBody.Companion.toResponseBody
import okio.Timeout

/** Real transports behind synchronous HTTP/socket fixtures; only calling-thread allocation is measured. */
object ResponsePresenceBenchmark {
    private var smoke = false

    @JvmStatic
    fun main(args: Array<String>) {
        require(args.all { it == "--smoke" || it == "--http-scoped" })
        smoke = args.contains("--smoke")
        println("JDK ${System.getProperty("java.version")}; isolated implementation; 12 warmup/20 samples")
        if (args.contains("--http-scoped")) {
            runBlocking { measureHTTPScoped() }
            println("Verified scoped HTTP values/counts; no per-request outer runBlocking.")
            return
        }
        measureHTTP()
        for (mode in TransportMode.entries) {
            measureSocket(mode, "unary")
            measureSocket(mode, "setup-ack-cancel")
            measureSocket(mode, "dispatch")
        }
        measureSocket(TransportMode.JSON, "dispatch", nullValue = true)
        println("Verified calls, subscriptions, cancellations and callbacks; current-thread B/op only.")
    }

    private fun measureHTTP() {
        for ((name, value) in httpValues()) {
            measureHTTPValue(name, "{\"data\":$value}".toByteArray(), value, TransportMode.JSON)
        }
        measureHTTPValue("binary", byteArrayOf(14), null, TransportMode.BINARY)
    }

    private fun httpValues(): Map<String, JsonElement> = linkedMapOf(
        "small" to JsonPrimitive(7),
        "null" to JsonNull,
        "nested16" to JsonArray(List(16) { buildJsonObject { put("id", 7); put("name", "sample") } }),
        "unicode4K" to JsonPrimitive("发布检查".repeat(350)),
    )

    private suspend fun measureHTTPScoped() {
        for ((name, value) in httpValues()) {
            measureHTTPScopedValue(name, "{\"data\":$value}".toByteArray(), value, TransportMode.JSON)
        }
        measureHTTPScopedValue("binary", byteArrayOf(14), null, TransportMode.BINARY)
    }

    private suspend fun measureHTTPScopedValue(name: String, body: ByteArray, expected: JsonElement?, mode: TransportMode) {
        val client = ImmediateHTTPClient(body)
        val transport = OkHttpTransport("http://localhost/luvia", client = client, mode = mode,
            schema = mapOf("watch" to APISchemaEntry(77)))
        suspend fun invoke(): Long {
            val data = transport.call("watch")
            if (mode == TransportMode.BINARY) check((data as ByteArray).contentEquals(body))
            else check(data == expected)
            return 1L
        }
        try {
            check(invoke() == 1L)
            val calls = PairedJvmBenchmark.isolatedSuspending("HTTP-scoped/$name",
                if (smoke) 5 else 10_000, if (smoke) 5 else 20_000) { invoke() }
            check(client.calls == calls + 1)
        } finally {
            client.dispatcher.executorService.shutdownNow()
            client.connectionPool.evictAll()
        }
    }

    private fun measureHTTPValue(name: String, body: ByteArray, expected: JsonElement?, mode: TransportMode) {
        val client = ImmediateHTTPClient(body)
        val transport = OkHttpTransport("http://localhost/luvia", client = client, mode = mode,
            schema = mapOf("watch" to APISchemaEntry(77)))
        fun invoke(): Long = runBlocking {
            val data = transport.call("watch")
            if (mode == TransportMode.BINARY) check((data as ByteArray).contentEquals(body))
            else check(data == expected)
            1L
        }
        try {
            check(invoke() == 1L)
            val calls = PairedJvmBenchmark.isolated("HTTP/$name", LongSupplier { invoke() },
                if (smoke) 5 else 10_000, if (smoke) 5 else 20_000)
            check(client.calls == calls + 1)
        } finally {
            client.dispatcher.executorService.shutdownNow()
            client.connectionPool.evictAll()
        }
    }

    private fun measureSocket(mode: TransportMode, operation: String, nullValue: Boolean = false) {
        SocketFixture(mode, nullValue).use { fixture ->
            val cancel = if (operation == "dispatch") runBlocking { fixture.subscribe() } else null
            val action = LongSupplier {
                when (operation) {
                    "unary" -> fixture.call()
                    "setup-ack-cancel" -> runBlocking { fixture.subscribe().invoke() }
                    "dispatch" -> fixture.deliver()
                    else -> error("unknown operation")
                }
                1L
            }
            action.asLong
            val count = 1 + PairedJvmBenchmark.isolated("WS/$mode/$operation/null=$nullValue", action,
                if (smoke) 5 else if (operation == "dispatch") 100_000 else 20_000,
                if (smoke) 5 else if (operation == "dispatch") 100_000 else 50_000)
            cancel?.invoke()
            fixture.verify(operation, count)
        }
    }

    private class SocketFixture(private val mode: TransportMode, private val nullValue: Boolean) : AutoCloseable {
        private val client = TestSocketClient()
        private val transport = LuxoWebSocket("ws://localhost/luvia", client, 0u, 30_000)
        private var calls = 0
        private var starts = 0
        private var cancels = 0
        private var received = 0
        private val data = if (nullValue) "null" else "7"
        private val textFrame = """{"${'$'}stream":1,"data":$data}"""
        private val binaryFrame = frame(0x12, 1, byteArrayOf(14))
        private val observer = StreamObserver(onError = { error("Unexpected error: $it") },
            onComplete = { error("Unexpected completion") })

        init {
            check(!nullValue || mode == TransportMode.JSON)
            transport.setMode(mode)
            transport.setSchema(mapOf("watch" to APISchemaEntry(77)))
            transport.connect()
            client.socket.open()
            client.socket.onText = ::respondJSON
            client.socket.onBinary = { respondBinary(it.toByteArray()) }
        }

        private fun respondJSON(text: String) {
            val json = Json.parseToJsonElement(text).jsonObject
            when {
                json.containsKey("\$sub") -> {
                    starts++
                    client.socket.receive("""{"${'$'}sub":${json.getValue("\$id")},"ok":true}""")
                }
                json.containsKey("\$unsub") -> cancels++
                json.containsKey("\$api") -> {
                    calls++
                    client.socket.receive("""{"${'$'}id":${json.getValue("\$id")},"data":$data}""")
                }
                else -> error("Unexpected request")
            }
        }

        private fun respondBinary(bytes: ByteArray) {
            val decoder = LuxoDecoder(bytes)
            when (decoder.readVarint()) {
                0x10L -> { starts++; client.socket.receive(frame(0x13, decoder.readVarint())) }
                0x11L -> cancels++
                0x01L -> { calls++; client.socket.receive(frame(2, decoder.readVarint(), byteArrayOf(14))) }
                else -> error("Unexpected frame")
            }
        }

        private fun decode(value: Any): Long {
            if (nullValue) { check(value === JsonNull); return 0 }
            val number = if (value is ByteArray) LuxoDecoder(value).run { readInt().also { checkComplete() } }
            else decodeLuxoJsonInt(value as JsonElement)
            check(number == 7L)
            return number
        }

        fun call() = runBlocking { decode(transport.call("watch")) }
        suspend fun subscribe(): () -> Unit = transport.subscribe("watch", ::decode, observer = observer) { received++ }
        fun deliver() {
            if (mode == TransportMode.JSON) client.socket.receive(textFrame) else client.socket.receive(binaryFrame)
        }

        fun verify(operation: String, count: Int) {
            when (operation) {
                "unary" -> check(calls == count && starts == 0 && cancels == 0 && received == 0)
                "setup-ack-cancel" -> check(calls == 0 && starts == count && cancels == count && received == 0)
                "dispatch" -> check(calls == 0 && starts == 1 && cancels == 1 && received == count)
            }
            check(client.socket.sent == calls + starts + cancels)
            val previous = received
            deliver()
            check(previous == received)
        }

        override fun close() {
            transport.close()
            client.dispatcher.executorService.shutdownNow()
            client.connectionPool.evictAll()
        }
    }

    private fun frame(type: Long, id: Long, payload: ByteArray = byteArrayOf()): ByteArray = LuxoEncoder().apply {
        writeVarint(type); writeVarint(id); writeRawBytes(payload)
    }.bytes()

    private class ImmediateHTTPClient(private val body: ByteArray) : OkHttpClient() {
        var calls = 0
            private set

        override fun newCall(request: Request): Call = object : Call {
            private var executed = false
            private var cancelled = false
            override fun request(): Request = request
            override fun execute(): Response {
                check(!executed && !cancelled)
                executed = true
                calls++
                return Response.Builder().request(request).protocol(Protocol.HTTP_1_1).code(200)
                    .message("fixture").body(body.toResponseBody()).build()
            }
            override fun enqueue(responseCallback: Callback) = responseCallback.onResponse(this, execute())
            override fun cancel() { cancelled = true }
            override fun isExecuted(): Boolean = executed
            override fun isCanceled(): Boolean = cancelled
            override fun timeout(): Timeout = Timeout.NONE
            override fun clone(): Call = newCall(request)
        }
    }
}
