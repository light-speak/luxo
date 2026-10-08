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
    val seen = HashSet<Int>()
    for (api in apis.values) {
        checkSchemaID(api.id, WireIDLimits.API, seen, "API", api.name)
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
