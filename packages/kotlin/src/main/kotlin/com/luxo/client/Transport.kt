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

/** Transport interface -- implement for different runtimes. */
interface Transport {
    /**
     * Call a Luxo API.
     * @param api API name (e.g., "getUser")
     * @param params named parameters
     * @return raw JsonElement response data
     */
    suspend fun call(api: String, params: Map<String, JsonElement> = emptyMap()): JsonElement
}

/** OkHttp-based transport for Android / JVM. */
class OkHttpTransport(
    private val endpoint: String,
    token: String? = null,
    headers: Map<String, String> = emptyMap(),
    client: OkHttpClient? = null,
) : Transport {

    private val client: OkHttpClient = client ?: OkHttpClient()
    private val extraHeaders = ConcurrentHashMap<String, String>()

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

    /** Update authorization token. */
    fun setToken(token: String) {
        extraHeaders["Authorization"] = "Bearer $token"
    }

    companion object {
        private val JSON_MEDIA_TYPE = "application/json; charset=utf-8".toMediaType()
    }
}

/** Suspend wrapper for OkHttp async call. */
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
