package com.luxo.client

import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertContains

class GeneratedRequestTest {
    @Test fun nonCapturingDelimitedWriterPreservesBoundariesAndRollback() {
        for (size in listOf(0, 1, 127, 128, 16383, 16384)) {
            val payload = ByteArray(size) { 9 }
            val expected = LuxoEncoder(1).apply { writeVarint(7); writeBytes(payload) }.bytes()
            val encoder = LuxoEncoder(1).apply {
                writeVarint(7)
                writeDelimitedValue(payload) { target, value -> target.writeRawBytes(value) }
            }
            assertContentEquals(expected, encoder.bytes())
            val failure = IllegalStateException("writer failed")
            val actual = assertFailsWith<IllegalStateException> {
                encoder.writeDelimitedValue(payload) { target, value ->
                    target.writeDelimitedValue(value) { nested, item -> nested.writeRawBytes(item) }
                    throw failure
                }
            }
            kotlin.test.assertSame(failure, actual)
            assertContentEquals(expected, encoder.bytes())
            encoder.writeDelimitedValue(payload) { target, value -> target.writeRawBytes(value) }
            assertContentEquals(LuxoEncoder(1).apply {
                writeVarint(7); writeBytes(payload); writeBytes(payload)
            }.bytes(), encoder.bytes())
        }
    }

    @Test fun integerListWriterPreservesWireForRandomAndSequentialLists() {
        for (values in listOf(emptyList(), listOf(0L),
            listOf(Long.MIN_VALUE, -65L, -1L, 0L, 64L, Long.MAX_VALUE),
            List(128) { it.toLong() - 64 })) {
            val expected = LuxoEncoder(1).apply {
                writeVarint(values.size.toLong())
                for (value in values) writeSvarint(value)
            }.bytes()
            val random = object : ArrayList<Long>(values) {
                override fun iterator(): MutableIterator<Long> = error("random access must not allocate an iterator")
            }
            val sequential = object : java.util.LinkedList<Long>(values) {
                override fun get(index: Int): Long = error("sequential access must not scan by index")
            }
            for (input in listOf(values, random, sequential)) {
                assertContentEquals(expected, LuxoEncoder(1).apply { writeIntList(input) }.bytes())
            }
        }
    }

    @Test fun generatedIntegerAndDurationListsUseTheTypedWriter() {
        for (type in listOf("Int", "Duration")) {
            val schema = LuxoSchema(apis = mapOf("echo" to LuxoAPI(1, "echo", "test",
                returnType = "Int", params = listOf(LuxoParam(1, "values", type, isList = true)))))
            assertContains(LuxoCodegen.genClient(schema, "test"), "encoder.writeIntList(value)")
        }
    }

    @Test fun generatedModelsPassValuesWithoutCapturingClosures() {
        for (isList in listOf(false, true)) {
            val schema = LuxoSchema(types = mapOf("Input" to LuxoTypeDecl("Input",
                fields = listOf(LuxoField(1, "value", "Int")))),
                apis = mapOf("echo" to LuxoAPI(1, "echo", "test", returnType = "Int",
                    params = listOf(LuxoParam(1, "input", "Model", typeName = "Input", isList = isList)))))
            val source = LuxoCodegen.genClient(schema, "test")
            val value = if (isList) "item" else "value"
            assertContains(source, "encoder.writeDelimitedValue($value) { target, model -> model.writeLuxo(target) }")
            kotlin.test.assertFalse(source.contains("encoder.writeDelimited {"))
        }
    }

    private class Request(private val id: Long,
        override val filters: List<LuxoFilter>? = null,
        override val sorters: List<LuxoSorter>? = null) : GeneratedRequest {
        override val apiID = 7
        override fun writeParameters(encoder: LuxoEncoder) {
            encoder.writeVarint(3)
            encoder.writeSvarint(id)
        }
        override fun toJSONParameters(): Map<String, Any?> = error("JSON on Binary")
    }

    @Test fun typedControlsPreserveWireAndValidation() {
        val filters = listOf(LuxoFilter("id", "eq", Long.MAX_VALUE), LuxoFilter("name", "contains", "中文"))
        val sorters = listOf(LuxoSorter("id", "desc"))
        val schema = APISchemaEntry(7, listOf(ParamSchema(3, "id", "Int")))
        val dynamic = LuxoBinaryProtocol.encodeRequest(schema, mapOf("id" to 42L,
            "\$filters" to filters, "\$sorters" to sorters))
        assertContentEquals(dynamic, LuxoBinaryProtocol.encodeRequest(schema, Request(42, filters, sorters)))
        for (filter in listOf(LuxoFilter("id", "unknown", 1L), LuxoFilter("", "eq", 1L),
            LuxoFilter("score", "eq", Double.NaN))) {
            assertFailsWith<LuxoError> { LuxoBinaryProtocol.encodeRequest(schema, Request(42, listOf(filter))) }
        }
        assertFailsWith<LuxoError> { LuxoBinaryProtocol.encodeRequest(schema, Request(42, List(1001) { filters[0] })) }
        assertFailsWith<LuxoError> { LuxoBinaryProtocol.encodeRequest(schema, Request(42, sorters = List(101) { sorters[0] })) }
    }

    @Test fun directRequestHasIdenticalWireWithoutParameterMetadata() {
        val schema = APISchemaEntry(7, listOf(ParamSchema(3, "id", "Int")))
        val expected = LuxoBinaryProtocol.encodeRequest(schema, mapOf("id" to 9007199254740993L))
        assertContentEquals(expected, LuxoBinaryProtocol.encodeRequest(APISchemaEntry(7), Request(9007199254740993L)))
        assertFailsWith<LuxoError> { LuxoBinaryProtocol.encodeRequest(APISchemaEntry(8), Request(42)) }
    }
}
