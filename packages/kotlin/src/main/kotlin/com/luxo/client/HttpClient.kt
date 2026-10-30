package com.luxo.client

import java.io.IOException
import java.net.InetAddress
import java.net.Socket
import java.util.concurrent.TimeUnit
import javax.net.SocketFactory
import okhttp3.Dispatcher
import okhttp3.OkHttpClient

internal fun defaultHTTPClient(timeoutSeconds: Long, maximum: Int): OkHttpClient =
    OkHttpClient.Builder()
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
