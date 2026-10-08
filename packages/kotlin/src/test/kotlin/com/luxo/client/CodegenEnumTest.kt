package com.luxo.client

import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertFalse
import kotlin.test.assertFailsWith

class CodegenEnumTest {
    private val fields = listOf(
        LuxoField(1, "state", "Enum", typeName = "State"),
        LuxoField(2, "optional", "Enum", typeName = "State", nullable = true),
        LuxoField(3, "states", "Enum", typeName = "State", isList = true),
    )
    private val schema = LuxoSchema(
        enums = mapOf("State" to LuxoEnum("State", listOf("OPEN", "when", "name", "rawValue"))),
        types = mapOf("Record" to LuxoTypeDecl("Record", usage = TypeUsage.INPUT_OUTPUT, fields = fields)),
        apis = mapOf("echo" to LuxoAPI(1, "echo", "test", returnType = "State", returnList = true,
            params = listOf(LuxoParam(1, "states", "Enum", typeName = "State", isList = true),
                LuxoParam(2, "optional", "Enum", typeName = "State", nullable = true, hasDefault = true)))),
    )

    @Test
    fun `named enums preserve unknown text with exhaustive known and unknown variants`() {
        val source = LuxoCodegen.genTypes(schema, "test")
        assertContains(source, "sealed interface State")
        assertContains(source, "enum class Known(override val rawValue: String) : State")
        assertContains(source, "class Unknown private constructor(override val rawValue: String) : State")
        assertContains(source, "`when`(\"when\")")
        assertContains(source, "`name\$`(\"name\")")
        assertContains(source, "`rawValue\$`(\"rawValue\")")
        assertFalse(source.contains("typealias State = String"))
    }

    @Test
    fun `enum models read and write raw strings without runtime serializer discovery`() {
        val source = LuxoCodegen.genTypes(schema, "test")
        assertContains(source, "State.fromValue(dec.readString())")
        assertContains(source, "State.fromValue(decodeLuxoJsonString(value))")
        assertContains(source, "State.fromValue(_state[i])")
        assertContains(source, "encoder.writeString(state.rawValue)")
        assertContains(source, "luxoUtf8Length(state.rawValue)")
        assertContains(source, "JsonPrimitive(this@RecordInput.state.rawValue)")
    }

    @Test
    fun `enum parameters unwrap only declared positions and keep optional null`() {
        val source = LuxoCodegen.genClient(schema, "test")
        assertContains(source, "put(\"states\", p0.map { it.rawValue })")
        assertContains(source, "put(\"optional\", p1.value?.rawValue)")
        assertContains(source, "encoder.writeString(item.rawValue)")
        assertContains(source, "encoder.writeString(value.rawValue)")
        assertFalse(source.substringAfter("override fun writeParameters").substringBefore("override fun toJSONParameters").contains(".map {"))
        assertContains(source, "State.fromValue(dec.readString())")
        assertContains(source, "State.fromValue(decodeLuxoJsonString(it))")
    }

    @Test
    fun `incomplete enum metadata is rejected rather than generated as strings`() {
        val missing = schema.copy(types = mapOf("Broken" to LuxoTypeDecl("Broken",
            fields = listOf(LuxoField(1, "state", "Enum")))))
        val empty = schema.copy(enums = mapOf("State" to LuxoEnum("State")))
        val duplicate = schema.copy(enums = mapOf("State" to LuxoEnum("State", listOf("OPEN", "OPEN"))))
        val mismatch = schema.copy(enums = mapOf("State" to LuxoEnum("Other", listOf("OPEN"))))
        val bareReturn = schema.copy(apis = mapOf("bad" to LuxoAPI(1, "bad", "test", returnType = "Enum")))
        for (invalid in listOf(missing, empty, duplicate, mismatch, bareReturn)) {
            assertFailsWith<IllegalArgumentException> { LuxoCodegen.genTypes(invalid, "test") }
            assertFailsWith<IllegalArgumentException> { LuxoCodegen.genClient(invalid, "test") }
        }
    }
}
