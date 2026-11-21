package com.luxo.client.compiler

import org.jetbrains.kotlin.ir.declarations.IrClass
import org.jetbrains.kotlin.ir.expressions.IrConst
import org.jetbrains.kotlin.ir.expressions.IrVararg
import org.jetbrains.kotlin.ir.symbols.IrClassSymbol
import org.jetbrains.kotlin.ir.symbols.UnsafeDuringIrConstructionAPI
import org.jetbrains.kotlin.ir.types.IrSimpleType
import org.jetbrains.kotlin.ir.types.IrType
import org.jetbrains.kotlin.ir.types.IrTypeProjection
import org.jetbrains.kotlin.ir.types.classOrNull
import org.jetbrains.kotlin.ir.util.fqNameWhenAvailable
import org.jetbrains.kotlin.ir.util.getAnnotation
import org.jetbrains.kotlin.name.FqName

private val sumMarker = FqName("com.luxo.client.LuxoSealedType")
private val caseMarker = FqName("com.luxo.client.LuxoSealedCase")

/** Resolve generated sum ownership from compiler symbols, never member spelling. */
@OptIn(UnsafeDuringIrConstructionAPI::class)
internal object SelectionSealed {
    fun cases(type: IrType, depth: Int = 0): List<String>? {
        if (depth > 64) return null
        val owner = type.classOrNull?.owner ?: return null
        classCases(owner)?.let { return it }
        val kind = owner.fqNameWhenAvailable?.asString()
        if (kind !in setOf("kotlin.collections.List", "kotlin.Array", "com.luxo.client.Selected")) return null
        val argument = (type as? IrSimpleType)?.arguments?.singleOrNull() as? IrTypeProjection ?: return null
        return cases(argument.type, depth + 1)
    }

    fun caseName(symbol: IrClassSymbol?): String? {
        val owner = symbol?.owner ?: return null
        val annotation = owner.getAnnotation(caseMarker) ?: return null
        val name = (annotation.arguments.getOrNull(1) as? IrConst)?.value as? String ?: return null
        val wireOwner = (annotation.arguments.getOrNull(0) as? IrConst)?.value as? String ?: return null
        val parent = owner.parent as? IrClass ?: return null
        if (parent.name.asString() != wireOwner || name !in classCases(parent).orEmpty()) return null
        if (owner.superTypes.none { it.classOrNull == parent.symbol }) return null
        return name
    }

    private fun classCases(owner: IrClass): List<String>? {
        val annotation = owner.getAnnotation(sumMarker) ?: return null
        val values = annotation.arguments.singleOrNull() as? IrVararg ?: return null
        return values.elements.map { (it as? IrConst)?.value as? String ?: return null }
    }
}
