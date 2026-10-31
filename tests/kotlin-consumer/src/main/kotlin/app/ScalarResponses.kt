package app

import com.luxo.client.*
import com.luxo.generated.LuxoClient
import com.luxo.generated.Role
import com.luxo.generated.decodeColumnarTextRecord
import com.luxo.generated.decodeTextRecord
import com.luxo.generated.decodeJSONTextRecord
import com.luxo.generated.decodeJSONPrecisionRecord
import java.util.Base64
import kotlinx.serialization.json.*

internal class ResponseTransport : Transport {
    var response: Any = JsonNull
    var callback: (Any) -> Unit = {}
    var observer: StreamObserver? = null
    var params: Map<String, Any?> = emptyMap()
    override suspend fun call(api: String, params: Map<String, Any?>): Any {
        this.params = params
        return response
    }
    override suspend fun call(api: String, request: GeneratedRequest): Any = call(api, request.toJSONParameters())
    override suspend fun <T> subscribe(api: String, decoder: (Any) -> T, request: GeneratedRequest,
        observer: StreamObserver?, handler: (T) -> Unit): () -> Unit =
        subscribe(api, decoder, request.toJSONParameters(), observer, handler)
    override suspend fun <T> subscribe(api: String, decoder: (Any) -> T, params: Map<String, Any?>, observer: StreamObserver?, handler: (T) -> Unit): () -> Unit {
        callback = { data -> handler(decoder(data)) }
        this.observer = observer
        return {}
    }
    override fun setSchema(schema: Map<String, APISchemaEntry>) = Unit
    override fun setMode(mode: TransportMode) = Unit
    override fun setToken(token: String) = Unit
}

private data class ScalarResponse(
    val kind: String,
    val list: Boolean,
    val call: suspend LuxoClient.() -> Any,
    val subscribe: suspend LuxoClient.((Any) -> Unit) -> Unit,
)

private fun scalarResponses(): List<ScalarResponse> = listOf(
    ScalarResponse("Int", false, { probeInt() }, { emit -> subscribeWatchProbeInt { emit(it) } }),
    ScalarResponse("Int", true, { probeIntList() }, { emit -> subscribeWatchProbeIntList { emit(it) } }),
    ScalarResponse("Duration", false, { probeDuration() }, { emit -> subscribeWatchProbeDuration { emit(it) } }),
    ScalarResponse("Duration", true, { probeDurationList() }, { emit -> subscribeWatchProbeDurationList { emit(it) } }),
    ScalarResponse("String", false, { probeString() }, { emit -> subscribeWatchProbeString { emit(it) } }),
    ScalarResponse("String", true, { probeStringList() }, { emit -> subscribeWatchProbeStringList { emit(it) } }),
    ScalarResponse("Float", false, { probeFloat() }, { emit -> subscribeWatchProbeFloat { emit(it) } }),
    ScalarResponse("Float", true, { probeFloatList() }, { emit -> subscribeWatchProbeFloatList { emit(it) } }),
    ScalarResponse("Boolean", false, { probeBoolean() }, { emit -> subscribeWatchProbeBoolean { emit(it) } }),
    ScalarResponse("Boolean", true, { probeBooleanList() }, { emit -> subscribeWatchProbeBooleanList { emit(it) } }),
    ScalarResponse("Bytes", false, { probeBytes() }, { emit -> subscribeWatchProbeBytes { emit(it) } }),
    ScalarResponse("Bytes", true, { probeBytesList() }, { emit -> subscribeWatchProbeBytesList { emit(it) } }),
    ScalarResponse("JSON", false, { probeJSON() }, { emit -> subscribeWatchProbeJSON { emit(it) } }),
    ScalarResponse("JSON", true, { probeJSONList() }, { emit -> subscribeWatchProbeJSONList { emit(it) } }),
    ScalarResponse("DateTime", false, { probeDateTime() }, { emit -> subscribeWatchProbeDateTime { emit(it) } }),
    ScalarResponse("DateTime", true, { probeDateTimeList() }, { emit -> subscribeWatchProbeDateTimeList { emit(it) } }),
    ScalarResponse("UUID", false, { probeUUID() }, { emit -> subscribeWatchProbeUUID { emit(it) } }),
    ScalarResponse("UUID", true, { probeUUIDList() }, { emit -> subscribeWatchProbeUUIDList { emit(it) } }),
    ScalarResponse("Decimal", false, { probeDecimal() }, { emit -> subscribeWatchProbeDecimal { emit(it) } }),
    ScalarResponse("Decimal", true, { probeDecimalList() }, { emit -> subscribeWatchProbeDecimalList { emit(it) } }),
    ScalarResponse("Enum", false, { probeEnum() }, { emit -> subscribeWatchProbeEnum { emit(it) } }),
    ScalarResponse("Enum", true, { probeEnumList() }, { emit -> subscribeWatchProbeEnumList { emit(it) } }),
    ScalarResponse("Role", false, { probeRole() }, { emit -> subscribeWatchProbeRole { emit(it) } }),
    ScalarResponse("Role", true, { probeRoleList() }, { emit -> subscribeWatchProbeRoleList { emit(it) } }),
)

