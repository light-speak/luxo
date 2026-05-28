package com.luxo.client

import kotlinx.coroutines.*
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.serialization.json.*
import okhttp3.*
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.IOException
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicLong
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException
import kotlin.math.min

/** Transport mode: JSON for debugging, BINARY for production. */
enum class TransportMode { JSON, BINARY }

/** API schema metadata for binary encoding. */
data class APISchemaEntry(
    val id: Int,
    val params: List<ParamSchema> = emptyList(),
)

data class ParamSchema(
    val fieldID: Int,
    val name: String,
    val type: String,
    /** True when the param is an array ([T]) — encoded as [count][items...]. */
    val isList: Boolean = false,
)

/**
 * Transport interface — single call() method, mode handled internally.
 *
 * JSON mode: returns parsed JsonElement (for kotlinx.serialization decode).
 * Binary mode: returns raw ByteArray (for generated decode{Model} functions).
 */
interface Transport {
    suspend fun call(api: String, params: Map<String, Any?> = emptyMap()): Any
    fun setSchema(schema: Map<String, APISchemaEntry>)
    fun setMode(mode: TransportMode)
    fun setToken(token: String)
}

/** OkHttp-based transport for Android / JVM. */
class OkHttpTransport(
    private val endpoint: String,
    token: String? = null,
    headers: Map<String, String> = emptyMap(),
    client: OkHttpClient? = null,
    mode: TransportMode = TransportMode.JSON,
    private val schema: Map<String, APISchemaEntry> = emptyMap(),
    /** Request timeout in seconds (default 30) */
    timeoutSeconds: Long = 30,
    /** Called on 401 — return new token to auto-retry, null to fail */
    private val onTokenExpired: (suspend () -> String?)? = null,
) : Transport {

    private val client: OkHttpClient = (client ?: OkHttpClient.Builder()
        .connectTimeout(timeoutSeconds, java.util.concurrent.TimeUnit.SECONDS)
        .readTimeout(timeoutSeconds, java.util.concurrent.TimeUnit.SECONDS)
        .writeTimeout(timeoutSeconds, java.util.concurrent.TimeUnit.SECONDS)
        .build())
    private val extraHeaders = ConcurrentHashMap<String, String>()
    @Volatile private var currentMode: TransportMode = mode
    private var currentSchema: Map<String, APISchemaEntry> = schema

    init {
        extraHeaders.putAll(headers)
        if (token != null) {
            extraHeaders["Authorization"] = "Bearer $token"
        }
    }

    /**
     * Call a Luxo API. Returns JsonElement (JSON mode) or ByteArray (binary mode).
     * Generated client code handles both transparently.
     */
    override fun setSchema(schema: Map<String, APISchemaEntry>) { currentSchema = schema }
    override fun setMode(mode: TransportMode) { currentMode = mode }
    override fun setToken(token: String) { extraHeaders["Authorization"] = "Bearer $token" }

    override suspend fun call(api: String, params: Map<String, Any?>): Any {
        return if (currentMode == TransportMode.BINARY) callBinary(api, params) else callJSON(api, params)
    }

    private suspend fun callJSON(api: String, params: Map<String, Any?>, isRetry: Boolean = false): JsonElement {
        val body = buildJsonObject {
            put("\$api", JsonPrimitive(api))
            for ((k, v) in params) {
                when (v) {
                    is Number -> put(k, JsonPrimitive(v))
                    is String -> put(k, JsonPrimitive(v))
                    is Boolean -> put(k, JsonPrimitive(v))
                    null -> put(k, JsonNull)
                }
            }
        }

        val request = Request.Builder()
            .url(endpoint)
            .post(body.toString().toRequestBody(JSON_MEDIA_TYPE))
            .apply {
                header("Content-Type", "application/json")
                for ((k, v) in extraHeaders) header(k, v)
            }
            .build()

        val response = client.awaitFullResponse(request)

        // 401 auto-refresh: invoke callback, retry once with new token
        if (response.code == 401 && !isRetry && onTokenExpired != null) {
            val newToken = onTokenExpired.invoke()
            if (newToken != null) {
                setToken(newToken)
                return callJSON(api, params, isRetry = true)
            }
        }

        val responseBody = response.body

        val json = try {
            Json.parseToJsonElement(responseBody).jsonObject
        } catch (e: Exception) {
            throw LuxoError("ParseError", 0, "invalid JSON response: ${e.message}")
        }

        val error = json["error"]
        if (error != null && error is JsonPrimitive) {
            throw LuxoError(
                error = error.content,
                code = json["code"]?.jsonPrimitive?.int ?: 0,
                message = json["message"]?.jsonPrimitive?.content ?: "",
                traceId = json["traceId"]?.jsonPrimitive?.contentOrNull,
            )
        }

        return json["data"] ?: JsonNull
    }

    private suspend fun callBinary(api: String, params: Map<String, Any?>, isRetry: Boolean = false): ByteArray {
        val meta = currentSchema[api]
            ?: throw LuxoError("ConfigError", 0, "no schema for API \"$api\" — binary mode requires schema")

        val enc = LuxoEncoder()
        enc.writeVarint(meta.id.toLong())
        enc.writeVarint(0) // field mask = 0 (SELECT *)

        for (pm in meta.params) {
            val v = params[pm.name] ?: continue
            if (pm.isList) {
                encodeListParam(enc, pm, v)
            } else {
                encodeScalarParam(enc, pm, v)
            }
        }
        enc.writeEnd()

        val request = Request.Builder()
            .url(endpoint)
            .post(enc.bytes().toRequestBody(BINARY_MEDIA_TYPE))
            .apply {
                header("Content-Type", "application/x-luxo")
                header("X-Luxo-Mode", "binary")
                for ((k, v) in extraHeaders) header(k, v)
            }
            .build()

        val response = client.awaitFullBinaryResponse(request)

        // 401 auto-refresh: invoke callback, retry once with new token
        if (response.code == 401 && !isRetry && onTokenExpired != null) {
            val newToken = onTokenExpired.invoke()
            if (newToken != null) {
                setToken(newToken)
                return callBinary(api, params, isRetry = true)
            }
        }

        return response.body
    }

    /** Encode a single scalar param to binary using its declared type. */
    private fun encodeScalarParam(enc: LuxoEncoder, pm: ParamSchema, v: Any) {
        when (pm.type) {
            "Int", "Duration" -> enc.writeFieldInt(pm.fieldID, (v as Number).toLong())
            "Float" -> enc.writeFieldFloat(pm.fieldID, (v as Number).toDouble())
            "UUID" -> enc.writeFieldUuid(pm.fieldID, v as String)
            // Per protocol: DateTime = svarint(unix seconds). Accept ISO string or number.
            "DateTime" -> {
                val sec = when (v) {
                    is Long -> v
                    is Number -> v.toLong()
                    is String -> java.time.Instant.parse(v).epochSecond
                    else -> throw IllegalArgumentException("invalid DateTime param: ${v::class}")
                }
                enc.writeFieldInt(pm.fieldID, sec)
            }
            "String", "Enum", "Decimal" -> enc.writeFieldString(pm.fieldID, v as String)
            "Boolean" -> enc.writeFieldBool(pm.fieldID, v as Boolean)
        }
    }

    /** Encode a list param ([count][items...]) to binary using its element type. */
    private fun encodeListParam(enc: LuxoEncoder, pm: ParamSchema, v: Any) {
        val items = v as List<*>
        when (pm.type) {
            "Int", "Duration" ->
                enc.writeFieldArray(pm.fieldID, items) { enc.writeSvarint((it as Number).toLong()) }
            "Float" ->
                enc.writeFieldArray(pm.fieldID, items) { enc.writeFixed64((it as Number).toDouble()) }
            "UUID" ->
                enc.writeFieldArray(pm.fieldID, items) { enc.writeUuid(it as String) }
            "DateTime" ->
                enc.writeFieldArray(pm.fieldID, items) {
                    val sec = when (it) {
                        is Long -> it
                        is Number -> it.toLong()
                        is String -> java.time.Instant.parse(it).epochSecond
                        else -> throw IllegalArgumentException("invalid DateTime param: ${v::class}")
                    }
                    enc.writeSvarint(sec)
                }
            "String", "Enum", "Decimal" ->
                enc.writeFieldArray(pm.fieldID, items) { enc.writeString(it as String) }
            "Boolean" ->
                enc.writeFieldArray(pm.fieldID, items) { enc.writeBool(it as Boolean) }
        }
    }


    companion object {
        private val JSON_MEDIA_TYPE = "application/json; charset=utf-8".toMediaType()
        private val BINARY_MEDIA_TYPE = "application/x-luxo".toMediaType()
    }
}

