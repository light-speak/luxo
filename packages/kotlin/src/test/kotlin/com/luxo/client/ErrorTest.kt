package com.luxo.client

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

class LuxoErrorTest {

    @Test
    fun `constructor sets all properties`() {
        val err = LuxoError(
            error = "NotFound",
            code = 404,
            message = "user not found",
            traceId = "abc-123",
        )

        assertEquals("NotFound", err.error)
        assertEquals(404, err.code)
        assertEquals("user not found", err.message)
        assertEquals("abc-123", err.traceId)
    }

    @Test
    fun `traceId defaults to null`() {
        val err = LuxoError(
            error = "Unauthorized",
            code = 401,
            message = "invalid token",
        )

        assertNull(err.traceId)
    }

    @Test
    fun `is an Exception`() {
        val err = LuxoError("InternalError", 500, "something went wrong")
        assertTrue(err is Exception)
        assertEquals("something went wrong", err.message)
    }

    @Test
    fun `toString format`() {
        val err = LuxoError("BadRequest", 400, "missing field")
        assertEquals("LuxoError(BadRequest, 400, missing field)", err.toString())
    }

    @Test
    fun `can be thrown and caught`() {
        try {
            throw LuxoError("Conflict", 409, "duplicate entry", "trace-xyz")
        } catch (e: LuxoError) {
            assertEquals("Conflict", e.error)
            assertEquals(409, e.code)
            assertEquals("duplicate entry", e.message)
            assertEquals("trace-xyz", e.traceId)
        }
    }
}
