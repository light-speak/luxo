package com.luxo.client

import kotlinx.serialization.json.*
import java.io.File
import java.net.HttpURLConnection
import java.net.URI

/**
 * Generate typed Kotlin client from Luxo schema introspection.
 *
 * Usage:
 *   LuxoCodegen.generate(
 *     endpoint = "http://localhost:4000/luvia",
 *     key = "YOUR_KEY",
 *     outDir = "src/main/kotlin/com/example/luxo",
 *     packageName = "com.example.luxo",
 *   )
 */
object LuxoCodegen {

    private const val modelDepthParameter = "`\$modelDepth`"
    private fun depthFunction(name: String): String = "`$name\$depth`"

    private fun StringBuilder.modelDepthCheck(nonempty: String? = null, indent: String = "    ") {
        val budget = "$modelDepthParameter in 1..64"
        val condition = if (nonempty == null) budget else "$nonempty == 0 || $budget"
        appendLine("${indent}require($condition) { \"model nesting exceeds 64 levels\" }")
    }

    fun generate(
        endpoint: String,
        key: String,
        outDir: String,
        packageName: String = "com.luxo.generated",
        schemaFile: String? = null,
    ) {
        val connection = introspectionConnection(endpoint, key)
        val bytes = try {
            val status = connection.responseCode
            if (status != HttpURLConnection.HTTP_OK) {
                throw RuntimeException("HTTP $status")
            }
            connection.inputStream.use { it.readBytes() }
        } catch (e: Exception) {
            throw RuntimeException("luxo introspection failed: ${e.message}", e)
        } finally {
            connection.disconnect()
        }
        val body: String
        val schema = try {
            body = decodeJSONDocumentText(bytes)
            Json.decodeFromString<LuxoSchema>(requireJSONUnicode(body))
        } catch (e: Exception) {
            throw RuntimeException("luxo introspection returned invalid JSON: ${e.message}", e)
        }
        schema.validateIDs()
        KotlinEnumCodegen.validate(schema)
        val queries = KotlinQueryCodegen.generate(schema, packageName)
        if (schemaFile != null) {
            val snapshot = File(schemaFile)
            snapshot.parentFile?.mkdirs()
            snapshot.writeText(body)
        }

        File(outDir).mkdirs()
        File("$outDir/Types.kt").writeText(genTypes(schema, packageName))
        File("$outDir/Schema.kt").writeText(genSchemaMap(schema, packageName))
        File("$outDir/LuxoClient.kt").writeText(genClient(schema, packageName))
        File("$outDir/Queries.kt").writeText(queries)
        println("[luxo] Generated types -> $outDir/")
    }

    internal fun introspectionConnection(endpoint: String, key: String): HttpURLConnection {
        val base = URI(endpoint.substringBefore('#'))
        val separator = when (base.rawQuery) { null -> "?"; "" -> ""; else -> "&" }
        return (URI("$base$separator\$schema").toURL().openConnection() as HttpURLConnection).apply {
            instanceFollowRedirects = false
            requestMethod = "GET"
            setRequestProperty("X-Introspection-Key", key)
        }
    }

    internal fun luxoTypeToKt(type: String, schema: LuxoSchema): String {
        val base = when (type) {
            "Int", "Duration" -> "Long"
            "Float" -> "Double"
            "String", "DateTime", "UUID", "Decimal", "Enum" -> "String"
            "Boolean" -> "Boolean"
            "Bytes" -> "ByteArray"
            "JSON" -> "JsonElement"
            else -> return type
        }
        if (base !in schema.models && base !in schema.types && base !in schema.enums && base !in schema.sealeds) return base
        return if (base == "JsonElement") "kotlinx.serialization.json.JsonElement" else "kotlin.$base"
    }

    internal fun resolveFieldType(
        f: LuxoField,
        schema: LuxoSchema,
        usages: TypeUsages,
        input: Boolean = false,
    ): String {
        val tn = f.typeName ?: f.type
        if (schema.enums.containsKey(tn)) return if (f.isList) "List<$tn>" else tn
        if (schema.models.containsKey(tn) || schema.types.containsKey(tn) || schema.sealeds.containsKey(tn)) {
            val name = if (input) inputTypeName(tn, usages) else tn
            return if (f.isList) "List<$name>" else name
        }
        val base = luxoTypeToKt(f.type, schema)
        return if (f.isList) "List<$base>" else base
    }

    /** Kotlin type for an API param, wrapping list params in List<>. */
    private fun paramKtType(p: LuxoParam, schema: LuxoSchema, usages: TypeUsages): String {
        val typeName = p.typeName ?: p.type
        val base = if (
            schema.enums.containsKey(typeName) || schema.models.containsKey(typeName) || schema.types.containsKey(typeName) || schema.sealeds.containsKey(typeName)
        ) inputTypeName(typeName, usages) else luxoTypeToKt(p.type, schema)
        return if (p.isList) "List<$base>" else base
    }

    private fun hasInputUsage(usage: TypeUsage): Boolean =
        usage == TypeUsage.INPUT || usage == TypeUsage.INPUT_OUTPUT

    private fun hasOutputUsage(usage: TypeUsage): Boolean =
        usage == TypeUsage.OUTPUT || usage == TypeUsage.INPUT_OUTPUT

    internal class TypeUsages(
        states: Map<String, TypeUsage>,
        val inputAliases: Map<String, String>,
    ) : Map<String, TypeUsage> by states

    private fun planInputAliases(schema: LuxoSchema, states: Map<String, TypeUsage>): Map<String, String> {
        var collisions: MutableList<String>? = null
        for ((name, usage) in states) {
            if (usage != TypeUsage.INPUT_OUTPUT) continue
            val preferred = "${name}Input"
            if ((states[preferred] ?: TypeUsage.UNUSED) != TypeUsage.UNUSED || preferred in schema.enums) {
                if (collisions == null) collisions = mutableListOf()
                collisions.add(name)
            }
        }
        val names = collisions ?: return emptyMap()
        val reserved = schema.enums.keys.toMutableSet()
        for ((name, usage) in states) {
            if (usage != TypeUsage.UNUSED) reserved.add(name)
            if (usage == TypeUsage.INPUT_OUTPUT) reserved.add("${name}Input")
        }
        val aliases = mutableMapOf<String, String>()
        for (name in names.sorted()) {
            val fields = schema.models[name]?.fields ?: schema.types[name]?.fields ?: emptyList()
            val members = fields.mapTo(mutableSetOf()) { it.name }
            val base = "Luxo${name}Input"
            var candidate = base
            var ordinal = 2
            while (candidate in reserved || candidate in members) candidate = base + ordinal++
            reserved.add(candidate)
            aliases[name] = candidate
        }
        return aliases
    }

