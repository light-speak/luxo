package com.luxo.client

import java.util.function.LongSupplier

/** Local parser comparisons, not network, Android or subscription measurements. */
object UuidBenchmark {
    @Volatile private var sink = byteArrayOf()

    @JvmStatic
    fun main(args: Array<String>) {
        println("JDK ${System.getProperty("java.version")}; UUID parsing, 12 warmups and 20 alternating samples")
        for (sample in listOf("00112233-4455-6677-8899-aabbccddeeff",
            "AABBCCDD-EEFF-0011-2233-445566778899", "00000000-0000-0000-0000-000000000000")) {
            check(PreviousUuidParser.parse(sample).contentEquals(UuidCodec.parse(sample)))
            PairedJvmBenchmark.compare("parse $sample", LongSupplier {
                sink = PreviousUuidParser.parse(sample); sink.size.toLong()
            }, LongSupplier {
                sink = UuidCodec.parse(sample); sink.size.toLong()
            })
            for (count in listOf(1, 128)) {
                PairedJvmBenchmark.compare("encode $sample count=$count", LongSupplier {
                    val encoder = LuxoEncoder(16 * count)
                    repeat(count) { encoder.writeRawBytes(PreviousUuidParser.parse(sample)) }
                    sink = encoder.bytes(); sink.size.toLong()
                }, LongSupplier {
                    val encoder = LuxoEncoder(16 * count)
                    repeat(count) { encoder.writeUuid(sample) }
                    sink = encoder.bytes(); sink.size.toLong()
                }, iterations = 20_000)
            }
        }
    }
}

// Frozen pre-fix parser. Do not reuse the current nibble or separator checks.
private object PreviousUuidParser {
    fun parse(s: String): ByteArray {
        if (s.length != 36) throw LuxoCodecException("invalid UUID length: ${s.length}")
        val out = ByteArray(16)
        var j = 0
        var i = 0
        while (i < s.length) {
            val c = s[i]
            if (c == '-') { i++; continue }
            if (i + 1 >= s.length || j >= 16) throw LuxoCodecException("invalid UUID: $s")
            val hi = nibble(s[i])
            val lo = nibble(s[i + 1])
            out[j++] = ((hi shl 4) or lo).toByte()
            i += 2
        }
        if (j != 16) throw LuxoCodecException("invalid UUID: $s")
        return out
    }

    private fun nibble(c: Char): Int = when (c) {
        in '0'..'9' -> c - '0'
        in 'a'..'f' -> c - 'a' + 10
        in 'A'..'F' -> c - 'A' + 10
        else -> throw LuxoCodecException("invalid hex digit: $c")
    }
}
