package com.luxo.compiler

import org.jetbrains.kotlin.backend.common.extensions.IrPluginContext
import org.jetbrains.kotlin.ir.IrElement
import org.jetbrains.kotlin.ir.declarations.*
import org.jetbrains.kotlin.ir.expressions.*
import org.jetbrains.kotlin.ir.types.*
import org.jetbrains.kotlin.ir.util.fqNameWhenAvailable
import org.jetbrains.kotlin.ir.util.kotlinFqName
import org.jetbrains.kotlin.ir.visitors.IrElementVisitorVoid
import org.jetbrains.kotlin.ir.visitors.acceptChildrenVoid
import org.jetbrains.kotlin.name.FqName

/**
 * Walks Kotlin IR to collect field access patterns on LuxoClient API responses.
 *
 * Tracks:
 * - Direct access: user.name → records "name" for getUser
 * - Nested relations: post.user.name → records "user{name}" for getPost
 * - Lambda params: post.comments.forEach { it.user.name } → "comments{user{name}}"
 * - Variable aliases: val author = post.user; author.name → "user{name}"
 */
class SelectCollector(
    private val pluginContext: IrPluginContext
) : IrElementVisitorVoid() {

    // variable (IrVariable) → API name
    private val varToAPI = mutableMapOf<IrVariable, String>()

    // variable → return type FqName (for model field validation)
    private val varToType = mutableMapOf<IrVariable, String>()

    // variable → parent source (for alias/lambda tracking)
    private val varToParent = mutableMapOf<IrVariable, ParentRef>()

    // API name → field tree
    private val apiTrees = mutableMapOf<String, FieldNode>()

    data class ParentRef(val parentVar: IrVariable, val field: String)

    fun visitModule(module: IrModuleFragment) {
        module.acceptChildrenVoid(this)
    }

    override fun visitElement(element: IrElement) {
        element.acceptChildrenVoid(this)
    }

    override fun visitVariable(declaration: IrVariable) {
        val init = declaration.initializer ?: run {
            declaration.acceptChildrenVoid(this)
            return
        }

        // Pattern: val user = client.getUser(1)
        val call = unwrapCall(init)
        if (call != null) {
            val apiName = extractAPIName(call)
            if (apiName != null) {
                varToAPI[declaration] = apiName
                apiTrees.putIfAbsent(apiName, FieldNode.root())

                // Track return type
                val returnType = call.type.classifierFqName()
                if (returnType != null) {
                    varToType[declaration] = returnType
                }
            }
        }

        // Pattern: val author = post.user (member access alias)
        if (init is IrGetField || init is IrCall) {
            val memberAccess = extractMemberAccess(init)
            if (memberAccess != null) {
                val (receiver, fieldName) = memberAccess
                if (receiver is IrGetValue) {
                    val parentVar = receiver.symbol.owner as? IrVariable
                    if (parentVar != null && (varToAPI.containsKey(parentVar) || varToParent.containsKey(parentVar))) {
                        varToParent[declaration] = ParentRef(parentVar, fieldName)
                        // Resolve nested type
                        val parentType = varToType[parentVar]
                        if (parentType != null) {
                            // Type resolution would need schema info — simplified here
                            varToType[declaration] = fieldName
                        }
                    }
                }
            }
        }

        declaration.acceptChildrenVoid(this)
    }

    override fun visitCall(expression: IrCall) {
        // Track lambda params for forEach/map/filter etc.
        trackLambdaParams(expression)

        // Record field access: post.user.name
        recordFieldAccess(expression)

        expression.acceptChildrenVoid(this)
    }

    override fun visitGetField(expression: IrGetField) {
        recordFieldAccessFromGetField(expression)
        expression.acceptChildrenVoid(this)
    }

    private fun trackLambdaParams(call: IrCall) {
        val methodName = call.symbol.owner.name.asString()
        if (methodName !in COLLECTION_METHODS) return

        val dispatchReceiver = call.dispatchReceiver ?: return

        // Find the lambda argument
        for (i in 0 until call.valueArgumentsCount) {
            val arg = call.getValueArgument(i) ?: continue
            val lambda = extractLambda(arg) ?: continue
            val param = lambda.valueParameters.firstOrNull() ?: continue

            // Resolve source: post.comments.forEach { ... }
            val memberAccess = extractMemberAccess(dispatchReceiver)
            if (memberAccess != null) {
                val (receiver, fieldName) = memberAccess
                if (receiver is IrGetValue) {
                    val parentVar = receiver.symbol.owner as? IrVariable
                    if (parentVar != null) {
                        // Create a synthetic variable reference for the lambda param
                        varToParent[param as? IrVariable ?: return] = ParentRef(parentVar, fieldName)
                    }
                }
            }
        }
    }

    private fun recordFieldAccess(call: IrCall) {
        val propName = extractPropertyName(call) ?: return
        val receiver = call.dispatchReceiver ?: return

        val (apiName, chain) = buildAccessChain(receiver, propName) ?: return

        val tree = apiTrees[apiName] ?: return
        var node = tree
        for (field in chain) {
            node = node.addChild(field)
        }
    }

    private fun recordFieldAccessFromGetField(expr: IrGetField) {
        val fieldName = expr.symbol.owner.name.asString()
        val receiver = expr.receiver ?: return

        val (apiName, chain) = buildAccessChain(receiver, fieldName) ?: return

        val tree = apiTrees[apiName] ?: return
        var node = tree
        for (field in chain) {
            node = node.addChild(field)
        }
    }

    private fun buildAccessChain(receiver: IrExpression, fieldName: String): Pair<String, List<String>>? {
        val chain = mutableListOf(fieldName)
        var current: IrExpression = receiver

        while (true) {
            when (current) {
                is IrCall -> {
                    val prop = extractPropertyName(current)
                    if (prop != null) {
                        chain.add(0, prop)
                        current = current.dispatchReceiver ?: break
                    } else break
                }
                is IrGetField -> {
                    chain.add(0, current.symbol.owner.name.asString())
                    current = current.receiver ?: break
                }
                is IrGetValue -> {
                    val variable = current.symbol.owner
                    if (variable is IrVariable) {
                        val apiName = varToAPI[variable]
                        if (apiName != null) {
                            return Pair(apiName, chain)
                        }
                        val parentChain = resolveParentChain(variable)
                        if (parentChain != null) {
                            // Find root variable's API
                            var root = variable
                            val seen = mutableSetOf<IrVariable>()
                            while (varToParent.containsKey(root) && root !in seen) {
                                seen.add(root)
                                root = varToParent[root]!!.parentVar
                            }
                            val rootAPI = varToAPI[root] ?: break
                            return Pair(rootAPI, parentChain + chain)
                        }
                    }
                    break
                }
                else -> break
            }
        }

        return null
    }

    private fun resolveParentChain(variable: IrVariable): List<String>? {
        val fields = mutableListOf<String>()
        var current = variable
        val seen = mutableSetOf<IrVariable>()

        while (varToParent.containsKey(current)) {
            if (current in seen) break
            seen.add(current)
            val parent = varToParent[current]!!
            fields.add(0, parent.field)
            current = parent.parentVar
        }

        return if (varToAPI.containsKey(current) && fields.isNotEmpty()) fields else null
    }

    // addChainToTree removed — chain resolution now happens inline in recordFieldAccess

    // ─── Result builders ────────────────────────────────────────────────────

    fun buildHints(): Map<String, String> {
        val result = mutableMapOf<String, String>()
        for ((api, tree) in apiTrees) {
            val selectStr = tree.toSelectString()
            if (selectStr.isNotEmpty()) {
                result[api] = selectStr
            }
        }
        return result
    }

    fun getDepth(apiName: String): Int {
        return apiTrees[apiName]?.maxDepth() ?: 0
    }

    // ─── IR helpers ─────────────────────────────────────────────────────────

    private fun unwrapCall(expr: IrExpression): IrCall? {
        return when (expr) {
            is IrCall -> expr
            is IrTypeOperatorCall -> unwrapCall(expr.argument)
            else -> null
        }
    }

    private fun extractAPIName(call: IrCall): String? {
        val name = call.symbol.owner.name.asString()
        val receiver = call.dispatchReceiver ?: call.extensionReceiver ?: return null
        val receiverType = receiver.type.classifierFqName() ?: return null

        // Check if receiver is a LuxoClient
        if ("Client" in receiverType || "Luxo" in receiverType) {
            return name
        }
        return null
    }

    private fun extractPropertyName(call: IrCall): String? {
        val owner = call.symbol.owner
        // Kotlin property getters: getName() for val name
        val name = owner.name.asString()
        if (name.startsWith("get") && name.length > 3) {
            return name[3].lowercaseChar() + name.substring(4)
        }
        // Direct property access in IR
        if (owner.correspondingPropertySymbol != null) {
            return owner.correspondingPropertySymbol!!.owner.name.asString()
        }
        return null
    }

    private fun extractMemberAccess(expr: IrExpression): Pair<IrExpression, String>? {
        return when (expr) {
            is IrCall -> {
                val propName = extractPropertyName(expr) ?: return null
                val receiver = expr.dispatchReceiver ?: return null
                Pair(receiver, propName)
            }
            is IrGetField -> {
                val receiver = expr.receiver ?: return null
                Pair(receiver, expr.symbol.owner.name.asString())
            }
            else -> null
        }
    }

    private fun extractLambda(expr: IrExpression): IrSimpleFunction? {
        return when (expr) {
            is IrFunctionExpression -> expr.function
            is IrFunctionReference -> expr.symbol.owner as? IrSimpleFunction
            else -> null
        }
    }

    private fun IrType.classifierFqName(): String? {
        val classifier = this.classifierOrNull ?: return null
        return classifier.owner.kotlinFqName.asString()
    }

    companion object {
        val COLLECTION_METHODS = setOf(
            "forEach", "map", "filter", "find", "any", "all",
            "none", "flatMap", "sortedBy", "groupBy", "associate",
            "mapNotNull", "firstOrNull", "lastOrNull",
        )
    }
}
