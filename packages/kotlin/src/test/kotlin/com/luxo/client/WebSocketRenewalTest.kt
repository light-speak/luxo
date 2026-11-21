package com.luxo.client

import kotlinx.coroutines.*
import kotlinx.serialization.json.*
import kotlin.test.*
import okhttp3.OkHttpClient
import okhttp3.Request
import org.junit.jupiter.api.Assumptions.assumeTrue
import java.util.concurrent.atomic.AtomicInteger

class WebSocketRenewalTest {
    @Test
    fun liveGoGatewayStreamExpiryNeverReplaysSubscriptions() = runBlocking {
        val endpoint = System.getenv("LUXO_WS_RENEWAL_URL")
        assumeTrue(endpoint != null && System.getenv("LUXO_WS_RENEWAL_STREAM") == "1", "requires owned Go stream fixture")
        val client = OkHttpClient()
        try {
            for (mode in TransportMode.entries) checkLiveStreamRenewal(client, requireNotNull(endpoint), mode)
        } finally {
            client.dispatcher.executorService.shutdownNow()
            client.connectionPool.evictAll()
        }
    }

    private suspend fun liveRequest(client: OkHttpClient, url: String, refresh: String? = null): Pair<String, String?> =
        withContext(Dispatchers.IO) {
            val request = Request.Builder().url(url).apply {
                if (refresh != null) header("Authorization", "Bearer $refresh")
            }.build()
            client.newCall(request).execute().use {
                assertEquals(200, it.code)
                requireNotNull(it.body).string() to it.header("X-Refresh-Token")
            }
        }

    private suspend fun liveStats(client: OkHttpClient, endpoint: String): Triple<Int, Int, Int> {
        val value = Json.parseToJsonElement(liveRequest(client, "$endpoint/stats").first).jsonObject
        return Triple(value.getValue("started").jsonPrimitive.int,
            value.getValue("gateway").jsonPrimitive.int, value.getValue("backend").jsonPrimitive.int)
    }

    private suspend fun waitLiveCleanup(client: OkHttpClient, endpoint: String) = withTimeout(2_000) {
        while (true) {
            val counts = liveStats(client, endpoint)
            if (counts.second == 0 && counts.third == 0) break
            delay(10)
        }
    }

    private fun assertLiveIdentity(mode: TransportMode, value: Any) {
        if (mode == TransportMode.JSON) assertEquals(JsonPrimitive(42), value)
        else assertContentEquals(byteArrayOf(84), value as ByteArray)
    }

    private suspend fun checkLiveStreamRenewal(client: OkHttpClient, endpoint: String, mode: TransportMode) {
        waitLiveCleanup(client, endpoint)
        val before = liveStats(client, endpoint).first
        val credentials = liveRequest(client, "$endpoint/token")
        val renewed = CompletableDeferred<Unit>()
        val first = CompletableDeferred<Any>()
        val ended = CompletableDeferred<LuxoError>()
        val errors = AtomicInteger(); val completed = AtomicInteger(); val values = AtomicInteger()
        val transport = LuxoWebSocket(endpoint.replace("http://", "ws://") + "/ws", client,
            token = credentials.first, timeoutMillis = 5_000, onTokenExpired = {
                val access = liveRequest(client, "$endpoint/refresh", requireNotNull(credentials.second)).first
                renewed.complete(Unit)
                access
            })
        try {
            transport.setMode(mode)
            transport.setSchema(mapOf("identity" to APISchemaEntry(7), "watch" to APISchemaEntry(8)))
            transport.subscribe("watch", { it }, observer = StreamObserver(
                onError = { errors.incrementAndGet(); ended.complete(it) },
                onComplete = { completed.incrementAndGet() })) { values.incrementAndGet(); first.complete(it) }
            assertLiveIdentity(mode, withTimeout(5_000) { first.await() })
            assertEquals(401, withTimeout(6_000) { ended.await() }.code)
            withTimeout(6_000) { renewed.await() }
            assertLiveIdentity(mode, transport.call("identity"))
            waitLiveCleanup(client, endpoint)
            assertEquals(before + 1, liveStats(client, endpoint).first)
            val fresh = CompletableDeferred<Any>()
            val stop = transport.subscribe("watch", { it }, observer = StreamObserver(
                onError = { fresh.completeExceptionally(it) })) { fresh.complete(it) }
            assertLiveIdentity(mode, withTimeout(5_000) { fresh.await() })
            assertEquals(before + 2, liveStats(client, endpoint).first)
            stop()
            assertEquals(1, errors.get()); assertEquals(0, completed.get()); assertEquals(1, values.get())
        } finally { transport.close() }
    }

