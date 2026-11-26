package com.luxo.client

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.withContext
import kotlinx.coroutines.launch
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.serialization.json.*
import okhttp3.*
import okhttp3.MediaType.Companion.toMediaType
import java.io.IOException
import java.util.Base64
import java.util.concurrent.TimeUnit
import kotlin.coroutines.CoroutineContext
import kotlin.coroutines.EmptyCoroutineContext
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException
import kotlin.time.Duration.Companion.milliseconds
import kotlin.time.Duration.Companion.nanoseconds

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

/** API schema metadata for binary encoding and typed JSON parameters. */
data class APISchemaEntry(
    val id: Int,
    val params: List<ParamSchema> = emptyList(),
    val fields: Map<String, SelectionFieldSchema> = emptyMap(),
    val types: Map<String, Map<String, SelectionFieldSchema>> = emptyMap(),
    val responseType: SelectionFieldSchema? = null,
) {
    /** Retain independently compiled clients' original constructor and default bridge. */
    internal constructor(id: Int, params: List<ParamSchema> = emptyList(),
        fields: Map<String, SelectionFieldSchema> = emptyMap(),
        types: Map<String, Map<String, SelectionFieldSchema>> = emptyMap()) : this(id, params, fields, types, null)

    /** The JVM name also preserves the old compiler-generated copy$default entry. */
    @JvmName("copy")
    internal fun legacyCopy(id: Int = this.id, params: List<ParamSchema> = this.params,
        fields: Map<String, SelectionFieldSchema> = this.fields,
        types: Map<String, Map<String, SelectionFieldSchema>> = this.types): APISchemaEntry =
        APISchemaEntry(id, params, fields, types, responseType)

    // Built once with the schema, not on the request path.
    internal val paramsByName = params.associateBy { it.name }
    internal var selectionSnapshot = false
}

data class SelectionFieldSchema(
    val fieldID: Int,
    val typeName: String? = null,
	val type: String? = null,
	val isList: Boolean = false,
	val nullable: Boolean = false,
)

data class ParamSchema(
    val fieldID: Int,
    val name: String,
    val type: String,
    /** True when the param is an array ([T]) — encoded as [count][items...]. */
    val isList: Boolean = false,
    val nullable: Boolean = false,
	val typeName: String? = null,
)

/** Convert supported Kotlin values to their lossless JSON representation. */
internal fun luxoJsonValue(value: Any?): JsonElement = luxoJsonValue(value, 0, Int.MAX_VALUE)

internal fun luxoGenericJSONValue(value: Any?): JsonElement = luxoJsonValue(value, 0, 128)

private fun luxoJsonValue(value: Any?, depth: Int, maxDepth: Int): JsonElement = when (value) {
    null -> JsonNull
    is JsonElement -> value.also { if (maxDepth == 128) requireJSONValueUnicode(it, depth) }
    is String -> JsonPrimitive(value)
    is Boolean -> JsonPrimitive(value)
    is Number -> JsonPrimitive(value)
    is ByteArray -> JsonPrimitive(Base64.getEncoder().encodeToString(value))
    is Map<*, *> -> buildJsonObject {
        requireJSONContainerBudget(maxDepth - depth)
        for ((key, item) in value) {
            require(key is String) { "JSON object keys must be strings" }
            put(key, luxoJsonValue(item, depth + 1, maxDepth))
        }
    }
    is Iterable<*> -> buildJsonArray {
        requireJSONContainerBudget(maxDepth - depth)
        value.forEach { add(luxoJsonValue(it, depth + 1, maxDepth)) }
    }
    is Array<*> -> buildJsonArray {
        requireJSONContainerBudget(maxDepth - depth)
        value.forEach { add(luxoJsonValue(it, depth + 1, maxDepth)) }
    }
	is LuxoBinaryEncodable -> luxoJsonValue(value.toLuxoJson(), depth, maxDepth)
    else -> throw LuxoError("ConfigError", 0, "unsupported JSON value: ${value::class.qualifiedName}")
}

