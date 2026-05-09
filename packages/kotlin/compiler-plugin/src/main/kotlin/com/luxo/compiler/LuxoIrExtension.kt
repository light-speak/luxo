package com.luxo.compiler

import org.jetbrains.kotlin.backend.common.extensions.IrGenerationExtension
import org.jetbrains.kotlin.backend.common.extensions.IrPluginContext
import org.jetbrains.kotlin.config.CompilerConfiguration
import org.jetbrains.kotlin.ir.declarations.IrModuleFragment

/**
 * IR Generation Extension — entry point for analyzing Kotlin IR.
 * Walks all function bodies to find LuxoClient API calls and trace
 * field access patterns for compile-time $select optimization.
 */
class LuxoIrExtension(
    private val configuration: CompilerConfiguration
) : IrGenerationExtension {

    override fun generate(moduleFragment: IrModuleFragment, pluginContext: IrPluginContext) {
        val collector = SelectCollector(pluginContext)
        collector.visitModule(moduleFragment)

        val hints = collector.buildHints()
        if (hints.isNotEmpty()) {
            // Log collected hints (will be used by gradle plugin to generate SelectHints.kt)
            for ((api, select) in hints) {
                val depth = collector.getDepth(api)
                if (depth > MAX_NESTING_DEPTH) {
                    println("[luxo] Warning: $api has $depth-level nested field selection " +
                        "(max recommended: $MAX_NESTING_DEPTH). Consider restructuring your query.")
                }
                println("[luxo] $api → \$select: $select")
            }
        }
    }

    companion object {
        const val MAX_NESTING_DEPTH = 5
    }
}
