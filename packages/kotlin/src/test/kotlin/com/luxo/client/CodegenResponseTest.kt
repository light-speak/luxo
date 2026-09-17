package com.luxo.client

import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertFalse
import kotlin.test.assertEquals

class CodegenResponseTest {
    @Test
    fun `model and type lists require a schema field without changing unary signatures`() {
        val fields = listOf(LuxoField(1, "label", "String", nullable = true),
            LuxoField(2, "children", "Record", isList = true))
        val apis = listOf(
            LuxoAPI(1, "one", "test", returnType = "Record"),
            LuxoAPI(2, "many", "test", returnType = "Record", returnList = true),
            LuxoAPI(3, "page", "test", returnType = "Record", returnList = true, paginated = true),
            LuxoAPI(4, "watch", "test", returnType = "Record", returnList = true, stream = true),
        ).associateBy { it.name }
        for (type in listOf(false, true)) {
            val schema = if (type) LuxoSchema(apis = apis, types = mapOf("Record" to
                LuxoTypeDecl("Record", fields = fields, usage = TypeUsage.OUTPUT)))
            else LuxoSchema(apis = apis, models = mapOf("Record" to
                LuxoModel("Record", fields = fields, usage = TypeUsage.OUTPUT)))
            val types = LuxoCodegen.genTypes(schema, "test")
            val client = LuxoCodegen.genClient(schema, "test")
            assertContains(types, "fun decodeJSONRecord(data: JsonObject): Record {")
            assertContains(types, "internal fun `decodeJSONRecord\$item`(data: JsonObject): Record")
            assertContains(types, "if (!value.label.isSelected && !value.children.isSelected) throw LuxoError(\"ParseError\"")
            assertContains(types, "val `label\$json` = data[\"label\"]")
            assertContains(types, "`decodeJSONRecord\$item`(item.jsonObject)")
            assertContains(client, "decodeJSONRecord((data as JsonElement).jsonObject)")
            assertContains(client, "`decodeJSONArray\$Record`(data as JsonArray)")
            assertFalse(types.contains("data.keys.any"))
        }
    }

    @Test
    fun `projection temporaries cannot collide with schema field names`() {
        val fields = listOf("label", "labelJSON", "hasField", "requireField", "result").mapIndexed { i, name ->
            LuxoField(i + 1, name, "String", nullable = true)
        }
        val source = LuxoCodegen.genTypes(LuxoSchema(types = mapOf("Names" to
            LuxoTypeDecl("Names", fields = fields, usage = TypeUsage.OUTPUT))), "test")
        for (field in fields) {
            assertContains(source, "val `${field.name}\$json` = data[\"${field.name}\"]")
            assertContains(source, "val _${field.name}: Selected<String?>")
        }
    }

    @Test
    fun `JSON field array readers are shared by element type and exclude inputs`() {
        val fields = listOf("first", "second", "third").mapIndexed { index, name ->
            LuxoField(index + 1, name, "String", isList = true, nullable = index == 2)
        }
        val source = LuxoCodegen.genTypes(LuxoSchema(types = mapOf(
            "Output" to LuxoTypeDecl("Output", fields = fields, usage = TypeUsage.OUTPUT),
            "Input" to LuxoTypeDecl("Input", fields = listOf(LuxoField(1, "numbers", "Int", isList = true)), usage = TypeUsage.INPUT),
        )), "test")
        assertEquals(1, Regex(Regex.escape("internal fun `decodeJSONArray\$String`")).findAll(source).count())
        assertContains(source, "data.map { item -> decodeLuxoJsonString(item) }")
        assertFalse(source.contains("internal fun `decodeJSONArray\$Int`"))
    }

