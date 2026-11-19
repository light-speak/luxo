package com.luxo.client

import java.io.IOException
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.net.SocketException
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.ConcurrentLinkedQueue
import javax.net.SocketFactory
import java.net.InetSocketAddress
import com.sun.net.httpserver.HttpServer
import com.sun.net.httpserver.HttpExchange
import kotlinx.coroutines.runBlocking
import okhttp3.Call
import okhttp3.Connection
import okhttp3.EventListener
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.RequestBody.Companion.toRequestBody
import okio.Buffer
import kotlin.test.*

class HttpSocketTest {
    private val generatedRequest = object : GeneratedRequest {
        override val apiID = 1
        override fun writeParameters(encoder: LuxoEncoder) {}
        override fun toJSONParameters(): Map<String, Any?> = emptyMap()
    }

    @Test
    fun `one shot bodies retain bytes for an explicit authenticated retry`() {
        for (content in listOf(byteArrayOf(), byteArrayOf(0, -1, 42), "发布 café".toByteArray())) {
            for (type in listOf(HttpProtocolHeaders.JSON_MEDIA_TYPE, HttpProtocolHeaders.BINARY_MEDIA_TYPE)) {
                val body = HTTPRequestBody(content, type)
                assertTrue(body.isOneShot())
                assertSame(type, body.contentType())
                assertEquals(content.size.toLong(), body.contentLength())
                repeat(2) {
                    val buffer = Buffer()
                    body.writeTo(buffer)
                    assertContentEquals(content, buffer.readByteArray())
                }
            }
        }
    }

    @Test
    fun `timeout response cannot implicitly replay a POST`() = runBlocking { assertNoImplicitReplays(408) }

    @Test
    fun `overload response cannot implicitly replay a POST`() = runBlocking { assertNoImplicitReplays(503) }

    private suspend fun assertNoImplicitReplays(status: Int) {
        for (mode in TransportMode.entries) for (generated in listOf(false, true)) {
            for (defaults in listOf(false, true)) {
                assertNoImplicitReplay(mode, generated, defaults, status)
            }
        }
    }

    private suspend fun assertNoImplicitReplay(mode: TransportMode, generated: Boolean, defaults: Boolean, status: Int) {
        val calls = AtomicInteger()
        withHTTPPeer(defaults, respond = { exchange, _ ->
            exchange.responseHeaders.add("Retry-After", "0")
            reply(exchange, mode, if (calls.incrementAndGet() == 1) status else 200)
        }) { client, endpoint ->
            val transport = replayTransport(endpoint, client, mode)
            val outcome = runCatching { call(transport, generated) }
            assertEquals(1, calls.get(), "one invocation executed multiple POSTs: mode=$mode generated=$generated defaults=$defaults status=$status")
            val error = assertIs<LuxoError>(outcome.exceptionOrNull())
            assertEquals(status, error.code)
            assertEquals("Unavailable", error.error)
            call(transport, generated)
            assertEquals(2, calls.get(), "an explicit later invocation must remain possible")
        }
    }

    @Test
    fun `lost response never replays an executed POST`() = runBlocking {
        for (mode in TransportMode.entries) for (generated in listOf(false, true)) {
            for (defaults in listOf(false, true)) assertLostResponse(mode, generated, defaults, false)
        }
    }

    @Test
    fun `replayable body control repeats a POST after losing its response`() = runBlocking {
        for (mode in TransportMode.entries) assertLostResponse(mode, false, false, true)
    }

