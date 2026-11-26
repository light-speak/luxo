package com.luxo.client

import java.io.IOException
import java.net.InetSocketAddress
import java.util.concurrent.Executors
import java.util.concurrent.ConcurrentLinkedQueue
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicInteger
import com.sun.net.httpserver.HttpServer
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.cancel
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.withContext
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
import kotlin.coroutines.CoroutineContext
import kotlin.test.*

class HttpProtocolTest {
    @Test
    fun `binary HTTP requires body and preserves null marker`() = runBlocking {
        var body = byteArrayOf()
        var refreshed = 0
        withClient({ request, _ -> response(request, 200, body) }) { client, _ ->
            val transport = transport(client, TransportMode.BINARY) { refreshed++; "unused" }
            val request = object : GeneratedRequest {
                override val apiID = 1
                override fun writeParameters(encoder: LuxoEncoder) {}
                override fun toJSONParameters(): Map<String, Any?> = error("Binary used JSON parameters")
            }
            for (generated in listOf(false, true)) {
                suspend fun call() = if (generated) transport.call("echo", request) else transport.call("echo")
                body = byteArrayOf()
                val failure = assertFailsWith<LuxoError> { call() }
                assertEquals("ParseError", failure.error)
                assertEquals(200, failure.code)
                body = byteArrayOf(0)
                assertContentEquals(body, assertIs<ByteArray>(call()))
            }
            assertEquals(0, refreshed)
        }
    }

    @OptIn(kotlinx.serialization.ExperimentalSerializationApi::class)
    @Test
    fun `raw JSON sibling injection is rejected before any HTTP send`() = runBlocking {
        for (mode in TransportMode.entries) withClient({ _, _ -> error("invalid input reached HTTP") }) { client, sent ->
            val transport = OkHttpTransport(endpoint, client = client, mode = mode,
                schema = mapOf("echo" to APISchemaEntry(1, listOf(ParamSchema(1, "value", "JSON")))))
            for (token in listOf("0,1", "0,\"extra\":true", "")) {
                val value = kotlinx.serialization.json.JsonArray(listOf(kotlinx.serialization.json.JsonUnquotedLiteral(token)))
                assertFails { transport.call("echo", mapOf("value" to value)) }
                assertTrue(sent.isEmpty(), "$mode accepted $token")
            }
        }
    }

    @Test
    fun `invalid JSON tokens cannot become success or trigger token renewal`() = runBlocking {
        for (status in listOf(200, 401)) for (token in listOf("NaN", "01", "1e+", "[1,]", "\"\\q\"")) {
            var refreshes = 0
            val body = if (status == 200) """{"data":$token,"data":true}"""
                else """{"error":"Unauthorized","code":401,"data":$token,"data":null}"""
            withClient({ request, count -> response(request, if (count == 1) status else 200,
                (if (count == 1) body else """{"data":true}""").toByteArray()) }) { client, sent ->
                val transport = transport(client, TransportMode.JSON) { refreshes++; "must-not-refresh" }
                val error = assertFailsWith<LuxoError> { transport.call("echo") }
                assertEquals("ParseError", error.error, token)
                assertEquals(0, refreshes, token)
                assertEquals(1, sent.size, token)
                assertEquals(JsonPrimitive(true), transport.call("echo"))
                assertEquals(2, sent.size)
            }
        }
    }

    @Test
    fun `late unauthorized response does not refresh a cancelled caller`() = runBlocking {
        for (mode in TransportMode.entries) {
            val caller = java.util.concurrent.atomic.AtomicReference<kotlinx.coroutines.Job>()
            val refreshes = java.util.concurrent.atomic.AtomicInteger()
            withClient({ request, count ->
                if (count == 1) {
                    caller.get().cancel()
                    response(request, 401, errorBody(mode, 401))
                } else response(request, 200,
                    if (mode == TransportMode.JSON) "{\"data\":true}".toByteArray() else byteArrayOf(1))
            }) { client, sent ->
                val transport = OkHttpTransport(endpoint, client = client, mode = mode,
                    schema = mapOf("echo" to APISchemaEntry(1)),
                    onTokenExpired = { refreshes.incrementAndGet(); "must-not-install" })
                val pending = async {
                    caller.set(currentCoroutineContext()[kotlinx.coroutines.Job]!!)
                    transport.call("echo")
                }
                assertFailsWith<CancellationException> { pending.await() }
                withTimeout(2000) {
                    while (client.dispatcher.runningCallsCount() != 0) kotlinx.coroutines.delay(1)
                }
                assertEquals(0, refreshes.get(), "cancelled response invoked refresh: $mode")
                assertEquals(1, sent.size)
                transport.call("echo")
                assertEquals(2, sent.size)
            }
        }
    }