    @Test
    fun `text and boolean JSON fields and responses share strict readers`() {
        for (type in listOf("String", "Boolean", "DateTime", "UUID", "Decimal", "Enum", "Bytes")) {
            val reader = if (type == "Boolean") "decodeLuxoJsonBoolean" else "decodeLuxoJsonString"
            val fields = listOf(false, true).flatMap { nullable -> listOf(false, true).map { list ->
                LuxoField(1 + (if (nullable) 2 else 0) + (if (list) 1 else 0), "value${nullable}_$list", type, nullable = nullable, isList = list)
            } }
            val apis = listOf(false, true).flatMap { stream -> listOf(false, true).map { list ->
                val name = "value${stream}_$list"
                name to LuxoAPI(1, name, "test", returnType = type, returnList = list, stream = stream)
            } }.toMap()
            val schema = LuxoSchema(apis = apis, models = mapOf("Record" to LuxoModel("Record", fields = fields, usage = TypeUsage.OUTPUT)))
            for (source in listOf(LuxoCodegen.genClient(schema, "test"), LuxoCodegen.genTypes(schema, "test"))) {
                assertContains(source, "$reader(")
                assertFalse(source.contains(".jsonPrimitive.content"))
                assertFalse(source.contains(".jsonPrimitive.boolean"))
            }
            assertContains(LuxoCodegen.genTypes(schema, "test"), "import com.luxo.client.$reader")
        }
    }

    @Test
    fun `every generated numeric JSON reader uses strict conversion`() {
        for ((type, reader) in mapOf("Int" to "decodeLuxoJsonInt", "Duration" to "decodeLuxoJsonInt", "Float" to "decodeLuxoJsonFloat")) {
            val fields = listOf(false, true).flatMap { nullable -> listOf(false, true).map { list ->
                LuxoField(1 + (if (nullable) 2 else 0) + (if (list) 1 else 0), "number${nullable}_$list", type, nullable = nullable, isList = list)
            } }
            val apis = listOf(false, true).flatMap { stream -> listOf(false, true).map { list ->
                val name = "value${stream}_$list"
                name to LuxoAPI(1, name, "test", returnType = type, returnList = list, stream = stream)
            } }.toMap()
            val schema = LuxoSchema(apis = apis, models = mapOf("Record" to LuxoModel("Record", fields = fields, usage = TypeUsage.OUTPUT)))
            for (source in listOf(LuxoCodegen.genClient(schema, "test"), LuxoCodegen.genTypes(schema, "test"))) {
                assertContains(source, "$reader(")
                assertFalse(source.contains(".jsonPrimitive.long"))
                assertFalse(source.contains(".jsonPrimitive.double"))
            }
            assertContains(LuxoCodegen.genTypes(schema, "test"), "import com.luxo.client.$reader")
        }
    }

    @Test
    fun `scalar calls and streams check the whole response before returning a value`() {
        for (type in listOf(null, "Int", "Duration", "String", "Float", "Boolean", "Bytes", "JSON", "DateTime", "UUID", "Decimal", "Enum")) {
            for (list in listOf(false, true)) {
                for (stream in listOf(false, true)) {
                    val api = LuxoAPI(1, "echo", "test", returnType = type, returnList = list, stream = stream)
                    val source = LuxoCodegen.genClient(LuxoSchema(apis = mapOf("echo" to api)), "test")
                    assertContains(source, "dec.checkComplete()", message = "$type list=$list stream=$stream")
                    assertContains(source, "val value = ")
                }
            }
        }
    }

    @Test
    fun `named enum returns use scalar readers without model selection`() {
        val api = LuxoAPI(1, "state", "test", returnType = "State")
        val schema = LuxoSchema(apis = mapOf("state" to api), enums = mapOf("State" to LuxoEnum("State", listOf("ready"))))
        val source = LuxoCodegen.genClient(schema, "test")
        assertContains(source, "suspend fun state(): State")
        assertContains(source, "dec.readString()")
        assertFalse(source.contains("decodeState("))
    }

    @Test
    fun `JSON row array and column readers never replace malformed bytes`() {
        val fields = listOf(false, true).flatMap { nullable -> listOf(false, true).map { list ->
            LuxoField(1 + (if (nullable) 2 else 0) + (if (list) 1 else 0), "json${nullable}_$list", "JSON", nullable = nullable, isList = list)
        } }
        val schema = LuxoSchema(models = mapOf("Record" to LuxoModel("Record", fields = fields, usage = TypeUsage.OUTPUT)))
        val source = LuxoCodegen.genTypes(schema, "test")
        assertFalse(source.contains(".toString(Charsets.UTF_8)"))
        assertContains(source, "decodeLuxoJSON(dec.readString())")
        assertContains(source, "decodeLuxoJsonValue(value)")
        assertContains(source, "cell.checkComplete()")
    }
}
