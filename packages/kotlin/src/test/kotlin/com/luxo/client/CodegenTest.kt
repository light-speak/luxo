package com.luxo.client

import com.sun.net.httpserver.HttpServer
import java.io.File
import java.net.InetSocketAddress
import java.nio.file.Files
import java.util.concurrent.Executors
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class CodegenTest {
    @Test
    fun malformedSealedMetadataRejectsBeforeReplacingFiles() {
        val variant = LuxoSealedVariant(1,"Value",listOf(LuxoParam(1,"value","Int")))
        val owner = LuxoSealed("Choice",variants=listOf(variant))
        val invalid = listOf(owner.copy(name="Other"), owner.copy(variants=listOf(variant.copy(id=0))),
            owner.copy(variants=listOf(variant.copy(id=65536))), owner.copy(variants=listOf(variant,variant.copy(name="Again"))),
            owner.copy(variants=listOf(variant,variant.copy(id=2))),
            owner.copy(variants=listOf(variant.copy(fields=listOf(LuxoParam(0,"value","Int"))))),
            owner.copy(variants=listOf(variant.copy(fields=listOf(LuxoParam(1,"x","Int"),LuxoParam(1,"y","Int"))))),
            owner.copy(variants=listOf(variant.copy(fields=listOf(LuxoParam(1,"x","Int"),LuxoParam(2,"x","Int"))))),
            owner.copy(variants=listOf(variant.copy(fields=listOf(LuxoParam(1,"x","Sealed",typeName="Missing"))))))
        for (candidate in invalid) {
            val schema = LuxoSchema(sealeds=mapOf("Choice" to candidate))
            withIntrospection(Json.encodeToString(LuxoSchema.serializer(),schema).toByteArray()) { endpoint, directory ->
                val files = listOf("Types.kt","Schema.kt","LuxoClient.kt","Queries.kt","snapshot.json")
                for (name in files) File(directory,name).writeText("existing $name")
                assertFailsWith<IllegalArgumentException> {
                    LuxoCodegen.generate(endpoint,"test-key",directory.path,schemaFile=File(directory,"snapshot.json").path)
                }
                for (name in files) assertEquals("existing $name",File(directory,name).readText())
            }
        }
    }

    @Test
    fun emptySchemaNamesRejectBeforeReplacingFiles() {
        val field = """{"id":1,"name":"value","type":"Int"}"""
        val invalid = """{"id":1,"name":"","type":"Int"}"""
        val bodies = listOf(
            """{"models":{"":{"name":"","fields":[$field]}},"apis":{}}""",
            """{"models":{},"types":{"":{"name":"","fields":[$field]}},"apis":{}}""",
            """{"models":{},"enums":{"":{"name":"","values":["READY"]}},"apis":{}}""",
            """{"models":{},"apis":{"":{"id":1,"name":"","module":"test","returnType":"Int"}}}""",
            """{"models":{"Payload":{"name":"Payload","fields":[$invalid]}},"apis":{"read":{"id":1,"name":"read","module":"test","returnType":"Payload"}}}""",
            """{"models":{},"apis":{"read":{"id":1,"name":"read","module":"test","returnType":"Int","params":[$invalid]}}}""",
        )
        val failures = mutableListOf<Int>()
        bodies.forEachIndexed { index, body ->
            withIntrospection(body.toByteArray()) { endpoint, root ->
                val files = listOf("Types.kt", "Schema.kt", "LuxoClient.kt", "Queries.kt", "schema.json")
                files.forEach { File(root, it).writeText("existing $it") }
                var rejected = false
                try {
                    LuxoCodegen.generate(endpoint, "test-key", root.path, schemaFile = File(root, "schema.json").path)
                } catch (error: IllegalArgumentException) {
                    assertContains(error.message.orEmpty(), "empty")
                    rejected = true
                }
                if (!rejected || files.any { File(root, it).readText() != "existing $it" }) failures += index
            }
        }
        assertTrue(failures.isEmpty(), "accepted empty schema names: $failures")
    }

    @Test
    fun `scalar classification covers all supported kinds without accepting unknown names`() {
        for (type in listOf("Int", "Float", "String", "Boolean", "DateTime", "Duration", "UUID", "Bytes", "Decimal", "JSON", "Enum")) {
            assertTrue(LuxoCodegen.isScalar(type), type)
        }
        for (type in listOf("", "Missing", "Int64")) assertFalse(LuxoCodegen.isScalar(type), type)
    }

    @Test
    fun `undeclared member types reject before replacing files`() {
        val failures = mutableListOf<String>()
        for (target in listOf("models", "types", "params", "unused")) {
            for (type in listOf("", "Missing", "Int64")) for (list in listOf(false, true)) {
                val field = """{"id":1,"name":"value","type":"$type","isList":$list}"""
                val body = if (target == "params")
                    """{"apis":{"read":{"id":1,"name":"read","module":"test","returnType":"Int","params":[$field]}}}"""
                else {
                    val directory = if (target == "unused") "types" else target
                    val usage = if (target == "unused") ",\"usage\":\"unused\"" else ""
                    """{"$directory":{"Payload":{"name":"Payload"$usage,"fields":[$field]}}}"""
                }
                withIntrospection(body.toByteArray()) { endpoint, root ->
                    val files = listOf("Types.kt", "Schema.kt", "LuxoClient.kt", "Queries.kt", "schema.json")
                    for (name in files) File(root, name).writeText("existing $name")
                    var rejected = false
                    try {
                        LuxoCodegen.generate(endpoint, "test-key", root.path, schemaFile = File(root, "schema.json").path)
                    } catch (error: RuntimeException) {
                        rejected = error.message.orEmpty().contains("type", ignoreCase = true)
                    }
                    if (!rejected || files.any { File(root, it).readText() != "existing $it" }) {
                        failures += "$target/type=$type/list=$list"
                    }
                }
            }
        }
        assertTrue(failures.isEmpty(), failures.joinToString())
    }

    @Test
    fun `Schema enum values reject non-string members`() {
        for (value in listOf("null", "false", "true", "0", "7", "[]", "{}")) {
            assertFailsWith<SerializationException>("enum value=$value") {
                Json.decodeFromString<LuxoEnum>("""{"name":"State","values":[$value]}""")
            }
        }
        assertEquals(listOf("READY", "0"), Json.decodeFromString<LuxoEnum>("""{"name":"State","values":["READY","0"]}""").values)
    }

    @Test
    fun `Schema member strings reject malformed JSON values`() {
        for (target in listOf("model", "type", "param")) {
            fun document(field: String): String = when (target) {
                "model" -> """{"models":{"Payload":{"name":"Payload","fields":[$field]}}}"""
                "type" -> """{"types":{"Input":{"name":"Input","fields":[$field]}}}"""
                else -> """{"apis":{"read":{"id":1,"name":"read","module":"test","params":[$field]}}}"""
            }
            Json.decodeFromString<LuxoSchema>(document("""{"id":1,"name":"value","type":"String"}"""))
            for (member in listOf("name", "type", "typeName")) {
                val values = if (member == "typeName") listOf("false", "true", "0", "7", "[]", "{}")
                    else listOf("missing", "null", "false", "true", "0", "7", "[]", "{}")
                for (value in values) {
                    val properties = mutableListOf("\"id\":1")
                    if (member != "name") properties += "\"name\":\"value\""
                    if (member != "type") properties += "\"type\":\"String\""
                    if (value != "missing") properties += "\"$member\":$value"
                    val field = "{${properties.joinToString(",")}}"
                    assertFailsWith<SerializationException>("$target.$member=$value") {
                        Json.decodeFromString<LuxoSchema>(document(field))
                    }
                }
            }
            Json.decodeFromString<LuxoSchema>(document("""{"id":1,"name":"value","type":"String","typeName":null}"""))
        }
    }

    @Test
    fun `Schema flags retain the generic primitive decoder contract`() {
        val strategy = object : kotlinx.serialization.DeserializationStrategy<Boolean> {
            override val descriptor = SchemaBooleanSerializer.descriptor
            override fun deserialize(decoder: kotlinx.serialization.encoding.Decoder): Boolean =
                SchemaBooleanSerializer.deserialize(object : kotlinx.serialization.encoding.Decoder by decoder {})
        }
        assertTrue(Json.decodeFromString(strategy, "true"))
        assertFalse(Json.decodeFromString(strategy, "false"))
    }

    @Test
    fun `introspection rejects quoted Schema flags without replacing files`() {
        val documents = listOf(
            """{"apis":{"read":{"id":1,"name":"read","module":"test","returnType":"String","returnList":"false"}}}""",
            """{"models":{"Payload":{"name":"Payload","fields":[{"id":1,"name":"value","type":"String","nullable":"false"}]}}}""",
            """{"apis":{"read":{"id":1,"name":"read","module":"test","returnType":"String","params":[{"id":1,"name":"value","type":"String","hasDefault":"false"}]}}}""",
        )
        for (document in documents) withIntrospection(document.toByteArray()) { endpoint, root ->
            val files = listOf("Types.kt", "Schema.kt", "LuxoClient.kt", "Queries.kt", "schema.json")
            for (name in files) File(root, name).writeText("existing $name")
            val error = assertFailsWith<RuntimeException> {
                LuxoCodegen.generate(endpoint, "test-key", root.path, schemaFile = File(root, "schema.json").path)
            }
            assertContains(error.message.orEmpty(), "invalid JSON")
            for (name in files) assertEquals("existing $name", File(root, name).readText())
        }
    }

    @Test
    fun `Schema flags reject non-boolean JSON metadata`() {
        val groups = listOf(
            "api" to listOf("internal", "returnList", "returnNullable", "paginated", "stream", "deprecated"),
            "field" to listOf("nullable", "isList", "relation", "computed", "primaryKey"),
            "param" to listOf("nullable", "isList", "hasDefault"),
        )
        for ((group, flags) in groups) for (flag in flags) {
            for (value in listOf("true", "false", "\"true\"", "\"false\"", "0", "1", "[]", "{}")) {
                val field = """{"id":1,"name":"value","type":"String","$flag":$value}"""
                val api = """{"id":1,"name":"read","module":"test","returnType":"String""""
                val body = when (group) {
                    "api" -> """{"apis":{"read":$api,"$flag":$value}}}"""
                    "field" -> """{"models":{"Payload":{"name":"Payload","fields":[$field]}}}"""
                    else -> """{"apis":{"read":$api,"params":[$field]}}}"""
                }
                if (value == "true" || value == "false") {
                    val schema = Json.decodeFromString<LuxoSchema>(body)
                    val encoded = Json.encodeToString(LuxoSchema.serializer(), schema)
                    assertEquals(schema, Json.decodeFromString<LuxoSchema>(encoded))
                } else assertFailsWith<SerializationException>("$group.$flag=$value") {
                    Json.decodeFromString<LuxoSchema>(body)
                }
            }
        }
    }

    @Test
    fun `runtime artifact excludes obsolete API-wide hint entry points`() {
        for (name in listOf("SelectAnalyzer", "SelectAnalyzerCli")) {
            assertFailsWith<ClassNotFoundException> {
                Class.forName("com.luxo.client.$name", false, javaClass.classLoader)
            }
        }
    }

    @Test
    fun `output getters and APIs carry static selection markers without runtime hint fallback`() {
        val schema = LuxoSchema(
            models = mapOf("User" to LuxoModel("User", fields = listOf(LuxoField(1, "id", "Int")))),
            apis = mapOf("getUser" to LuxoAPI(1, "getUser", "user", returnType = "User")),
        )
        assertContains(LuxoCodegen.genTypes(schema, "probe"), "@get:LuxoSelectionField(\"id\")")
        val client = LuxoCodegen.genClient(schema, "probe")
        assertContains(client, "@LuxoSelection")
        assertFalse(client.contains("hint("))
        assertFalse(client.contains("SelectHints"))
    }

    @Test
    fun `stable numbering rejects invalid and duplicate IDs before writing`() {
        for (target in listOf("models", "types", "apis", "params")) {
            val maximum = when (target) { "apis" -> 2147483647L; "params" -> 2147483645L; else -> 65535L }
            for (id in listOf(0L, -1L, maximum + 1)) {
                rejectNumberedSchema(numberedSchema(target, id), id <= Int.MAX_VALUE)
            }
            rejectNumberedSchema(numberedSchema(target, 1, duplicate = true), true)
        }
    }

    @Test
    fun `stable numbering preserves legal sparse maxima`() {
        for ((target, id) in listOf("models" to 65535L, "types" to 65535L,
            "apis" to 2147483647L, "params" to 2147483645L)) {
            withIntrospection(numberedSchema(target, id)) { endpoint, root ->
                LuxoCodegen.generate(endpoint, "test-key", File(root, "generated").path)
                assertContains(File(root, "generated/Schema.kt").readText(), id.toString())
            }
        }
    }

    private fun rejectNumberedSchema(body: ByteArray, checkMessage: Boolean) {
        withIntrospection(body) { endpoint, root ->
            val output = File(root, "generated").also { it.mkdirs() }
            File(output, "Types.kt").writeText("existing client")
            val snapshot = File(root, "schema.json").also { it.writeText("existing schema") }
            val error = assertFailsWith<RuntimeException> {
                LuxoCodegen.generate(endpoint, "test-key", output.path, "example", snapshot.path)
            }
            if (checkMessage) assertContains(error.message.orEmpty(), "ID")
            assertEquals("existing client", File(output, "Types.kt").readText())
            assertEquals("existing schema", snapshot.readText())
            assertEquals(setOf("Types.kt"), output.list()?.toSet())
        }
    }

    private fun numberedSchema(target: String, id: Long, duplicate: Boolean = false): ByteArray {
        val field = """{"id":$id,"name":"value","type":"Int"}"""
        val fields = "[$field" + (if (duplicate) "," + field.replace("value", "other") else "") + "]"
        val apiID = if (target == "apis") id else 1
        val params = if (target == "params") ",\"params\":$fields" else ""
        val api = """{"id":$apiID,"name":"read","module":"test","returnType":"Int"$params}"""
        val apis = """"apis":{"read":$api""" +
            (if (target == "apis" && duplicate) ",\"other\":" + api.replace("read", "other") else "") + "}"
        val models = if (target == "models" || target == "types")
            ""","$target":{"Payload":{"name":"Payload","fields":$fields}}""" else ""
        return "{$apis$models}".toByteArray()
    }

    @Test
    fun internalRoutesUseExplicitMetadataInsteadOfNames() {
        val fixture = Json.decodeFromString<LuxoSchema>("""
            {"models":{},"apis":{
                "ping":{"id":1,"name":"ping","module":"test","returnType":"Int","internal":false},
                "privateOperation":{"id":2,"name":"privateOperation","module":"test","returnType":"Int","internal":true}
            }}
        """.trimIndent())
        for (source in listOf(LuxoCodegen.genClient(fixture, "test"), LuxoCodegen.genSchemaMap(fixture, "test"))) {
            assertContains(source, "ping")
            assertFalse(source.contains("privateOperation"))
        }
    }

    @Test
    fun enumArraysUseScalarReadersInRowsAndColumns() {
        val fixture = LuxoSchema(models = mapOf("EnumRecord" to LuxoModel(
            name = "EnumRecord", usage = TypeUsage.OUTPUT,
            fields = listOf(LuxoField(1, "states", "Enum", typeName = "State", nullable = true, isList = true)),
        )), enums = mapOf("State" to LuxoEnum("State", listOf("OPEN"))))
        val generated = LuxoCodegen.genTypes(fixture, "com.example")
        assertContains(generated, "if (dec.readBool()) dec.readArray { State.fromValue(dec.readString()) } else null")
        assertContains(generated, "cell.readArray { State.fromValue(cell.readString()) }")
    }

    @Test
    fun nullableArrayColumnsKeepPresenceOutsideTheCellPayload() {
        val fixture = LuxoSchema(models = mapOf("ArrayRecord" to LuxoModel(
            name = "ArrayRecord", usage = TypeUsage.OUTPUT,
            fields = listOf(LuxoField(1, "values", "Int", nullable = true, isList = true)),
        )))
        val generated = LuxoCodegen.genTypes(fixture, "com.example")
        assertContains(generated, "if (dec.readBool()) dec.readArray { dec.readInt() } else null")
        assertContains(generated, "List<ByteArray?>")
        assertContains(generated, "_values = dec.readColumnBytesPtr()")
        assertContains(generated, "_values[i]?.let { data -> LuxoDecoder(data).let { cell -> val value = cell.readArray { cell.readInt() }; cell.checkComplete(); value } }")
        assertFalse(generated.contains("cell.readBool()"))
    }
    private val schema = LuxoSchema(
        models = mapOf(
            "Payload" to LuxoModel(
                name = "Payload",
                usage = TypeUsage.OUTPUT,
                fields = listOf(
                    LuxoField(1, "id", "Int"),
                    LuxoField(2, "blob", "Bytes"),
                    LuxoField(3, "metadata", "JSON"),
                ),
            ),
        ),
        types = mapOf(
            "CreateInput" to LuxoTypeDecl(
                name = "CreateInput",
                usage = TypeUsage.INPUT,
                fields = listOf(LuxoField(1, "name", "String")),
            ),
        ),
        apis = mapOf(
            "createPayload" to LuxoAPI(
                id = 6,
                name = "createPayload",
                module = "file",
                returnType = "Payload",
                params = listOf(
					LuxoParam(1, "input", "Model", typeName = "CreateInput"),
                    LuxoParam(2, "note", "String", nullable = true),
                    LuxoParam(3, "caption", "String", nullable = true, hasDefault = true),
                ),
            ),
            "listPayloads" to LuxoAPI(
                id = 7,
                name = "listPayloads",
                module = "file",
                returnType = "Payload",
                returnList = true,
            ),
            "listPayloadPage" to LuxoAPI(
                id = 8,
                name = "listPayloadPage",
                module = "file",
                returnType = "Payload",
                returnList = true,
                paginated = true,
                params = listOf(LuxoParam(7, "page", "Int", hasDefault = true), LuxoParam(9, "pageSize", "Int", hasDefault = true)),
            ),
            "watchPayload" to LuxoAPI(
                id = 9,
                name = "watchPayload",
                module = "file",
                returnType = "Payload",
                stream = true,
                params = listOf(LuxoParam(1, "projectId", "Int")),
            ),
            "searchPayloads" to LuxoAPI(
                id = 10,
                name = "searchPayloads",
                module = "file",
                returnType = "Payload",
                returnList = true,
                paginated = true,
                params = listOf(LuxoParam(7, "page", "Int", hasDefault = true), LuxoParam(9, "pageSize", "Int", hasDefault = true)),
            ),
            "createSnapshot" to LuxoAPI(
                id = 11,
                name = "createSnapshot",
                module = "file",
                returnType = "Payload",
            ),
        ),
    )

    @Test
    fun `paginated decoders never narrow int64 metadata`() {
        val types = LuxoCodegen.genTypes(schema, "com.example")
        val client = LuxoCodegen.genClient(schema, "com.example")
        assertContains(types, "return Page(items, total, page, pageSize)")
        for (name in listOf("total", "page", "pageSize")) {
            assertContains(types, "val $name = dec.readSvarint()")
            assertContains(client, "decodeLuxoJsonInt(obj.getValue(\"$name\"))")
        }
    }

    @Test
    fun `introspection key is sent only in the canonical header`() {
        val connection = LuxoCodegen.introspectionConnection("https://api.example.com/luvia", "secret-key")

        assertEquals("secret-key", connection.getRequestProperty("X-Introspection-Key"))
        assertEquals("\$schema", connection.url.query)
        assertFalse(connection.url.toString().contains("key="))
        connection.disconnect()
    }

    @Test
    fun `introspection preserves UTF-8 descriptions and accepts one document BOM`() {
        val description = "\uFEFF发布检查 café \uFFFD"
        val document = schemaBytes(description.toByteArray()).decodeToString()
        val expected = Json.decodeFromString<LuxoSchema>(document)
        for (charset in listOf("utf-8", "iso-8859-1")) {
            for (bom in listOf(byteArrayOf(), byteArrayOf(-17, -69, -65))) {
                withIntrospection(bom + document.toByteArray(), charset = charset) { endpoint, root ->
                    val output = File(root, "generated")
                    val snapshot = File(root, "snapshot/schema.json")
                    LuxoCodegen.generate(endpoint, "test-key", output.path, "example", snapshot.path)
                    assertEquals(document, snapshot.readText())
                    assertEquals(description, Json.decodeFromString<LuxoSchema>(snapshot.readText()).apis.getValue("echo").description)
                    assertEquals(LuxoCodegen.genClient(expected, "example"), File(output, "LuxoClient.kt").readText())
                    assertEquals(setOf("Types.kt", "Schema.kt", "LuxoClient.kt", "Queries.kt"), output.list()?.toSet())
                }
            }
        }
    }

    @Test
    fun `introspection rejects malformed text without creating or overwriting outputs`() {
        val malformed = listOf(byteArrayOf(-1), byteArrayOf(-64, -128), byteArrayOf(-19, -96, -128),
            byteArrayOf(-12, -112, -128, -128), byteArrayOf(-30, -126))
        for (existing in listOf(false, true)) {
            for (bytes in malformed + listOf("\\uD800", "\\uDC00", "\\uD800\\u0061").map { it.encodeToByteArray() }) {
                withIntrospection(schemaBytes(bytes)) { endpoint, root ->
                    val output = File(root, "generated")
                    val snapshot = File(root, "schema.json")
                    if (existing) {
                        output.mkdirs()
                        File(output, "Types.kt").writeText("existing generated code")
                        snapshot.writeText("existing schema")
                    }
                    val error = assertFailsWith<RuntimeException> {
                        LuxoCodegen.generate(endpoint, "test-key", output.path, "example", snapshot.path)
                    }
                    assertContains(error.message.orEmpty(), "introspection returned invalid JSON")
                    if (existing) {
                        assertEquals("existing schema", snapshot.readText())
                        assertEquals(setOf("Types.kt"), output.list()?.toSet())
                        assertEquals("existing generated code", File(output, "Types.kt").readText())
                    } else {
                        assertFalse(output.exists())
                        assertFalse(snapshot.exists())
                    }
                }
            }
        }
    }

    @Test
    fun `introspection rejects invalid JSON and HTTP failures before writing files`() {
        val bodies = listOf(byteArrayOf(-17, -69), "\uFEFF\uFEFF{}".toByteArray(),
            "[]".toByteArray(), "{\"apis\":".toByteArray(),
            byteArrayOf(-1, -2) + "{}".toByteArray(Charsets.UTF_16LE))
        for ((status, body) in bodies.map { 200 to it } + (503 to "{}".toByteArray())) {
            withIntrospection(body, status) { endpoint, root ->
                val error = assertFailsWith<RuntimeException> {
                    LuxoCodegen.generate(endpoint, "test-key", File(root, "generated").path,
                        schemaFile = File(root, "schema.json").path)
                }
                assertContains(error.message.orEmpty(), if (status == 200) "invalid JSON" else "HTTP 503")
                assertTrue(root.list().orEmpty().isEmpty())
            }
        }
    }

    @Test
    fun `invalid enum metadata preserves existing generated files and snapshot`() {
        val invalid = schema.copy(types = mapOf("Broken" to LuxoTypeDecl("Broken",
            fields = listOf(LuxoField(1, "state", "Enum", typeName = "Missing")))))
        val body = Json.encodeToString(LuxoSchema.serializer(), invalid).toByteArray()
        for (existing in listOf(false, true)) withIntrospection(body) { endpoint, root ->
            val output = File(root, "generated")
            val snapshot = File(root, "schema.json")
            if (existing) {
                output.mkdirs()
                File(output, "Types.kt").writeText("existing client")
                snapshot.writeText("existing schema")
            }
            assertFailsWith<IllegalArgumentException> {
                LuxoCodegen.generate(endpoint, "test-key", output.path, "example", snapshot.path)
            }
            if (existing) {
                assertEquals("existing client", File(output, "Types.kt").readText())
                assertEquals("existing schema", snapshot.readText())
                assertEquals(setOf("Types.kt"), output.list()?.toSet())
            } else {
                assertFalse(output.exists())
                assertFalse(snapshot.exists())
            }
        }
    }

    @Test
    fun `missing model references preserve generated files and snapshot`() {
        for (target in listOf("models", "types", "params")) {
            for (name in listOf(null, "", "Missing")) for (list in listOf(false, true)) {
                val reference = name?.let { ",\"typeName\":\"$it\"" }.orEmpty()
                val field = """{"id":1,"name":"child","type":"Model","isList":$list,"nullable":$list$reference}"""
                val body = if (target == "params") {
                    """{"apis":{"read":{"id":1,"name":"read","module":"test","returnType":"Int","params":[$field]}}}"""
                } else {
                    """{"$target":{"Payload":{"name":"Payload","fields":[$field]}}}"""
                }
                withIntrospection(body.toByteArray()) { endpoint, root ->
                    val output = File(root, "generated").also { it.mkdirs() }
                    val types = File(output, "Types.kt").also { it.writeText("existing client") }
                    val snapshot = File(root, "schema.json").also { it.writeText("existing schema") }
                    val error = assertFailsWith<IllegalArgumentException>("$target/$name/$list") {
                        LuxoCodegen.generate(endpoint, "test-key", output.path, "example", snapshot.path)
                    }
                    assertContains(error.message.orEmpty(), "Model requires a declared typeName")
                    assertEquals("existing client", types.readText())
                    assertEquals("existing schema", snapshot.readText())
                    assertEquals(setOf("Types.kt"), output.list()?.toSet())
                }
            }
        }
    }

    @Test
    fun `declared recursive model and type references generate successfully`() {
        val body = """{
            "models":{"Container":{"name":"Container","fields":[
                {"id":1,"name":"node","type":"Model","typeName":"Node"}]}},
            "types":{"Node":{"name":"Node","fields":[
                {"id":1,"name":"child","type":"Model","typeName":"Node","nullable":true},
                {"id":2,"name":"parent","type":"Model","typeName":"Container","nullable":true}]}},
            "apis":{"read":{"id":1,"name":"read","module":"test","returnType":"Container"}}
        }"""
        withIntrospection(body.toByteArray()) { endpoint, root ->
            LuxoCodegen.generate(endpoint, "test-key", root.path, "example")
            val source = File(root, "Types.kt").readText()
            assertContains(source, "class Container")
            assertContains(source, "class Node")
        }
    }

    @Test
    fun `omitted API returns preserve existing files`() {
        for (list in listOf(false, true)) {
            val body = """{"apis":{"read":{"id":1,"name":"read","module":"test","returnList":$list}}}"""
            withIntrospection(body.toByteArray()) { endpoint, root ->
                val output = File(root, "generated").also { it.mkdirs() }
                val types = File(output, "Types.kt").also { it.writeText("existing client") }
                val error = assertFailsWith<IllegalArgumentException> {
                    LuxoCodegen.generate(endpoint, "test-key", output.path, "example")
                }
                assertContains(error.message.orEmpty(), "return type")
                assertEquals("existing client", types.readText())
                assertEquals(setOf("Types.kt"), output.list()?.toSet())
            }
        }
    }

    @Test
    fun `undeclared API returns preserve existing files`() {
        for (type in listOf("Missing", "", "Enum", "Void")) for (list in listOf(false, true)) {
            val body = """{"apis":{"read":{"id":1,"name":"read","module":"test","returnType":"$type","returnList":$list}}}"""
            withIntrospection(body.toByteArray()) { endpoint, root ->
                val output = File(root, "generated").also { it.mkdirs() }
                val types = File(output, "Types.kt").also { it.writeText("existing client") }
                val snapshot = File(root, "schema.json").also { it.writeText("existing schema") }
                val error = assertFailsWith<IllegalArgumentException> {
                    LuxoCodegen.generate(endpoint, "test-key", output.path, "example", snapshot.path)
                }
                assertContains(error.message.orEmpty(), "return type")
                assertEquals("existing client", types.readText())
                assertEquals("existing schema", snapshot.readText())
                assertEquals(setOf("Types.kt"), output.list()?.toSet())
            }
        }
    }

    @Test
    fun `mismatched declaration names preserve existing files`() {
        for (target in listOf("models", "types", "apis")) {
            val declaration = if (target == "apis") {
                """{"id":1,"name":"Other","module":"test","returnType":"Int"}"""
            } else {
                """{"name":"Other","fields":[{"id":1,"name":"id","type":"Int"}]}"""
            }
            withIntrospection("""{"$target":{"Payload":$declaration}}""".toByteArray()) { endpoint, root ->
                val types = File(root, "Types.kt").also { it.writeText("existing client") }
                val error = assertFailsWith<IllegalArgumentException> {
                    LuxoCodegen.generate(endpoint, "test-key", root.path)
                }
                assertContains(error.message.orEmpty(), "does not match")
                assertEquals("existing client", types.readText())
                assertEquals(setOf("Types.kt"), root.list()?.toSet())
            }
        }
    }

    @Test
    fun `duplicate member names with distinct IDs preserve existing files`() {
        for (target in listOf("models", "types", "params")) for (type in listOf("Int", "String")) {
            val fields = """[{"id":1,"name":"value","type":"Int"},{"id":2,"name":"value","type":"$type"}]"""
            val body = if (target == "params") {
                """{"apis":{"read":{"id":1,"name":"read","module":"test","returnType":"Int","params":$fields}}}"""
            } else {
                """{"$target":{"Payload":{"name":"Payload","fields":$fields}}}"""
            }
            withIntrospection(body.toByteArray()) { endpoint, root ->
                val types = File(root, "Types.kt").also { it.writeText("existing client") }
                val error = assertFailsWith<IllegalArgumentException> {
                    LuxoCodegen.generate(endpoint, "test-key", root.path)
                }
                assertContains(error.message.orEmpty(), "duplicate name value")
                assertEquals("existing client", types.readText())
                assertEquals(setOf("Types.kt"), root.list()?.toSet())
            }
        }
    }

    @Test
    fun `cross namespace type collisions preserve existing files`() {
        for (pair in listOf("model-type", "model-enum", "type-enum")) {
            val declarations = mutableListOf<String>()
            val fields = """{"Payload":{"name":"Payload","fields":[{"id":1,"name":"id","type":"Int"}]}}"""
            if ("model" in pair) declarations += "\"models\":$fields"
            if ("type" in pair) declarations += "\"types\":$fields"
            if ("enum" in pair) declarations += """"enums":{"Payload":{"name":"Payload","values":["OPEN"]}}"""
            withIntrospection("{${declarations.joinToString(",")}}".toByteArray()) { endpoint, root ->
                val types = File(root, "Types.kt").also { it.writeText("existing client") }
                val error = assertFailsWith<IllegalArgumentException> {
                    LuxoCodegen.generate(endpoint, "test-key", root.path)
                }
                assertContains(error.message.orEmpty(), "duplicate type")
                assertEquals("existing client", types.readText())
                assertEquals(setOf("Types.kt"), root.list()?.toSet())
            }
        }
    }

    private fun schemaBytes(description: ByteArray): ByteArray =
        """{"apis":{"echo":{"id":1,"name":"echo","module":"sample","returnType":"String","description":"""".toByteArray() +
            description + "\"}}}".toByteArray()

    @Test
    fun introspectionPreservesRawQueryAndIgnoresFragment() {
        for ((suffix, query) in listOf(
            "" to "\$schema",
            "?" to "\$schema",
            "?tenant=a%2Fb&tag=one&tag=two" to "tenant=a%2Fb&tag=one&tag=two&\$schema",
            "#section" to "\$schema",
            "?tenant=a%26b#section" to "tenant=a%26b&\$schema",
        )) {
            val connection = LuxoCodegen.introspectionConnection("https://api.example.com/luvia$suffix", "secret")
            assertEquals(query, connection.url.query)
            assertEquals(null, connection.url.ref)
            assertEquals("/luvia", connection.url.path)
            connection.disconnect()
        }
    }

    @Test
    fun introspectionDoesNotForwardKeyThroughRedirect() = withCodegenDirectory("introspection-redirect-") { root ->
        val source = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
        val target = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
        val received = java.util.concurrent.atomic.AtomicReference<String?>()
        target.createContext("/capture") { exchange ->
            exchange.use {
                received.set(it.requestHeaders.getFirst("X-Introspection-Key"))
                val body = numberedSchema("apis", 1)
                it.sendResponseHeaders(200, body.size.toLong())
                it.responseBody.use { output -> output.write(body) }
            }
        }
        source.createContext("/luvia") { exchange ->
            exchange.use {
                it.responseHeaders.set("Location", "http://127.0.0.1:${target.address.port}/capture")
                it.sendResponseHeaders(302, -1)
            }
        }
        try {
            target.start()
            source.start()
            val result = runCatching {
                LuxoCodegen.generate("http://127.0.0.1:${source.address.port}/luvia", "test-secret", root.path)
            }
            assertEquals(null, received.get(), "introspection key reached another origin")
            assertTrue(result.isFailure, "redirect must not silently replace the Schema endpoint")
            assertEquals(emptyList(), root.listFiles()?.toList())
        } finally {
            source.stop(0)
            target.stop(0)
        }
    }

    @Test
    fun introspectionWithQueryGeneratesThroughHTTP() {
        val query = "tenant=a%2Fb&tag=one&tag=two"
        withIntrospection(numberedSchema("apis", 1), expectedQuery = "$query&\$schema") { endpoint, root ->
            LuxoCodegen.generate("$endpoint?$query#section", "test-key", File(root, "generated").path)
            assertContains(File(root, "generated/LuxoClient.kt").readText(), "fun read(")
        }
    }

    private fun withIntrospection(
        body: ByteArray, status: Int = 200, charset: String = "utf-8",
        expectedQuery: String = "\$schema", block: (String, File) -> Unit,
    ) = withCodegenDirectory("kotlin-introspection-") { root ->
        val server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
        val executor = Executors.newSingleThreadExecutor()
        try {
            server.executor = executor
            server.createContext("/luvia") { exchange ->
                exchange.use {
                    val authorized = it.requestURI.rawQuery == expectedQuery &&
                        it.requestHeaders.getFirst("X-Introspection-Key") == "test-key"
                    it.responseHeaders.set("Content-Type", "application/json; charset=$charset")
                    it.sendResponseHeaders(if (authorized) status else 403, body.size.toLong())
                    it.responseBody.use { output -> output.write(body) }
                }
            }
            server.start()
            block("http://127.0.0.1:${server.address.port}/luvia", root)
        } finally {
            server.stop(0)
            executor.shutdownNow()
        }
    }

    @Test
    fun `generates canonical columnar and paginated decoders`() {
        val types = LuxoCodegen.genTypes(schema, "com.example")
        val client = LuxoCodegen.genClient(schema, "com.example")
		val wireSchema = LuxoCodegen.genSchemaMap(schema, "com.example")

        assertContains(types, "fun decodeColumnarPayload(data: ByteArray): List<Payload>")
        assertContains(types, "fun decodePaginatedPayload(data: ByteArray): Page<Payload>")
        assertContains(types, "import com.luxo.client.ColumnarDecoder")
        assertContains(types, "import com.luxo.client.Page")
        assertContains(types, "val id: Selected<Long> = Selected.unselected()")
        assertContains(types, "Selected.value(dec.readInt())")
        assertFalse(types.contains("?: 0L"))
        assertContains(types, "fun decodeJSONPayload(data: JsonObject): Payload")
        assertContains(types, "Base64.getDecoder().decode")
        assertContains(types, "dec.readColumnBytes()")
        assertContains(types, "decodeBoundedLuxoJSON")
        assertFalse(types.contains("!!"))
        assertContains(client, "decodeColumnarPayload(data)")
        assertContains(client, "decodePaginatedPayload(data)")
        assertContains(client, "input: CreateInput")
		assertFalse(client.contains("Json.encodeToJsonElement(input)"))
		assertContains(types, "data class CreateInput(")
		assertContains(types, ") : LuxoBinaryEncodable")
		assertContains(types, "override fun writeLuxo(encoder: LuxoEncoder)")
		assertContains(types, "encoder.writeString(this.name)")
		assertContains(wireSchema, "ParamSchema(1, \"input\", \"Model\", isList = false, nullable = false, typeName = \"CreateInput\")")
		assertContains(wireSchema, "SelectionFieldSchema(1, type = \"String\"")
        assertContains(client, "note: String?")
        assertContains(client, "caption: LuxoOptional<String> = LuxoOptional.Absent")
        assertContains(client, "is LuxoOptional.Present) put(\"caption\"")
        assertContains(client, "filters: List<LuxoFilter>? = null")
        assertContains(client, "override val filters: List<LuxoFilter>?")
        assertContains(client, "override val selectionPlan: PreparedSelection?")
        assertContains(client, "val sel = select ?: `\$selectionPlan`?.text")
        assertContains(client, "suspend fun subscribeWatchPayload(projectId: Long, select: String? = null, `\$selectionPlan`: PreparedSelection? = null, observer: StreamObserver? = null, onData: (Payload) -> kotlin.Unit): () -> kotlin.Unit")
        assertContains(client, "transport.subscribe(\"watchPayload\", { data -> decode_watchPayload(data) }, _watchPayloadRequest(projectId, select ?: `\$selectionPlan`?.text, `\$selectionPlan`), observer, onData)")
        assertFalse(client.contains("mutableMapOf<String, Any?>"))
        assertFalse(client.contains("onData(decode_watchPayload(data))"))
        assertContains(client, "endpoint.startsWith(\"ws://\") || endpoint.startsWith(\"wss://\")")
        assertContains(client, "LuxoWebSocket(endpoint, token = token)")
        assertContains(client, "suspend fun searchPayloads(page: Long? = null, pageSize: Long? = null, select: String? = null, `\$selectionPlan`: PreparedSelection? = null, filters: List<LuxoFilter>? = null, sorters: List<LuxoSorter>? = null)")
        assertContains(client, "suspend fun createSnapshot(select: String? = null, `\$selectionPlan`: PreparedSelection? = null): Payload")
        assertFalse(client.contains("createSnapshot(input:"))
        assertContains(client, "suspend fun createPayload(input: CreateInput, note: String?, caption: LuxoOptional<String> = LuxoOptional.Absent, select: String? = null, `\$selectionPlan`: PreparedSelection? = null)")
        assertFalse(client.contains("try { SelectHints.hints[api] }"))
        assertFalse(client.contains("!!"))
    }

    @Test
    fun `generated inputs project every JSON field without serializer discovery`() {
        val fields = listOf("Int", "Duration", "Float", "String", "Boolean", "DateTime", "UUID", "Decimal", "Bytes", "JSON", "Enum", "Model")
            .flatMapIndexed { index, type ->
                val name = type.lowercase()
                val typeName = when (type) { "Enum" -> "Role"; "Model" -> "Child"; else -> type }
                listOf(
                    LuxoField(index * 4 + 1, name, type, typeName),
                    LuxoField(index * 4 + 2, "${name}Optional", type, typeName, nullable = true),
                    LuxoField(index * 4 + 3, "${name}List", type, typeName, isList = true),
                    LuxoField(index * 4 + 4, "${name}OptionalList", type, typeName, nullable = true, isList = true),
                )
            }
        val fixture = LuxoSchema(
            enums = mapOf("Role" to LuxoEnum("Role", listOf("MEMBER"))),
            types = mapOf(
                "Input" to LuxoTypeDecl("Input", TypeUsage.INPUT, fields),
                "Child" to LuxoTypeDecl("Child", TypeUsage.INPUT, listOf(LuxoField(1, "value", "Int"))),
            ),
        )
        val generated = LuxoCodegen.genTypes(fixture, "com.example")
        assertFalse(generated.contains("encodeToJsonElement"))
        assertContains(generated, "override fun toLuxoJson(): JsonElement = `toLuxoJson\$depth`(1)")
        for (field in fields) assertContains(generated, "put(\"${field.name}\",")
        assertContains(generated, "JsonPrimitive(this@Input.int)")
        assertContains(generated, "luxoJsonFloat(this@Input.float)")
        assertContains(generated, "java.util.Base64.getEncoder().encodeToString(this@Input.bytes)")
        assertContains(generated, "put(\"json\", decodeLuxoJsonValue(this@Input.json))")
        assertContains(generated, "this@Input.model.`toLuxoJson\$depth`(`\$modelDepth` + 1)")
        assertContains(generated, "JsonArray(this@Input.modelList.map { item -> item.`toLuxoJson\$depth`(`\$modelDepth` + 1) })")
        assertContains(generated, "this@Input.intOptional?.let { value -> JsonPrimitive(value) } ?: JsonNull")
    }

    @Test
    fun `separates strict input DTOs from selected output models`() {
        val shared = LuxoSchema(
            models = emptyMap(),
            types = mapOf(
                "Profile" to LuxoTypeDecl(
                    name = "Profile",
                    usage = TypeUsage.UNUSED,
                    fields = listOf(
                        LuxoField(1, "name", "String"),
                        LuxoField(2, "bio", "String", nullable = true),
                    ),
                ),
            ),
            apis = mapOf(
                "updateProfile" to LuxoAPI(
                    id = 1,
                    name = "updateProfile",
                    module = "profile",
                    returnType = "Profile",
                    params = listOf(LuxoParam(1, "profile", "Model", typeName = "Profile")),
                ),
            ),
        )

        val types = LuxoCodegen.genTypes(shared, "com.example")
        val client = LuxoCodegen.genClient(shared, "com.example")

        assertContains(types, "data class Profile(")
        assertContains(types, "val name: Selected<String> = Selected.unselected()")
        assertContains(types, "val bio: Selected<String?> = Selected.unselected()")
        assertContains(types, "@Serializable\ndata class ProfileInput(")
        assertContains(types, "val name: String")
        assertContains(types, "val bio: String? = null")
        assertContains(client, "profile: ProfileInput")
        assertContains(client, "): Profile")
    }
}

internal fun <T> withCodegenDirectory(prefix: String, block: (File) -> T): T {
    val project = File("../..").canonicalFile
    check(File(project, "go.mod").isFile && File(project, "packages/kotlin/src").isDirectory) {
        "codegen tests must run from the Kotlin SDK project directory"
    }
    val scratch = File(project, ".tmp").apply { mkdirs() }
    val root = Files.createTempDirectory(scratch.toPath(), prefix).toFile()
    return try {
        block(root)
    } finally {
        check(root.canonicalFile.parentFile == scratch)
        check(root.deleteRecursively())
    }
}
