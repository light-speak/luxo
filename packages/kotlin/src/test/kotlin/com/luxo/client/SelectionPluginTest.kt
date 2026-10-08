package com.luxo.client

import java.io.File
import java.nio.file.Files
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertContains
import kotlin.test.assertNotEquals

class SelectionPluginTest {
    @Test
    fun `compiler injects selections per resolved call and preserves explicit selection`() {
        verify("""
                import com.luxo.client.LuxoSelection
                import com.luxo.client.LuxoSelectionField
                class User(@get:LuxoSelectionField("id") val id: Long,
                    @get:LuxoSelectionField("name") val name: String)
                class Client {
                    @LuxoSelection fun getUser(select: String? = null): User {
                        println(select)
                        return User(42, "Ada")
                    }
                }
                class Other { fun getUser(): User = User(9, "Other") }
                fun main() {
                    val client = Client()
                    val first = client.getUser()
                    println(first.id)
                    val second = client.getUser()
                    val alias = second
                    println(alias.name)
                    println(client.getUser(select = "id,name").name)
                    println(Other().getUser().name)
                }
            """.trimIndent(), listOf("id", "42", "name", "Ada", "id,name", "Ada", "Other"))
    }

    @Test
    fun `opaque model escape fails even when another field is read locally`() {
        verify("""
            import com.luxo.client.LuxoSelection
            import com.luxo.client.LuxoSelectionField
            class User(@get:LuxoSelectionField("id") val id: Long)
            @LuxoSelection fun getUser(select: String? = null) = User(1)
            fun consume(user: User) { println(user.id) }
            fun main() {
                val user = getUser()
                println(user.id)
                consume(user)
            }
        """.trimIndent(), null)
    }

    @Test
    fun `selected nested fields collections and subscription callbacks belong to their calls`() {
        verify("""
            import com.luxo.client.*
            class Address(@get:LuxoSelectionField("city") val city: Selected<String>)
            class User(@get:LuxoSelectionField("id") val id: Selected<Long>,
                @get:LuxoSelectionField("address") val address: Selected<Address>)
            fun user() = User(Selected.value(42L), Selected.value(Address(Selected.value("Tokyo"))))
            @LuxoSelection fun getUser(select: String? = null): User { println(select); return user() }
            @LuxoSelection fun listUsers(select: String? = null): List<User> { println(select); return listOf(user()) }
            @LuxoSelection fun subscribeUser(select: String? = null, onData: (User) -> Unit): () -> Unit {
                println(select); onData(user()); return {}
            }
            fun main() {
                val model = getUser()
                println(model.address.value().city.value())
                println(listUsers().map { it.id.value() }.single())
                val cancel = subscribeUser { println(it.id.value()) }
                subscribeUser { println(it.address.value().city.value()) }()
                cancel()
            }
        """.trimIndent(), listOf("address{city}", "Tokyo", "id", "42", "id", "42", "address{city}", "Tokyo"))
    }

    @Test
    fun `vararg escapes and unapplied API references fail compilation`() {
        val declarations = """
            import com.luxo.client.*
            class User(@get:LuxoSelectionField("id") val id: Long)
            @LuxoSelection fun getUser(select: String? = null) = User(1)
        """.trimIndent()
        verify(declarations + "\nfun main() { val user = getUser(); println(user.id); println(listOf(user)) }", null)
        verify(declarations + "\nfun main() { val callable = ::getUser; println(callable(null).id) }", null)
    }

