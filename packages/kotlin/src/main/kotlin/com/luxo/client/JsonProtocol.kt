package com.luxo.client

import kotlinx.serialization.json.*

internal fun decodeHTTPJSON(bytes: ByteArray): JsonObject =
    decodeLuxoJSON(decodeJSONDocumentText(bytes)).jsonObject

/** JSON documents use UTF-8; accept one document BOM, never remove U+FEFF from field values. */
internal fun decodeJSONDocumentText(bytes: ByteArray): String {
    val start = if (bytes.size >= 3 && bytes[0] == 0xef.toByte() &&
        bytes[1] == 0xbb.toByte() && bytes[2] == 0xbf.toByte()) 3 else 0
    return bytes.decodeToString(startIndex = start, throwOnInvalidSequence = true)
}

/** Generated inputs encode typed Floats without discovering a serializer. */
fun luxoJsonFloat(value: Double): JsonPrimitive {
    if (!value.isFinite()) throw LuxoError("ConfigError", 0, "Float must be finite")
    return JsonPrimitive(value)
}

/** Preserve the JSON value kind instead of converting numbers, booleans or null to text. */
fun decodeLuxoJsonString(value: JsonElement): String {
    if (value !is JsonPrimitive || !value.isString) invalidJSONKind("string")
    val text = value.content
    if (!isLuxoUnicode(text)) invalidJSONKind("complete Unicode string")
    return text
}

/** Quoted boolean text is not a Boolean response. */
fun decodeLuxoJsonBoolean(value: JsonElement): Boolean {
    if (value !is JsonPrimitive || value.isString) invalidJSONKind("boolean")
    return when (value.content) {
        "true" -> true
        "false" -> false
        else -> invalidJSONKind("boolean")
    }
}

private fun invalidJSONKind(kind: String): Nothing =
    throw LuxoError("ParseError", 0, "expected a JSON $kind")

/** Accept exact signed integer inputs, never Number.toLong() coercion. */
internal fun requireLuxoInt64(value: Any?): Long = when (value) {
    is Long -> value
    is Int -> value.toLong()
    is Short -> value.toLong()
    is Byte -> value.toLong()
    is JsonPrimitive -> if (!value.isString) parseLuxoIntToken(value.content) ?: invalidInteger() else invalidInteger()
    else -> invalidInteger()
}

private fun invalidInteger(): Nothing =
    throw LuxoError("ConfigError", 0, "Int and Duration require an exact signed 64-bit integer")

/** Validate while constructing JSON; do not serialize or parse a second time. */
internal fun JsonObjectBuilder.putLuxoParams(params: Map<String, Any?>, meta: APISchemaEntry?) {
    for ((name, value) in params) {
        val param = meta?.paramsByName?.get(name)
        put(name, if (param == null) luxoJsonValue(value) else typedJsonValue(
            value, param.type, param.isList, param.nullable, param.typeName, meta.types,
        ))
    }
}

private fun typedJsonValue(
    value: Any?, type: String?, isList: Boolean, nullable: Boolean, typeName: String?,
    types: Map<String, Map<String, SelectionFieldSchema>>,
): JsonElement {
    if (type != "Int" && type != "Duration" && type != "Model" && type != "DateTime") return luxoJsonValue(value)
    if (value == null || value === JsonNull) {
        if (!nullable) throw LuxoError("ConfigError", 0, "parameter is not nullable")
        return JsonNull
    }
    if (isList) return typedJsonList(value, type, typeName, types)
    return when (type) {
        "DateTime" -> {
            val text = if (value is String) value else if (value is JsonPrimitive && value.isString) value.content else throw LuxoError("ConfigError", 0, "DateTime must be a string")
            JsonPrimitive(DateTimeCodec.validate(text))
        }
        "Int", "Duration" -> {
            requireLuxoInt64(value)
            luxoJsonValue(value)
        }
        "Model" -> typedJsonObject(value, typeName, types)
        else -> luxoJsonValue(value)
    }
}

private fun typedJsonList(
    value: Any, type: String?, typeName: String?, types: Map<String, Map<String, SelectionFieldSchema>>,
): JsonArray {
    if (value is JsonArray) {
        for (item in value) typedJsonValue(item, type, false, false, typeName, types)
        return value
    }
    return buildJsonArray {
        when (value) {
            is Iterable<*> -> for (item in value) add(typedJsonValue(item, type, false, false, typeName, types))
            is Array<*> -> for (item in value) add(typedJsonValue(item, type, false, false, typeName, types))
            else -> throw LuxoError("ConfigError", 0, "parameter must be a list")
        }
    }
}

private fun typedJsonObject(
    value: Any, typeName: String?, types: Map<String, Map<String, SelectionFieldSchema>>,
): JsonObject {
    val fields = types[typeName]
        ?: throw LuxoError("ConfigError", 0, "no input schema for $typeName")
    val source = if (value is LuxoBinaryEncodable) value.toLuxoJson() else value
    if (source is JsonObject) {
        for ((name, item) in source) typedJsonMember(name, item, fields, types)
        return source
    }
    val members = source as? Map<*, *>
        ?: throw LuxoError("ConfigError", 0, "$typeName must be an object")
    return buildJsonObject {
        for ((name, item) in members) {
            if (name !is String) throw LuxoError("ConfigError", 0, "JSON object keys must be strings")
            put(name, typedJsonMember(name, item, fields, types))
        }
    }
}

private fun typedJsonMember(
    name: String, item: Any?, fields: Map<String, SelectionFieldSchema>,
    types: Map<String, Map<String, SelectionFieldSchema>>,
): JsonElement {
    val field = fields[name] ?: return luxoJsonValue(item)
    return typedJsonValue(item, field.type, field.isList, field.nullable, field.typeName, types)
}

/** Error decoding is shared by HTTP responses and correlated WebSocket frames. */
internal fun decodeLuxoJsonError(json: JsonObject, statusCode: Int): LuxoError? {
    if (!json.containsKey("error")) return null
    return try {
        val code = json["code"] as? JsonPrimitive
        require(code != null && !code.isString) { "error code must be an integer" }
        val parsed = parseLuxoIntToken(code.content) ?: throw IllegalArgumentException("error code must be an integer")
        require(parsed in Int.MIN_VALUE.toLong()..Int.MAX_VALUE.toLong()) { "error code exceeds signed 32-bit range" }
        val exactCode = parsed.toInt()
        LuxoError(
            json.requiredString("error"), exactCode, json.requiredString("message"),
            json.optionalString("traceId"), json["data"]?.let(::decodeLuxoJsonValue), json.optionalString("cause"),
        )
    } catch (error: IllegalArgumentException) {
        LuxoError("ParseError", statusCode, "invalid JSON error response: ${error.message}")
    }
}

private fun JsonObject.requiredString(name: String): String {
    val value = get(name) as? JsonPrimitive
    require(value != null && value.isString) { "$name must be a string" }
    return requireLuxoUnicode(value.content)
}

private fun JsonObject.optionalString(name: String): String? =
    if (get(name) == null || get(name) === JsonNull) null else requiredString(name)
