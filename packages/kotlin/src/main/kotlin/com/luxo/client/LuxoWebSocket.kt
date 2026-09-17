package com.luxo.client

import kotlinx.coroutines.*
import kotlinx.serialization.json.*
import okhttp3.*
import okio.ByteString
import okio.ByteString.Companion.toByteString
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicLong
import kotlin.math.min

// MARK: - WebSocket Transport

/**
 * OkHttp WebSocket transport that implements Transport interface for RPC calls,
 * plus independently identified subscriptions for streaming.
 *
 * Features:
 * - Implements Transport.call() via request/response over WebSocket
 * - Auto-reconnect the connection (1s, 2s, 4s... up to 30s), never replay subscriptions
 * - Subscription API for streaming data
 *
 * Usage:
 *   val ws = LuxoWebSocket("ws://localhost:4000/luvia/ws")
 *   ws.connect()
 *   val result = ws.call("getUser", mapOf("id" to 1))  // RPC over WS
 *   ws.subscribe("liveTraces", { it }, mapOf("projectId" to 1)) { data -> ... }
 *   ws.close()
 */
class LuxoWebSocket(
    private val url: String,
    private val client: OkHttpClient = OkHttpClient(),
    token: String? = null,
    private val autoReconnect: Boolean = true,
    private val maxReconnectDelayMs: Long = 30_000L,
    private val timeoutMillis: Long = 30_000L,
) : Transport {
    @Volatile private var ws: WebSocket? = null
    private class Subscription<T>(
        val mode: TransportMode,
        val decoder: (Any) -> T,
        val handler: (T) -> Unit,
        val observer: StreamObserver?,
        @Volatile var acknowledgement: CompletableDeferred<() -> Unit>?,
    ) {
        fun deliver(transport: LuxoWebSocket, id: Long, data: Any) {
            val value = try { decoder(data) } catch (error: Exception) {
                transport.failDecoding(id, this, error)
                return
            }
            // Application exceptions propagate unchanged, outside the decoding catch.
            handler(value)
        }
    }

    // Long carries the unsigned ID bits, never a signed business Int/Duration conversion.
    private val subscriptions = ConcurrentHashMap<Long, Subscription<*>>()
    private val pendingCalls = ConcurrentHashMap<Long, CompletableDeferred<Any>>()
    private val requestIdCounter = AtomicLong(0)
    @Volatile private var currentToken: String? = token
    @Volatile private var currentMode = TransportMode.JSON
    @Volatile private var currentSchema: Map<String, APISchemaEntry> = emptyMap()
    @Volatile private var connected = false
    @Volatile private var closed = false
    private var reconnectAttempt = 0
    private var reconnectJob: Job? = null
    private val scope = CoroutineScope(Dispatchers.IO + SupervisorJob())
    // Subscription allocation and enqueue share the lifecycle lock, not the data path.
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
        val id = nextWebSocketRequestID(requestIdCounter)
        val message: Any = if (currentMode == TransportMode.BINARY) {
            val meta = requireSchema(api)
            LuxoBinaryProtocol.callFrame(id, LuxoBinaryProtocol.encodeRequest(meta, params))
        } else {
            encodeLuxoJSON(buildJsonObject {
                putLuxoParams(params, currentSchema[api])
                put("\$id", webSocketJSONID(id))
                put("\$api", api)
            })
        }
        val deferred = CompletableDeferred<Any>()
        pendingCalls[id] = deferred
        val sent = when (message) {
            is ByteArray -> ws?.send(message.toByteString())
            is String -> ws?.send(message)
            else -> false
        } ?: false
        if (!sent) {
            pendingCalls.remove(id)
            throw LuxoError("ConnectionError", 0, "failed to send WebSocket message")
        }
        return try {
            withTimeout(timeoutMillis) { deferred.await() }
        } catch (_: TimeoutCancellationException) {
            throw LuxoError("TimeoutError", 0, "request timed out after ${timeoutMillis}ms")
        } finally {
            pendingCalls.remove(id)
        }
    }

    fun connect() = synchronized(connectionLock) {
        if (connected || connecting || closed) return@synchronized
        if (connectionReady.isCompleted) connectionReady = CompletableDeferred()
        connecting = true
        openSocket()
    }

    private fun openSocket() {
        try {
            val request = Request.Builder().url(url)
                .apply { currentToken?.let { header("Authorization", "Bearer $it") } }.build()
            // OkHttp's asynchronous callbacks cannot observe a half-installed socket.
            ws = client.newWebSocket(request, listener)
        } catch (error: Exception) {
            failPending(LuxoError("ConnectionError", 0, "WebSocket connection failed: ${error.message}"))
        }
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

    private val listener = object : WebSocketListener() {
        override fun onOpen(webSocket: WebSocket, response: Response) = opened(webSocket)
        override fun onMessage(webSocket: WebSocket, text: String) {
            if (ws === webSocket && !closed) handleJsonMessage(text, webSocket)
        }
        override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
            if (ws === webSocket && !closed) handleBinaryMessage(bytes.toByteArray())
        }
        override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
            disconnected(webSocket, LuxoError("ConnectionError", 0, "WebSocket connection lost: ${t.message}"))
        }
        override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
            disconnected(webSocket, LuxoError("ConnectionError", 0, "WebSocket closed: $reason"))
        }
    }

    private fun opened(socket: WebSocket) = synchronized(connectionLock) {
        if (ws !== socket || closed) {
            socket.close(1000, "stale connection")
            return@synchronized
        }
        connected = true
        connecting = false
        reconnectAttempt = 0
        connectionReady.complete(Unit)
    }

    private fun disconnected(socket: WebSocket, error: LuxoError, subscriptionError: LuxoError? = null) {
        val failure: LuxoError
        val observers = synchronized(connectionLock) {
            if (ws !== socket) return
            ws = null
            failure = subscriptionError ?: LuxoError("NetworkError", 0, error.message)
            val observers = subscriptions.values.mapNotNull {
                if (it.acknowledgement == null) it.observer?.onError else null
            }
            failPending(error, failure)
            if (autoReconnect && !closed) scheduleReconnect()
            observers
        }
        notifyDisconnected(observers, failure)
    }

    private fun notifyDisconnected(observers: List<(LuxoError) -> Unit>, error: LuxoError) {
        // Retire all subscriptions first; one application's exception must not hide other failures.
        var failure: Throwable? = null
        for (observer in observers) {
            try { observer(error) }
            catch (thrown: Throwable) {
                if (failure == null) failure = thrown
                else if (failure !== thrown) failure.addSuppressed(thrown)
            }
        }
        failure?.let { throw it }
    }

    private fun scheduleReconnect() {
        reconnectJob?.cancel()
        val delayMs = min(1000L * (1L shl min(reconnectAttempt, 14)), maxReconnectDelayMs)
        reconnectAttempt++
        reconnectJob = scope.launch {
            delay(delayMs)
            connect()
        }
    }

    // Called only while holding connectionLock. A lost connection has no resumable subscriptions.
    private fun failPending(error: LuxoError, subscriptionError: LuxoError = error) {
        connected = false
        connecting = false
        connectionReady.completeExceptionally(error)
        for (deferred in pendingCalls.values) deferred.completeExceptionally(error)
        pendingCalls.clear()
        for (subscription in subscriptions.values) subscription.acknowledgement?.completeExceptionally(subscriptionError)
        subscriptions.clear()
    }

    private fun handleJsonMessage(text: String, socket: WebSocket) {
        val json = try {
            decodeLuxoJSON(text).jsonObject
        } catch (_: IllegalArgumentException) {
            // Parsing only: application callback failures must not be swallowed.
            val error = LuxoError("ParseError", 0, "invalid WebSocket JSON message")
            try { disconnected(socket, error, error) }
            finally { socket.close(1007, "invalid JSON") }
            return
        }
        val end = json["\$end"]
        if (end != null) {
            if (json.containsKey("\$sub") || json.containsKey("\$stream") || json.containsKey("\$id")) return
            val id = webSocketJSONRequestID(end) ?: return
            finishSubscription(id, TransportMode.JSON, webSocketJSONTerminalError(json))
            return
        }
        val subscription = json["\$sub"]
        if (subscription != null) {
            val id = webSocketJSONRequestID(subscription) ?: return
            val error = decodeLuxoJsonError(json, 0)
            val ok = json["ok"] as? JsonPrimitive
            if (error == null && (ok == null || ok.isString || ok.booleanOrNull != true)) return
            acknowledgeSubscription(id, TransportMode.JSON, error)
            return
        }
        val stream = json["\$stream"]
        if (stream != null) {
            val id = webSocketJSONRequestID(stream) ?: return
            deliver(id, TransportMode.JSON, json["data"] ?: JsonNull)
            return
        }
        val id = webSocketJSONRequestID(json["\$id"]) ?: return
        val deferred = pendingCalls.remove(id) ?: return
        val error = decodeLuxoJsonError(json, 0)
        if (error == null) deferred.complete(json["data"] ?: JsonNull)
        else deferred.completeExceptionally(error)
    }

    private fun handleBinaryMessage(bytes: ByteArray) {
        val dec = LuxoDecoder(bytes)
        val type: Long
        val id: Long
        try {
            type = dec.readVarint()
            if (dec.offset() != 1) return
            if (type !in LuxoBinaryProtocol.CALL_SUCCESS.toLong()..LuxoBinaryProtocol.CALL_ERROR.toLong() &&
                type !in LuxoBinaryProtocol.STREAM.toLong()..LuxoBinaryProtocol.STREAM_ERROR.toLong()) return
            id = dec.readVarint()
        } catch (_: Exception) {
            // Without a valid header the frame cannot be correlated safely.
            return
        }
        if (type == LuxoBinaryProtocol.SUBSCRIBE_SUCCESS.toLong()) {
            if (dec.remaining() == 0) acknowledgeSubscription(id, TransportMode.BINARY, null)
            return
        }
        handleBinaryPayload(type, id, dec.readRemainingBytes())
    }

    private fun handleBinaryPayload(type: Long, id: Long, payload: ByteArray) {
        when (type) {
            LuxoBinaryProtocol.CALL_SUCCESS.toLong() -> pendingCalls.remove(id)?.complete(payload)
            LuxoBinaryProtocol.CALL_ERROR.toLong() -> pendingCalls.remove(id)?.completeExceptionally(
                LuxoBinaryProtocol.decodeError(payload, 0),
            )
            LuxoBinaryProtocol.STREAM.toLong() -> deliver(id, TransportMode.BINARY, payload)
            else -> handleBinarySubscriptionResult(type, id, payload)
        }
    }

    private fun handleBinarySubscriptionResult(type: Long, id: Long, payload: ByteArray) {
        // The header whitelist leaves only setup errors and terminal tags on this cold path.
        val error = if (type == LuxoBinaryProtocol.STREAM_COMPLETE.toLong()) {
            if (payload.isEmpty()) null else LuxoError("ParseError", 0, "completion frame has trailing payload")
        } else LuxoBinaryProtocol.decodeError(payload, 0)
        finishSubscription(id, TransportMode.BINARY, error, terminal = type != LuxoBinaryProtocol.SUBSCRIBE_ERROR.toLong())
    }

    private fun deliver(id: Long, mode: TransportMode, data: Any) {
        val subscription = subscriptions[id] ?: return
        if (subscription.mode == mode && subscription.acknowledgement == null) subscription.deliver(this, id, data)
    }

    private fun failDecoding(id: Long, subscription: Subscription<*>, cause: Exception) {
        synchronized(connectionLock) {
            if (!subscriptions.remove(id, subscription)) return
            // Remote cancellation is advisory; a failed write must not restore local state.
            try { sendCancellation(id, subscription.mode) } catch (_: Exception) { }
        }
        subscription.observer?.onError?.invoke(LuxoError("DecodeError", 0, cause.message ?: "invalid subscription payload"))
    }

    override suspend fun <T> subscribe(
        api: String,
        decoder: (Any) -> T,
        params: Map<String, Any?>,
        observer: StreamObserver?,
        handler: (T) -> Unit,
    ): () -> Unit {
        awaitConnection()
        val acknowledgement = CompletableDeferred<() -> Unit>()
        val id = synchronized(connectionLock) {
            if (closed || !connected) throw LuxoError("ConnectionError", 0, "WebSocket is not connected")
            val id = nextWebSocketRequestID(requestIdCounter)
            val mode = currentMode
            val message = subscriptionMessage(id, api, params, mode)
            val subscription = Subscription(mode, decoder, handler, observer, acknowledgement)
            subscriptions[id] = subscription
            try {
                if (!send(message)) throw LuxoError("ConnectionError", 0, "failed to send subscription")
            } catch (error: Exception) {
                subscriptions.remove(id, subscription)
                throw error
            }
            id
        }
        return try {
            withTimeout(timeoutMillis) { acknowledgement.await() }
        } catch (_: TimeoutCancellationException) {
            unsubscribe(id)
            throw LuxoError("TimeoutError", 0, "subscription timed out after ${timeoutMillis}ms")
        } catch (error: CancellationException) {
            unsubscribe(id)
            throw error
        }
    }

    private fun acknowledgeSubscription(id: Long, mode: TransportMode, error: LuxoError?) {
        if (error != null) {
            finishSubscription(id, mode, error, terminal = false)
            return
        }
        synchronized(connectionLock) {
            val subscription = subscriptions[id] ?: return
            if (subscription.mode != mode) return
            val acknowledgement = subscription.acknowledgement ?: return
            subscription.acknowledgement = null
            acknowledgement.complete { unsubscribe(id) }
        }
    }

    private fun finishSubscription(id: Long, mode: TransportMode, error: LuxoError?, terminal: Boolean = true) {
        val subscription = synchronized(connectionLock) {
            val current = subscriptions[id] ?: return
            if (current.mode != mode) return
            subscriptions.remove(id, current)
            val acknowledgement = current.acknowledgement
            if (acknowledgement != null) {
                acknowledgement.completeExceptionally(if (terminal)
                    LuxoError("ParseError", 0, "stream ended before subscription acknowledgement") else requireNotNull(error))
                return
            }
            current
        }
        // Remove first and notify outside the lifecycle lock: callbacks may cancel or close.
        if (error != null) subscription.observer?.onError?.invoke(error)
        else subscription.observer?.onComplete?.invoke()
    }

    private fun subscriptionMessage(id: Long, api: String, params: Map<String, Any?>, mode: TransportMode): Any {
        if (mode == TransportMode.BINARY) {
            return LuxoBinaryProtocol.subscribeFrame(id, LuxoBinaryProtocol.encodeRequest(requireSchema(api), params)).toByteString()
        }
        return encodeLuxoJSON(buildJsonObject {
            putLuxoParams(params, currentSchema[api])
            put("\$id", webSocketJSONID(id))
            put("\$sub", api)
        })
    }

    private fun requireSchema(api: String): APISchemaEntry = currentSchema[api]
        ?: throw LuxoError("ConfigError", 0, "no schema for API \"$api\" — binary mode requires schema")

    private fun send(message: Any): Boolean = when (message) {
        is ByteString -> ws?.send(message) == true
        is String -> ws?.send(message) == true
        else -> false
    }

    private fun unsubscribe(id: Long) = synchronized(connectionLock) {
        val subscription = subscriptions.remove(id) ?: return@synchronized
        sendCancellation(id, subscription.mode)
    }

    private fun sendCancellation(id: Long, mode: TransportMode) {
        if (!connected) return
        if (mode == TransportMode.BINARY) {
            send(LuxoBinaryProtocol.unsubscribeFrame(id).toByteString())
        } else {
            send(buildJsonObject { put("\$unsub", webSocketJSONID(id)) }.toString())
        }
    }

    fun close() {
        val socket = synchronized(connectionLock) {
            closed = true
            reconnectJob?.cancel()
            val socket = ws
            ws = null
            failPending(LuxoError("ConnectionError", 0, "WebSocket closed by client"))
            socket
        }
        socket?.close(1000, "client close")
        scope.cancel()
    }
}
