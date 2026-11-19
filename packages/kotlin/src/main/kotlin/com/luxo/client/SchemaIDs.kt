package com.luxo.client

internal object WireIDLimits {
    const val SELECTION_FIELD = 65535
    const val API = 2147483647
    const val PARAM = API - 2
}

/** Validate the complete directory before writing generated code or snapshots. */
internal fun LuxoSchema.validateIDs() {
    for (model in models.values) validateFieldIDs(model.fields, "model ${model.name}")
    for (type in types.values) validateFieldIDs(type.fields, "type ${type.name}")
    for (owner in sealeds.values) {
        val tags = HashSet<Int>()
        for (variant in owner.variants) {
            checkSchemaID(variant.id, WireIDLimits.SELECTION_FIELD, tags, "sealed ${owner.name}", variant.name)
            val fields = HashSet<Int>()
            for (field in variant.fields) {
                checkSchemaID(field.id, WireIDLimits.SELECTION_FIELD, fields, "${owner.name}.${variant.name}", field.name)
            }
        }
    }
    val seen = HashSet<Int>()
    for (api in apis.values) {
        checkSchemaID(api.id, WireIDLimits.API, seen, "API", api.name)
        if (api.paginated) {
            for (name in listOf("page", "pageSize")) {
                val param = api.params.singleOrNull { it.name == name }
                require(param != null && param.type == "Int" && !param.nullable && !param.isList) {
                    "API ${api.name} pagination requires a numbered non-null Int parameter $name"
                }
            }
        }
        val params = HashSet<Int>()
        val owner = "API ${api.name}"
        for (param in api.params) {
            checkSchemaID(param.id, WireIDLimits.PARAM, params, owner, param.name)
        }
    }
}

private fun validateFieldIDs(fields: List<LuxoField>, owner: String) {
    val seen = HashSet<Int>()
    for (field in fields) checkSchemaID(field.id, WireIDLimits.SELECTION_FIELD, seen, owner, field.name)
}

internal fun checkSchemaID(id: Int, maximum: Int, seen: MutableSet<Int>, owner: String, name: String) {
    require(id in 1..maximum) { "$owner.$name: ID must be from 1 to $maximum" }
    require(seen.add(id)) { "$owner.$name: duplicate ID $id" }
}
