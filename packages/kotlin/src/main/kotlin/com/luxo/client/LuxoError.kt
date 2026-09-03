package com.luxo.client

import kotlinx.serialization.json.JsonElement

/**
 * Luxo API error with structured error info.
 */
class LuxoError(
    /** Error name (e.g., "NotFound", "Unauthorized"). */
    val error: String,
    /** HTTP-like status code. */
    val code: Int,
    /** Human-readable message. */
    override val message: String,
    /** Trace ID for debugging. */
    val traceId: String? = null,
    /** Structured application error details. */
    val data: JsonElement? = null,
    /** Development-only internal cause. */
    val developmentCause: String? = null,
) : Exception(message) {

    override fun toString(): String = "LuxoError($error, $code, $message)"
}
