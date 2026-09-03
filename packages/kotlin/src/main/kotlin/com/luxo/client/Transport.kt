package com.luxo.client

import kotlinx.coroutines.*
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.serialization.json.*
import okhttp3.*
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import okio.ByteString
import java.io.IOException
import java.util.Base64
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicLong
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException
import kotlin.math.min

/** Transport mode: JSON for debugging, BINARY for production. */
enum class TransportMode { JSON, BINARY }

private const val BINARY_FILTERS_FIELD_ID = 0x7ffffffe
private const val BINARY_SORTERS_FIELD_ID = 0x7fffffff
private val filterOperatorIDs = mapOf(
    "eq" to 1,
    "ne" to 2,
    "gt" to 3,
    "gte" to 4,
    "lt" to 5,
    "lte" to 6,
    "contains" to 7,
    "startswith" to 8,
    "endswith" to 9,
    "match" to 10,
)

data class LuxoFilter(val field: String, val op: String, val value: Any)
data class LuxoSorter(val field: String, val order: String)

/** API schema metadata for binary encoding. */
data class APISchemaEntry(
    val id: Int,
    val params: List<ParamSchema> = emptyList(),
    val fields: Map<String, SelectionFieldSchema> = emptyMap(),
    val types: Map<String, Map<String, SelectionFieldSchema>> = emptyMap(),
)

data class SelectionFieldSchema(
    val fieldID: Int,
    val typeName: String? = null,
)

data class ParamSchema(
    val fieldID: Int,
    val name: String,
    val type: String,
    /** True when the param is an array ([T]) — encoded as [count][items...]. */
    val isList: Boolean = false,
    val nullable: Boolean = false,
)

private data class SelectedField(val name: String, val children: List<SelectedField>? = null)

private class SelectionParser(private val input: String) {
    private var offset = 0

    fun parse(): List<SelectedField> {
        val fields = parseList(nested = false, depth = 0)
        skipSpaces()
        if (offset != input.length) fail("unexpected '${input[offset]}'")
        return fields
    }

    private fun parseList(nested: Boolean, depth: Int): List<SelectedField> {
        if (depth >= 32) fail("selection depth exceeds 32")
        val fields = mutableListOf<SelectedField>()
        val names = mutableSetOf<String>()
        while (true) {
            skipSpaces()
            if (offset >= input.length || (nested && input[offset] == '}')) break
            val name = readIdentifier()
            if (name.isEmpty()) fail("expected field name")
            if (!names.add(name)) fail("duplicate field '$name'")
            skipSpaces()
            val children = if (offset < input.length && input[offset] == '{') parseChildren(name, depth) else null
            fields += SelectedField(name, children)
            skipSpaces()
            if (offset >= input.length || input[offset] != ',') break
            offset++
        }
        return fields
    }

    private fun parseChildren(name: String, depth: Int): List<SelectedField> {
        offset++
        val children = parseList(nested = true, depth = depth + 1)
        if (children.isEmpty()) fail("empty selection for '$name'")
        skipSpaces()
        if (offset >= input.length || input[offset] != '}') fail("missing '}' for '$name'")
        offset++
        return children
    }

    private fun readIdentifier(): String {
        val start = offset
        if (offset >= input.length || !input[offset].isIdentifierStart()) return ""
        offset++
        while (offset < input.length && input[offset].isIdentifierPart()) offset++
        return input.substring(start, offset)
    }

    private fun skipSpaces() {
        while (offset < input.length && input[offset].isWhitespace()) offset++
    }

    private fun fail(message: String): Nothing =
        throw LuxoError("ConfigError", 0, "$message at position $offset")
}

private fun Char.isIdentifierStart(): Boolean = this == '_' || this in 'A'..'Z' || this in 'a'..'z'
private fun Char.isIdentifierPart(): Boolean = isIdentifierStart() || this in '0'..'9'

/** Convert supported Kotlin values to their lossless JSON representation. */
internal fun luxoJsonValue(value: Any?): JsonElement = when (value) {
    null -> JsonNull
    is JsonElement -> value
    is String -> JsonPrimitive(value)
    is Boolean -> JsonPrimitive(value)
    is Number -> JsonPrimitive(value)
    is ByteArray -> JsonPrimitive(Base64.getEncoder().encodeToString(value))
    is Map<*, *> -> buildJsonObject {
        for ((key, item) in value) {
            require(key is String) { "JSON object keys must be strings" }
            put(key, luxoJsonValue(item))
        }
    }
    is Iterable<*> -> buildJsonArray { value.forEach { add(luxoJsonValue(it)) } }
    is Array<*> -> buildJsonArray { value.forEach { add(luxoJsonValue(it)) } }
    else -> throw LuxoError("ConfigError", 0, "unsupported JSON value: ${value::class.qualifiedName}")
}

