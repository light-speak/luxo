package com.luxo.client

import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.serialization.json.*
import okhttp3.*
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.IOException
import java.util.Base64
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

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
) {
    // Built once with the schema, not on the request path.
    internal val paramsByName = params.associateBy { it.name }
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
	is LuxoBinaryEncodable -> value.toLuxoJson()
    else -> throw LuxoError("ConfigError", 0, "unsupported JSON value: ${value::class.qualifiedName}")
}

/** Canonical Luxo binary request, error, and WebSocket frame codec. */
internal object LuxoBinaryProtocol {
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

    fun encodeRequest(meta: APISchemaEntry, request: GeneratedRequest): ByteArray {
        if (meta.id != request.apiID) throw LuxoError("ConfigError", 0, "generated request API ID differs from registered schema")
        val enc = LuxoEncoder()
        enc.writeVarint(request.apiID.toLong())
        writeFieldMask(enc, meta, request.selection)
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
                        require(value in Int.MIN_VALUE.toLong()..Int.MAX_VALUE.toLong()) { "error code exceeds signed 32-bit range" }
                        code = value.toInt()
                    }
                    2 -> name = dec.readString()
                    3 -> message = dec.readString()
                    4 -> traceId = dec.readString()
                    5 -> data = decodeLuxoJSON(dec.readString())
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

    private fun writeFieldMask(enc: LuxoEncoder, meta: APISchemaEntry, selection: String?) {
		if (meta.fields.isEmpty()) {
            enc.writeVarint(0)
            return
        }
		if (selection.isNullOrBlank()) {
			throw LuxoError("ConfigError", 0, "\$select is required for structured responses")
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
            "JSON" -> enc.writeBytes(encodeLuxoJSON(luxoJsonValue(value)).toByteArray(Charsets.UTF_8))
			"Model" -> writeStructuredParam(enc, param, value)
            else -> throw LuxoError("ConfigError", 0, "unsupported binary param type: ${param.type}")
        }
    }

