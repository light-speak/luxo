package com.luxo.client

import kotlinx.serialization.ExperimentalSerializationApi
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import java.util.concurrent.atomic.AtomicLong

/** Long carries the uint64 bit pattern, just as the existing binary varint codec does. */
internal fun nextWebSocketRequestID(counter: AtomicLong): Long {
    while (true) {
        val previous = counter.get()
        if (previous == -1L) throw LuxoError("RequestIDExhausted", 0, "WebSocket request IDs exhausted")
        val next = previous + 1
        if (counter.compareAndSet(previous, next)) return next
    }
}

/** Keep ordinary IDs on the existing signed-number allocation path. */
@OptIn(ExperimentalSerializationApi::class)
internal fun webSocketJSONID(bits: Long): JsonPrimitive =
    if (bits >= 0) JsonPrimitive(bits) else JsonPrimitive(bits.toULong())

internal fun webSocketJSONRequestID(element: JsonElement?): Long? {
    if (element !is JsonPrimitive || element.isString) return null
    val token = element.content
    if (token.isEmpty() || token.length > 20 || (token.length > 1 && token[0] == '0')) return null
    // Fixed-width lexical comparison bounds the only length that can overflow uint64.
    if (token.length == 20 && token > "18446744073709551615") return null
    var bits = 0L
    for (character in token) {
        val digit = character - '0'
        if (digit !in 0..9) return null
        // Deliberate bit-pattern arithmetic, not a signed business-integer conversion.
        bits = bits * 10 + digit
    }
    return bits
}

internal fun webSocketJSONTerminalError(json: JsonObject): LuxoError? {
    val error = decodeLuxoJsonError(json, 0)
    if (error != null) {
        return if (json.containsKey("ok")) LuxoError("ParseError", 0, "ambiguous stream terminal status") else error
    }
    val ok = json["ok"] as? JsonPrimitive
    if (ok == null || ok.isString || ok.booleanOrNull != true || json.containsKey("data")) {
        return LuxoError("ParseError", 0, "invalid stream completion")
    }
    return null
}
