package com.luxo.client

import java.util.function.LongSupplier
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.*

/** Compares valid full JSON request construction with the same schema checks on both paths. */
object JSONInputBenchmark {
    @Volatile private var sink = ""

    @JvmStatic
    fun main(args: Array<String>) {
        println("JDK ${System.getProperty("java.version")}; 12 warmup pairs, 20 alternating samples, 20000 operations/sample; ns/op and B/op")
        for (count in listOf(1, 16)) compare(count)
    }

    private fun compare(count: Int) {
        val input = BenchmarkInput(List(count) { BenchmarkRow(Long.MAX_VALUE - it, Long.MIN_VALUE + it, it.toLong(), "精度 café $it") })
        val previous = object : LuxoBinaryEncodable {
            override fun writeLuxo(encoder: LuxoEncoder) = error("JSON benchmark only")
            override fun toLuxoJson(): JsonElement = Json.encodeToJsonElement(input)
        }
        val types = mapOf(
            "Input" to mapOf("rows" to SelectionFieldSchema(1, "Row", type = "Model", isList = true)),
            "Row" to mapOf(
                "value" to SelectionFieldSchema(1, type = "Int"),
                "duration" to SelectionFieldSchema(2, type = "Duration"),
                "optional" to SelectionFieldSchema(3, type = "Int", nullable = true),
                "label" to SelectionFieldSchema(4, type = "String"),
            ),
        )
        val schema = APISchemaEntry(1, listOf(ParamSchema(1, "input", "Model", typeName = "Input")), types = types)
        val before = mapOf("input" to previous)
        val after = mapOf("input" to input)
        check(encode(before, schema) == encode(after, schema))
        verifyProjectionSource()
        PairedJvmBenchmark.compare("JSON generated input $count rows", LongSupplier {
            sink = encode(before, schema); sink.length.toLong()
        }, LongSupplier {
            sink = encode(after, schema); sink.length.toLong()
        }, iterations = 20_000)
    }

    private fun encode(params: Map<String, LuxoBinaryEncodable>, schema: APISchemaEntry): String = buildJsonObject {
        put("\$api", "echoRows")
        putLuxoParams(params, schema)
    }.toString()

    private fun verifyProjectionSource() {
        val schema = LuxoSchema(types = mapOf(
            "BenchmarkRow" to LuxoTypeDecl("BenchmarkRow", TypeUsage.INPUT, listOf(
                LuxoField(1, "value", "Int"), LuxoField(2, "duration", "Duration"),
                LuxoField(3, "optional", "Int", nullable = true), LuxoField(4, "label", "String"),
            )),
            "BenchmarkInput" to LuxoTypeDecl("BenchmarkInput", TypeUsage.INPUT,
                listOf(LuxoField(1, "rows", "Model", "BenchmarkRow", isList = true))),
        ))
        val generated = LuxoCodegen.genTypes(schema, "com.luxo.client")
        for (line in listOf(
            "put(\"value\", JsonPrimitive(this@BenchmarkRow.value))",
            "put(\"duration\", JsonPrimitive(this@BenchmarkRow.duration))",
            "put(\"optional\", this@BenchmarkRow.optional?.let { value -> JsonPrimitive(value) } ?: JsonNull)",
            "put(\"label\", JsonPrimitive(this@BenchmarkRow.label))",
            "put(\"rows\", JsonArray(this@BenchmarkInput.rows.map { item -> item.toLuxoJson() }))",
        )) check(line in generated) { "benchmark projection no longer matches generated code: $line" }
    }

    @Serializable
    private data class BenchmarkRow(val value: Long, val duration: Long, val optional: Long? = null, val label: String) {
        fun toLuxoJson(): JsonElement = buildJsonObject {
            put("value", JsonPrimitive(this@BenchmarkRow.value))
            put("duration", JsonPrimitive(this@BenchmarkRow.duration))
            put("optional", this@BenchmarkRow.optional?.let { value -> JsonPrimitive(value) } ?: JsonNull)
            put("label", JsonPrimitive(this@BenchmarkRow.label))
        }
    }

    @Serializable
    private data class BenchmarkInput(val rows: List<BenchmarkRow>) : LuxoBinaryEncodable {
        override fun writeLuxo(encoder: LuxoEncoder) = error("JSON benchmark only")
        override fun toLuxoJson(): JsonElement = buildJsonObject {
            put("rows", JsonArray(this@BenchmarkInput.rows.map { item -> item.toLuxoJson() }))
        }
    }
}