/** Canonical Luxo binary request, error, and WebSocket frame codec. */
internal object LuxoBinaryProtocol {
    fun missingResponse(statusCode: Int = 0) = LuxoError("ParseError", statusCode, "missing binary response data")

    const val CALL_REQUEST = 0x01
    const val CALL_SUCCESS = 0x02
    const val CALL_ERROR = 0x03
    const val SUBSCRIBE = 0x10
    const val UNSUBSCRIBE = 0x11
    const val STREAM = 0x12
    const val SUBSCRIBE_SUCCESS = 0x13
    const val SUBSCRIBE_ERROR = 0x14
    const val STREAM_COMPLETE = 0x15
    const val STREAM_ERROR = 0x16

    fun encodeRequest(meta: APISchemaEntry, params: Map<String, Any?>): ByteArray {
        val enc = LuxoEncoder()
        enc.writeVarint(meta.id.toLong())
        writeFieldMask(enc, meta, params["\$select"] as? String)
        for (param in meta.params) {
            if (!params.containsKey(param.name)) continue
            val value = params[param.name]
            enc.writeVarint(param.fieldID.toLong())
            if (param.nullable) {
                if (value == null || (param.type == "JSON" && !param.isList && value === JsonNull)) {
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

    fun encodeRequest(meta: APISchemaEntry, request: GeneratedRequest): ByteArray {
        if (meta.id != request.apiID) throw LuxoError("ConfigError", 0, "generated request API ID differs from registered schema")
        val enc = LuxoEncoder()
        enc.writeVarint(request.apiID.toLong())
        writeFieldMask(enc, meta, request.selection, request.selectionPlan)
        request.writeParameters(enc)
        request.filters?.let { encodeTypedFilters(enc, it) }
        request.sorters?.let { encodeTypedSorters(enc, it) }
        enc.writeEnd()
        return enc.bytes()
    }

    fun callFrame(sequence: Long, body: ByteArray): ByteArray = frame(CALL_REQUEST, sequence, body)

    fun subscribeFrame(sequence: Long, body: ByteArray): ByteArray = frame(SUBSCRIBE, sequence, body)

    fun unsubscribeFrame(sequence: Long): ByteArray = frame(UNSUBSCRIBE, sequence, ByteArray(0))

    private fun frame(type: Int, id: Long, payload: ByteArray): ByteArray {
        val enc = LuxoEncoder()
        enc.writeVarint(type.toLong())
        enc.writeVarint(id)
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
                val field = dec.readVarint()
                if (field == 0L) {
                    ended = true
                    break
                }
                require(field in 1L..6L) { "unknown binary error field $field" }
                val bit = 1 shl (field.toInt() - 1)
                require(seen and bit == 0) { "duplicate binary error field $field" }
                seen = seen or bit
                when (field.toInt()) {
                    1 -> {
                        val value = dec.readInt()
                        require(value in 400L..599L) { "invalid error status" }
                        code = value.toInt()
                    }
                    2 -> name = dec.readString()
                    3 -> message = dec.readString()
                    4 -> traceId = dec.readString()
                    5 -> data = decodeBoundedLuxoJSON(dec.readString())
                    6 -> cause = dec.readString()
                }
            }
            require(ended) { "missing end marker" }
            require(dec.remaining() == 0) { "trailing bytes" }
            require(seen and 7 == 7) { "missing required fields" }
            LuxoError(name, code, message, traceId, data, cause)
        } catch (error: Exception) {
            LuxoError("ParseError", statusCode, "invalid binary error response: ${error.message}")
        }
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
            writeFilter(enc, filter, index)
        }
    }

    private fun encodeTypedFilters(enc: LuxoEncoder, filters: List<LuxoFilter>) {
        if (filters.size > 1000) throw LuxoError("ConfigError", 0, "\$filters exceeds 1000 entries")
        enc.writeVarint(BINARY_FILTERS_FIELD_ID.toLong())
        enc.writeVarint(filters.size.toLong())
        for ((index, filter) in filters.withIndex()) writeFilter(enc, filter, index)
    }

    private fun writeFilter(enc: LuxoEncoder, filter: LuxoFilter, index: Int) {
        val operatorID = filterOperatorIDs[filter.op]
            ?: throw LuxoError("ConfigError", 0, "invalid \$filters entry at index $index")
        if (filter.field.isEmpty() || !validFilterValue(filter.value)) {
            throw LuxoError("ConfigError", 0, "invalid \$filters entry at index $index")
        }
        enc.writeString(filter.field)
        enc.writeVarint(operatorID.toLong())
        enc.writeString(filterValueText(filter.value))
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
            writeSorter(enc, sorter, index)
        }
    }

