package com.luxo.client

import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicInteger
import java.net.InetSocketAddress
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
