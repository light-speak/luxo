package app

import com.luxo.client.OkHttpTransport
import com.luxo.client.TransportMode
import com.luxo.generated.LuxoClient
import com.sun.net.httpserver.HttpExchange
import com.sun.net.httpserver.HttpServer
import java.io.File
import java.net.InetSocketAddress
import java.util.concurrent.atomic.AtomicReference
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.OkHttpClient

private const val selection = "value, duration, optional, values"

private data class IntegerCase(val value: Long, val wire: String)
private data class ExchangeCase(val request: ByteArray, val response: ByteArray, val contentType: String)

/** Compiles with the consumer's own toolchain and uses no SDK-internal codec APIs. */
suspend fun main(args: Array<String>) {
    verifyGeneratedInputs()
    verifyJSONProjection()
    verifyScalarResponses()
    verifyStreamIsolation()
    check(System.getProperty("java.class.path").split(File.pathSeparator).none {
        File(it).name.startsWith("kotlin-compiler-embeddable-")
    }) { "the field analyzer compiler must not leak into the SDK runtime" }
    val cases = readCases(File(args.single()))
    ProtocolServer().use { fixture ->
        for (mode in TransportMode.entries) {
            val client = LuxoClient(OkHttpTransport(fixture.endpoint, client = fixture.http, mode = mode))
            for (value in cases) {
                verifyPage(client, fixture, mode, value, cases, optional = null)
                verifyPage(client, fixture, mode, value, cases, optional = value)
            }
        }
    }
    println("Generated Kotlin consumer: ${cases.size * TransportMode.entries.size * 2} exact HTTP round trips passed")
}

private fun readCases(file: File): List<IntegerCase> =
    Json.parseToJsonElement(file.readText()).jsonObject.getValue("int64Cases").jsonArray.map {
        val entry = it.jsonObject
        IntegerCase(entry.getValue("value").jsonPrimitive.content.toLong(), entry.getValue("wire").jsonPrimitive.content)
    }

private suspend fun verifyPage(
    client: LuxoClient,
    fixture: ProtocolServer,
    mode: TransportMode,
    value: IntegerCase,
    cases: List<IntegerCase>,
    optional: IntegerCase?,
) {
    val minimum = cases.single { it.value == Long.MIN_VALUE }
    val metadata = if (value.value > 0) value else cases.single { it.value == Long.MAX_VALUE }
    fixture.expect(exchangeCase(mode, value, minimum, metadata, optional))
    val page = client.precisionPage(
        value = value.value, duration = minimum.value, optional = optional?.value,
        values = listOf(value.value, minimum.value), page = metadata.value, pageSize = metadata.value,
        select = selection,
    )
    fixture.verify()
    check(page.total == metadata.value && page.page == metadata.value && page.pageSize == metadata.value)
    val record = page.items.single()
    check(record.value.value() == value.value)
    check(record.duration.value() == minimum.value)
    check(record.optional.isSelected && record.optional.value() == optional?.value)
    check(record.values.value() == listOf(value.value, minimum.value))
    check(!record.ignored.isSelected)
}

private fun exchangeCase(
    mode: TransportMode,
    value: IntegerCase,
    minimum: IntegerCase,
    metadata: IntegerCase,
    optional: IntegerCase?,
): ExchangeCase {
    val fields = """"value":${value.value},"duration":${minimum.value},"optional":${optional?.value},"values":[${value.value},${minimum.value}]"""
    if (mode == TransportMode.JSON) {
        val request = """{"${'$'}api":"precisionPage",$fields,"page":${metadata.value},"pageSize":${metadata.value},"${'$'}select":"$selection"}"""
        val response = """{"data":{"items":[{$fields}],"total":${metadata.value},"page":${metadata.value},"pageSize":${metadata.value}}}"""
        return ExchangeCase(request.toByteArray(), response.toByteArray(), "application/json")
    }
    // Fixture bytes are composed from Go-verified vectors, never encoded with the client under test.
    val array = "02${value.wire}${minimum.wire}"
    val arrayLength = (array.length / 2).toString(16).padStart(2, '0')
    val nullableWire = optional?.let { "01${it.wire}" } ?: "00"
    val parameters = "01${value.wire}02${minimum.wire}03${nullableWire}04$array"
    val request = "0202010f${parameters}05${metadata.wire}06${metadata.wire}00"
    val response = "010001${value.wire}02${minimum.wire}03${nullableWire}04$arrayLength${array}00" + metadata.wire.repeat(3)
    return ExchangeCase(request.hexToByteArray(), response.hexToByteArray(), "application/x-luxo")
}

private class ProtocolServer : AutoCloseable {
    val http = OkHttpClient()
    private val expected = AtomicReference<ExchangeCase?>()
    private val failure = AtomicReference<Throwable?>()
    private val server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0).apply {
        createContext("/luvia", ::respond)
        start()
    }
    val endpoint = "http://127.0.0.1:${server.address.port}/luvia"

    fun expect(exchange: ExchangeCase) {
        check(expected.compareAndSet(null, exchange)) { "previous fixture request was not received" }
    }

    fun verify() {
        failure.get()?.let { throw AssertionError("HTTP fixture rejected the generated request", it) }
        check(expected.get() == null) { "expected a real HTTP request" }
    }

    private fun respond(exchange: HttpExchange) {
        try {
            val test = checkNotNull(expected.getAndSet(null)) { "unexpected request" }
            check(exchange.requestMethod == "POST")
            check(exchange.requestHeaders.getFirst("Content-Type").startsWith(test.contentType))
            val actual = exchange.requestBody.readAllBytes()
            if (test.contentType == "application/json") {
                check(Json.parseToJsonElement(actual.decodeToString()) == Json.parseToJsonElement(test.request.decodeToString()))
            } else {
                check(actual.contentEquals(test.request)) { "binary request mismatch: ${actual.toHexString()}" }
            }
            exchange.responseHeaders.set("Content-Type", test.contentType)
            exchange.sendResponseHeaders(200, test.response.size.toLong())
            exchange.responseBody.write(test.response)
        } catch (error: Throwable) {
            failure.set(error)
            exchange.sendResponseHeaders(500, -1)
        } finally {
            exchange.close()
        }
    }

    override fun close() {
        server.stop(0)
        http.dispatcher.executorService.shutdownNow()
        http.connectionPool.evictAll()
    }
}
