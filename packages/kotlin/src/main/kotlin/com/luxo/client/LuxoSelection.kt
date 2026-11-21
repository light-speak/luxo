package com.luxo.client

/** Build-time markers; no runtime reflection or field registry is required. */
@Target(AnnotationTarget.FUNCTION)
@Retention(AnnotationRetention.BINARY)
annotation class LuxoSelection

@Target(AnnotationTarget.PROPERTY_GETTER)
@Retention(AnnotationRetention.BINARY)
annotation class LuxoSelectionField(val name: String)

/** Exact generated sum ownership for the compiler's resolved-symbol analysis. */
@Target(AnnotationTarget.CLASS)
@Retention(AnnotationRetention.BINARY)
annotation class LuxoSealedType(vararg val cases: String)

@Target(AnnotationTarget.CLASS)
@Retention(AnnotationRetention.BINARY)
annotation class LuxoSealedCase(val owner: String, val name: String)
