package com.luxo.client

import java.lang.ref.WeakReference

/** Immutable call-site syntax with a bounded, weak-identity schema cache. */
class PreparedSelection(val text: String) {
    private val selected = SelectionParser(text).parse()
    @Volatile private var cached: CachedMask? = null

    init {
        if (text.isBlank()) throw LuxoError("ConfigError", 0, "selection must not be empty")
    }

    internal fun writeMask(encoder: LuxoEncoder, schema: APISchemaEntry) {
        val bytes = mask(schema)
        encoder.writeVarint(bytes.size.toLong())
        encoder.writeRawBytes(bytes)
    }

    private fun mask(schema: APISchemaEntry): ByteArray {
        if (!schema.selectionSnapshot) return encodeSelectionNode(selected, schema.fields, schema.types)
        val current = cached
        if (current != null && current.schema.get() === schema) return current.bytes
        val bytes = encodeSelectionNode(selected, schema.fields, schema.types)
        // Concurrent misses may compute twice; publication is atomic and bytes never mutate.
        cached = CachedMask(WeakReference(schema), bytes)
        return bytes
    }

    private class CachedMask(val schema: WeakReference<APISchemaEntry>, val bytes: ByteArray)
}

private data class SelectedField(val name: String, val children: List<SelectedField>? = null)

private class SelectionParser(private val input: String) {
    private var offset = 0

    fun parse(): List<SelectedField> {
        val fields = parseList(nested = false, depth = 0)
        skipSpaces()
        if (offset != input.length) fail("unexpected '${input[offset]}'")
        return fields
    }

    private fun parseList(nested: Boolean, depth: Int): List<SelectedField> {
        if (depth >= 32) fail("selection depth exceeds 32")
        val fields = mutableListOf<SelectedField>()
        val names = mutableSetOf<String>()
        while (true) {
            skipSpaces()
            if (offset >= input.length || (nested && input[offset] == '}')) {
                if (fields.isNotEmpty()) fail("expected field after comma")
                break
            }
            val name = readIdentifier()
            if (name.isEmpty()) fail("expected field name")
            if (!names.add(name)) fail("duplicate field '$name'")
            skipSpaces()
            val children = if (offset < input.length && input[offset] == '{') parseChildren(name, depth) else null
            fields += SelectedField(name, children)
            skipSpaces()
            if (offset >= input.length || input[offset] != ',') break
            offset++
        }
        return fields
    }

    private fun parseChildren(name: String, depth: Int): List<SelectedField> {
        offset++
        val children = parseList(nested = true, depth = depth + 1)
        if (children.isEmpty()) fail("empty selection for '$name'")
        skipSpaces()
        if (offset >= input.length || input[offset] != '}') fail("missing '}' for '$name'")
        offset++
        return children
    }

    private fun readIdentifier(): String {
        val start = offset
        if (offset >= input.length || !input[offset].isIdentifierStart()) return ""
        offset++
        while (offset < input.length && input[offset].isIdentifierPart()) offset++
        return input.substring(start, offset)
    }

    private fun skipSpaces() {
        while (offset < input.length && input[offset].isWhitespace()) offset++
    }

    private fun fail(message: String): Nothing =
        throw LuxoError("ConfigError", 0, "$message at position $offset")
}

private fun Char.isIdentifierStart(): Boolean = this == '_' || this in 'A'..'Z' || this in 'a'..'z'
private fun Char.isIdentifierPart(): Boolean = isIdentifierStart() || this in '0'..'9'

internal fun writeFieldMask(enc: LuxoEncoder, meta: APISchemaEntry, selection: String?, plan: PreparedSelection? = null) {
    if (meta.fields.isEmpty()) {
        enc.writeVarint(0)
        return
    }
    if (selection.isNullOrBlank()) {
        throw LuxoError("ConfigError", 0, "\$select is required for structured responses")
    }
    if (plan != null && plan.text == selection) {
        plan.writeMask(enc, meta)
        return
    }
    val mask = encodeSelectionNode(SelectionParser(selection).parse(), meta.fields, meta.types)
    enc.writeVarint(mask.size.toLong())
    enc.writeRawBytes(mask)
}

private fun encodeSelectionNode(
    selected: List<SelectedField>,
    fields: Map<String, SelectionFieldSchema>,
    types: Map<String, Map<String, SelectionFieldSchema>>,
): ByteArray {
    var mask = ByteArray(0)
    val children = mutableListOf<Pair<Int, ByteArray>>()
    for (field in selected) {
        val meta = fields[field.name]
            ?: throw LuxoError("ConfigError", 0, "unknown selected field: ${field.name}")
        mask = FieldMask.set(mask, meta.fieldID)
        val nestedSelection = field.children ?: continue
        val nestedFields = meta.typeName?.let(types::get)
            ?: throw LuxoError("ConfigError", 0, "field ${field.name} does not support nested selection")
        children += meta.fieldID to encodeSelectionNode(nestedSelection, nestedFields, types)
    }
    val node = LuxoEncoder()
    node.writeVarint(mask.size.toLong())
    node.writeRawBytes(mask)
    for ((fieldID, child) in children.sortedBy { it.first }) {
        node.writeVarint(fieldID.toLong())
        node.writeVarint(child.size.toLong())
        node.writeRawBytes(child)
    }
    return node.bytes()
}

