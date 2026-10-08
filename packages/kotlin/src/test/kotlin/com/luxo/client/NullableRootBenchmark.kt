package com.luxo.client

import com.sun.net.httpserver.HttpServer
import java.io.File
import java.net.InetSocketAddress
import java.nio.file.Files
import java.util.concurrent.TimeUnit
import kotlinx.serialization.json.*

/** Compile the real generator output; fixture setup and compilation never enter measured operations. */
object NullableRootBenchmark {
    private val declarations = listOf(
        Triple("integer", "Int", false), Triple("boolean", "Boolean", false),
        Triple("text", "String", false), Triple("bytes", "Bytes", false),
        Triple("integers", "Int", true), Triple("object", "Snapshot", false),
        Triple("objects", "Snapshot", true),
    )

    @JvmStatic
    fun main(args: Array<String>) {
        val mode = args.firstOrNull() ?: "measure"
        require(mode in listOf("verify", "measure", "control"))
        require(args.size == if (mode == "control") 2 else 1)
        val root = File(".tmp").canonicalFile.apply { mkdirs() }
        val directory = Files.createTempDirectory(root.toPath(), "nullable-root-benchmark-").toFile()
        try {
            generate(directory)
            writeProbe(directory)
            val classes = File(directory, "classes")
            compile(directory, classes, directory.listFiles()!!.filter { it.extension == "kt" })
            if (mode == "control") control(directory, classes, File(args[1]))
            else repeat(if (mode == "verify") 1 else 3) { index ->
                println("nullable $mode process ${index + 1}")
                execute(directory, classes.path, if (mode == "verify") "--verify" else "--measure")
            }
        } finally {
            check(!Files.isSymbolicLink(directory.toPath()) && directory.canonicalFile.parentFile == root)
            check(directory.deleteRecursively()) { "Could not clean owned benchmark directory" }
        }
    }

    private fun generate(directory: File) {
        val bytes = schema().toString().toByteArray()
        val server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
        server.createContext("/") { exchange ->
            exchange.responseHeaders.add("Content-Type", "application/json")
            exchange.sendResponseHeaders(200, bytes.size.toLong())
            exchange.responseBody.use { it.write(bytes) }
        }
        server.start()
        try {
            LuxoCodegen.generate("http://127.0.0.1:${server.address.port}", "benchmark",
                directory.path, "com.luxo.nullablefixture")
        } finally {
            server.stop(0)
        }
    }

    private fun schema(): JsonObject = buildJsonObject {
        put("models", JsonObject(emptyMap()))
        putJsonObject("types") {
            putJsonObject("Snapshot") {
                put("name", "Snapshot"); put("usage", "output")
                putJsonArray("fields") {
                    add(buildJsonObject { put("id", 1); put("name", "id"); put("type", "Int") })
                    add(buildJsonObject { put("id", 2); put("name", "label"); put("type", "String") })
                }
            }
        }
        putJsonObject("apis") {
            for ((index, item) in declarations.withIndex()) {
                val (name, type, list) = item
                for (nullable in listOf(false, true)) {
                    val api = name + if (nullable) "Nullable" else "Plain"
                    putJsonObject(api) {
                        put("id", index * 2 + if (nullable) 2 else 1)
                        put("name", api); put("module", "benchmark"); put("stream", true)
                        put("returnType", type); put("returnList", list); put("returnNullable", nullable)
                    }
                }
            }
        }
    }

    private fun writeProbe(directory: File) {
        val registrations = buildString {
            for ((name) in declarations) for (nullable in listOf(false, true)) {
                val method = name.replaceFirstChar { it.uppercase() } + if (nullable) "Nullable" else "Plain"
                appendLine("        client.subscribe$method { }")
            }
        }
        val source = File("benchmark/NullableRootProbe.kt.txt").readText()
        val marker = "// __REGISTER_DECODERS__"
        check(source.indexOf(marker) >= 0 && source.indexOf(marker) == source.lastIndexOf(marker))
        File(directory, "NullableRootProbe.kt").writeText(source.replace(marker, registrations))
    }

    private fun classpath(): String {
        val raw = System.getProperty("luxo.testRuntimeClasspath") ?: System.getProperty("java.class.path")
        val emptyOutputs = listOf("classes/java/test", "classes/java/main", "resources/test", "resources/main")
            .map { File("build/$it").absolutePath }.toSet()
        return raw.split(File.pathSeparator).filter { path ->
            if (File(path).exists()) true
            else { check(path in emptyOutputs) { "Missing classpath dependency: $path" }; false }
        }.joinToString(File.pathSeparator)
    }

    private fun compile(directory: File, output: File, sources: List<File>) {
        val friends = listOf("main", "test").joinToString(",") { File("build/classes/kotlin/$it").absolutePath }
        run(directory, listOf("-Xmx512m", "-cp", classpath(), "org.jetbrains.kotlin.cli.jvm.K2JVMCompiler",
            "-no-stdlib", "-no-reflect", "-jvm-target", "1.8", "-Werror", "-classpath", classpath(),
            "-Xfriend-paths=$friends", "-d", output.path) + sources.map { it.path })
    }

    private fun control(directory: File, classes: File, baseline: File) {
        val source = baseline.readText()
        check(source.contains("class LuxoDecoder(private val buf: ByteArray)") && !source.contains("fun isNullRoot("))
        val before = File(directory, "before")
        compile(directory, before, listOf(baseline))
        repeat(3) { round ->
            for (variant in if (round % 2 == 0) listOf("before", "after") else listOf("after", "before")) {
                println("nullable control $variant process ${round + 1}")
                val selected = if (variant == "before") before.path + File.pathSeparator + classes.path else classes.path
                execute(directory, selected, "--plain-only")
            }
        }
    }

    private fun execute(directory: File, classes: String, mode: String) = run(directory,
        listOf("-Xms512m", "-Xmx512m", "-cp", classes + File.pathSeparator + classpath(),
            "com.luxo.nullablefixture.NullableRootProbe", mode))

    private fun run(directory: File, arguments: List<String>) {
        val java = File(System.getProperty("java.home"), "bin/java").path
        val process = ProcessBuilder(listOf(java, "-Djava.io.tmpdir=${directory.absolutePath}") + arguments).inheritIO().start()
        try {
            check(process.waitFor(120, TimeUnit.SECONDS)) { "Benchmark child exceeded two-minute budget" }
            check(process.exitValue() == 0) { "Benchmark child failed: ${process.exitValue()}" }
        } finally {
            if (process.isAlive) {
                process.destroy()
                if (!process.waitFor(2, TimeUnit.SECONDS)) process.destroyForcibly().waitFor()
            }
        }
    }
}
