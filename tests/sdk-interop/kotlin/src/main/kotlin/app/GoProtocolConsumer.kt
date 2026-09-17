package app

import com.luxo.client.LuxoWebSocket
import com.luxo.client.LuxoDecoder
import com.luxo.client.LuxoCodecException
import com.luxo.client.LuxoError
import com.luxo.client.decodeLuxoJSON
import com.luxo.interop.decodeNestedGroup
import com.luxo.interop.decodeColumnarNestedGroup
import com.luxo.interop.decodeJSONScalarArrays
import com.luxo.client.OkHttpTransport
import com.luxo.client.TransportMode
import com.luxo.client.StreamObserver
import com.luxo.interop.LuxoClient
import com.luxo.interop.Precision
import com.luxo.interop.PrecisionInput
import com.luxo.interop.NullableArrays
import com.luxo.interop.NullableArraysInput
import com.luxo.interop.NestedPrecision
import com.luxo.interop.NestedPrecisionInput
import com.luxo.interop.NestedGroup
import com.luxo.interop.NestedGroupInput
import com.luxo.interop.ScalarArrays
import com.luxo.interop.ScalarArraysInput
import java.io.File
import java.util.concurrent.CompletableFuture
import java.util.concurrent.TimeUnit
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException
import kotlin.coroutines.suspendCoroutine
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.OkHttpClient
import kotlin.coroutines.Continuation
import kotlin.coroutines.EmptyCoroutineContext
import kotlin.coroutines.startCoroutine

private const val selection = "value, duration, optional, values, label"

suspend fun main(args: Array<String>) {
    verifyNestedBoundaries()
    check(System.getProperty("java.class.path").split(File.pathSeparator).none {
        File(it).name.startsWith("kotlin-compiler-embeddable-")
    }) { "the compiler must not enter the consumer runtime" }
    val corpus = Json.parseToJsonElement(File(args[1]).readText()).jsonObject
    verifyJSONDateTimeBoundaries(corpus.getValue("dateTimeBoundaryCases").jsonArray)
    for (test in corpus.getValue("jsonUnicodeCases").jsonArray) {
        val item = test.jsonObject
        val raw = item.getValue("raw").jsonPrimitive.content
        val valid = item.getValue("valid").jsonPrimitive.content == "true"
        check(runCatching { decodeLuxoJSON(raw) }.isSuccess == valid) { item.toString() }
    }
    val values = corpus.getValue("int64Cases").jsonArray.map { it.jsonObject.getValue("value").jsonPrimitive.content.toLong() }
    val tokens = corpus.getValue("jsonNumberTokens").jsonArray.map { it.jsonPrimitive.content }
    val dates = corpus.getValue("dateTimeCases").jsonArray.map {
        it.jsonObject.getValue("input").jsonPrimitive.content to it.jsonObject.getValue("canonical").jsonPrimitive.content
    }
    val gateway = when (args.getOrNull(2)) {
        "direct" -> false
        "gateway" -> true
        else -> error("missing or invalid topology; use the Go -run owner")
    }
    val scope = if (gateway) "gateway success/streams/errors" else "direct success/streams/errors"
    for (websocket in listOf(false, true)) {
        for (mode in TransportMode.entries) {
            verifyChannel(args[0], websocket, mode, values, tokens, dates, corpus.getValue("uuidCanonicalCases").jsonArray)
            println("$scope: ${if (websocket) "WebSocket" else "HTTP"} $mode passed")
        }
    }
    verifySubscriptionIDs(args[0])
    verifyTerminalMaximumIDs(args[0])
}