    private suspend fun assertLostResponse(mode: TransportMode, generated: Boolean, defaults: Boolean, replayable: Boolean) {
        val calls = AtomicInteger()
        val bodies = ConcurrentLinkedQueue<ByteArray>()
        val ports = ConcurrentLinkedQueue<Int>()
        withHTTPPeer(defaults, respond = { exchange, body ->
            bodies.add(body)
            ports.add(exchange.remoteAddress.port)
            // Warm the pool, then lose the response after accepting the next POST.
            if (calls.incrementAndGet() != 2) reply(exchange, mode, 200)
        }) { original, endpoint ->
            val client = if (!replayable) original else original.newBuilder().addInterceptor { chain ->
                val request = chain.request()
                val output = Buffer()
                request.body!!.writeTo(output)
                val body = output.readByteArray().toRequestBody(request.body!!.contentType())
                chain.proceed(request.newBuilder().post(body).build())
            }.build()
            val transport = replayTransport(endpoint, client, mode)
            call(transport, generated)
            val outcome = runCatching { call(transport, generated) }
            if (replayable) {
                assertTrue(outcome.isSuccess, "legacy-body control did not retry")
                assertEquals(3, calls.get(), "legacy-body control did not reproduce duplicate POST")
            } else {
                assertEquals("NetworkError", assertIs<LuxoError>(outcome.exceptionOrNull()).error)
                assertEquals(2, calls.get(), "executed POST was replayed after losing its response")
                call(transport, generated)
                assertEquals(3, calls.get(), "explicit next call must remain usable")
            }
            assertEquals(ports.elementAt(0), ports.elementAt(1), "probe did not reuse the warmed connection")
            val sent = bodies.toList()
            assertEquals(3, sent.size)
            for (body in sent.drop(1)) assertContentEquals(sent[0], body)
        }
    }

    @Test
    fun `real unauthorized refresh starts a new call with the same request bytes`() = runBlocking {
        for (mode in TransportMode.entries) for (generated in listOf(false, true)) {
            val bodies = ConcurrentLinkedQueue<ByteArray>()
            val tokens = ConcurrentLinkedQueue<String>()
            val refreshes = AtomicInteger()
            withHTTPPeer(true, respond = { exchange, body ->
                bodies.add(body)
                tokens.add(exchange.requestHeaders.getFirst("Authorization"))
                reply(exchange, mode, if (bodies.size == 1) 401 else 200)
            }) { client, endpoint ->
                val transport = replayTransport(endpoint, client, mode) { refreshes.incrementAndGet(); "renewed" }
                call(transport, generated)
                assertEquals(listOf("Bearer original", "Bearer renewed"), tokens.toList())
                assertEquals(2, bodies.size)
                assertContentEquals(bodies.elementAt(0), bodies.elementAt(1))
                assertEquals(1, refreshes.get())
            }
        }
    }

    private fun replayTransport(endpoint: String, client: OkHttpClient, mode: TransportMode, refresh: (suspend () -> String?)? = null) =
        OkHttpTransport(endpoint, client = client, mode = mode, token = "original", maxConcurrentRequests = 1,
            schema = mapOf("mutate" to APISchemaEntry(1)), onTokenExpired = refresh)

    private suspend fun call(transport: OkHttpTransport, generated: Boolean): Any =
        if (generated) transport.call("mutate", generatedRequest) else transport.call("mutate")

    private suspend fun withHTTPPeer(
        defaults: Boolean, respond: (HttpExchange, ByteArray) -> Unit, check: suspend (OkHttpClient, String) -> Unit,
    ) {
        val server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
        server.createContext("/") { exchange ->
            exchange.use { respond(it, it.requestBody.use { input -> input.readBytes() }) }
        }
        server.start()
        val client = if (defaults) defaultHTTPClient(3, 1) else OkHttpClient.Builder().build()
        try { check(client, "http://127.0.0.1:${server.address.port}/") }
        finally {
            server.stop(0)
            client.connectionPool.evictAll()
            client.dispatcher.executorService.shutdown()
        }
    }

    private fun reply(exchange: HttpExchange, mode: TransportMode, status: Int) {
        val body = if (status != 200) replayErrorBody(mode, status)
            else if (mode == TransportMode.JSON) "{\"data\":42}".toByteArray() else byteArrayOf(84)
        exchange.sendResponseHeaders(status, body.size.toLong())
        exchange.responseBody.use { it.write(body) }
    }

