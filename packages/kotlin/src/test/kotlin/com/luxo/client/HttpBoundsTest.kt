package com.luxo.client

import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference
import java.net.InetSocketAddress
import java.io.IOException
import java.lang.ref.WeakReference
import com.sun.net.httpserver.HttpServer
import kotlinx.coroutines.withTimeout
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.async
import kotlinx.coroutines.runBlocking
import okhttp3.*
import okhttp3.ResponseBody.Companion.toResponseBody
import okio.Buffer
import okio.BufferedSource
import okio.ForwardingSource
import okio.buffer
import kotlin.test.*

class HttpBoundsTest {
    private val schema = mapOf("read" to APISchemaEntry(1))

    @Test
    fun `completed HTTP bodies are released before the caller suspends again`() = runBlocking {
        val retained = mutableListOf<String>()
        for (timeout in listOf(0L, 30L)) for (mode in TransportMode.entries) for (generated in listOf(false, true)) {
            val observed = AtomicReference<WeakReference<RequestBody>>()
            val client = OkHttpClient.Builder().addInterceptor { chain ->
                observed.set(WeakReference(chain.request().body!!))
                response(chain.request(), (if (mode == TransportMode.JSON) "{\"data\":42}".toByteArray() else byteArrayOf(84)).toResponseBody())
            }.build()
            try {
                val transport = OkHttpTransport("https://example.test", client = client, mode = mode, timeoutSeconds = timeout,
                    schema = mapOf("read" to APISchemaEntry(7, params = listOf(ParamSchema(1, "blob", "Bytes")))))
                val value = inputLifetimeCall(transport, generated, mutableListOf())
                if (mode == TransportMode.JSON) assertEquals("42", value.toString())
                else assertContentEquals(byteArrayOf(84), value as ByteArray)
                if (retainsCompletedBody(client, assertNotNull(observed.get()))) retained += "$mode/$generated/$timeout"
            } finally { cleanup(client) }
        }
        assertTrue(retained.isEmpty(), "completed bodies retained: $retained")
    }

    @Test
    fun `failed HTTP bodies are released before the caller suspends again`() = runBlocking {
        val retained = mutableListOf<String>()
        for (timeout in listOf(0L, 30L)) for (mode in TransportMode.entries) for (kind in listOf("network", "parse", "business", "refresh")) {
            val observed = AtomicReference<WeakReference<RequestBody>>()
            val failure = IllegalStateException("refresh failed")
            val status = if (kind == "refresh") 401 else 400
            val client = OkHttpClient.Builder().addInterceptor { chain ->
                observed.set(WeakReference(chain.request().body!!))
                if (kind == "network") throw IOException("network failed")
                val bytes = if (kind != "parse") terminalErrorBody(mode, status)
                    else if (mode == TransportMode.JSON) "{".toByteArray() else ByteArray(0)
                response(chain.request(), bytes.toResponseBody()).newBuilder().code(if (kind == "parse") 200 else status).build()
            }.build()
            try {
                val transport = OkHttpTransport("https://example.test", client = client, mode = mode, timeoutSeconds = timeout,
                    schema = mapOf("read" to APISchemaEntry(7, params = listOf(ParamSchema(1, "blob", "Bytes")))),
                    onTokenExpired = { throw failure })
                val error = runCatching { inputLifetimeCall(transport, false, mutableListOf()) }.exceptionOrNull()
                if (kind == "refresh") assertSame(failure, error)
                else assertEquals(when (kind) { "network" -> "NetworkError"; "parse" -> "ParseError"; else -> "Bad" }, assertIs<LuxoError>(error).error)
                if (retainsCompletedBody(client, assertNotNull(observed.get()))) retained += "$mode/$kind/$timeout"
            } finally { cleanup(client) }
        }
        assertTrue(retained.isEmpty(), "failed bodies retained: $retained")
    }