private fun verifyJSONDateTimeBoundaries(cases: List<JsonElement>) {
    for (entry in cases) {
        val item = entry.jsonObject
        val input = item.getValue("input")
        val payload = buildJsonObject { put("dates", kotlinx.serialization.json.JsonArray(listOf(input))) }
        val result = runCatching { decodeJSONScalarArrays(payload).dates.value()?.single() }
        if (item.getValue("canonical") == JsonNull) {
            check(result.exceptionOrNull() is LuxoCodecException) { "accepted invalid JSON DateTime: $input" }
        } else {
            check(result.getOrThrow() == input.jsonPrimitive.content)
        }
    }
}

private suspend fun verifyUUIDs(client: LuxoClient, cases: List<JsonElement>) {
    for (entry in cases) {
        val item = entry.jsonObject
        val input = item.getValue("input").jsonPrimitive.content
        val canonical = item.getValue("canonical")
        if (canonical != JsonNull) {
            check(client.echoUUIDs(listOf(input)) == listOf(canonical.jsonPrimitive.content))
        } else {
            val error = runCatching { client.echoUUIDs(listOf(input)) }.exceptionOrNull()
            check(error is LuxoCodecException ||
                error is LuxoError && error.message.orEmpty().contains("uuid", ignoreCase = true)) { "$input: $error" }
        }
    }
    val valid = listOf("00112233-4455-6677-8899-aabbccddeeff")
    check(client.echoUUIDs(valid) == valid)
}

private suspend fun verifySubscriptionIDs(endpoint: String) {
    val starts = listOf(0uL, 9007199254740990uL, Long.MAX_VALUE.toULong() - 1u, ULong.MAX_VALUE - 2u)
    for (mode in TransportMode.entries) for (start in starts) {
        val http = OkHttpClient()
        val socket = LuxoWebSocket(endpoint.replace("http:", "ws:"), http, start, 5_000)
        socket.setMode(mode)
        val client = LuxoClient(socket)
        try {
            run {
                val firstValue = CompletableFuture<Precision>()
                val secondValue = CompletableFuture<Precision>()
                val first = startSubscription { client.subscribeWatchPrecision(Long.MIN_VALUE, select = selection) { firstValue.complete(it) } }
                val second = startSubscription { client.subscribeWatchPrecision(Long.MAX_VALUE, select = selection) { secondValue.complete(it) } }
                val cancelFirst = awaitValue(first)
                val cancelSecond = awaitValue(second)
                try {
                    check(awaitValue(firstValue).value.value() == Long.MIN_VALUE)
                    check(awaitValue(secondValue).value.value() == Long.MAX_VALUE)
                } finally {
                    cancelFirst()
                    cancelFirst()
                    cancelSecond()
                }
            }
            if (start == ULong.MAX_VALUE - 2u) {
                try {
                    client.subscribeWatchPrecision(0, select = selection) {}
                    error("subscription ID wrapped after UInt64.max")
                } catch (error: LuxoError) {
                    check(error.error == "RequestIDExhausted")
                }
            }
            println("concurrent subscription IDs $mode after $start passed")
        } finally {
            socket.close()
            http.dispatcher.executorService.shutdownNow()
            http.connectionPool.evictAll()
        }
    }
}

private fun startSubscription(block: suspend () -> (() -> Unit)): CompletableFuture<() -> Unit> {
    val future = CompletableFuture<() -> Unit>()
    block.startCoroutine(object : Continuation<() -> Unit> {
        override val context = EmptyCoroutineContext
        override fun resumeWith(result: Result<() -> Unit>) {
            result.fold({ future.complete(it) }, { future.completeExceptionally(it) })
        }
    })
    return future
}

