package com.luxo.client

import java.io.IOException
import java.net.InetSocketAddress
import java.util.concurrent.Executors
import java.util.concurrent.ConcurrentLinkedQueue
import com.sun.net.httpserver.HttpServer
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.JsonPrimitive
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.Response
import okhttp3.ResponseBody
import okhttp3.ResponseBody.Companion.toResponseBody
import okio.Buffer
import okio.BufferedSource
import okio.ForwardingSource
import okio.buffer
import kotlin.test.*

class HttpProtocolTest {
    private val endpoint = "http://localhost/luvia"
    private val schema = mapOf("echo" to APISchemaEntry(1))

    @Test
    fun `JSON responses are UTF-8 regardless of the HTTP charset`() = runBlocking {
        val value = "\uFEFF精度 café \uFFFD"
        val json = """{"data":"$value"}""".toByteArray(Charsets.UTF_8)
        for (charset in listOf("utf-8", "iso-8859-1", "utf-16le", "utf-32be")) {
            for (bom in listOf(byteArrayOf(), byteArrayOf(-17, -69, -65))) {
                withClient({ request, _ -> response(request, 200, bom + json, "application/json; charset=$charset") }) { client, _ ->
                    assertEquals(JsonPrimitive(value), transport(client, TransportMode.JSON).call("echo"))
                }
            }
        }
    }

