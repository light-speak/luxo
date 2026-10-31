package com.luxo.client

import java.io.File
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class EncoderCapacityTest {
    @Test fun `zero capacity encoder grows without hanging`() {
        val classpath = listOf(EncoderCapacityProbe::class.java, LuxoEncoder::class.java, Unit::class.java)
            .map { File(it.protectionDomain.codeSource.location.toURI()).path }
            .distinct().joinToString(File.pathSeparator)
        val child = ProcessBuilder(
            File(System.getProperty("java.home"), "bin/java").path,
            "-cp", classpath, EncoderCapacityProbe::class.java.name,
        ).redirectErrorStream(true).start()
        try {
            val completed = child.waitFor(5, TimeUnit.SECONDS)
            assertTrue(completed, "zero-capacity encoding did not finish")
            assertEquals(0, child.exitValue(), child.inputStream.bufferedReader().readText())
        } finally {
            if (child.isAlive) {
                child.destroyForcibly()
                assertTrue(child.waitFor(5, TimeUnit.SECONDS), "failed to stop encoder probe")
            }
        }
    }

    @Test fun `negative capacity remains invalid`() {
        assertFailsWith<NegativeArraySizeException> { LuxoEncoder(-1) }
    }
}

object EncoderCapacityProbe {
    @JvmStatic fun main(args: Array<String>) {
        for (capacity in listOf(0, 1, 4, 1024)) {
            val encoder = LuxoEncoder(capacity)
            check(encoder.bytes().isEmpty())
            encoder.writeVarint(128)
            encoder.writeRawBytes(ByteArray(4096) { it.toByte() })
            val expected = byteArrayOf(-128, 1) + ByteArray(4096) { it.toByte() }
            check(encoder.bytes().contentEquals(expected))
            val first = encoder.bytes()
            encoder.writeEnd()
            check(first.contentEquals(expected))
            check(encoder.bytes().contentEquals(expected + byteArrayOf(0)))
        }
    }
}
