package com.luxo.client

import okio.ByteString
import okio.ByteString.Companion.toByteString
import java.util.function.LongSupplier

/** Compare immutable send snapshots without folding away the socket-owned result. */
object WebSocketBinarySendBenchmark {
    @Volatile private var sent: ByteString = ByteString.EMPTY

    @JvmStatic
    fun main(args: Array<String>) {
        println("JDK ${System.getProperty("java.version")}; 12 warmup pairs, 20 alternating samples; ns/op and B/op")
        for (size in listOf(2, 8, 128, 1024, 72 * 1024)) {
            val frame = ByteArray(size) { it.toByte() }
            check(ByteString.of(*frame) == frame.toByteString())
            PairedJvmBenchmark.compare("immutable binary snapshot $size bytes", LongSupplier {
                sent = ByteString.of(*frame)
                sent.size.toLong()
            }, LongSupplier {
                sent = frame.toByteString()
                sent.size.toLong()
            }, iterations = if (size > 1024) 2_000 else 100_000)
        }
        println("sink=${PairedJvmBenchmark.sink}")
    }
}