    @Test
    fun `cancellation during generated encoding never reaches the HTTP server`() = runBlocking {
        val received = java.util.concurrent.atomic.AtomicInteger()
        val server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
        server.createContext("/api") { exchange -> exchange.use {
            received.incrementAndGet()
            it.requestBody.use { input -> input.readBytes() }
            val body = if (it.requestHeaders.getFirst("Content-Type").startsWith("application/json"))
                "{\"data\":true}".toByteArray() else byteArrayOf(1)
            it.sendResponseHeaders(200, body.size.toLong())
            it.responseBody.use { output -> output.write(body) }
        } }
        val client = OkHttpClient()
        server.start()
        try {
            for (mode in TransportMode.entries) {
                received.set(0)
                val transport = OkHttpTransport("http://127.0.0.1:${server.address.port}/api",
                    client = client, mode = mode, schema = mapOf("echo" to APISchemaEntry(1)))
                val pending = async {
                    val context = currentCoroutineContext()
                    val request = object : GeneratedRequest {
                        override val apiID = 1
                        override fun writeParameters(encoder: LuxoEncoder) { context.cancel() }
                        override fun toJSONParameters(): Map<String, Any?> { context.cancel(); return emptyMap() }
                    }
                    transport.call("echo", request)
                }
                assertFailsWith<CancellationException> { pending.await() }
                withTimeout(2000) {
                    while (client.dispatcher.runningCallsCount() != 0) kotlinx.coroutines.delay(1)
                }
                assertEquals(0, received.get(), "cancelled request reached server: $mode")
                transport.call("echo")
                assertEquals(1, received.get(), "cancellation broke following request: $mode")
            }
        } finally {
            server.stop(0)
            client.dispatcher.executorService.shutdownNow()
            client.connectionPool.evictAll()
        }
    }

    @Test
    fun `redirect policy preserves injected client resources and configuration`() {
        val interceptor = okhttp3.Interceptor { chain -> chain.proceed(chain.request()) }
        val original = OkHttpClient.Builder().addInterceptor(interceptor)
            .callTimeout(1234, java.util.concurrent.TimeUnit.MILLISECONDS).build()
        val direct = directHTTPClient(original)
        assertNotSame(original, direct)
        assertTrue(original.followRedirects)
        assertTrue(original.followSslRedirects)
        assertFalse(direct.followRedirects)
        assertFalse(direct.followSslRedirects)
        assertSame(original.connectionPool, direct.connectionPool)
        assertSame(original.dispatcher, direct.dispatcher)
        assertSame(interceptor, direct.interceptors.single())
        assertEquals(original.callTimeoutMillis, direct.callTimeoutMillis)
        assertSame(direct, directHTTPClient(direct))
        val defaults = defaultHTTPClient(5, 2)
        assertFalse(defaults.followRedirects)
        assertFalse(defaults.followSslRedirects)
        assertSame(defaults, directHTTPClient(defaults))
    }

    @Test
    fun `business redirects never forward credentials or bodies`() = runBlocking {
        val targets = java.util.concurrent.atomic.AtomicInteger()
        val status = java.util.concurrent.atomic.AtomicInteger(303)
        val location = java.util.concurrent.atomic.AtomicReference("")
        val expireNext = java.util.concurrent.atomic.AtomicBoolean()
        val sent = ConcurrentLinkedQueue<Pair<String?, ByteArray>>()
        val source = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
        val target = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
        val capture = com.sun.net.httpserver.HttpHandler { exchange -> exchange.use {
            targets.incrementAndGet()
            it.requestBody.use { input -> input.readBytes() }
            val body = "{\"data\":true}".toByteArray()
            it.sendResponseHeaders(200, body.size.toLong())
            it.responseBody.use { output -> output.write(body) }
        } }
        source.createContext("/capture", capture); target.createContext("/capture", capture)
        source.createContext("/api") { exchange -> exchange.use {
            val body = it.requestBody.use { input -> input.readBytes() }
            sent.add(it.requestHeaders.getFirst("Authorization") to body)
            if (expireNext.getAndSet(false)) {
                val mode = if (it.requestHeaders.getFirst("Content-Type").startsWith("application/json")) TransportMode.JSON else TransportMode.BINARY
                val error = errorBody(mode, 401)
                it.sendResponseHeaders(401, error.size.toLong())
                it.responseBody.use { output -> output.write(error) }
            } else {
                it.responseHeaders.set("Location", location.get())
                it.sendResponseHeaders(status.get(), -1)
            }
        } }
        source.start(); target.start()
        val clients = listOf(OkHttpClient(), defaultHTTPClient(5, 2))
        try {
            for (client in clients) for (mode in TransportMode.entries) {
                var refreshes = 0
                val transport = OkHttpTransport("http://127.0.0.1:${source.address.port}/api", client = client,
                    mode = mode, schema = mapOf("echo" to APISchemaEntry(1, listOf(ParamSchema(1, "value", "String")))),
                    headers = mapOf("X-Project-Key" to "test-only-key"), onTokenExpired = { refreshes++; "unused" })
                for (refresh in listOf(false, true)) for (code in listOf(303, 301, 302, 307, 308)) {
                    status.set(code)
                    for (destination in listOf("/capture", "http://127.0.0.1:${target.address.port}/capture")) {
                        location.set(destination)
                        expireNext.set(refresh)
                        sent.clear()
                        val previousRefreshes = refreshes
                        val error = assertFailsWith<LuxoError> { withTimeout(5000) { transport.call("echo", mapOf("value" to "private-input")) } }
                        assertEquals(code, error.code)
                        assertEquals(0, targets.get())
                        assertEquals(previousRefreshes + if (refresh) 1 else 0, refreshes)
                        assertEquals(if (refresh) 2 else 1, sent.size)
                        if (refresh) {
                            val calls = sent.toList()
                            assertContentEquals(calls[0].second, calls[1].second)
                            assertEquals("Bearer unused", calls[1].first)
                        }
                    }
                }
            }
        } finally {
            source.stop(0); target.stop(0)
            for (client in clients) { client.dispatcher.executorService.shutdownNow(); client.connectionPool.evictAll() }
        }
    }