    internal fun inputTypeName(name: String, usages: TypeUsages): String =
        usages.inputAliases[name] ?: if (usages[name] == TypeUsage.INPUT_OUTPUT) "${name}Input" else name

    private fun mergeUsage(current: TypeUsage, next: TypeUsage): TypeUsage = when {
        current == TypeUsage.UNUSED -> next
        current == next || current == TypeUsage.INPUT_OUTPUT -> current
        else -> TypeUsage.INPUT_OUTPUT
    }

    /** Backward-compatible inference for schema snapshots without explicit usage metadata. */
    private fun inferTypeUsages(schema: LuxoSchema): TypeUsages {
        val fields = buildMap {
            schema.models.values.forEach { put(it.name, it.fields) }
            schema.types.values.forEach { put(it.name, it.fields) }
            schema.sealeds.values.forEach { owner -> put(owner.name, owner.variants.flatMap { it.fields }.map { LuxoField(it.id, it.name, it.type, typeName = it.typeName, isList = it.isList, nullable = it.nullable) }) }
        }
        val declared = buildMap {
            schema.models.values.forEach { put(it.name, it.usage) }
            schema.types.values.forEach { put(it.name, it.usage) }
            schema.sealeds.values.forEach { put(it.name, it.usage) }
        }
        val usages = fields.keys.associateWith { TypeUsage.UNUSED }.toMutableMap()
        val visited = mutableSetOf<String>()

        fun mark(name: String?, usage: TypeUsage) {
            if (name == null || name !in fields || !visited.add("${usage.name}:$name")) return
            usages[name] = mergeUsage(usages.getValue(name), usage)
            fields.getValue(name).forEach { mark(it.typeName ?: it.type, usage) }
        }

        schema.apis.values.forEach { api ->
            mark(api.returnType, TypeUsage.OUTPUT)
            api.params.forEach { mark(it.typeName ?: it.type, TypeUsage.INPUT) }
        }
        declared.forEach { (name, usage) ->
            if (usage == TypeUsage.INPUT || usage == TypeUsage.INPUT_OUTPUT) mark(name, TypeUsage.INPUT)
            if (usage == TypeUsage.OUTPUT || usage == TypeUsage.INPUT_OUTPUT) mark(name, TypeUsage.OUTPUT)
            if (usage == null && usages[name] == TypeUsage.UNUSED) mark(name, TypeUsage.OUTPUT)
        }
        return TypeUsages(usages, planInputAliases(schema, usages))
    }

    internal fun isScalar(type: String): Boolean = when (type) {
        "Int", "Float", "String", "Boolean", "DateTime", "Duration",
        "UUID", "Bytes", "Decimal", "JSON", "Enum" -> true
        else -> false
    }

    /** Element reader expression for a scalar type (used inside readArray). */
    private fun scalarElementRead(type: String, decoder: String = "dec", enumName: String? = null): String =
        if (enumName != null) "$enumName.fromValue($decoder.readString())" else when (type) {
        "Int", "Duration" -> "$decoder.readInt()"
        "Float" -> "$decoder.readFloat()"
        "Boolean" -> "$decoder.readBool()"
        "UUID" -> "$decoder.readUuid()"
        "DateTime" -> "$decoder.readDateTime()" // seconds/nanoseconds -> RFC3339 string
        "Bytes" -> "$decoder.readBytes()"
        "JSON" -> "decodeBoundedLuxoJSON($decoder.readString())"
        else -> "$decoder.readString()" // String, Decimal, Enum
    }

    private fun scalarArrayRead(type: String, decoder: String = "dec", enumName: String? = null): String =
        if (type == "DateTime") "$decoder.readDateTimeArray()"
        else "$decoder.readArray { ${scalarElementRead(type, decoder, enumName)} }"

    internal fun binaryRead(f: LuxoField): String {
        val n = f.nullable
        if (f.type == "Sealed") {
            val typeName = requireNotNull(f.typeName)
            val raw = if (f.isList) "dec.readArray { ${depthFunction("decode$typeName")}(LuxoDecoder(dec.readBytes()), $modelDepthParameter + 1) }"
                else "${depthFunction("decode$typeName")}(LuxoDecoder(dec.readBytes()), $modelDepthParameter + 1)"
            return if (n) "if (dec.readNullableMarker()) $raw else null" else raw
        }
        if (f.isList && n) return "if (dec.readBool()) ${binaryRead(f.copy(nullable = false))} else null"
        // Scalar arrays: [count][items...] — decode element-by-element.
        if (f.isList && isScalar(f.type)) {
            return scalarArrayRead(f.type, enumName = f.typeName.takeIf { f.type == "Enum" })
        }
        return when (f.type) {
            "Int", "Duration" -> if (n) "dec.readIntPtr()" else "dec.readInt()"
            "Float" -> if (n) "dec.readFloatPtr()" else "dec.readFloat()"
            "Boolean" -> if (n) "dec.readBoolPtr()" else "dec.readBool()"
            "UUID" -> if (n) "dec.readUuidPtr()" else "dec.readUuid()"
            "Bytes" -> if (n) "dec.readNullable { dec.readBytes() }" else "dec.readBytes()"
            "JSON" -> {
                val read = scalarElementRead("JSON")
                if (n) "dec.readNullable { $read }.takeUnless { it === JsonNull }" else read
            }
            // DateTime: exact seconds/nanoseconds on the wire -> RFC3339 string.
            "DateTime" -> if (n) "dec.readDateTimePtr()" else "dec.readDateTime()"
            "Enum" -> if (n) "dec.readStringPtr()?.let { ${f.typeName}.fromValue(it) }"
                else "${f.typeName}.fromValue(dec.readString())"
            "String", "Decimal" -> if (n) "dec.readStringPtr()" else "dec.readString()"
            else -> {
                // Responses carry independent, length-delimited row or columnar messages.
                val tn = f.typeName ?: f.type
                val decoded = if (f.isList) "${depthFunction("decodeColumnar$tn")}(ColumnarDecoder(dec.readBytes()), $modelDepthParameter + 1)"
                    else "${depthFunction("decode$tn")}(LuxoDecoder(dec.readBytes()), $modelDepthParameter + 1)"
                if (n) "if (dec.readNullableMarker()) $decoded else null" else decoded
            }
        }
    }

