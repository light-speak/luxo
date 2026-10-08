package app

import com.luxo.client.GeneratedRequest

import com.luxo.client.APISchemaEntry
import com.luxo.client.LuxoCodecException
import com.luxo.client.LuxoEncoder
import com.luxo.client.Transport
import com.luxo.client.TransportMode
import com.luxo.client.StreamObserver
import com.luxo.interop.LuxoClient
import com.luxo.interop.Precision
import com.luxo.interop.PrecisionInput
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import java.util.concurrent.CompletableFuture

internal suspend fun verifyNullableRootCalls(client: LuxoClient) {
    for (value in listOf(null, 0L, Long.MIN_VALUE, Long.MAX_VALUE)) {
        check(client.echoOptionalInteger(value) == value)
    }
    for (values in listOf(null, emptyList(), listOf(Long.MIN_VALUE, Long.MAX_VALUE))) {
        check(client.echoOptionalIntegers(values) == values)
    }
    check(client.echoOptionalPrecision(null, select = "value") == null)
    check(client.echoOptionalPrecisions(null, select = "value") == null)
    check(client.echoOptionalPrecisions(emptyList(), select = "value") == emptyList<Precision>())
    val input = PrecisionInput(Long.MAX_VALUE, 0, null, emptyList(), "", 0)
    val row = checkNotNull(client.echoOptionalPrecision(input, select = "value"))
    check(row.value.value() == Long.MAX_VALUE && !row.label.isSelected)
    val rows = checkNotNull(client.echoOptionalPrecisions(listOf(input), select = "value"))
    check(rows.single().value.value() == Long.MAX_VALUE && !rows.single().label.isSelected)
}

internal suspend fun verifyNullableRootStreams(client: LuxoClient) {
    for (kind in 0..2) {
        val values = mutableListOf<Any?>()
        val terminal = CompletableFuture<Unit>()
        val observer = StreamObserver(onComplete = { terminal.complete(Unit) },
            onError = { terminal.completeExceptionally(it) })
        val cancel = when (kind) {
            0 -> client.subscribeWatchOptionalInteger(observer = observer) { values += it }
            1 -> client.subscribeWatchOptionalIntegers(observer = observer) { values.add(it) }
            else -> client.subscribeWatchOptionalPrecision(select = "value", observer = observer) { values += it }
        }
        try {
            awaitValue(terminal)
            check(values.size == 2 && values.first() == null)
            check(rootValue(values.last()) == when (kind) {
                0 -> 0L
                1 -> emptyList<Long>()
                else -> 42L
            })
        } finally { cancel() }
    }
    check(client.echoInteger(42) == 42L)
}

private class RootTransport : Transport {
    var response: Any = JsonNull
    override suspend fun call(api: String, params: Map<String, Any?>): Any = response
    override suspend fun call(api: String, request: GeneratedRequest): Any = response
    override fun setSchema(schema: Map<String, APISchemaEntry>) {}
    override fun setMode(mode: TransportMode) {}
    override fun setToken(token: String) {}
}

private data class RootCase(val call: suspend () -> Any?, val body: ByteArray, val expected: Any)

internal suspend fun verifyNullableRootDecoding() {
    val transport = RootTransport()
    val client = LuxoClient(transport)
    for ((call, body, expected) in nullableRootCases(client)) {
        for (value in listOf(JsonNull, byteArrayOf(0))) {
            transport.response = value
            check(call() == null)
        }
        transport.response = byteArrayOf(1) + body
        check(rootValue(call()) == expected)
        val malformed = listOf(byteArrayOf(), byteArrayOf(1), byteArrayOf(2),
            byteArrayOf(-1), byteArrayOf(0, 0), byteArrayOf(0) + body,
            byteArrayOf(1) + body + byteArrayOf(99)) +
            (1 until body.size).map { byteArrayOf(1) + body.copyOf(it) }
        for (value in malformed) {
            transport.response = value
            check(runCatching { call() }.exceptionOrNull() is LuxoCodecException)
        }
    }
    transport.response = JsonPrimitive(42)
    check(client.echoOptionalInteger(null) == 42L)
    transport.response = buildJsonObject { put("value", JsonPrimitive(42)) }
    check(client.echoOptionalPrecision(null, select = "value")!!.value.value() == 42L)
}

private fun rootValue(value: Any?): Any? = when (value) {
    is Precision -> value.value.value()
    is List<*> -> value.map(::rootValue)
    else -> value
}

private fun nullableRootCases(client: LuxoClient): List<RootCase> {
    val id = LuxoClient.schema.getValue("echoOptionalPrecision").fields.getValue("value").fieldID
    val row = LuxoEncoder().apply { writeVarint(0); writeFieldInt(id, 42); writeEnd() }.bytes()
    val column = LuxoEncoder().apply {
        writeVarint(1); writeVarint(0); writeFieldInt(id, 42); writeEnd()
    }.bytes()
    return listOf(
        RootCase({ client.echoOptionalInteger(null) }, byteArrayOf(0), 0L),
        RootCase({ client.echoOptionalInteger(null) }, byteArrayOf(84), 42L),
        RootCase({ client.echoOptionalIntegers(null) }, byteArrayOf(0), emptyList<Long>()),
        RootCase({ client.echoOptionalIntegers(null) }, byteArrayOf(1, 84), listOf(42L)),
        RootCase({ client.echoOptionalPrecision(null, select = "value") }, row, 42L),
        RootCase({ client.echoOptionalPrecisions(null, select = "value") }, column, listOf(42L)),
        RootCase({ client.echoOptionalPrecisions(null, select = "value") }, byteArrayOf(0, 0, 0), emptyList<Long>()),
    )
}
