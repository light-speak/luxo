package com.luxo.client

import java.util.concurrent.Executors
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertFailsWith

class PreparedSelectionTest {
    private fun schema(id: Int) = snapshotAPISchema(mapOf("api" to APISchemaEntry(
        1, fields = mapOf("user" to SelectionFieldSchema(1, "User")),
        types = mapOf("User" to mapOf("id" to SelectionFieldSchema(id), "name" to SelectionFieldSchema(2))),
    ))).getValue("api")

    private fun encode(meta: APISchemaEntry, text: String, plan: PreparedSelection?): ByteArray =
        LuxoBinaryProtocol.encodeRequest(meta, object : GeneratedRequest {
            override val apiID = 1
            override val selection = text
            override val selectionPlan = plan
            override fun writeParameters(encoder: LuxoEncoder) = Unit
            override fun toJSONParameters(): Map<String, Any?> = emptyMap()
        })

    @Test
    fun `rejects trailing separators in dynamic and prepared selections`() {
        for (text in listOf("user,", "user,  ", "user{id,}", "user{id, }", "user{id},")) {
            assertFailsWith<LuxoError> { PreparedSelection(text) }
            assertFailsWith<LuxoError> { encode(schema(1), text, null) }
        }
    }

    @Test
    fun `plans isolate schema identity and output ownership`() {
        val plan = PreparedSelection("user{id}")
        for (meta in listOf(schema(1), schema(65535))) {
            val expected = encode(meta, plan.text, null)
            val output = encode(meta, plan.text, plan)
            assertContentEquals(expected, output)
            output.fill(0)
            assertContentEquals(expected, encode(meta, plan.text, plan))
            assertContentEquals(encode(meta, "user{name}", null), encode(meta, "user{name}", plan))
        }
    }

    @Test
    fun `mutable metadata is not cached and invalid plans fail`() {
        val fields = mutableMapOf("id" to SelectionFieldSchema(1))
        val meta = APISchemaEntry(1, fields = fields)
        val plan = PreparedSelection("id")
        encode(meta, "id", plan)
        fields["id"] = SelectionFieldSchema(2)
        assertContentEquals(encode(meta, "id", null), encode(meta, "id", plan))
        for (text in listOf("", "id,id", "user{")) {
            assertFailsWith<LuxoError> { PreparedSelection(text) }
        }
        assertFailsWith<LuxoError> { encode(meta, "missing", PreparedSelection("missing")) }
    }

    @Test
    fun `registered selection maps reject external mutation`() {
        val meta = schema(1)
        assertFailsWith<UnsupportedOperationException> {
            (meta.fields as MutableMap)["other"] = SelectionFieldSchema(3)
        }
        assertFailsWith<UnsupportedOperationException> {
            (meta.types.getValue("User") as MutableMap)["id"] = SelectionFieldSchema(3)
        }
        assertFailsWith<UnsupportedOperationException> {
            (meta.types as MutableMap)["User"] = emptyMap()
        }
    }

    @Test
    fun `shared plans remain correct across concurrent schemas`() {
        val plan = PreparedSelection("user{id}")
        val schemas = listOf(schema(1), schema(3))
        val expected = schemas.map { encode(it, plan.text, null) }
        val executor = Executors.newFixedThreadPool(4)
        try {
            val tasks = (0 until 16).map { index -> executor.submit {
                repeat(100) {
                    val choice = (index + it) % schemas.size
                    assertContentEquals(expected[choice], encode(schemas[choice], plan.text, plan))
                }
            } }
            tasks.forEach { it.get() }
        } finally {
            executor.shutdownNow()
        }
    }
}
