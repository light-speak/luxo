package com.luxo.gradle

import org.gradle.api.Project
import org.gradle.api.file.DirectoryProperty
import org.gradle.api.file.RegularFileProperty
import org.gradle.api.provider.Property

/** User-facing Luxo build configuration. */
abstract class LuxoExtension {
    abstract val endpoint: Property<String>
    abstract val introspectionKey: Property<String>
    abstract val packageName: Property<String>
    abstract val outputDirectory: DirectoryProperty
    abstract val schemaFile: RegularFileProperty

    internal fun applyConventions(project: Project) {
        val properties = project.providers
        endpoint.convention(properties.gradleProperty("luxo.endpoint").orElse("http://localhost:4000/luvia"))
        introspectionKey.convention(
            properties.environmentVariable("LUXO_INTROSPECTION_KEY")
                .orElse(properties.gradleProperty("luxo.key"))
                .orElse(""),
        )
        packageName.convention(properties.gradleProperty("luxo.package").orElse("com.luxo.generated"))
        outputDirectory.convention(project.layout.projectDirectory.dir("src/main/kotlin/com/luxo/generated"))
        schemaFile.convention(project.layout.projectDirectory.file("src/main/luxo/luxo.schema.json"))

        properties.gradleProperty("luxo.outDir").orNull?.let { outputDirectory.set(project.file(it)) }
        properties.gradleProperty("luxo.schemaFile").orNull?.let { schemaFile.set(project.file(it)) }
    }
}
