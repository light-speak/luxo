package com.luxo.gradle

import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertTrue
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
        assertTrue(
            compileKotlin.get().taskDependencies.getDependencies(compileKotlin.get())
                .any { it.name == "luxoAnalyze" },
        )
    }
}