    private fun encodeTypedSorters(enc: LuxoEncoder, sorters: List<LuxoSorter>) {
        if (sorters.size > 100) throw LuxoError("ConfigError", 0, "\$sorters exceeds 100 entries")
        enc.writeVarint(BINARY_SORTERS_FIELD_ID.toLong())
        enc.writeVarint(sorters.size.toLong())
        for ((index, sorter) in sorters.withIndex()) writeSorter(enc, sorter, index)
    }

    private fun writeSorter(enc: LuxoEncoder, sorter: LuxoSorter, index: Int) {
        if (sorter.field.isEmpty() || (sorter.order != "asc" && sorter.order != "desc")) {
            throw LuxoError("ConfigError", 0, "invalid \$sorters entry at index $index")
        }
        enc.writeString(sorter.field)
        enc.writeBool(sorter.order == "desc")
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
            "Int", "Duration" -> enc.writeSvarint(requireLuxoInt64(value))
            "Float" -> enc.writeFixed64((value as Number).toDouble())
            "UUID" -> enc.writeUuid(value as String)
            "DateTime" -> writeDateTimeParam(enc, value)
            "String", "Enum", "Decimal" -> enc.writeString(value as String)
            "Boolean" -> enc.writeBool(value as Boolean)
            "Bytes" -> enc.writeBytes(value as ByteArray)
            "JSON" -> enc.writeBytes(encodeBoundedLuxoJSON(luxoGenericJSONValue(value)).toByteArray(Charsets.UTF_8))
			"Model" -> writeStructuredParam(enc, param, value)
            else -> throw LuxoError("ConfigError", 0, "unsupported binary param type: ${param.type}")
        }
    }

    private fun encodeListParam(enc: LuxoEncoder, param: ParamSchema, value: Any) {
        val items = value as? List<*>
            ?: throw LuxoError("ConfigError", 0, "parameter ${param.name} must be a list")
        enc.writeArrayHeader(items.size)
        when (param.type) {
            "Int", "Duration" -> items.forEach { enc.writeSvarint(requireLuxoInt64(it)) }
            "Float" -> items.forEach { enc.writeFixed64((it as Number).toDouble()) }
            "UUID" -> items.forEach { enc.writeUuid(it as String) }
            "DateTime" -> items.forEach {
                val item = it ?: throw LuxoError("ConfigError", 0, "parameter ${param.name} contains null")
                writeDateTimeParam(enc, item)
            }
            "String", "Enum", "Decimal" -> items.forEach { enc.writeString(it as String) }
            "Boolean" -> items.forEach { enc.writeBool(it as Boolean) }
            "Bytes" -> items.forEach { enc.writeBytes(it as ByteArray) }
            "JSON" -> items.forEach {
                enc.writeBytes(encodeBoundedLuxoJSON(luxoGenericJSONValue(it)).toByteArray(Charsets.UTF_8))
            }
			"Model" -> items.forEach { writeStructuredParam(enc, param, it) }
            else -> throw LuxoError("ConfigError", 0, "unsupported binary list param type: ${param.type}")
        }
    }

	private fun writeStructuredParam(enc: LuxoEncoder, param: ParamSchema, value: Any?) {
		val structured = value as? LuxoBinaryEncodable
			?: throw LuxoError(
				"ConfigError",
				0,
				"${param.typeName ?: "structured parameter"} must implement LuxoBinaryEncodable",
			)
		enc.writeDelimited(structured::writeLuxo)
	}

    private fun writeDateTimeParam(enc: LuxoEncoder, value: Any): Unit = when (value) {
        is String -> enc.writeDateTime(value)
        else -> throw LuxoError("ConfigError", 0, "invalid DateTime parameter: ${value::class.qualifiedName}")
    }
}

