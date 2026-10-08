package com.luxo.client.compiler

import org.jetbrains.kotlin.backend.common.extensions.IrPluginContext
import org.jetbrains.kotlin.cli.common.messages.CompilerMessageSeverity
import org.jetbrains.kotlin.cli.common.messages.CompilerMessageLocation
import org.jetbrains.kotlin.config.CompilerConfiguration
import org.jetbrains.kotlin.config.messageCollector
import org.jetbrains.kotlin.config.MessageCollectorAccess
import org.jetbrains.kotlin.ir.IrElement
import org.jetbrains.kotlin.ir.declarations.IrFile
import org.jetbrains.kotlin.ir.declarations.IrVariable
import org.jetbrains.kotlin.ir.expressions.IrCall
import org.jetbrains.kotlin.ir.expressions.IrConst
import org.jetbrains.kotlin.ir.expressions.IrExpression
import org.jetbrains.kotlin.ir.expressions.IrGetValue
import org.jetbrains.kotlin.ir.expressions.IrTypeOperatorCall
import org.jetbrains.kotlin.ir.expressions.IrFunctionAccessExpression
import org.jetbrains.kotlin.ir.expressions.IrReturn
import org.jetbrains.kotlin.ir.expressions.IrSetField
import org.jetbrains.kotlin.ir.expressions.IrFunctionExpression
import org.jetbrains.kotlin.ir.expressions.IrSetValue
import org.jetbrains.kotlin.ir.expressions.IrStringConcatenation
import org.jetbrains.kotlin.ir.expressions.IrVararg
import org.jetbrains.kotlin.ir.expressions.IrSpreadElement
import org.jetbrains.kotlin.ir.expressions.IrFunctionReference
import org.jetbrains.kotlin.ir.declarations.IrParameterKind
import org.jetbrains.kotlin.ir.symbols.IrValueSymbol
import org.jetbrains.kotlin.ir.types.IrType
import org.jetbrains.kotlin.ir.types.IrSimpleType
import org.jetbrains.kotlin.ir.types.IrTypeProjection
import org.jetbrains.kotlin.ir.expressions.impl.IrConstImpl
import org.jetbrains.kotlin.ir.util.getAnnotation
import org.jetbrains.kotlin.ir.util.fqNameWhenAvailable
import org.jetbrains.kotlin.ir.types.classOrNull
import org.jetbrains.kotlin.ir.visitors.IrVisitor
import org.jetbrains.kotlin.ir.symbols.UnsafeDuringIrConstructionAPI
import org.jetbrains.kotlin.name.FqName

private val apiMarker = FqName("com.luxo.client.LuxoSelection")
private val fieldMarker = FqName("com.luxo.client.LuxoSelectionField")

/** Works with resolved symbols, never receiver names or source-text matching. */
@OptIn(UnsafeDuringIrConstructionAPI::class, MessageCollectorAccess::class)
internal class SelectionCalls(private val context: IrPluginContext, private val configuration: CompilerConfiguration) {
    private val calls = linkedMapOf<IrCall, MutableSet<List<String>>>()
    private val escaped = mutableSetOf<IrCall>()
    private val bindings = mutableMapOf<IrValueSymbol, Source>()
    private val boundCallbacks = mutableSetOf<IrCall>()
    private var unresolvedAlias = false

    fun rewrite(file: IrFile) {
        file.accept(object : IrVisitor<Unit, Unit>() {
            override fun visitElement(element: IrElement, data: Unit) {
                if (element is IrCall) bindCallback(element)
                if (element is IrFunctionReference && element.symbol.owner.getAnnotation(apiMarker) != null) reject(file, element)
                element.acceptChildren(this, data)
                if (element is IrCall) collect(element)
                checkEscape(element)
            }
        }, Unit)
        for ((call, paths) in calls) {
            val parameter = call.symbol.owner.parameters.singleOrNull { it.name.asString() == "select" }
            if (parameter == null) { reject(file, call); continue }
            val index = parameter.indexInParameters
            if (call.arguments[index] != null) continue
            if (paths.isEmpty() || call in escaped || unresolvedAlias) { reject(file, call); continue }
            call.arguments[index] = IrConstImpl.string(call.startOffset, call.endOffset,
                context.irBuiltIns.stringType, render(paths.toList()))
        }
    }

    private fun bindCallback(call: IrCall) {
        val function = call.symbol.owner
        val isAPI = function.getAnnotation(apiMarker) != null
        val origin = if (isAPI) Source(call, emptyList()) else {
            if (!isCollectionCallback(call)) return
            source(extensionReceiver(call)) ?: return
        }
        val callback = function.parameters.singleOrNull {
            if (isAPI) it.name.asString() == "onData" else it.name.asString() in setOf("transform", "action")
        } ?: return
        val expression = call.arguments[callback.indexInParameters] as? IrFunctionExpression ?: return
        val parameter = expression.function.parameters.singleOrNull { it.kind == IrParameterKind.Regular } ?: return
        bindings[parameter.symbol] = origin
        boundCallbacks.add(call)
    }

