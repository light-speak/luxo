package com.luxo.client

import java.util.function.LongSupplier
import kotlinx.serialization.json.*

/** Numeric conversion only; JSON parsing, fixtures and assertions stay outside timing. */
object JsonNumbersBenchmark {
    @JvmStatic
    fun main(args: Array<String>) {
        println("JDK ${System.getProperty("java.version")}; 12 warmup pairs, 20 alternating samples; ns/op and B/op")
        integers("small integers", Array(1024) { JsonPrimitive((it + 128).toLong()) })
        integers("full-width integers", Array(1024) { Json.parseToJsonElement((Long.MAX_VALUE - it).toString()).jsonPrimitive })
        floats("ordinary floats", Array(1024) { JsonPrimitive(it + 0.25) })
        floats("zero", arrayOf("0", "-0", "0.00", "-0e+10").map { Json.parseToJsonElement(it).jsonPrimitive }.toTypedArray())
        floats("extreme finite floats", arrayOf("5e-324", "-5e-324", "1.7976931348623157e308").map {
            Json.parseToJsonElement(it).jsonPrimitive
        }.toTypedArray())
        records()
        invalidFloats()
    }

    private fun integers(name: String, values: Array<JsonPrimitive>) {
        for (value in values) check(value.long == decodeLuxoJsonInt(value))
        var before = 0
        var after = 0
        PairedJvmBenchmark.compare(name, LongSupplier { values[before++ % values.size].long },
            LongSupplier { decodeLuxoJsonInt(values[after++ % values.size]) }, iterations = 100_000)
    }

    private fun floats(name: String, values: Array<JsonPrimitive>) {
        for (value in values) {
            check(value.double.toBits() == decodeLuxoJsonFloat(value).toBits())
            check(previousStrictFloat(value).toBits() == decodeLuxoJsonFloat(value).toBits())
        }
        var before = 0
        var after = 0
        PairedJvmBenchmark.compare(name, LongSupplier { values[before++ % values.size].double.toBits() },
            LongSupplier { decodeLuxoJsonFloat(values[after++ % values.size]).toBits() }, iterations = 100_000)
        before = 0
        after = 0
        PairedJvmBenchmark.compare("strict parser / $name", LongSupplier { previousStrictFloat(values[before++ % values.size]).toBits() },
            LongSupplier { decodeLuxoJsonFloat(values[after++ % values.size]).toBits() }, iterations = 100_000)
        before = 0
        after = 0
        for (value in values) check(singleScanFloatCandidate(value).toBits() == decodeLuxoJsonFloat(value).toBits())
        PairedJvmBenchmark.compare("single scan candidate / $name", LongSupplier { decodeLuxoJsonFloat(values[before++ % values.size]).toBits() },
            LongSupplier { singleScanFloatCandidate(values[after++ % values.size]).toBits() }, iterations = 100_000)
    }

    private fun records() {
        val rows = (0..<16).map { index ->
            Json.parseToJsonElement("""{"id":${Long.MAX_VALUE - index},"ratio":${index + 0.25}}""").jsonObject
        }
        PairedJvmBenchmark.compare("16 records numeric fields", LongSupplier {
            var result = 0L
            for (row in rows) result = result xor row.getValue("id").jsonPrimitive.long xor row.getValue("ratio").jsonPrimitive.double.toBits()
            result
        }, LongSupplier {
            var result = 0L
            for (row in rows) result = result xor decodeLuxoJsonInt(row.getValue("id")) xor decodeLuxoJsonFloat(row.getValue("ratio")).toBits()
            result
        }, iterations = 10_000)
    }

    @OptIn(kotlinx.serialization.ExperimentalSerializationApi::class)
    private fun invalidFloats() {
        for (token in listOf("0x1p0", "1e2.3", "1e+e2", "1e-400")) {
            val value = JsonUnquotedLiteral(token)
            check(rejectedFloat { previousStrictFloat(value) } == rejectedFloat { decodeLuxoJsonFloat(value) })
            PairedJvmBenchmark.compare("strict rejection / $token",
                LongSupplier { rejectedFloat { previousStrictFloat(value) } },
                LongSupplier { rejectedFloat { decodeLuxoJsonFloat(value) } }, iterations = 10_000)
            check(rejectedFloat { singleScanFloatCandidate(value) } == rejectedFloat { decodeLuxoJsonFloat(value) })
            PairedJvmBenchmark.compare("single scan rejection / $token",
                LongSupplier { rejectedFloat { decodeLuxoJsonFloat(value) } },
                LongSupplier { rejectedFloat { singleScanFloatCandidate(value) } }, iterations = 10_000)
        }
    }

