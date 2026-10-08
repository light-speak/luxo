package com.luxo.client

import kotlinx.serialization.json.JsonPrimitive

/** Enum decisions are compiled into the generated client, never looked up per request. */
internal object KotlinEnumCodegen {
    fun validate(schema: LuxoSchema) {
        for ((key, declaration) in schema.enums) {
            require(key == declaration.name) { "Enum key $key disagrees with ${declaration.name}" }
            require(declaration.values.isNotEmpty() && declaration.values.distinct().size == declaration.values.size) {
                "Enum $key requires distinct members"
            }
        }
        val fields = schema.models.values.flatMap { it.fields } + schema.types.values.flatMap { it.fields }
        for (field in fields) validateReference(schema, field.type, field.typeName)
        for (api in schema.apis.values) {
            require(api.returnType != "Enum") { "API ${api.name} requires a named enum return type" }
            for (param in api.params) validateReference(schema, param.type, param.typeName)
        }
    }

    private fun validateReference(schema: LuxoSchema, type: String, name: String?) {
        require(type != "Enum" || name in schema.enums) { "Enum requires a declared typeName, got $name" }
    }

    fun append(out: StringBuilder, declaration: LuxoEnum) {
        val name = declaration.name
        out.appendLine("@Serializable(with = $name.Serializer::class)")
        out.appendLine("sealed interface $name {")
        out.appendLine("    val rawValue: String")
        out.appendLine("    enum class Known(override val rawValue: String) : $name {")
        out.appendLine(declaration.values.joinToString(",\n") { "        ${member(it)}(${literal(it)})" } + ";")
        out.appendLine("    }")
        appendUnknown(out, declaration)
        out.appendLine("    companion object {")
        out.appendLine("        fun fromValue(value: String): $name = Unknown.fromValue(value)")
        out.appendLine("    }")
        appendSerializer(out, name)
        out.appendLine("}\n")
    }

    private fun appendUnknown(out: StringBuilder, declaration: LuxoEnum) {
        val name = declaration.name
        out.appendLine("    class Unknown private constructor(override val rawValue: String) : $name {")
        out.appendLine("        override fun equals(other: Any?): Boolean = other is Unknown && rawValue == other.rawValue")
        out.appendLine("        override fun hashCode(): Int = rawValue.hashCode()")
        out.appendLine("        companion object {")
        out.appendLine("            internal fun fromValue(value: String): $name = when (value) {")
        for (value in declaration.values) out.appendLine("                ${literal(value)} -> Known.${member(value)}")
        out.appendLine("                else -> Unknown(value)")
        out.appendLine("            }")
        out.appendLine("        }")
        out.appendLine("    }")
    }

    private fun appendSerializer(out: StringBuilder, name: String) {
        out.appendLine("    object Serializer : kotlinx.serialization.KSerializer<$name> {")
        out.appendLine("        override val descriptor = kotlinx.serialization.descriptors.PrimitiveSerialDescriptor(\"$name\", kotlinx.serialization.descriptors.PrimitiveKind.STRING)")
        out.appendLine("        override fun serialize(encoder: kotlinx.serialization.encoding.Encoder, value: $name) {")
        out.appendLine("            luxoUtf8Length(value.rawValue)")
        out.appendLine("            encoder.encodeString(value.rawValue)")
        out.appendLine("        }")
        out.appendLine("        override fun deserialize(decoder: kotlinx.serialization.encoding.Decoder): $name {")
        out.appendLine("            val value = decoder.decodeString()")
        out.appendLine("            luxoUtf8Length(value)")
        out.appendLine("            return fromValue(value)")
        out.appendLine("        }")
        out.appendLine("    }")
    }

    private fun literal(value: String): String = JsonPrimitive(value).toString().replace("$", "\\$")

    // Escaped suffixes cannot collide with DSL identifiers and enum intrinsic members.
    private fun member(value: String): String = "`" +
        (if (value in setOf("name", "ordinal", "rawValue", "entries", "values", "valueOf", "Known")) "$value$" else value) + "`"
}
