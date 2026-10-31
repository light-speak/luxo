package com.luxo.client.compiler

import org.jetbrains.kotlin.backend.common.extensions.IrPluginContext
import org.jetbrains.kotlin.backend.common.lower.DeclarationIrBuilder
import org.jetbrains.kotlin.descriptors.DescriptorVisibilities
import org.jetbrains.kotlin.ir.builders.declarations.buildField
import org.jetbrains.kotlin.ir.builders.irCallConstructor
import org.jetbrains.kotlin.ir.declarations.IrDeclarationWithName
import org.jetbrains.kotlin.ir.declarations.IrFile
import org.jetbrains.kotlin.ir.declarations.IrValueParameter
import org.jetbrains.kotlin.ir.expressions.IrCall
import org.jetbrains.kotlin.ir.expressions.IrExpression
import org.jetbrains.kotlin.ir.expressions.impl.IrConstImpl
import org.jetbrains.kotlin.ir.expressions.impl.IrGetFieldImpl
import org.jetbrains.kotlin.ir.symbols.UnsafeDuringIrConstructionAPI
import org.jetbrains.kotlin.ir.types.classOrNull
import org.jetbrains.kotlin.ir.types.makeNotNull
import org.jetbrains.kotlin.ir.util.constructors
import org.jetbrains.kotlin.ir.util.fqNameWhenAvailable
import org.jetbrains.kotlin.name.Name

/** One immutable plan per resolved call site, initialized by the JVM once. */
@OptIn(UnsafeDuringIrConstructionAPI::class)
internal fun hoistSelectionPlan(
    context: IrPluginContext, file: IrFile, call: IrCall,
    parameter: IrValueParameter, text: String,
): IrExpression? {
    val type = parameter.type.makeNotNull()
    val owner = type.classOrNull?.owner ?: return null
    if (owner.fqNameWhenAvailable?.asString() != "com.luxo.client.PreparedSelection") return null
    val constructor = owner.constructors.singleOrNull() ?: return null
    var name = Name.identifier("\$luxoSelection${call.startOffset}")
    while (file.declarations.any { (it as? IrDeclarationWithName)?.name == name }) {
        name = Name.identifier(name.asString() + "_")
    }
    val field = context.irFactory.buildField {
        this.name = name
        this.type = type
        visibility = DescriptorVisibilities.PRIVATE
        isFinal = true
        isStatic = true
    }
    field.parent = file
    val initializer = DeclarationIrBuilder(context, field.symbol).irCallConstructor(constructor.symbol, emptyList())
    initializer.arguments[0] = IrConstImpl.string(call.startOffset, call.endOffset, context.irBuiltIns.stringType, text)
    field.initializer = context.irFactory.createExpressionBody(call.startOffset, call.endOffset, initializer)
    file.declarations.add(field)
    return IrGetFieldImpl(call.startOffset, call.endOffset, field.symbol, type)
}
