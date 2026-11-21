package app

import com.luxo.client.*
import com.luxo.generated.*
import kotlinx.serialization.json.*

// These decoder fixtures inspect whole results rather than statically accessed fields.
private const val textProjection = "enabled, texts, payload, child { enabled }"
private const val precisionProjection = "value, optional"

suspend fun verifyJSONProjection() {
    val transport = ResponseTransport()
    val client = LuxoClient(transport)
    var delivered = 0
    val stop = client.subscribeWatchTextRecords(select = textProjection) { delivered++ }
    for (bad in listOf("{}", """{"unknown":true}""")) {
        val valid = """{"value":1,"enabled":true}"""
        for (json in listOf("[$bad]", "[$valid,$bad]", "[$bad,$valid]", "[$valid,$bad,$valid]")) {
            transport.response = Json.parseToJsonElement(json)
            check((runCatching { client.probeTextRecords(select = textProjection) }.exceptionOrNull() as? LuxoError)?.error == "ParseError") {
                "fieldless list was not rejected as ParseError: $json"
            }
            check((runCatching { transport.callback(transport.response) }.exceptionOrNull() as? LuxoError)?.error == "ParseError")
            transport.response = Json.parseToJsonElement("""{"items":$json,"total":3,"page":1,"pageSize":3}""")
            check((runCatching { client.precisionPage(0, 0, null, emptyList(), select = precisionProjection) }.exceptionOrNull() as? LuxoError)?.error == "ParseError")
        }
    }
    check(delivered == 0)
    for (json in listOf("[null]", "[1]", "[true]", "[[]]", "[\"text\"]")) {
        transport.response = Json.parseToJsonElement(json)
        check(runCatching { client.probeTextRecords(select = textProjection) }.isFailure)
        check(runCatching { transport.callback(transport.response) }.isFailure)
    }
    check(delivered == 0)
    stop()
    verifyNestedProjection()
    verifySingleLookup()
    verifyValidProjection(client, transport)
    println("Generated Kotlin JSON lists: empty/unknown-only records rejected, valid/null/generic JSON preserved")
}

private fun verifySingleLookup() {
    val source = mapOf<String, JsonElement>("label" to JsonNull)
    val accesses = mutableListOf<String>()
    val tracked = object : Map<String, JsonElement> by source {
        override fun containsKey(key: String): Boolean = error("Field lookup must not be repeated")
        override fun get(key: String): JsonElement? {
            accesses += key
            return source[key]
        }
    }
    val value = decodeJSONProjection(JsonObject(tracked))
    check(value.label.isSelected && value.label.value() == null)
    check(accesses == listOf("label", "children", "models", "child"))
    val names = decodeJSONProjectionNames(Json.parseToJsonElement(
        """{"label":null,"labelJSON":"one","hasField":"two","requireField":"three","result":"four"}""",
    ).jsonObject)
    check(names.label.isSelected && names.label.value() == null)
    check(names.labelJSON.value() == "one" && names.hasField.value() == "two")
    check(names.requireField.value() == "three" && names.result.value() == "four")
    val elements = listOf(JsonPrimitive("one"), JsonPrimitive("two"))
    val sequential = object : AbstractList<JsonElement>() {
        override val size: Int get() = elements.size
        override fun get(index: Int): JsonElement = error("Array decoding must support sequential lists")
        override fun iterator(): Iterator<JsonElement> = elements.iterator()
    }
    check(decodeJSONTextRecord(JsonObject(mapOf("texts" to JsonArray(sequential)))).texts.value() == listOf("one", "two"))
}

private fun verifyNestedProjection() {
    for (field in listOf("models", "children")) {
        for (row in listOf("{}", """{"unknown":true}""")) {
            val nested = """{"$field":[$row]}"""
            for (json in listOf(nested, """{"child":$nested}""", """{"children":[$nested]}""")) {
                check((runCatching { decodeJSONProjection(Json.parseToJsonElement(json).jsonObject) }
                    .exceptionOrNull() as? LuxoError)?.error == "ParseError")
            }
        }
    }
    val valid = listOf(
        "{}" to Projection(),
        """{"children":[]}""" to Projection(children = Selected.value(emptyList())),
        """{"children":[{"label":null}]}""" to Projection(children = Selected.value(listOf(Projection(label = Selected.value(null))))),
        """{"models":[{"enabled":null}]}""" to Projection(models = Selected.value(listOf(TextRecord(enabled = Selected.value(null))))),
        """{"children":[{"child":{}},{"models":[]}]}""" to Projection(children = Selected.value(listOf(
            Projection(child = Selected.value(Projection())), Projection(models = Selected.value(emptyList())),
        ))),
    )
    for ((json, expected) in valid) {
        check(decodeJSONProjection(Json.parseToJsonElement(json).jsonObject) == expected)
    }
    val unary = ::decodeJSONProjection
    check(listOf(JsonObject(emptyMap())).map(unary).single() == Projection())
}

private suspend fun verifyValidProjection(client: LuxoClient, transport: ResponseTransport) {
    var received: List<TextRecord>? = null
    val stop = client.subscribeWatchTextRecords(select = textProjection) { received = it }
    val valid = listOf(
        "[]" to emptyList(),
        """[{"enabled":null}]""" to listOf(TextRecord(enabled = Selected.value(null))),
        """[{"texts":[]}]""" to listOf(TextRecord(texts = Selected.value(emptyList()))),
        """[{"payload":{}},{"child":{}}]""" to listOf(TextRecord(payload = Selected.value(JsonObject(emptyMap()))),
            TextRecord(child = Selected.value(TextRecord()))),
    )
    for ((json, expected) in valid) {
        val rows = Json.parseToJsonElement(json).jsonArray
        transport.response = rows
        check(client.probeTextRecords(select = textProjection) == expected)
        transport.callback(rows)
        check(received == expected)
    }
    for ((rows, expected) in listOf("[]" to emptyList(),
        """[{"optional":null}]""" to listOf(PrecisionRecord(optional = Selected.value(null))),
        """[{"value":9223372036854775807}]""" to listOf(PrecisionRecord(value = Selected.value(Long.MAX_VALUE))))) {
        transport.response = Json.parseToJsonElement("""{"items":$rows,"total":1,"page":1,"pageSize":1}""")
        check(client.precisionPage(0, 0, null, emptyList(), select = precisionProjection).items == expected)
    }
    transport.response = Json.parseToJsonElement("""[{},null,{"unknown":[]}]""")
    check(JsonArray(client.probeJSONList()) == transport.response)
    stop()
}