    @Test
    fun `retirement preserves a request body retained by the caller`() = runBlocking {
        for (mode in TransportMode.entries) {
            val captured = AtomicReference<Request>()
            val before = AtomicReference<ByteArray>()
            val client = OkHttpClient.Builder().addInterceptor { chain ->
                captured.set(chain.request())
                before.set(Buffer().also { chain.request().body!!.writeTo(it) }.readByteArray())
                response(chain.request(), (if (mode == TransportMode.JSON) "{\"data\":42}".toByteArray() else byteArrayOf(84)).toResponseBody())
            }.build()
            try {
                val transport = OkHttpTransport("https://example.test", client = client, mode = mode,
                    schema = mapOf("read" to APISchemaEntry(7, params = listOf(ParamSchema(1, "blob", "Bytes")))))
                inputLifetimeCall(transport, false, mutableListOf())
                val after = Buffer().also { captured.get().body!!.writeTo(it) }.readByteArray()
                assertTrue(after.size >= 256 * 1024)
                assertContentEquals(before.get(), after)
            } finally { cleanup(client) }
        }
    }

    private fun retainsCompletedBody(client: OkHttpClient, body: WeakReference<RequestBody>): Boolean {
        repeat(100) { if (client.dispatcher.runningCallsCount() != 0) Thread.sleep(1) }
        assertEquals(0, client.dispatcher.runningCallsCount())
        val collected = WeakReference(Any())
        val strong = Any()
        val reachable = WeakReference(strong)
        repeat(12) { System.gc(); Thread.sleep(10) }
        assertNull(collected.get())
        assertSame(strong, reachable.get())
        return body.get() != null
    }

    private fun terminalErrorBody(mode: TransportMode, status: Int): ByteArray =
        if (mode == TransportMode.JSON) "{\"error\":\"Bad\",\"code\":$status,\"message\":\"failed\"}".toByteArray()
        else LuxoEncoder().apply {
            writeVarint(1); writeSvarint(status.toLong()); writeVarint(2); writeString("Bad")
            writeVarint(3); writeString("failed"); writeEnd()
        }.bytes()

    @Test
    fun `encoded inputs are released while waiting for an HTTP reply`() = runBlocking {
        val retained = mutableListOf<String>()
        for (timeout in listOf(0L, 30L)) for (mode in TransportMode.entries) for (generated in listOf(false, true)) {
            val started = CountDownLatch(1)
            val finish = CountDownLatch(1)
            val client = OkHttpClient.Builder().addInterceptor { chain ->
                val buffer = Buffer()
                chain.request().body!!.writeTo(buffer)
                assertTrue(buffer.size >= 256 * 1024)
                started.countDown()
                check(finish.await(10, TimeUnit.SECONDS))
                response(chain.request(), (if (mode == TransportMode.JSON) "{\"data\":42}".toByteArray() else byteArrayOf(84)).toResponseBody())
            }.build()
            val transport = OkHttpTransport("https://example.test", client = client, mode = mode,
                schema = mapOf("read" to APISchemaEntry(7, params = listOf(ParamSchema(1, "blob", "Bytes")))),
                timeoutSeconds = timeout)
            val refs = mutableListOf<WeakReference<Any>>()
            val pending = async(start = CoroutineStart.UNDISPATCHED) { inputLifetimeCall(transport, generated, refs) }
            try {
                assertTrue(started.await(5, TimeUnit.SECONDS))
                val collected = WeakReference(Any())
                val strong = Any()
                val reachable = WeakReference(strong)
                repeat(12) { System.gc(); Thread.sleep(10) }
                assertNull(collected.get(), "GC control did not clear an unreferenced object")
                assertSame(strong, reachable.get())
                if (refs.any { it.get() != null }) retained += "$mode generated=$generated timeout=$timeout"
            } finally {
                finish.countDown()
                try {
                    val result = pending.await()
                    if (mode == TransportMode.JSON) assertEquals("42", result.toString())
                    else assertContentEquals(byteArrayOf(84), result as ByteArray)
                } finally { cleanup(client) }
            }
        }
        assertTrue(retained.isEmpty(), "retained encoded inputs: $retained")
    }

    @Test
    fun `generated encoding preserves credential snapshot order`() = runBlocking {
        for (mode in TransportMode.entries) {
            val seen = mutableListOf<String?>()
            val client = OkHttpClient.Builder().addInterceptor { chain ->
                seen += chain.request().header("Authorization")
                response(chain.request(), (if (mode == TransportMode.JSON) "{\"data\":42}".toByteArray() else byteArrayOf(84)).toResponseBody())
            }.build()
            val transport = OkHttpTransport("https://example.test", token = "old", client = client, mode = mode, schema = schema)
            val request = object : GeneratedRequest {
                override val apiID = 1
                override fun writeParameters(encoder: LuxoEncoder) { transport.setToken("new") }
                override fun toJSONParameters(): Map<String, Any?> { transport.setToken("new"); return emptyMap() }
            }
            try {
                transport.call("read", request)
                assertEquals(listOf<String?>(if (mode == TransportMode.JSON) "Bearer new" else "Bearer old"), seen)
            } finally { cleanup(client) }
        }
    }

