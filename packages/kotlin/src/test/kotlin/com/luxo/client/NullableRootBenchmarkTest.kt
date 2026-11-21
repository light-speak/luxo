package com.luxo.client

import kotlin.test.Test

class NullableRootBenchmarkTest {
    @Test
    fun generatedNullableFixturesPreserveTheWireContract() {
        NullableRootBenchmark.main(arrayOf("verify"))
    }
}
