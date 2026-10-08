package app

import com.luxo.client.*
import com.luxo.generated.*
import kotlinx.serialization.json.*

suspend fun verifyEnums() {
    verifyEnumVariants()
    verifyEnumRecords()
    verifyEnumParameters()
    val transport = ResponseTransport()
    val client = LuxoClient(transport)
    val unknown = Role.fromValue("未来状态")
    for (value in listOf(Role.Known.MEMBER, unknown)) {
        val binary = LuxoEncoder().apply { writeString(value.rawValue) }.bytes()
        for (response in listOf(JsonPrimitive(value.rawValue), binary)) {
            transport.response = response
            check(client.probeRole() == value)
            var received: Role? = null
            client.subscribeWatchProbeRole { received = it }
            transport.callback(response)
            check(received == value)
        }
        val list = LuxoEncoder().apply { writeVarint(2); repeat(2) { writeString(value.rawValue) } }.bytes()
        for (response in listOf(JsonArray(List(2) { JsonPrimitive(value.rawValue) }), list)) {
            transport.response = response
            check(client.probeRoleList() == List(2) { value })
            var received: List<Role>? = null
            client.subscribeWatchProbeRoleList { received = it }
            transport.callback(response)
            check(received == List(2) { value })
        }
    }
    println("Generated Kotlin enums: known/unknown, rows/columns, inputs and streams passed")
}

private fun enumLabel(value: Role): String = when (value) {
    Role.Known.MEMBER -> "member"
    Role.Known.ADMIN -> "admin"
    is Role.Unknown -> value.rawValue
}

private fun verifyEnumVariants() {
    check(Role.fromValue("MEMBER") === Role.Known.MEMBER)
    for (text in listOf("future", "", "member", "未来状态", "\uFFFD")) {
        val value = Role.fromValue(text)
        check(value is Role.Unknown && enumLabel(value) == text)
        check(value == Role.fromValue(text) && value.hashCode() == text.hashCode())
        check(!value.equals(OtherRole.fromValue(text)))
        check(Json.encodeToString(Role.Serializer, value) == JsonPrimitive(text).toString())
        check(Json.decodeFromString(Role.Serializer, JsonPrimitive(text).toString()) == value)
    }
    check(EscapedRole.fromValue("when") === EscapedRole.Known.`when`)
    check(EscapedRole.fromValue("name") === EscapedRole.Known.`name$`)
    check(EscapedRole.fromValue("rawValue") === EscapedRole.Known.`rawValue$`)
    check(EscapedRole.fromValue("entries") === EscapedRole.Known.`entries$`)
    check(EscapedRole.fromValue("Known") === EscapedRole.Known.`Known$`)
}

private fun verifyEnumRecords() {
    val future = Role.fromValue("未来状态")
    val input = EnumRecordInput(future, null, listOf(Role.Known.MEMBER, future), null)
    val record = decodeJSONEnumRecord(input.toLuxoJson().jsonObject)
    check(record.state.value() == future && record.optional.value() == null)
    check(record.states.value() == input.states && record.optionalStates.value() == null)
    val encoder = LuxoEncoder().apply { input.writeLuxo(this) }
    check(decodeEnumRecord(LuxoDecoder(encoder.bytes())) == record)
    val absent = decodeJSONEnumRecord(JsonObject(emptyMap()))
    check(!absent.state.isSelected && !absent.optional.isSelected)
    val column = LuxoEncoder().apply {
        writeVarint(1); writeVarint(0)
        writeVarint(1); writeString(future.rawValue)
        writeVarint(2); writeBool(false)
        writeVarint(3); writeBytes(LuxoEncoder().apply {
            writeVarint(2); writeString("MEMBER"); writeString(future.rawValue)
        }.bytes())
        writeVarint(4); writeBool(false); writeEnd()
    }.bytes()
    check(decodeColumnarEnumRecord(column).single() == record)
    for (text in listOf("\uD800", "\uDC00")) {
        check(runCatching { decodeJSONEnumRecord(buildJsonObject { put("state", text) }) }.isFailure)
        check(runCatching { input.copy(state = Role.fromValue(text)).writeLuxo(LuxoEncoder()) }.isFailure)
        check(runCatching { encodeLuxoJSON(input.copy(state = Role.fromValue(text)).toLuxoJson()) }.isFailure)
    }
}

private suspend fun verifyEnumParameters() {
    val transport = ResponseTransport().apply { response = JsonPrimitive("MEMBER") }
    val client = LuxoClient(transport)
    client.enumOptions(required = null)
    check(transport.params == mapOf("required" to null))
    client.enumOptions(required = Role.Known.ADMIN, optional = LuxoOptional.Present(null), states = LuxoOptional.Present(null))
    check(transport.params == mapOf("required" to "ADMIN", "optional" to null, "states" to null))
    val future = Role.fromValue("未来状态")
    val values = mutableListOf<Role>(Role.Known.MEMBER, future)
    client.enumOptions(required = future, state = Role.Known.ADMIN,
        optional = LuxoOptional.Present(future), states = LuxoOptional.Present(values))
    check(transport.params == mapOf("required" to future.rawValue, "state" to "ADMIN",
        "optional" to future.rawValue, "states" to listOf("MEMBER", future.rawValue)))
    values.clear()
    check(transport.params["states"] == listOf("MEMBER", future.rawValue))
}