    @Test
    fun `encoding uses the original deadline and preserves late encoding failures`() = runBlocking {
        for (mode in TransportMode.entries) for (throws in listOf(false, true)) {
            val client = OkHttpClient.Builder().addInterceptor { chain ->
                response(chain.request(), byteArrayOf(84).toResponseBody())
            }.build()
            val failure = IllegalStateException("late encoding failure")
            val request = object : GeneratedRequest {
                override val apiID = 1
                private fun encode() {
                    Thread.sleep(1100)
                    if (throws) throw failure
                }
                override fun writeParameters(encoder: LuxoEncoder) { encode() }
                override fun toJSONParameters(): Map<String, Any?> { encode(); return emptyMap() }
            }
            try {
                val transport = OkHttpTransport("https://example.test", client = client, mode = mode,
                    schema = schema, timeoutSeconds = 1)
                val error = runCatching { transport.call("read", request) }.exceptionOrNull()
                if (throws) assertSame(failure, error)
                else assertEquals("TimeoutError", assertIs<LuxoError>(error).error)
            } finally { cleanup(client) }
        }
    }

    private suspend fun inputLifetimeCall(transport: OkHttpTransport, generated: Boolean, refs: MutableList<WeakReference<Any>>): Any {
        val blob = ByteArray(256 * 1024)
        val params = mapOf("blob" to blob)
        refs += WeakReference(blob)
        refs += WeakReference(params)
        return if (generated) {
            val request = object : GeneratedRequest {
                override val apiID = 7
                override fun writeParameters(encoder: LuxoEncoder) { encoder.writeVarint(1); encoder.writeBytes(blob) }
                override fun toJSONParameters(): Map<String, Any?> = params
            }
            refs += WeakReference(request)
            transport.call("read", request)
        } else transport.call("read", params)
    }

    @Test
    fun `request creation and network failures return exactly one capacity slot`() = runBlocking {
        for (synchronous in listOf(true, false)) {
            val attempts = AtomicInteger()
            val started = CountDownLatch(1)
            val finish = CountDownLatch(1)
            val client = OkHttpClient.Builder().eventListenerFactory {
                if (synchronous && attempts.getAndIncrement() == 0) error("creation failed")
                EventListener.NONE
            }.addInterceptor { chain ->
                if (!synchronous && attempts.getAndIncrement() == 0) throw IOException("network failed")
                started.countDown()
                check(finish.await(5, TimeUnit.SECONDS))
                response(chain.request(), byteArrayOf(42).toResponseBody())
            }.build()
            try {
                val transport = OkHttpTransport("https://example.test", client = client,
                    mode = TransportMode.BINARY, schema = schema, maxConcurrentRequests = 1)
                assertEquals("NetworkError", assertFailsWith<LuxoError> { transport.call("read") }.error)
                val pending = async(start = CoroutineStart.UNDISPATCHED) { transport.call("read") }
                assertTrue(started.await(5, TimeUnit.SECONDS))
                assertEquals(503, assertFailsWith<LuxoError> { transport.call("read") }.code)
                assertEquals(2, attempts.get())
                finish.countDown()
                assertContentEquals(byteArrayOf(42), pending.await() as ByteArray)
                assertContentEquals(byteArrayOf(42), transport.call("read") as ByteArray)
            } finally { finish.countDown(); cleanup(client) }
        }
    }