    @Test
    fun `JSON rejects malformed UTF-8 before success or authentication refresh`() = runBlocking {
        val malformed = listOf(byteArrayOf(-1), byteArrayOf(-64, -128), byteArrayOf(-19, -96, -128),
            byteArrayOf(-12, -112, -128, -128), byteArrayOf(-30, -126))
        for (status in listOf(200, 401)) {
            for (bytes in malformed) {
                val prefix = if (status == 200) """{"data":"""" else """{"error":"Unauthorized","code":401,"message":""""
                val body = prefix.toByteArray() + bytes + "\"}".toByteArray()
                var refreshed = 0
                withClient({ request, _ -> response(request, status, body) }) { client, sent ->
                    val transport = transport(client, TransportMode.JSON) { refreshed++; "must-not-be-used" }
                    val error = assertFailsWith<LuxoError> { transport.call("echo") }
                    assertEquals("ParseError", error.error)
                    assertEquals(status, error.code)
                    assertEquals(0, refreshed)
                    assertEquals(1, sent.size)
                }
            }
        }
    }

    @Test
    fun `JSON rejects UTF-16 and UTF-32 documents rather than changing protocol encoding`() = runBlocking {
        val json = """{"data":"precision"}"""
        val encodings = listOf("UTF-16LE" to byteArrayOf(-1, -2), "UTF-16BE" to byteArrayOf(-2, -1),
            "UTF-32LE" to byteArrayOf(-1, -2, 0, 0), "UTF-32BE" to byteArrayOf(0, 0, -2, -1))
        for ((charset, bom) in encodings) {
            val body = bom + json.toByteArray(charset(charset))
            withClient({ request, _ -> response(request, 200, body, "application/json; charset=$charset") }) { client, _ ->
                assertEquals("ParseError", assertFailsWith<LuxoError> {
                    transport(client, TransportMode.JSON).call("echo")
                }.error)
            }
        }
    }

    @Test
    fun `JSON rejects escaped bad characters before success or token refresh`() = runBlocking {
        for (status in listOf(200, 401, 500)) {
            for (raw in listOf("\"\\uD800\"", "{\"\\uDC00\":1}", "{\"v\":\"\\uDBFF\",\"v\":\"ok\"}")) {
                val body = if (status == 200) "{\"data\":$raw}" else
                    "{\"error\":\"Rejected\",\"code\":$status,\"message\":\"no\",\"data\":$raw}"
                var refreshed = 0
                withClient({ request, _ -> response(request, status, body.encodeToByteArray()) }) { client, sent ->
                    val transport = transport(client, TransportMode.JSON) { refreshed++; "must-not-be-used" }
                    assertEquals("ParseError", assertFailsWith<LuxoError> { transport.call("echo") }.error)
                    assertEquals(0, refreshed)
                    assertEquals(1, sent.size)
                }
            }
        }
    }

    @Test
    fun `JSON rejects partial or repeated document BOMs and non-object envelopes`() {
        for (bytes in listOf(byteArrayOf(), byteArrayOf(-17), byteArrayOf(-17, -69),
            byteArrayOf(-17, 0, -65), byteArrayOf(-17, -69, 0),
            "\uFEFF\uFEFF{}".toByteArray(), "[]".toByteArray(), "null".toByteArray())) {
            assertFails { decodeHTTPJSON(bytes) }
        }
    }

    @Test
    fun `real chunked HTTP keeps UTF-8 boundaries and never refreshes corrupt responses`() = runBlocking {
        val valid = "\uFEFF" + """{"data":"精度 café \uFEFF\uFFFD"}""".replace("\\uFEFF", "\uFEFF").replace("\\uFFFD", "\uFFFD")
        val replies = ConcurrentLinkedQueue(listOf(
            200 to valid.toByteArray(),
            200 to ("{\"data\":\"".toByteArray() + byteArrayOf(-1) + "\"}".toByteArray()),
            401 to ("{\"error\":\"Unauthorized\",\"code\":401,\"message\":\"".toByteArray() + byteArrayOf(-1) + "\"}".toByteArray()),
        ))
        val executor = Executors.newSingleThreadExecutor()
        val server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
        server.executor = executor
        server.createContext("/luvia") { exchange ->
            exchange.use {
                it.requestBody.use { input -> input.readBytes() }
                val (status, body) = replies.poll() ?: (500 to byteArrayOf())
                it.responseHeaders.set("Content-Type", "application/json; charset=iso-8859-1")
                it.sendResponseHeaders(status, 0)
                it.responseBody.use { output ->
                    for (byte in body) { output.write(byte.toInt() and 255); output.flush() }
                }
            }
        }
        server.start()
        val client = OkHttpClient()
        try {
            var refreshed = 0
            val transport = OkHttpTransport("http://127.0.0.1:${server.address.port}/luvia", client = client,
                schema = schema, onTokenExpired = { refreshed++; "must-not-be-used" })
            assertEquals(JsonPrimitive("精度 café \uFEFF\uFFFD"), withTimeout(5000) { transport.call("echo") })
            for (status in listOf(200, 401)) {
                val error = assertFailsWith<LuxoError> { withTimeout(5000) { transport.call("echo") } }
                assertEquals("ParseError", error.error)
                assertEquals(status, error.code)
            }
            assertEquals(0, refreshed)
            assertTrue(replies.isEmpty())
        } finally {
            client.dispatcher.executorService.shutdownNow()
            client.connectionPool.evictAll()
            server.stop(0)
            executor.shutdownNow()
        }
    }

    @Test
    fun `malformed unauthorized responses never refresh or resend`() = runBlocking {
        for (mode in TransportMode.entries) {
            val bodies = if (mode == TransportMode.JSON) listOf(
                "{}".toByteArray(), "{".toByteArray(),
                """{"error":false,"code":401,"message":"bad"}""".toByteArray(),
            ) else listOf(byteArrayOf(0), byteArrayOf(1, -128), "{}".toByteArray())
            for (body in bodies) {
                var refreshed = 0
                withClient({ request, _ -> response(request, 401, body) }) { client, sent ->
                    val transport = transport(client, mode) { refreshed++; "must-not-be-used" }
                    val error = assertFailsWith<LuxoError> { transport.call("echo") }
                    assertEquals("ParseError", error.error)
                    assertEquals(401, error.code)
                    assertEquals(0, refreshed)
                    assertEquals(1, sent.size)
                }
            }
        }
    }

    @Test
    fun `refresh requires matching HTTP and envelope codes and happens once`() = runBlocking {
        for (mode in TransportMode.entries) {
            for ((status, code) in listOf(401 to 401, 403 to 401, 401 to 403, 401 to 0)) {
                var refreshed = 0
                withClient({ request, _ -> response(request, status, errorBody(mode, code)) }) { client, sent ->
                    val transport = transport(client, mode) { refreshed++; "renewed" }
                    val error = assertFailsWith<LuxoError> { transport.call("echo") }
                    assertEquals("Unauthorized", error.error)
                    assertEquals(code, error.code)
                    val eligible = status == 401 && code == 401
                    assertEquals(if (eligible) 1 else 0, refreshed)
                    assertEquals(if (eligible) 2 else 1, sent.size)
                }
            }
        }
    }

    @Test
    fun `refresh decline and exceptions preserve the original failure`() = runBlocking {
        for (mode in TransportMode.entries) {
            for (throws in listOf(false, true)) {
                val failure = IllegalStateException("refresh failed")
                var refreshed = 0
                withClient({ request, _ -> response(request, 401, errorBody(mode, 401)) }) { client, sent ->
                    val transport = transport(client, mode) {
                        refreshed++
                        if (throws) throw failure else null
                    }
                    if (throws) assertSame(failure, assertFailsWith<IllegalStateException> { transport.call("echo") })
                    else assertEquals("Unauthorized", assertFailsWith<LuxoError> { transport.call("echo") }.error)
                    assertEquals(1, refreshed)
                    assertEquals(1, sent.size)
                }
            }
        }
    }

    @Test
    fun `retry preserves the original mode and updates authorization`() = runBlocking {
        for (mode in TransportMode.entries) {
            val success = if (mode == TransportMode.JSON) """{"data":true}""".toByteArray() else byteArrayOf(1)
            withClient({ request, count ->
                response(request, if (count == 1) 401 else 200, if (count == 1) errorBody(mode, 401) else success)
            }) { client, sent ->
                lateinit var transport: OkHttpTransport
                transport = transport(client, mode) {
                    transport.setMode(if (mode == TransportMode.JSON) TransportMode.BINARY else TransportMode.JSON)
                    "renewed"
                }
                val result = transport.call("echo")
                if (mode == TransportMode.JSON) assertEquals(JsonPrimitive(true), result)
                else assertContentEquals(success, result as ByteArray)
                assertEquals(2, sent.size)
                assertEquals(requestBody(sent.first()), requestBody(sent.last()))
                assertEquals(sent.first().header("Content-Type"), sent.last().header("Content-Type"))
                assertEquals("Bearer renewed", sent.last().header("Authorization"))
            }
        }
    }

    @Test
    fun `malformed second responses never cause a third attempt`() = runBlocking {
        for (mode in TransportMode.entries) {
            var refreshed = 0
            withClient({ request, count ->
                response(request, 401, if (count == 1) errorBody(mode, 401) else byteArrayOf(0))
            }) { client, sent ->
                val transport = transport(client, mode) { refreshed++; "renewed" }
                assertEquals("ParseError", assertFailsWith<LuxoError> { transport.call("echo") }.error)
                assertEquals(1, refreshed)
                assertEquals(2, sent.size)
            }
        }
    }

    @Test
    fun `non-success JSON data envelopes are not returned as successful data`() = runBlocking {
        for (status in listOf(201, 400, 401, 500)) {
            withClient({ request, _ -> response(request, status, """{"data":42}""".toByteArray()) }) { client, _ ->
                val error = assertFailsWith<LuxoError> { transport(client, TransportMode.JSON).call("echo") }
                assertEquals("ParseError", error.error)
                assertEquals(status, error.code)
            }
        }
    }

    @Test
    fun `only HTTP 200 carries a successful Binary response`() = runBlocking {
        for (status in listOf(201, 202, 204, 299)) {
            withClient({ request, _ -> response(request, status, errorBody(TransportMode.BINARY, 403)) }) { client, _ ->
                val error = assertFailsWith<LuxoError> { transport(client, TransportMode.BINARY).call("echo") }
                assertEquals("Unauthorized", error.error)
                assertEquals(403, error.code)
            }
        }
    }

    @Test
    fun `protocol headers cannot be overridden by custom header casing`() = runBlocking {
        val headers = linkedMapOf("content-type" to "text/plain", "X-LUXO-MODE" to "wrong",
            "authorization" to "Bearer custom", "X-Trace" to "trace")
        withClient({ request, _ -> response(request, 200, """{"data":true}""".toByteArray()) }) { client, sent ->
            val transport = OkHttpTransport(endpoint, headers = headers, client = client, schema = schema)
            headers["X-Trace"] = "changed after construction"
            for (mode in TransportMode.entries) {
                transport.setMode(mode)
                transport.call("echo")
                val request = sent.last()
                assertEquals(if (mode == TransportMode.JSON) "json" else "binary", request.header("X-Luxo-Mode"))
                assertEquals(request.body!!.contentType().toString(), request.header("Content-Type"))
                assertEquals(1, request.headers.values("Content-Type").size)
                assertEquals("trace", request.header("X-Trace"))
                assertEquals("Bearer custom", request.header("Authorization"))
            }
            transport.setToken("changed")
            for (mode in TransportMode.entries) {
                transport.setMode(mode)
                transport.call("echo")
                assertEquals(listOf("Bearer changed"), sent.last().headers.values("Authorization"))
            }
        }
    }

    @Test
    fun `explicit token overrides a custom authorization header in both modes`() = runBlocking {
        withClient({ request, _ -> response(request, 200, """{"data":true}""".toByteArray()) }) { client, sent ->
            val transport = OkHttpTransport(endpoint, token = "explicit", headers = mapOf("AUTHORIZATION" to "custom"),
                client = client, schema = schema)
            for (mode in TransportMode.entries) {
                transport.setMode(mode)
                transport.call("echo")
                assertEquals(listOf("Bearer explicit"), sent.last().headers.values("Authorization"))
            }
        }
    }

    @Test
    fun `response body failures complete the caller and never refresh`() = runBlocking {
        for (mode in TransportMode.entries) {
            var refreshed = 0
            var closed = false
            val body = object : ResponseBody() {
                override fun contentType() = "application/json".toMediaType()
                override fun contentLength() = -1L
                private val source = object : ForwardingSource(Buffer()) {
                    override fun read(sink: Buffer, byteCount: Long): Long = throw IOException("body read failed")
                    override fun close() { closed = true; super.close() }
                }.buffer()
                override fun source(): BufferedSource = source
            }
            withClient({ request, _ -> response(request, 401, byteArrayOf()).newBuilder().body(body).build() }) { client, sent ->
                val transport = transport(client, mode) { refreshed++; "must-not-be-used" }
                val error = assertFailsWith<LuxoError> { withTimeout(2000) { transport.call("echo") } }
                assertEquals("NetworkError", error.error)
                assertEquals(0, refreshed)
                assertEquals(1, sent.size)
                assertTrue(closed)
            }
        }
    }

    private fun transport(client: OkHttpClient, mode: TransportMode, refresh: (suspend () -> String?)? = null) =
        OkHttpTransport(endpoint, client = client, mode = mode, schema = schema, onTokenExpired = refresh)

    private fun errorBody(mode: TransportMode, code: Int): ByteArray {
        if (mode == TransportMode.JSON) return """{"error":"Unauthorized","code":$code,"message":"expired"}""".toByteArray()
        return LuxoEncoder().apply {
            writeFieldInt(1, code.toLong())
            writeFieldString(2, "Unauthorized")
            writeFieldString(3, "expired")
            writeEnd()
        }.bytes()
    }

    private fun response(request: Request, code: Int, body: ByteArray, contentType: String = "application/json"): Response =
        Response.Builder().request(request).protocol(Protocol.HTTP_1_1).code(code).message("fixture")
            .body(body.toResponseBody(contentType.toMediaType())).build()

    private fun requestBody(request: Request): String = Buffer().apply { request.body!!.writeTo(this) }.readByteString().hex()

    private suspend fun withClient(
        respond: (Request, Int) -> Response,
        block: suspend (OkHttpClient, ConcurrentLinkedQueue<Request>) -> Unit,
    ) {
        val sent = ConcurrentLinkedQueue<Request>()
        val client = OkHttpClient.Builder().addInterceptor { chain ->
            sent.add(chain.request())
            respond(chain.request(), sent.size)
        }.build()
        try { block(client, sent) } finally {
            client.dispatcher.executorService.shutdownNow()
            client.connectionPool.evictAll()
        }
    }
}
