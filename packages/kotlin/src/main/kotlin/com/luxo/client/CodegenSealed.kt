package com.luxo.client

/** Closed sums use concrete case classes and the existing typed field codecs. */
internal object KotlinSealedCodegen {
    private const val depth = "`\$modelDepth`"
    private fun function(name: String) = "`$name\$depth`"
    private fun field(param: LuxoParam) = LuxoField(param.id, param.name, param.type,
        typeName = param.typeName, isList = param.isList, nullable = param.nullable)

    fun hasResponseSealed(name: String?, schema: LuxoSchema, seen: MutableSet<String>? = null): Boolean {
        if (name == null || schema.sealeds.isEmpty()) return false
        if (name in schema.sealeds) return true
        val fields = schema.models[name]?.fields ?: schema.types[name]?.fields ?: return false
        val visited = seen ?: mutableSetOf()
        if (!visited.add(name)) return false
        return fields.any { hasResponseSealed(it.typeName ?: it.type, schema, visited) }
    }

    fun caseFields(owner: LuxoSealed?): List<LuxoField> = owner?.variants?.map {
        LuxoField(it.id, it.name, "Model", typeName = "${owner.name}.${it.name}")
    }.orEmpty()

    fun selectionTypes(schema: LuxoSchema): List<Pair<String, List<LuxoField>>> = buildList {
        for (owner in schema.sealeds.values) {
            val cases = caseFields(owner)
            add(owner.name to cases)
            add("@sealed:${owner.name}" to cases)
            for (variant in owner.variants) add("${owner.name}.${variant.name}" to variant.fields.map(::field))
        }
    }

    fun generate(owner: LuxoSealed, schema: LuxoSchema, usages: LuxoCodegen.TypeUsages): String = buildString {
        val usage = usages[owner.name] ?: TypeUsage.UNUSED
        if (usage == TypeUsage.OUTPUT || usage == TypeUsage.INPUT_OUTPUT) {
            output(this, owner, schema, usages)
            decoders(this, owner, schema, usages)
        }
        if (usage == TypeUsage.INPUT || usage == TypeUsage.INPUT_OUTPUT) input(this, owner, schema, usages)
    }

