package com.luxo.client

import kotlinx.coroutines.*
import kotlinx.serialization.json.*
import okhttp3.*
import okio.ByteString
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicLong
import kotlin.math.min

// MARK: - WebSocket Transport

/** Frozen pre-terminal UInt64 transport for paired benchmarks only; not a production fallback. */
internal class PreTerminalWebSocketBaseline(
    private val url: String,
    private val client: OkHttpClient = OkHttpClient(),
    token: String? = null,
    private val autoReconnect: Boolean = true,
    private val maxReconnectDelayMs: Long = 30_000L,
    private val timeoutMillis: Long = 30_000L,
) : Transport {
    @Volatile private var ws: WebSocket? = null
    private data class Subscription(
        val mode: TransportMode,
        val message: Any,
        val handler: (Any) -> Unit,
        @Volatile var acknowledgement: CompletableDeferred<() -> Unit>?,
    )

    // Long carries the unsigned ID bits, never a signed business Int/Duration conversion.
    private val subscriptions = ConcurrentHashMap<Long, Subscription>()
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
            buildJsonObject {
                putLuxoParams(params, currentSchema[api])
                put("\$id", webSocketJSONID(id))
                put("\$api", api)
            }.toString()
        }
        val deferred = CompletableDeferred<Any>()
        pendingCalls[id] = deferred
        val sent = when (message) {
            is ByteArray -> ws?.send(ByteString.of(*message))
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
            if (ws === webSocket && !closed) handleJsonMessage(text)
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
        try {
            // Replay must precede new subscriptions and follow unsigned server watermarks.
            for (id in subscriptions.keys.sortedWith(java.lang.Long::compareUnsigned)) {
                val subscription = subscriptions[id] ?: continue
                if (!send(subscription.message)) throw LuxoError("ConnectionError", 0, "failed to restore subscription")
            }
            connected = true
            connecting = false
            reconnectAttempt = 0
            connectionReady.complete(Unit)
        } catch (error: Exception) {
            socket.close(1000, "subscription restore failed")
            disconnected(socket, LuxoError("ConnectionError", 0, "failed to restore subscriptions: ${error.message}"))
        }
    }

    private fun disconnected(socket: WebSocket, error: LuxoError) = synchronized(connectionLock) {
        if (ws !== socket) return@synchronized
        ws = null
        failPending(error)
        if (autoReconnect && !closed) scheduleReconnect()
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

    // Called only while holding connectionLock; established subscriptions retain replay state.
    private fun failPending(error: LuxoError) {
        connected = false
        connecting = false
        connectionReady.completeExceptionally(error)
        for (deferred in pendingCalls.values) deferred.completeExceptionally(error)
        pendingCalls.clear()
        for ((id, subscription) in subscriptions) {
            val acknowledgement = subscription.acknowledgement ?: continue
            acknowledgement.completeExceptionally(error)
            subscriptions.remove(id, subscription)
        }
    }

    private fun handleJsonMessage(text: String) {
        try {
            val json = Json.parseToJsonElement(text).jsonObject
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
        } catch (_: Exception) {
            // Ignore malformed or unrelated frames. They cannot be correlated safely.
        }
    }

    private fun handleBinaryMessage(bytes: ByteArray) {
        try {
            val dec = LuxoDecoder(bytes)
            val type = dec.readVarint()
            if (dec.offset() != 1) return
            if (type !in LuxoBinaryProtocol.CALL_SUCCESS.toLong()..LuxoBinaryProtocol.CALL_ERROR.toLong() &&
                type !in LuxoBinaryProtocol.STREAM.toLong()..LuxoBinaryProtocol.SUBSCRIBE_ERROR.toLong()) return
            val id = dec.readVarint()
            if (type == LuxoBinaryProtocol.SUBSCRIBE_SUCCESS.toLong()) {
                if (dec.remaining() == 0) acknowledgeSubscription(id, TransportMode.BINARY, null)
                return
            }
            handleBinaryPayload(type, id, dec.readRemainingBytes())
        } catch (_: Exception) {
            // Ignore malformed or unrelated frames. They cannot be correlated safely.
        }
    }

    private fun handleBinaryPayload(type: Long, id: Long, payload: ByteArray) {
        when (type) {
            LuxoBinaryProtocol.SUBSCRIBE_ERROR.toLong() ->
                acknowledgeSubscription(id, TransportMode.BINARY, LuxoBinaryProtocol.decodeError(payload, 0))
            LuxoBinaryProtocol.CALL_SUCCESS.toLong() -> pendingCalls.remove(id)?.complete(payload)
            LuxoBinaryProtocol.CALL_ERROR.toLong() -> pendingCalls.remove(id)?.completeExceptionally(
                LuxoBinaryProtocol.decodeError(payload, 0),
            )
            LuxoBinaryProtocol.STREAM.toLong() -> deliver(id, TransportMode.BINARY, payload)
        }
    }

    private fun deliver(id: Long, mode: TransportMode, data: Any) {
        val subscription = subscriptions[id] ?: return
        if (subscription.mode == mode && subscription.acknowledgement == null) subscription.handler(data)
    }

    suspend fun subscribeRaw(api: String, params: Map<String, Any?>, observer: StreamObserver?, handler: (Any) -> Unit): () -> Unit {
        awaitConnection()
        val acknowledgement = CompletableDeferred<() -> Unit>()
        val id = synchronized(connectionLock) {
            if (closed || !connected) throw LuxoError("ConnectionError", 0, "WebSocket is not connected")
            val id = nextWebSocketRequestID(requestIdCounter)
            val mode = currentMode
            val subscription = Subscription(mode, subscriptionMessage(id, api, params, mode), handler, acknowledgement)
            subscriptions[id] = subscription
            try {
                if (!send(subscription.message)) throw LuxoError("ConnectionError", 0, "failed to send subscription")
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

    private fun acknowledgeSubscription(id: Long, mode: TransportMode, error: LuxoError?) = synchronized(connectionLock) {
        val subscription = subscriptions[id] ?: return@synchronized
        if (subscription.mode != mode) return@synchronized
        val acknowledgement = subscription.acknowledgement
        if (error != null) {
            subscriptions.remove(id, subscription)
            acknowledgement?.completeExceptionally(error)
        } else if (acknowledgement != null) {
            subscription.acknowledgement = null
            acknowledgement.complete { unsubscribe(id) }
        }
    }

    private fun subscriptionMessage(id: Long, api: String, params: Map<String, Any?>, mode: TransportMode): Any {
        if (mode == TransportMode.BINARY) {
            return ByteString.of(*LuxoBinaryProtocol.subscribeFrame(id, LuxoBinaryProtocol.encodeRequest(requireSchema(api), params)))
        }
        return buildJsonObject {
            putLuxoParams(params, currentSchema[api])
            put("\$id", webSocketJSONID(id))
            put("\$sub", api)
        }.toString()
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
        if (!connected) return@synchronized
        if (subscription.mode == TransportMode.BINARY) {
            send(ByteString.of(*LuxoBinaryProtocol.unsubscribeFrame(id)))
        } else {
            send(buildJsonObject { put("\$unsub", webSocketJSONID(id)) }.toString())
        }
        Unit
    }

    fun close() {
        val socket = synchronized(connectionLock) {
            closed = true
            reconnectJob?.cancel()
            val socket = ws
            ws = null
            failPending(LuxoError("ConnectionError", 0, "WebSocket closed by client"))
            subscriptions.clear()
            socket
        }
        socket?.close(1000, "client close")
        scope.cancel()
    }
}