    internal fun genTypes(schema: LuxoSchema, pkg: String): String = buildString {
        KotlinEnumCodegen.validate(schema)
        val usages = inferTypeUsages(schema)
        appendLine("// Auto-generated by luxo. DO NOT EDIT.\n")
        appendLine("package $pkg\n")
        appendLine("import com.luxo.client.ColumnarDecoder")
		appendLine("import com.luxo.client.LuxoBinaryEncodable")
        appendLine("import com.luxo.client.LuxoDecoder")
        appendLine("import com.luxo.client.LuxoError")
		appendLine("import com.luxo.client.LuxoEncoder")
        appendLine("import com.luxo.client.Page")
        appendLine("import com.luxo.client.Selected")
        appendLine("import com.luxo.client.LuxoSelectionField")
        if (schema.sealeds.isNotEmpty()) {
            appendLine("import com.luxo.client.LuxoOptional")
            appendLine("import com.luxo.client.LuxoSealedType")
            appendLine("import com.luxo.client.LuxoSealedCase")
        }
		appendLine("import com.luxo.client.luxoUtf8Length")
        appendLine("import com.luxo.client.decodeLuxoUtf8")
        appendLine("import com.luxo.client.luxoJsonFloat")
        appendLine("import com.luxo.client.decodeLuxoJsonInt")
        appendLine("import com.luxo.client.decodeLuxoJsonFloat")
        appendLine("import com.luxo.client.decodeLuxoJsonString")
        appendLine("import com.luxo.client.DateTimeCodec")
        appendLine("import com.luxo.client.decodeLuxoJsonBoolean")
        appendLine("import com.luxo.client.decodeBoundedLuxoJSON")
        appendLine("import com.luxo.client.encodeBoundedLuxoJSON")
        appendLine("import com.luxo.client.decodeLuxoJsonValue")
        appendLine("import kotlinx.serialization.Serializable")
        if (schema.enums.isNotEmpty()) appendLine("import kotlinx.serialization.builtins.serializer")
        appendLine("import kotlinx.serialization.json.*\n")

        // Enums
        for (e in schema.enums.values) KotlinEnumCodegen.append(this, e)

        // Type declarations (non-DB)
        for (t in schema.types.values) {
            val usage = usages[t.name] ?: TypeUsage.UNUSED
            if (hasOutputUsage(usage)) {
                append(generateKotlinDeclaration(t.name, t.fields, schema, usages, output = true))
                append(generateKotlinDecoders(t.name, t.fields, schema, usages))
            }
            if (hasInputUsage(usage)) {
                append(generateKotlinDeclaration(inputTypeName(t.name, usages), t.fields, schema, usages, output = false))
            }
        }

        // Models
        for (m in schema.models.values) {
            val usage = usages[m.name] ?: TypeUsage.UNUSED
            if (hasOutputUsage(usage)) {
                append(generateKotlinDeclaration(m.name, m.fields, schema, usages, output = true))
                append(generateKotlinDecoders(m.name, m.fields, schema, usages))
            }
            if (hasInputUsage(usage)) {
                append(generateKotlinDeclaration(inputTypeName(m.name, usages), m.fields, schema, usages, output = false))
            }
        }
        for (owner in schema.sealeds.values) append(KotlinSealedCodegen.generate(owner, schema, usages))
        append(generateKotlinJSONArrays(schema, usages))
    }

    private fun generateKotlinDeclaration(
        name: String,
        fields: List<LuxoField>,
        schema: LuxoSchema,
        usages: TypeUsages,
        output: Boolean,
    ): String = buildString {
        if (!output) appendLine("@Serializable")
        appendLine("data class $name(")
        for ((index, field) in fields.withIndex()) {
            val base = resolveFieldType(field, schema, usages, input = !output)
            val value = if (field.nullable) "$base?" else base
            val type = if (output) "Selected<$value> = Selected.unselected()" else if (field.nullable) "$value = null" else value
            val comma = if (index < fields.lastIndex) "," else ""
            if (output) appendLine("    @get:LuxoSelectionField(\"${field.name}\")")
            appendLine("    val ${field.name}: $type$comma")
        }
		if (output) {
			appendLine(")\n")
		} else {
			appendLine(") : LuxoBinaryEncodable {")
			append(generateKotlinBinaryEncoder(fields, schema))
            append(generateKotlinJSONEncoder(name, fields, schema))
			appendLine("}\n")
		}
    }

	private fun generateKotlinBinaryEncoder(fields: List<LuxoField>, schema: LuxoSchema): String = buildString {
		val arenaTerms = fields.filter { !it.isList && (it.type == "String" || it.type == "Enum") }.map {
			val value = kotlinBinaryFieldReference(it)
			val raw = if (it.type == "Enum") "$value${if (it.nullable) "?." else "."}rawValue" else value
			if (it.nullable) "($raw?.let(::luxoUtf8Length) ?: 0)" else "luxoUtf8Length($raw)"
		}
		appendLine("    override fun writeLuxo(encoder: LuxoEncoder) {")
        appendLine("        ${depthFunction("writeLuxo")}(encoder, 1)")
        appendLine("    }")
        appendLine("    internal fun ${depthFunction("writeLuxo")}(encoder: LuxoEncoder, $modelDepthParameter: Int) {")
        modelDepthCheck(indent = "        ")
		val arenaLength = if (arenaTerms.isEmpty()) "0" else arenaTerms.joinToString(" + ")
		appendLine("        encoder.writeVarint(($arenaLength).toLong())")
		for (field in fields) writeKotlinBinaryField(this, field, schema)
		appendLine("        encoder.writeEnd()")
		appendLine("    }")
	}

