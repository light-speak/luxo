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
        return (URI("$endpoint?\$schema").toURL().openConnection() as HttpURLConnection).apply {
            requestMethod = "GET"
            setRequestProperty("X-Introspection-Key", key)
        }
    }

    private fun luxoTypeToKt(type: String): String = when (type) {
        "Int", "Duration" -> "Long"
        "Float" -> "Double"
        "String", "DateTime", "UUID", "Decimal", "Enum" -> "String"
        "Boolean" -> "Boolean"
        "Bytes" -> "ByteArray"
        "JSON" -> "JsonElement"
        else -> type
    }

    private fun resolveFieldType(
        f: LuxoField,
        schema: LuxoSchema,
        usages: Map<String, TypeUsage>,
        input: Boolean = false,
    ): String {
        val tn = f.typeName ?: f.type
        if (schema.enums.containsKey(tn)) return if (f.isList) "List<$tn>" else tn
        if (schema.models.containsKey(tn) || schema.types.containsKey(tn)) {
            val name = if (input) inputTypeName(tn, usages) else tn
            return if (f.isList) "List<$name>" else name
        }
        val base = luxoTypeToKt(f.type)
        return if (f.isList) "List<$base>" else base
    }

    /** Kotlin type for an API param, wrapping list params in List<>. */
    private fun paramKtType(p: LuxoParam, schema: LuxoSchema, usages: Map<String, TypeUsage>): String {
        val typeName = p.typeName ?: p.type
        val base = if (
            schema.enums.containsKey(typeName) || schema.models.containsKey(typeName) || schema.types.containsKey(typeName)
        ) inputTypeName(typeName, usages) else luxoTypeToKt(p.type)
        return if (p.isList) "List<$base>" else base
    }

    private fun hasInputUsage(usage: TypeUsage): Boolean =
        usage == TypeUsage.INPUT || usage == TypeUsage.INPUT_OUTPUT

    private fun hasOutputUsage(usage: TypeUsage): Boolean =
        usage == TypeUsage.OUTPUT || usage == TypeUsage.INPUT_OUTPUT

    private fun inputTypeName(name: String, usages: Map<String, TypeUsage>): String =
        if (usages[name] == TypeUsage.INPUT_OUTPUT) "${name}Input" else name

    private fun mergeUsage(current: TypeUsage, next: TypeUsage): TypeUsage = when {
        current == TypeUsage.UNUSED -> next
        current == next || current == TypeUsage.INPUT_OUTPUT -> current
        else -> TypeUsage.INPUT_OUTPUT
    }

    /** Backward-compatible inference for schema snapshots without explicit usage metadata. */
    private fun inferTypeUsages(schema: LuxoSchema): Map<String, TypeUsage> {
        val fields = buildMap {
            schema.models.values.forEach { put(it.name, it.fields) }
            schema.types.values.forEach { put(it.name, it.fields) }
        }
        val declared = buildMap {
            schema.models.values.forEach { put(it.name, it.usage) }
            schema.types.values.forEach { put(it.name, it.usage) }
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
        return usages
    }

    private fun isScalar(type: String): Boolean =
        type in setOf(
            "Int", "Float", "String", "Boolean", "DateTime", "Duration",
            "UUID", "Bytes", "Decimal", "JSON", "Enum",
        )

    /** Element reader expression for a scalar type (used inside readArray). */
    private fun scalarElementRead(type: String, decoder: String = "dec", enumName: String? = null): String =
        if (enumName != null) "$enumName.fromValue($decoder.readString())" else when (type) {
        "Int", "Duration" -> "$decoder.readInt()"
        "Float" -> "$decoder.readFloat()"
        "Boolean" -> "$decoder.readBool()"
        "UUID" -> "$decoder.readUuid()"
        "DateTime" -> "$decoder.readDateTime()" // seconds/nanoseconds -> RFC3339 string
        "Bytes" -> "$decoder.readBytes()"
        "JSON" -> "decodeLuxoJSON($decoder.readString())"
        else -> "$decoder.readString()" // String, Decimal, Enum
    }

    private fun scalarArrayRead(type: String, decoder: String = "dec", enumName: String? = null): String =
        if (type == "DateTime") "$decoder.readDateTimeArray()"
        else "$decoder.readArray { ${scalarElementRead(type, decoder, enumName)} }"

    private fun binaryRead(f: LuxoField): String {
        val n = f.nullable
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
                if (n) "dec.readNullable { $read }" else read
            }
            // DateTime: exact seconds/nanoseconds on the wire -> RFC3339 string.
            "DateTime" -> if (n) "dec.readDateTimePtr()" else "dec.readDateTime()"
            "Enum" -> if (n) "dec.readStringPtr()?.let { ${f.typeName}.fromValue(it) }"
                else "${f.typeName}.fromValue(dec.readString())"
            "String", "Decimal" -> if (n) "dec.readStringPtr()" else "dec.readString()"
            else -> {
                // Responses carry independent, length-delimited row or columnar messages.
                val tn = f.typeName ?: f.type
                val decoded = if (f.isList) "decodeColumnar$tn(dec.readBytes())"
                    else "decode$tn(LuxoDecoder(dec.readBytes()))"
                if (n) "dec.readNullable { $decoded }" else decoded
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
		appendLine("import com.luxo.client.luxoUtf8Length")
        appendLine("import com.luxo.client.decodeLuxoUtf8")
        appendLine("import com.luxo.client.luxoJsonFloat")
        appendLine("import com.luxo.client.decodeLuxoJsonInt")
        appendLine("import com.luxo.client.decodeLuxoJsonFloat")
        appendLine("import com.luxo.client.decodeLuxoJsonString")
        appendLine("import com.luxo.client.DateTimeCodec")
        appendLine("import com.luxo.client.decodeLuxoJsonBoolean")
        appendLine("import com.luxo.client.decodeLuxoJSON")
        appendLine("import com.luxo.client.encodeLuxoJSON")
        appendLine("import com.luxo.client.decodeLuxoJsonValue")
        appendLine("import kotlinx.serialization.Serializable")
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
        append(generateKotlinJSONArrays(schema, usages))
    }

    private fun generateKotlinDeclaration(
        name: String,
        fields: List<LuxoField>,
        schema: LuxoSchema,
        usages: Map<String, TypeUsage>,
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
            val raw = if (it.type == "Enum") "${it.name}${if (it.nullable) "?." else "."}rawValue" else it.name
			if (it.nullable) "($raw?.let(::luxoUtf8Length) ?: 0)" else "luxoUtf8Length($raw)"
		}
		appendLine("    override fun writeLuxo(encoder: LuxoEncoder) {")
		val arenaLength = if (arenaTerms.isEmpty()) "0" else arenaTerms.joinToString(" + ")
		appendLine("        encoder.writeVarint(($arenaLength).toLong())")
		for (field in fields) writeKotlinBinaryField(this, field, schema)
		appendLine("        encoder.writeEnd()")
		appendLine("    }")
	}

    private fun generateKotlinJSONEncoder(name: String, fields: List<LuxoField>, schema: LuxoSchema): String = buildString {
        appendLine()
        appendLine("    override fun toLuxoJson(): JsonElement = buildJsonObject {")
        for (field in fields) {
            val value = "this@$name.${field.name}"
            val expression = if (field.nullable) {
                "$value?.let { value -> ${kotlinJSONValue(field, "value", schema)} } ?: JsonNull"
            } else kotlinJSONValue(field, value, schema)
            appendLine("        put(\"${field.name}\", $expression)")
        }
        appendLine("    }")
    }

    private fun kotlinJSONValue(field: LuxoField, value: String, schema: LuxoSchema): String =
        if (field.isList) "JsonArray($value.map { item -> ${kotlinJSONScalar(field, "item", schema)} })"
        else kotlinJSONScalar(field, value, schema)

    private fun kotlinJSONScalar(field: LuxoField, value: String, schema: LuxoSchema): String {
        if (isNested(field, schema)) return "$value.toLuxoJson()"
        return when (field.type) {
            "JSON" -> value
            "Bytes" -> "JsonPrimitive(java.util.Base64.getEncoder().encodeToString($value))"
            "Float" -> "luxoJsonFloat($value)"
            "Enum" -> "JsonPrimitive($value.rawValue)"
            "Int", "Duration", "String", "Boolean", "DateTime", "UUID", "Decimal" -> "JsonPrimitive($value)"
            else -> error("unsupported structured field type ${field.type}")
        }
    }

	private fun writeKotlinBinaryField(out: StringBuilder, field: LuxoField, schema: LuxoSchema) {
		out.appendLine("        encoder.writeVarint(${field.id}L)")
		if (!field.nullable) {
			writeKotlinBinaryValue(out, field, field.name, schema, "        ")
			return
		}
		val local = "_${field.name}Value"
		out.appendLine("        val $local = ${field.name}")
		out.appendLine("        if ($local == null) {")
		out.appendLine("            encoder.writeBool(false)")
		out.appendLine("        } else {")
		out.appendLine("            encoder.writeBool(true)")
		writeKotlinBinaryValue(out, field, local, schema, "            ")
		out.appendLine("        }")
	}

	private fun writeKotlinBinaryValue(
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
		out.appendLine("${indent}encoder.writeVarint($value.size.toLong())")
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
		if (isNested(field, schema)) {
			out.appendLine("${indent}$value.writeLuxo(encoder)")
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
			"JSON" -> "encoder.writeBytes(encodeLuxoJSON($value).toByteArray(Charsets.UTF_8))"
			else -> error("unsupported structured field type ${field.type}")
		}
		out.appendLine("$indent$statement")
	}

    private fun generateKotlinDecoders(
        name: String,
        fields: List<LuxoField>,
        schema: LuxoSchema,
        usages: Map<String, TypeUsage>,
    ): String = buildString {
        appendLine("fun decode$name(dec: LuxoDecoder): $name {")
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
        usages: Map<String, TypeUsage>,
    ): String = buildString {
        appendLine("fun decodeJSON$name(data: JsonObject): $name {")
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
        appendLine("    val value = decodeJSON$name(data)")
        val missing = fields.joinToString(" && ") { "!value.${it.name}.isSelected" }.ifEmpty { "true" }
        appendLine("    if ($missing) throw LuxoError(\"ParseError\", 0, \"$name list item must contain a selected schema field\")")
        appendLine("    return value")
        appendLine("}\n")
    }

    private fun kotlinJSONValue(field: LuxoField, schema: LuxoSchema, value: String): String {
        if (field.isList) return "${kotlinJSONArrayName(field)}($value.jsonArray)"
        val typeName = field.typeName ?: field.type
        return if (isNested(field, schema)) "decodeJSON$typeName($value.jsonObject)"
            else scalarJSONRead(field.type, value, field.typeName.takeIf { field.type == "Enum" })
    }

    private fun kotlinJSONArrayName(field: LuxoField): String =
        "`decodeJSONArray\$${field.typeName ?: field.type}`"

    /** Keep array loops small and shared across all output fields with the same element type. */
    private fun generateKotlinJSONArrays(schema: LuxoSchema, usages: Map<String, TypeUsage>): String = buildString {
        val declarations = schema.models.values.map { it.name to it.fields } + schema.types.values.map { it.name to it.fields }
        val outputFields = declarations.filter { hasOutputUsage(usages[it.first] ?: TypeUsage.UNUSED) }
            .flatMap { it.second }.filter { it.isList }
        val returns = schema.apis.values.filter { isStructuredReturn(it, schema) && (it.returnList || it.paginated) }
            .map { LuxoField(0, "", "Model", typeName = it.returnType, isList = true) }
        val fields = (outputFields + returns).distinctBy(::kotlinJSONArrayName)
        for (field in fields) {
            val typeName = field.typeName ?: field.type
            val read = if (isNested(field, schema)) "`decodeJSON$typeName\$item`(item.jsonObject)"
                else scalarJSONRead(field.type, "item", field.typeName.takeIf { field.type == "Enum" })
            val type = resolveFieldType(field, schema, usages)
            appendLine("internal fun ${kotlinJSONArrayName(field)}(data: JsonArray): $type =")
            appendLine("    data.map { item -> $read }\n")
        }
    }

    private fun generateKotlinColumnarDecoder(
        name: String,
        fields: List<LuxoField>,
        schema: LuxoSchema,
        usages: Map<String, TypeUsage>,
        paginated: Boolean,
    ): String = buildString {
        val function = if (paginated) "decodePaginated$name" else "decodeColumnar$name"
        val result = if (paginated) "Page<$name>" else "List<$name>"
        appendLine("fun $function(data: ByteArray): $result = $function(ColumnarDecoder(data))\n")
        appendLine("fun $function(dec: ColumnarDecoder): $result {")
        for (f in fields) appendLine("    var _${f.name}: ${kotlinColumnType(f, schema, usages)}? = null")
        appendLine("    while (dec.nextColumn()) {")
        appendLine("        when (dec.fieldID) {")
        for (f in fields) appendLine("            ${f.id} -> _${f.name} = ${kotlinColumnRead(f, schema)}")
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

    private fun isNested(f: LuxoField, schema: LuxoSchema): Boolean {
        val name = f.typeName ?: f.type
        return f.relation || schema.models.containsKey(name) || schema.types.containsKey(name)
    }

    private fun kotlinColumnType(f: LuxoField, schema: LuxoSchema, usages: Map<String, TypeUsage>): String {
        if (isNested(f, schema) || f.isList || f.type == "Bytes" || f.type == "JSON") {
            return if (f.nullable) "List<ByteArray?>" else "List<ByteArray>"
        }
        val base = if (f.type == "Enum") "String" else resolveFieldType(f, schema, usages)
        return if (f.nullable) "List<$base?>" else "List<$base>"
    }

    private fun kotlinColumnRead(f: LuxoField, schema: LuxoSchema): String {
        if (isNested(f, schema) || f.isList || f.type == "Bytes" || f.type == "JSON") {
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

    private fun kotlinColumnValue(f: LuxoField, schema: LuxoSchema, usages: Map<String, TypeUsage>): String {
        val column = "_${f.name}"
        val typeName = f.typeName ?: f.type
        val base = resolveFieldType(f, schema, usages)
        val valueType = if (f.nullable) "$base?" else base
        val absent = "Selected.unselected<$valueType>()"
        val value: String
        if (isNested(f, schema)) {
            value = if (f.isList) {
                if (f.nullable) "$column[i]?.let { decodeColumnar$typeName(it) }"
                else "decodeColumnar$typeName($column[i])"
            } else if (f.nullable) {
                "$column[i]?.let { decode$typeName(LuxoDecoder(it)) }"
            } else {
                "decode$typeName(LuxoDecoder($column[i]))"
            }
        } else if (f.isList) {
            val data = if (f.nullable) "data" else "$column[i]"
            val decoded = completeScalarRead(data, scalarArrayRead(f.type, "cell", f.typeName.takeIf { f.type == "Enum" }), "cell")
            value = if (f.nullable) "$column[i]?.let { data -> $decoded }" else decoded
        } else if (f.type == "Enum") {
            value = if (f.nullable) "$column[i]?.let { $typeName.fromValue(it) }" else "$typeName.fromValue($column[i])"
        } else if (f.type == "JSON") {
            value = if (f.nullable) {
                "$column[i]?.let { decodeLuxoJSON(decodeLuxoUtf8(it)) }"
            } else {
                "decodeLuxoJSON(decodeLuxoUtf8($column[i]))"
            }
        } else {
            value = "$column[i]"
        }
        return "if ($column == null) $absent else Selected.value<$valueType>($value)"
    }

	internal fun genSchemaMap(schema: LuxoSchema, pkg: String): String = buildString {
        appendLine("// Auto-generated by luxo. DO NOT EDIT.\n")
        appendLine("package $pkg\n")
        appendLine("import com.luxo.client.APISchemaEntry")
        appendLine("import com.luxo.client.ParamSchema")
        appendLine("import com.luxo.client.SelectionFieldSchema\n")
        appendLine("private val LUXO_SELECTION_TYPES: Map<String, Map<String, SelectionFieldSchema>> = mapOf(")
        val selectionTypes = schema.types.values.map { it.name to it.fields } +
            schema.models.values.map { it.name to it.fields }
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
                schema.models[name]?.fields ?: schema.types[name]?.fields
            }.orEmpty()
			val needsTypes = returnFields.isNotEmpty() || api.params.any { it.type == "Model" }
            if (api.params.isNotEmpty() || returnFields.isNotEmpty()) {
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
            append(KotlinRequestCodegen.generate(api, api.paginated || isStructuredReturn(api, schema),
                { paramKtType(it, schema, usages) },
                { out, field, value -> writeKotlinBinaryScalar(out, field, value, schema, "        ") }))
        }
    }

    private fun genMethod(
        b: StringBuilder, api: LuxoAPI, schema: LuxoSchema, usages: Map<String, TypeUsage>,
    ) {
        val selection = api.paginated || isStructuredReturn(api, schema)
        val types: (LuxoParam) -> String = { paramKtType(it, schema, usages) }
        val declarations = KotlinRequestCodegen.declarations(api, types, selection).toMutableList()
        val ret = returnType(api)
        if (selection) b.appendLine("    @LuxoSelection")
        if (api.stream) {
            declarations += "observer: StreamObserver? = null"
            declarations += "onData: ($ret) -> Unit"
            val method = "subscribe" + api.name.replaceFirstChar { it.uppercase() }
            b.appendLine("    suspend fun $method(${declarations.joinToString(", ")}): () -> Unit {")
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
        val ret = returnType(api)
        if (api.returnNullable) {
            b.appendLine("    private fun decode_${api.name}(data: Any): $ret {")
            b.appendLine("        if (data === JsonNull || (data is ByteArray && isNullRoot(data))) return null")
            b.appendLine("        return when (data) {")
        } else {
            b.appendLine("    private fun decode_${api.name}(data: Any): $ret = when (data) {")
        }
        if (isStructuredReturn(api, schema)) genModelResponse(b, api) else genScalarResponse(b, api, schema)
        if (api.returnNullable) b.appendLine("        }")
        b.appendLine("    }\n")
    }

    private fun genScalarResponse(b: StringBuilder, api: LuxoAPI, schema: LuxoSchema) {
        val type = api.returnType ?: "Int"
        val enumName = type.takeIf { it in schema.enums }
        val element = scalarElementRead(type, enumName = enumName)
        val binaryRead = if (api.returnList) scalarArrayRead(type, enumName = enumName) else element
        val jsonRead = if (api.returnList) {
            "(data as JsonArray).map { ${scalarJSONRead(type, "it", enumName)} }"
        } else scalarJSONRead(type, "(data as JsonElement)", enumName)
        b.appendLine("        is ByteArray -> ${completeScalarRead("data", binaryRead, nullableRoot = api.returnNullable)}")
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

    private fun returnType(api: LuxoAPI): String {
        val t = luxoTypeToKt(api.returnType ?: "Int")
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