    @Test
    fun liveGoExpiryRefreshAndReconnect() = runBlocking {
        val endpoint = System.getenv("LUXO_WS_RENEWAL_URL")
        assumeTrue(endpoint != null, "requires owned Go renewal fixture")
        val client = OkHttpClient()
        try {
            for (mode in TransportMode.entries) {
                val credentials = withContext(Dispatchers.IO) {
                    client.newCall(Request.Builder().url("$endpoint/token").build()).execute().use {
                        assertEquals(200, it.code)
                        requireNotNull(it.body).string() to requireNotNull(it.header("X-Refresh-Token"))
                    }
                }
                val refreshed = CompletableDeferred<Unit>()
                val transport = LuxoWebSocket(
                    endpoint!!.replace("http://", "ws://") + "/ws", client, token = credentials.first,
                    timeoutMillis = 5_000, onTokenExpired = {
                        withContext(Dispatchers.IO) {
                            val request = Request.Builder().url("$endpoint/refresh")
                                .header("Authorization", "Bearer ${credentials.second}").build()
                            client.newCall(request).execute().use {
                                assertEquals(200, it.code)
                                val value = requireNotNull(it.body).string()
                                refreshed.complete(Unit)
                                value
                            }
                        }
                    })
                try {
                    transport.setSchema(mapOf("identity" to APISchemaEntry(7)))
                    transport.setMode(mode)
                    val first = transport.call("identity")
                    if (mode == TransportMode.JSON) assertEquals(JsonPrimitive(42), first)
                    else assertContentEquals(byteArrayOf(84), first as ByteArray)
                    withTimeout(6_000) { refreshed.await() }
                    val second = transport.call("identity")
                    if (mode == TransportMode.JSON) assertEquals(first, second)
                    else assertContentEquals(first as ByteArray, second as ByteArray)
                } finally { transport.close() }
            }
        } finally {
            client.dispatcher.executorService.shutdownNow()
            client.connectionPool.evictAll()
        }
    }

    @Test
    fun expiryRefreshesOnceAndNeverReplaysOldCalls() = runBlocking {
        for (manual in listOf(false, true)) {
            val client = TestSocketClient()
            val started = CompletableDeferred<Unit>()
            val token = CompletableDeferred<String?>()
            var refreshes = 0
            val transport = LuxoWebSocket("ws://localhost/luvia/ws", client, token = "old", autoReconnect = false,
                onTokenExpired = { refreshes++; started.complete(Unit); token.await() })
            try {
                transport.connect()
                val old = client.socket
                old.open()
                val pending = async { assertFailsWith<LuxoError> { transport.call("echo") } }
                yield()
                assertEquals(1, old.sent)
                old.closing(4001)
                old.closing(4001)
                assertEquals(401, pending.await().code)
                withTimeout(2_000) { started.await() }
                assertEquals(1, refreshes)
                val next = async { transport.call("echo") }
                yield()
                assertSame(old, client.socket)
                if (manual) transport.setToken("manual")
                token.complete("renewed")
                withTimeout(2_000) { while (client.socket === old) yield() }
                val fresh = client.socket
                assertEquals("Bearer " + if (manual) "manual" else "renewed", fresh.request().header("Authorization"))
                fresh.onText = { text ->
                    val id = Json.parseToJsonElement(text).jsonObject.getValue("\$id")
                    fresh.receive("{\"\$id\":$id,\"data\":42}")
                }
                fresh.open()
                assertEquals(JsonPrimitive(42), withTimeout(2_000) { next.await() })
                assertEquals(1, fresh.sent)
            } finally { transport.close() }
        }
    }