/** Canonical Luxo binary request, error, and WebSocket frame codec. */
internal object LuxoBinaryProtocol {
    const val CALL_REQUEST = 0x01
    const val CALL_SUCCESS = 0x02
    const val CALL_ERROR = 0x03
    const val SUBSCRIBE = 0x04
    const val UNSUBSCRIBE = 0x05
    const val STREAM = 0x06
    const val SUBSCRIBE_SUCCESS = 0x07
    const val SUBSCRIBE_ERROR = 0x08

    fun encodeRequest(meta: APISchemaEntry, params: Map<String, Any?>): ByteArray {
        val enc = LuxoEncoder()
        enc.writeVarint(meta.id.toLong())
        writeFieldMask(enc, meta, params["\$select"] as? String)
        for (param in meta.params) {
            if (!params.containsKey(param.name)) continue
            val value = params[param.name]
            enc.writeVarint(param.fieldID.toLong())
            if (param.nullable) {
                if (value == null) {
                    enc.writeBool(false)
                    continue
                }
                enc.writeBool(true)
            } else if (value == null) {
                throw LuxoError("ConfigError", 0, "parameter ${param.name} is not nullable")
            }
            if (param.isList) encodeListParam(enc, param, value)
            else encodeScalarParam(enc, param, value)
        }
        params["\$filters"]?.let { encodeFilters(enc, it) }
        params["\$sorters"]?.let { encodeSorters(enc, it) }
        enc.writeEnd()
        return enc.bytes()
    }

    fun callFrame(sequence: Long, body: ByteArray): ByteArray = frame(CALL_REQUEST, sequence, body)

    fun subscribeFrame(body: ByteArray): ByteArray = frame(SUBSCRIBE, null, body)

    fun unsubscribeFrame(apiID: Int): ByteArray = frame(UNSUBSCRIBE, apiID.toLong(), ByteArray(0))

    private fun frame(type: Int, id: Long?, payload: ByteArray): ByteArray {
        val enc = LuxoEncoder()
        enc.writeVarint(type.toLong())
        if (id != null) enc.writeVarint(id)
        enc.writeRawBytes(payload)
        return enc.bytes()
    }

    fun decodeError(body: ByteArray, statusCode: Int): LuxoError {
        return try {
            val dec = LuxoDecoder(body)
            var code = statusCode
            var name = "Error"
            var message = "HTTP $statusCode"
            var traceId: String? = null
            var data: JsonElement? = null
            var cause: String? = null
            var seen = 0
            var ended = false
            while (dec.remaining() > 0) {
                if (!dec.nextField()) {
                    ended = true
                    break
                }
                when (dec.fieldID) {
                    1 -> { code = dec.readInt().toInt(); seen = seen or 1 }
                    2 -> { name = dec.readString(); seen = seen or 2 }
                    3 -> { message = dec.readString(); seen = seen or 4 }
                    4 -> traceId = dec.readString()
                    5 -> data = Json.parseToJsonElement(dec.readBytes().toString(Charsets.UTF_8))
                    6 -> cause = dec.readString()
                    else -> return LuxoError(
                        "ParseError",
                        statusCode,
                        "unknown binary error field ${dec.fieldID}",
                    )
                }
            }
            if (!ended) return LuxoError("ParseError", statusCode, "invalid binary error response: missing end marker")
            if (dec.remaining() != 0) {
                return LuxoError("ParseError", statusCode, "invalid binary error response: trailing bytes")
            }
            if (seen != 7) {
                return LuxoError("ParseError", statusCode, "invalid binary error response: missing required fields")
            }
            LuxoError(name, code, message, traceId, data, cause)
        } catch (error: Exception) {
            LuxoError("ParseError", statusCode, "invalid binary error response: ${error.message}")
        }
    }

    private fun writeFieldMask(enc: LuxoEncoder, meta: APISchemaEntry, selection: String?) {
        if (selection.isNullOrBlank() || meta.fields.isEmpty()) {
            enc.writeVarint(0)
            return
        }
        val mask = encodeSelectionNode(SelectionParser(selection).parse(), meta.fields, meta.types)
        enc.writeVarint(mask.size.toLong())
        enc.writeRawBytes(mask)
    }

