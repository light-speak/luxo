package com.luxo.gradle

import java.util.Properties
import org.gradle.api.Project
import org.gradle.api.artifacts.Configuration
import org.gradle.api.provider.Provider
import org.jetbrains.kotlin.gradle.plugin.KotlinCompilation
import org.jetbrains.kotlin.gradle.plugin.KotlinCompilerPluginSupportPlugin
import org.jetbrains.kotlin.gradle.plugin.SubpluginArtifact
import org.jetbrains.kotlin.gradle.plugin.SubpluginOption

/** Registers Luxo code generation and compiler-AST field analysis. */
class LuxoGradlePlugin : KotlinCompilerPluginSupportPlugin {
    override fun getCompilerPluginId() = "com.luxo.select"
    override fun getPluginArtifact() = SubpluginArtifact("com.luxo", "luxo-client", ToolingVersions.luxo)
    override fun isApplicable(kotlinCompilation: KotlinCompilation<*>) = true
    override fun applyToCompilation(kotlinCompilation: KotlinCompilation<*>): Provider<List<SubpluginOption>> =
        kotlinCompilation.target.project.provider { emptyList() }

    override fun apply(target: Project) {
        val extension = target.extensions.create("luxo", LuxoExtension::class.java)
        extension.applyConventions(target)
        val codegenClasspath = target.toolingClasspath("luxoCodegenRuntimeClasspath")
        val analyzerClasspath = target.toolingClasspath("luxoAnalyzerRuntimeClasspath", includeCompiler = true)

        val generate = target.tasks.register("luxoGenerate", LuxoGenerateTask::class.java) {
            it.configureFrom(extension, codegenClasspath)
        }
        target.tasks.register("luxoAnalyze", LuxoAnalyzeTask::class.java) {
            it.configureFrom(extension, analyzerClasspath)
            it.mustRunAfter(generate)
        }

    }
}

private fun Project.toolingClasspath(name: String, includeCompiler: Boolean = false): Configuration {
    val classpath = configurations.create(name) {
        it.isCanBeConsumed = false
        it.isCanBeResolved = true
        it.description = "Isolated runtime for Luxo build tooling"
    }
    dependencies.add(name, "com.luxo:luxo-client:${ToolingVersions.luxo}")
    if (includeCompiler) {
        dependencies.add(name, "org.jetbrains.kotlin:kotlin-compiler-embeddable:${ToolingVersions.kotlin}")
    }
    return classpath
}

/** Build-time versions come from the SDK build, never the host Gradle Kotlin runtime. */
private object ToolingVersions {
    private val properties = Properties().apply {
        checkNotNull(LuxoGradlePlugin::class.java.getResourceAsStream("tooling.properties")) {
            "Luxo plugin tooling versions are missing"
        }.use { load(it) }
    }
    val luxo: String = checkNotNull(properties.getProperty("luxoVersion"))
    val kotlin: String = checkNotNull(properties.getProperty("kotlinVersion"))
}
