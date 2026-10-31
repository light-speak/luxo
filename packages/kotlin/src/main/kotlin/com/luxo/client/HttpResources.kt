package com.luxo.client

import java.util.concurrent.atomic.AtomicInteger
import okhttp3.Response

internal class HTTPRequestCapacity(private val maximum: Int) {
    private val active = AtomicInteger()

    fun acquire(): Boolean {
        while (true) {
            val count = active.get()
            if (count >= maximum) return false
            if (active.compareAndSet(count, count + 1)) return true
        }
    }

    fun release() { active.decrementAndGet() }
}

internal fun readBoundedHTTPBody(response: Response, maximum: Int): ByteArray {
    val body = response.body ?: throw java.io.IOException("missing response body")
    fun tooLarge(): Nothing = throw LuxoError("ParseError", response.code, "HTTP response body exceeds limit")
    val declared = body.contentLength()
    if (declared > maximum) tooLarge()
    val source = body.source()
    // Reuse the response source's buffer; requesting one extra byte detects overflow.
    if (source.request(maximum.toLong() + 1)) tooLarge()
    val result = source.buffer.readByteArray()
    if (declared != -1L && declared != result.size.toLong()) {
        throw java.io.IOException("Content-Length ($declared) and stream length (${result.size}) disagree")
    }
    return result
}