private suspend fun verifyChannel(
    endpoint: String, websocket: Boolean, mode: TransportMode, values: List<Long>, tokens: List<String>, dates: List<Pair<String, String>>,
    uuidCases: List<JsonElement>,
) {
    val http = OkHttpClient()
    val socket = if (websocket) LuxoWebSocket(endpoint.replace("http:", "ws:"), client = http, autoReconnect = false) else null
    val transport = socket ?: OkHttpTransport(endpoint, client = http, mode = mode)
    transport.setMode(mode)
    val client = LuxoClient(transport)
    try {
        socket?.connect()
        verifyUUIDs(client, uuidCases)
        verifyUnary(client, values)
        verifyTimestamps(client, dates)
        verifyBusinessFloats(client)
        verifyNamedEnums(client, websocket)
        check(client.checkListEffects() == listOf(1L, 2L, 3L, 3L))
        verifyPagination(client)
        verifyOpaqueJSON(client)
        verifyUnicode(client)
        verifyJSONNumbers(client, tokens, websocket)
        verifyErrors(client, values)
        verifyJSONErrors(client, tokens)
        verifyNullableArrays(client, websocket)
        verifyNestedResponses(client, websocket)
        verifyScalarArrays(client, websocket)
        if (websocket) {
            verifyStreams(client, values)
            for (fail in listOf(false, true)) verifyFiniteStream(client, fail)
        }
    } finally {
        socket?.close()
        http.dispatcher.executorService.shutdownNow()
        http.connectionPool.evictAll()
    }
}

private fun jsonFixtureTexts(tokens: List<String>) = tokens.flatMap { token ->
    listOf(token, """{"n":$token,"nested":[$token,null]}""")
}

private suspend fun verifyTimestamps(client: LuxoClient, cases: List<Pair<String, String>>) {
    cases.forEach { (input, expected) -> check(client.echoTimestamp(input) == expected) { "DateTime mismatch: $input" } }
    val inputs = cases.map { it.first }
    val expected = cases.map { it.second }
    check(client.echoTimestamps(inputs) == expected)
    val input = scalarArraysInput(0).copy(dates = inputs)
    check(client.echoScalarArrays(input, select = "dates, tail").dates.value() == expected)
    for (row in client.listScalarArrays(input, select = "dates, tail")) {
        check(row.dates.value() == expected)
        check(row.tail.value() == 42L)
    }
}

private suspend fun verifyBusinessFloats(client: LuxoClient) {
    for (value in listOf(0.0, -0.0, Double.MIN_VALUE, -Double.MIN_VALUE, Double.MAX_VALUE, -Double.MAX_VALUE)) {
        check(client.scaleFloat(value, 1.0).toRawBits() == value.toRawBits())
        val rows = client.scaleFloatList(value, 1.0)
        check(rows.size == 2 && rows.all { it.toRawBits() == value.toRawBits() })
    }
    for (value in listOf(Double.MAX_VALUE, -Double.MAX_VALUE)) {
        for (list in listOf(false, true)) {
            try {
                if (list) client.scaleFloatList(value, 2.0) else client.scaleFloat(value, 2.0)
                error("business Float overflow was accepted")
            } catch (error: LuxoError) {
                check(error.code == 500 && error.error == "Internal" && error.message == "error.internal")
                check(error.data == null && error.developmentCause == null)
            }
            check(client.scaleFloat(1.25, 2.0) == 2.5)
        }
    }
}

private suspend fun verifyJSONNumbers(client: LuxoClient, tokens: List<String>, websocket: Boolean) {
    for (text in jsonFixtureTexts(tokens)) {
        val value = Json.parseToJsonElement(text)
        check(client.echoJSON(value).toString() == text)
        check(client.echoJSONs(listOf(value)).single().toString() == text)
        if (websocket) verifyJSONStreams(client, value, text)
    }
}

private suspend fun verifyJSONErrors(client: LuxoClient, tokens: List<String>) {
    for (text in jsonFixtureTexts(tokens)) {
        verifyJSONError(client, Json.parseToJsonElement(text), text)
    }
}