/** Optional terminal notifications. Explicit cancellation does not invoke either callback. */
class StreamObserver(
    val onComplete: (() -> Unit)? = null,
    val onError: ((LuxoError) -> Unit)? = null,
)

/**
 * Transport interface for calls and subscriptions, mode handled internally.
 *
 * JSON mode: returns parsed JsonElement (for kotlinx.serialization decode).
 * Binary mode: returns raw ByteArray (for generated decode{Model} functions).
 */
interface Transport {
    suspend fun call(api: String, params: Map<String, Any?> = emptyMap()): Any
    suspend fun call(api: String, request: GeneratedRequest): Any =
        throw LuxoError("ConfigError", 0, "transport must implement generated requests")
    suspend fun <T> subscribe(
        api: String, decoder: (Any) -> T, request: GeneratedRequest,
        observer: StreamObserver? = null, handler: (T) -> Unit,
    ): () -> Unit = throw LuxoError("ConfigError", 0, "transport must implement generated subscriptions")
    /** Decode separately so an invalid payload cannot be confused with an application callback error. */
    suspend fun <T> subscribe(
        api: String,
        decoder: (Any) -> T,
        params: Map<String, Any?> = emptyMap(),
        observer: StreamObserver? = null,
        handler: (T) -> Unit,
    ): () -> Unit =
        throw LuxoError("ConfigError", 0, "subscriptions require a WebSocket endpoint")
    fun setSchema(schema: Map<String, APISchemaEntry>)
    fun setMode(mode: TransportMode)
    fun setToken(token: String)
}

/** Mode-specific headers and their HTTP credential generation. */
internal class HttpProtocolHeaders private constructor(
    val json: Headers,
    val binary: Headers,
    val session: Any,
) {
    // Accessed only under the owning transport's refresh lock.
    internal var refresh: HTTPTokenRefresh? = null

    private constructor(base: Headers, session: Any) : this(
        base.newBuilder().set("Content-Type", JSON_MEDIA_TYPE.toString()).set("X-Luxo-Mode", "json").build(),
        base.newBuilder().set("Content-Type", BINARY_MEDIA_TYPE.toString()).set("X-Luxo-Mode", "binary").build(),
        session,
    )

    internal constructor(headers: Map<String, String>, token: String?) : this(Headers.Builder().apply {
        for ((name, value) in headers) set(name, value)
        if (token != null) set("Authorization", "Bearer $token")
    }.build(), Any())

    fun withToken(token: String, session: Any = Any()): HttpProtocolHeaders =
        HttpProtocolHeaders(json.newBuilder().set("Authorization", "Bearer $token").build(), session)

    fun nextGeneration(): HttpProtocolHeaders = HttpProtocolHeaders(json, binary, session)

    companion object {
        val JSON_MEDIA_TYPE = "application/json; charset=utf-8".toMediaType()
        val BINARY_MEDIA_TYPE = "application/x-luxo".toMediaType()
    }
}

private object HTTPTokenRefreshScope : CoroutineScope {
    override val coroutineContext: CoroutineContext = EmptyCoroutineContext
}

internal class HTTPTokenRefresh(context: CoroutineContext, callback: suspend () -> String?) :
    CoroutineContext.Element, CoroutineContext.Key<HTTPTokenRefresh> {
    // Distinct keys retain ancestor flights when a callback refreshes another transport.
    override val key: CoroutineContext.Key<*> get() = this

    val outcome = CompletableDeferred<Result<String?>>()
    // The refresh belongs to its waiters, not to the caller that happened to start it.
    val worker = HTTPTokenRefreshScope.launch(context.minusKey(Job) + this, start = CoroutineStart.LAZY) {
        outcome.complete(runCatching {
            val token = callback()
            currentCoroutineContext().ensureActive()
            token
        })
    }
    var waiters = 0
    var discarded = false

    fun discard() {
        outcome.complete(Result.success(null))
        worker.cancel()
    }
}

