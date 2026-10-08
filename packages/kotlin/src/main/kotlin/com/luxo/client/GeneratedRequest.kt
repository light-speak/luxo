package com.luxo.client

/** Generated parameters retain their concrete types until the wire writer consumes them. */
interface GeneratedRequest {
    val apiID: Int
    val selection: String? get() = null
    val filters: List<LuxoFilter>? get() = null
    val sorters: List<LuxoSorter>? get() = null
    fun writeParameters(encoder: LuxoEncoder)
    fun toJSONParameters(): Map<String, Any?>
}
