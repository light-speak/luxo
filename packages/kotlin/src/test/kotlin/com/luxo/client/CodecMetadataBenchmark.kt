package com.luxo.client

import java.util.function.LongSupplier

/** Valid-wire comparisons with frozen pre-bounds readers; not a released-SHA or device gate. */
object CodecMetadataBenchmark {
    @Volatile private var valuesSink: List<*> = emptyList<Nothing>()

    @JvmStatic
    fun main(args: Array<String>) {
        println("JDK ${System.getProperty("java.version")}; 12 warmup pairs, 20 alternating samples; ns/op and B/op")
        compareRows()
        for (count in listOf(1, 16, 128)) {
            compareArray(count)
            compareColumns(count)
        }
    }

    private fun compareRows() {
        val wire = LuxoEncoder().apply {
            for (id in listOf(1, 128, 16384, Int.MAX_VALUE)) writeFieldInt(id, Long.MAX_VALUE - id)
            writeEnd()
        }.bytes()
        fun previous(): Long {
            val decoder = PreviousMetadataRow(wire)
            var sum = 0L
            while (decoder.nextField()) sum = sum xor decoder.fieldID.toLong() xor decoder.readInt()
            return sum
        }
        fun current(): Long {
            val decoder = LuxoDecoder(wire)
            var sum = 0L
            while (decoder.nextField()) sum = sum xor decoder.fieldID.toLong() xor decoder.readInt()
            return sum
        }
        check(previous() == current())
        PairedJvmBenchmark.compare("four row fields", LongSupplier { previous() }, LongSupplier { current() }, 20_000)
    }

    private fun compareArray(count: Int) {
        val wire = LuxoEncoder().apply {
            writeVarint(count.toLong())
            repeat(count) { writeSvarint(Long.MIN_VALUE + it) }
        }.bytes()
        fun previous(): List<Long> = PreviousMetadataRow(wire).let { decoder -> decoder.readArray { decoder.readInt() } }
        fun current(): List<Long> = LuxoDecoder(wire).let { decoder -> decoder.readArray { decoder.readInt() } }
        compareLists("array $count", ::previous, ::current)
    }

    private fun compareColumns(count: Int) {
        val integers = column(count) { repeat(count) { writeSvarint(Long.MIN_VALUE + it) } }
        val strings = column(count) { repeat(count) { writeString(if (it % 3 == 0) "" else "任务-$it") } }
        val floats = column(count) { repeat(count) { writeFixed64(it + 0.5) } }
        compareLists("Int column $count", { PreviousMetadataColumn(integers).apply { check(nextColumn()) }.readColumnInt() },
            { ColumnarDecoder(integers).apply { check(nextColumn()) }.readColumnInt() })
        compareLists("String column $count", { PreviousMetadataColumn(strings).apply { check(nextColumn()) }.readColumnString() },
            { ColumnarDecoder(strings).apply { check(nextColumn()) }.readColumnString() })
        compareLists("Float column $count", { PreviousMetadataColumn(floats).apply { check(nextColumn()) }.readColumnFloat() },
            { ColumnarDecoder(floats).apply { check(nextColumn()) }.readColumnFloat() })
    }

    // Complete valid columns on both sides, including field ID and end marker.
    private fun column(count: Int, write: LuxoEncoder.() -> Unit): ByteArray = LuxoEncoder().apply {
        writeVarint(count.toLong()); writeVarint(0); writeVarint(1); write(); writeEnd()
    }.bytes()

    private fun compareLists(name: String, previous: () -> List<*>, current: () -> List<*>) {
        check(previous() == current())
        PairedJvmBenchmark.compare(name, LongSupplier {
            valuesSink = previous(); valuesSink.size.toLong()
        }, LongSupplier {
            valuesSink = current(); valuesSink.size.toLong()
        }, iterations = 20_000)
    }
}

// Frozen before metadata bounds. Keep the original cursor fields and primitive loops on both
// baselines instead of wrapping today's decoder and inadvertently charging it for new checks.
private class PreviousMetadataRow(private val buf: ByteArray) {
    private var off = 0
    var fieldID = 0
        private set

    fun nextField(): Boolean {
        if (off >= buf.size) { fieldID = 0; return false }
        fieldID = readVarintRaw().toInt()
        return fieldID != 0
    }

    fun readInt(): Long {
        val uv = readVarintRaw()
        return (uv ushr 1) xor -(uv and 1L)
    }

    fun <T> readArray(decode: () -> T): List<T> {
        val count = readVarintRaw().toInt()
        return List(count) { decode() }
    }

    private fun readVarintRaw(): Long {
        var result = 0L
        var shift = 0
        while (true) {
            checkRemaining(1)
            val b = buf[off++].toLong() and 0xff
            result = result or ((b and 0x7f) shl shift)
            if (b and 0x80 == 0L) {
                if (shift == 63 && b > 1) throw LuxoCodecException("varint overflow")
                break
            }
            shift += 7
            if (shift >= 64) throw LuxoCodecException("varint overflow")
        }
        return result
    }

    private fun checkRemaining(n: Int) {
        if (off + n > buf.size) throw LuxoCodecException("unexpected end of buffer at offset $off, need $n bytes")
    }
}

private class PreviousMetadataColumn(private val buf: ByteArray) {
    private var off = 0
    val count = readVarintRaw().toInt()
    val arenaSize = readVarintRaw().toInt()
    var fieldID = 0
        private set

    fun nextColumn(): Boolean {
        fieldID = readVarintRaw().toInt()
        return fieldID != 0
    }

    fun readColumnInt(): List<Long> {
        val result = LongArray(count)
        for (i in 0 until count) {
            val uv = readVarintRaw()
            result[i] = (uv ushr 1) xor -(uv and 1L)
        }
        return result.asList()
    }

    fun readColumnString(): List<String> {
        val result = Array(count) { "" }
        for (i in 0 until count) {
            val len = readVarintRaw().toInt()
            if (len == 0) { result[i] = ""; continue }
            checkRemaining(len)
            result[i] = String(buf, off, len, Charsets.UTF_8)
            off += len
        }
        return result.asList()
    }

    fun readColumnFloat(): List<Double> {
        val result = DoubleArray(count)
        for (i in 0 until count) {
            checkRemaining(8)
            var bits = 0L
            bits = bits or (buf[off++].toLong() and 0xff)
            bits = bits or ((buf[off++].toLong() and 0xff) shl 8)
            bits = bits or ((buf[off++].toLong() and 0xff) shl 16)
            bits = bits or ((buf[off++].toLong() and 0xff) shl 24)
            bits = bits or ((buf[off++].toLong() and 0xff) shl 32)
            bits = bits or ((buf[off++].toLong() and 0xff) shl 40)
            bits = bits or ((buf[off++].toLong() and 0xff) shl 48)
            bits = bits or ((buf[off++].toLong() and 0xff) shl 56)
            result[i] = java.lang.Double.longBitsToDouble(bits)
        }
        return result.asList()
    }

    private fun readVarintRaw(): Long {
        var result = 0L
        var shift = 0
        while (true) {
            checkRemaining(1)
            val b = buf[off++].toLong() and 0xff
            result = result or ((b and 0x7f) shl shift)
            if (b and 0x80 == 0L) {
                if (shift == 63 && b > 1) throw LuxoCodecException("varint overflow")
                break
            }
            shift += 7
            if (shift >= 64) throw LuxoCodecException("varint overflow")
        }
        return result
    }

    private fun checkRemaining(n: Int) {
        if (off + n > buf.size) throw LuxoCodecException("unexpected end of buffer at offset $off, need $n bytes")
    }
}