private suspend fun verifyNamedEnums(client: LuxoClient, websocket: Boolean) {
    for (value in listOf("OPEN", "CLOSED")) check(client.echoFixtureState(value) == value)
    for (values in listOf(emptyList(), listOf("OPEN"), listOf("CLOSED", "OPEN"))) {
        check(client.echoFixtureStates(values) == values)
    }
    if (!websocket) return
    val value = CompletableFuture<String>()
    val cancel = client.subscribeWatchFixtureState { value.complete(it) }
    try {
        check(awaitValue(value) == "OPEN")
    } finally {
        cancel()
    }
    val values = CompletableFuture<List<String>>()
    val cancelList = client.subscribeWatchFixtureStates { values.complete(it) }
    try {
        check(awaitValue(values) == listOf("OPEN", "CLOSED"))
    } finally {
        cancelList()
    }
}

private suspend fun verifyOpaqueJSON(client: LuxoClient) {
    val values = listOf(
        "9007199254740993", "9223372036854775807", "18446744073709551617", "-18446744073709551617",
        "0.123456789012345678901234567890", "1e400", "1e-400", "-0.0",
        """{"value":18446744073709551617,"nested":[18446744073709551617,-18446744073709551617]}""",
    ).map { Json.parseToJsonElement(it) }
    for (value in values) check(client.echoJSON(value) == value)
    check(client.echoJSONs(values) == values)
    check(client.echoJSONs(emptyList()).isEmpty())
}

private suspend fun verifyUnicode(client: LuxoClient) {
    for (text in listOf("東京 café", "\uD800\uDC00", "\uDBFF\uDFFF", "\uFFFD\uFEFF\u0000", "e\u0301", "\\uD800")) {
        val input = PrecisionInput(Long.MAX_VALUE, Long.MIN_VALUE, null, emptyList(), text, 42)
        check(client.echoPrecision(input, select = "label").label.value() == text)
        check(client.listPrecision(input, select = "label").all { it.label.value() == text })
        val value = buildJsonObject { put(text, JsonPrimitive(text)); put("number", Json.parseToJsonElement("123456789012345678901234567890")) }
        check(client.echoJSON(value) == value)
        check(client.echoJSONs(listOf(value)) == listOf(value))
    }
}

private suspend fun verifyErrors(client: LuxoClient, values: List<Long>) {
    for (value in values) {
        try {
            client.rejectPrecision(value)
            error("Expected PrecisionRejected")
        } catch (error: LuxoError) {
            check(error.error == "PrecisionRejected" && error.code == 422)
            check(error.message == "error.precision_rejected" && !error.traceId.isNullOrEmpty())
            val data = checkNotNull(error.data).jsonObject
            check(data.getValue("value").jsonPrimitive.content.toLong() == value)
            check(data.getValue("label").jsonPrimitive.content == "精度 café")
            check(error.developmentCause == null)
        }
        check(client.echoInteger(value) == value)
    }
}

private suspend fun verifyJSONError(client: LuxoClient, value: JsonElement, text: String) {
    try {
        client.rejectJSON(value)
        error("Expected JSONRejected")
    } catch (error: LuxoError) {
        check(error.error == "JSONRejected" && error.code == 422)
        check(error.message == "error.json_rejected" && !error.traceId.isNullOrEmpty())
        check(checkNotNull(error.data).jsonObject.getValue("value").toString() == text)
        check(error.developmentCause == null)
    }
    check(client.echoJSON(value).toString() == text)
}

private suspend fun verifyJSONStreams(client: LuxoClient, value: JsonElement, text: String) {
    val received = CompletableFuture<JsonElement>()
    val cancel = client.subscribeWatchJSON(value) { received.complete(it) }
    try {
        check(awaitValue(received).toString() == text)
    } finally {
        cancel()
    }
    val rows = CompletableFuture<List<JsonElement>>()
    val cancelList = client.subscribeWatchJSONs(listOf(value, JsonNull)) { rows.complete(it) }
    try {
        check(awaitValue(rows).map { it.toString() } == listOf(text, "null"))
    } finally {
        cancelList()
    }
}