    private fun encodeSelectionNode(
        selected: List<SelectedField>,
        fields: Map<String, SelectionFieldSchema>,
        types: Map<String, Map<String, SelectionFieldSchema>>,
    ): ByteArray {
        var mask = ByteArray(0)
        val children = mutableListOf<Pair<Int, ByteArray>>()
        for (field in selected) {
            val meta = fields[field.name]
                ?: throw LuxoError("ConfigError", 0, "unknown selected field: ${field.name}")
            mask = FieldMask.set(mask, meta.fieldID)
            val nestedSelection = field.children ?: continue
            val nestedFields = meta.typeName?.let(types::get)
                ?: throw LuxoError("ConfigError", 0, "field ${field.name} does not support nested selection")
            children += meta.fieldID to encodeSelectionNode(nestedSelection, nestedFields, types)
        }
        val node = LuxoEncoder()
        node.writeVarint(mask.size.toLong())
        node.writeRawBytes(mask)
        for ((fieldID, child) in children.sortedBy { it.first }) {
            node.writeVarint(fieldID.toLong())
            node.writeVarint(child.size.toLong())
            node.writeRawBytes(child)
        }
        return node.bytes()
    }

    private fun encodeFilters(enc: LuxoEncoder, value: Any) {
        val filters = value as? List<*>
            ?: throw LuxoError("ConfigError", 0, "\$filters must be a list")
        if (filters.size > 1000) throw LuxoError("ConfigError", 0, "\$filters exceeds 1000 entries")
        enc.writeVarint(BINARY_FILTERS_FIELD_ID.toLong())
        enc.writeVarint(filters.size.toLong())
        for ((index, item) in filters.withIndex()) {
            val filter = filterParts(item)
                ?: throw LuxoError("ConfigError", 0, "invalid \$filters entry at index $index")
            val operatorID = filterOperatorIDs[filter.op]
                ?: throw LuxoError("ConfigError", 0, "invalid \$filters entry at index $index")
            if (filter.field.isEmpty() || !validFilterValue(filter.value)) {
                throw LuxoError("ConfigError", 0, "invalid \$filters entry at index $index")
            }
            enc.writeString(filter.field)
            enc.writeVarint(operatorID.toLong())
            enc.writeString(filterValueText(filter.value))
        }
    }

    private fun encodeSorters(enc: LuxoEncoder, value: Any) {
        val sorters = value as? List<*>
            ?: throw LuxoError("ConfigError", 0, "\$sorters must be a list")
        if (sorters.size > 100) throw LuxoError("ConfigError", 0, "\$sorters exceeds 100 entries")
        enc.writeVarint(BINARY_SORTERS_FIELD_ID.toLong())
        enc.writeVarint(sorters.size.toLong())
        for ((index, item) in sorters.withIndex()) {
            val sorter = sorterParts(item)
                ?: throw LuxoError("ConfigError", 0, "invalid \$sorters entry at index $index")
            if (sorter.field.isEmpty() || (sorter.order != "asc" && sorter.order != "desc")) {
                throw LuxoError("ConfigError", 0, "invalid \$sorters entry at index $index")
            }
            enc.writeString(sorter.field)
            enc.writeBool(sorter.order == "desc")
        }
    }

    private fun filterParts(value: Any?): LuxoFilter? = when (value) {
        is LuxoFilter -> value
        is Map<*, *> -> {
            val field = value["field"] as? String
            val op = value["op"] as? String
            val filterValue = value["value"]
            if (field == null || op == null || filterValue == null) null else LuxoFilter(field, op, filterValue)
        }
        else -> null
    }

    private fun sorterParts(value: Any?): LuxoSorter? = when (value) {
        is LuxoSorter -> value
        is Map<*, *> -> {
            val field = value["field"] as? String
            val order = value["order"] as? String
            if (field == null || order == null) null else LuxoSorter(field, order)
        }
        else -> null
    }

    private fun validFilterValue(value: Any): Boolean = when (value) {
        is String, is Boolean, is Byte, is Short, is Int, is Long -> true
        is Float -> value.isFinite()
        is Double -> value.isFinite()
        else -> false
    }

    private fun filterValueText(value: Any): String = when (value) {
        is Boolean -> if (value) "true" else "false"
        else -> value.toString()
    }

