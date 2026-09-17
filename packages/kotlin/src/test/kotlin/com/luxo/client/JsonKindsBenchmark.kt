package com.luxo.client

import java.util.function.LongSupplier
import kotlinx.serialization.json.*

/** Same-toolchain local readers; input parsing and transport are outside timing. */
object JsonKindsBenchmark {
    @Volatile private var resultSink: Any? = null

    @JvmStatic
    fun main(args: Array<String>) {
        println("JDK ${System.getProperty("java.version")}; JSON kind readers; ns/op and B/op")
        compareSingle("String", listOf(JsonPrimitive(""), JsonPrimitive("发布 café"), JsonPrimitive("false")),
            { it.jsonPrimitive.content.length.toLong() }, { decodeLuxoJsonString(it).length.toLong() })
        compareSingle("Boolean", listOf(JsonPrimitive(true), JsonPrimitive(false)),
            { if (it.jsonPrimitive.boolean) 1L else 0L }, { if (decodeLuxoJsonBoolean(it)) 1L else 0L })
        for (count in listOf(1, 16, 128)) {
            compareList("String", count, listOf(JsonPrimitive("false"), JsonPrimitive("发布 café")),
                { it.jsonPrimitive.content }, { decodeLuxoJsonString(it) })
            compareList("Boolean", count, listOf(JsonPrimitive(true), JsonPrimitive(false)),
                { it.jsonPrimitive.boolean }, { decodeLuxoJsonBoolean(it) })
        }
        check(resultSink != null)
    }

    private fun compareSingle(name: String, values: List<JsonElement>, before: (JsonElement) -> Long, after: (JsonElement) -> Long) {
        var oldIndex = 0
        var newIndex = 0
        for (value in values) check(before(value) == after(value))
        PairedJvmBenchmark.compare("$name scalar",
            LongSupplier { before(values[oldIndex++ % values.size]) },
            LongSupplier { after(values[newIndex++ % values.size]) })
    }

    private fun <T> compareList(name: String, count: Int, values: List<JsonElement>, before: (JsonElement) -> T, after: (JsonElement) -> T) {
        val source = List(count) { values[it % values.size] }
        check(source.map(before) == source.map(after))
        PairedJvmBenchmark.compare("$name list $count", LongSupplier {
            val result = source.map(before)
            resultSink = result
            result.size.toLong()
        }, LongSupplier {
            val result = source.map(after)
            resultSink = result
            result.size.toLong()
        }, iterations = 100_000)
    }
}