private suspend fun verifyFiniteStream(client: LuxoClient, fail: Boolean, reuse: Boolean = true) {
    val value = Json.parseToJsonElement("""{"n":123456789012345678901234567890,"f":1.2345678901234567890123456789,"zero":-0}""")
    val rows = mutableListOf<List<JsonElement>>()
    val terminal = CompletableFuture<LuxoError?>()
    var notifications = 0
    val observer = StreamObserver(
        onComplete = { notifications++; terminal.complete(null) },
        onError = { notifications++; terminal.complete(it) },
    )
    val cancel = client.subscribeWatchFiniteJSON(value, fail, observer = observer) { rows += it }
    try {
        val error = awaitValue(terminal)
        check(rows.size == 64) { "terminal overtook accepted messages: ${rows.size}" }
        rows.forEachIndexed { index, row ->
            check(row.size == 2 && row[0].jsonPrimitive.content == index.toString())
            check(row[1].toString() == value.toString())
        }
        if (fail) {
            check(error?.error == "JSONRejected" && error.code == 422)
            check(checkNotNull(error.data).jsonObject.getValue("value").toString() == value.toString())
        } else check(error == null)
        if (reuse) check(client.echoJSON(value).toString() == value.toString())
        check(notifications == 1)
    } finally {
        cancel()
        cancel()
    }
}

private suspend fun verifyTerminalMaximumIDs(endpoint: String) {
    for (mode in TransportMode.entries) for (fail in listOf(false, true)) {
        val http = OkHttpClient()
        val socket = LuxoWebSocket(endpoint.replace("http:", "ws:"), http, ULong.MAX_VALUE - 1u, 5_000)
        socket.setMode(mode)
        try {
            verifyFiniteStream(LuxoClient(socket), fail, reuse = false)
            println("terminal UInt64.max $mode failure=$fail passed")
        } finally {
            socket.close()
            http.dispatcher.executorService.shutdownNow()
            http.connectionPool.evictAll()
        }
    }
}

private data class PageCase(val page: Long?, val size: Long?, val expectedPage: Long, val expectedSize: Long, val ids: List<Long>)

private suspend fun verifyPagination(client: LuxoClient) {
    val cases = listOf(
        PageCase(null, null, 1, 2, listOf(1, 2)), PageCase(2, 2, 2, 2, listOf(3)),
        PageCase(3, 2, 3, 2, emptyList()), PageCase(9007199254740993, 2, 9007199254740993, 2, emptyList()),
        PageCase(Long.MAX_VALUE, 2, Long.MAX_VALUE, 2, emptyList()),
        PageCase(0, 2, 1, 2, listOf(1, 2)), PageCase(-1, 2, 1, 2, listOf(1, 2)),
        PageCase(1, 0, 1, 0, listOf(1, 2, 3)), PageCase(1, 100, 1, 100, listOf(1, 2, 3)),
        PageCase(1, -1, 1, 20, listOf(1, 2, 3)), PageCase(1, 101, 1, 20, listOf(1, 2, 3)),
    )
    for (value in listOf(Long.MIN_VALUE, Long.MAX_VALUE)) {
        for (case in cases) {
            val result = client.browsePrecision(value, page = case.page, pageSize = case.size, select = "id, value")
            check(result.total == 3L && result.page == case.expectedPage && result.pageSize == case.expectedSize)
            check(result.items.size == case.ids.size)
            result.items.forEachIndexed { index, row ->
                check(row.id.value() == case.ids[index] && row.value.value() == value)
                check(!row.label.isSelected)
            }
        }
    }
}