    @Test
    fun `refresh preserves encoded body across parameter and schema changes`() = runBlocking {
        for (mode in TransportMode.entries) for (generated in listOf(false, true)) {
            val params = mutableMapOf<String, Any?>("count" to 42L)
            var encodes = 0
            val request = object : GeneratedRequest {
                override val apiID = 1
                override fun writeParameters(encoder: LuxoEncoder) { encodes++; encoder.writeFieldInt(1, params.getValue("count") as Long) }
                override fun toJSONParameters(): Map<String, Any?> { encodes++; return params }
            }
            val bodies = mutableListOf<String>()
            withClient({ sent, count ->
                bodies.add(requestBody(sent))
                if (count == 1) response(sent, 401, errorBody(mode, 401))
                else response(sent, 200, if (mode == TransportMode.JSON) "{\"data\":42}".toByteArray() else byteArrayOf(84))
            }) { client, sent ->
                lateinit var transport: OkHttpTransport
                transport = OkHttpTransport(endpoint, client = client, mode = mode, onTokenExpired = {
                    params["count"] = 99L
                    transport.setSchema(mapOf("echo" to APISchemaEntry(if (generated) 1 else 2,
                        listOf(ParamSchema(if (generated) 1 else 3, "count", "Int")))))
                    "renewed"
                })
                transport.setSchema(mapOf("echo" to APISchemaEntry(1, listOf(ParamSchema(1, "count", "Int")))))
                if (generated) transport.call("echo", request) else transport.call("echo", params)
                assertEquals(2, bodies.size)
                assertEquals(bodies[0], bodies[1], "retry changed request: $mode generated=$generated")
                assertSame(sent.first().body, sent.last().body)
                if (generated) assertEquals(1, encodes)
                if (generated) transport.call("echo", request) else transport.call("echo", params)
                assertNotEquals(bodies[0], bodies[2])
            }
        }
    }

    @Test
    fun `generated retry shares deadline and cancels its underlying call`() = runBlocking {
        for (mode in TransportMode.entries) {
            val retryCancelled = CompletableDeferred<Unit>()
            var calls = 0
            val client = OkHttpClient.Builder().addInterceptor { chain ->
                if (++calls == 1) response(chain.request(), 401, errorBody(mode, 401))
                else {
                    while (!chain.call().isCanceled()) Thread.sleep(5)
                    retryCancelled.complete(Unit)
                    throw IOException("cancelled retry")
                }
            }.build()
            try {
                val transport = OkHttpTransport(endpoint, client = client, mode = mode, schema = schema,
                    timeoutSeconds = 1, onTokenExpired = { kotlinx.coroutines.delay(600); "renewed" })
                val request = object : GeneratedRequest {
                    override val apiID = schema.getValue("echo").id
                    override fun writeParameters(encoder: LuxoEncoder) {}
                    override fun toJSONParameters(): Map<String, Any?> = emptyMap()
                }
                val failure = withTimeout(1400) { runCatching { transport.call("echo", request) }.exceptionOrNull() }
                assertIs<LuxoError>(failure)
                assertEquals("TimeoutError", failure.error)
                withTimeout(1000) { retryCancelled.await() }
                assertEquals(2, calls)
            } finally {
                client.dispatcher.cancelAll()
                client.dispatcher.executorService.shutdownNow()
                client.connectionPool.evictAll()
            }
        }
    }

    @Test
    fun `caller deadline remains coroutine cancellation during refresh`() = runBlocking {
        withClient({ request, _ -> response(request, 401, errorBody(TransportMode.JSON, 401)) }) { client, _ ->
            val transport = transport(client, TransportMode.JSON) { awaitCancellation() }
            assertFailsWith<kotlinx.coroutines.TimeoutCancellationException> {
                withTimeout(100) { transport.call("echo") }
            }
        }
    }

    @Test
    fun `request deadline cancels a pending refresh without replacing credentials`() = runBlocking {
        for (mode in TransportMode.entries) {
            val cancelled = CompletableDeferred<Unit>()
            withClient({ request, count ->
                if (count == 1) response(request, 401, errorBody(mode, 401))
                else response(request, 200, if (mode == TransportMode.JSON) "{\"data\":42}".toByteArray() else byteArrayOf(84))
            }) { client, sent ->
                val transport = OkHttpTransport(endpoint, token = "original", client = client,
                    mode = mode, schema = schema, timeoutSeconds = 1, onTokenExpired = {
                        try { awaitCancellation() } finally { cancelled.complete(Unit) }
                    })
                val failure = withTimeout(3000) { runCatching { transport.call("echo") }.exceptionOrNull() }
                assertIs<LuxoError>(failure)
                assertEquals("TimeoutError", failure.error)
                assertTrue(cancelled.isCompleted)
                assertEquals(1, sent.size)
                transport.call("echo")
                assertEquals("Bearer original", sent.last().header("authorization"))
            }
        }
    }

