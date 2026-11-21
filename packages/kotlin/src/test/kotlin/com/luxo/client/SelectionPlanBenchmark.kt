package com.luxo.client

import java.util.function.LongSupplier

/** Local request encoding only; registration and process startup are excluded. */
object SelectionPlanBenchmark {
    @Volatile private var bytes = byteArrayOf()

    private class Input(override val selection: String,
        override val selectionPlan: PreparedSelection? = null) : GeneratedRequest {
        override val apiID = 1
        override fun writeParameters(encoder: LuxoEncoder) = Unit
        override fun toJSONParameters(): Map<String, Any?> = error("JSON on Binary path")
    }

    @JvmStatic
    fun main(args: Array<String>) {
        for (count in listOf(0, 2, 16, 64)) {
            val fields = (1..count).associate { "f$it" to SelectionFieldSchema(it) }
            val source = mapOf("api" to APISchemaEntry(1, fields = fields))
            val meta = snapshotAPISchema(source).getValue("api")
            val text = fields.keys.joinToString(",")
            val plain = Input(text)
            val planned = Input(text, if (count == 0) null else PreparedSelection(text))
            val expected = (if (count == 0) listOf(1, 0, 0) else
                listOf(1, 1 + (count + 7) / 8, (count + 7) / 8) +
                (0 until (count + 7) / 8).map { (1 shl minOf(8, count - it * 8)) - 1 } + listOf(0))
                .map { it.toByte() }.toByteArray()
            check(LuxoBinaryProtocol.encodeRequest(meta, planned).contentEquals(expected))
            check(LuxoBinaryProtocol.encodeRequest(meta, plain).contentEquals(expected))
            PairedJvmBenchmark.compare("warm $count fields", LongSupplier { encode(meta, plain) },
                LongSupplier { encode(meta, planned) }, iterations = 20_000)
            if (count == 0) continue
            PairedJvmBenchmark.compare("first $count fields", LongSupplier { encode(meta, Input(text)) },
                LongSupplier { encode(meta, Input(text, PreparedSelection(text))) }, iterations = 10_000)
            val other = snapshotAPISchema(source).getValue("api")
            PairedJvmBenchmark.compare("two schemas $count fields", LongSupplier {
                encode(meta, plain) + encode(other, plain)
            }, LongSupplier {
                encode(meta, planned) + encode(other, planned)
            }, iterations = 10_000)
        }
    }

    private fun encode(meta: APISchemaEntry, input: Input): Long {
        bytes = LuxoBinaryProtocol.encodeRequest(meta, input)
        return bytes.size.toLong()
    }
}