// MARK: - WebSocket Transport

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
class LuxoWebSocket(
    private val url: String,
    private val client: OkHttpClient = OkHttpClient(),
    token: String? = null,
    private val autoReconnect: Boolean = true,
    /** Max reconnect delay in milliseconds */
    private val maxReconnectDelayMs: Long = 30_000L,
) : Transport {

    private var ws: WebSocket? = null
    private val handlers = ConcurrentHashMap<String, (JsonElement) -> Unit>()
    private val pendingCalls = ConcurrentHashMap<String, CompletableDeferred<Any>>()
    private val requestIdCounter = AtomicLong(0)
    @Volatile private var currentToken: String? = token
    @Volatile private var currentMode: TransportMode = TransportMode.JSON
    @Volatile private var currentSchema: Map<String, APISchemaEntry> = emptyMap()
    @Volatile private var connected: Boolean = false
    @Volatile private var closed: Boolean = false
    private var reconnectAttempt: Int = 0
    private var reconnectJob: Job? = null
    private val scope = CoroutineScope(Dispatchers.IO + SupervisorJob())

    override fun setSchema(schema: Map<String, APISchemaEntry>) { currentSchema = schema }
    override fun setMode(mode: TransportMode) { currentMode = mode }
    override fun setToken(token: String) { currentToken = token }

    override suspend fun call(api: String, params: Map<String, Any?>): Any {
        if (!connected) throw LuxoError("ConnectionError", 0, "WebSocket not connected")

        val reqId = "req_${requestIdCounter.incrementAndGet()}"
        val deferred = CompletableDeferred<Any>()
        pendingCalls[reqId] = deferred

        val msg = buildJsonObject {
            put("type", JsonPrimitive("call"))
            put("id", JsonPrimitive(reqId))
            put("\$api", JsonPrimitive(api))
            put("params", buildJsonObject {
                for ((k, v) in params) {
                    when (v) {
                        is Number -> put(k, JsonPrimitive(v))
                        is String -> put(k, JsonPrimitive(v))
                        is Boolean -> put(k, JsonPrimitive(v))
                        null -> put(k, JsonNull)
                    }
                }
            })
        }

        val sent = ws?.send(msg.toString()) ?: false
        if (!sent) {
            pendingCalls.remove(reqId)
            throw LuxoError("ConnectionError", 0, "failed to send WebSocket message")
        }

        return try {
            deferred.await()
        } finally {
            pendingCalls.remove(reqId)
        }
    }

    fun connect() {
        closed = false
        doConnect()
    }

    private fun doConnect() {
        val request = Request.Builder()
            .url(url)
            .apply { currentToken?.let { header("Authorization", "Bearer $it") } }
            .build()

        ws = client.newWebSocket(request, object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                connected = true
                reconnectAttempt = 0
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
                try {
                    val json = Json.parseToJsonElement(text).jsonObject

                    // Handle RPC response
                    val id = json["id"]?.jsonPrimitive?.content
                    if (id != null && pendingCalls.containsKey(id)) {
                        val deferred = pendingCalls.remove(id) ?: return
                        val error = json["error"]
                        if (error != null && error is JsonPrimitive) {
                            deferred.completeExceptionally(LuxoError(
                                error = error.content,
                                code = json["code"]?.jsonPrimitive?.int ?: 0,
                                message = json["message"]?.jsonPrimitive?.content ?: "",
                                traceId = json["traceId"]?.jsonPrimitive?.contentOrNull,
                            ))
                        } else {
                            deferred.complete(json["data"] ?: JsonNull)
                        }
                        return
                    }

                    // Handle subscription push
                    val api = json["api"]?.jsonPrimitive?.content
                    if (api != null) {
                        val data = json["data"] ?: return
                        handlers[api]?.invoke(data)
                    }
                } catch (_: Exception) {}
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                connected = false
                // Fail all pending calls
                val error = LuxoError("ConnectionError", 0, "WebSocket connection lost: ${t.message}")
                for (deferred in pendingCalls.values) {
                    deferred.completeExceptionally(error)
                }
                pendingCalls.clear()

                // Auto-reconnect with exponential backoff
                if (autoReconnect && !closed) {
                    scheduleReconnect()
                }
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                connected = false
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
                doConnect()
            }
        }
    }

    fun subscribe(api: String, params: Map<String, Any?> = emptyMap(), handler: (JsonElement) -> Unit) {
        handlers[api] = handler
        val msg = buildJsonObject {
            put("type", JsonPrimitive("subscribe"))
            put("api", JsonPrimitive(api))
            put("params", buildJsonObject {
                for ((k, v) in params) {
                    when (v) {
                        is Number -> put(k, JsonPrimitive(v))
                        is String -> put(k, JsonPrimitive(v))
                        is Boolean -> put(k, JsonPrimitive(v))
                        null -> put(k, JsonNull)
                    }
                }
            })
        }
        ws?.send(msg.toString())
    }

    fun unsubscribe(api: String) {
        handlers.remove(api)
        val msg = buildJsonObject {
            put("type", JsonPrimitive("unsubscribe"))
            put("api", JsonPrimitive(api))
        }
        ws?.send(msg.toString())
    }

    fun close() {
        closed = true
        reconnectJob?.cancel()
        ws?.close(1000, "client close")
        ws = null
        connected = false
        handlers.clear()
        // Fail any pending calls
        val error = LuxoError("ConnectionError", 0, "WebSocket closed by client")
        for (deferred in pendingCalls.values) {
            deferred.completeExceptionally(error)
        }
        pendingCalls.clear()
        scope.cancel()
    }
}

