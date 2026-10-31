package app

import com.luxo.client.*
import com.luxo.generated.LuxoClient
import kotlinx.serialization.json.*
import okhttp3.*
import okio.ByteString

private typealias StartStream = suspend (LuxoClient, StreamObserver, (Any) -> Unit) -> (() -> Unit)

suspend fun verifyStreamIsolation() {
    val cases = listOf<StartStream>(
        { client, observer, data -> client.subscribeWatchProbeInt(observer, data) },
        { client, observer, data -> client.subscribeWatchProbeString(observer, data) },
        { client, observer, data -> client.subscribeWatchProbeStringList(observer, data) },
        { client, observer, data -> client.subscribeWatchTextRecord(select = "texts", observer = observer, onData = data) },
    )
    for (mode in TransportMode.entries) {
        for (start in cases) StreamFixture(mode).use { verifyDecodeFailure(it, start) }
        StreamFixture(mode).use { fixture ->
            verifyDecodeFailure(fixture, { client, observer, data ->
                client.subscribeWatchTextRecords(select = "enabled", observer = observer, onData = data)
            }, fieldless = true)
        }
        StreamFixture(mode).use { verifyBusinessFailure(it) }
    }
    println("Generated Kotlin streams: scalar/list/model decode failures isolated; application failures unchanged")
}

private suspend fun verifyDecodeFailure(fixture: StreamFixture, start: StartStream, fieldless: Boolean = false) {
    val errors = mutableListOf<LuxoError>()
    var received = 0
    var completed = 0
    val stop = start(fixture.client, StreamObserver({ completed++ }, { errors += it })) { received++ }
    repeat(2) { if (fieldless) fixture.fieldlessList(1) else fixture.data(1, malformed = true) }
    fixture.complete(1)
    stop(); stop()
    var peer = 0L
    val stopPeer = fixture.client.subscribeWatchProbeInt { peer = it }
    fixture.data(2)
    check(errors.single().error == "DecodeError" && received == 0 && completed == 0)
    check(peer == 7L && fixture.client.probeInt() == 7L)
    stopPeer()
}

private suspend fun verifyBusinessFailure(fixture: StreamFixture) {
    val expected = IllegalStateException("application")
    var completed = 0
    val stop = fixture.client.subscribeWatchProbeInt(observer = StreamObserver({ completed++ }, { error("not protocol failure") })) {
        throw expected
    }
    repeat(2) { check(runCatching { fixture.data(1) }.exceptionOrNull() === expected) }
    fixture.complete(1)
    check(completed == 1)
    stop()
}

private class StreamFixture(private val mode: TransportMode) : OkHttpClient(), AutoCloseable {
    private val transport = LuxoWebSocket("ws://localhost/unused", this, autoReconnect = false)
    val client = LuxoClient(transport)
    private lateinit var socket: FixtureSocket

    init {
        transport.setMode(mode)
        transport.connect()
        socket.open()
    }

    override fun newWebSocket(request: Request, listener: WebSocketListener): WebSocket =
        FixtureSocket(request, listener).also { socket = it }

    fun data(id: Int, malformed: Boolean = false) {
        if (mode == TransportMode.JSON) socket.receive("""{"${'$'}stream":$id,"data":${if (malformed) "true" else "7"}}""")
        else socket.receive(byteArrayOf(0x12, id.toByte(), if (malformed) 0x80.toByte() else 14))
    }

    fun complete(id: Int) {
        if (mode == TransportMode.JSON) socket.receive("""{"${'$'}end":$id,"ok":true}""")
        else socket.receive(byteArrayOf(0x15, id.toByte()))
    }

    fun fieldlessList(id: Int) {
        if (mode == TransportMode.JSON) socket.receive("""{"${'$'}stream":$id,"data":[{}]}""")
        else socket.receive(byteArrayOf(0x12, id.toByte(), 1, 0, 0))
    }

    override fun close() {
        transport.close()
        dispatcher.executorService.shutdownNow()
        connectionPool.evictAll()
    }
}

/** Small controlled frames; exercises the real transport without a socket dependency. */
private class FixtureSocket(private val request: Request, private val listener: WebSocketListener) : WebSocket {
    fun open() = listener.onOpen(this, Response.Builder().request(request).protocol(Protocol.HTTP_1_1).code(101).message("upgrade").build())
    fun receive(text: String) = listener.onMessage(this, text)
    fun receive(bytes: ByteArray) = listener.onMessage(this, ByteString.of(*bytes))
    override fun request() = request
    override fun queueSize() = 0L
    override fun close(code: Int, reason: String?) = true
    override fun cancel() = Unit

    override fun send(text: String): Boolean {
        val json = Json.parseToJsonElement(text).jsonObject
        val id = json["\$id"] ?: return true
        if (json.containsKey("\$sub")) receive("""{"${'$'}sub":$id,"ok":true}""")
        else receive("""{"${'$'}id":$id,"data":7}""")
        return true
    }

    override fun send(bytes: ByteString): Boolean {
        val tag = bytes[0].toInt()
        val id = bytes[1]
        check(id >= 0) { "fixture expects a small subscription ID" }
        if (tag == 0x10) receive(byteArrayOf(0x13, id))
        else if (tag == 1) receive(byteArrayOf(2, id, 14))
        else check(tag == 0x11)
        return true
    }
}