    private fun generateKotlinJSONEncoder(name: String, fields: List<LuxoField>, schema: LuxoSchema): String = buildString {
        appendLine()
        appendLine("    override fun toLuxoJson(): JsonElement = ${depthFunction("toLuxoJson")}(1)")
        appendLine("    internal fun ${depthFunction("toLuxoJson")}($modelDepthParameter: Int): JsonElement {")
        modelDepthCheck(indent = "        ")
        appendLine("        return buildJsonObject {")
        for (field in fields) {
            val value = "this@$name.${field.name}"
            val expression = if (field.nullable) {
                "$value?.let { value -> ${kotlinJSONValue(field, "value", schema)} } ?: JsonNull"
            } else kotlinJSONValue(field, value, schema)
            appendLine("            put(\"${field.name}\", $expression)")
        }
        appendLine("        }")
        appendLine("    }")
    }

    internal fun kotlinJSONValue(field: LuxoField, value: String, schema: LuxoSchema): String =
        if (field.isList) "JsonArray($value.map { item -> ${kotlinJSONScalar(field, "item", schema)} })"
        else kotlinJSONScalar(field, value, schema)

    private fun kotlinJSONScalar(field: LuxoField, value: String, schema: LuxoSchema): String {
        if (field.type == "Sealed" || isNested(field, schema)) return "$value.${depthFunction("toLuxoJson")}($modelDepthParameter + 1)"
        return when (field.type) {
            "JSON" -> "decodeLuxoJsonValue($value)"
            "Bytes" -> "JsonPrimitive(java.util.Base64.getEncoder().encodeToString($value))"
            "Float" -> "luxoJsonFloat($value)"
            "Enum" -> "JsonPrimitive($value.rawValue)"
            "Int", "Duration", "String", "Boolean", "DateTime", "UUID", "Decimal" -> "JsonPrimitive($value)"
            else -> error("unsupported structured field type ${field.type}")
        }
    }

	private fun kotlinBinaryFieldReference(field: LuxoField): String =
		"this.${field.name}"

    internal fun kotlinNullCondition(field: LuxoField, value: String): String =
        if (field.type == "JSON" && !field.isList) "$value == null || $value === JsonNull" else "$value == null"

	private fun writeKotlinBinaryField(out: StringBuilder, field: LuxoField, schema: LuxoSchema) {
		val value = kotlinBinaryFieldReference(field)
		out.appendLine("        encoder.writeVarint(${field.id}L)")
		if (!field.nullable) {
			writeKotlinBinaryValue(out, field, value, schema, "        ")
			return
		}
		val local = "_${field.name}Value"
		out.appendLine("        val $local = $value")
		out.appendLine("        if (${kotlinNullCondition(field, local)}) {")
		out.appendLine("            encoder.writeBool(false)")
		out.appendLine("        } else {")
		out.appendLine("            encoder.writeBool(true)")
		writeKotlinBinaryValue(out, field, local, schema, "            ")
		out.appendLine("        }")
	}

	internal fun writeKotlinBinaryValue(
		out: StringBuilder,
		field: LuxoField,
		value: String,
		schema: LuxoSchema,
		indent: String,
	) {
		if (!field.isList) {
			writeKotlinBinaryScalar(out, field, value, schema, indent)
			return
		}
		out.appendLine("${indent}encoder.writeArrayHeader($value.size)")
		out.appendLine("${indent}for (item in $value) {")
		writeKotlinBinaryScalar(out, field, "item", schema, "$indent    ")
		out.appendLine("$indent}")
	}

	private fun writeKotlinBinaryScalar(
		out: StringBuilder,
		field: LuxoField,
		value: String,
		schema: LuxoSchema,
		indent: String,
	) {
        if (field.type == "Sealed") {
            out.appendLine("${indent}encoder.writeDelimitedValue($value) { target, sum -> sum.${depthFunction("writeLuxo")}(target, $modelDepthParameter + 1) }")
            return
        }
		if (isNested(field, schema)) {
			out.appendLine("${indent}$value.${depthFunction("writeLuxo")}(encoder, $modelDepthParameter + 1)")
			return
		}
		val statement = when (field.type) {
			"Int", "Duration" -> "encoder.writeSvarint($value)"
			"Float" -> "encoder.writeFixed64($value)"
			"Enum" -> "encoder.writeString($value.rawValue)"
			"String", "Decimal" -> "encoder.writeString($value)"
			"Boolean" -> "encoder.writeBool($value)"
			"DateTime" -> "encoder.writeDateTime($value)"
			"UUID" -> "encoder.writeUuid($value)"
			"Bytes" -> "encoder.writeBytes($value)"
			"JSON" -> "encoder.writeBytes(encodeBoundedLuxoJSON($value).toByteArray(Charsets.UTF_8))"
			else -> error("unsupported structured field type ${field.type}")
		}
		out.appendLine("$indent$statement")
	}

    private fun generateKotlinDecoders(
        name: String,
        fields: List<LuxoField>,
        schema: LuxoSchema,
        usages: TypeUsages,
    ): String = buildString {
        appendLine("fun decode$name(dec: LuxoDecoder): $name {")
        appendLine("    return ${depthFunction("decode$name")}(dec, 1)")
        appendLine("}\n")
        appendLine("internal fun ${depthFunction("decode$name")}(dec: LuxoDecoder, $modelDepthParameter: Int): $name {")
        modelDepthCheck()
        for (f in fields) {
            val base = resolveFieldType(f, schema, usages)
            val value = if (f.nullable) "$base?" else base
            appendLine("    var _${f.name}: Selected<$value> = Selected.unselected()")
        }
        appendLine("    dec.skipArenaHeader()")
        appendLine("    while (dec.nextField()) {")
        appendLine("        when (dec.fieldID) {")
        for (f in fields) appendLine("            ${f.id} -> _${f.name} = Selected.value(${binaryRead(f)})")
        appendLine("            else -> error(\"unknown $name field ID \${dec.fieldID}\")")
        appendLine("        }")
        appendLine("    }")
        appendLine("    dec.checkComplete()")
        appendLine("    return $name(")
        for (f in fields) {
            appendLine("        ${f.name} = _${f.name},")
        }
        appendLine("    )")
        appendLine("}\n")
        append(generateKotlinJSONDecoder(name, fields, schema, usages))
        append(generateKotlinColumnarDecoder(name, fields, schema, usages, false))
        append(generateKotlinColumnarDecoder(name, fields, schema, usages, true))
    }

