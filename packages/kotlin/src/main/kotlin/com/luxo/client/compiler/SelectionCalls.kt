package com.luxo.client.compiler

import org.jetbrains.kotlin.backend.common.extensions.IrPluginContext
import org.jetbrains.kotlin.cli.common.messages.CompilerMessageSeverity
import org.jetbrains.kotlin.cli.common.messages.CompilerMessageLocation
import org.jetbrains.kotlin.config.CompilerConfiguration
import org.jetbrains.kotlin.config.messageCollector
import org.jetbrains.kotlin.config.MessageCollectorAccess
import org.jetbrains.kotlin.ir.IrElement
import org.jetbrains.kotlin.ir.declarations.IrFile
import org.jetbrains.kotlin.ir.declarations.IrClass
import org.jetbrains.kotlin.ir.declarations.IrDeclarationOrigin
import org.jetbrains.kotlin.ir.declarations.IrVariable
import org.jetbrains.kotlin.ir.expressions.IrCall
import org.jetbrains.kotlin.ir.expressions.IrConst
import org.jetbrains.kotlin.ir.expressions.IrExpression
import org.jetbrains.kotlin.ir.expressions.IrGetValue
import org.jetbrains.kotlin.ir.expressions.IrTypeOperatorCall
import org.jetbrains.kotlin.ir.expressions.IrTypeOperator
import org.jetbrains.kotlin.ir.expressions.IrContainerExpression
import org.jetbrains.kotlin.ir.expressions.IrWhen
import org.jetbrains.kotlin.ir.expressions.IrFunctionAccessExpression
import org.jetbrains.kotlin.ir.expressions.IrReturn
import org.jetbrains.kotlin.ir.expressions.IrSetField
import org.jetbrains.kotlin.ir.expressions.IrFunctionExpression
import org.jetbrains.kotlin.ir.expressions.IrSetValue
import org.jetbrains.kotlin.ir.expressions.IrStringConcatenation
import org.jetbrains.kotlin.ir.expressions.IrVararg
import org.jetbrains.kotlin.ir.expressions.IrSpreadElement
import org.jetbrains.kotlin.ir.expressions.IrFunctionReference
import org.jetbrains.kotlin.ir.expressions.IrPropertyReference
import org.jetbrains.kotlin.ir.declarations.IrParameterKind
import org.jetbrains.kotlin.ir.symbols.IrValueSymbol
import org.jetbrains.kotlin.ir.symbols.IrClassSymbol
import org.jetbrains.kotlin.ir.symbols.IrTypeParameterSymbol
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
    private var nullChecks: MutableList<Source>? = null
    private var modelClasses: MutableMap<IrClassSymbol, Boolean>? = null
    private var unresolvedAlias = false

    fun rewrite(file: IrFile) {
        file.accept(object : IrVisitor<Unit, Unit>() {
            override fun visitElement(element: IrElement, data: Unit) {
                if (element is IrCall) bindCallback(element)
                if (element is IrFunctionReference && element.symbol.owner.getAnnotation(apiMarker) != null) reject(file, element)
                element.acceptChildren(this, data)
                if (element is IrCall) collect(element)
                if (element is IrTypeOperatorCall) collectSealedCheck(element)
                checkEscape(element)
            }
        }, Unit)
        nullChecks?.forEach { origin ->
            if (!hasSelectedChild(origin)) escaped.add(origin.call)
        }
        for ((call, paths) in calls) {
            val parameter = call.symbol.owner.parameters.singleOrNull { it.name.asString() == "select" }
            if (parameter == null) { reject(file, call); continue }
            val index = parameter.indexInParameters
            if (call.arguments[index] != null) continue
            val plan = call.symbol.owner.parameters.singleOrNull { it.name.asString() == "\$selectionPlan" }
            if (plan != null && call.arguments[plan.indexInParameters] != null) continue
            if (paths.isEmpty() || call in escaped || unresolvedAlias) { reject(file, call); continue }
            val text = render(paths.toList())
            call.arguments[index] = IrConstImpl.string(call.startOffset, call.endOffset,
                context.irBuiltIns.stringType, text)
            if (plan != null) {
                val prepared = hoistSelectionPlan(context, file, call, plan, text)
                if (prepared == null) { reject(file, call); continue }
                call.arguments[plan.indexInParameters] = prepared
            }
        }
    }

    private fun bindCallback(call: IrCall) {
        val function = call.symbol.owner
        val isAPI = function.getAnnotation(apiMarker) != null
        val origin = if (isAPI) null else {
            if (!isCollectionCallback(call)) return
            source(extensionReceiver(call)) ?: return
        }
        val callback = function.parameters.singleOrNull {
            if (isAPI) it.name.asString() == "onData" else it.name.asString() in setOf("transform", "action")
        } ?: return
        val expression = call.arguments[callback.indexInParameters] as? IrFunctionExpression ?: return
        val parameter = expression.function.parameters.singleOrNull { it.kind == IrParameterKind.Regular } ?: return
        bindings[parameter.symbol] = origin ?: Source(call, emptyList(), structured(parameter.type))
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
                        isUnwrap(element) || element in boundCallbacks || pageProperty(element) != null ||
                        collectNullCheck(element) || collectSealedMetadata(element))) return
                escapeArguments(element.arguments)
            }
            is IrFunctionReference -> escapeArguments(element.arguments)
            is IrPropertyReference -> escapeArguments(element.arguments)
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

    private fun escapeArguments(arguments: List<IrExpression?>) {
        arguments.filterNotNull().forEach(::escape)
    }

    // Valid equality built-ins have two argument slots; their List receiver is a JVM Collection.
    private fun collectNullCheck(call: IrCall): Boolean {
        if (call.symbol != context.irBuiltIns.eqeqSymbol && call.symbol != context.irBuiltIns.eqeqeqSymbol) return false
        if (call.arguments.none { it is IrConst && it.value == null }) return false
        for (argument in call.arguments) {
            val origin = source(argument) ?: continue
            if (addSealedLeaves(origin, argument?.type)) continue
            if (origin.isStructured) {
                (nullChecks ?: mutableListOf<Source>().also { nullChecks = it }).add(origin)
            }
        }
        return true
    }

    // Each preserved root call is registered before this post-pass; calls are never removed.
    // Paths are JVM Collections, while List.indices is IntRange, which is not a Collection.
    private fun hasSelectedChild(origin: Source): Boolean = calls[origin.call]?.any { path ->
        path.size > origin.path.size && origin.path.indices.all { path[it] == origin.path[it] }
    } == true

    // Standard bound symbols return their owner or throw. Standard external Kotlin class stubs
    // have named class/package parents, so their fully qualified name is available.
    private fun pageProperty(call: IrCall): String? {
        val property = call.symbol.owner.correspondingPropertySymbol?.owner ?: return null
        val name = property.name.asString()
        if (name != "items" && name != "total" && name != "page" && name != "pageSize") return null
        val owner = property.parent as? IrClass ?: return null
        if (owner.origin != IrDeclarationOrigin.IR_EXTERNAL_DECLARATION_STUB ||
            owner.fqNameWhenAvailable?.asString() != "com.luxo.client.Page") return null
        return name
    }

    private fun escape(value: IrExpression) {
        val origin = source(value) ?: return
        // Casts can erase the expression's type; provenance retains its shape.
        if (origin.path.isEmpty() || origin.isStructured) escaped.add(origin.call)
    }

    private fun structured(type: IrType, depth: Int = 0): Boolean {
        if (depth > 64) return true
        val owner = type.classOrNull
        if (owner == context.irBuiltIns.anyClass) return true
        if (owner != null && modelClass(owner)) return true
        val simple = type as? IrSimpleType ?: return true
        if (simple.classifier is IrTypeParameterSymbol) return true
        // The arguments List is always a JVM Collection for the inlined any check.
        return simple.arguments.any { it !is IrTypeProjection || structured(it.type, depth + 1) }
    }

    // IrClass.declarations is a List, so the inlined any receiver is a JVM Collection.
    private fun modelClass(symbol: IrClassSymbol): Boolean {
        // Declarations stay immutable during this file's selection pass.
        val cache = modelClasses ?: mutableMapOf<IrClassSymbol, Boolean>().also { modelClasses = it }
        return cache.getOrPut(symbol) {
            SelectionSealed.cases(symbol.owner.thisReceiver?.type ?: return@getOrPut true)?.isNotEmpty() == true ||
                SelectionSealed.caseName(symbol) != null || symbol.owner.declarations.any {
                it is org.jetbrains.kotlin.ir.declarations.IrProperty && it.getter?.getAnnotation(fieldMarker) != null
            }
        }
    }

    private fun isUnwrap(call: IrCall): Boolean =
        call.symbol.owner.fqNameWhenAvailable?.asString() == "com.luxo.client.Selected.value" &&
            call.symbol.owner.dispatchReceiverParameter != null

    private fun collect(call: IrCall) {
        if (call.symbol.owner.getAnnotation(apiMarker) != null) calls.getOrPut(call) { linkedSetOf() }
        val source = source(call) ?: return
        if (source.path.isNotEmpty() && !source.isStructured) calls.getOrPut(source.call) { linkedSetOf() }.add(source.path)
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
            is IrTypeOperatorCall -> {
                val origin = source(expression.argument, depth + 1) ?: return null
                val case = if (expression.operator in setOf(IrTypeOperator.CAST, IrTypeOperator.SAFE_CAST, IrTypeOperator.IMPLICIT_CAST))
                    expression.typeOperand.classOrNull else null
                appendCase(origin, case)
            }
            is IrContainerExpression -> source(expression.statements.lastOrNull() as? IrExpression, depth + 1)
            is IrWhen -> conditionalSource(expression, depth)
            is IrCall -> callSource(expression, depth)
            else -> null
        }
    }

    private fun conditionalSource(expression: IrWhen, depth: Int): Source? {
        var found: Source? = null
        var unresolved = false
        for (branch in expression.branches) {
            val candidate = source(branch.result, depth + 1)
            if (candidate == null || (!candidate.isStructured && candidate.path.isNotEmpty())) {
                if (!nullOrNever(branch.result)) unresolved = true
            } else if (found == null) {
                found = candidate
            } else if (found != candidate) {
                escaped.add(found.call)
                escaped.add(candidate.call)
                unresolved = true
            }
        }
        if (unresolved && found != null) {
            escaped.add(found.call)
            return null
        }
        return found
    }

    private fun nullOrNever(expression: IrExpression): Boolean {
        var current = expression
        repeat(128) {
            if (current.type == context.irBuiltIns.nothingType) return true
            current = when (val value = current) {
                is IrTypeOperatorCall -> value.argument
                is IrContainerExpression -> value.statements.lastOrNull() as? IrExpression ?: return false
                else -> return value is IrConst && value.value == null
            }
        }
        return false
    }

    private fun callSource(call: IrCall, depth: Int): Source? {
        if (call.symbol.owner.getAnnotation(apiMarker) != null) {
            if (call.symbol.owner.parameters.any { it.name.asString() == "onData" }) return null
            return Source(call, emptyList(), structured(call.symbol.owner.returnType))
        }
        if (isUnwrap(call) || pageProperty(call) == "items") return source(call.dispatchReceiver, depth + 1)
        val field = call.symbol.owner.getAnnotation(fieldMarker) ?: return null
        val name = (field.arguments.singleOrNull() as? IrConst)?.value as? String ?: return null
        val receiver = source(call.dispatchReceiver, depth + 1) ?: return null
        val owner = call.symbol.owner.parent as? IrClass
        val origin = appendCase(receiver, owner?.symbol)
        return Source(origin.call, origin.path + name, structured(call.type))
    }

    private fun appendCase(origin: Source, symbol: IrClassSymbol?): Source {
        val name = SelectionSealed.caseName(symbol) ?: return origin
        if (origin.caseType == symbol) return origin
        return origin.copy(path = origin.path + name, isStructured = true, caseType = symbol)
    }

    private fun addSealedLeaves(origin: Source, type: IrType?): Boolean {
        val names = type?.let { SelectionSealed.cases(it) } ?: return false
        if (names.isEmpty()) return false
        val paths = calls.getOrPut(origin.call) { linkedSetOf() }
        names.forEach { paths.add(origin.path + it) }
        return true
    }

    private fun collectSealedCheck(expression: IrTypeOperatorCall) {
        if (expression.operator != IrTypeOperator.INSTANCEOF && expression.operator != IrTypeOperator.NOT_INSTANCEOF) return
        val origin = source(expression.argument) ?: return
        addSealedLeaves(origin, expression.argument.type)
    }

    private fun collectSealedMetadata(call: IrCall): Boolean {
        val receiver = call.dispatchReceiver ?: extensionReceiver(call) ?: return false
        val function = call.symbol.owner
        val property = function.correspondingPropertySymbol?.owner
        val owner = property?.parent as? IrClass
        val size = property?.name?.asString() == "size" && owner?.fqNameWhenAvailable?.asString() in
            setOf("kotlin.collections.Collection", "kotlin.collections.List", "kotlin.Array")
        val empty = function.fqNameWhenAvailable?.asString() in
            setOf("kotlin.collections.isEmpty", "kotlin.collections.Collection.isEmpty", "kotlin.collections.List.isEmpty")
        if (!size && !empty) return false
        val origin = source(receiver) ?: return false
        return addSealedLeaves(origin, receiver.type)
    }

    private fun render(paths: List<List<String>>): String = paths.filter { it.isNotEmpty() }
        .groupBy { it.first() }.toSortedMap().entries.joinToString(",") { (name, nested) ->
            val children = render(nested.map { it.drop(1) })
            if (children.isEmpty()) name else "$name{$children}"
        }

    private data class Source(val call: IrCall, val path: List<String>, val isStructured: Boolean, val caseType: IrClassSymbol? = null)
}