/** Only the calling coroutine owns this reference; intercepted request bodies stay immutable. */
private class HTTPRequestBodySlot(body: RequestBody) {
    var value: RequestBody = body
        private set

    fun release() { value = retiredBody }

    companion object {
        private val retiredBody = HTTPRequestBody(ByteArray(0), HttpProtocolHeaders.BINARY_MEDIA_TYPE)
    }
}

/** OkHttp-based transport for Android / JVM. */
class OkHttpTransport(
    private val endpoint: String,
    token: String? = null,
    headers: Map<String, String> = emptyMap(),
    client: OkHttpClient? = null,
    mode: TransportMode = TransportMode.JSON,
    schema: Map<String, APISchemaEntry> = emptyMap(),
    /** Request timeout in seconds (default 30) */
    timeoutSeconds: Long = 30,
    /** Called only for valid HTTP/code 401 errors; a new token retries once in the original mode. */
    private val onTokenExpired: (suspend () -> String?)? = null,
    private val maxResponseBytes: Int = 2 * 1024 * 1024,
    maxConcurrentRequests: Int = 16,
) : Transport {

    init {
        require(maxResponseBytes > 0) { "maxResponseBytes must be positive" }
        require(timeoutSeconds >= 0) { "timeoutSeconds must not be negative" }
        require(maxConcurrentRequests > 0) { "maxConcurrentRequests must be positive" }
    }
    private val requestTimeoutMillis = TimeUnit.SECONDS.toMillis(timeoutSeconds)
    private val capacity = HTTPRequestCapacity(maxConcurrentRequests)

    // Preserve OkHttp URL normalization and call-time failures; reuse its immutable value.
    private val endpointURL by lazy { Request.Builder().url(endpoint).build().url }

    private val client: OkHttpClient = client?.let(::directHTTPClient) ?: defaultHTTPClient(timeoutSeconds, maxConcurrentRequests)
    private val refreshLock = Any()
    @Volatile private var currentHeaders = HttpProtocolHeaders(headers, token)
    @Volatile private var currentMode: TransportMode = mode
    @Volatile private var currentSchema: Map<String, APISchemaEntry> = snapshotAPISchema(schema)

    /**
     * Call a Luxo API. Returns JsonElement (JSON mode) or ByteArray (binary mode).
     * Generated client code handles both transparently.
     */
    override fun setSchema(schema: Map<String, APISchemaEntry>) { currentSchema = snapshotAPISchema(schema) }
    override fun setMode(mode: TransportMode) { currentMode = mode }
    override fun setToken(token: String) {
        val stale = synchronized(refreshLock) {
            val previous = currentHeaders
            currentHeaders = previous.withToken(token)
            previous.refresh?.also { it.discarded = true }
        }
        stale?.discard()
    }

    override suspend fun call(api: String, params: Map<String, Any?>): Any {
        val started = if (requestTimeoutMillis == 0L) 0L else System.nanoTime()
        val mode = currentMode
        val headers = currentHeaders
        val meta = currentSchema[api]
        val body = if (mode == TransportMode.BINARY) encodeBinaryRequest(api, params, null)
            else encodeJSONRequest(meta, api, params)
        return dispatchPrepared(body, mode, headers, started, meta)
    }

    override suspend fun call(api: String, request: GeneratedRequest): Any {
        val started = if (requestTimeoutMillis == 0L) 0L else System.nanoTime()
        val mode = currentMode
        if (mode == TransportMode.BINARY) {
            val headers = currentHeaders
            return dispatchPrepared(encodeBinaryRequest(api, emptyMap(), request), mode, headers, started, null)
        }
        val meta = currentSchema[api]
        val body = encodeGeneratedJSONRequest(api, request, meta)
        return dispatchPrepared(body, mode, currentHeaders, started, meta)
    }

    private suspend inline fun dispatchPrepared(body: RequestBody, mode: TransportMode, headers: HttpProtocolHeaders, started: Long, schema: APISchemaEntry?): Any {
        val slot = HTTPRequestBodySlot(body)
        return if (requestTimeoutMillis == 0L) sendPrepared(slot, mode, headers, schema)
            else withinRequestDeadline(slot, mode, headers, started, schema)
    }

    private suspend fun withinRequestDeadline(body: HTTPRequestBodySlot, mode: TransportMode, headers: HttpProtocolHeaders, started: Long, schema: APISchemaEntry?): Any {
        try {
            // Charge synchronous encoding to the same monotonic deadline without retaining its inputs.
            val remaining = requestTimeoutMillis.milliseconds - (System.nanoTime() - started).nanoseconds
            // Preserve callback exceptions without coroutine stack-recovery copies.
            // Cancellation must still cross the timeout boundary normally.
            val outcome = withTimeoutOrNull(remaining) {
                try { Result.success(sendPrepared(body, mode, headers, schema)) }
                catch (error: CancellationException) { throw error }
                catch (error: Exception) { Result.failure<Any>(error) }
            } ?: throw LuxoError("TimeoutError", 0, "request timed out after ${requestTimeoutMillis}ms")
            return outcome.getOrThrow()
        } finally { body.release() }
    }

    private suspend fun sendPrepared(body: HTTPRequestBodySlot, mode: TransportMode, headers: HttpProtocolHeaders, schema: APISchemaEntry?): Any =
        if (mode == TransportMode.BINARY) callBinary(body, headers) else callJSON(body, headers, schema)

    private suspend fun callJSON(body: HTTPRequestBodySlot, headers: HttpProtocolHeaders, schema: APISchemaEntry?, isRetry: Boolean = false): JsonElement {
        try {
            val response = client.awaitFullResponse(
                Request.Builder().url(endpointURL).post(body.value).headers(headers.json).build(),
                capacity, maxResponseBytes,
            )
            val json = try {
                decodeHTTPJSON(response.body, schema)
            } catch (e: Exception) {
                throw LuxoError("ParseError", response.code, "invalid JSON response: ${e.message}")
            }
            val decoded = decodeLuxoJsonError(json, response.code)
            if (response.code == 200 && decoded == null) {
                return json["data"] ?: throw LuxoError("ParseError", response.code, "missing JSON response data")
            }
            val error = decoded ?: LuxoError("ParseError", response.code, "missing JSON error envelope")
            if (refreshUnauthorized(response.code, error, isRetry, headers)) {
                currentCoroutineContext().ensureActive()
                val renewed = currentHeaders
                if (renewed.session === headers.session) return callJSON(body, renewed, schema, isRetry = true)
            }
            throw error
        } finally { body.release() }
    }

    private suspend fun callBinary(body: HTTPRequestBodySlot, headers: HttpProtocolHeaders, isRetry: Boolean = false): ByteArray {
        try {
            val response = client.awaitFullResponse(
                Request.Builder().url(endpointURL).post(body.value).headers(headers.binary).build(),
                capacity, maxResponseBytes,
            )
            if (response.code == 200) {
                if (response.body.isEmpty()) throw LuxoBinaryProtocol.missingResponse(response.code)
                return response.body
            }
            val error = LuxoBinaryProtocol.decodeError(response.body, response.code)
            if (refreshUnauthorized(response.code, error, isRetry, headers)) {
                currentCoroutineContext().ensureActive()
                val renewed = currentHeaders
                if (renewed.session === headers.session) return callBinary(body, renewed, isRetry = true)
            }
            throw error
        } finally { body.release() }
    }

    private fun encodeGeneratedJSONRequest(api: String, request: GeneratedRequest, meta: APISchemaEntry?): RequestBody {
        return encodeJSONRequest(meta, api, request.toJSONParameters())
    }

    private fun encodeJSONRequest(meta: APISchemaEntry?, api: String, params: Map<String, Any?>): RequestBody {
        val body = buildJsonObject {
            putLuxoParams(params, meta)
            put("\$api", JsonPrimitive(api))
        }
        return HTTPRequestBody(encodeLuxoJSON(body).toByteArray(Charsets.UTF_8), HttpProtocolHeaders.JSON_MEDIA_TYPE)
    }

    private fun encodeBinaryRequest(api: String, params: Map<String, Any?>, generated: GeneratedRequest?): RequestBody {
        val meta = currentSchema[api]
            ?: throw LuxoError("ConfigError", 0, "no schema for API \"$api\" — binary mode requires schema")

        val body = if (generated == null) LuxoBinaryProtocol.encodeRequest(meta, params)
            else LuxoBinaryProtocol.encodeRequest(meta, generated)

        return HTTPRequestBody(body, HttpProtocolHeaders.BINARY_MEDIA_TYPE)
    }

    private suspend fun refreshUnauthorized(status: Int, error: LuxoError, isRetry: Boolean, headers: HttpProtocolHeaders): Boolean {
        if (isRetry || status != 401 || error.code != 401 || error.error == "ParseError") return false
        val callback = onTokenExpired ?: return false
        val context = currentCoroutineContext()
        context.ensureActive()
        val refresh = synchronized(refreshLock) {
            val current = currentHeaders
            if (current.session !== headers.session) return false
            val active = headers.refresh
            // A callback's own unauthorized request cannot await its callback's outcome.
            if (active != null && !active.outcome.isCompleted && context[active] === active) return false
            val flight = active ?: HTTPTokenRefresh(context, callback).also { headers.refresh = it }
            flight.waiters++
            flight
        }
        try {
            refresh.worker.start()
            val outcome = refresh.outcome.await()
            context.ensureActive()
            synchronized(refreshLock) {
                if (currentHeaders.session !== headers.session || refresh.discarded) return false
                if (currentHeaders === headers) {
                    // Retain this outcome for already-sent requests. A failed refresh
                    // gives future requests a new generation with unchanged headers.
                    val token = outcome.getOrNull()
                    currentHeaders = if (token == null) headers.nextGeneration()
                        else headers.withToken(token, headers.session)
                }
            }
            return outcome.getOrThrow() != null
        } finally {
            val abandoned = synchronized(refreshLock) {
                refresh.waiters--
                if (refresh.waiters == 0 && currentHeaders === headers) {
                    refresh.discarded = true
                    // A response still in flight can start the replacement refresh.
                    headers.refresh = null
                    true
                } else false
            }
            if (abandoned) {
                refresh.discard()
                // Keep the last caller's structured cleanup semantics.
                withContext(NonCancellable) { refresh.worker.join() }
            }
        }
    }
}