    private inline fun rejectedFloat(read: () -> Double): Long {
        try {
            read()
        } catch (error: LuxoError) {
            check(error.error == "ParseError")
            return error.error.length.toLong()
        }
        error("invalid benchmark input was accepted")
    }
}

// Experimental full grammar scan. Keep it outside production until its
// successful and malformed-input costs have both passed the performance gate.
fun singleScanFloatCandidate(value: JsonElement): Double {
    if (value !is JsonPrimitive || value.isString) previousInvalidJSONNumber("Float")
    val token = value.content
    if (!singleScanFloatSyntax(token)) previousInvalidJSONNumber("Float")
    val number = token.toDouble()
    if (!number.isFinite() || (number == 0.0 && previousNonzeroMantissa(token))) previousInvalidJSONNumber("Float")
    return number
}

private fun singleScanFloatSyntax(token: String): Boolean {
    if (token.isEmpty() || token.last() !in '0'..'9') return false
    val start = if (token[0] == '-') 1 else 0
    when (token[start]) {
        '0' -> if (start + 1 < token.length && token[start + 1] !in ".eE") return false
        in '1'..'9' -> Unit
        else -> return false
    }
    var fraction = false
    var exponent = false
    for (index in start + 1 until token.length) {
        val character = token[index]
        if (character in '0'..'9') continue
        when (character) {
            '.' -> {
                // The final character is a digit, so lookahead is in bounds.
                if (fraction || exponent || token[index + 1] !in '0'..'9') return false
                fraction = true
            }
            'e', 'E' -> {
                if (exponent) return false
                exponent = true
            }
            '+', '-' -> if (token[index - 1] != 'e' && token[index - 1] != 'E') return false
            else -> return false
        }
    }
    return true
}

// Frozen strict reader before sharing syntax validation with Double.parseDouble.
// Keep this independent from production helpers so future edits do not move the baseline.
// Match the public reader and its error helper's call depth for exception allocation measurements.
fun previousStrictFloat(value: JsonElement): Double {
    if (value !is JsonPrimitive || value.isString) previousInvalidJSONNumber("Float")
    val token = value.content
    if (!previousFloatSyntax(token)) previousInvalidJSONNumber("Float")
    val number = token.toDouble()
    if (!number.isFinite() || (number == 0.0 && previousNonzeroMantissa(token))) previousInvalidJSONNumber("Float")
    return number
}

private fun previousFloatSyntax(token: String): Boolean {
    if (token.isEmpty()) return false
    var index = if (token[0] == '-') 1 else 0
    if (index == token.length) return false
    when (token[index]) {
        '0' -> index++
        in '1'..'9' -> index = previousDigitsEnd(token, index)
        else -> return false
    }
    if (index < token.length && token[index] == '.') {
        val start = ++index
        index = previousDigitsEnd(token, index)
        if (index == start) return false
    }
    if (index < token.length && (token[index] == 'e' || token[index] == 'E')) {
        index++
        if (index < token.length && (token[index] == '+' || token[index] == '-')) index++
        val start = index
        index = previousDigitsEnd(token, index)
        if (index == start) return false
    }
    return index == token.length
}

private fun previousDigitsEnd(token: String, start: Int): Int {
    var index = start
    while (index < token.length && token[index] in '0'..'9') index++
    return index
}

private fun previousNonzeroMantissa(token: String): Boolean {
    for (character in token) {
        if (character == 'e' || character == 'E') return false
        if (character in '1'..'9') return true
    }
    return false
}

private fun previousInvalidJSONNumber(type: String): Nothing =
    throw LuxoError("ParseError", 0, "invalid JSON $type numeric syntax or range")