    private fun encodeListParam(enc: LuxoEncoder, param: ParamSchema, value: Any) {
        val items = value as? List<*>
            ?: throw LuxoError("ConfigError", 0, "parameter ${param.name} must be a list")
        enc.writeVarint(items.size.toLong())
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
                enc.writeBytes(encodeLuxoJSON(luxoJsonValue(it)).toByteArray(Charsets.UTF_8))
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

/** Immutable mode-specific headers; configuration and token changes stay off the request path. */
internal class HttpProtocolHeaders private constructor(base: Headers) {
    val json: Headers = base.newBuilder().set("Content-Type", JSON_MEDIA_TYPE.toString())
        .set("X-Luxo-Mode", "json").build()
    val binary: Headers = base.newBuilder().set("Content-Type", BINARY_MEDIA_TYPE.toString())
        .set("X-Luxo-Mode", "binary").build()

    constructor(headers: Map<String, String>, token: String?) : this(Headers.Builder().apply {
        for ((name, value) in headers) set(name, value)
        if (token != null) set("Authorization", "Bearer $token")
    }.build())

    fun withToken(token: String): HttpProtocolHeaders =
        HttpProtocolHeaders(json.newBuilder().set("Authorization", "Bearer $token").build())

    companion object {
        val JSON_MEDIA_TYPE = "application/json; charset=utf-8".toMediaType()
        val BINARY_MEDIA_TYPE = "application/x-luxo".toMediaType()
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
) : Transport {

    private val client: OkHttpClient = (client ?: OkHttpClient.Builder()
        .connectTimeout(timeoutSeconds, java.util.concurrent.TimeUnit.SECONDS)
        .readTimeout(timeoutSeconds, java.util.concurrent.TimeUnit.SECONDS)
        .writeTimeout(timeoutSeconds, java.util.concurrent.TimeUnit.SECONDS)
        .build())
    @Volatile private var currentHeaders = HttpProtocolHeaders(headers, token)
    @Volatile private var currentMode: TransportMode = mode
    @Volatile private var currentSchema: Map<String, APISchemaEntry> = snapshotAPISchema(schema)

    /**
     * Call a Luxo API. Returns JsonElement (JSON mode) or ByteArray (binary mode).
     * Generated client code handles both transparently.
     */
    override fun setSchema(schema: Map<String, APISchemaEntry>) { currentSchema = snapshotAPISchema(schema) }
    override fun setMode(mode: TransportMode) { currentMode = mode }
    override fun setToken(token: String) { currentHeaders = currentHeaders.withToken(token) }

    override suspend fun call(api: String, params: Map<String, Any?>): Any {
        return if (currentMode == TransportMode.BINARY) callBinary(api, params) else callJSON(api, params)
    }

    override suspend fun call(api: String, request: GeneratedRequest): Any =
        if (currentMode == TransportMode.BINARY) callBinary(api, emptyMap(), generated = request)
        else callJSON(api, request.toJSONParameters())

    private suspend fun callJSON(api: String, params: Map<String, Any?>, isRetry: Boolean = false): JsonElement {
        val body = buildJsonObject {
            put("\$api", JsonPrimitive(api))
            putLuxoParams(params, currentSchema[api])
        }

        val request = Request.Builder()
            .url(endpoint)
            .post(encodeLuxoJSON(body).toRequestBody(HttpProtocolHeaders.JSON_MEDIA_TYPE))
            .headers(currentHeaders.json)
            .build()

        val response = client.awaitFullResponse(request) {
            HTTPResponseBytes(it.code, it.body?.bytes() ?: throw IOException("missing response body"))
        }
        val json = try {
            decodeHTTPJSON(response.body)
        } catch (e: Exception) {
            throw LuxoError("ParseError", response.code, "invalid JSON response: ${e.message}")
        }
        val decoded = decodeLuxoJsonError(json, response.code)
        if (response.code == 200 && decoded == null) {
            return json["data"] ?: throw LuxoError("ParseError", response.code, "missing JSON response data")
        }
        val error = decoded ?: LuxoError("ParseError", response.code, "missing JSON error envelope")
        val newToken = refreshFor(response.code, error, isRetry)?.invoke()
        if (newToken != null) {
            setToken(newToken)
            return callJSON(api, params, isRetry = true)
        }
        throw error
    }

    private suspend fun callBinary(api: String, params: Map<String, Any?>, isRetry: Boolean = false, generated: GeneratedRequest? = null): ByteArray {
        val meta = currentSchema[api]
            ?: throw LuxoError("ConfigError", 0, "no schema for API \"$api\" — binary mode requires schema")

        val body = if (generated == null) LuxoBinaryProtocol.encodeRequest(meta, params)
            else LuxoBinaryProtocol.encodeRequest(meta, generated)

        val request = Request.Builder()
            .url(endpoint)
            .post(body.toRequestBody(HttpProtocolHeaders.BINARY_MEDIA_TYPE))
            .headers(currentHeaders.binary)
            .build()

        val response = client.awaitFullResponse(request) {
            HTTPResponseBytes(it.code, it.body?.bytes() ?: throw IOException("missing response body"))
        }
        if (response.code == 200) return response.body
        val error = LuxoBinaryProtocol.decodeError(response.body, response.code)
        val newToken = refreshFor(response.code, error, isRetry)?.invoke()
        if (newToken != null) {
            setToken(newToken)
            return callBinary(api, params, isRetry = true, generated = generated)
        }
        throw error
    }

    private fun refreshFor(status: Int, error: LuxoError, isRetry: Boolean): (suspend () -> String?)? =
        if (!isRetry && status == 401 && error.code == 401 && error.error != "ParseError") onTokenExpired else null
}

/** Internal response wrapper carrying HTTP status code. */
private data class HTTPResponseBytes(val code: Int, val body: ByteArray)

private suspend inline fun <T> OkHttpClient.awaitFullResponse(request: Request, crossinline read: (Response) -> T): T =
    suspendCancellableCoroutine { cont ->
        val call = newCall(request)
        cont.invokeOnCancellation { call.cancel() }
        call.enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) {
                cont.resumeWithException(LuxoError("NetworkError", 0, e.message ?: "network error"))
            }
            override fun onResponse(call: Call, response: Response) {
                val result = try {
                    response.use { read(it) }
                } catch (error: Exception) {
                    cont.resumeWithException(LuxoError("NetworkError", 0, error.message ?: "response body failed"))
                    return
                }
                cont.resume(result)
            }
        })
    }