    private fun generateKotlinJSONDecoder(
        name: String,
        fields: List<LuxoField>,
        schema: LuxoSchema,
        usages: TypeUsages,
    ): String = buildString {
        appendLine("fun decodeJSON$name(data: JsonObject): $name {")
        appendLine("    return ${depthFunction("decodeJSON$name")}(data, 1)")
        appendLine("}\n")
        appendLine("internal fun ${depthFunction("decodeJSON$name")}(data: JsonObject, $modelDepthParameter: Int): $name {")
        modelDepthCheck()
        for (field in fields) {
            val base = resolveFieldType(field, schema, usages)
            val type = if (field.nullable) "$base?" else base
            val raw = "`${field.name}\$json`"
            val decoded = kotlinJSONValue(field, schema, "value")
            val selectedValue = if (field.nullable) {
                "$raw.takeUnless { it is JsonNull }?.let { value -> $decoded }"
            } else {
                "$raw.let { value -> $decoded }"
            }
            // JSON null is JsonNull, so Kotlin null unambiguously denotes an absent key.
            appendLine("    val $raw = data[\"${field.name}\"]")
            appendLine("    val _${field.name}: Selected<$type> = if ($raw != null) {")
            appendLine("        Selected.value($selectedValue)")
            appendLine("    } else Selected.unselected()")
        }
        appendLine("    return $name(")
        for (field in fields) appendLine("        ${field.name} = _${field.name},")
        appendLine("    )")
        appendLine("}\n")
        // Check statically known Selected states, never look up the JSON fields a second time.
        // '$' cannot occur in DSL identifiers; keep the public unary function reference unambiguous.
        appendLine("internal fun `decodeJSON$name\$item`(data: JsonObject): $name {")
        appendLine("    return ${depthFunction("decodeJSON$name\$item")}(data, 1)")
        appendLine("}\n")
        appendLine("internal fun ${depthFunction("decodeJSON$name\$item")}(data: JsonObject, $modelDepthParameter: Int): $name {")
        appendLine("    val value = ${depthFunction("decodeJSON$name")}(data, $modelDepthParameter)")
        val missing = fields.joinToString(" && ") { "!value.${it.name}.isSelected" }.ifEmpty { "true" }
        appendLine("    if ($missing) throw LuxoError(\"ParseError\", 0, \"$name list item must contain a selected schema field\")")
        appendLine("    return value")
        appendLine("}\n")
    }

    internal fun kotlinJSONValue(field: LuxoField, schema: LuxoSchema, value: String): String {
        val typeName = field.typeName ?: field.type
        if (field.type == "Sealed") {
            val read = "${depthFunction("decodeJSON$typeName")}(value.jsonObject, $modelDepthParameter + 1)"
            return if (field.isList) "$value.jsonArray.map { value -> $read }" else read.replace("value.jsonObject", "$value.jsonObject")
        }
        if (field.isList) return if (isNested(field, schema))
            "${depthFunction("decodeJSONArray\$$typeName")}($value.jsonArray, $modelDepthParameter + 1)"
            else "${kotlinJSONArrayName(field)}($value.jsonArray)"
        return if (isNested(field, schema)) "${depthFunction("decodeJSON$typeName")}($value.jsonObject, $modelDepthParameter + 1)"
            else scalarJSONRead(field.type, value, field.typeName.takeIf { field.type == "Enum" })
    }

    private fun kotlinJSONArrayName(field: LuxoField): String =
        "`decodeJSONArray\$${field.typeName ?: field.type}`"

    /** Keep array loops small and shared across all output fields with the same element type. */
    private fun generateKotlinJSONArrays(schema: LuxoSchema, usages: TypeUsages): String = buildString {
        val declarations = schema.models.values.map { it.name to it.fields } + schema.types.values.map { it.name to it.fields }
        val allDeclarations = if (schema.sealeds.isEmpty()) declarations else declarations +
            schema.sealeds.values.map { owner -> owner.name to owner.variants.flatMap { variant -> variant.fields.map { LuxoField(it.id, it.name, it.type, typeName = it.typeName, nullable = it.nullable, isList = it.isList) } } }
        val outputFields = allDeclarations.filter { hasOutputUsage(usages[it.first] ?: TypeUsage.UNUSED) }
            .flatMap { it.second }.filter { it.isList && it.type != "Sealed" }
        val returns = schema.apis.values.filter { isStructuredReturn(it, schema) && it.returnType !in schema.sealeds && (it.returnList || it.paginated) }
            .map { LuxoField(0, "", "Model", typeName = it.returnType, isList = true) }
        val fields = (outputFields + returns).distinctBy(::kotlinJSONArrayName)
        for (field in fields) {
            val typeName = field.typeName ?: field.type
            val nested = isNested(field, schema)
            val read = if (nested) "${depthFunction("decodeJSON$typeName\$item")}(item.jsonObject, $modelDepthParameter)"
                else scalarJSONRead(field.type, "item", field.typeName.takeIf { field.type == "Enum" })
            val type = resolveFieldType(field, schema, usages)
            appendLine("internal fun ${kotlinJSONArrayName(field)}(data: JsonArray): $type =")
            if (nested) {
                val function = depthFunction("decodeJSONArray\$$typeName")
                appendLine("    $function(data, 1)\n")
                appendLine("internal fun $function(data: JsonArray, $modelDepthParameter: Int): $type =")
            }
            appendLine("    data.map { item -> $read }\n")
        }
    }

    private fun generateKotlinColumnarDecoder(
        name: String,
        fields: List<LuxoField>,
        schema: LuxoSchema,
        usages: TypeUsages,
        paginated: Boolean,
    ): String = buildString {
        val function = if (paginated) "decodePaginated$name" else "decodeColumnar$name"
        val result = if (paginated) "Page<$name>" else "List<$name>"
        appendLine("fun $function(data: ByteArray): $result = $function(ColumnarDecoder(data))\n")
        appendLine("fun $function(dec: ColumnarDecoder): $result {")
        appendLine("    return ${depthFunction(function)}(dec, 1)")
        appendLine("}\n")
        appendLine("internal fun ${depthFunction(function)}(dec: ColumnarDecoder, $modelDepthParameter: Int): $result {")
        modelDepthCheck(nonempty = "dec.count")
        for (f in fields) appendLine("    var _${f.name}: ${kotlinColumnType(f, schema, usages)}? = null")
        appendLine("    while (dec.nextColumn()) {")
        appendLine("        when (dec.fieldID) {")
        for (f in fields) writeKotlinColumnRead(f, schema)
        appendLine("            else -> error(\"unknown $name column ID \${dec.fieldID}\")")
        appendLine("        }")
        appendLine("    }")
        if (paginated) {
            for (metadata in listOf("total", "page", "pageSize")) {
                appendLine("    val $metadata = dec.readSvarint()")
            }
        }
        appendLine("    dec.checkComplete()")
        appendLine("    val items = List(dec.count) { i -> $name(")
        for (f in fields) appendLine("        ${f.name} = ${kotlinColumnValue(f, schema, usages)},")
        appendLine("    ) }")
        if (paginated) {
            appendLine("    return Page(items, total, page, pageSize)")
        } else {
            appendLine("    return items")
        }
        appendLine("}\n")
    }

