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

/** Transport interface -- implement for different runtimes. */
interface Transport {
    suspend fun call(api: String, params: Map<String, JsonElement> = emptyMap()): JsonElement
    /** Binary call — returns raw bytes for generated ReadLuxo decoding. */
    suspend fun callBinary(api: String, params: Map<String, Any?> = emptyMap()): ByteArray
}

/** OkHttp-based transport for Android / JVM. */
class OkHttpTransport(
    private val endpoint: String,
    token: String? = null,
    headers: Map<String, String> = emptyMap(),
    client: OkHttpClient? = null,
    mode: TransportMode = TransportMode.JSON,
    schema: Map<String, APISchemaEntry> = emptyMap(),
) : Transport {

    private val client: OkHttpClient = client ?: OkHttpClient()
    private val extraHeaders = ConcurrentHashMap<String, String>()
    @Volatile private var mode: TransportMode = mode
    private val schema: Map<String, APISchemaEntry> = schema

    init {
        extraHeaders.putAll(headers)
        if (token != null) {
            extraHeaders["Authorization"] = "Bearer $token"
        }
    }

    override suspend fun call(api: String, params: Map<String, JsonElement>): JsonElement {
        val body = buildJsonObject {
            put("\$api", JsonPrimitive(api))
            for ((k, v) in params) put(k, v)
        }

        val request = Request.Builder()
            .url(endpoint)
            .post(body.toString().toRequestBody(JSON_MEDIA_TYPE))
            .apply {
                header("Content-Type", "application/json")
                for ((k, v) in extraHeaders) header(k, v)
            }
            .build()

        val responseBody = client.awaitCall(request)

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

    override suspend fun callBinary(api: String, params: Map<String, Any?>): ByteArray {
        val meta = schema[api]
            ?: throw LuxoError("ConfigError", 0, "no schema for API \"$api\" — binary mode requires schema")

        // Encode binary request
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

    /** Update authorization token. */
    fun setToken(token: String) {
        extraHeaders["Authorization"] = "Bearer $token"
    }

    /** Switch transport mode at runtime. */
    fun setMode(newMode: TransportMode) {
        mode = newMode
    }

    companion object {
        private val JSON_MEDIA_TYPE = "application/json; charset=utf-8".toMediaType()
        private val BINARY_MEDIA_TYPE = "application/x-luxo".toMediaType()
    }
}

/** Suspend wrapper for OkHttp async call — returns response body as String. */
private suspend fun OkHttpClient.awaitCall(request: Request): String =
    suspendCancellableCoroutine { cont ->
        val call = newCall(request)
        cont.invokeOnCancellation { call.cancel() }
        call.enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) {
                cont.resumeWithException(LuxoError("NetworkError", 0, e.message ?: "network error"))
            }

            override fun onResponse(call: Call, response: Response) {
                response.use { resp ->
                    val body = resp.body?.string() ?: "{}"
                    cont.resume(body)
                }
            }
        })
    }

/** Suspend wrapper for OkHttp async call — returns response body as ByteArray. */
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
                        // Error responses are JSON
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
                    val bytes = resp.body?.bytes() ?: ByteArray(0)
                    cont.resume(bytes)
                }
            }
        })
    }