    @Test
    fun `real generated client sends per-call selection through typed binary requests`() {
        val schema = LuxoSchema(
            models = mapOf("User" to LuxoModel("User", fields = listOf(
                LuxoField(1, "id", "Int"), LuxoField(2, "name", "String")))),
            apis = mapOf("getUser" to LuxoAPI(1, "getUser", "user", returnType = "User")),
        )
        val sources = mapOf("Types.kt" to LuxoCodegen.genTypes(schema, "probe"),
            "Schema.kt" to LuxoCodegen.genSchemaMap(schema, "probe"),
            "LuxoClient.kt" to LuxoCodegen.genClient(schema, "probe"))
        verify("""
            import com.luxo.client.*
            import kotlinx.coroutines.runBlocking
            import kotlinx.serialization.json.Json
            import probe.LuxoClient
            class Wire : Transport {
                private var schema = emptyMap<String, APISchemaEntry>()
                override suspend fun call(api: String, params: Map<String, Any?>): Any = error("dynamic path")
                override suspend fun call(api: String, request: GeneratedRequest): Any {
                    val metadata = schema.getValue(api)
                    val bytes = LuxoBinaryProtocol.encodeRequest(metadata, request)
                    check(bytes.contentEquals(LuxoBinaryProtocol.encodeRequest(metadata, mapOf("\${'$'}select" to request.selection))))
                    println(request.selection)
                    return Json.parseToJsonElement("{\"id\":42,\"name\":\"Ada\"}")
                }
                override fun setSchema(schema: Map<String, APISchemaEntry>) { this.schema = schema }
                override fun setMode(mode: TransportMode) {}
                override fun setToken(token: String) {}
            }
            fun main() = runBlocking {
                val client = LuxoClient(Wire())
                val first = client.getUser()
                println(first.id.value())
                val second = client.getUser()
                println(second.name.value())
            }
        """.trimIndent(), listOf("id", "42", "name", "Ada"), sources)
    }

    private fun verify(sourceText: String, expected: List<String>?, extras: Map<String, String> = emptyMap()) {
        val directory = Files.createTempDirectory(File(".tmp").apply { mkdirs() }.toPath(), "selection-plugin-").toFile()
        try {
            val source = File(directory, "Probe.kt").apply { writeText(sourceText) }
            val extraFiles = extras.map { (name, content) -> File(directory, name).apply { writeText(content) } }
            var classpath = System.getProperty("luxo.testRuntimeClasspath")
                .split(File.pathSeparator).filter { File(it).exists() }.joinToString(File.pathSeparator)
            if (extraFiles.isNotEmpty()) {
                val dependency = compile(directory, classpath, extraFiles, "dependency", plugin = false)
                assertEquals(0, dependency.first, dependency.second)
                classpath = File(directory, "dependency").path + File.pathSeparator + classpath
            }
            val compiled = compile(directory, classpath, listOf(source), "classes", plugin = true)
            if (expected == null) {
                assertNotEquals(0, compiled.first, compiled.second)
                assertContains(compiled.second, "cannot infer Luxo selection", ignoreCase = true)
                return
            }
            assertEquals(0, compiled.first, compiled.second)
            val result = run(directory, listOf("-cp", File(directory, "classes").path + File.pathSeparator + classpath, "ProbeKt"))
            assertEquals(0, result.first, result.second)
            assertEquals(expected, result.second.trim().lines())
        } finally {
            directory.deleteRecursively()
        }
    }

    private fun compile(directory: File, classpath: String, sources: List<File>, output: String, plugin: Boolean): Pair<Int, String> {
        val arguments = listOf("-cp", classpath, "org.jetbrains.kotlin.cli.jvm.K2JVMCompiler",
            "-no-stdlib", "-no-reflect", "-classpath", classpath, "-Xverify-ir=error",
            "-Xfriend-paths=${File("build/classes/kotlin/main").absolutePath}", "-d", File(directory, output).path)
        val plugins = if (plugin) listOf("-Xplugin=${System.getProperty("luxo.testPluginJar")}") else emptyList()
        return run(directory, arguments + plugins + sources.map { it.path })
    }

    private fun run(directory: File, arguments: List<String>): Pair<Int, String> {
        val log = File(directory, "process.log")
        val java = File(System.getProperty("java.home"), "bin/java").path
        val process = ProcessBuilder(listOf(java, "-Djava.io.tmpdir=${directory.absolutePath}") + arguments)
            .redirectErrorStream(true).redirectOutput(log).start()
        try {
            check(process.waitFor(90, TimeUnit.SECONDS)) { "Compiler test timed out" }
            return process.exitValue() to log.readText()
        } finally {
            if (process.isAlive) { process.destroyForcibly(); process.waitFor() }
        }
    }
}