    private fun StringBuilder.writeKotlinColumnRead(field: LuxoField, schema: LuxoSchema) {
        val column = "_${field.name}"
        val read = kotlinColumnRead(field, schema)
        if (field.type != "Sealed" && !isNested(field, schema) && !field.isList && field.type != "JSON") {
            appendLine("            ${field.id} -> $column = $read")
            return
        }
        appendLine("            ${field.id} -> {")
        appendLine("                if ($column != null) for (i in $column.indices) {")
        appendLine("                    ${kotlinColumnElementValue(field, schema)}")
        appendLine("                }")
        appendLine("                $column = $read")
        appendLine("            }")
    }

    private fun isNested(f: LuxoField, schema: LuxoSchema): Boolean {
        val name = f.typeName ?: f.type
        return f.relation || schema.models.containsKey(name) || schema.types.containsKey(name)
    }

    private fun kotlinColumnType(f: LuxoField, schema: LuxoSchema, usages: TypeUsages): String {
        if (f.type == "Sealed" || isNested(f, schema) || f.isList || f.type == "Bytes" || f.type == "JSON") {
            return if (f.nullable) "List<ByteArray?>" else "List<ByteArray>"
        }
        val base = if (f.type == "Enum") "String" else resolveFieldType(f, schema, usages)
        return if (f.nullable) "List<$base?>" else "List<$base>"
    }

    private fun kotlinColumnRead(f: LuxoField, schema: LuxoSchema): String {
        if (f.type == "Sealed" || isNested(f, schema) || f.isList || f.type == "Bytes" || f.type == "JSON") {
            return if (f.nullable) "dec.readColumnBytesPtr()" else "dec.readColumnBytes()"
        }
        return when (f.type) {
            "Int", "Duration" -> if (f.nullable) "dec.readColumnIntPtr()" else "dec.readColumnInt()"
            "Float" -> if (f.nullable) "dec.readColumnFloatPtr()" else "dec.readColumnFloat()"
            "Boolean" -> if (f.nullable) "dec.readColumnBoolPtr()" else "dec.readColumnBool()"
            "DateTime" -> if (f.nullable) "dec.readColumnDateTimePtr()" else "dec.readColumnDateTime()"
            "UUID" -> if (f.nullable) "dec.readColumnUuidPtr()" else "dec.readColumnUuid()"
            else -> if (f.nullable) "dec.readColumnStringPtr()" else "dec.readColumnString()"
        }
    }

    private fun kotlinColumnValue(f: LuxoField, schema: LuxoSchema, usages: TypeUsages): String {
        val column = "_${f.name}"
        val base = resolveFieldType(f, schema, usages)
        val valueType = if (f.nullable) "$base?" else base
        val absent = "Selected.unselected<$valueType>()"
        return "if ($column == null) $absent else Selected.value<$valueType>(${kotlinColumnElementValue(f, schema)})"
    }

    private fun kotlinColumnElementValue(f: LuxoField, schema: LuxoSchema): String {
        val column = "_${f.name}"
        val typeName = f.typeName ?: f.type
        val value: String
        if (f.type == "Sealed") {
            val raw = if (f.isList) "${depthFunction("decodeSealedList$typeName")}(LuxoDecoder(data), $modelDepthParameter + 1)"
                else "${depthFunction("decode$typeName")}(LuxoDecoder(data), $modelDepthParameter + 1)"
            value = if (f.nullable) "$column[i]?.let { data -> $raw }" else raw.replace("LuxoDecoder(data)", "LuxoDecoder($column[i])")
        } else if (isNested(f, schema)) {
            value = if (f.isList) {
                if (f.nullable) "$column[i]?.let { ${depthFunction("decodeColumnar$typeName")}(ColumnarDecoder(it), $modelDepthParameter + 1) }"
                else "${depthFunction("decodeColumnar$typeName")}(ColumnarDecoder($column[i]), $modelDepthParameter + 1)"
            } else if (f.nullable) {
                "$column[i]?.let { ${depthFunction("decode$typeName")}(LuxoDecoder(it), $modelDepthParameter + 1) }"
            } else {
                "${depthFunction("decode$typeName")}(LuxoDecoder($column[i]), $modelDepthParameter + 1)"
            }
        } else if (f.isList) {
            val data = if (f.nullable) "data" else "$column[i]"
            val decoded = completeScalarRead(data, scalarArrayRead(f.type, "cell", f.typeName.takeIf { f.type == "Enum" }), "cell")
            value = if (f.nullable) "$column[i]?.let { data -> $decoded }" else decoded
        } else if (f.type == "Enum") {
            value = if (f.nullable) "$column[i]?.let { $typeName.fromValue(it) }" else "$typeName.fromValue($column[i])"
        } else if (f.type == "JSON") {
            value = if (f.nullable) {
                "$column[i]?.let { decodeBoundedLuxoJSON(decodeLuxoUtf8(it)) }.takeUnless { it === JsonNull }"
            } else {
                "decodeBoundedLuxoJSON(decodeLuxoUtf8($column[i]))"
            }
        } else {
            value = "$column[i]"
        }
        return value
    }

