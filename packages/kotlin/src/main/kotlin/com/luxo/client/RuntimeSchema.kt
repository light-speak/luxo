package com.luxo.client

import java.util.IdentityHashMap

private typealias Fields = Map<String, SelectionFieldSchema>
private typealias Types = Map<String, Fields>

/** Validate once; transports privately own this read-only graph and never expose it. */
internal fun snapshotAPISchema(schema: Map<String, APISchemaEntry>): Map<String, APISchemaEntry> {
    if (schema.isEmpty()) return emptyMap()
    val seen = HashSet<Int>()
    val snapshots = SchemaSnapshots()
    val result = LinkedHashMap<String, APISchemaEntry>()
    for ((name, entry) in schema) {
        checkSchemaID(entry.id, WireIDLimits.API, seen, "API", name)
        val owner = "API $name"
        result[name] = entry.copy(params = snapshotParams(entry.params, owner),
            fields = snapshots.fields(entry.fields, owner), types = snapshots.types(entry.types))
    }
    return result
}

private fun snapshotParams(params: List<ParamSchema>, owner: String): List<ParamSchema> {
    if (params.isEmpty()) return emptyList()
    val copy = if (params.size == 1) listOf(params[0]) else params.toTypedArray().asList()
    val seen = HashSet<Int>()
    for (param in copy) checkSchemaID(param.fieldID, WireIDLimits.PARAM, seen, owner, param.name)
    // Metadata value classes are final and contain only immutable scalar values.
    return copy
}

private class SchemaSnapshots {
    private val fieldCopies = IdentityHashMap<Fields, Fields>()
    private val typeCopies = IdentityHashMap<Types, Types>()

    fun fields(source: Fields, owner: String): Fields {
        if (source.isEmpty()) return emptyMap()
        fieldCopies[source]?.let { return it }
        val copy = LinkedHashMap(source)
        val seen = HashSet<Int>()
        for ((name, field) in copy) checkSchemaID(field.fieldID, WireIDLimits.SELECTION_FIELD, seen, owner, name)
        fieldCopies[source] = copy
        return copy
    }

    fun types(source: Types): Types {
        if (source.isEmpty()) return emptyMap()
        typeCopies[source]?.let { return it }
        val copy = LinkedHashMap<String, Fields>()
        for ((name, definition) in source) copy[name] = fields(definition, "type $name")
        typeCopies[source] = copy
        return copy
    }
}
