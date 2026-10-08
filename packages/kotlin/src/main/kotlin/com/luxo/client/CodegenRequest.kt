package com.luxo.client

internal object KotlinRequestCodegen {
    private val pagination = listOf("page", "pageSize")
    private fun optional(api: LuxoAPI, p: LuxoParam) = p.hasDefault || (api.paginated && p.name in pagination)
    private fun type(api: LuxoAPI, p: LuxoParam, base: String): String = when {
        p.nullable && p.hasDefault -> "LuxoOptional<$base>"
        p.nullable || optional(api, p) -> "$base?"
        else -> base
    }
    private fun missingPages(api: LuxoAPI) = if (api.paginated) pagination.filter { name -> api.params.none { it.name == name } } else emptyList()

    fun declarations(api: LuxoAPI, types: (LuxoParam) -> String, selection: Boolean): List<String> = buildList {
        for (p in api.params) {
            val default = if (p.nullable && p.hasDefault) " = LuxoOptional.Absent" else if (optional(api, p)) " = null" else ""
            add("${p.name}: ${type(api, p, types(p))}$default")
        }
        for (name in missingPages(api)) add("$name: Long? = null")
        if (selection) add("select: String? = null")
        if (api.paginated) addAll(listOf("filters: List<LuxoFilter>? = null", "sorters: List<LuxoSorter>? = null"))
    }

    fun invocation(api: LuxoAPI, selection: Boolean, select: String): String {
        val args = api.params.map { it.name }.toMutableList()
        args += missingPages(api)
        if (selection) args += select
        if (api.paginated) args += listOf("filters", "sorters")
        return "_${api.name}Request(${args.joinToString(", ")})"
    }

    fun generate(api: LuxoAPI, selection: Boolean, types: (LuxoParam) -> String,
        scalar: (StringBuilder, LuxoField, String) -> Unit): String = buildString {
        val fields = api.params.mapIndexed { i, p -> "private val p$i: ${type(api, p, types(p))}" }.toMutableList()
        for (name in missingPages(api)) fields += "private val $name: Long?"
        if (selection) fields += "override val selection: String?"
        if (api.paginated) fields += listOf("override val filters: List<LuxoFilter>?", "override val sorters: List<LuxoSorter>?")
        appendLine("\nprivate class _${api.name}Request(${fields.joinToString(", ")}) : GeneratedRequest {")
        appendLine("    override val apiID: Int = ${api.id}")
        appendLine("    override fun writeParameters(encoder: LuxoEncoder) {")
        for ((i, p) in api.params.withIndex()) writeParam(this, api, p, i, scalar)
        appendLine("    }")
        appendLine("    override fun toJSONParameters(): Map<String, Any?> = buildMap {")
        for ((i, p) in api.params.withIndex()) writeJSON(this, api, p, i)
        for (name in missingPages(api)) appendLine("        if ($name != null) put(\"$name\", $name)")
        if (selection) appendLine("        if (selection != null) put(\"\${'$'}select\", selection)")
        if (api.paginated) {
            appendLine("        if (filters != null) put(\"\${'$'}filters\", filters.map { mapOf(\"field\" to it.field, \"op\" to it.op, \"value\" to it.value) })")
            appendLine("        if (sorters != null) put(\"\${'$'}sorters\", sorters.map { mapOf(\"field\" to it.field, \"order\" to it.order) })")
        }
        appendLine("    }\n}")
    }

    private fun writeParam(out: StringBuilder, api: LuxoAPI, p: LuxoParam, i: Int,
        scalar: (StringBuilder, LuxoField, String) -> Unit) {
        val tri = p.nullable && p.hasDefault
        out.appendLine("        run {")
        if (tri) out.appendLine("        if (p$i is LuxoOptional.Present) {")
        out.appendLine("        val value = p$i${if (tri) ".value" else ""}")
        if (optional(api, p) && !tri) out.appendLine("        if (value != null) {")
        out.appendLine("        encoder.writeVarint(${p.id}L)")
        if (p.nullable) out.appendLine("        if (value == null) { encoder.writeBool(false) } else {\n        encoder.writeBool(true)")
        val field = LuxoField(p.id, p.name, p.type, typeName = p.typeName)
        if (p.isList && p.type in listOf("Int", "Duration")) {
            out.appendLine("        encoder.writeIntList(value)")
        } else if (p.isList) {
            out.appendLine("        encoder.writeVarint(value.size.toLong())\n        for (item in value) {")
            writeScalar(out, field, "item", scalar)
            out.appendLine("        }")
        } else writeScalar(out, field, "value", scalar)
        if (p.nullable) out.appendLine("        }")
        out.appendLine("        }")
        if (optional(api, p)) out.appendLine("        }")
    }

    private fun writeScalar(out: StringBuilder, field: LuxoField, value: String,
        scalar: (StringBuilder, LuxoField, String) -> Unit) {
        if (field.type == "Model") out.appendLine("        encoder.writeDelimitedValue($value) { target, model -> model.writeLuxo(target) }")
        else scalar(out, field, value)
    }

    private fun writeJSON(out: StringBuilder, api: LuxoAPI, p: LuxoParam, i: Int) {
        val tri = p.nullable && p.hasDefault
        val value = "p$i${if (tri) ".value" else ""}"
        val raw = if (p.type != "Enum") value else if (p.isList) "$value${if (p.nullable) "?" else ""}.map { it.rawValue }"
            else "$value${if (p.nullable) "?" else ""}.rawValue"
        val condition = if (tri) "if (p$i is LuxoOptional.Present) " else if (optional(api, p)) "if (p$i != null) " else ""
        out.appendLine("        ${condition}put(\"${p.name}\", $raw)")
    }
}