    @Test
    fun `declared length mismatch is rejected even within collection limit`() = runBlocking {
        for (actual in listOf(63, 65)) {
            var closed = false
            val body = object : ResponseBody() {
                override fun contentType(): MediaType? = null
                override fun contentLength() = 64L
                private val input = object : ForwardingSource(Buffer().write(ByteArray(actual))) {
                    override fun close() { closed = true; super.close() }
                }.buffer()
                override fun source(): BufferedSource = input
            }
            val client = OkHttpClient.Builder().addInterceptor { chain -> response(chain.request(), body) }.build()
            try {
                val transport = OkHttpTransport("https://example.test", client = client,
                    mode = TransportMode.BINARY, schema = schema, maxResponseBytes = 128)
                assertEquals("NetworkError", assertFailsWith<LuxoError> { transport.call("read") }.error)
                assertTrue(closed)
            } finally { cleanup(client) }
        }
    }

    @Test
    fun `caller cancellation cannot release capacity before body cleanup`() = runBlocking {
        val closing = CountDownLatch(1)
        val release = CountDownLatch(1)
        val idle = CountDownLatch(1)
        val calls = AtomicInteger()
        val client = OkHttpClient.Builder().addInterceptor { chain ->
            val body = if (calls.getAndIncrement() == 0) closingBody(closing, release)
                else byteArrayOf(42).toResponseBody()
            response(chain.request(), body)
        }.build()
        client.dispatcher.idleCallback = Runnable { idle.countDown() }
        try {
            val transport = OkHttpTransport("https://example.test", client = client,
                mode = TransportMode.BINARY, schema = schema, maxConcurrentRequests = 1)
            val first = async(start = CoroutineStart.UNDISPATCHED) { transport.call("read") }
            assertTrue(closing.await(5, TimeUnit.SECONDS))
            first.cancel()
            withTimeout(2000) { first.join() }
            assertEquals(503, assertFailsWith<LuxoError> { transport.call("read") }.code)
            assertEquals(1, calls.get())
            release.countDown()
            assertTrue(idle.await(5, TimeUnit.SECONDS))
            assertContentEquals(byteArrayOf(42), transport.call("read") as ByteArray)
        } finally { release.countDown(); cleanup(client) }
    }

    private fun closingBody(closing: CountDownLatch, release: CountDownLatch): ResponseBody =
        object : ResponseBody() {
            override fun contentType(): MediaType? = null
            override fun contentLength() = 1L
            private val input = object : ForwardingSource(Buffer().writeByte(42)) {
                override fun close() {
                    closing.countDown()
                    check(release.await(5, TimeUnit.SECONDS))
                    super.close()
                }
            }.buffer()
            override fun source(): BufferedSource = input
        }

    @Test
    fun `real unfinished response rejects before EOF and leaves client reusable`() = runBlocking {
        val release = CountDownLatch(1)
        val executor = Executors.newFixedThreadPool(2)
        val server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
        server.executor = executor
        server.createContext("/overflow") { exchange ->
            exchange.use {
                it.requestBody.close()
                it.responseHeaders.set("Content-Type", "application/x-luxo")
                it.sendResponseHeaders(200, 0)
                it.responseBody.write(ByteArray(65))
                it.responseBody.flush()
                release.await(5, TimeUnit.SECONDS)
            }
        }
        server.createContext("/small") { exchange ->
            exchange.use {
                it.requestBody.close()
                it.sendResponseHeaders(200, 1)
                it.responseBody.write(byteArrayOf(42))
            }
        }
        server.start()
        val client = OkHttpClient()
        try {
            fun transport(path: String) = OkHttpTransport("http://127.0.0.1:${server.address.port}/$path",
                client = client, mode = TransportMode.BINARY, schema = schema, maxResponseBytes = 64)
            val error = assertFailsWith<LuxoError> { withTimeout(2000) { transport("overflow").call("read") } }
            assertEquals("ParseError", error.error)
            assertEquals(1L, release.count)
            assertContentEquals(byteArrayOf(42), withTimeout(2000) { transport("small").call("read") } as ByteArray)
        } finally {
            release.countDown()
            cleanup(client)
            server.stop(0)
            executor.shutdownNow()
        }
    }

