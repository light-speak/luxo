package com.luxo.client

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement

/** Generated input values encode directly in binary mode and lazily in JSON mode. */
interface LuxoBinaryEncodable {
	fun writeLuxo(encoder: LuxoEncoder)
	fun toLuxoJson(): JsonElement
}

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

/** Paginated list response with full-width Luxo Int metadata. */
@Serializable
data class Page<T>(
    val items: List<T>,
    val total: Long,
    val page: Long,
    val pageSize: Long,
)

/** Luxo schema definition (from introspection). */
@Serializable
data class LuxoSchema(
    val models: Map<String, LuxoModel> = emptyMap(),
    val apis: Map<String, LuxoAPI> = emptyMap(),
    val enums: Map<String, LuxoEnum> = emptyMap(),
    val types: Map<String, LuxoTypeDecl> = emptyMap(),
    val sealeds: Map<String, LuxoSealed> = emptyMap(),
) {
    /** Preserve generated tools compiled before the sealed directory was introduced. */
    internal constructor(models: Map<String, LuxoModel> = emptyMap(), apis: Map<String, LuxoAPI> = emptyMap(),
        enums: Map<String, LuxoEnum> = emptyMap(), types: Map<String, LuxoTypeDecl> = emptyMap()) :
        this(models, apis, enums, types, emptyMap())

    @JvmName("copy")
    internal fun legacyCopy(models: Map<String, LuxoModel> = this.models, apis: Map<String, LuxoAPI> = this.apis,
        enums: Map<String, LuxoEnum> = this.enums, types: Map<String, LuxoTypeDecl> = this.types): LuxoSchema =
        LuxoSchema(models, apis, enums, types, sealeds)
}

@Serializable
data class LuxoSealed(
    val name: String,
    val variants: List<LuxoSealedVariant> = emptyList(),
    val module: String = "",
    val usage: TypeUsage? = null,
    val description: String = "",
)

@Serializable
data class LuxoSealedVariant(
    val id: Int,
    val name: String,
    val fields: List<LuxoParam> = emptyList(),
)

@Serializable
data class LuxoEnum(
    val name: String,
    val values: List<String> = emptyList(),
    val module: String = "",
    val description: String = "",
)

@Serializable
data class LuxoTypeDecl(
    val name: String,
    val usage: TypeUsage? = null,
    val fields: List<LuxoField> = emptyList(),
    val module: String = "",
    val description: String = "",
    val directives: List<String> = emptyList(),
)

@Serializable
data class LuxoModel(
    val name: String,
    val usage: TypeUsage? = null,
    val fields: List<LuxoField> = emptyList(),
    val module: String = "",
    val description: String = "",
    val directives: List<String> = emptyList(),
    val computedSources: Map<String, LuxoComputedSource> = emptyMap(),
)

@Serializable
data class LuxoComputedSource(
    val model: String,
    val module: String = "",
)

@Serializable
enum class RelationKind {
    @SerialName("belongsTo") BELONGS_TO,
    @SerialName("hasMany") HAS_MANY,
    @SerialName("hasOne") HAS_ONE,
}

@Serializable
data class LuxoField(
    val id: Int,
    val name: String,
    val type: String,
    val typeName: String? = null,
    @Serializable(with = SchemaBooleanSerializer::class) val nullable: Boolean = false,
    @Serializable(with = SchemaBooleanSerializer::class) val isList: Boolean = false,
    @Serializable(with = SchemaBooleanSerializer::class) val relation: Boolean = false,
    @Serializable(with = SchemaBooleanSerializer::class) val computed: Boolean = false,
    @Serializable(with = SchemaBooleanSerializer::class) val primaryKey: Boolean = false,
    val description: String = "",
    val directives: List<String> = emptyList(),
    val relationKind: RelationKind? = null,
    val localKey: String = "",
    val remoteKey: String = "",
    val targetModule: String = "",
    val module: String = "",
    val foreignKey: String = "",
)

@Serializable
data class LuxoAPI(
    val id: Int,
    val name: String,
    val module: String,
    val returnType: String? = null,
    @Serializable(with = SchemaBooleanSerializer::class) val returnList: Boolean = false,
    @Serializable(with = SchemaBooleanSerializer::class) val paginated: Boolean = false,
    val defaultPageSize: Int? = null,
    @Serializable(with = SchemaBooleanSerializer::class) val stream: Boolean = false,
    val params: List<LuxoParam> = emptyList(),
    @Serializable(with = SchemaBooleanSerializer::class) val deprecated: Boolean = false,
    val deprecatedReason: String = "",
    val description: String = "",
    val directives: List<String> = emptyList(),
    @Serializable(with = SchemaBooleanSerializer::class)
    @SerialName("internal") val isInternal: Boolean = false,
    @Serializable(with = SchemaBooleanSerializer::class) val returnNullable: Boolean = false,
    val query: LuxoQueryCapabilities? = null,
)

@Serializable
data class LuxoQueryCapabilities(
    val filters: List<LuxoQueryFilter> = emptyList(),
    val sorters: List<String> = emptyList(),
)

@Serializable
data class LuxoQueryFilter(val field: String, val type: String, val operators: List<String>)

@Serializable
data class LuxoParam(
    val id: Int,
    val name: String,
    val type: String,
    val typeName: String? = null,
    /** True when the param is an array ([T]). */
    @Serializable(with = SchemaBooleanSerializer::class) val isList: Boolean = false,
    @Serializable(with = SchemaBooleanSerializer::class) val nullable: Boolean = false,
    @Serializable(with = SchemaBooleanSerializer::class) val hasDefault: Boolean = false,
    val description: String = "",
)
