package com.luxo.gradle

import org.gradle.api.DefaultTask
import org.gradle.api.artifacts.Configuration
import org.gradle.api.file.ConfigurableFileCollection
import org.gradle.api.file.DirectoryProperty
import org.gradle.api.file.RegularFileProperty
import org.gradle.api.provider.Property
import org.gradle.api.tasks.CacheableTask
import org.gradle.api.tasks.Classpath
import org.gradle.api.tasks.Input
import org.gradle.api.tasks.InputFile
import org.gradle.api.tasks.InputFiles
import org.gradle.api.tasks.Internal
import org.gradle.api.tasks.OutputDirectory
import org.gradle.api.tasks.OutputFile
import org.gradle.api.tasks.PathSensitive
import org.gradle.api.tasks.PathSensitivity
import org.gradle.api.tasks.TaskAction
import org.gradle.process.ExecOperations
import org.gradle.work.DisableCachingByDefault
import javax.inject.Inject

@DisableCachingByDefault(because = "Schema introspection reads a remote endpoint")
abstract class LuxoGenerateTask : DefaultTask() {
    @get:Classpath abstract val toolingClasspath: ConfigurableFileCollection
    @get:Input abstract val endpoint: Property<String>
    @get:Internal abstract val introspectionKey: Property<String>
    @get:Input abstract val packageName: Property<String>
    @get:OutputDirectory abstract val outputDirectory: DirectoryProperty
    @get:OutputFile abstract val schemaFile: RegularFileProperty
    @get:Inject abstract val execOperations: ExecOperations

    init {
        group = "luxo"
        description = "Generate a typed Kotlin client from Luxo introspection"
        outputs.upToDateWhen { false }
    }

    @TaskAction
    fun generate() {
        execOperations.javaexec {
            it.classpath(toolingClasspath)
            it.mainClass.set("com.luxo.client.LuxoCodegenCli")
            it.environment("LUXO_INTROSPECTION_KEY", introspectionKey.get())
            it.args(
                endpoint.get(),
                outputDirectory.get().asFile.path,
                packageName.get(),
                schemaFile.get().asFile.path,
            )
        }
    }

    internal fun configureFrom(extension: LuxoExtension, classpath: Configuration) {
        toolingClasspath.from(classpath)
        endpoint.set(extension.endpoint)
        introspectionKey.set(extension.introspectionKey)
        packageName.set(extension.packageName)
        outputDirectory.set(extension.outputDirectory)
        schemaFile.set(extension.schemaFile)
    }
}

@CacheableTask
abstract class LuxoAnalyzeTask : DefaultTask() {
    @get:Classpath abstract val toolingClasspath: ConfigurableFileCollection
    @get:Internal abstract val sourceDirectory: DirectoryProperty
    @get:InputFiles
    @get:PathSensitive(PathSensitivity.RELATIVE)
    abstract val sourceFiles: ConfigurableFileCollection
    @get:Input abstract val packageName: Property<String>
    @get:InputFile
    @get:PathSensitive(PathSensitivity.RELATIVE)
    abstract val schemaFile: RegularFileProperty
    @get:OutputFile abstract val hintsFile: RegularFileProperty
    @get:Inject abstract val execOperations: ExecOperations

    init {
        group = "luxo"
        description = "Generate SelectHints from Kotlin syntax trees and the Luxo schema"
    }

    @TaskAction
    fun analyze() {
        execOperations.javaexec {
            it.classpath(toolingClasspath)
            it.mainClass.set("com.luxo.client.SelectAnalyzerCli")
            it.args(
                sourceDirectory.get().asFile.path,
                hintsFile.get().asFile.path,
                packageName.get(),
                schemaFile.get().asFile.path,
            )
        }
    }

    internal fun configureFrom(extension: LuxoExtension, classpath: Configuration) {
        toolingClasspath.from(classpath)
        sourceDirectory.set(extension.sourceDirectory)
        sourceFiles.from(extension.sourceDirectory.map { directory ->
            directory.asFileTree.matching {
                it.include("**/*.kt")
                it.exclude("**/generated/**", "**/test/**")
            }
        })
        packageName.set(extension.packageName)
        schemaFile.set(extension.schemaFile)
        hintsFile.set(extension.hintsFile)
    }
}