	internal fun genSchemaMap(schema: LuxoSchema, pkg: String): String = buildString {
        appendLine("// Auto-generated by luxo. DO NOT EDIT.\n")
        appendLine("package $pkg\n")
        appendLine("import com.luxo.client.APISchemaEntry")
        appendLine("import com.luxo.client.ParamSchema")
        appendLine("import com.luxo.client.SelectionFieldSchema\n")
        appendLine("private val LUXO_SELECTION_TYPES: Map<String, Map<String, SelectionFieldSchema>> = mapOf(")
        val selectionTypes = schema.types.values.map { it.name to it.fields } +
            schema.models.values.map { it.name to it.fields } + KotlinSealedCodegen.selectionTypes(schema)
        for ((name, fields) in selectionTypes.sortedBy { it.first }) {
            append("    \"$name\" to mapOf(")
            append(fields.joinToString(", ") { field ->
                val typeName = field.typeName ?: field.type
				val declaredType = if (field.typeName != null) ", \"$typeName\"" else ""
				"\"${field.name}\" to SelectionFieldSchema(${field.id}$declaredType, type = \"${field.type}\", isList = ${field.isList}, nullable = ${field.nullable})"
            })
            appendLine("),")
        }
        appendLine(")\n")
        appendLine("/** API schema map — used by binary transport for encoding requests. */")
        appendLine("val LUXO_SCHEMA: Map<String, APISchemaEntry> = mapOf(")
        for (api in schema.apis.values) {
            if (api.isInternal) continue
            append("    \"${api.name}\" to APISchemaEntry(${api.id}")
            val returnFields = api.returnType?.let { name ->
                schema.models[name]?.fields ?: schema.types[name]?.fields ?: KotlinSealedCodegen.caseFields(schema.sealeds[name])
            }.orEmpty()
			val responseSealed = KotlinSealedCodegen.hasResponseSealed(api.returnType, schema)
            val needsTypes = responseSealed || returnFields.isNotEmpty() || api.params.any { it.type == "Model" || it.type == "Sealed" }
            if (api.params.isNotEmpty() || returnFields.isNotEmpty() || needsTypes) {
                appendLine(", listOf(")
                for (p in api.params) {
					val typeName = p.typeName?.let { ", typeName = \"$it\"" }.orEmpty()
					appendLine("        ParamSchema(${p.id}, \"${p.name}\", \"${p.type}\", isList = ${p.isList}, nullable = ${p.nullable}$typeName),")
                }
                append("    )")
            }
			if (returnFields.isNotEmpty()) {
				append(", LUXO_SELECTION_TYPES.getValue(\"${api.returnType}\")")
			} else if (needsTypes) {
				append(", emptyMap()")
			}
			if (needsTypes) {
				append(", LUXO_SELECTION_TYPES")
            }
            if (responseSealed) {
                val kind = if (api.returnType in schema.sealeds) "Sealed" else "Model"
                append(", responseType = SelectionFieldSchema(0, \"${api.returnType}\", type = \"$kind\", isList = ${api.returnList}, nullable = ${api.returnNullable})")
            }
            appendLine("),")
        }
        appendLine(")")
    }

    internal fun genClient(schema: LuxoSchema, pkg: String): String = buildString {
        KotlinEnumCodegen.validate(schema)
        val usages = inferTypeUsages(schema)
        appendLine("// Auto-generated by luxo. DO NOT EDIT.\n")
        appendLine("package $pkg\n")
        appendLine("import com.luxo.client.*")
        appendLine("import kotlinx.serialization.json.*\n")
        appendLine("class LuxoClient(val transport: Transport) {\n")
        appendLine("    init { transport.setSchema(LUXO_SCHEMA) }\n")
        appendLine("    companion object {")
        appendLine("        val schema = LUXO_SCHEMA")
        appendLine("        /** Create client from URL — auto-selects HTTP or WebSocket transport. */")
        appendLine("        fun create(endpoint: String, mode: TransportMode = TransportMode.JSON, token: String? = null): LuxoClient {")
        appendLine("            val transport: Transport = if (endpoint.startsWith(\"ws://\") || endpoint.startsWith(\"wss://\")) {")
        appendLine("                LuxoWebSocket(endpoint, token = token).also {")
        appendLine("                    it.setMode(mode)")
        appendLine("                    it.setSchema(LUXO_SCHEMA)")
        appendLine("                    it.connect()")
        appendLine("                }")
        appendLine("            } else {")
        appendLine("                OkHttpTransport(endpoint, token = token, mode = mode, schema = LUXO_SCHEMA)")
        appendLine("            }")
        appendLine("            return LuxoClient(transport)")
        appendLine("        }")
        appendLine("    }\n")
        appendLine("    fun setMode(mode: TransportMode) = transport.setMode(mode)")
        appendLine("    fun setToken(token: String) = transport.setToken(token)")

        for (api in schema.apis.values) {
            if (api.isInternal) continue
            genMethod(this, api, schema, usages)
        }

        appendLine("}")
        for (api in schema.apis.values.filterNot { it.isInternal }) {
            append(KotlinRequestCodegen.generate(api, api.paginated || (isStructuredReturn(api, schema) && schema.sealeds[api.returnType]?.variants?.isEmpty() != true),
                { paramKtType(it, schema, usages) },
                { out, field, value ->
                    if (field.type == "Sealed") out.appendLine("        encoder.writeDelimitedValue($value) { target, sum -> sum.writeLuxo(target) }")
                    else writeKotlinBinaryScalar(out, field, value, schema, "        ")
                }))
        }
    }

    private fun genMethod(
        b: StringBuilder, api: LuxoAPI, schema: LuxoSchema, usages: TypeUsages,
    ) {
        val selection = api.paginated || (isStructuredReturn(api, schema) && schema.sealeds[api.returnType]?.variants?.isEmpty() != true)
        val types: (LuxoParam) -> String = { paramKtType(it, schema, usages) }
        val declarations = KotlinRequestCodegen.declarations(api, types, selection).toMutableList()
        val ret = returnType(api, schema)
        if (selection) b.appendLine("    @LuxoSelection")
        if (api.stream) {
            declarations += "observer: StreamObserver? = null"
            declarations += "onData: ($ret) -> kotlin.Unit"
            val method = "subscribe" + api.name.replaceFirstChar { it.uppercase() }
            b.appendLine("    suspend fun $method(${declarations.joinToString(", ")}): () -> kotlin.Unit {")
            b.appendLine("        return transport.subscribe(\"${api.name}\", { data -> decode_${api.name}(data) }, ${KotlinRequestCodegen.invocation(api, selection, "select ?: `\$selectionPlan`?.text")}, observer, onData)")
        } else {
            b.appendLine("    suspend fun ${api.name}(${declarations.joinToString(", ")}): $ret {")
            if (selection) b.appendLine("        val sel = select ?: `\$selectionPlan`?.text")
            b.appendLine("        val data = transport.call(\"${api.name}\", ${KotlinRequestCodegen.invocation(api, selection, "sel")})")
            b.appendLine("        return decode_${api.name}(data)")
        }
        b.appendLine("    }\n")
        genDecodeHelper(b, api, schema)
    }

