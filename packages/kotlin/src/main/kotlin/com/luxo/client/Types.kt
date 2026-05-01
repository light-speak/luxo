package com.luxo.client

import kotlinx.serialization.Serializable

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
)

@Serializable
data class LuxoModel(
    val name: String,
    val fields: List<LuxoField> = emptyList(),
)

@Serializable
data class LuxoField(
    val id: Int,
    val name: String,
    val type: String,
    val nullable: Boolean = false,
    val isList: Boolean = false,
)

@Serializable
data class LuxoAPI(
    val id: Int,
    val name: String,
    val module: String,
    val returnType: String? = null,
    val returnList: Boolean = false,
    val paginated: Boolean = false,
    val params: List<LuxoParam> = emptyList(),
)

@Serializable
data class LuxoParam(
    val id: Int,
    val name: String,
    val type: String,
)
