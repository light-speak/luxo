package com.luxo.client

import java.util.function.LongSupplier

/** Local JVM costs only: byte decoding, removed row copies and final bounds checks. */
object CodecTextBenchmark {
    @Volatile private var valueSink: Any? = null

    @JvmStatic
    fun main(args: Array<String>) {
        for ((name, text) in linkedMapOf("empty" to "", "ascii" to "hello-world", "unicode" to "发布检查 café",
            "bom" to "\ufeffhello", "replacement" to "\ufffd", "long" to "发布检查".repeat(350))) {
            compareRange(name, text)
            compareRows(name, text, false)
            compareRows(name, text, true)
        }
        compareCompletion()
    }

    private fun compareRange(name: String, text: String) {
        val prefix = "prefix".encodeToByteArray()
        val value = text.encodeToByteArray()
        val bytes = prefix + value + "suffix".encodeToByteArray()
        val start = prefix.size
        val end = start + value.size
        check(String(bytes, start, value.size, Charsets.UTF_8) == decodeLuxoUtf8(bytes, start, end))
        PairedJvmBenchmark.compare("UTF8 range $name", LongSupplier {
            val result = String(bytes, start, value.size, Charsets.UTF_8)
            valueSink = result; result.length.toLong()
        }, LongSupplier {
            val result = decodeLuxoUtf8(bytes, start, end)
            valueSink = result; result.length.toLong()
        }, 5000)
    }

    private fun compareRows(name: String, text: String, list: Boolean) {
        val bytes = LuxoEncoder().apply {
            if (list) writeVarint(16)
            repeat(if (list) 16 else 1) { writeString(text) }
        }.bytes()
        fun before(): Any = LuxoDecoder(bytes).let { decoder ->
            // The previous readString was exactly readBytes().toString(UTF_8).
            if (list) decoder.readArray { decoder.readBytes().toString(Charsets.UTF_8) }
            else decoder.readBytes().toString(Charsets.UTF_8)
        }
        fun after(): Any = LuxoDecoder(bytes).let { decoder ->
            if (list) decoder.readArray { decoder.readString() } else decoder.readString()
        }
        check(before() == after())
        PairedJvmBenchmark.compare("row $name list=$list", LongSupplier {
            valueSink = before(); 1
        }, LongSupplier {
            valueSink = after(); 1
        }, 5000)
    }

    private fun compareCompletion() {
        val integer = LuxoEncoder().apply { writeSvarint(Long.MAX_VALUE) }.bytes()
        val array = LuxoEncoder().apply { writeVarint(16); repeat(16) { writeSvarint(Long.MIN_VALUE + it) } }.bytes()
        PairedJvmBenchmark.compare("scalar Int completion", LongSupplier {
            LuxoDecoder(integer).readInt()
        }, LongSupplier {
            LuxoDecoder(integer).let { decoder -> val value = decoder.readInt(); decoder.checkComplete(); value }
        }, 20_000)
        PairedJvmBenchmark.compare("array Int completion", LongSupplier {
            val decoder = LuxoDecoder(array)
            valueSink = decoder.readArray { decoder.readInt() }; 1
        }, LongSupplier {
            val decoder = LuxoDecoder(array)
            val value = decoder.readArray { decoder.readInt() }
            decoder.checkComplete(); valueSink = value; 1
        }, 20_000)
    }
}