/** Internal response wrapper carrying HTTP status code. */
private data class HTTPResponseBytes(val code: Int, val body: ByteArray)

private suspend fun OkHttpClient.awaitFullResponse(request: Request, capacity: HTTPRequestCapacity, limit: Int): HTTPResponseBytes =
    suspendCancellableCoroutine { cont ->
        if (!capacity.acquire()) {
            cont.resumeWithException(LuxoError("NetworkError", 503, "HTTP request capacity exceeded"))
            return@suspendCancellableCoroutine
        }
        try {
            val call = newCall(request)
            val callback = object : Callback, (Throwable?) -> Unit {
                @Volatile var activeCall: Call? = null
                override fun invoke(cause: Throwable?) { activeCall?.cancel() }
                override fun onFailure(call: Call, e: IOException) {
                    activeCall = null
                    capacity.release()
                    cont.resumeWithException(LuxoError("NetworkError", 0, e.message ?: "network error"))
                }
                override fun onResponse(call: Call, response: Response) {
                    val result = try {
                        Result.success(response.use { HTTPResponseBytes(it.code, readBoundedHTTPBody(it, limit)) })
                    } catch (error: Exception) {
                        call.cancel()
                        Result.failure(error as? LuxoError ?: LuxoError("NetworkError", 0, error.message ?: "response body failed"))
                    } finally { activeCall = null; capacity.release() }
                    cont.resumeWith(result)
                }
            }
            callback.activeCall = call
            try {
                cont.invokeOnCancellation(callback)
                call.enqueue(callback)
            } catch (error: Exception) {
                callback.activeCall = null
                throw error
            }
        } catch (error: Exception) {
            capacity.release()
            cont.resumeWithException(LuxoError("NetworkError", 0, error.message ?: "request failed"))
        }
    }