    @Test
    fun `non-object envelopes never refresh and do not poison later calls`() = runBlocking {
        for (status in listOf(200, 401, 500)) {
            for (body in listOf("[]", "[{\"data\":true}]", "null", "true", "42", "9223372036854775807", "1.234567890123456789", "\"data\"", "{}")) {
                var refreshes = 0
                withClient({ request, count ->
                    if (count == 1) response(request, status, body.toByteArray())
                    else response(request, 200, "{\"data\":null}".toByteArray())
                }) { client, sent ->
                    val transport = transport(client, TransportMode.JSON) { refreshes++; "must-not-refresh" }
                    val error = assertFailsWith<LuxoError> { transport.call("echo") }
                    assertEquals("ParseError", error.error)
                    assertEquals(status, error.code)
                    assertEquals(0, refreshes)
                    assertEquals(1, sent.size)
                    assertEquals(kotlinx.serialization.json.JsonNull, transport.call("echo"))
                    assertEquals(2, sent.size)
                }
            }
        }
    }
    @Test
    fun `stale unauthorized response cannot start refresh for a replacement identity`() = runBlocking {
        for (mode in TransportMode.entries) {
            val entered = CompletableDeferred<Unit>()
            val release = CompletableDeferred<Unit>()
            var refreshes = 0
            withClient({ request, _ ->
                entered.complete(Unit)
                runBlocking { release.await() }
                response(request, 401, errorBody(mode, 401))
            }) { client, sent ->
                val transport = transport(client, mode) { refreshes++; "old-user-refreshed" }
                transport.setToken("original-token")
                val result = async { runCatching { transport.call("echo") } }
                try {
                    withTimeout(3000) { entered.await() }
                    transport.setToken("different-user")
                } finally {
                    release.complete(Unit)
                }
                assertEquals(401, (result.await().exceptionOrNull() as LuxoError).code)
                assertEquals(0, refreshes)
                assertEquals(1, sent.size)
            }
        }
    }

    @Test
    fun `late refresh cannot overwrite explicitly replaced credentials`() = runBlocking {
        for (mode in TransportMode.entries) {
            for (replacement in listOf("different-user", "original-token")) {
                val entered = CompletableDeferred<Unit>()
                val refresh = CompletableDeferred<String?>()
                withClient({ request, attempt ->
                    if (attempt == 1) response(request, 401, errorBody(mode, 401))
                    else response(request, 200, if (mode == TransportMode.JSON) """{"data":true}""".toByteArray() else byteArrayOf(1))
                }) { client, sent ->
                    val transport = transport(client, mode) { entered.complete(Unit); refresh.await() }
                    transport.setToken("original-token")
                    val result = async { runCatching { transport.call("echo") } }
                    entered.await()
                    transport.setToken(replacement)
                    refresh.complete("old-user-refreshed")
                    val outcome = result.await()
                    val callsBefore = sent.size
                    transport.call("echo")
                    assertEquals("Bearer $replacement", sent.last().header("Authorization"))
                    assertEquals(1, callsBefore)
                    assertEquals(401, (outcome.exceptionOrNull() as LuxoError).code)
                }
            }
        }
    }

    @Test
    fun `HTTP status and error presence override JSON data`() = runBlocking {
        for ((status, body, name) in listOf(
            Triple(200, """{"data":true,"error":null,"code":401,"message":"bad"}""", "ParseError"),
            Triple(200, """{"data":true,"error":false,"code":401,"message":"bad"}""", "ParseError"),
            Triple(200, """{"data":true,"error":"Unauthorized","code":401,"message":"bad"}""", "Unauthorized"),
            Triple(201, """{"data":true}""", "ParseError"),
            Triple(403, """{"data":true}""", "ParseError"),
            Triple(500, """{"data":true}""", "ParseError"),
        )) {
            var refreshed = 0
            withClient({ request, attempt ->
                if (attempt == 1) response(request, status, body.toByteArray())
                else response(request, 200, """{"data":true}""".toByteArray())
            }) { client, sent ->
                val transport = transport(client, TransportMode.JSON) { refreshed++; "unused" }
                val error = assertFailsWith<LuxoError> { transport.call("echo") }
                assertEquals(name, error.error)
                assertEquals(if (name == "Unauthorized") 401 else status, error.code)
                assertEquals(1, sent.size)
                assertEquals(0, refreshed)
                assertEquals(JsonPrimitive(true), transport.call("echo"))
                assertEquals(2, sent.size)
            }
        }
    }

    @Test
    fun `parameters cannot replace the selected HTTP API`() = runBlocking {
        withClient({ request, _ -> response(request, 200, "{\"data\":true}".toByteArray()) }) { client, sent ->
            val transport = OkHttpTransport(endpoint, client = client, schema = schema)
            transport.call("echo", mapOf("\$api" to "other"))
            val buffer = Buffer()
            sent.single().body!!.writeTo(buffer)
            assertTrue(buffer.readUtf8().contains("\"\$api\":\"echo\""))
        }
    }

    @Test
    fun `concurrent first calls publish one immutable endpoint`() = runBlocking {
        withClient({ request, _ -> response(request, 200, byteArrayOf(1)) }) { client, sent ->
            val transport = OkHttpTransport(endpoint, client = client, mode = TransportMode.BINARY, schema = schema,
                maxConcurrentRequests = 32)
            List(32) { async(Dispatchers.Default) { transport.call("echo") } }.awaitAll()
            assertEquals(32, sent.size)
            val first = sent.first().url
            sent.forEach { assertSame(first, it.url) }
        }
    }

