package com.luxo.client

import java.util.concurrent.ConcurrentHashMap
import java.util.function.LongSupplier
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.jsonObject
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.MediaType
import okhttp3.Request
import okhttp3.OkHttpClient
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.ResponseBody.Companion.toResponseBody

/** Local, same-toolchain comparisons; initialization is separate from request processing. */
object HttpProtocolBenchmark {
    @Volatile private var bytesSink = byteArrayOf()
    @Volatile private var requestSink: Request? = null
    @Volatile private var errorSink: LuxoError? = null
    @Volatile private var headersSink: Any? = null
    @Volatile private var jsonSink: JsonElement? = null

    @JvmStatic
    fun main(args: Array<String>) {
        if (args.contains("--endpoint-init-only")) { measureEndpointInitialization(); return }
        println("JDK ${System.getProperty("java.version")}; 12 warmup pairs, 20 alternating samples; ns/op and B/op")
        if (args.contains("--endpoint-only")) { compareEndpoints(); return }
        compareJSONBodies(candidate = args.contains("--candidate"))
        if (args.contains("--json-only")) return
        compareLengths()
        compareHeaders()
        for (detailed in listOf(false, true)) compareErrors(detailed)
    }

    private fun measureEndpointInitialization() {
        val client = OkHttpClient()
        try {
            println("Actual Transport construction, shared prebuilt OkHttpClient, empty Schema; no URL access/network")
            PairedJvmBenchmark.isolated("endpoint transport construction", LongSupplier {
                headersSink = OkHttpTransport("https://example.test:8443/api%20v1", client = client)
                1L
            }, iterations = 20_000, warmupIterations = 20_000)
        } finally {
            client.dispatcher.executorService.shutdownNow()
            client.connectionPool.evictAll()
        }
    }

    private fun compareEndpoints() {
        val body = byteArrayOf(1, 0, 0).toRequestBody(HttpProtocolHeaders.BINARY_MEDIA_TYPE)
        for (endpoint in listOf("http://localhost/luvia", "https://example.test:8443/api%20v1")) {
            val cached by lazy { Request.Builder().url(endpoint).build().url }
            fun previous() = Request.Builder().url(endpoint).post(body).build()
            fun current() = Request.Builder().url(cached).post(body).build()
            check(previous().url == current().url)
            PairedJvmBenchmark.compare("endpoint warm $endpoint", LongSupplier {
                requestSink = previous(); requestSink!!.url.encodedPath.length.toLong()
            }, LongSupplier {
                requestSink = current(); requestSink!!.url.encodedPath.length.toLong()
            }, iterations = 50_000)
            PairedJvmBenchmark.compare("endpoint first access $endpoint", LongSupplier {
                requestSink = previous(); requestSink!!.url.encodedPath.length.toLong()
            }, LongSupplier {
                val first by lazy { Request.Builder().url(endpoint).build().url }
                requestSink = Request.Builder().url(first).post(body).build()
                requestSink!!.url.encodedPath.length.toLong()
            }, iterations = 50_000)
        }
    }

    private fun compareJSONBodies(candidate: Boolean) {
        val texts = linkedMapOf(
            "ascii" to """{"data":{"id":9223372036854775807,"name":"alpha"}}""",
            "unicode" to """{"data":{"id":9223372036854775807,"name":"精度 café 東京"}}""",
            "replacement" to """{"data":"\uFFFD"}""".replace("\\uFFFD", "\uFFFD"),
            "bom" to "\uFEFF" + """{"data":"\uFEFFvalue"}""".replace("\\uFEFF", "\uFEFF"),
            "rows16" to (1..16).joinToString(prefix = "{\"data\":[", postfix = "]}") {
                """{"id":9223372036854775807,"name":"row$it","value":1.25}"""
            },
            "long-unicode" to "{\"data\":\"" + "发布检查".repeat(350) + "\"}",
        )
        val contentType = "application/json; charset=utf-8".toMediaType()
        for ((name, text) in texts) {
            val bytes = text.toByteArray(Charsets.UTF_8)
            // Actual previous OkHttp body conversion, with the same JSON parser and fixtures.
            fun previous() = bytes.toResponseBody(contentType).use { Json.parseToJsonElement(it.string()).jsonObject }
            fun current() = bytes.toResponseBody(contentType).use { decodeHTTPJSON(it.bytes()) }
            check(previous() == current())
            PairedJvmBenchmark.compare("HTTP JSON body $name ${bytes.size}B", LongSupplier {
                val value = previous(); jsonSink = value; value.size.toLong()
            }, LongSupplier {
                val value = current(); jsonSink = value; value.size.toLong()
            }, iterations = 5000)
            if (candidate) compareJSONCandidate(name, bytes, contentType)
        }
    }

    // Exploratory alternative, not the HTTP implementation: U+FFFD may decode twice.
    private fun compareJSONCandidate(name: String, bytes: ByteArray, contentType: MediaType) {
        fun current() = bytes.toResponseBody(contentType).use { decodeHTTPJSON(it.bytes()) }
        fun sharedReader() = bytes.toResponseBody(contentType).use {
            Json.parseToJsonElement(decodeLuxoUtf8(it.bytes()).removePrefix("\uFEFF")).jsonObject
        }
        check(current() == sharedReader())
        PairedJvmBenchmark.compare("HTTP JSON existing reader candidate $name", LongSupplier {
            val value = current(); jsonSink = value; value.size.toLong()
        }, LongSupplier {
            val value = sharedReader(); jsonSink = value; value.size.toLong()
        }, iterations = 5000)
    }