    private fun output(out: StringBuilder, owner: LuxoSealed, schema: LuxoSchema, usages: LuxoCodegen.TypeUsages) {
        out.appendLine("@LuxoSealedType(${owner.variants.joinToString(", ") { "\"${it.name}\"" }})")
        out.appendLine("sealed interface ${owner.name} {")
        for (variant in owner.variants) {
            out.appendLine("    @LuxoSealedCase(\"${owner.name}\", \"${variant.name}\")")
            out.appendLine("    class `${variant.name}`(")
            for (p in variant.fields) {
                val base = LuxoCodegen.resolveFieldType(field(p), schema, usages)
                val type = base + if (p.nullable) "?" else ""
                out.appendLine("        @get:LuxoSelectionField(\"${p.name}\")")
                out.appendLine("        val `${p.name}`: Selected<$type> = Selected.unselected(),")
            }
            out.appendLine("    ) : ${owner.name}")
        }
        out.appendLine("}\n")
    }

    private fun input(out: StringBuilder, owner: LuxoSealed, schema: LuxoSchema, usages: LuxoCodegen.TypeUsages) {
        val name = LuxoCodegen.inputTypeName(owner.name, usages)
        out.appendLine("sealed interface $name : LuxoBinaryEncodable {")
        out.appendLine("    fun ${function("writeLuxo")}(encoder: LuxoEncoder, $depth: Int)")
        out.appendLine("    fun ${function("toLuxoJson")}($depth: Int): JsonElement")
        for (variant in owner.variants) {
            out.appendLine("    class `${variant.name}`(")
            for (p in variant.fields) {
                val base = LuxoCodegen.resolveFieldType(field(p), schema, usages, input = true)
                val type = when {
                    p.nullable && p.hasDefault -> "LuxoOptional<$base> = LuxoOptional.Absent"
                    p.hasDefault -> "$base? = null"
                    p.nullable -> "$base?"
                    else -> base
                }
                out.appendLine("        val `${p.name}`: $type,")
            }
            out.appendLine("    ) : $name {")
            inputBinary(out, variant, schema)
            inputJSON(out, variant, schema)
            out.appendLine("    }")
        }
        out.appendLine("}\n")
    }

    private fun inputBinary(out: StringBuilder, variant: LuxoSealedVariant, schema: LuxoSchema) {
        out.appendLine("        override fun writeLuxo(encoder: LuxoEncoder) = ${function("writeLuxo")}(encoder, 1)")
        out.appendLine("        override fun ${function("writeLuxo")}(encoder: LuxoEncoder, $depth: Int) {")
        out.appendLine("            require($depth in 1..63) { \"model nesting exceeds 64 levels\" }")
        out.appendLine("            encoder.writeVarint(${variant.id}L)")
        out.appendLine("            encoder.writeDelimited { target ->")
        out.appendLine("                writePayload(target, $depth + 1)")
        out.appendLine("            }")
        out.appendLine("        }")
        out.appendLine("        private fun writePayload(encoder: LuxoEncoder, $depth: Int) {")
        val terms = variant.fields.filter { !it.isList && it.type in listOf("String", "Enum") }.map { p ->
            val ref = inputReference(p)
            val raw = if (p.type == "Enum") "$ref${if (p.nullable || p.hasDefault) "?" else ""}.rawValue" else ref
            if (p.nullable || p.hasDefault) "($raw?.let(::luxoUtf8Length) ?: 0)" else "luxoUtf8Length($raw)"
        }
        out.appendLine("            encoder.writeVarint((${terms.joinToString(" + ").ifEmpty { "0" }}).toLong())")
        for (p in variant.fields) writeInputField(out, p, schema)
        out.appendLine("            encoder.writeEnd()")
        out.appendLine("        }")
    }

    private fun inputReference(p: LuxoParam): String = if (p.nullable && p.hasDefault)
        "(this.`${p.name}`.let { if (it is LuxoOptional.Present) it.value else null })" else "this.`${p.name}`"

    private fun writeInputField(out: StringBuilder, p: LuxoParam, schema: LuxoSchema) {
        val field = field(p)
        out.appendLine("            run {")
        if (p.nullable && p.hasDefault) out.appendLine("            if (this.`${p.name}` is LuxoOptional.Present) {")
        out.appendLine("            val value = ${if (p.nullable && p.hasDefault) "this.`${p.name}`.value" else inputReference(p)}")
        if (p.hasDefault && !p.nullable) out.appendLine("            if (value != null) {")
        out.appendLine("            encoder.writeVarint(${p.id}L)")
        if (p.nullable) {
            out.appendLine("            if (${LuxoCodegen.kotlinNullCondition(field, "value")}) { encoder.writeBool(false) } else {")
            out.appendLine("            encoder.writeBool(true)")
        }
        LuxoCodegen.writeKotlinBinaryValue(out, field, "value", schema, "            ")
        if (p.nullable) out.appendLine("            }")
        if (p.hasDefault) out.appendLine("            }")
        out.appendLine("            }")
    }

    private fun inputJSON(out: StringBuilder, variant: LuxoSealedVariant, schema: LuxoSchema) {
        out.appendLine("        override fun toLuxoJson(): JsonElement = ${function("toLuxoJson")}(1)")
        out.appendLine("        override fun ${function("toLuxoJson")}($depth: Int): JsonElement {")
        out.appendLine("            require($depth in 1..63) { \"model nesting exceeds 64 levels\" }")
        out.appendLine("            return buildJsonObject { put(\"${variant.name}\", jsonPayload($depth + 1)) }")
        out.appendLine("        }")
        out.appendLine("        private fun jsonPayload($depth: Int): JsonObject = buildJsonObject {")
        for (p in variant.fields) {
            out.appendLine("            run {")
            if (p.nullable && p.hasDefault) out.appendLine("            if (this@`${variant.name}`.`${p.name}` is LuxoOptional.Present) {")
            val ref = (if (p.nullable && p.hasDefault) "this.`${p.name}`.value" else inputReference(p)).replace("this.", "this@`${variant.name}`.")
            out.appendLine("            val value = $ref")
            if (p.hasDefault && !p.nullable) out.appendLine("            if (value != null) {")
            val raw = LuxoCodegen.kotlinJSONValue(field(p), "value", schema)
            val value = if (p.nullable) "if (value == null) JsonNull else $raw" else raw
            out.appendLine("            put(\"${p.name}\", $value)")
            if (p.hasDefault) out.appendLine("            }")
            out.appendLine("            }")
        }
        out.appendLine("        }")
    }

    private fun decoders(out: StringBuilder, owner: LuxoSealed, schema: LuxoSchema, usages: LuxoCodegen.TypeUsages) {
        val name = owner.name
        out.appendLine("fun decode$name(dec: LuxoDecoder): $name = ${function("decode$name")}(dec, 1)")
        out.appendLine("internal fun ${function("decode$name")}(dec: LuxoDecoder, $depth: Int): $name {")
        out.appendLine("    require($depth in 1..63) { \"model nesting exceeds 64 levels\" }")
        out.appendLine("    val tag = dec.readVarint()")
        out.appendLine("    val payload = LuxoDecoder(dec.readBytes())")
        out.appendLine("    dec.checkComplete()")
        out.appendLine("    return when (tag) {")
        for (v in owner.variants) out.appendLine("        ${v.id}L -> ${function("decode\$$name\$${v.name}")}(payload, $depth + 1)")
        out.appendLine("        else -> error(\"unknown $name variant tag \$tag\")")
        out.appendLine("    }\n}\n")
        out.appendLine("fun decodeJSON$name(data: JsonObject): $name = ${function("decodeJSON$name")}(data, 1)")
        out.appendLine("internal fun ${function("decodeJSON$name")}(data: JsonObject, $depth: Int): $name {")
        out.appendLine("    require($depth in 1..63) { \"model nesting exceeds 64 levels\" }")
        out.appendLine("    require(data.size == 1) { \"$name requires exactly one variant\" }")
        out.appendLine("    val entry = data.entries.single()")
        out.appendLine("    return when (entry.key) {")
        for (v in owner.variants) out.appendLine("        \"${v.name}\" -> ${function("decodeJSON\$$name\$${v.name}")}(entry.value.jsonObject, $depth + 1)")
        out.appendLine("        else -> error(\"unknown $name variant\")\n    }\n}\n")
        out.appendLine("internal fun ${function("decodeSealedList$name")}(dec: LuxoDecoder, $depth: Int): List<$name> =")
        out.appendLine("    dec.readArray { ${function("decode$name")}(LuxoDecoder(dec.readBytes()), $depth) }.also { dec.checkComplete() }\n")
        out.appendLine("internal fun ${function("decodeJSONArray\$$name")}(data: JsonArray, $depth: Int): List<$name> =")
        out.appendLine("    data.map { ${function("decodeJSON$name")}(it.jsonObject, $depth) }\n")
        for (v in owner.variants) {
            payloadDecoder(out, owner, v, schema, usages, json = false)
            payloadDecoder(out, owner, v, schema, usages, json = true)
        }
    }

    private fun payloadDecoder(out: StringBuilder, owner: LuxoSealed, variant: LuxoSealedVariant,
        schema: LuxoSchema, usages: LuxoCodegen.TypeUsages, json: Boolean) {
        val name = owner.name
        val stem = "${if (json) "decodeJSON" else "decode"}\$$name\$${variant.name}"
        val parameter = if (json) "data: JsonObject" else "dec: LuxoDecoder"
        out.appendLine("private fun ${function(stem)}($parameter, $depth: Int): $name.${variant.name} {")
        if (json) jsonPayloadReferences(out, owner, variant)
        for (p in variant.fields) {
            val base = LuxoCodegen.resolveFieldType(field(p), schema, usages) + if (p.nullable) "?" else ""
            out.appendLine("    var `\$${p.name}`: Selected<$base> = Selected.unselected()")
        }
        if (json) jsonPayloadFields(out, variant, schema) else binaryPayloadFields(out, variant)
        out.appendLine("    return $name.${variant.name}(")
        for (p in variant.fields) out.appendLine("        `${p.name}` = `\$${p.name}`,")
        out.appendLine("    )\n}\n")
    }

    private fun binaryPayloadFields(out: StringBuilder, variant: LuxoSealedVariant) {
        out.appendLine("    dec.skipArenaHeader()\n    while (dec.nextField()) {\n        when (dec.fieldID) {")
        for (p in variant.fields) out.appendLine("            ${p.id} -> `\$${p.name}` = Selected.value(${LuxoCodegen.binaryRead(field(p))})")
        out.appendLine("            else -> error(\"unknown payload field ID\")\n        }\n    }\n    dec.checkComplete()")
    }

    private fun jsonPayloadReferences(out: StringBuilder, owner: LuxoSealed, variant: LuxoSealedVariant) {
        for (p in variant.fields) out.appendLine("    var `\$${p.name}\$json`: JsonElement? = null")
        out.appendLine("    for ((key, value) in data) {")
        out.appendLine("        when (key) {")
        for (p in variant.fields) out.appendLine("            \"${p.name}\" -> `\$${p.name}\$json` = value")
        out.appendLine("            else -> throw IllegalArgumentException(\"unknown ${owner.name}.${variant.name} field\")")
        out.appendLine("        }")
        out.appendLine("    }")
    }

    private fun jsonPayloadFields(out: StringBuilder, variant: LuxoSealedVariant, schema: LuxoSchema) {
        for (p in variant.fields) {
            val read = LuxoCodegen.kotlinJSONValue(field(p), schema, "value")
            val value = if (p.nullable) "if (value is JsonNull) null else $read" else read
            out.appendLine("    `\$${p.name}\$json`?.let { value -> `\$${p.name}` = Selected.value($value) }")
        }
    }
}
