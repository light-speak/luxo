package com.luxo.gradle

import java.util.Properties
import org.gradle.api.Plugin
import org.gradle.api.Project
import org.gradle.api.artifacts.Configuration

/** Registers Luxo code generation and compiler-AST field analysis. */
class LuxoGradlePlugin : Plugin<Project> {
    override fun apply(target: Project) {
        val extension = target.extensions.create("luxo", LuxoExtension::class.java)
        extension.applyConventions(target)
        val codegenClasspath = target.toolingClasspath("luxoCodegenRuntimeClasspath")
        val analyzerClasspath = target.toolingClasspath("luxoAnalyzerRuntimeClasspath", includeCompiler = true)

        val generate = target.tasks.register("luxoGenerate", LuxoGenerateTask::class.java) {
            it.configureFrom(extension, codegenClasspath)
        }
        val analyze = target.tasks.register("luxoAnalyze", LuxoAnalyzeTask::class.java) {
            it.configureFrom(extension, analyzerClasspath)
            it.mustRunAfter(generate)
        }

        target.tasks.configureEach {
            if (it.name.startsWith("compile") && it.name.endsWith("Kotlin")) {
                it.dependsOn(analyze)
            }
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
