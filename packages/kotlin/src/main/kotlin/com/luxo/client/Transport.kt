package com.luxo.client

import kotlinx.coroutines.suspendCancellableCoroutine
import kotlinx.serialization.json.*
import okhttp3.*
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.IOException
import java.util.concurrent.ConcurrentHashMap
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

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
) : Transport {

    private val client: OkHttpClient = client ?: OkHttpClient()
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

    private suspend fun callJSON(api: String, params: Map<String, Any?>): JsonElement {
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

        val responseBody = client.awaitStringCall(request)

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

    private suspend fun callBinary(api: String, params: Map<String, Any?>): ByteArray {
        val meta = currentSchema[api]
            ?: throw LuxoError("ConfigError", 0, "no schema for API \"$api\" — binary mode requires schema")

        val enc = LuxoEncoder()
        enc.writeVarint(meta.id.toLong())
        enc.writeVarint(0) // field mask = 0 (SELECT *)

        for (pm in meta.params) {
            val v = params[pm.name] ?: continue
            when (pm.type) {
                "Int" -> enc.writeFieldInt(pm.fieldID, (v as Number).toLong())
                "Float" -> enc.writeFieldFloat(pm.fieldID, (v as Number).toDouble())
                "String" -> enc.writeFieldString(pm.fieldID, v as String)
                "Boolean" -> enc.writeFieldBool(pm.fieldID, v as Boolean)
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

        return client.awaitBinaryCall(request)
    }


    companion object {
        private val JSON_MEDIA_TYPE = "application/json; charset=utf-8".toMediaType()
        private val BINARY_MEDIA_TYPE = "application/x-luxo".toMediaType()
    }
}

private suspend fun OkHttpClient.awaitStringCall(request: Request): String =
    suspendCancellableCoroutine { cont ->
        val call = newCall(request)
        cont.invokeOnCancellation { call.cancel() }
        call.enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) {
                cont.resumeWithException(LuxoError("NetworkError", 0, e.message ?: "network error"))
            }
            override fun onResponse(call: Call, response: Response) {
                response.use { resp ->
                    cont.resume(resp.body?.string() ?: "{}")
                }
            }
        })
    }

private suspend fun OkHttpClient.awaitBinaryCall(request: Request): ByteArray =
    suspendCancellableCoroutine { cont ->
        val call = newCall(request)
        cont.invokeOnCancellation { call.cancel() }
        call.enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) {
                cont.resumeWithException(LuxoError("NetworkError", 0, e.message ?: "network error"))
            }
            override fun onResponse(call: Call, response: Response) {
                response.use { resp ->
                    if (!resp.isSuccessful) {
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
                    cont.resume(resp.body?.bytes() ?: ByteArray(0))
                }
            }
        })
    }
