package com.luxo.client

import kotlinx.serialization.KSerializer
import kotlinx.serialization.SerializationException
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.encoding.Decoder
import kotlinx.serialization.encoding.Encoder
import kotlinx.serialization.json.JsonDecoder
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull

/** Introspection flags are JSON booleans; quoted text must never change schema shape. */
internal object SchemaBooleanSerializer : KSerializer<Boolean> {
    override val descriptor = Boolean.serializer().descriptor

    override fun serialize(encoder: Encoder, value: Boolean) = encoder.encodeBoolean(value)

    override fun deserialize(decoder: Decoder): Boolean {
        if (decoder !is JsonDecoder) return decoder.decodeBoolean()
        val value = decoder.decodeJsonElement()
        if (value is JsonPrimitive && !value.isString) {
            value.booleanOrNull?.let { return it }
        }
        throw SerializationException("Schema flag must be a boolean")
    }
}
