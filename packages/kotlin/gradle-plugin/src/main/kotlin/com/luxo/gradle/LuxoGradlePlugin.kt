package com.luxo.gradle

import org.gradle.api.Project
import org.gradle.api.provider.Provider
import org.jetbrains.kotlin.gradle.plugin.*

/**
 * Luxo Gradle Plugin — configures the Kotlin compiler plugin for
 * compile-time field selection analysis.
 *
 * Usage in build.gradle.kts:
 *   plugins {
 *       id("com.luxo.select")
 *   }
 */
class LuxoGradlePlugin : KotlinCompilerPluginSupportPlugin {

    override fun apply(target: Project) {
        // Plugin applied — compiler plugin will be auto-registered
    }

    override fun isApplicable(kotlinCompilation: KotlinCompilation<*>): Boolean {
        return kotlinCompilation.target.project.plugins.hasPlugin(LuxoGradlePlugin::class.java)
    }

    override fun getCompilerPluginId(): String = "com.luxo.select"

    override fun getPluginArtifact(): SubpluginArtifact = SubpluginArtifact(
        groupId = "com.luxo",
        artifactId = "luxo-compiler-plugin",
        version = "0.1.0",
    )

    override fun applyToCompilation(kotlinCompilation: KotlinCompilation<*>): Provider<List<SubpluginOption>> {
        return kotlinCompilation.target.project.provider { emptyList() }
    }
}
