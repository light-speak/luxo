package com.luxo.client

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

class LuxoErrorTest {

    @Test
    fun `constructor sets all properties`() {
        val data = buildJsonObject { put("field", "email") }
        val err = LuxoError(
            error = "NotFound",
            code = 404,
            message = "user not found",
            traceId = "abc-123",
            data = data,
            developmentCause = "database constraint",
        )

        assertEquals("NotFound", err.error)
        assertEquals(404, err.code)
        assertEquals("user not found", err.message)
        assertEquals("abc-123", err.traceId)
        assertEquals(data, err.data)
        assertEquals("database constraint", err.developmentCause)
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
        val exception: Exception = err
        assertTrue(exception is LuxoError)
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
