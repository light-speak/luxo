package com.luxo.client

import kotlinx.serialization.json.Json
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertEquals
import kotlin.test.assertFails
import java.io.File
import java.nio.file.Files
import java.util.concurrent.TimeUnit

class QueryCodegenTest {
    @Test fun `rejects malformed query capabilities`() {
        for (field in listOf(
            LuxoQueryFilter("raw", "JSON", listOf("eq")),
            LuxoQueryFilter("active", "Boolean", listOf("gt")),
            LuxoQueryFilter("id", "Int", emptyList()),
            LuxoQueryFilter("id", "Int", listOf("eq", "eq")),
            LuxoQueryFilter("bad\"", "Int", listOf("eq")),
        )) {
            val schema = LuxoSchema(apis = mapOf("read" to LuxoAPI(1, "read", "x", paginated = true,
                query = LuxoQueryCapabilities(filters = listOf(field)))))
            assertFails { KotlinQueryCodegen.generate(schema, "probe") }
        }
    }
    @Test fun `query builders follow API capabilities`() {
        val schema = Json.decodeFromString<LuxoSchema>("""{"models":{},"enums":{"State":{"name":"State","values":["OPEN"]}},"apis":{
          "listUsers":{"id":1,"name":"listUsers","module":"user","paginated":true,
            "query":{"filters":[{"field":"id","type":"Int","operators":["eq","gt"]},{"field":"state","type":"State","operators":["eq"]}],"sorters":["id"]}},
          "custom":{"id":2,"name":"custom","module":"user","returnList":true}
        }}""")
        val source = KotlinQueryCodegen.generate(schema, "probe")
        assertContains(source, "object LuxoQuery_listUsers")
        assertContains(source, "fun id_eq(value: Long)")
        assertContains(source, "fun id_desc()")
        assertFalse(source.contains("id_contains"))
        assertFalse(source.contains("LuxoQuery_custom"))
        compileAndRun(source, LuxoCodegen.genTypes(schema, "probe"))
    }

    private fun compileAndRun(source: String, types: String) {
        val root = File(".tmp").apply { mkdirs() }
        val directory = Files.createTempDirectory(root.toPath(), "query-generated-").toFile()
        val classpath = (System.getProperty("luxo.testRuntimeClasspath") ?: System.getProperty("java.class.path"))
            .split(File.pathSeparator).filter { File(it).exists() }.joinToString(File.pathSeparator)
        try {
            val queries = File(directory, "Queries.kt").apply { writeText(source) }
            val models = File(directory, "Types.kt").apply { writeText(types) }
            val consumer = File(directory, "Consumer.kt").apply { writeText("""
                package probe
                fun main() {
                    val f = LuxoQuery_listUsers.id_eq(9007199254740993L)
                    check(f.field == "id" && f.op == "eq" && f.value == 9007199254740993L)
                    check(LuxoQuery_listUsers.id_desc().order == "desc")
                    check(LuxoQuery_listUsers.state_eq(State.fromValue("OPEN")).value == "OPEN")
                    println(f.value)
                }
            """.trimIndent()) }
            val args = listOf("-cp", classpath, "org.jetbrains.kotlin.cli.jvm.K2JVMCompiler", "-no-stdlib", "-no-reflect",
                "-classpath", classpath, "-d", File(directory, "classes").path, queries.path, models.path, consumer.path)
            val compiled = run(directory, args)
            assertEquals(0, compiled.first, compiled.second)
            val executed = run(directory, listOf("-cp", File(directory, "classes").path + File.pathSeparator + classpath, "probe.ConsumerKt"))
            assertEquals(0, executed.first, executed.second)
            assertContains(executed.second, "9007199254740993")
            consumer.writeText("package probe; fun main() { LuxoQuery_listUsers.id_eq(\"42\") }")
            assertNotEquals(0, run(directory, args).first)
        } finally { directory.deleteRecursively() }
    }

    private fun run(directory: File, arguments: List<String>): Pair<Int, String> {
        val log = File(directory, "process.log")
        val process = ProcessBuilder(listOf(File(System.getProperty("java.home"), "bin/java").path) + arguments)
            .redirectErrorStream(true).redirectOutput(log).start()
        try {
            check(process.waitFor(60, TimeUnit.SECONDS)) { "query compiler timed out" }
            return process.exitValue() to log.readText()
        } finally { if (process.isAlive) { process.destroyForcibly(); process.waitFor() } }
    }
}
