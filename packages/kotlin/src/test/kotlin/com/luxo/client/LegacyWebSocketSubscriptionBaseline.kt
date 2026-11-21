package com.luxo.client

import kotlinx.coroutines.*
import kotlinx.serialization.json.*
import okhttp3.*
import okio.ByteString
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicLong
import kotlin.math.min

// Frozen pre-UInt64-subscription transport, only for paired performance measurements.
// This is not an alternative production implementation or a compatibility fallback.
/**
 * OkHttp WebSocket transport that implements Transport interface for RPC calls,
 * plus subscription support for streaming.
 *
 * Features:
 * - Implements Transport.call() via request/response over WebSocket
 * - Auto-reconnect with exponential backoff (1s, 2s, 4s... up to 30s)
 * - Subscription API for streaming data
 *
 * Usage:
 *   val ws = LuxoWebSocket("ws://localhost:4000/luvia/ws")
 *   ws.connect()
 *   val result = ws.call("getUser", mapOf("id" to 1))  // RPC over WS
 *   ws.subscribe("liveTraces", mapOf("projectId" to 1)) { data -> ... }
 *   ws.close()
 */
internal class LegacyWebSocketSubscriptionBaseline(
    private val url: String,
    private val client: OkHttpClient = OkHttpClient(),
    token: String? = null,
    private val autoReconnect: Boolean = true,
    /** Max reconnect delay in milliseconds */
    private val maxReconnectDelayMs: Long = 30_000L,
    private val timeoutMillis: Long = 30_000L,
) : Transport {

    @Volatile private var ws: WebSocket? = null
    private data class Subscription(
        val params: Map<String, Any?>,
        val handler: (Any) -> Unit,
        var acknowledgement: CompletableDeferred<() -> Unit>?,
    )

    private val subscriptions = ConcurrentHashMap<String, Subscription>()
    // Metadata uses unsigned bit patterns; business Int/Duration values remain signed Long.
    private val pendingCalls = ConcurrentHashMap<Long, CompletableDeferred<Any>>()
    private val requestIdCounter = AtomicLong(0)
    @Volatile private var currentToken: String? = token
    @Volatile private var currentMode: TransportMode = TransportMode.JSON
    @Volatile private var currentSchema: Map<String, APISchemaEntry> = emptyMap()
    @Volatile private var connected: Boolean = false
    @Volatile private var closed: Boolean = false
    private var reconnectAttempt: Int = 0
    private var reconnectJob: Job? = null
    private val scope = CoroutineScope(Dispatchers.IO + SupervisorJob())
    private val connectionLock = Any()
    private var connecting = false
    private var connectionReady = CompletableDeferred<Unit>()

    // Internal boundary-test seam; the public transport always starts at zero.
    internal constructor(url: String, client: OkHttpClient, initialSequence: ULong, timeoutMillis: Long) :
        this(url, client, autoReconnect = false, timeoutMillis = timeoutMillis) {
        requestIdCounter.set(initialSequence.toLong())
    }

    override fun setSchema(schema: Map<String, APISchemaEntry>) { currentSchema = schema }
    override fun setMode(mode: TransportMode) { currentMode = mode }
    override fun setToken(token: String) { currentToken = token }

    override suspend fun call(api: String, params: Map<String, Any?>): Any {
        awaitConnection()

        val requestID = nextWebSocketRequestID(requestIdCounter)
        val message: Any = if (currentMode == TransportMode.BINARY) {
            val meta = currentSchema[api]
                ?: throw LuxoError("ConfigError", 0, "no schema for API \"$api\" — binary mode requires schema")
            LuxoBinaryProtocol.callFrame(requestID, LuxoBinaryProtocol.encodeRequest(meta, params))
        } else {
            buildJsonObject {
                putLuxoParams(params, currentSchema[api])
                put("\$id", webSocketJSONID(requestID))
                put("\$api", api)
            }.toString()
        }
        val deferred = CompletableDeferred<Any>()
        pendingCalls[requestID] = deferred

        val sent = when (message) {
            is ByteArray -> ws?.send(ByteString.of(*message))
            is String -> ws?.send(message)
            else -> false
        } ?: false
        if (!sent) {
            pendingCalls.remove(requestID)
            throw LuxoError("ConnectionError", 0, "failed to send WebSocket message")
        }

        return try {
            withTimeout(timeoutMillis) { deferred.await() }
        } catch (_: TimeoutCancellationException) {
            throw LuxoError("TimeoutError", 0, "request timed out after ${timeoutMillis}ms")
        } finally {
            pendingCalls.remove(requestID)
        }
    }

    fun connect() {
        if (closed) return
        startConnection()
    }

    private suspend fun awaitConnection() {
        if (closed) throw LuxoError("ConnectionError", 0, "WebSocket closed by client")
        connect()
        val ready = synchronized(connectionLock) { connectionReady }
        try {
            withTimeout(timeoutMillis) { ready.await() }
        } catch (_: TimeoutCancellationException) {
            throw LuxoError("TimeoutError", 0, "connection timed out after ${timeoutMillis}ms")
        }
    }

    private fun startConnection() {
        synchronized(connectionLock) {
            if (connected || connecting || closed) return
            if (connectionReady.isCompleted) connectionReady = CompletableDeferred()
            connecting = true
        }
        try {
            doConnect()
        } catch (error: Throwable) {
            handleDisconnect(LuxoError("ConnectionError", 0, "WebSocket connection failed: ${error.message}"))
        }
    }

    private fun doConnect() {
        val request = Request.Builder()
            .url(url)
            .apply { currentToken?.let { header("Authorization", "Bearer $it") } }
            .build()

        ws = client.newWebSocket(request, object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                val ready = synchronized(connectionLock) {
                    if (ws !== webSocket || closed) return@synchronized null
                    connected = true
                    connecting = false
                    connectionReady
                }
                if (ready == null) {
                    webSocket.close(1000, "stale connection")
                    return
                }
                ready.complete(Unit)
                reconnectAttempt = 0
                for ((api, subscription) in subscriptions) {
                    sendSubscription(api, subscription.params)
                }
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
                handleJsonMessage(text)
            }

            override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
                handleBinaryMessage(bytes.toByteArray())
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                if (ws !== webSocket) return
                ws = null
                val error = LuxoError("ConnectionError", 0, "WebSocket connection lost: ${t.message}")
                handleDisconnect(error)

                // Auto-reconnect with exponential backoff
                if (autoReconnect && !closed) {
                    scheduleReconnect()
                }
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                if (ws !== webSocket) return
                ws = null
                handleDisconnect(LuxoError("ConnectionError", 0, "WebSocket closed: $reason"))
                if (autoReconnect && !closed) {
                    scheduleReconnect()
                }
            }
        })
    }

    private fun scheduleReconnect() {
        reconnectJob?.cancel()
        reconnectJob = scope.launch {
            val delayMs = min(1000L * (1L shl min(reconnectAttempt, 14)), maxReconnectDelayMs)
            reconnectAttempt++
            delay(delayMs)
            if (!closed) {
                startConnection()
            }
        }
    }

    private fun handleDisconnect(error: LuxoError) {
        val ready = synchronized(connectionLock) {
            connected = false
            connecting = false
            connectionReady
        }
        if (!ready.isCompleted) ready.completeExceptionally(error)
        for (deferred in pendingCalls.values) deferred.completeExceptionally(error)
        pendingCalls.clear()
        for ((api, subscription) in subscriptions) {
            val acknowledgement = subscription.acknowledgement ?: continue
            acknowledgement.completeExceptionally(error)
            subscriptions.remove(api, subscription)
        }
    }

    private fun handleJsonMessage(text: String) {
        try {
            val json = Json.parseToJsonElement(text).jsonObject
            val subscription = json["\$sub"] as? JsonPrimitive
            if (subscription != null) {
                if (!subscription.isString) return
                val error = decodeLuxoJsonError(json, 0)
                val ok = json["ok"] as? JsonPrimitive
                if (error == null && (ok == null || ok.isString || ok.booleanOrNull != true)) return
                acknowledgeSubscription(subscription.content, error)
                return
            }
            val stream = json["\$stream"]?.jsonPrimitive?.contentOrNull
            if (stream != null) {
                subscriptions[stream]?.handler?.invoke(json["data"] ?: JsonNull)
                return
            }
            val requestID = webSocketJSONRequestID(json["\$id"]) ?: return
            val deferred = pendingCalls.remove(requestID) ?: return
            val error = decodeLuxoJsonError(json, 0)
            if (error != null) {
                deferred.completeExceptionally(error)
            } else {
                deferred.complete(json["data"] ?: JsonNull)
            }
        } catch (_: Exception) {
            // Ignore malformed or unrelated frames. They cannot be correlated safely.
        }
    }

    private fun handleBinaryMessage(bytes: ByteArray) {
        try {
            val dec = LuxoDecoder(bytes)
            val frameType = dec.readVarint()
            if (dec.offset() != 1) return
            val id = dec.readVarint()
            val payload = dec.readRemainingBytes()
            when (frameType) {
                0x07L,
                0x08L -> {
                    if (frameType == 0x07L && payload.isNotEmpty()) return
                    val api = currentSchema.entries.firstOrNull { it.value.id.toLong() == id }?.key
                    if (api != null) {
                        acknowledgeSubscription(
                            api,
                            if (frameType == 0x08L) {
                                LuxoBinaryProtocol.decodeError(payload, 0)
                            } else {
                                null
                            },
                        )
                    }
                }
                LuxoBinaryProtocol.CALL_SUCCESS.toLong() -> pendingCalls.remove(id)?.complete(payload)
                LuxoBinaryProtocol.CALL_ERROR.toLong() -> pendingCalls.remove(id)?.completeExceptionally(
                    LuxoBinaryProtocol.decodeError(payload, 0),
                )
                0x06L -> {
                    val api = currentSchema.entries.firstOrNull { it.value.id.toLong() == id }?.key
                    if (api != null) subscriptions[api]?.handler?.invoke(payload)
                }
            }
        } catch (_: Exception) {
            // Ignore malformed or unrelated frames. They cannot be correlated safely.
        }
    }

    suspend fun subscribeRaw(api: String, params: Map<String, Any?>, observer: StreamObserver?, handler: (Any) -> Unit): () -> Unit {
        awaitConnection()
        val acknowledgement = CompletableDeferred<() -> Unit>()
        val subscription = Subscription(params, handler, acknowledgement)
        if (subscriptions.putIfAbsent(api, subscription) != null) {
            throw LuxoError("ConfigError", 0, "already subscribed to \"$api\"")
        }
        if (currentMode == TransportMode.BINARY && currentSchema[api] == null) {
            subscriptions.remove(api)
            throw LuxoError("ConfigError", 0, "no schema for API \"$api\" — binary mode requires schema")
        }
        try {
            if (!sendSubscription(api, params)) {
                throw LuxoError("ConnectionError", 0, "failed to send subscription")
            }
        } catch (error: Throwable) {
            subscriptions.remove(api, subscription)
            throw error
        }
        return try {
            withTimeout(timeoutMillis) { acknowledgement.await() }
        } catch (_: TimeoutCancellationException) {
            subscriptions.remove(api, subscription)
            throw LuxoError("TimeoutError", 0, "subscription timed out after ${timeoutMillis}ms")
        }
    }

    private fun acknowledgeSubscription(api: String, error: LuxoError?) {
        val subscription = subscriptions[api] ?: return
        val acknowledgement = subscription.acknowledgement
        if (error != null) {
            subscriptions.remove(api, subscription)
            acknowledgement?.completeExceptionally(error)
            return
        }
        if (acknowledgement == null) return
        subscription.acknowledgement = null
        acknowledgement.complete { unsubscribe(api) }
    }

    private fun sendSubscription(api: String, params: Map<String, Any?>): Boolean {
        if (currentMode == TransportMode.BINARY) {
            val meta = currentSchema[api]
                ?: throw LuxoError("ConfigError", 0, "no schema for API \"$api\"")
            val frame = legacyFrame(4, null, LuxoBinaryProtocol.encodeRequest(meta, params))
            return ws?.send(ByteString.of(*frame)) == true
        }
        val message = buildJsonObject {
            put("\$sub", api)
            putLuxoParams(params, currentSchema[api])
        }
        return ws?.send(message.toString()) == true
    }

    fun unsubscribe(api: String) {
        if (subscriptions.remove(api) == null || !connected) return
        if (currentMode == TransportMode.BINARY) {
            val meta = currentSchema[api] ?: return
            ws?.send(ByteString.of(*legacyFrame(5, meta.id.toLong(), byteArrayOf())))
        } else {
            ws?.send(buildJsonObject { put("\$unsub", api) }.toString())
        }
    }

    fun close() {
        closed = true
        reconnectJob?.cancel()
        val socket = ws
        ws = null
        socket?.close(1000, "client close")
        connected = false
        // Fail any pending calls
        val error = LuxoError("ConnectionError", 0, "WebSocket closed by client")
        synchronized(connectionLock) {
            connecting = false
            if (!connectionReady.isCompleted) connectionReady.completeExceptionally(error)
        }
        for (subscription in subscriptions.values) {
            subscription.acknowledgement?.completeExceptionally(error)
        }
        subscriptions.clear()
        for (deferred in pendingCalls.values) {
            deferred.completeExceptionally(error)
        }
        pendingCalls.clear()
        scope.cancel()
    }
}

private fun legacyFrame(type: Long, id: Long?, payload: ByteArray): ByteArray {
    val encoder = LuxoEncoder()
    encoder.writeVarint(type)
    if (id != null) encoder.writeVarint(id)
    encoder.writeRawBytes(payload)
    return encoder.bytes()
}
