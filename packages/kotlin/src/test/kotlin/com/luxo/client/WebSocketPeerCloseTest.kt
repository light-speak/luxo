package com.luxo.client

import java.io.DataInputStream
import java.net.InetAddress
import java.net.ServerSocket
import java.security.MessageDigest
import java.util.Base64
import kotlinx.coroutines.*
import okhttp3.OkHttpClient
import kotlin.test.*

class WebSocketPeerCloseTest {
    @Test
    fun `real peer close receives reply and fails pending call without timeout`() = runBlocking {
        for (empty in listOf(false, true)) {
            ServerSocket(0, 1, InetAddress.getByName("127.0.0.1")).use { server ->
                server.soTimeout = 3_000
                val peer = async(Dispatchers.IO) {
                    server.accept().use { socket ->
                        socket.soTimeout = 3_000
                        val input = DataInputStream(socket.getInputStream())
                        val headers = readHeaders(input)
                        val key = headers.lineSequence().first { it.startsWith("Sec-WebSocket-Key:", true) }.substringAfter(':').trim()
                        val accept = Base64.getEncoder().encodeToString(MessageDigest.getInstance("SHA-1").digest((key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").toByteArray()))
                        val output = socket.getOutputStream()
                        output.write("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: $accept\r\n\r\n".toByteArray())
                        output.flush()
                        assertEquals(1, readMaskedFrame(input).first)
                        output.write(if (empty) byteArrayOf(0x88.toByte(), 0) else byteArrayOf(0x88.toByte(), 2, 3, 0xe8.toByte()))
                        output.flush()
                        val (opcode, payload) = readMaskedFrame(input)
                        assertEquals(8, opcode)
                        assertTrue(payload.size >= 2)
                        assertEquals(1000, ((payload[0].toInt() and 255) shl 8) or (payload[1].toInt() and 255))
                    }
                }
                val client = OkHttpClient()
                val transport = LuxoWebSocket("ws://127.0.0.1:${server.localPort}", client, 0uL, 2_000)
                try {
                    transport.setSchema(mapOf("echo" to APISchemaEntry(1)))
                    val error = assertFailsWith<LuxoError> { withTimeout(5_000) { transport.call("echo") } }
                    assertEquals("ConnectionError", error.error)
                    withTimeout(5_000) { peer.await() }
                } finally {
                    transport.close()
                    client.dispatcher.executorService.shutdownNow()
                    client.connectionPool.evictAll()
                }
            }
        }
    }

    private fun readHeaders(input: DataInputStream): String {
        val text = StringBuilder()
        while (!text.endsWith("\r\n\r\n")) {
            check(text.length < 8192)
            text.append(input.readUnsignedByte().toChar())
        }
        return text.toString()
    }

    private fun readMaskedFrame(input: DataInputStream): Pair<Int, ByteArray> {
        val opcode = input.readUnsignedByte() and 15
        val header = input.readUnsignedByte()
        check(header and 128 != 0)
        val length = header and 127
        check(length < 126)
        val mask = ByteArray(4).also(input::readFully)
        val payload = ByteArray(length).also(input::readFully)
        for (index in payload.indices) payload[index] = (payload[index].toInt() xor mask[index % 4].toInt()).toByte()
        return opcode to payload
    }
}