suspend fun verifyScalarResponses() {
    verifyEnums()
    verifyStreamObserver()
    verifyJSONKinds()
    verifyUnicodeResponses()
    verifyJSONKindRecords()
    verifyJSONNumericResponses()
    verifyJSONNumericRecords()
    verifyJSONNumericPages()
    for (test in scalarResponses()) verifyResponse(test)
    val transport = ResponseTransport().apply { response = "feffffffffffffffff01".hexToByteArray() }
    val client = LuxoClient(transport)
    check(client.probeImplicit() == Long.MAX_VALUE)
    transport.response = (transport.response as ByteArray) + byteArrayOf(0)
    check(runCatching { client.probeImplicit() }.isFailure)
    verifyTextRecords()
    println("Generated Kotlin responses: 12 scalar kinds, single/list, call/stream, JSON/Binary, truncation and trailing bytes passed")
}

private suspend fun verifyJSONKinds() {
    for (test in scalarResponses().filter { it.kind !in setOf("Int", "Duration", "Float", "JSON") }) {
        val tokens = listOf("0", "1", "9007199254740993", "1.0", "null", "{}", "[]") +
            if (test.kind == "Boolean") listOf("\"true\"", "\"false\"", "\"\"") else listOf("true", "false")
        val transport = ResponseTransport()
        val client = LuxoClient(transport)
        var received = 0
        test.subscribe(client) { received++ }
        for (token in tokens) {
            val value = Json.parseToJsonElement(token)
            transport.response = if (test.list) JsonArray(listOf(value)) else value
            for (result in listOf(runCatching { test.call(client) }, runCatching { transport.callback(transport.response) })) {
                check((result.exceptionOrNull() as? LuxoError)?.error == "ParseError") {
                    "${test.kind} list=${test.list} must reject $token as ParseError, got $result"
                }
            }
        }
        check(received == 0) { "${test.kind} delivered an invalid JSON value" }
    }
}

private suspend fun verifyUnicodeResponses() {
    for (test in scalarResponses().filter { it.kind in setOf("String", "Enum", "Role", "JSON") }) {
        val transport = ResponseTransport()
        val client = LuxoClient(transport)
        var received = 0
        test.subscribe(client) { received++ }
        for (text in listOf("\uD800", "\uDC00", "valid\uDBFF")) {
            val value = if (test.kind == "JSON") buildJsonObject { put(text, text) } else JsonPrimitive(text)
            transport.response = if (test.list) JsonArray(listOf(value)) else value
            check(runCatching { test.call(client) }.isFailure) { "${test.kind} accepted incomplete text" }
            check(runCatching { transport.callback(transport.response) }.isFailure)
        }
        check(received == 0)
    }
    for (text in listOf("\uD800", "\uDC00")) {
        val row = buildJsonObject { put("texts", JsonArray(listOf(JsonPrimitive(text)))) }
        check(runCatching { decodeJSONTextRecord(row) }.isFailure)
        check(runCatching { decodeJSONTextRecord(buildJsonObject { put("child", row) }) }.isFailure)
        check(runCatching { decodeJSONTextRecord(buildJsonObject { put("payload", row) }) }.isFailure)
    }
}

