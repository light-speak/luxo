package com.luxo.client

import java.util.function.LongSupplier

/** Compare registration ownership with the prior borrowed metadata, never with requests. */
object SchemaRegistrationBenchmark {
    @JvmStatic
    fun main(args: Array<String>) {
        val paramCount = args.singleOrNull()?.toInt() ?: 1
        require(paramCount in 1..128)
        println("JDK ${System.getProperty("java.version")}; prebuilt legal metadata; registration and encoding separately")
        for ((apis, fields, types) in listOf(Triple(1, 2, 0), Triple(16, 16, 0), Triple(16, 16, 16))) {
            val source = fixture(apis, fields, types, paramCount)
            val before = Registration(paramCount)
            val after = Registration(paramCount)
            val label = "$apis APIs x $fields fields + $types shared types, $paramCount params"
            PairedJvmBenchmark.compare("register $label", LongSupplier { before.register(source) },
                LongSupplier { after.register(snapshotAPISchema(source)) }, iterations = 10_000)
            check(before.encode().contentEquals(after.encode()))
            PairedJvmBenchmark.compare("encode control $label", LongSupplier { before.encode().size.toLong() },
                LongSupplier { after.encode().size.toLong() }, iterations = 100_000)
        }
        println("sink=${PairedJvmBenchmark.sink}")
    }

    private class Registration(paramCount: Int) {
        @Volatile private var schema = emptyMap<String, APISchemaEntry>()
        private val params = (1..paramCount).associateTo(linkedMapOf<String, Any>()) { paramName(it) to 7L }
            .apply { put("\$select", "field1") }
        @Volatile private var bytes = byteArrayOf()

        fun register(value: Map<String, APISchemaEntry>): Long {
            schema = value
            return schema.size.toLong()
        }

        fun encode(): ByteArray {
            val result = LuxoBinaryProtocol.encodeRequest(schema.getValue("api1"), params)
            bytes = result
            return result
        }
    }

    private fun fixture(apis: Int, fields: Int, types: Int, paramCount: Int): Map<String, APISchemaEntry> {
        val selections = (1..fields).associate { "field$it" to SelectionFieldSchema(it, type = "Int") }
        val shared = (1..types).associate { "Type$it" to selections }
        return (1..apis).associate {
            val params = if (paramCount == 1) listOf(ParamSchema(1, "value", "Int"))
                else listOf(*Array(paramCount) { index -> ParamSchema(index + 1, paramName(index + 1), "Int") })
            "api$it" to APISchemaEntry(it, params, selections, shared)
        }
    }

    private fun paramName(id: Int): String = if (id == 1) "value" else "value$id"
}