    @Test
    fun `calls reuse the parsed endpoint across modes`() = runBlocking {
        withClient({ request, _ ->
            response(request, 200, if (request.header("X-Luxo-Mode") == "binary") byteArrayOf(1) else "{\"data\":1}".toByteArray())
        }) { client, sent ->
            val endpoint = "https://example.test:8443/api%20v1"
            val transport = OkHttpTransport(endpoint, client = client, schema = schema)
            transport.call("echo")
            transport.setMode(TransportMode.BINARY)
            transport.call("echo")
            assertEquals(2, sent.size)
            val requests = sent.toList()
            assertEquals(endpoint, requests[0].url.toString())
            assertSame(requests[0].url, requests[1].url)
        }
    }

    @Test
    fun `invalid endpoint remains a call time error`() = runBlocking {
        val transport = OkHttpTransport("http://[invalid", schema = schema)
        for (mode in TransportMode.entries) {
            transport.setMode(mode)
            assertFailsWith<IllegalArgumentException> { transport.call("echo") }
        }
    }

    @Test
    fun `success requires data while explicit null remains valid`() = runBlocking {
        var body = "{}"
        var refreshed = 0
        withClient({ request, _ -> response(request, 200, body.toByteArray()) }) { client, sent ->
            val transport = transport(client, TransportMode.JSON) { refreshed++; "unused" }
            for (missing in listOf("{}", """{"ok":true}""", """{"value":42}""")) {
                body = missing
                val error = assertFailsWith<LuxoError> { transport.call("echo") }
                assertEquals("ParseError", error.error)
                assertEquals(200, error.code)
            }
            for (value in listOf("null", "false", "0", "\"\"", "[]", "{}")) {
                body = """{"data":$value}"""
                assertEquals(decodeLuxoJSON(value), transport.call("echo"))
            }
            assertEquals(0, refreshed)
            assertEquals(9, sent.size)
        }
    }

    private val endpoint = "http://localhost/luvia"
    private val schema = mapOf("echo" to APISchemaEntry(1))

    @Test
    fun `refresh failure preserves credentials and does not retry`() = runBlocking {
        for (mode in TransportMode.entries) {
            for (throws in listOf(false, true)) {
                val callbackError = IllegalStateException("refresh unavailable")
                var refreshed = 0
                withClient({ request, attempt ->
                    if (attempt == 1) response(request, 401, errorBody(mode, 401))
                    else response(request, 200, if (mode == TransportMode.JSON) "{\"data\":42}".toByteArray() else byteArrayOf(84))
                }) { client, sent ->
                    val transport = OkHttpTransport(endpoint, token = "original", client = client,
                        mode = mode, schema = schema, onTokenExpired = {
                            refreshed++
                            if (throws) throw callbackError
                            null
                        })
                    if (throws) {
                        assertSame(callbackError, assertFailsWith<IllegalStateException> { transport.call("echo") })
                    } else {
                        val error = assertFailsWith<LuxoError> { transport.call("echo") }
                        assertEquals(401, error.code)
                        assertEquals("Unauthorized", error.error)
                    }
                    assertEquals(1, sent.size)
                    transport.call("echo")
                    assertEquals(2, sent.size)
                    assertEquals("Bearer original", sent.last().header("Authorization"))
                    assertEquals(1, refreshed)
                }
            }
        }
    }

    @Test
    fun `cache overload preserves identity without automatic retry or token refresh`() = runBlocking {
        for (mode in TransportMode.entries) {
            var refreshed = 0
            val success = if (mode == TransportMode.JSON) """{"data":42}""".toByteArray() else byteArrayOf(84)
            withClient({ request, attempt ->
                if (attempt == 1) response(request, 503, errorBody(mode, 503, "CacheOriginBusy", "Cache origin capacity exhausted"))
                else response(request, 200, success)
            }) { client, sent ->
                val transport = transport(client, mode) { refreshed++; "must-not-refresh" }
                val error = assertFailsWith<LuxoError> { transport.call("echo") }
                assertEquals("CacheOriginBusy", error.error)
                assertEquals(503, error.code)
                assertEquals("Cache origin capacity exhausted", error.message)
                assertEquals(1, sent.size)
                assertEquals(0, refreshed)
                val next = transport.call("echo")
                if (mode == TransportMode.JSON) assertEquals(JsonPrimitive(42), next)
                else assertContentEquals(byteArrayOf(84), next as ByteArray)
                assertEquals(2, sent.size)
            }
        }
    }

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
                    assertEquals(if (code == 0) "ParseError" else "Unauthorized", error.error)
                    assertEquals(if (code == 0) status else code, error.code)
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