    private fun isStructuredReturn(api: LuxoAPI, schema: LuxoSchema): Boolean =
        api.returnType?.let { !isScalar(it) && it !in schema.enums } == true

    private fun genDecodeHelper(b: StringBuilder, api: LuxoAPI, schema: LuxoSchema) {
        val ret = returnType(api, schema)
        if (api.returnNullable) {
            b.appendLine("    private fun decode_${api.name}(data: Any): $ret {")
            b.appendLine("        if (data === JsonNull || (data is ByteArray && isNullRoot(data))) return null")
            b.appendLine("        return when (data) {")
        } else {
            b.appendLine("    private fun decode_${api.name}(data: Any): $ret = when (data) {")
        }
        if (api.returnType in schema.sealeds) genSealedResponse(b, api)
        else if (isStructuredReturn(api, schema)) genModelResponse(b, api) else genScalarResponse(b, api, schema)
        if (api.returnNullable) b.appendLine("        }")
        b.appendLine("    }\n")
    }

    private fun genSealedResponse(b: StringBuilder, api: LuxoAPI) {
        val type = api.returnType
        val decoder = if (api.returnNullable) "LuxoDecoder.atOffset(data, 1)" else "LuxoDecoder(data)"
        val read = if (api.returnList) "${depthFunction("decodeSealedList$type")}($decoder, 1)" else "decode$type($decoder)"
        val json = if (api.returnList) "${depthFunction("decodeJSONArray\$$type")}(data as JsonArray, 1)"
            else "decodeJSON$type((data as JsonElement).jsonObject)"
        b.appendLine("        is ByteArray -> $read")
        b.appendLine("        else -> $json")
    }

    private fun genScalarResponse(b: StringBuilder, api: LuxoAPI, schema: LuxoSchema) {
        val type = api.returnType ?: "Int"
        val enumName = type.takeIf { it in schema.enums }
        val element = scalarElementRead(type, enumName = enumName)
        val binaryRead = if (api.returnList) scalarArrayRead(type, enumName = enumName) else element
        val normalizedRead = if (type == "JSON" && api.returnNullable && !api.returnList)
            "$binaryRead.takeUnless { it === JsonNull }" else binaryRead
        val jsonRead = if (api.returnList) {
            "(data as JsonArray).map { ${scalarJSONRead(type, "it", enumName)} }"
        } else scalarJSONRead(type, "(data as JsonElement)", enumName)
        b.appendLine("        is ByteArray -> ${completeScalarRead("data", normalizedRead, nullableRoot = api.returnNullable)}")
        b.appendLine("        else -> $jsonRead")
    }

    private fun scalarJSONRead(type: String, value: String, enumName: String? = null): String =
        if (enumName != null) "$enumName.fromValue(decodeLuxoJsonString($value))" else when (type) {
        "Int", "Duration" -> "decodeLuxoJsonInt($value)"
        "Float" -> "decodeLuxoJsonFloat($value)"
        "Boolean" -> "decodeLuxoJsonBoolean($value)"
        "Bytes" -> "java.util.Base64.getDecoder().decode(decodeLuxoJsonString($value))"
        "JSON" -> "decodeLuxoJsonValue($value)"
        "DateTime" -> "DateTimeCodec.validate(decodeLuxoJsonString($value))"
        else -> "decodeLuxoJsonString($value)"
    }

    // Kotlin's let is inline: no decoder callback object or extra runtime dispatch.
    private fun completeScalarRead(bytes: String, read: String, decoder: String = "dec", nullableRoot: Boolean = false): String {
        val constructor = if (nullableRoot) "LuxoDecoder.atOffset($bytes, 1)" else "LuxoDecoder($bytes)"
        return "$constructor.let { $decoder -> val value = $read; $decoder.checkComplete(); value }"
    }

    private fun genModelResponse(b: StringBuilder, api: LuxoAPI) {
        val t = api.returnType
        val jsonDecode = when {
            api.paginated -> "(data as JsonElement).jsonObject.let { obj -> Page(`decodeJSONArray\$$t`(obj.getValue(\"items\").jsonArray), decodeLuxoJsonInt(obj.getValue(\"total\")), decodeLuxoJsonInt(obj.getValue(\"page\")), decodeLuxoJsonInt(obj.getValue(\"pageSize\"))) }"
            api.returnList -> "`decodeJSONArray\$$t`(data as JsonArray)"
            else -> "decodeJSON$t((data as JsonElement).jsonObject)"
        }
        val columnInput = if (api.returnNullable) "ColumnarDecoder.atOffset(data, 1)" else "data"
        val rowInput = if (api.returnNullable) "LuxoDecoder.atOffset(data, 1)" else "LuxoDecoder(data)"
        val binaryDecode = when {
            api.paginated -> "decodePaginated$t($columnInput)"
            api.returnList -> "decodeColumnar$t($columnInput)"
            else -> "decode$t($rowInput)"
        }
        b.appendLine("        is ByteArray -> $binaryDecode")
        b.appendLine("        else -> $jsonDecode")
    }

    private fun returnType(api: LuxoAPI, schema: LuxoSchema): String {
        val t = luxoTypeToKt(api.returnType ?: "Int", schema)
        val result = when {
            api.paginated -> "Page<$t>"
            api.returnList -> "List<$t>"
            else -> t
        }
        return if (api.returnNullable) "$result?" else result
    }
}

/** CLI entry point for gradle JavaExec task. */
object LuxoCodegenCli {
    @JvmStatic
    fun main(args: Array<String>) {
        require(args.isNotEmpty()) { "Usage: LuxoCodegenCli <endpoint> [outDir] [package] [schemaFile]" }
        val endpoint = args[0]
        val key = System.getenv("LUXO_INTROSPECTION_KEY").orEmpty()
        val outDir = args.getOrElse(1) { "src/main/kotlin/com/luxo/generated" }
        val pkg = args.getOrElse(2) { "com.luxo.generated" }
        val schemaFile = args.getOrNull(3)
        LuxoCodegen.generate(endpoint, key, outDir, pkg, schemaFile)
    }
}