private suspend fun verifyUnary(client: LuxoClient, values: List<Long>) {
    for (value in values) {
        check(client.echoInteger(value) == value)
        check(client.echoDuration(value) == value)
        for (optional in listOf(null, value)) {
            val input = PrecisionInput(value, value, optional, values, "精度 café", 42)
            verify(client.echoPrecision(input, select = selection), input)
            val rows = client.listPrecision(input, select = selection)
            check(rows.size == 2)
            rows.forEach { verify(it, input) }
        }
    }
    check(client.echoIntegers(values) == values)
    check(client.echoIntegers(emptyList()).isEmpty())
    val input = PrecisionInput(Long.MAX_VALUE, Long.MIN_VALUE, null, emptyList(), "not selected", 0)
    val row = client.echoPrecision(input, select = "value")
    check(row.value.value() == input.value)
    check(!row.duration.isSelected && !row.optional.isSelected && !row.values.isSelected)
    check(!row.label.isSelected && !row.ignored.isSelected)
}

private suspend fun verifyStreams(client: LuxoClient, values: List<Long>) {
    for (value in values) {
        val received = CompletableFuture<Precision>()
        val cancel = client.subscribeWatchPrecision(value, select = selection) { received.complete(it) }
        try {
            verify(awaitValue(received), PrecisionInput(value, value, value, listOf(value), "精度 café", 42))
        } finally {
            cancel()
        }
    }
}

private suspend fun <T> awaitValue(received: CompletableFuture<T>): T = suspendCoroutine { continuation ->
    received.orTimeout(5, TimeUnit.SECONDS).whenComplete { value, error ->
        if (error == null) continuation.resume(value) else continuation.resumeWithException(error)
    }
}

private fun verify(row: Precision, input: PrecisionInput) {
    check(row.value.value() == input.value)
    check(row.duration.value() == input.duration)
    check(row.optional.isSelected && row.optional.value() == input.optional)
    check(row.values.value() == input.values)
    check(row.label.value() == input.label)
    check(!row.ignored.isSelected)
}

private const val nullableSelection = "values, durations, flags, tail"
private val nullableCases = listOf(
    NullableArraysInput(null, null, null, 42),
    NullableArraysInput(emptyList(), emptyList(), emptyList(), 42),
    NullableArraysInput(listOf(Long.MIN_VALUE, Long.MAX_VALUE), listOf(Long.MIN_VALUE, Long.MAX_VALUE), listOf(true, false), 42),
)

private suspend fun verifyNullableArrays(client: LuxoClient, websocket: Boolean) {
    for ((state, input) in nullableCases.withIndex()) {
        verifyNullable(client.echoNullableArrays(input, select = nullableSelection), input)
        val rows = client.listNullableArrays(input, select = nullableSelection)
        check(rows.size == 2)
        rows.forEach { verifyNullable(it, input) }
        val partials = client.listNullableArrays(input, select = "tail") + client.echoNullableArrays(input, select = "tail")
        for (partial in partials) {
            check(partial.tail.value() == input.tail)
            check(!partial.values.isSelected && !partial.durations.isSelected && !partial.flags.isSelected)
        }
        if (websocket) {
            val received = CompletableFuture<NullableArrays>()
            val cancel = client.subscribeWatchNullableArrays(state.toLong(), select = nullableSelection) { received.complete(it) }
            try { verifyNullable(awaitValue(received), input) } finally { cancel() }
        }
    }
}

private fun verifyNullable(row: NullableArrays, input: NullableArraysInput) {
    check(row.values.isSelected && row.values.value() == input.values)
    check(row.durations.isSelected && row.durations.value() == input.durations)
    check(row.flags.isSelected && row.flags.value() == input.flags)
    check(row.tail.value() == input.tail)
}

private const val nestedGroupSelection = "label, primary { $selection }, optional { $selection }, rows { $selection }, optionalRows { $selection }, tail"
private const val nestedSelection = "group { $nestedGroupSelection }, groups { $nestedGroupSelection }, tail"

private fun nestedInput(state: Int): NestedPrecisionInput {
    val row = PrecisionInput(Long.MAX_VALUE, Long.MIN_VALUE, Long.MAX_VALUE, listOf(Long.MIN_VALUE, Long.MAX_VALUE), "精度 café", 42)
    val group = NestedGroupInput("nested 東京", row, if (state == 0) null else row,
        if (state == 2) listOf(row) else emptyList(),
        when (state) { 0 -> null; 1 -> emptyList(); else -> listOf(row) }, 42)
    return NestedPrecisionInput(group, listOf(group), -42)
}