private fun verifyJSONKindRecords() {
    val valid = Json.parseToJsonElement("""{"texts":["false","9007199254740993"],"enabled":null,"flags":[true,false],"state":null,"states":["MEMBER"],"child":{"enabled":true}}""").jsonObject
    val result = decodeJSONTextRecord(valid)
    check(result.texts.value() == listOf("false", "9007199254740993"))
    check(result.enabled.value() == null && result.state.value() == null && !result.payload.isSelected)
    check(result.flags.value() == listOf(true, false) && result.child.value()?.enabled?.value() == true)
    for (field in listOf("texts", "enabled", "flags", "state", "states")) {
        val tokens = listOf("0", "1.0", "{}", "[]") +
            if (field in setOf("enabled", "flags")) listOf("\"true\"", "\"false\"") else listOf("true", "false")
        for (token in tokens) {
            val value = Json.parseToJsonElement(token)
            val row = JsonObject(mapOf(field to if (field in setOf("texts", "flags", "states")) JsonArray(listOf(value)) else value))
            for (data in listOf(row, JsonObject(mapOf("child" to row)))) {
                check((runCatching { decodeJSONTextRecord(data) }.exceptionOrNull() as? LuxoError)?.error == "ParseError") {
                    "structured JSON accepted $data"
                }
            }
        }
    }
}

private suspend fun verifyStreamObserver() {
    val transport = ResponseTransport()
    val client = LuxoClient(transport)
    var completed = 0
    var receivedError: LuxoError? = null
    val observer = StreamObserver({ completed++ }, { receivedError = it })
    client.subscribeWatchProbeInt(observer = observer) {}
    check(transport.observer === observer)
    transport.observer?.onComplete?.invoke()
    val error = LuxoError("Rejected", 409, "no")
    transport.observer?.onError?.invoke(error)
    check(completed == 1 && receivedError === error)
}

private fun verifyJSONNumericRecords() {
    val valid = Json.parseToJsonElement("""{"value":9223372036854775807,"optional":null,"values":[-9223372036854775808],"ratio":-0,"ratios":[5e-324],"optionalRatio":null,"optionalRatios":null}""").jsonObject
    val result = decodeJSONPrecisionRecord(valid)
    check(result.value.value() == Long.MAX_VALUE && result.values.value().single() == Long.MIN_VALUE)
    check(result.ratio.value().toBits() == (-0.0).toBits() && result.ratios.value().single() == Double.MIN_VALUE)
    check(result.optional.value() == null && result.optionalRatio.value() == null && result.optionalRatios.value() == null)
    check(!result.duration.isSelected)
    for (field in listOf("value", "duration", "optional", "values", "ratio", "ratios", "optionalRatio", "optionalRatios")) {
        for (token in listOf("\"1\"", "1e-400", "1e400", "true")) {
            val scalar = Json.parseToJsonElement(token)
            val value = if (field in setOf("values", "ratios", "optionalRatios")) JsonArray(listOf(scalar)) else scalar
            check(runCatching { decodeJSONPrecisionRecord(JsonObject(mapOf(field to value))) }.isFailure) {
                "structured field $field accepted $token"
            }
        }
    }
}

