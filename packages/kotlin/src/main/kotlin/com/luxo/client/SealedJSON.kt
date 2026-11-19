package com.luxo.client

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject

/** Inspect every raw occurrence before JsonObject can discard duplicate members. */
internal fun validateSealedResponseJSON(text: String, schema: APISchemaEntry?) {
    val root = schema?.responseType ?: return
    SealedJSONReader(text, schema.types).envelope(root)
}

internal fun decodeResponseJSON(text: String, schema: APISchemaEntry?): JsonObject {
    if (schema?.responseType == null) return decodeLuxoJSON(text).jsonObject
    requireJSONTokens(requireJSONUnicode(text))
    validateSealedResponseJSON(text, schema)
    return Json.parseToJsonElement(text).jsonObject
}

internal fun decodeSealedWebSocketJSON(text: String, schema: (Long, Boolean) -> APISchemaEntry?): JsonObject {
    requireJSONTokens(requireJSONUnicode(text))
    val reader = SealedJSONReader(text, emptyMap())
    val id = reader.requestID()
    if (id != null) validateSealedResponseJSON(text, schema(id.first, id.second))
    return Json.parseToJsonElement(text).jsonObject
}

private class SealedJSONReader(
    private val text: String,
    private val types: Map<String, Map<String, SelectionFieldSchema>>,
) {
    private var offset = 0

    fun envelope(root: SelectionFieldSchema) {
        var error = false
        members { name -> if (name == "error") error = true; skip() }
        if (error) return
        offset = 0
        members { name -> if (name == "data") value(root, 1) else skip() }
    }

    fun requestID(): Pair<Long, Boolean>? {
        var call: Long? = null
        var stream: Long? = null
        var hasStream = false
        members { name ->
            space()
            val start = offset
            skip()
            if (name == "\$id" || name == "\$stream") {
                val id = webSocketJSONRequestID(Json.parseToJsonElement(text.substring(start, offset)))
                if (name == "\$stream") { stream = id; hasStream = true } else call = id
            }
        }
        return if (hasStream) stream?.let { it to true } else call?.let { it to false }
    }

    private fun value(field: SelectionFieldSchema, depth: Int, list: Boolean = field.isList, nullable: Boolean = field.nullable) {
        space()
        if (text.startsWith("null", offset)) {
            require(nullable || (field.type == "JSON" && !list)) { "non-nullable field is null" }
            offset += 4
            return
        }
        if (list) {
            require(take('[')) { "expected JSON array" }
            if (take(']')) return
            do { value(field, depth, list = false, nullable = false) } while (take(','))
            require(take(']')) { "incomplete JSON array" }
            return
        }
        when (field.type) {
            "Sealed" -> sum(requireNotNull(field.typeName), depth)
            "Model" -> model(requireNotNull(field.typeName), depth, false)
            "JSON" -> skip(128)
            else -> skip()
        }
    }

    private fun sum(name: String, depth: Int) {
        require(depth in 1..63) { "model nesting exceeds 64 levels" }
        val cases = types["@sealed:$name"] ?: throw IllegalArgumentException("missing sealed response schema $name")
        var count = 0
        members { key ->
            count++
            require(count == 1) { "$name requires exactly one variant" }
            val variant = cases[key] ?: throw IllegalArgumentException("unknown $name variant $key")
            model(requireNotNull(variant.typeName), depth + 1, true)
        }
        require(count == 1) { "$name requires exactly one variant" }
    }

    private fun model(name: String, depth: Int, closed: Boolean) {
        require(depth in 1..64) { "model nesting exceeds 64 levels" }
        val fields = types[name] ?: throw IllegalArgumentException("missing response schema $name")
        members { key ->
            val field = fields[key]
            if (field == null) {
                require(!closed) { "unknown $name field $key" }
                skip()
            } else value(field, depth + 1)
        }
    }

    private fun members(read: (String) -> Unit) {
        require(take('{')) { "expected JSON object" }
        if (take('}')) return
        do {
            space()
            require(offset < text.length && text[offset] == '"') { "expected JSON member" }
            val end = jsonQuotedTokenEnd(text, offset)
            val key = Json.parseToJsonElement(text.substring(offset, end)).jsonPrimitive.content
            offset = end
            require(take(':')) { "missing JSON colon" }
            read(key)
        } while (take(','))
        require(take('}')) { "incomplete JSON object" }
    }

    /** Skip iteratively, keeping generic JSON's independent container budget. */
    private fun skip(limit: Int = Int.MAX_VALUE) {
        space()
        if (text[offset] == '"') { offset = jsonQuotedTokenEnd(text, offset); return }
        if (text[offset] != '{' && text[offset] != '[') { offset = jsonLiteralEnd(text, offset); return }
        var depth = 0
        do {
            when (text[offset]) {
                '"' -> { offset = jsonQuotedTokenEnd(text, offset); continue }
                '{', '[' -> { depth++; require(depth <= limit) { "JSON nesting exceeds 128 levels" } }
                '}', ']' -> depth--
            }
            offset++
        } while (depth > 0)
    }

    private fun take(char: Char): Boolean {
        space()
        if (offset >= text.length || text[offset] != char) return false
        offset++
        return true
    }

    private fun space() { while (offset < text.length && text[offset] in " \t\r\n") offset++ }
}
