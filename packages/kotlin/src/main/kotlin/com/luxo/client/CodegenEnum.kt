package com.luxo.client

import kotlinx.serialization.json.JsonPrimitive

/** Enum decisions are compiled into the generated client, never looked up per request. */
internal object KotlinEnumCodegen {
    fun validate(schema: LuxoSchema) {
        for ((key, declaration) in schema.enums) {
            validateName(key, declaration.name)
            require(declaration.values.distinct().size == declaration.values.size) {
                "Enum $key requires distinct members"
            }
        }
        for ((key, model) in schema.models) {
            validateName(key, model.name)
            require(key !in schema.types && key !in schema.enums) { "duplicate type $key" }
            validateFields(schema, model.fields)
        }
        for ((key, type) in schema.types) {
            validateName(key, type.name)
            require(key !in schema.enums) { "duplicate type $key" }
            validateFields(schema, type.fields)
        }
        for ((key, owner) in schema.sealeds) {
            validateName(key, owner.name)
            require(key !in schema.models && key !in schema.types && key !in schema.enums) { "duplicate type $key" }
            val names = mutableSetOf<String>()
            for (variant in owner.variants) {
                validateMemberName(variant.name, names)
                val fields = mutableSetOf<String>()
                for (field in variant.fields) {
                    validateMemberName(field.name, fields)
                    validateReference(schema, field.type, field.typeName)
                }
            }
        }
        for ((key, api) in schema.apis) {
            validateName(key, api.name)
            require(api.returnType != "Enum") { "API ${api.name} requires a named enum return type" }
            val type = api.returnType
            require(type != null && (LuxoCodegen.isScalar(type) || type in schema.models ||
                type in schema.types || type in schema.enums || type in schema.sealeds)) {
                "API ${api.name} has an undeclared return type $type"
            }
            val names = mutableSetOf<String>()
            for (param in api.params) {
                validateMemberName(param.name, names)
                validateReference(schema, param.type, param.typeName)
            }
        }
    }

    private fun validateName(key: String, name: String) {
        require(name.isNotEmpty()) { "Declaration name must not be empty" }
        require(key == name) { "Declaration name $name does not match key $key" }
    }

    private fun validateMemberName(name: String, names: MutableSet<String>) {
        require(name.isNotEmpty()) { "Member name must not be empty" }
        require(names.add(name)) { "duplicate name $name" }
    }

    private fun validateFields(schema: LuxoSchema, fields: List<LuxoField>) {
        val names = mutableSetOf<String>()
        for (field in fields) {
            validateMemberName(field.name, names)
            validateReference(schema, field.type, field.typeName)
        }
    }

    private fun validateReference(schema: LuxoSchema, type: String, name: String?) {
        require(type != "Enum" || name in schema.enums) { "Enum requires a declared typeName, got $name" }
        require(type != "Model" || name in schema.models || name in schema.types) {
            "Model requires a declared typeName, got $name"
        }
        require(type != "Sealed" || name in schema.sealeds) { "Sealed requires a declared typeName, got $name" }
        require(type == "Model" || type == "Enum" || type == "Sealed" || LuxoCodegen.isScalar(type) ||
            type in schema.models || type in schema.types || type in schema.enums || type in schema.sealeds) {
            "undeclared type $type"
        }
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
        // Luxo Schema owns logical enum names; serialization describes the string wire value.
        out.appendLine("        override val descriptor = kotlin.String.serializer().descriptor")
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