    private fun replayErrorBody(mode: TransportMode, code: Int): ByteArray {
        val name = if (code == 401) "Unauthorized" else "Unavailable"
        if (mode == TransportMode.JSON) return """{"error":"$name","code":$code,"message":"retry later"}""".toByteArray()
        return LuxoEncoder().apply {
            writeFieldInt(1, code.toLong())
            writeFieldString(2, name)
            writeFieldString(3, "retry later")
            writeEnd()
        }.bytes()
    }

    @Test
    fun `real HTTP1 connections use default option but preserve injected clients`() = runBlocking {
        val server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
        server.createContext("/") { exchange ->
            exchange.use {
                it.requestBody.use { input -> input.readBytes() }
                val body = "{\"data\":42}".toByteArray()
                it.sendResponseHeaders(200, body.size.toLong())
                it.responseBody.use { output -> output.write(body) }
            }
        }
        server.start()
        try {
            for (defaults in listOf(false, true)) {
                var observed: Boolean? = null
                val builder = if (defaults) defaultHTTPClient(7, 9).newBuilder() else OkHttpClient.Builder()
                val client = builder.eventListener(object : EventListener() {
                    override fun connectionAcquired(call: Call, connection: Connection) {
                        assertEquals(Protocol.HTTP_1_1, connection.protocol())
                        observed = connection.socket().tcpNoDelay
                    }
                }).build()
                try {
                    val transport = OkHttpTransport("http://127.0.0.1:${server.address.port}/", client = client)
                    transport.call("ping")
                    assertEquals(defaults, observed)
                } finally {
                    client.connectionPool.evictAll()
                    client.dispatcher.executorService.shutdown()
                }
            }
        } finally { server.stop(0) }
    }

    @Test
    fun `default client disables Nagle before connecting`() {
        val client = defaultHTTPClient(7, 9)
        client.socketFactory.createSocket().use { socket ->
            assertFalse(socket.isConnected)
            assertTrue(socket.tcpNoDelay)
        }
        assertEquals(7000, client.callTimeoutMillis)
        assertEquals(9, client.dispatcher.maxRequestsPerHost)
        client.dispatcher.executorService.shutdown()
        client.connectionPool.evictAll()
    }

    @Test
    fun `all connected overloads preserve endpoint and disable Nagle`() {
        val loopback = InetAddress.getByName("127.0.0.1")
        ServerSocket(0, 8, loopback).use { server ->
            val factory = HTTPNoDelaySocketFactory()
            val create = listOf<() -> Socket>(
                { factory.createSocket("127.0.0.1", server.localPort) },
                { factory.createSocket(loopback, server.localPort) },
                { factory.createSocket("127.0.0.1", server.localPort, loopback, 0) },
                { factory.createSocket(loopback, server.localPort, loopback, 0) },
            )
            for (operation in create) operation().use { socket ->
                server.accept().use {
                    assertTrue(socket.tcpNoDelay)
                    assertEquals(server.localPort, socket.port)
                    assertEquals(loopback, socket.localAddress)
                }
            }
        }
    }

    @Test
    fun `failed configuration closes socket and preserves failure`() {
        for (closeFails in listOf(false, true)) {
            val failure = SocketException("option rejected")
            val closeFailure = IOException("close rejected")
            var closed = false
            val socket = object : Socket() {
                override fun setTcpNoDelay(on: Boolean) { throw failure }
                override fun close() {
                    closed = true
                    if (closeFails) throw closeFailure
                }
            }
            val delegate = object : SocketFactory() {
                override fun createSocket() = socket
                override fun createSocket(host: String, port: Int) = socket
                override fun createSocket(host: String, port: Int, local: InetAddress, localPort: Int) = socket
                override fun createSocket(host: InetAddress, port: Int) = socket
                override fun createSocket(host: InetAddress, port: Int, local: InetAddress, localPort: Int) = socket
            }
            assertSame(failure, assertFailsWith<SocketException> {
                HTTPNoDelaySocketFactory(delegate).createSocket()
            })
            assertTrue(closed)
            assertEquals(if (closeFails) listOf(closeFailure) else emptyList(), failure.suppressed.toList())
        }
    }
}
