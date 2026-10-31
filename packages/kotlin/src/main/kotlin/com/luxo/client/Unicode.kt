package com.luxo.client

import kotlinx.serialization.json.*

/** Validate before a platform UTF-8 encoder can silently replace a code unit. */
internal fun requireLuxoUnicode(text: String): String {
    require(isLuxoUnicode(text)) { "String contains an isolated Unicode surrogate" }
    return text
}

internal fun isLuxoUnicode(text: String): Boolean {
    for (index in 0 until text.length) {
        // Surrogates share a five-bit prefix; the platform still handles pairing.
        if (text[index].code and 0xf800 == 0xd800) return isUnicodeTail(text, index)
    }
    return true
}

private fun isUnicodeTail(text: String, start: Int): Boolean {
    var index = start
    while (index < text.length) {
        val point = text.codePointAt(index)
        if (point in 0xd800..0xdfff) return false
        index += Character.charCount(point)
    }
    return true
}

/** The platform handles pairing; an isolated surrogate is returned as one BMP code unit. */
internal fun requireLuxoSurrogatePair(text: String, index: Int) {
    require(text.codePointAt(index) > 0xffff) { "String contains an isolated Unicode surrogate" }
}

/** Validate Unicode only; kotlinx.serialization still owns JSON syntax and number tokens. */
internal fun requireJSONUnicode(text: String): String {
    requireLuxoUnicode(text)
    var slash = text.indexOf('\\')
    while (slash >= 0 && slash + 1 < text.length) {
        val step = if (text[slash + 1] != 'u') 2 else when (surrogateKind(text, slash + 2)) {
            2 -> throw IllegalArgumentException("JSON contains an isolated Unicode surrogate")
            1 -> {
                require(slash + 12 <= text.length && text[slash + 6] == '\\' && text[slash + 7] == 'u' &&
                    surrogateKind(text, slash + 8) == 2) { "JSON contains an isolated Unicode surrogate" }
                12
            }
            else -> 6
        }
        slash = text.indexOf('\\', slash + step)
    }
    return text
}

// Valid surrogate escapes are identified by their first two hex digits. The native
// parser rejects truncated or invalid hex syntax; this check never repairs it.
private fun surrogateKind(text: String, offset: Int): Int {
    if (offset + 4 > text.length || text[offset].code or 32 != 'd'.code) return 0
    val second = text[offset + 1].code or 32
    return when (second) {
        in '8'.code..'9'.code, in 'a'.code..'b'.code -> 1
        in 'c'.code..'f'.code -> 2
        else -> 0
    }
}

/** Preserve raw numbers and reject bad text before duplicate object keys can discard it. */
fun decodeLuxoJSON(text: String): JsonElement = Json.parseToJsonElement(requireJSONUnicode(text))

/** Serialize once, then check keys, strings and raw JSON literals without reparsing. */
fun encodeLuxoJSON(value: JsonElement): String = requireJSONUnicode(value.toString())

/** Protect direct generated decoders and custom transports without copying the JSON tree. */
fun decodeLuxoJsonValue(value: JsonElement): JsonElement {
    requireJSONValueUnicode(value, 0)
    return value
}

private fun requireJSONValueUnicode(value: JsonElement, depth: Int) {
    require(depth <= 128) { "JSON nesting exceeds 128 levels" }
    when (value) {
        is JsonObject -> for ((key, item) in value) {
            requireLuxoUnicode(key)
            requireJSONValueUnicode(item, depth + 1)
        }
        is JsonArray -> for (item in value) requireJSONValueUnicode(item, depth + 1)
        is JsonPrimitive -> if (value.isString) requireLuxoUnicode(value.content) else requireJSONUnicode(value.content)
    }
}