private suspend fun verifyJSONNumericPages() {
    val valid = Json.parseToJsonElement("""{"items":[],"total":1,"page":1,"pageSize":1}""").jsonObject
    val transport = ResponseTransport()
    val client = LuxoClient(transport)
    for (field in listOf("total", "page", "pageSize")) {
        for (token in listOf("\"1\"", "1e0", "1e-400", "9223372036854775808")) {
            transport.response = JsonObject(valid + (field to Json.parseToJsonElement(token)))
            check(runCatching { client.precisionPage(1, 0, null, emptyList(), 1, 1, select = "value") }.isFailure) {
                "page metadata $field accepted $token"
            }
        }
    }
}

private suspend fun verifyJSONNumericResponses() {
    var rejected = 0
    val failures = mutableListOf<String>()
    for (test in scalarResponses().filter { it.kind in setOf("Int", "Duration", "Float") }) {
        val invalid = listOf("\"1\"", "true", "null", "{}", "[]", "1e400", "1e-400", "+1", "01") +
            if (test.kind == "Float") listOf("NaN", "Infinity", "1f", "0x1.0p0")
            else listOf("1.0", "1e0", "9223372036854775808", "-9223372036854775809")
        val transport = ResponseTransport()
        val client = LuxoClient(transport)
        var received = 0
        test.subscribe(client) { received++ }
        for (token in invalid) {
            val value = Json.parseToJsonElement(token)
            transport.response = if (test.list) JsonArray(listOf(value)) else value
            for ((name, result) in listOf(
                "call" to runCatching { test.call(client) },
                "stream" to runCatching { transport.callback(transport.response) },
            )) {
                if (result.isSuccess) failures += "${test.kind} list=${test.list} $name accepted $token"
                else rejected++
            }
        }
        if (received != 0) failures += "${test.kind} invoked callback for invalid numeric data"
    }
    check(failures.isEmpty()) { failures.joinToString("\n") }
    println("Generated Kotlin numeric responses: $rejected invalid call/stream values rejected")
}

private suspend fun verifyResponse(test: ScalarResponse) {
    val transport = ResponseTransport()
    val client = LuxoClient(transport)
    val element = wireValue(test.kind)
    val wire = if (test.list) byteArrayOf(2) + element + element else element
    val value = jsonValue(test.kind)
    val expected = if (test.list) JsonArray(listOf(value, value)) else value
    var received = 0
    test.subscribe(client) { actual -> check(normalized(actual) == expected); received++ }
    for (data in listOf(expected, wire)) {
        transport.response = data
        check(normalized(test.call(client)) == expected) { test.kind }
        transport.callback(data)
    }
    check(received == 2)
    for (invalid in wire.indices.map { wire.copyOf(it) } + listOf(wire + byteArrayOf(0)) + malformed(test.kind, test.list)) {
        transport.response = invalid
        check(runCatching { test.call(client) }.isFailure) { test.kind + " accepted bad response " + invalid.toHexString() }
        check(runCatching { transport.callback(invalid) }.isFailure) { test.kind + " accepted bad stream value" }
        check(received == 2)
    }
    if (test.list) {
        for (data in listOf(JsonArray(emptyList()), byteArrayOf(0))) {
            transport.response = data
            check(normalized(test.call(client)) == JsonArray(emptyList()))
        }
    }
}

private fun normalized(value: Any?): JsonElement = when (value) {
    null -> JsonNull
    is JsonElement -> value
    is ByteArray -> JsonPrimitive(Base64.getEncoder().encodeToString(value))
    is String -> JsonPrimitive(value)
    is Role -> JsonPrimitive(value.rawValue)
    is Boolean -> JsonPrimitive(value)
    is Number -> JsonPrimitive(value)
    is List<*> -> JsonArray(value.map { normalized(it) })
    else -> error("Unexpected response type")
}

private fun wireValue(kind: String): ByteArray = when (kind) {
    "Int" -> "feffffffffffffffff01".hexToByteArray()
    "Duration" -> "ffffffffffffffffff01".hexToByteArray()
    "Float" -> "000000000000f43f".hexToByteArray()
    "Boolean" -> byteArrayOf(1)
    "DateTime" -> byteArrayOf(0, 0)
    "UUID" -> "531f2d0020e547f2b87422b6fc76328b".hexToByteArray()
    "Bytes" -> byteArrayOf(3, 0, -1, -128)
    "JSON" -> field(jsonValue(kind).toString().encodeToByteArray())
    else -> field(jsonValue(kind).jsonPrimitive.content.encodeToByteArray())
}

