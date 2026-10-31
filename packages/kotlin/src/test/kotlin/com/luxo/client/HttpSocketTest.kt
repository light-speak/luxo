package com.luxo.client

import java.io.IOException
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.net.SocketException
import javax.net.SocketFactory
import java.net.InetSocketAddress
import com.sun.net.httpserver.HttpServer
import kotlinx.coroutines.runBlocking
import okhttp3.Call
import okhttp3.Connection
import okhttp3.EventListener
import okhttp3.OkHttpClient
import okhttp3.Protocol
import kotlin.test.*

class HttpSocketTest {
    @Test
    fun `real HTTP1 connections use default option but preserve injected clients`() = runBlocking {
        val server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
        server.createContext("/") { exchange ->
            exchange.use {
                it.requestBody.use { input -> input.readBytes() }
                val body = "{\"data\":42}".toByteArray()
                it.sendResponseHeaders(200, body.size.toLong())
                it.responseBody.use { output -> output.write(body) }
            }
        }
        server.start()
        try {
            for (defaults in listOf(false, true)) {
                var observed: Boolean? = null
                val builder = if (defaults) defaultHTTPClient(7, 9).newBuilder() else OkHttpClient.Builder()
                val client = builder.eventListener(object : EventListener() {
                    override fun connectionAcquired(call: Call, connection: Connection) {
                        assertEquals(Protocol.HTTP_1_1, connection.protocol())
                        observed = connection.socket().tcpNoDelay
                    }
                }).build()
                try {
                    val transport = OkHttpTransport("http://127.0.0.1:${server.address.port}/", client = client)
                    transport.call("ping")
                    assertEquals(defaults, observed)
                } finally {
                    client.connectionPool.evictAll()
                    client.dispatcher.executorService.shutdown()
                }
            }
        } finally { server.stop(0) }
    }

    @Test
    fun `default client disables Nagle before connecting`() {
        val client = defaultHTTPClient(7, 9)
        client.socketFactory.createSocket().use { socket ->
            assertFalse(socket.isConnected)
            assertTrue(socket.tcpNoDelay)
        }
        assertEquals(7000, client.callTimeoutMillis)
        assertEquals(9, client.dispatcher.maxRequestsPerHost)
        client.dispatcher.executorService.shutdown()
        client.connectionPool.evictAll()
    }

    @Test
    fun `all connected overloads preserve endpoint and disable Nagle`() {
        val loopback = InetAddress.getByName("127.0.0.1")
        ServerSocket(0, 8, loopback).use { server ->
            val factory = HTTPNoDelaySocketFactory()
            val create = listOf<() -> Socket>(
                { factory.createSocket("127.0.0.1", server.localPort) },
                { factory.createSocket(loopback, server.localPort) },
                { factory.createSocket("127.0.0.1", server.localPort, loopback, 0) },
                { factory.createSocket(loopback, server.localPort, loopback, 0) },
            )
            for (operation in create) operation().use { socket ->
                server.accept().use {
                    assertTrue(socket.tcpNoDelay)
                    assertEquals(server.localPort, socket.port)
                    assertEquals(loopback, socket.localAddress)
                }
            }
        }
    }

    @Test
    fun `failed configuration closes socket and preserves failure`() {
        for (closeFails in listOf(false, true)) {
            val failure = SocketException("option rejected")
            val closeFailure = IOException("close rejected")
            var closed = false
            val socket = object : Socket() {
                override fun setTcpNoDelay(on: Boolean) { throw failure }
                override fun close() {
                    closed = true
                    if (closeFails) throw closeFailure
                }
            }
            val delegate = object : SocketFactory() {
                override fun createSocket() = socket
                override fun createSocket(host: String, port: Int) = socket
                override fun createSocket(host: String, port: Int, local: InetAddress, localPort: Int) = socket
                override fun createSocket(host: InetAddress, port: Int) = socket
                override fun createSocket(host: InetAddress, port: Int, local: InetAddress, localPort: Int) = socket
            }
            assertSame(failure, assertFailsWith<SocketException> {
                HTTPNoDelaySocketFactory(delegate).createSocket()
            })
            assertTrue(closed)
            assertEquals(if (closeFails) listOf(closeFailure) else emptyList(), failure.suppressed.toList())
        }
    }
}