private suspend fun verifyNestedResponses(client: LuxoClient, websocket: Boolean) {
    for (state in 0..2) {
        val input = nestedInput(state)
        verifyNested(client.echoNestedPrecision(input, select = nestedSelection), input)
        val rows = client.listNestedPrecision(input, select = nestedSelection)
        check(rows.size == 2)
        rows.forEach { verifyNested(it, input) }
        val partials = client.listNestedPrecision(input, select = "tail") + client.echoNestedPrecision(input, select = "tail")
        for (row in partials) {
            check(row.tail.value() == input.tail)
            check(!row.group.isSelected && !row.groups.isSelected)
        }
        if (websocket) {
            val received = CompletableFuture<NestedPrecision>()
            val cancel = client.subscribeWatchNestedPrecision(state.toLong(), select = nestedSelection) { received.complete(it) }
            try { verifyNested(awaitValue(received), input) } finally { cancel() }
        }
    }
}

private fun verifyNested(row: NestedPrecision, input: NestedPrecisionInput) {
    verifyGroup(row.group.value(), input.group)
    val groups = row.groups.value()
    check(groups.size == input.groups.size)
    groups.zip(input.groups).forEach { (actual, expected) -> verifyGroup(actual, expected) }
    check(row.tail.value() == input.tail)
}

private fun verifyGroup(row: NestedGroup, input: NestedGroupInput) {
    check(row.label.value() == input.label)
    verify(row.primary.value(), input.primary)
    val optional = input.optional
    if (optional == null) check(row.optional.value() == null) else verify(checkNotNull(row.optional.value()), optional)
    verifyNestedRows(row.rows.value(), input.rows)
    verifyNestedRows(row.optionalRows.value(), input.optionalRows)
    check(row.tail.value() == input.tail)
}

private fun verifyNestedRows(rows: List<Precision>?, inputs: List<PrecisionInput>?) {
    if (inputs == null) { check(rows == null); return }
    checkNotNull(rows)
    check(rows.size == inputs.size)
    rows.zip(inputs).forEach { (actual, expected) -> verify(actual, expected) }
}

// Malformed messages supplement the live Go round trips above.
private fun verifyNestedBoundaries() {
    for (columnar in listOf(false, true)) {
        fun read(bytes: ByteArray): NestedGroup = if (columnar) decodeColumnarNestedGroup(bytes).single()
            else decodeNestedGroup(LuxoDecoder(bytes))
        for (field in 2..5) {
            val child = if (field < 4) byteArrayOf(0, 1, 84, 0) else byteArrayOf(1, 0, 1, 84, 0)
            fun wrap(payload: ByteArray): ByteArray = (if (columnar) byteArrayOf(1, 0) else byteArrayOf(0)) +
                byteArrayOf(field.toByte()) + (if (field == 3 || field == 5) byteArrayOf(1) else byteArrayOf()) +
                byteArrayOf(payload.size.toByte()) + payload + byteArrayOf(6, 84, 0)
            val valid = wrap(child)
            val row = read(valid)
            check(row.tail.value() == 42L)
            val value = when (field) {
                2 -> row.primary.value(); 3 -> checkNotNull(row.optional.value())
                4 -> row.rows.value().single(); else -> checkNotNull(row.optionalRows.value()).single()
            }
            check(value.value.value() == 42L)
            val invalid = mutableListOf(wrap(child + byteArrayOf(99)), valid + byteArrayOf(99))
            for (end in child.indices) invalid += wrap(child.copyOf(end))
            for (end in valid.indices) invalid += valid.copyOf(end)
            if (field == 3 || field == 5) invalid += valid.copyOf().also { it[if (columnar) 3 else 2] = 2 }
            for (bytes in invalid) {
                var rejected = false
                try { read(bytes) } catch (_: LuxoCodecException) { rejected = true }
                check(rejected) { "accepted malformed nested field $field, columnar=$columnar" }
            }
        }
    }
}