private fun jsonValue(kind: String): JsonElement = when (kind) {
    "Int" -> JsonPrimitive(Long.MAX_VALUE)
    "Duration" -> JsonPrimitive(Long.MIN_VALUE)
    "Float" -> JsonPrimitive(1.25)
    "Boolean" -> JsonPrimitive(true)
    "DateTime" -> JsonPrimitive("1970-01-01T00:00:00Z")
    "UUID" -> JsonPrimitive("531f2d00-20e5-47f2-b874-22b6fc76328b")
    "Bytes" -> JsonPrimitive("AP+A")
    "JSON" -> Json.parseToJsonElement("""{"n":9223372036854775808,"fraction":0.1234567890123456789}""")
    "Decimal" -> JsonPrimitive("12345678901234567890.12345")
    "Enum", "Role" -> JsonPrimitive("MEMBER")
    else -> JsonPrimitive("\ufeff发布\u0000\ufffd")
}

private fun malformed(kind: String, list: Boolean): List<ByteArray> {
    val values = when (kind) {
        "Int", "Duration" -> listOf(ByteArray(10) { -1 })
        "Boolean" -> listOf(byteArrayOf(2))
        "String", "Decimal", "Enum", "Role" -> listOf(byteArrayOf(1, -1), byteArrayOf(2, -64, -128))
        "JSON" -> listOf(byteArrayOf(3, 34, -1, 34), byteArrayOf(1, 123), byteArrayOf(0))
        else -> emptyList()
    }
    return if (list) values.map { byteArrayOf(1) + it } else values
}

private fun field(bytes: ByteArray): ByteArray {
    require(bytes.size < 128)
    return byteArrayOf(bytes.size.toByte()) + bytes
}

private fun verifyTextRecords() {
    val text = byteArrayOf(1) + field("\ufeff发布\ufffd".encodeToByteArray())
    val json = field("""{"n":9223372036854775808}""".encodeToByteArray())
    val row = byteArrayOf(0, 1) + text + byteArrayOf(2) + json + byteArrayOf(3, 1, 1) + json + byteArrayOf(0)
    val value = decodeTextRecord(LuxoDecoder(row))
    check(value.texts.value().single() == "\ufeff发布\ufffd")
    check(value.payload.value().jsonObject.getValue("n").jsonPrimitive.content == "9223372036854775808")
    check(value.payloads.value()!!.single() == value.payload.value())
    val column = byteArrayOf(1, 0, 1) + field(text) + byteArrayOf(2) + json + byteArrayOf(3, 1) + field(byteArrayOf(1) + json) + byteArrayOf(0)
    check(decodeColumnarTextRecord(column).single() == value)
    for (bytes in listOf(byteArrayOf(1, 1, -1), text + byteArrayOf(0))) {
        check(runCatching { decodeColumnarTextRecord(byteArrayOf(1, 0, 1) + field(bytes) + byteArrayOf(0)) }.isFailure)
    }
    for (bytes in listOf(byteArrayOf(3, 34, -1, 34), byteArrayOf(1, 123), byteArrayOf(0))) {
        check(runCatching { decodeTextRecord(LuxoDecoder(byteArrayOf(0, 2) + bytes + byteArrayOf(0))) }.isFailure)
        check(runCatching { decodeColumnarTextRecord(byteArrayOf(1, 0, 2) + bytes + byteArrayOf(0)) }.isFailure)
        val cell = byteArrayOf(1) + bytes
        check(runCatching { decodeColumnarTextRecord(byteArrayOf(1, 0, 3, 1) + field(cell) + byteArrayOf(0)) }.isFailure)
    }
}
