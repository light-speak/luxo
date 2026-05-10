package com.luxo.compiler

/**
 * Tree node for nested field selection.
 * Root → { user → { name, email }, comments → { content, user → { id } } }
 *
 * Generates $select strings: "name,email,comments{content,user{id}}"
 */
class FieldNode private constructor(val name: String) {
    val children = mutableMapOf<String, FieldNode>()

    companion object {
        fun root() = FieldNode("")
    }

    fun addChild(fieldName: String): FieldNode {
        return children.getOrPut(fieldName) { FieldNode(fieldName) }
    }

    fun maxDepth(): Int {
        if (children.isEmpty()) return 0
        return children.values.maxOf { it.maxDepth() } + 1
    }

    fun toSelectString(): String {
        if (children.isEmpty()) return ""
        return children.values.joinToString(",") { child ->
            val nested = child.toSelectString()
            if (nested.isNotEmpty()) "${child.name}{$nested}" else child.name
        }
    }
}