    private fun compareLengths() {
        for (length in listOf(0, 16, 1024)) {
            val frame = LuxoEncoder().apply { writeBytes(ByteArray(length) { (it + 1).toByte() }) }.bytes()
            check(previousBytes(frame).contentEquals(LuxoDecoder(frame).readBytes()))
            PairedJvmBenchmark.compare("byte length $length", LongSupplier {
                bytesSink = previousBytes(frame); bytesSink.size.toLong()
            }, LongSupplier {
                bytesSink = LuxoDecoder(frame).readBytes(); bytesSink.size.toLong()
            }, iterations = 20_000)
        }
    }

    private fun compareHeaders() {
        val extra = ConcurrentHashMap(mapOf("Authorization" to "Bearer token", "X-Trace" to "trace", "X-Tenant" to "tenant"))
        val cached = HttpProtocolHeaders(extra, null)
        val body = byteArrayOf(1, 0, 0).toRequestBody(HttpProtocolHeaders.BINARY_MEDIA_TYPE)
        fun previous(): Request = Request.Builder().url("http://localhost/luvia").post(body).apply {
            header("Content-Type", "application/x-luxo"); header("X-Luxo-Mode", "binary")
            for ((name, value) in extra) header(name, value)
        }.build()
        fun current(): Request = Request.Builder().url("http://localhost/luvia").post(body).headers(cached.binary).build()
        check(previous().headers.toMultimap() == current().headers.toMultimap())
        PairedJvmBenchmark.compare("Binary request headers", LongSupplier {
            requestSink = previous(); requestSink!!.headers.size.toLong()
        }, LongSupplier {
            requestSink = current(); requestSink!!.headers.size.toLong()
        }, iterations = 20_000)
        PairedJvmBenchmark.compare("headers initialization (not per request)", LongSupplier {
            headersSink = ConcurrentHashMap(extra); extra.size.toLong()
        }, LongSupplier {
            headersSink = HttpProtocolHeaders(extra, null); cached.binary.size.toLong()
        }, iterations = 20_000)
    }

    private fun compareErrors(detailed: Boolean) {
        val frame = LuxoEncoder().apply {
            writeFieldInt(1, 401)
            writeFieldString(2, "Unauthorized")
            writeFieldString(3, if (detailed) "请求失败" else "expired")
            if (detailed) {
                writeFieldString(4, "trace")
                writeFieldString(5, "{\"id\":9223372036854775807,\"ratio\":1.5}")
                writeFieldString(6, "原因")
            }
            writeEnd()
        }.bytes()
        val before = previousError(frame, 502)
        val after = LuxoBinaryProtocol.decodeError(frame, 502)
        check(listOf(before.error, before.code, before.message, before.traceId, before.data, before.developmentCause) ==
            listOf(after.error, after.code, after.message, after.traceId, after.data, after.developmentCause))
        PairedJvmBenchmark.compare("Binary error detailed=$detailed", LongSupplier {
            errorSink = previousError(frame, 502); errorSink!!.code.toLong()
        }, LongSupplier {
            errorSink = LuxoBinaryProtocol.decodeError(frame, 502); errorSink!!.code.toLong()
        }, iterations = 5_000)
    }

    // Frozen pre-fix length behavior, only called with valid fixtures.
    private fun previousBytes(frame: ByteArray): ByteArray {
        val decoder = LuxoDecoder(frame)
        val size = decoder.readVarint().toInt()
        val offset = decoder.offset()
        if (offset + size > frame.size) throw LuxoCodecException("unexpected end of buffer")
        return frame.copyOfRange(offset, offset + size)
    }

    // Pre-strict-error behavior with the same current primitive codec on both sides.
    private fun previousError(body: ByteArray, status: Int): LuxoError = try {
        val decoder = LuxoDecoder(body)
        var code = status
        var name = "Error"
        var message = "HTTP $status"
        var trace: String? = null
        var data: JsonElement? = null
        var cause: String? = null
        var seen = 0
        var ended = false
        while (decoder.remaining() > 0) {
            if (!decoder.nextField()) { ended = true; break }
            when (decoder.fieldID) {
                1 -> {
                    val value = decoder.readInt()
                    require(value in Int.MIN_VALUE.toLong()..Int.MAX_VALUE.toLong())
                    code = value.toInt(); seen = seen or 1
                }
                2 -> { name = decoder.readString(); seen = seen or 2 }
                3 -> { message = decoder.readString(); seen = seen or 4 }
                4 -> trace = decoder.readString()
                5 -> data = Json.parseToJsonElement(decoder.readBytes().toString(Charsets.UTF_8))
                6 -> cause = decoder.readString()
                else -> throw IllegalArgumentException("unknown binary error field")
            }
        }
        require(ended && decoder.remaining() == 0 && seen == 7)
        LuxoError(name, code, message, trace, data, cause)
    } catch (error: Exception) { LuxoError("ParseError", status, "invalid binary error response: ${error.message}") }
}
