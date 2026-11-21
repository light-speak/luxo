package com.luxo.client

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFails
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.ResponseBody.Companion.toResponseBody

class SealedJSONTest {
    private val cases = mapOf("Value" to SelectionFieldSchema(1, "Outcome.Value", type = "Model"),
        "Pending" to SelectionFieldSchema(2, "Outcome.Pending", type = "Model"))
    private val types = mapOf("Outcome" to cases, "@sealed:Outcome" to cases,
        "Outcome.Value" to mapOf("child" to SelectionFieldSchema(1, "Outcome", type = "Sealed", nullable = true),
            "json" to SelectionFieldSchema(2, type = "JSON"), "number" to SelectionFieldSchema(3, type = "Int")), "Outcome.Pending" to emptyMap(),
        "Box" to mapOf("value" to SelectionFieldSchema(1, "Outcome", type = "Sealed")))
    private fun schema(name: String = "Outcome", list: Boolean = false) = APISchemaEntry(1, types = types,
        responseType = SelectionFieldSchema(0, name, type = if (name == "Outcome") "Sealed" else "Model", isList = list))
    private fun decode(value: String, meta: APISchemaEntry = schema()) = decodeHTTPJSON("{\"data\":$value}".toByteArray(), meta)

    @Test fun `HTTP transport validates sealed raw bodies and preserves error data`() = kotlinx.coroutines.runBlocking {
        for ((body, fails) in listOf("{\"data\":{\"Pending\":{},\"Pending\":{}}}" to true,
            "{\"data\":{\"Other\":{}}}" to true, "{\"data\":{\"Pending\":{}}}" to false)) {
            val client = okhttp3.OkHttpClient.Builder().addInterceptor { chain ->
                okhttp3.Response.Builder().request(chain.request()).protocol(okhttp3.Protocol.HTTP_1_1).code(200).message("OK")
                    .body(body.toResponseBody()).build()
            }.build()
            try {
                val transport = OkHttpTransport("http://owned.test/", client = client, schema = mapOf("echo" to schema()))
                if (fails) assertFails { transport.call("echo") } else transport.call("echo")
            } finally { client.dispatcher.executorService.shutdownNow(); client.connectionPool.evictAll() }
        }
        val error = decodeHTTPJSON("{\"error\":\"BadRequest\",\"code\":400,\"message\":\"bad\",\"data\":{\"arbitrary\":true}}".toByteArray(), schema())
        assertEquals("true", error["data"]!!.jsonObject["arbitrary"].toString())
    }

    @Test fun `sealed raw response rejects duplicate and unknown case occurrences`() {
        for (value in listOf("{}", "{\"Pending\":{},\"Pending\":{}}", "{\"Pending\":{},\"Value\":{}}",
            "{\"Other\":{}}", "{\"Value\":{\"other\":1}}")) assertFails(value) { decode(value) }
        assertFails { decode("{\"value\":{\"Pending\":{},\"Pending\":{}}}", schema("Box")) }
        assertFails { decode("[{\"Pending\":{},\"Pending\":{}}]", schema(list = true)) }
        assertEquals("{}", decode("{\"Pending\":{}}")["data"]!!.jsonObject["Pending"].toString())
        decode("[{\"Pending\":{}}]", schema(list=true))
        decode("{\"Value\":{\"number\":7}}")
        decode("{\"value\":{\"Pending\":{}},\"ignored\":[1]}", schema("Box"))
        assertFails { decode("null") }
    }

    @Test fun `sealed raw response checks overwritten values and independent depth budgets`() {
        var value = "{\"Pending\":{}}"
        repeat(31) { value = "{\"Value\":{\"child\":$value}}" }
        decode(value)
        assertFails { decode("{\"Value\":{\"child\":$value}}") }
        assertFails { decode("{\"Value\":{\"child\":{\"Value\":{\"child\":$value}},\"child\":null}}") }
        val json128 = "[".repeat(128) + "0" + "]".repeat(128)
        decode("{\"Value\":{\"json\":$json128}}")
        assertFails { decode("{\"Value\":{\"json\":[$json128],\"json\":null}}") }
        val result = decode("{\"Value\":{\"child\":{\"Pending\":{}},\"child\":null}}")
        assertEquals("null", result["data"]!!.jsonObject["Value"]!!.jsonObject["child"].toString())
        val ordinary = decodeHTTPJSON("{\"data\":{\"a\":1,\"a\":2}}".toByteArray())
        assertEquals("2", ordinary["data"]!!.jsonObject["a"]!!.jsonPrimitive.content)
    }

    @Test fun ordinaryJSONDoesNotLinkSealedHelper() {
        val origin = APISchemaEntry::class.java.protectionDomain.codeSource.location
        val loader = object : java.net.URLClassLoader(arrayOf(origin), javaClass.classLoader) {
            override fun loadClass(name: String, resolve: Boolean): Class<*> {
                if (name == "com.luxo.client.SealedJSONKt") throw ClassNotFoundException("ordinary path linked sealed helper")
                if (!name.startsWith("com.luxo.client.")) return super.loadClass(name, resolve)
                synchronized(getClassLoadingLock(name)) {
                    val found = findLoadedClass(name) ?: findClass(name)
                    if (resolve) resolveClass(found)
                    return found
                }
            }
        }
        loader.use {
            val schemaClass = loader.loadClass("com.luxo.client.APISchemaEntry")
            val meta = schemaClass.getConstructor(Integer.TYPE, List::class.java, Map::class.java, Map::class.java)
                .newInstance(1, emptyList<ParamSchema>(), emptyMap<String, SelectionFieldSchema>(), emptyMap<String, Map<String, SelectionFieldSchema>>())
            val method = loader.loadClass("com.luxo.client.JsonProtocolKt").getMethod("decodeHTTPJSON", ByteArray::class.java, schemaClass)
            for (schema in listOf(null, meta)) {
                val value = method.invoke(null, """{"data":{"total":7,"total":8}}""".toByteArray(), schema) as kotlinx.serialization.json.JsonObject
                assertEquals("8", value["data"]!!.jsonObject["total"]!!.jsonPrimitive.content)
                for (bytes in listOf("{".toByteArray(), byteArrayOf(0xff.toByte()))) {
                    val error = assertFails { method.invoke(null, bytes, schema) }
                    kotlin.test.assertIs<java.lang.reflect.InvocationTargetException>(error)
                    kotlin.test.assertFalse(error.targetException is LinkageError)
                }
            }
        }
    }
}