private const val scalarSelection = "ints, floats, strings, bools, dates, durations, uuids, bytes, decimals, jsons, enums, tail"

private fun <T> scalarArrayState(state: Int, values: List<T>): List<T>? = when (state) {
    0 -> null; 1 -> emptyList(); else -> values
}

private fun scalarArraysInput(state: Int) = ScalarArraysInput(
    ints = scalarArrayState(state, listOf(Long.MIN_VALUE, Long.MAX_VALUE)),
    floats = scalarArrayState(state, if (state == 3) listOf(-1.5, 1.25, -0.0, Double.MIN_VALUE, -Double.MIN_VALUE, Double.MAX_VALUE)
        else listOf(-1.5, 1.25)),
    strings = scalarArrayState(state, listOf("", "精度 café 東京")),
    bools = scalarArrayState(state, listOf(true, false)),
    dates = scalarArrayState(state, listOf("1969-12-31T23:59:59Z", "2021-01-01T00:00:00Z")),
    durations = scalarArrayState(state, listOf(Long.MIN_VALUE, Long.MAX_VALUE)),
    uuids = scalarArrayState(state, listOf("00000000-0000-0000-0000-000000000000", "00000000-0000-0000-0000-000000000001")),
    bytes = scalarArrayState(state, listOf(byteArrayOf(), byteArrayOf(0, 127, -128, -1))),
    decimals = scalarArrayState(state, listOf("1234567890.123456789", "-0.000000001")),
    jsons = scalarArrayState(state, listOf(buildJsonObject {
        put("value", JsonPrimitive(Long.MAX_VALUE)); put("text", JsonPrimitive("東京"))
    }, JsonPrimitive(false))),
    enums = scalarArrayState(state, listOf("OPEN", "CLOSED")), tail = 42L,
)

private fun verifyScalarArrayValues(row: ScalarArrays, input: ScalarArraysInput) {
    check(row.ints.value() == input.ints)
    check(row.floats.value()?.map { it.toBits() } == input.floats?.map { it.toBits() })
    check(row.strings.value() == input.strings)
    check(row.bools.value() == input.bools)
    check(row.dates.value() == input.dates)
    check(row.durations.value() == input.durations)
    check(row.uuids.value() == input.uuids)
    check(row.bytes.value()?.map { it.toList() } == input.bytes?.map { it.toList() })
    check(row.decimals.value() == input.decimals)
    check(row.jsons.value() == input.jsons)
    check(row.enums.value() == input.enums)
    check(row.tail.value() == input.tail)
}

private suspend fun verifyScalarArrays(client: LuxoClient, websocket: Boolean) {
    for (state in 0..3) {
        val input = scalarArraysInput(state)
        verifyScalarArrayValues(client.echoScalarArrays(input, select = scalarSelection), input)
        val rows = client.listScalarArrays(input, select = scalarSelection)
        check(rows.size == 2)
        rows.forEach { verifyScalarArrayValues(it, input) }
        val partials = client.listScalarArrays(input, select = "tail") + client.echoScalarArrays(input, select = "tail")
        partials.forEach { row ->
            check(row.tail.value() == 42L)
            check(listOf(row.ints, row.floats, row.strings, row.bools, row.dates, row.durations,
                row.uuids, row.bytes, row.decimals, row.jsons, row.enums).none { it.isSelected })
        }
        if (websocket) {
            val received = CompletableFuture<ScalarArrays>()
            val cancel = client.subscribeWatchScalarArrays(state.toLong(), select = scalarSelection) { received.complete(it) }
            try { verifyScalarArrayValues(awaitValue(received), input) } finally { cancel() }
        }
    }
}
