package com.luxo.gradle

import java.io.File
import java.nio.file.Files
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import org.gradle.testkit.runner.GradleRunner

class SelectionCompilationTest {
    @Test
    fun `normal Gradle compilation injects calls and updates after source changes`() {
        val root = Files.createTempDirectory(File(".tmp").apply { mkdirs() }.toPath(), "selection-consumer-").toFile()
        try {
            prepare(root)
            val source = File(root, "src/main/kotlin/Main.kt").apply { parentFile.mkdirs() }
            val original = """
                import com.luxo.client.LuxoSelection
                import com.luxo.client.LuxoSelectionField
                class User(@get:LuxoSelectionField("id") val id: Long,
                    @get:LuxoSelectionField("name") val name: String)
                @LuxoSelection fun getUser(select: String? = null): User {
                    println("selection=" + select); return User(42, "Ada")
                }
                fun main() { val user = getUser(); println(user.id) }
            """.trimIndent()
            source.writeText(original)
            assertContains(run(root), "selection=id")
            assertEquals(original, source.readText())
            val changed = original.replace("println(user.id)", "println(user.name)")
            source.writeText(changed)
            assertContains(run(root), "selection=name")
            assertEquals(changed, source.readText())
        } finally {
            root.deleteRecursively()
        }
    }

    private fun prepare(root: File) {
        val artifact = LuxoGradlePlugin().getPluginArtifact()
        val sdk = File(System.getProperty("luxo.testPluginJar"))
        val destination = File(root, "repo/com/luxo/luxo-client/${artifact.version}").apply { mkdirs() }
        sdk.copyTo(File(destination, "luxo-client-${artifact.version}.jar"))
        File(destination, "luxo-client-${artifact.version}.pom").writeText("""
            <project><modelVersion>4.0.0</modelVersion><groupId>com.luxo</groupId>
            <artifactId>luxo-client</artifactId><version>${artifact.version}</version></project>
        """.trimIndent())
        publishPlugin(root, artifact.version!!)
        val version = Regex("""kotlin = "([^"]+)"""").find(File("../gradle/libs.versions.toml").readText())!!.groupValues[1]
        File(root, "settings.gradle.kts").writeText("""
            pluginManagement { repositories { maven { url = uri("repo") }; gradlePluginPortal(); mavenCentral() } }
            rootProject.name = "selection-consumer"
        """.trimIndent())
        File(root, "gradle.properties").writeText(
            "org.gradle.jvmargs=-Xmx512m -Djava.io.tmpdir=${root.absolutePath}\norg.gradle.daemon.idletimeout=1000\n")
        File(root, "build.gradle.kts").writeText("""
            plugins { kotlin("jvm") version "$version"; id("com.luxo.select") version "${artifact.version}"; application }
            repositories { maven { url = uri("repo") }; mavenCentral() }
            dependencies { implementation(files("${sdk.invariantSeparatorsPath}")) }
            application { mainClass.set("MainKt") }
        """.trimIndent())
    }

    private fun publishPlugin(root: File, version: String) {
        val plugin = File(root, "repo/com/luxo/gradle-plugin/$version").apply { mkdirs() }
        File(System.getProperty("luxo.testGradlePluginJar")).copyTo(File(plugin, "gradle-plugin-$version.jar"))
        File(plugin, "gradle-plugin-$version.pom").writeText("""
            <project><modelVersion>4.0.0</modelVersion><groupId>com.luxo</groupId>
            <artifactId>gradle-plugin</artifactId><version>$version</version></project>
        """.trimIndent())
        val marker = File(root, "repo/com/luxo/select/com.luxo.select.gradle.plugin/$version").apply { mkdirs() }
        File(marker, "com.luxo.select.gradle.plugin-$version.pom").writeText("""
            <project><modelVersion>4.0.0</modelVersion><groupId>com.luxo.select</groupId>
            <artifactId>com.luxo.select.gradle.plugin</artifactId><version>$version</version><packaging>pom</packaging>
            <dependencies><dependency><groupId>com.luxo</groupId><artifactId>gradle-plugin</artifactId>
            <version>$version</version></dependency></dependencies></project>
        """.trimIndent())
    }

    private fun run(root: File): String = GradleRunner.create().withProjectDir(root)
        .withArguments("run", "--offline", "--console=plain", "--no-watch-fs",
            "-Djava.io.tmpdir=${root.absolutePath}",
            "--gradle-user-home", System.getProperty("luxo.testGradleUserHome"),
            "-Pkotlin.compiler.execution.strategy=in-process").build().output
}