    @Test
    fun `three concurrent and late unauthorized responses share one refresh`() = runBlocking {
        val evidence = mutableListOf<String>()
        for (mode in TransportMode.entries) for (lateUnauthorized in listOf(false, true)) {
            val initial = AtomicInteger()
            val firstTwoSent = CompletableDeferred<Unit>()
            val allSent = CompletableDeferred<Unit>()
            val refreshes = AtomicInteger()
            val refreshStarted = CompletableDeferred<Unit>()
            val refresh = CompletableDeferred<String>()
            val releaseInitial = CountDownLatch(1)
            val releaseLate = CountDownLatch(if (lateUnauthorized) 1 else 0)
            val success = if (mode == TransportMode.JSON) """{"data":true}""".toByteArray() else byteArrayOf(1)
            withClient({ request, _ ->
                if (request.header("Authorization") == "Bearer expired") {
                    val index = initial.incrementAndGet()
                    if (index == 2) firstTwoSent.complete(Unit)
                    if (index == 3) allSent.complete(Unit)
                    check(releaseInitial.await(3, TimeUnit.SECONDS))
                    if (index == 3) check(releaseLate.await(3, TimeUnit.SECONDS))
                    response(request, 401, errorBody(mode, 401))
                } else {
                    response(request, 200, success)
                }
            }) { client, sent ->
                val transport = transport(client, mode) {
                    if (refreshes.incrementAndGet() == 1) refreshStarted.complete(Unit)
                    refresh.await()
                }
                transport.setToken("expired")
                withTimeout(5000) {
                    val boundaries = List(3) { HTTPResponseBoundary() }
                    fun start(boundary: HTTPResponseBoundary) =
                        async(boundary, start = CoroutineStart.UNDISPATCHED) {
                            runCatching { transport.call("echo") }
                        }
                    val firstTwo = boundaries.take(2).map(::start)
                    firstTwoSent.await()
                    val requests = firstTwo + start(boundaries.last())
                    try {
                        allSent.await()
                        releaseInitial.countDown()
                        val delivered = if (lateUnauthorized) boundaries.take(2) else boundaries
                        delivered.forEach { it.handled.await() }
                        refreshStarted.await()
                        refresh.complete("renewed")
                        if (lateUnauthorized) requests.first().await()
                    } finally {
                        releaseLate.countDown()
                    }
                    val outcomes = requests.awaitAll()
                    val successful = outcomes.count { it.isSuccess }
                    val unauthorized = outcomes.count { (it.exceptionOrNull() as? LuxoError)?.code == 401 }
                    evidence += "$mode late=$lateUnauthorized: callbacks=${refreshes.get()}, successful=$successful, unauthorized=$unauthorized, HTTP=${sent.size}"
                }
            }
        }
        assertEquals(
            TransportMode.entries.flatMap { mode ->
                listOf(false, true).map { "$mode late=$it: callbacks=1, successful=3, unauthorized=0, HTTP=6" }
            },
            evidence,
        )
    }

    @Test
    fun `canceling one request leaves its peer's refresh active`() = runBlocking {
        for (mode in TransportMode.entries) {
            val releaseInitial = CountDownLatch(1)
            val refreshStarted = CompletableDeferred<Unit>()
            val refreshFinished = CompletableDeferred<Unit>()
            val refresh = CompletableDeferred<String>()
            val refreshes = AtomicInteger()
            val success = if (mode == TransportMode.JSON) """{"data":true}""".toByteArray() else byteArrayOf(1)
            withClient({ request, _ ->
                if (request.header("Authorization") == "Bearer expired") {
                    check(releaseInitial.await(3, TimeUnit.SECONDS))
                    response(request, 401, errorBody(mode, 401))
                } else {
                    response(request, 200, success)
                }
            }) { client, sent ->
                val transport = transport(client, mode) {
                    refreshes.incrementAndGet()
                    refreshStarted.complete(Unit)
                    try { refresh.await() } finally { refreshFinished.complete(Unit) }
                }
                transport.setToken("expired")
                withTimeout(5000) {
                    val boundaries = List(2) { HTTPResponseBoundary() }
                    val calls = boundaries.map { boundary ->
                        async(boundary, start = CoroutineStart.UNDISPATCHED) { transport.call("echo") }
                    }
                    releaseInitial.countDown()
                    boundaries.forEach { it.handled.await() }
                    refreshStarted.await()
                    calls.first().cancelAndJoin()
                    assertFalse(refreshFinished.isCompleted)
                    refresh.complete("renewed")
                    calls.last().await()
                    assertEquals(1, refreshes.get())
                    assertEquals(listOf("Bearer expired", "Bearer expired", "Bearer renewed"),
                        sent.map { it.header("Authorization") })
                }
            }
        }
    }

    @Test
    fun `late unauthorized response can refresh after its sole peer cancelled`() = runBlocking {
        for (mode in TransportMode.entries) {
            val received = AtomicInteger()
            val lateSent = CompletableDeferred<Unit>()
            val releaseLate = CountDownLatch(1)
            val refreshStarted = CompletableDeferred<Unit>()
            val refreshStopped = CompletableDeferred<Unit>()
            val refreshes = AtomicInteger()
            val success = if (mode == TransportMode.JSON) """{"data":true}""".toByteArray() else byteArrayOf(1)
            withClient({ request, _ ->
                when (received.incrementAndGet()) {
                    1 -> {
                        lateSent.complete(Unit)
                        check(releaseLate.await(3, TimeUnit.SECONDS))
                        response(request, 401, errorBody(mode, 401))
                    }
                    2 -> response(request, 401, errorBody(mode, 401))
                    else -> response(request, 200, success)
                }
            }) { client, sent ->
                val transport = transport(client, mode) {
                    if (refreshes.incrementAndGet() > 1) "renewed" else {
                        refreshStarted.complete(Unit)
                        try { awaitCancellation() } finally { refreshStopped.complete(Unit) }
                    }
                }
                transport.setToken("expired")
                withTimeout(5000) {
                    val late = async { transport.call("echo") }
                    try {
                        lateSent.await()
                        val cancelled = async { transport.call("echo") }
                        refreshStarted.await()
                        cancelled.cancelAndJoin()
                        assertTrue(refreshStopped.isCompleted)
                    } finally {
                        releaseLate.countDown()
                    }
                    late.await()
                    assertEquals(2, refreshes.get())
                    assertEquals(
                        listOf("Bearer expired", "Bearer expired", "Bearer renewed"),
                        sent.map { it.header("Authorization") },
                    )
                }
            }
        }
    }

