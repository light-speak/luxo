package com.luxo.client

import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonPrimitive

/** Typed responses must not coerce strings, exponents or fractions to business integers. */
fun decodeLuxoJsonInt(value: JsonElement): Long =
    if (value is JsonPrimitive && !value.isString) parseLuxoIntToken(value.content) ?: invalidJSONNumber("Int")
    else invalidJSONNumber("Int")

/** IEEE-754 rounding is allowed; overflow and nonzero underflow are explicit failures. */
fun decodeLuxoJsonFloat(value: JsonElement): Double {
    if (value !is JsonPrimitive || value.isString) invalidJSONNumber("Float")
    val token = value.content
    if (!isJSONFloatCandidate(token)) invalidJSONNumber("Float")
    val number = try {
        token.toDouble()
    } catch (_: NumberFormatException) {
        invalidJSONNumber("Float")
    }
    if (!number.isFinite() || (number == 0.0 && nonzeroMantissa(token))) invalidJSONNumber("Float")
    return number
}

// Negative accumulation permits Long.MIN_VALUE without overflowing its positive counterpart.
internal fun parseLuxoIntToken(token: String): Long? {
    if (token.isEmpty()) return null
    val negative = token[0] == '-'
    val start = if (negative) 1 else 0
    val length = token.length - start
    if (length !in 1..19 || (length > 1 && token[start] == '0')) return null
    val limit = if (negative) Long.MIN_VALUE else -Long.MAX_VALUE
    val multiplyLimit = limit / 10
    var value = 0L
    for (index in start until token.length) {
        val digit = token[index] - '0'
        if (digit !in 0..9 || value < multiplyLimit) return null
        value *= 10
        if (value < limit + digit) return null
        value -= digit
    }
    return if (negative) value else -value
}

// Double.parseDouble validates the remaining grammar during conversion. Reject
// its non-JSON extensions here: whitespace/signs, hex, leading zeros, suffixes,
// and a decimal point without following digits. Do not rescan every digit.
private fun isJSONFloatCandidate(token: String): Boolean {
    if (token.isEmpty() || token.last() !in '0'..'9') return false
    val start = if (token[0] == '-') 1 else 0
    when (token[start]) {
        '0' -> if (start + 1 < token.length && token[start + 1] !in ".eE") return false
        in '1'..'9' -> Unit
        else -> return false
    }
    val point = token.indexOf('.')
    // The final character is a digit, so a decimal point cannot be last.
    return point < 0 || token[point + 1] in '0'..'9'
}

private fun nonzeroMantissa(token: String): Boolean {
    for (character in token) {
        if (character == 'e' || character == 'E') return false
        if (character in '1'..'9') return true
    }
    return false
}

private fun invalidJSONNumber(type: String): Nothing =
    throw LuxoError("ParseError", 0, "invalid JSON $type numeric syntax or range")