    @Test
    fun `exact boundary succeeds and declared overflow never reads body`() = runBlocking {
        for (declared in listOf(64L, 65L)) {
            var read = false
            var closed = false
            val body = object : ResponseBody() {
                override fun contentType(): MediaType? = null
                override fun contentLength() = declared
                private val input = object : ForwardingSource(Buffer().write(ByteArray(64))) {
                    override fun read(sink: Buffer, byteCount: Long): Long {
                        read = true
                        return super.read(sink, byteCount)
                    }
                    override fun close() { closed = true; super.close() }
                }.buffer()
                override fun source(): BufferedSource = input
            }
            val client = OkHttpClient.Builder().addInterceptor { chain -> response(chain.request(), body) }.build()
            try {
                val transport = OkHttpTransport("https://example.test", client = client,
                    mode = TransportMode.BINARY, schema = schema, maxResponseBytes = 64)
                if (declared == 64L) {
                    assertEquals(64, (transport.call("read") as ByteArray).size)
                    assertTrue(read)
                } else {
                    assertEquals("ParseError", assertFailsWith<LuxoError> { transport.call("read") }.error)
                    assertFalse(read)
                }
                assertTrue(closed)
            } finally { cleanup(client) }
        }
    }

    @Test
    fun `injected dispatcher and timeouts remain unchanged`() {
        val dispatcher = Dispatcher().apply { maxRequests = 9; maxRequestsPerHost = 3 }
        val client = OkHttpClient.Builder().dispatcher(dispatcher)
            .callTimeout(7, TimeUnit.SECONDS).readTimeout(11, TimeUnit.SECONDS).build()
        try {
            OkHttpTransport("https://example.test", client = client, timeoutSeconds = 2,
                maxConcurrentRequests = 16)
            assertEquals(9, dispatcher.maxRequests)
            assertEquals(3, dispatcher.maxRequestsPerHost)
            assertEquals(7000, client.callTimeoutMillis)
            assertEquals(11000, client.readTimeoutMillis)
        } finally { cleanup(client) }
    }

    @Test
    fun `limits must be positive`() {
        for (value in listOf(0, -1)) {
            assertFailsWith<IllegalArgumentException> { OkHttpTransport("https://example.test", maxResponseBytes = value) }
            assertFailsWith<IllegalArgumentException> { OkHttpTransport("https://example.test", maxConcurrentRequests = value) }
        }
    }

    @Test
    fun `unknown length body is stopped before EOF and closed`() = runBlocking {
        var closed = false
        val body = object : ResponseBody() {
            override fun contentType(): MediaType? = null
            override fun contentLength() = -1L
            private val input = object : ForwardingSource(Buffer().write(ByteArray(65))) {
                override fun close() { closed = true; super.close() }
            }.buffer()
            override fun source(): BufferedSource = input
        }
        val client = OkHttpClient.Builder().addInterceptor { chain -> response(chain.request(), body) }.build()
        try {
            val transport = OkHttpTransport("https://example.test", client = client,
                mode = TransportMode.BINARY, schema = schema, maxResponseBytes = 64)
            val error = assertFailsWith<LuxoError> { transport.call("read") }
            assertEquals("ParseError", error.error)
            assertTrue(closed)
        } finally { cleanup(client) }
    }

    @Test
    fun `full capacity rejects without enqueue and releases after response close`() = runBlocking {
        val started = CountDownLatch(1)
        val finish = CountDownLatch(1)
        var calls = 0
        val client = OkHttpClient.Builder().addInterceptor { chain ->
            calls++
            started.countDown()
            check(finish.await(5, TimeUnit.SECONDS))
            response(chain.request(), byteArrayOf(42).toResponseBody())
        }.build()
        try {
            val transport = OkHttpTransport("https://example.test", client = client,
                mode = TransportMode.BINARY, schema = schema, maxConcurrentRequests = 1)
            val first = async(start = CoroutineStart.UNDISPATCHED) { transport.call("read") }
            assertTrue(started.await(5, TimeUnit.SECONDS))
            val error = assertFailsWith<LuxoError> { transport.call("read") }
            assertEquals(503, error.code)
            assertEquals(1, calls)
            finish.countDown()
            assertContentEquals(byteArrayOf(42), first.await() as ByteArray)
            assertContentEquals(byteArrayOf(42), transport.call("read") as ByteArray)
            assertEquals(2, calls)
        } finally { finish.countDown(); cleanup(client) }
    }

    private fun response(request: Request, body: ResponseBody) = Response.Builder()
        .request(request).protocol(Protocol.HTTP_1_1).code(200).message("OK").body(body).build()

    private fun cleanup(client: OkHttpClient) {
        client.dispatcher.executorService.shutdownNow()
        client.connectionPool.evictAll()
    }
}
