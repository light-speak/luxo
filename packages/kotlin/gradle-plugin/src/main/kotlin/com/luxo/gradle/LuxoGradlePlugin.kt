package com.luxo.gradle

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
    dependencies.add(name, "com.luxo:luxo-client:$LUXO_VERSION")
    if (includeCompiler) {
        dependencies.add(name, "org.jetbrains.kotlin:kotlin-compiler-embeddable:$KOTLIN_COMPILER_VERSION")
    }
    return classpath
}

private const val LUXO_VERSION = "0.1.0"
private const val KOTLIN_COMPILER_VERSION = "2.4.10"