    @Test
    fun `explicit token replacement invalidates all shared refresh waiters`() = runBlocking {
        for (mode in TransportMode.entries) for (replacement in listOf("different", "expired")) {
            val releaseInitial = CountDownLatch(1)
            val refreshStarted = CompletableDeferred<Unit>()
            val cleanupEntered = CompletableDeferred<Unit>()
            val allowCleanup = CompletableDeferred<Unit>()
            val refreshStopped = CompletableDeferred<Unit>()
            val refreshes = AtomicInteger()
            val success = if (mode == TransportMode.JSON) """{"data":true}""".toByteArray() else byteArrayOf(1)
            withClient({ request, _ ->
                if (request.header("Authorization") == "Bearer renewed") {
                    response(request, 200, success)
                } else {
                    check(releaseInitial.await(3, TimeUnit.SECONDS))
                    response(request, 401, errorBody(mode, 401))
                }
            }) { client, sent ->
                val transport = transport(client, mode) {
                    if (refreshes.incrementAndGet() == 1) {
                        refreshStarted.complete(Unit)
                        try {
                            awaitCancellation()
                        } finally {
                            withContext(NonCancellable) {
                                cleanupEntered.complete(Unit)
                                allowCleanup.await()
                                refreshStopped.complete(Unit)
                            }
                        }
                    }
                    "renewed"
                }
                transport.setToken("expired")
                withTimeout(5000) {
                    val boundaries = List(2) { HTTPResponseBoundary() }
                    val stale = boundaries.map { boundary ->
                        async(boundary, start = CoroutineStart.UNDISPATCHED) {
                            runCatching { transport.call("echo") }
                        }
                    }
                    releaseInitial.countDown()
                    boundaries.forEach { it.handled.await() }
                    refreshStarted.await()
                    transport.setToken(replacement)
                    try {
                        cleanupEntered.await()
                        for (result in stale.awaitAll()) {
                            assertEquals(401, assertIs<LuxoError>(result.exceptionOrNull()).code)
                        }
                        assertFalse(refreshStopped.isCompleted)
                    } finally {
                        allowCleanup.complete(Unit)
                    }
                    refreshStopped.await()
                    transport.call("echo")
                    assertEquals(2, refreshes.get())
                    assertEquals(
                        listOf("Bearer expired", "Bearer expired", "Bearer $replacement", "Bearer renewed"),
                        sent.map { it.header("Authorization") },
                    )
                }
            }
        }
    }

    @Test
    fun `late unauthorized responses share a completed refresh failure`() = runBlocking {
        for (mode in TransportMode.entries) for (throws in listOf(false, true)) {
            val initial = AtomicInteger()
            val firstSent = CompletableDeferred<Unit>()
            val bothSent = CompletableDeferred<Unit>()
            val releaseResponses = CountDownLatch(1)
            val releaseLate = CountDownLatch(1)
            val refreshStarted = CompletableDeferred<Unit>()
            val completeRefresh = CompletableDeferred<Result<String?>>()
            val failure = IllegalStateException("refresh failed")
            val refreshes = AtomicInteger()
            val success = if (mode == TransportMode.JSON) """{"data":true}""".toByteArray() else byteArrayOf(1)
            withClient({ request, _ ->
                if (request.header("Authorization") == "Bearer renewed") {
                    response(request, 200, success)
                } else {
                    val index = initial.incrementAndGet()
                    if (index == 1) firstSent.complete(Unit)
                    if (index == 2) bothSent.complete(Unit)
                    check(releaseResponses.await(3, TimeUnit.SECONDS))
                    if (index == 2) check(releaseLate.await(3, TimeUnit.SECONDS))
                    response(request, 401, errorBody(mode, 401))
                }
            }) { client, _ ->
                val transport = transport(client, mode) {
                    if (refreshes.incrementAndGet() == 1) {
                        refreshStarted.complete(Unit)
                        completeRefresh.await().getOrThrow()
                    } else "renewed"
                }
                transport.setToken("expired")
                withTimeout(5000) {
                    val first = async { runCatching { transport.call("echo") } }
                    firstSent.await()
                    val late = async { runCatching { transport.call("echo") } }
                    try {
                        // The second request must capture the original cohort before the failure.
                        bothSent.await()
                        releaseResponses.countDown()
                        refreshStarted.await()
                        completeRefresh.complete(if (throws) Result.failure(failure) else Result.success(null))
                        first.await()
                    } finally {
                        releaseLate.countDown()
                    }
                    for (result in awaitAll(first, late)) {
                        val error = result.exceptionOrNull()
                        if (throws) assertSame(failure, error)
                        else assertEquals(401, assertIs<LuxoError>(error).code)
                    }
                    assertEquals(1, refreshes.get())
                    transport.call("echo")
                    assertEquals(2, refreshes.get())
                }
            }
        }
    }

