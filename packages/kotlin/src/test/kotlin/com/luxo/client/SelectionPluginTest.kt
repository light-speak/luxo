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
    fun `compiler hoists distinct plans and repeated calls reuse identity`() {
        verify("""
            import com.luxo.client.*
            class User(@get:LuxoSelectionField("id") val id: Long)
            val seen = mutableListOf<PreparedSelection>()
            @LuxoSelection fun getUser(select: String? = null,
                `${'$'}selectionPlan`: PreparedSelection? = null): User {
                val plan = checkNotNull(`${'$'}selectionPlan`)
                check(plan.text == select)
                seen += plan
                return User(42)
            }
            @LuxoSelection fun subscribeUser(select: String? = null,
                `${'$'}selectionPlan`: PreparedSelection? = null, onData: (User) -> Unit): () -> Unit {
                val plan = checkNotNull(`${'$'}selectionPlan`)
                check(plan.text == select)
                seen += plan
                onData(User(42))
                return {}
            }
            fun watch() { subscribeUser { check(it.id == 42L) }() }
            fun read(): Long { val user = getUser(); return user.id }
            fun main() {
                check(read() == 42L)
                check(read() == 42L)
                val other = getUser()
                check(other.id == 42L)
                check(seen[0] === seen[1])
                check(seen[0] !== seen[2])
                watch()
                watch()
                check(seen[3] === seen[4])
                check(seen[3] !== seen[0])
                println("plans reused and isolated")
            }
        """.trimIndent(), listOf("plans reused and isolated"))
    }

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
    fun `real generated client sends per-call selection over HTTP Binary`() {
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
            import com.sun.net.httpserver.HttpServer
            import java.net.InetSocketAddress
            import okhttp3.OkHttpClient
            import probe.LuxoClient
            var expectedPlan: PreparedSelection? = null
            class Wire(private val real: Transport) : Transport by real {
                private var schema = emptyMap<String, APISchemaEntry>()
                override suspend fun call(api: String, params: Map<String, Any?>): Any = error("dynamic path")
                override suspend fun call(api: String, request: GeneratedRequest): Any {
                    if (expectedPlan != null) check(request.selectionPlan === expectedPlan)
                    else check(request.selectionPlan != null)
                    check(request.toJSONParameters() == mapOf("\${'$'}select" to request.selection))
                    val metadata = schema.getValue(api)
                    val bytes = LuxoBinaryProtocol.encodeRequest(metadata, request)
                    check(bytes.contentEquals(LuxoBinaryProtocol.encodeRequest(metadata, mapOf("\${'$'}select" to request.selection))))
                    println(request.selection)
                    return real.call(api, request)
                }
                override fun setSchema(schema: Map<String, APISchemaEntry>) {
                    this.schema = schema; real.setSchema(schema)
                }
            }
            fun main() = runBlocking {
                val server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
                server.createContext("/luvia") { exchange ->
                    try {
                        val bytes = exchange.requestBody.use { it.readBytes() }
                        check(bytes.size == 5 && bytes[0] == 1.toByte() && bytes[1] == 2.toByte() &&
                            bytes[2] == 1.toByte() && bytes[4] == 0.toByte())
                        val mask = bytes[3].toInt()
                        check(mask == 1 || mask == 2)
                        val encoder = LuxoEncoder()
                        encoder.writeVarint(if (mask == 2) 3L else 0L)
                        if (mask == 1) encoder.writeFieldInt(1, 42L)
                        else encoder.writeFieldString(2, "Ada")
                        encoder.writeEnd()
                        val body = encoder.bytes()
                        exchange.sendResponseHeaders(200, body.size.toLong())
                        exchange.responseBody.use { it.write(body) }
                    } catch (error: Exception) {
                        exchange.sendResponseHeaders(500, -1)
                    } finally { exchange.close() }
                }
                server.start()
                val http = OkHttpClient()
                try {
                    val endpoint = "http://127.0.0.1:" + server.address.port + "/luvia"
                    val client = LuxoClient(Wire(OkHttpTransport(endpoint, client = http, mode = TransportMode.BINARY)))
                    val first = client.getUser()
                    println(first.id.value())
                    val second = client.getUser()
                    println(second.name.value())
                    expectedPlan = PreparedSelection("id")
                    val planned = client.getUser(`${'$'}selectionPlan` = expectedPlan)
                    println(planned.id.value())
                    val overridden = client.getUser(select = "name", `${'$'}selectionPlan` = expectedPlan)
                    println(overridden.name.value())
                } finally {
                    http.dispatcher.executorService.shutdownNow()
                    http.connectionPool.evictAll()
                    server.stop(0)
                }
            }
        """.trimIndent(), listOf("id", "42", "name", "Ada", "id", "42", "name", "Ada"), sources)
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
