package com.luxo.client.compiler

import org.jetbrains.kotlin.backend.common.extensions.IrGenerationExtension
import org.jetbrains.kotlin.backend.common.extensions.IrPluginContext
import org.jetbrains.kotlin.compiler.plugin.CompilerPluginRegistrar
import org.jetbrains.kotlin.compiler.plugin.ExperimentalCompilerApi
import org.jetbrains.kotlin.config.CompilerConfiguration
import org.jetbrains.kotlin.ir.declarations.IrModuleFragment
import org.jetbrains.kotlin.config.KotlinCompilerVersion
import org.jetbrains.kotlin.config.MessageCollectorAccess
import org.jetbrains.kotlin.config.messageCollector
import org.jetbrains.kotlin.cli.common.messages.CompilerMessageSeverity
import java.util.Properties

@OptIn(ExperimentalCompilerApi::class, MessageCollectorAccess::class)
class SelectionPluginRegistrar : CompilerPluginRegistrar() {
    override val pluginId = "com.luxo.select"
    override val supportsK2 = true

    override fun ExtensionStorage.registerExtensions(configuration: CompilerConfiguration) {
        val versions = Properties()
        val resource = SelectionPluginRegistrar::class.java.getResourceAsStream("tooling.properties")
        resource?.use { versions.load(it) }
        if (versions.getProperty("kotlinVersion") != KotlinCompilerVersion.VERSION) {
            configuration.messageCollector.report(CompilerMessageSeverity.ERROR,
                "Luxo selection plugin requires Kotlin ${versions.getProperty("kotlinVersion")}; found ${KotlinCompilerVersion.VERSION}")
            return
        }
        IrGenerationExtension.registerExtension(object : IrGenerationExtension {
            override fun generate(moduleFragment: IrModuleFragment, pluginContext: IrPluginContext) {
                for (file in moduleFragment.files) SelectionCalls(pluginContext, configuration).rewrite(file)
            }
        })
    }
}