    private fun extensionReceiver(call: IrCall): IrExpression? = call.symbol.owner.parameters
        .singleOrNull { it.kind == IrParameterKind.ExtensionReceiver }?.let { call.arguments[it.indexInParameters] }

    private fun isCollectionCallback(call: IrCall): Boolean = call.symbol.owner.fqNameWhenAvailable?.asString() in
        setOf("kotlin.collections.map", "kotlin.collections.mapNotNull", "kotlin.collections.forEach")

    private fun reject(file: IrFile, call: IrElement) {
        configuration.messageCollector.report(CompilerMessageSeverity.ERROR, "Cannot infer Luxo selection safely; supply select explicitly",
            CompilerMessageLocation.create(file.fileEntry.name, file.fileEntry.getLineNumber(call.startOffset) + 1,
                file.fileEntry.getColumnNumber(call.startOffset) + 1, null))
    }

    private fun checkEscape(element: IrElement) {
        when (element) {
            is IrFunctionAccessExpression -> {
                if (element is IrCall && (element.symbol.owner.getAnnotation(fieldMarker) != null ||
                        isUnwrap(element) || element in boundCallbacks)) return
                element.arguments.filterNotNull().forEach(::escape)
            }
            is IrReturn -> escape(element.value)
            is IrSetField -> escape(element.value)
            is IrSetValue -> escape(element.value)
            is IrStringConcatenation -> element.arguments.forEach(::escape)
            is IrVararg -> element.elements.forEach {
                when (it) {
                    is IrExpression -> escape(it)
                    is IrSpreadElement -> escape(it.expression)
                }
            }
            is IrVariable -> if (element.isVar) element.initializer?.let(::escape)
        }
    }

    private fun escape(value: IrExpression) {
        val origin = source(value) ?: return
        if (origin.path.isEmpty() || structured(value.type)) escaped.add(origin.call)
    }

    private fun structured(type: IrType, depth: Int = 0): Boolean {
        if (depth > 64) return true
        if (type.classOrNull?.owner?.declarations?.any {
            it is org.jetbrains.kotlin.ir.declarations.IrProperty && it.getter?.getAnnotation(fieldMarker) != null
        } == true) return true
        return (type as? IrSimpleType)?.arguments?.any {
            it is IrTypeProjection && structured(it.type, depth + 1)
        } == true
    }

    private fun isUnwrap(call: IrCall): Boolean =
        call.symbol.owner.fqNameWhenAvailable?.asString() == "com.luxo.client.Selected.value" &&
            call.symbol.owner.dispatchReceiverParameter != null

    private fun collect(call: IrCall) {
        if (call.symbol.owner.getAnnotation(apiMarker) != null) calls.getOrPut(call) { linkedSetOf() }
        val source = source(call) ?: return
        if (source.path.isNotEmpty()) calls.getOrPut(source.call) { linkedSetOf() }.add(source.path)
    }

    private fun source(expression: IrExpression?, depth: Int = 0): Source? {
        if (depth > 128) { unresolvedAlias = true; return null }
        return when (expression) {
            is IrGetValue -> {
                bindings[expression.symbol]?.let { return it }
                val variable = expression.symbol.owner as? IrVariable ?: return null
                if (variable.isVar) return null
                source(variable.initializer, depth + 1)
            }
            is IrTypeOperatorCall -> source(expression.argument, depth + 1)
            is IrCall -> callSource(expression, depth)
            else -> null
        }
    }

    private fun callSource(call: IrCall, depth: Int): Source? {
        if (call.symbol.owner.getAnnotation(apiMarker) != null) {
            if (call.symbol.owner.parameters.any { it.name.asString() == "onData" }) return null
            return Source(call, emptyList())
        }
        if (isUnwrap(call)) return source(call.dispatchReceiver, depth + 1)
        val field = call.symbol.owner.getAnnotation(fieldMarker) ?: return null
        val name = (field.arguments.singleOrNull() as? IrConst)?.value as? String ?: return null
        val receiver = source(call.dispatchReceiver, depth + 1) ?: return null
        return Source(receiver.call, receiver.path + name)
    }

    private fun render(paths: List<List<String>>): String = paths.filter { it.isNotEmpty() }
        .groupBy { it.first() }.toSortedMap().entries.joinToString(",") { (name, nested) ->
            val children = render(nested.map { it.drop(1) })
            if (children.isEmpty()) name else "$name{$children}"
        }

    private data class Source(val call: IrCall, val path: List<String>)
}
