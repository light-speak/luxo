package com.luxo.gradle

import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertIs
import kotlin.test.assertTrue
import kotlin.test.assertFalse
import org.jetbrains.kotlin.gradle.plugin.KotlinCompilerPluginSupportPlugin
import org.gradle.testfixtures.ProjectBuilder

class LuxoGradlePluginTest {
    @Test
    fun `does not register API-wide hint generation`() {
        val project = ProjectBuilder.builder()
            .withProjectDir(File("build/no-legacy-hints-test"))
            .build()
        project.pluginManager.apply(LuxoGradlePlugin::class.java)
        assertNull(project.tasks.findByName("luxoAnalyze"))
        assertNull(project.configurations.findByName("luxoAnalyzerRuntimeClasspath"))
    }

    @Test
    fun `registers production codegen and call-site compiler integration`() {
        val project = ProjectBuilder.builder()
            .withProjectDir(File("build/plugin-test"))
            .build()
        val compileKotlin = project.tasks.register("compileKotlin")

        project.pluginManager.apply(LuxoGradlePlugin::class.java)

        val extension = project.extensions.getByType(LuxoExtension::class.java)
        assertEquals("http://localhost:4000/luvia", extension.endpoint.get())
        assertEquals("com.luxo.generated", extension.packageName.get())
        assertIs<LuxoGenerateTask>(project.tasks.getByName("luxoGenerate"))
        assertIs<KotlinCompilerPluginSupportPlugin>(project.plugins.getPlugin(LuxoGradlePlugin::class.java))
        assertFalse(
            compileKotlin.get().taskDependencies.getDependencies(compileKotlin.get())
                .any { it.name == "luxoAnalyze" },
        )
        assertEquals("com.luxo.select", project.plugins.getPlugin(LuxoGradlePlugin::class.java).getCompilerPluginId())

        val codegen = project.configurations.getByName("luxoCodegenRuntimeClasspath").dependencies
        assertTrue(codegen.none { it.group == "org.jetbrains.kotlin" })
        assertEquals(codegen.single().version, project.plugins.getPlugin(LuxoGradlePlugin::class.java).getPluginArtifact().version)
    }
}
