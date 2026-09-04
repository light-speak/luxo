package com.luxo.client

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/** Three-state argument for patch APIs: absent, explicit null, or a value. */
sealed interface LuxoOptional<out T> {
    data object Absent : LuxoOptional<Nothing>
    data class Present<T>(val value: T?) : LuxoOptional<T>
}

/** Field-selected output value: unselected, selected null, or selected value. */
@JvmInline
value class Selected<out T> private constructor(private val storage: Any?) {
    val isSelected: Boolean get() = storage !== Unselected

    @Suppress("UNCHECKED_CAST")
    fun value(): T {
        check(isSelected) { "field was not selected" }
        return storage as T
    }

    companion object {
        private data object Unselected

        fun <T> unselected(): Selected<T> = Selected(Unselected)
        fun <T> value(value: T): Selected<T> = Selected(value)
    }
}

@Serializable
enum class TypeUsage {
    @SerialName("input") INPUT,
    @SerialName("output") OUTPUT,
    @SerialName("inputOutput") INPUT_OUTPUT,
    @SerialName("unused") UNUSED,
}

/** Paginated list response. */
@Serializable
data class Page<T>(
    val items: List<T>,
    val total: Int,
    val page: Int,
    val pageSize: Int,
)

/** Luxo schema definition (from introspection). */
@Serializable
data class LuxoSchema(
    val models: Map<String, LuxoModel> = emptyMap(),
    val apis: Map<String, LuxoAPI> = emptyMap(),
    val enums: Map<String, LuxoEnum> = emptyMap(),
    val types: Map<String, LuxoTypeDecl> = emptyMap(),
)

@Serializable
data class LuxoEnum(
    val name: String,
    val values: List<String> = emptyList(),
)

@Serializable
data class LuxoTypeDecl(
    val name: String,
    val usage: TypeUsage? = null,
    val fields: List<LuxoField> = emptyList(),
)

@Serializable
data class LuxoModel(
    val name: String,
    val usage: TypeUsage? = null,
    val fields: List<LuxoField> = emptyList(),
)

@Serializable
data class LuxoField(
    val id: Int,
    val name: String,
    val type: String,
    val typeName: String? = null,
    val nullable: Boolean = false,
    val isList: Boolean = false,
    val relation: Boolean = false,
)

@Serializable
data class LuxoAPI(
    val id: Int,
    val name: String,
    val module: String,
    val returnType: String? = null,
    val returnList: Boolean = false,
    val paginated: Boolean = false,
    val stream: Boolean = false,
    val params: List<LuxoParam> = emptyList(),
)

@Serializable
data class LuxoParam(
    val id: Int,
    val name: String,
    val type: String,
    val typeName: String? = null,
    /** True when the param is an array ([T]). */
    val isList: Boolean = false,
    val nullable: Boolean = false,
    val hasDefault: Boolean = false,
)