    private fun encodeScalarParam(enc: LuxoEncoder, param: ParamSchema, value: Any) {
        when (param.type) {
            "Int", "Duration" -> enc.writeSvarint((value as Number).toLong())
            "Float" -> enc.writeFixed64((value as Number).toDouble())
            "UUID" -> enc.writeUuid(value as String)
            "DateTime" -> enc.writeSvarint(unixSeconds(value))
            "String", "Enum", "Decimal" -> enc.writeString(value as String)
            "Boolean" -> enc.writeBool(value as Boolean)
            "Bytes" -> enc.writeBytes(value as ByteArray)
            "JSON" -> enc.writeBytes(luxoJsonValue(value).toString().toByteArray(Charsets.UTF_8))
            else -> throw LuxoError("ConfigError", 0, "unsupported binary param type: ${param.type}")
        }
    }

    private fun encodeListParam(enc: LuxoEncoder, param: ParamSchema, value: Any) {
        val items = value as? List<*>
            ?: throw LuxoError("ConfigError", 0, "parameter ${param.name} must be a list")
        enc.writeVarint(items.size.toLong())
        when (param.type) {
            "Int", "Duration" -> items.forEach { enc.writeSvarint((it as Number).toLong()) }
            "Float" -> items.forEach { enc.writeFixed64((it as Number).toDouble()) }
            "UUID" -> items.forEach { enc.writeUuid(it as String) }
            "DateTime" -> items.forEach { enc.writeSvarint(unixSeconds(it!!)) }
            "String", "Enum", "Decimal" -> items.forEach { enc.writeString(it as String) }
            "Boolean" -> items.forEach { enc.writeBool(it as Boolean) }
            "Bytes" -> items.forEach { enc.writeBytes(it as ByteArray) }
            "JSON" -> items.forEach {
                enc.writeBytes(luxoJsonValue(it).toString().toByteArray(Charsets.UTF_8))
            }
            else -> throw LuxoError("ConfigError", 0, "unsupported binary list param type: ${param.type}")
        }
    }

    private fun unixSeconds(value: Any): Long = when (value) {
        is String -> java.time.Instant.parse(value).epochSecond
        else -> throw LuxoError("ConfigError", 0, "invalid DateTime parameter: ${value::class.qualifiedName}")
    }
}

/**
 * Transport interface — single call() method, mode handled internally.
 *
 * JSON mode: returns parsed JsonElement (for kotlinx.serialization decode).
 * Binary mode: returns raw ByteArray (for generated decode{Model} functions).
 */
