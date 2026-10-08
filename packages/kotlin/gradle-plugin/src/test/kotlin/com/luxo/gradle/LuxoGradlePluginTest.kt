package com.luxo.gradle

import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertTrue
import kotlin.test.assertFalse
import org.jetbrains.kotlin.gradle.plugin.KotlinCompilerPluginSupportPlugin
import org.gradle.testfixtures.ProjectBuilder

class LuxoGradlePluginTest {
    @Test
    fun `registers production codegen and analyzer tasks`() {
        val project = ProjectBuilder.builder()
            .withProjectDir(File("build/plugin-test"))
            .build()
        val compileKotlin = project.tasks.register("compileKotlin")

        project.pluginManager.apply(LuxoGradlePlugin::class.java)

        val extension = project.extensions.getByType(LuxoExtension::class.java)
        assertEquals("http://localhost:4000/luvia", extension.endpoint.get())
        assertEquals("com.luxo.generated", extension.packageName.get())
        assertIs<LuxoGenerateTask>(project.tasks.getByName("luxoGenerate"))
        assertIs<LuxoAnalyzeTask>(project.tasks.getByName("luxoAnalyze"))
        assertIs<KotlinCompilerPluginSupportPlugin>(project.plugins.getPlugin(LuxoGradlePlugin::class.java))
        assertFalse(
            compileKotlin.get().taskDependencies.getDependencies(compileKotlin.get())
                .any { it.name == "luxoAnalyze" },
        )
        assertEquals("com.luxo.select", project.plugins.getPlugin(LuxoGradlePlugin::class.java).getCompilerPluginId())

        val catalog = File("../gradle/libs.versions.toml").readText()
        val compilerVersion = checkNotNull(Regex("""kotlin = "([^"]+)"""").find(catalog)).groupValues[1]
        val analyzer = project.configurations.getByName("luxoAnalyzerRuntimeClasspath").dependencies
        val compiler = analyzer.single { it.name == "kotlin-compiler-embeddable" }
        assertEquals(compilerVersion, compiler.version)
        val codegen = project.configurations.getByName("luxoCodegenRuntimeClasspath").dependencies
        assertTrue(codegen.none { it.group == "org.jetbrains.kotlin" })
        assertEquals(codegen.single().version, analyzer.single { it.name == "luxo-client" }.version)
    }
}