    // Initial calls start undispatched. The first dispatch processes the held HTTP response.
    private class HTTPResponseBoundary : CoroutineDispatcher() {
        private val firstDispatch = AtomicBoolean(true)
        val handled = CompletableDeferred<Unit>()

        override fun dispatch(context: CoroutineContext, block: Runnable) {
            val capture = firstDispatch.getAndSet(false)
            Dispatchers.Default.dispatch(context, Runnable {
                try { block.run() } finally { if (capture) handled.complete(Unit) }
            })
        }
    }

    @Test
    fun `cancelled refresh cannot replace credentials for later calls`() = runBlocking {
        for (mode in TransportMode.entries) {
            val success = if (mode == TransportMode.JSON) """{"data":true}""".toByteArray() else byteArrayOf(1)
            withClient({ request, count ->
                response(request, if (count == 1) 401 else 200, if (count == 1) errorBody(mode, 401) else success)
            }) { client, sent ->
                val transport = transport(client, mode) {
                    currentCoroutineContext().cancel()
                    "late-token"
                }
                transport.setToken("original-token")
                val first = async { transport.call("echo") }
                assertFailsWith<CancellationException> { first.await() }
                transport.call("echo")
                assertEquals("Bearer original-token", sent.last().header("Authorization"))
            }
        }
    }

    @Test
    fun `reentrant JSON refresh delivers the inner unauthorized response`() = runBlocking {
        expectReentrantUnauthorized(TransportMode.JSON)
    }

    @Test
    fun `reentrant Binary refresh delivers the inner unauthorized response`() = runBlocking {
        expectReentrantUnauthorized(TransportMode.BINARY)
    }

    @Test
    fun `nested transports retain the ancestor refresh flight`() = runBlocking {
        expectReentrantUnauthorized(TransportMode.BINARY, throughPeer = true)
    }

    private suspend fun expectReentrantUnauthorized(mode: TransportMode, throughPeer: Boolean = false) {
        val requests = if (throughPeer) 3 else 2
        val refreshes = AtomicInteger()
        val peerRefreshes = AtomicInteger()
        val refreshFinished = CompletableDeferred<Unit>()
        val peerFinished = CompletableDeferred<Unit>()
        val requestSchema = mapOf("echo" to APISchemaEntry(1), "refresh" to APISchemaEntry(2))
        withClient({ request, count ->
            val message = if (count == requests) "refresh denied" else "expired"
            response(request, 401, errorBody(mode, 401, message = message))
        }) { client, sent ->
            lateinit var transport: OkHttpTransport
            val peer = if (throughPeer) {
                OkHttpTransport(endpoint, client = client, mode = mode, schema = requestSchema, timeoutSeconds = 0,
                    onTokenExpired = {
                        peerRefreshes.incrementAndGet()
                        try { transport.call("refresh"); null } finally { peerFinished.complete(Unit) }
                    })
            } else null
            transport = OkHttpTransport(endpoint, client = client, mode = mode, schema = requestSchema, timeoutSeconds = 0,
                onTokenExpired = {
                    if (refreshes.incrementAndGet() != 1) null else try {
                        (peer ?: transport).call("refresh")
                        null
                    } finally { refreshFinished.complete(Unit) }
                })
            val error = try {
                assertFailsWith<LuxoError> { withTimeout(3000) { transport.call("echo") } }
            } finally {
                transport.setToken("cleanup")
                peer?.setToken("cleanup")
                withTimeout(3000) {
                    if (refreshes.get() != 0) refreshFinished.await()
                    if (peerRefreshes.get() != 0) peerFinished.await()
                }
            }
            assertEquals("Unauthorized", error.error)
            assertEquals(401, error.code)
            assertEquals("refresh denied", error.message)
            assertEquals(requests, sent.size)
            assertEquals(1, refreshes.get())
            assertEquals(if (throughPeer) 1 else 0, peerRefreshes.get())
        }
    }

    @Test
    fun `cooperative refresh cancellation terminates without retry`() = runBlocking {
        for (mode in TransportMode.entries) {
            val entered = CompletableDeferred<Unit>()
            withClient({ request, _ -> response(request, 401, errorBody(mode, 401)) }) { client, sent ->
                val transport = transport(client, mode) {
                    entered.complete(Unit)
                    awaitCancellation()
                }
                val first = async { transport.call("echo") }
                withTimeout(3000) { entered.await(); first.cancelAndJoin() }
                assertTrue(first.isCancelled)
                assertEquals(1, sent.size)
            }
        }
    }

    private fun transport(client: OkHttpClient, mode: TransportMode, refresh: (suspend () -> String?)? = null) =
        OkHttpTransport(endpoint, client = client, mode = mode, schema = schema, onTokenExpired = refresh)

    private fun errorBody(mode: TransportMode, code: Int, name: String = "Unauthorized", message: String = "expired"): ByteArray {
        if (mode == TransportMode.JSON) return """{"error":"$name","code":$code,"message":"$message"}""".toByteArray()
        return LuxoEncoder().apply {
            writeFieldInt(1, code.toLong())
            writeFieldString(2, name)
            writeFieldString(3, message)
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
