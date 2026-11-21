package com.luxo.client

import java.io.IOException
import java.net.InetAddress
import java.net.Socket
import java.util.concurrent.TimeUnit
import javax.net.SocketFactory
import okhttp3.Dispatcher
import okhttp3.OkHttpClient
import okhttp3.MediaType
import okhttp3.RequestBody
import okio.BufferedSink

// A new explicit SDK call may reuse these bytes, but OkHttp must not replay
// the POST after sending it or receiving a follow-up status such as 408/503.
internal class HTTPRequestBody(private val content: ByteArray, private val type: MediaType) : RequestBody() {
    override fun contentType(): MediaType = type
    override fun contentLength(): Long = content.size.toLong()
    override fun isOneShot(): Boolean = true
    override fun writeTo(sink: BufferedSink) { sink.write(content, 0, content.size) }
}

internal fun defaultHTTPClient(timeoutSeconds: Long, maximum: Int): OkHttpClient =
    OkHttpClient.Builder()
        .followRedirects(false)
        .followSslRedirects(false)
        .socketFactory(HTTPNoDelaySocketFactory())
        .dispatcher(Dispatcher().apply {
            maxRequests = maximum
            maxRequestsPerHost = maximum
        })
        .callTimeout(timeoutSeconds, TimeUnit.SECONDS)
        .connectTimeout(timeoutSeconds, TimeUnit.SECONDS)
        .readTimeout(timeoutSeconds, TimeUnit.SECONDS)
        .writeTimeout(timeoutSeconds, TimeUnit.SECONDS)
        .build()

// Retain the caller's pool, dispatcher and interceptors; never mutate its client.
internal fun directHTTPClient(client: OkHttpClient): OkHttpClient =
    if (!client.followRedirects && !client.followSslRedirects) client
    else client.newBuilder().followRedirects(false).followSslRedirects(false).build()

// Configure once when opening a connection, before OkHttp connects or wraps TLS.
internal class HTTPNoDelaySocketFactory(
    private val delegate: SocketFactory = getDefault(),
) : SocketFactory() {
    private fun configure(socket: Socket): Socket {
        try {
            socket.tcpNoDelay = true
            return socket
        } catch (failure: IOException) {
            try {
                socket.close()
            } catch (closeFailure: IOException) {
                failure.addSuppressed(closeFailure)
            }
            throw failure
        }
    }

    override fun createSocket(): Socket = configure(delegate.createSocket())
    override fun createSocket(host: String, port: Int): Socket = configure(delegate.createSocket(host, port))
    override fun createSocket(host: InetAddress, port: Int): Socket = configure(delegate.createSocket(host, port))
    override fun createSocket(host: String, port: Int, local: InetAddress, localPort: Int): Socket =
        configure(delegate.createSocket(host, port, local, localPort))
    override fun createSocket(host: InetAddress, port: Int, local: InetAddress, localPort: Int): Socket =
        configure(delegate.createSocket(host, port, local, localPort))
}
