package com.luxo.client

/** Build-time markers; no runtime reflection or field registry is required. */
@Target(AnnotationTarget.FUNCTION)
@Retention(AnnotationRetention.BINARY)
annotation class LuxoSelection

@Target(AnnotationTarget.PROPERTY_GETTER)
@Retention(AnnotationRetention.BINARY)
annotation class LuxoSelectionField(val name: String)