interface Transport {
    suspend fun call(api: String, params: Map<String, Any?> = emptyMap()): Any
    suspend fun subscribe(api: String, params: Map<String, Any?> = emptyMap(), handler: (Any) -> Unit): () -> Unit =
        throw LuxoError("ConfigError", 0, "subscriptions require a WebSocket endpoint")
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
                put(k, luxoJsonValue(v))
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
                data = json["data"],
                developmentCause = json["cause"]?.jsonPrimitive?.contentOrNull,
            )
        }

        return json["data"] ?: JsonNull
    }

    private suspend fun callBinary(api: String, params: Map<String, Any?>, isRetry: Boolean = false): ByteArray {
        val meta = currentSchema[api]
            ?: throw LuxoError("ConfigError", 0, "no schema for API \"$api\" — binary mode requires schema")

        val body = LuxoBinaryProtocol.encodeRequest(meta, params)

        val request = Request.Builder()
            .url(endpoint)
            .post(body.toRequestBody(BINARY_MEDIA_TYPE))
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

        if (response.code !in 200..299) {
            throw LuxoBinaryProtocol.decodeError(response.body, response.code)
        }
        return response.body
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
    private val timeoutMillis: Long = 30_000L,
) : Transport {

    @Volatile private var ws: WebSocket? = null
    private data class Subscription(
        val params: Map<String, Any?>,
        val handler: (Any) -> Unit,
        var acknowledgement: CompletableDeferred<() -> Unit>?,
    )

    private val subscriptions = ConcurrentHashMap<String, Subscription>()
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

    override fun setSchema(schema: Map<String, APISchemaEntry>) { currentSchema = schema }
    override fun setMode(mode: TransportMode) { currentMode = mode }
    override fun setToken(token: String) { currentToken = token }

    override suspend fun call(api: String, params: Map<String, Any?>): Any {
        awaitConnection()

        val requestID = requestIdCounter.incrementAndGet()
        val message: Any = if (currentMode == TransportMode.BINARY) {
            val meta = currentSchema[api]
                ?: throw LuxoError("ConfigError", 0, "no schema for API \"$api\" — binary mode requires schema")
            LuxoBinaryProtocol.callFrame(requestID, LuxoBinaryProtocol.encodeRequest(meta, params))
        } else {
            buildJsonObject {
                put("\$id", requestID)
                put("\$api", api)
                for ((key, value) in params) put(key, luxoJsonValue(value))
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
            val subscription = json["\$sub"]?.jsonPrimitive?.contentOrNull
            if (subscription != null) {
                val errorName = json["error"]?.jsonPrimitive?.contentOrNull
                acknowledgeSubscription(
                    subscription,
                    errorName?.let {
                        LuxoError(
                            error = it,
                            code = json["code"]?.jsonPrimitive?.int ?: 0,
                            message = json["message"]?.jsonPrimitive?.content ?: "",
                            traceId = json["traceId"]?.jsonPrimitive?.contentOrNull,
                            data = json["data"],
                            developmentCause = json["cause"]?.jsonPrimitive?.contentOrNull,
                        )
                    },
                )
                return
            }
            val stream = json["\$stream"]?.jsonPrimitive?.contentOrNull
            if (stream != null) {
                subscriptions[stream]?.handler?.invoke(json["data"] ?: JsonNull)
                return
            }
            val requestID = json["\$id"]?.jsonPrimitive?.longOrNull ?: return
            val deferred = pendingCalls.remove(requestID) ?: return
            val error = json["error"]?.jsonPrimitive?.contentOrNull
            if (error != null) {
                deferred.completeExceptionally(
                    LuxoError(
                        error = error,
                        code = json["code"]?.jsonPrimitive?.int ?: 0,
                        message = json["message"]?.jsonPrimitive?.content ?: "",
                        traceId = json["traceId"]?.jsonPrimitive?.contentOrNull,
                        data = json["data"],
                        developmentCause = json["cause"]?.jsonPrimitive?.contentOrNull,
                    ),
                )
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
            val frameType = dec.readVarint().toInt()
            val id = dec.readVarint()
            val payload = dec.readRemainingBytes()
            when (frameType) {
                LuxoBinaryProtocol.SUBSCRIBE_SUCCESS,
                LuxoBinaryProtocol.SUBSCRIBE_ERROR -> {
                    val api = currentSchema.entries.firstOrNull { it.value.id.toLong() == id }?.key
                    if (api != null) {
                        acknowledgeSubscription(
                            api,
                            if (frameType == LuxoBinaryProtocol.SUBSCRIBE_ERROR) {
                                LuxoBinaryProtocol.decodeError(payload, 0)
                            } else {
                                null
                            },
                        )
                    }
                }
                LuxoBinaryProtocol.CALL_SUCCESS -> pendingCalls.remove(id)?.complete(payload)
                LuxoBinaryProtocol.CALL_ERROR -> pendingCalls.remove(id)?.completeExceptionally(
                    LuxoBinaryProtocol.decodeError(payload, 0),
                )
                LuxoBinaryProtocol.STREAM -> {
                    val api = currentSchema.entries.firstOrNull { it.value.id.toLong() == id }?.key
                    if (api != null) subscriptions[api]?.handler?.invoke(payload)
                }
            }
        } catch (_: Exception) {
            // Ignore malformed or unrelated frames. They cannot be correlated safely.
        }
    }

    override suspend fun subscribe(api: String, params: Map<String, Any?>, handler: (Any) -> Unit): () -> Unit {
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
            val frame = LuxoBinaryProtocol.subscribeFrame(LuxoBinaryProtocol.encodeRequest(meta, params))
            return ws?.send(ByteString.of(*frame)) == true
        }
        val message = buildJsonObject {
            put("\$sub", api)
            for ((key, value) in params) put(key, luxoJsonValue(value))
        }
        return ws?.send(message.toString()) == true
    }

    fun unsubscribe(api: String) {
        if (subscriptions.remove(api) == null || !connected) return
        if (currentMode == TransportMode.BINARY) {
            val meta = currentSchema[api] ?: return
            ws?.send(ByteString.of(*LuxoBinaryProtocol.unsubscribeFrame(meta.id)))
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
                    cont.resume(BinaryResponse(resp.code, resp.body?.bytes() ?: ByteArray(0)))
                }
            }
        })
    }
