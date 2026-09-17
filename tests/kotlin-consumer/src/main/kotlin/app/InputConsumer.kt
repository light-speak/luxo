package app

import com.luxo.client.LuxoEncoder
import com.luxo.client.decodeLuxoJSON
import com.luxo.client.encodeLuxoJSON
import com.luxo.generated.EncodingChild
import com.luxo.generated.EncodingInput
import kotlinx.serialization.json.*

/** No serialization compiler plugin is applied in this independent consumer. */
fun verifyGeneratedInputs() {
    val input = encodingInput()
    val actual = input.toLuxoJson().jsonObject
    check(actual.getValue("number").jsonPrimitive.long == Long.MAX_VALUE)
    check(actual.getValue("duration").jsonPrimitive.long == Long.MIN_VALUE)
    check(actual.getValue("ratio").jsonPrimitive.double == input.ratio)
    check(actual.getValue("flag").jsonPrimitive.boolean)
    for (name in listOf("text", "date", "uuid", "decimal", "role")) check(actual.getValue(name).jsonPrimitive.isString)
    check(actual.getValue("blob").jsonPrimitive.content == "AP+A")
    check(actual.getValue("payload") === input.payload)
    check(actual.getValue("child").jsonObject.getValue("value").jsonPrimitive.long == Long.MIN_VALUE)
    val scalars = listOf("number", "duration", "ratio", "text", "flag", "date", "uuid", "decimal", "blob", "payload", "role", "child")
    val arrays = listOf("integers", "durations", "ratios", "texts", "flags", "dates", "uuids", "decimals", "blobs", "payloads", "roles", "children")
    for ((scalar, array) in scalars.zip(arrays)) check(actual.getValue(array).jsonArray.single() == actual.getValue(scalar))
    check(actual.getValue("optional") === JsonNull)
    check(Json.parseToJsonElement(actual.toString()) == actual)
    val nullable = input.copy(child = null, children = null, optional = Long.MAX_VALUE).toLuxoJson().jsonObject
    check(nullable.getValue("child") === JsonNull && nullable.getValue("children") === JsonNull)
    check(nullable.getValue("optional").jsonPrimitive.long == Long.MAX_VALUE)
    val empty = input.copy(integers = emptyList(), blobs = emptyList(), children = emptyList()).toLuxoJson().jsonObject
    for (name in listOf("integers", "blobs", "children")) check(empty.getValue(name).jsonArray.isEmpty())
    for (value in listOf(Double.NaN, Double.POSITIVE_INFINITY, Double.NEGATIVE_INFINITY)) {
        check(runCatching { input.copy(ratio = value).toLuxoJson() }.isFailure)
        check(runCatching { input.copy(ratios = listOf(value)).toLuxoJson() }.isFailure)
    }
    val encoder = LuxoEncoder()
    input.writeLuxo(encoder)
    check(encoder.bytes().isNotEmpty())
    verifyUnicodeInputs(input)
    verifyArrayWriter()
    println("Generated Kotlin inputs: all scalar/list kinds, nullable, nested and finite JSON checks passed")
}

private fun verifyArrayWriter() {
    val encoder = LuxoEncoder().apply { writeFieldArray(1, listOf("東京", "valid"), ::writeString) }
    val prefix = encoder.bytes()
    val failure = IllegalStateException("consumer callback failed")
    val actual = runCatching {
        encoder.writeFieldArray(2, listOf("first", "second")) {
            encoder.writeString(it)
            if (it == "second") throw failure
        }
    }.exceptionOrNull()
    check(actual === failure)
    check(encoder.bytes().contentEquals(prefix))
}

private fun verifyUnicodeInputs(input: EncodingInput) {
    for (text in listOf("\uD800", "\uDC00", "valid\uDBFF")) {
        val payload = buildJsonObject { put(text, text) }
        val invalid = listOf(
            input.copy(text = text), input.copy(texts = listOf("valid", text)),
            input.copy(role = text), input.copy(roles = listOf(text)),
            input.copy(decimal = text), input.copy(decimals = listOf(text)),
            input.copy(payload = payload), input.copy(payloads = listOf(payload)),
        )
        for (value in invalid) {
            // Projection is a tree, not the output boundary; validate the single encoding.
            check(runCatching { encodeLuxoJSON(value.toLuxoJson()) }.isFailure)
            check(runCatching { value.writeLuxo(LuxoEncoder()) }.isFailure)
        }
    }
    for (text in listOf("東京", "\uD800\uDC00", "\uDBFF\uDFFF", "\uFFFD\uFEFF", "\\uD800")) {
        val value = input.copy(text = text, texts = listOf(text)).toLuxoJson()
        check(decodeLuxoJSON(encodeLuxoJSON(value)) == value)
    }
}

private fun encodingInput(): EncodingInput {
    val date = "2026-09-14T00:00:00Z"
    val uuid = "531f2d00-20e5-47f2-b874-22b6fc76328b"
    val decimal = "12345678901234567890.123456789"
    val blob = byteArrayOf(0, -1, -128)
    val payload = Json.parseToJsonElement("""{"full":9223372036854775807,"beyond":9223372036854775808,"text":"精度"}""")
    val child = EncodingChild(Long.MIN_VALUE)
    return EncodingInput(
        number = Long.MAX_VALUE, duration = Long.MIN_VALUE, ratio = 1.25, text = "精度 café", flag = true,
        date = date, uuid = uuid, decimal = decimal, blob = blob, payload = payload, role = "MEMBER", child = child,
        integers = listOf(Long.MAX_VALUE), durations = listOf(Long.MIN_VALUE), ratios = listOf(1.25), texts = listOf("精度 café"),
        flags = listOf(true), dates = listOf(date), uuids = listOf(uuid), decimals = listOf(decimal), blobs = listOf(blob),
        payloads = listOf(payload), roles = listOf("MEMBER"), children = listOf(child),
    )
}
