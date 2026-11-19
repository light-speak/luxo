package com.luxo.gradle

import java.io.File
import java.nio.file.Files
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFalse

class BuildEnvironmentTest {
    private enum class Outcome(val suffix: String) { SUCCESS(""), UP_TO_DATE(" UP-TO-DATE"), FROM_CACHE(" FROM-CACHE") }

    @Test
    fun `owned network tests execute for every enabled fixture while ordinary tests stay incremental`() {
        withProject { project ->
            val environment = environment()
            assertInvocation(project, environment, Outcome.SUCCESS, 1)
            assertInvocation(project, environment, Outcome.UP_TO_DATE, 1)
            environment["LUXO_WS_RENEWAL_URL"] = "http://127.0.0.1:1"
            environment["LUXO_WS_RENEWAL_STREAM"] = "1"
            assertInvocation(project, environment, Outcome.SUCCESS, 2)
            assertInvocation(project, environment, Outcome.SUCCESS, 3)
            environment["LUXO_WS_RENEWAL_URL"] = "http://127.0.0.1:2"
            environment["LUXO_WS_RENEWAL_STREAM"] = "0"
            assertInvocation(project, environment, Outcome.SUCCESS, 4)
            environment.remove("LUXO_WS_RENEWAL_URL"); environment.remove("LUXO_WS_RENEWAL_STREAM")
            assertInvocation(project, environment, Outcome.FROM_CACHE, 4)
            assertInvocation(project, environment, Outcome.UP_TO_DATE, 4)
        }
    }

    @Test
    fun `shared protocol corpus changes invalidate test execution and cached results`() {
        withProject { project ->
            val environment = environment()
            val corpus = File(project, "../../pkg/lux/codec/testdata/protocol-v1.json")
            val original = corpus.readBytes()
            val changed = "{\"audit\":true}".toByteArray()
            assertFalse(original.contentEquals(changed))
            assertInvocation(project, environment, Outcome.SUCCESS, 1)
            assertInvocation(project, environment, Outcome.UP_TO_DATE, 1)
            corpus.writeBytes(changed)
            assertInvocation(project, environment, Outcome.SUCCESS, 2)
            assertInvocation(project, environment, Outcome.UP_TO_DATE, 2)
            corpus.writeBytes(original)
            assertInvocation(project, environment, Outcome.FROM_CACHE, 2)
            corpus.writeText("{")
            val failed = run(project, environment, expectedExit = 1)
            assertContains(failed.lineSequence().toList(), "> Task :test FAILED", failed)
            assertContains(File(project, "build/test-results/test/TEST-FixtureInvocationTest.xml").readText(),
                "JsonDecodingException")
            assertEquals(2, File(project, "invocations.txt").readLines().size)
        }
    }

    private fun environment() = System.getenv().toMutableMap().apply {
        remove("LUXO_WS_RENEWAL_URL"); remove("LUXO_WS_RENEWAL_STREAM")
    }

    private fun withProject(block: (File) -> Unit) {
        val source = File(System.getProperty("luxo.testRootProjectDir")).canonicalFile
        val temporary = File(source, ".tmp").apply { mkdirs() }.canonicalFile
        check(temporary.parentFile == source)
        val scratch = Files.createTempDirectory(temporary.toPath(), "test-environment-").toFile()
        try {
            block(prepare(source, scratch))
        } finally {
            check(scratch.canonicalFile.parentFile == temporary)
            Files.walk(scratch.toPath()).use { paths ->
                paths.sorted(Comparator.reverseOrder()).forEach { Files.delete(it) }
            }
        }
    }

    private fun prepare(source: File, scratch: File): File {
        val project = File(scratch, "packages/kotlin").apply { mkdirs() }
        File(source, "build.gradle.kts").copyTo(File(project, "build.gradle.kts"))
        File(project, "settings.gradle.kts").writeText(File(source, "settings.gradle.kts").readText()
            .replace("include(\"gradle-plugin\")", "") + "\nbuildCache { local { directory = file(\".tmp/build-cache\") } }\n")
        val catalog = File(project, "gradle/libs.versions.toml").apply { parentFile.mkdirs() }
        File(source, "gradle/libs.versions.toml").copyTo(catalog)
        File(source, "../../VERSION").copyTo(File(scratch, "VERSION"))
        val corpus = File(scratch, "pkg/lux/codec/testdata/protocol-v1.json").apply { parentFile.mkdirs() }
        File(source, "../../pkg/lux/codec/testdata/protocol-v1.json").copyTo(corpus)
        File(project, "gradle.properties").writeText(
            "org.gradle.jvmargs=-Xmx512m -Djava.io.tmpdir=${scratch.absolutePath}\norg.gradle.daemon.idletimeout=1000\n")
        File(project, "src/test/kotlin/FixtureInvocationTest.kt").apply { parentFile.mkdirs() }.writeText("""
            import java.io.File
            import kotlin.test.Test
            import kotlinx.serialization.json.Json
            class FixtureInvocationTest {
                @Test fun recordsInvocation() {
                    Json.parseToJsonElement(File("../../pkg/lux/codec/testdata/protocol-v1.json").readText())
                    File("invocations.txt").appendText("ran" + System.lineSeparator())
                }
            }
        """.trimIndent())
        return project
    }

    private fun assertInvocation(project: File, environment: Map<String, String>, outcome: Outcome, count: Int) {
        val result = run(project, environment)
        assertContains(result.lineSequence().toList(), "> Task :test" + outcome.suffix, result)
        assertEquals(count, File(project, "invocations.txt").readLines().size, result)
    }

    private fun run(project: File, environment: Map<String, String>, expectedExit: Int = 0): String {
        val source = File(System.getProperty("luxo.testRootProjectDir"))
        val windows = System.getProperty("os.name").startsWith("Windows")
        val wrapper = File(source, if (windows) "gradlew.bat" else "gradlew").absolutePath
        val launcher = if (windows) listOf("cmd", "/c", wrapper) else listOf(wrapper)
        val arguments = listOf("test", "--offline", "--no-daemon", "--build-cache", "--console=plain", "--no-watch-fs",
            "--gradle-user-home", System.getProperty("luxo.testGradleUserHome"),
            "-Pkotlin.compiler.execution.strategy=in-process")
        val log = File(project, ".tmp/invocation.log").apply { parentFile.mkdirs() }
        val builder = ProcessBuilder(launcher + arguments).directory(project).redirectErrorStream(true).redirectOutput(log)
        builder.environment().apply { clear(); putAll(environment) }
        val process = builder.start()
        try {
            check(process.waitFor(120, TimeUnit.SECONDS)) { log.readText() }
            assertEquals(expectedExit, process.exitValue(), log.readText())
            return log.readText()
        } finally {
            if (process.isAlive) {
                process.descendants().forEach { it.destroyForcibly() }
                process.destroyForcibly().waitFor(5, TimeUnit.SECONDS)
            }
        }
    }
}
