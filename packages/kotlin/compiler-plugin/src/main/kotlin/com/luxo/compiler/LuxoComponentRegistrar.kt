package com.luxo.compiler

import org.jetbrains.kotlin.backend.common.extensions.IrGenerationExtension
import org.jetbrains.kotlin.compiler.plugin.CompilerPluginRegistrar
import org.jetbrains.kotlin.compiler.plugin.ExperimentalCompilerApi
import org.jetbrains.kotlin.config.CompilerConfiguration

/**
 * Luxo Kotlin Compiler Plugin — registers the IR analysis extension.
 * Analyzes function bodies for field access patterns on LuxoClient return values.
 * Generates optimized $select strings at compile time.
 */
@OptIn(ExperimentalCompilerApi::class)
class LuxoComponentRegistrar : CompilerPluginRegistrar() {
    override val supportsK2 = true

    override fun ExtensionStorage.registerExtensions(configuration: CompilerConfiguration) {
        IrGenerationExtension.registerExtension(LuxoIrExtension(configuration))
    }
}