    @Test
    fun expiredSubscriptionsEndOnceInBothModesWithoutReplay() = runBlocking {
        for (mode in TransportMode.entries) {
            val client = TestSocketClient()
            val token = CompletableDeferred<String?>()
            val started = CompletableDeferred<Unit>()
            val transport = LuxoWebSocket("ws://localhost", client, autoReconnect = false,
                onTokenExpired = { started.complete(Unit); token.await() })
            try {
                transport.setMode(mode)
                transport.setSchema(mapOf("watch" to APISchemaEntry(1), "echo" to APISchemaEntry(2)))
                transport.connect()
                val old = client.socket
                old.open()
                old.onText = { text ->
                    val id = Json.parseToJsonElement(text).jsonObject.getValue("\$id")
                    old.receive("{\"\$sub\":$id,\"ok\":true}")
                }
                old.onBinary = { bytes ->
                    val decoder = LuxoDecoder(bytes.toByteArray())
                    assertEquals(0x10L, decoder.readVarint())
                    old.receive(frame(0x13, decoder.readVarint()))
                }
                val failures = mutableListOf<LuxoError>()
                val cancel = transport.subscribe("watch", { it }, observer = StreamObserver(onError = { failures += it })) {}
                old.closing(4001)
                old.closing(4001)
                assertEquals(401, failures.single().code)
                withTimeout(2_000) { started.await() }
                token.complete("new")
                val next = async { transport.call("echo") }
                withTimeout(2_000) { while (client.socket === old) yield() }
                val fresh = client.socket
                fresh.onText = { text ->
                    val id = Json.parseToJsonElement(text).jsonObject.getValue("\$id")
                    fresh.receive("{\"\$id\":$id,\"data\":42}")
                }
                fresh.onBinary = { bytes ->
                    val decoder = LuxoDecoder(bytes.toByteArray())
                    decoder.readVarint()
                    fresh.receive(frame(LuxoBinaryProtocol.CALL_SUCCESS, decoder.readVarint(), byteArrayOf(42)))
                }
                fresh.open()
                val result = withTimeout(2_000) { next.await() }
                if (mode == TransportMode.JSON) assertEquals(JsonPrimitive(42), result)
                else assertContentEquals(byteArrayOf(42), result as ByteArray)
                cancel()
                assertEquals(1, fresh.sent)
                assertEquals(1, failures.size)
            } finally { transport.close() }
        }
    }

    private fun frame(type: Int, id: Long, payload: ByteArray = byteArrayOf()): ByteArray {
        val encoder = LuxoEncoder()
        encoder.writeVarint(type.toLong())
        encoder.writeVarint(id)
        encoder.writeRawBytes(payload)
        return encoder.bytes()
    }

    @Test
    fun failedRenewalSettlesAllConcurrentWaiters() = runBlocking {
        for (throws in listOf(false, true)) {
            val client = TestSocketClient()
            val entered = CompletableDeferred<Unit>()
            val release = CompletableDeferred<Unit>()
            val calls = AtomicInteger()
            val transport = LuxoWebSocket("ws://localhost", client, timeoutMillis = 2_000, onTokenExpired = {
                calls.incrementAndGet()
                entered.complete(Unit)
                release.await()
                if (throws) error("refresh rejected")
                null
            })
            try {
                transport.connect()
                val old = client.socket
                old.open()
                old.closing(4001)
                withTimeout(2_000) { entered.await() }
                val waiters = List(8) {
                    async(start = CoroutineStart.UNDISPATCHED) {
                        assertFailsWith<LuxoError> { transport.call("echo") }
                    }
                }
                release.complete(Unit)
                withTimeout(2_000) { waiters.awaitAll() }.forEach { assertEquals(401, it.code) }
                assertEquals(1, calls.get())
                assertSame(old, client.socket)
            } finally { release.complete(Unit); transport.close() }
        }
    }

    @Test
    fun failedOrAbsentRefreshDoesNotReconnectWithExpiredToken() = runBlocking {
        for (kind in listOf("missing", "null", "throw", "timeout")) {
            val client = TestSocketClient()
            val callback: (suspend () -> String?)? = when (kind) {
                "missing" -> null
                "null" -> { { null } }
                "throw" -> { { error("refresh rejected") } }
                else -> { { awaitCancellation() } }
            }
            val transport = LuxoWebSocket("ws://localhost", client, timeoutMillis = 100, onTokenExpired = callback)
            try {
                transport.connect()
                val old = client.socket
                old.open()
                old.closing(4001)
                val error = withTimeout(2_000) { assertFailsWith<LuxoError> { transport.call("echo") } }
                assertEquals(401, error.code)
                delay(1_100)
                assertSame(old, client.socket)
            } finally { transport.close() }
        }
    }

    @Test
    fun closeDuringRefreshReleasesWaitersAndIgnoresLateToken() = runBlocking {
        val client = TestSocketClient()
        val started = CompletableDeferred<Unit>()
        val release = CompletableDeferred<Unit>()
        val finished = CompletableDeferred<Unit>()
        val transport = LuxoWebSocket("ws://localhost", client, onTokenExpired = {
            started.complete(Unit)
            withContext(NonCancellable) { release.await() }
            finished.complete(Unit)
            "late"
        })
        try {
            transport.connect()
            val old = client.socket
            old.open()
            old.closing(4001)
            withTimeout(2_000) { started.await() }
            val pending = async { assertFailsWith<LuxoError> { transport.call("echo") } }
            yield()
            transport.close()
            withTimeout(2_000) { pending.await() }
            release.complete(Unit)
            withTimeout(2_000) { finished.await() }
            assertSame(old, client.socket)
        } finally { release.complete(Unit); transport.close() }
    }
}