/** Internal response wrapper carrying HTTP status code. */
private data class StringResponse(val code: Int, val body: String)
private data class BinaryResponse(val code: Int, val body: ByteArray)

private suspend fun OkHttpClient.awaitFullResponse(request: Request): StringResponse =
    suspendCancellableCoroutine { cont ->
        val call = newCall(request)
        cont.invokeOnCancellation { call.cancel() }
        call.enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) {
                cont.resumeWithException(LuxoError("NetworkError", 0, e.message ?: "network error"))
            }
            override fun onResponse(call: Call, response: Response) {
                response.use { resp ->
                    cont.resume(StringResponse(resp.code, resp.body?.string() ?: "{}"))
                }
            }
        })
    }

private suspend fun OkHttpClient.awaitFullBinaryResponse(request: Request): BinaryResponse =
    suspendCancellableCoroutine { cont ->
        val call = newCall(request)
        cont.invokeOnCancellation { call.cancel() }
        call.enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) {
                cont.resumeWithException(LuxoError("NetworkError", 0, e.message ?: "network error"))
            }
            override fun onResponse(call: Call, response: Response) {
                response.use { resp ->
                    if (!resp.isSuccessful && resp.code != 401) {
                        val body = resp.body?.string() ?: ""
                        try {
                            val json = Json.parseToJsonElement(body).jsonObject
                            cont.resumeWithException(LuxoError(
                                json["error"]?.jsonPrimitive?.content ?: "Error",
                                json["code"]?.jsonPrimitive?.int ?: resp.code,
                                json["message"]?.jsonPrimitive?.content ?: "",
                                json["traceId"]?.jsonPrimitive?.contentOrNull,
                            ))
                        } catch (_: Exception) {
                            cont.resumeWithException(LuxoError("Error", resp.code, "HTTP ${resp.code}"))
                        }
                        return
                    }
                    cont.resume(BinaryResponse(resp.code, resp.body?.bytes() ?: ByteArray(0)))
                }
            }
        })
    }
