package com.luxo.client

import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertFalse
import kotlin.test.assertFailsWith
import kotlin.test.assertEquals
import java.io.File
import java.nio.file.Files
import java.util.concurrent.TimeUnit

class CodegenEnumTest {
    @Test
    fun `unknown-only generated enum compiles and roundtrips`() {
        val root = File(".tmp").apply { mkdirs() }
        val directory = Files.createTempDirectory(root.toPath(), "empty-enum-").toFile().absoluteFile
        try {
            directory.resolve("Types.kt").writeText(LuxoCodegen.genTypes(
                LuxoSchema(enums = mapOf("Empty" to LuxoEnum("Empty"))), "probe"))
            directory.resolve("Probe.kt").writeText("""
                import probe.Empty
                import kotlinx.serialization.json.Json
                fun main() {
                    check(Empty.Known.entries.isEmpty())
                    for (raw in listOf("", "FUTURE", "中文", "é", "e\u0301")) {
                        val value = Empty.fromValue(raw)
                        check(value is Empty.Unknown && value.rawValue == raw)
                        check(value == Empty.fromValue(raw))
                        val json = Json.encodeToString(Empty.Serializer, value)
                        check(Json.decodeFromString(Empty.Serializer, json) == value)
                        val encoder = com.luxo.client.LuxoEncoder()
                        encoder.writeString(value.rawValue)
                        check(com.luxo.client.LuxoDecoder(encoder.bytes()).readString() == raw)
                    }
                    check(Empty.fromValue("é") != Empty.fromValue("e\u0301"))
                    println("empty enum roundtrip passed")
                }
            """.trimIndent())
            val classpath = System.getProperty("luxo.testRuntimeClasspath").split(File.pathSeparator)
                .filter { File(it).exists() }.joinToString(File.pathSeparator)
            val output = directory.resolve("classes").path
            runGenerated(directory, listOf("-cp", classpath, "org.jetbrains.kotlin.cli.jvm.K2JVMCompiler",
                "-no-stdlib", "-no-reflect", "-Werror", "-classpath", classpath, "-d", output,
                directory.resolve("Types.kt").path, directory.resolve("Probe.kt").path))
            runGenerated(directory, listOf("-cp", output + File.pathSeparator + classpath, "ProbeKt"))
        } finally {
            check(directory.toPath().startsWith(root.absoluteFile.toPath()))
            directory.deleteRecursively()
        }
    }

    private fun runGenerated(directory: File, arguments: List<String>) {
        val log = directory.resolve("process.log")
        val java = File(System.getProperty("java.home"), "bin/java").path
        val process = ProcessBuilder(listOf(java, "-Djava.io.tmpdir=$directory") + arguments)
            .redirectErrorStream(true).redirectOutput(log).start()
        try {
            check(process.waitFor(90, TimeUnit.SECONDS)) { "Generated enum compilation timed out" }
            assertEquals(0, process.exitValue(), log.readText())
        } finally {
            if (process.isAlive) { process.destroyForcibly(); process.waitFor() }
        }
    }
    @Test
    fun `empty enums preserve unknown-only generation`() {
        val empty = schema.copy(enums = mapOf("State" to LuxoEnum("State")))
        val source = LuxoCodegen.genTypes(empty, "test")
        assertContains(source, "enum class Known(override val rawValue: String) : State {\n;\n    }")
        assertContains(source, "else -> Unknown(value)")
        assertContains(LuxoCodegen.genClient(empty, "test"), "State.fromValue(dec.readString())")
        System.getenv("LUXO_EMPTY_ENUM_OUTPUT")?.let { output ->
            val directory = java.io.File(output).apply { mkdirs() }
            directory.resolve("Models.kt").writeText(source)
            directory.resolve("Client.kt").writeText(LuxoCodegen.genClient(empty, "test"))
        }
    }
    @Test
    fun `enum descriptors avoid reflection based primitive name registration`() {
        val source = LuxoCodegen.genTypes(schema, "test")
        assertFalse(source.contains("PrimitiveSerialDescriptor"))
    }

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
        assertContains(source, "encoder.writeString(this.state.rawValue)")
        assertContains(source, "luxoUtf8Length(this.state.rawValue)")
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
        val duplicate = schema.copy(enums = mapOf("State" to LuxoEnum("State", listOf("OPEN", "OPEN"))))
        val mismatch = schema.copy(enums = mapOf("State" to LuxoEnum("Other", listOf("OPEN"))))
        val bareReturn = schema.copy(apis = mapOf("bad" to LuxoAPI(1, "bad", "test", returnType = "Enum")))
        for (invalid in listOf(missing, duplicate, mismatch, bareReturn)) {
            assertFailsWith<IllegalArgumentException> { LuxoCodegen.genTypes(invalid, "test") }
            assertFailsWith<IllegalArgumentException> { LuxoCodegen.genClient(invalid, "test") }
        }
    }
}
