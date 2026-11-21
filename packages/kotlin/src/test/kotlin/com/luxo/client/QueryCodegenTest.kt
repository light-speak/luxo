package com.luxo.client

import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.decodeFromJsonElement

import kotlinx.serialization.json.Json
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertEquals
import kotlin.test.assertContentEquals
import kotlin.test.assertFails
import java.io.File
import java.nio.file.Files
import java.util.concurrent.TimeUnit

class QueryCodegenTest {
    @Test fun `nullable JSON generated codecs normalize public null while preserving selection and required JSON`() {
        val fields = listOf(LuxoField(1, "optional", "JSON", nullable = true), LuxoField(2, "required", "JSON"),
            LuxoField(3, "values", "JSON", nullable = true, isList = true))
        val schema = LuxoSchema(types = mapOf("Payload" to LuxoTypeDecl("Payload", usage = TypeUsage.INPUT_OUTPUT, fields = fields)),
            sealeds = mapOf("Outcome" to LuxoSealed("Outcome", usage = TypeUsage.INPUT_OUTPUT,
                variants = listOf(LuxoSealedVariant(1, "Value", fields = listOf(LuxoParam(1, "json", "JSON", nullable = true)))))),
            apis = listOf(LuxoAPI(1, "optional", "test", returnType = "JSON", returnNullable = true, stream = true,
                params = listOf(LuxoParam(1, "value", "JSON", nullable = true))),
                LuxoAPI(2, "required", "test", returnType = "JSON"),
                LuxoAPI(3, "defaults", "test", returnType = "JSON", returnNullable = true,
                    params = listOf(LuxoParam(1, "value", "JSON", nullable = true, hasDefault = true))))
                .associateBy { it.name })
        compileAndRun(LuxoCodegen.genClient(schema, "probe"), LuxoCodegen.genTypes(schema, "probe"), """
            package probe
            import com.luxo.client.*
            import kotlinx.serialization.json.*
            import kotlinx.coroutines.runBlocking
            class Probe(var payload: Any = JsonNull) : Transport {
                var encoded = byteArrayOf()
                override suspend fun call(api: String, params: Map<String, Any?>): Any = payload
                override suspend fun call(api: String, request: GeneratedRequest): Any {
                    encoded = LuxoEncoder().also { request.writeParameters(it) }.bytes()
                    return payload
                }
                override suspend fun <T> subscribe(api: String, decoder: (Any) -> T, request: GeneratedRequest,
                    observer: StreamObserver?, handler: (T) -> Unit): () -> Unit {
                    encoded = LuxoEncoder().also { request.writeParameters(it) }.bytes()
                    handler(decoder(payload)); return {}
                }
                override fun setSchema(schema: Map<String, APISchemaEntry>) {}
                override fun setMode(mode: TransportMode) {}
                override fun setToken(token: String) {}
            }
            fun main() = runBlocking {
                fun bytes(value: LuxoBinaryEncodable) = LuxoEncoder().also { value.writeLuxo(it) }.bytes()
                val literal = byteArrayOf(4, 110, 117, 108, 108)
                for (value in listOf(null, JsonNull)) {
                    val encoded = bytes(PayloadInput(value, JsonNull, listOf(JsonNull)))
                    check(encoded.contentEquals(byteArrayOf(0, 1, 0, 2, 4, 110, 117, 108, 108, 3, 1, 1, 4, 110, 117, 108, 108, 0)))
                    check(bytes(OutcomeInput.Value(value)).contentEquals(byteArrayOf(1, 4, 0, 1, 0, 0)))
                }
                val nullRow = byteArrayOf(0, 1, 0, 2) + literal + byteArrayOf(0)
                val legacyRow = byteArrayOf(0, 1, 1) + literal + byteArrayOf(2) + literal + byteArrayOf(0)
                for (row in listOf(nullRow, legacyRow)) {
                    val decoded = decodePayload(LuxoDecoder(row))
                    check(decoded.optional.isSelected && decoded.optional.value() == null)
                    check(decoded.required.value() === JsonNull && !decoded.values.isSelected)
                }
                check(!decodePayload(LuxoDecoder(byteArrayOf(0, 0))).optional.isSelected)
                for (cell in listOf(byteArrayOf(0), byteArrayOf(1) + literal)) {
                    val column = byteArrayOf(1, 0, 1) + cell + byteArrayOf(0)
                    check(decodeColumnarPayload(column).single().optional.value() == null)
                }
                val json = decodeJSONPayload(buildJsonObject { put("optional", JsonNull); put("required", JsonNull) })
                check(json.optional.isSelected && json.optional.value() == null && json.required.value() === JsonNull)
                val transport = Probe()
                val client = LuxoClient(transport)
                for (payload in listOf(JsonNull, byteArrayOf(0), byteArrayOf(1) + literal)) {
                    transport.payload = payload
                    for (value in listOf(null, JsonNull)) {
                        val values = mutableListOf<JsonElement?>()
                        val stop = client.subscribeOptional(value) { values.add(it) }
                        stop()
                        check(values.size == 1 && values.single() == null)
                        check(transport.encoded.contentEquals(byteArrayOf(1, 0)))
                    }
                    check(client.defaults() == null && transport.encoded.isEmpty())
                    check(client.defaults(LuxoOptional.Present(JsonNull)) == null)
                    check(transport.encoded.contentEquals(byteArrayOf(1, 0)))
                }
                for (payload in listOf(JsonNull, literal)) {
                    transport.payload = payload
                    check(client.required() === JsonNull)
                }
                for (invalid in listOf(byteArrayOf(), byteArrayOf(0, 0), byteArrayOf(1))) {
                    transport.payload = invalid
                    check(runCatching { client.defaults() }.isFailure)
                }
            }
        """.trimIndent(), mapOf("Schema.kt" to LuxoCodegen.genSchemaMap(schema, "probe")))
    }

    @Test fun `sealed schema generates real closed input and selected output types`() {
        val fixture = Json.parseToJsonElement(File("../../pkg/lux/codec/testdata/sealed-v1.json").readText()).jsonObject.getValue("schema")
        val schema = Json.decodeFromJsonElement<LuxoSchema>(fixture)
        val source = LuxoCodegen.genTypes(schema, "probe")
        assertContains(source, "sealed interface Outcome")
        assertContains(source, "sealed interface OutcomeInput")
        assertContains(source, "class `Success`")
        assertFalse(source.contains("data class Success"))
        assertEquals(false, KotlinSealedCodegen.hasResponseSealed(null,schema))
        assertEquals(false, KotlinSealedCodegen.hasResponseSealed("Int",schema))
        assertEquals(false, KotlinSealedCodegen.hasResponseSealed("Int",LuxoSchema()))
        assertEquals(true, KotlinSealedCodegen.hasResponseSealed("Outcome",schema))
    }
    @Test fun `sealed actual Go vectors compile as closed static cases`() {
        val schema = Json.decodeFromString<LuxoSchema>(File("src/test/fixtures/SealedGoSchema.json").readText())
        val wire = File("src/test/fixtures/SealedGoWire.json").readText()
        compileAndRun("package probe", LuxoCodegen.genTypes(schema, "probe"), """
            package probe
            import com.luxo.client.*
            import kotlinx.serialization.json.*
            fun main() {
                val vectors = Json.parseToJsonElement(${Json.encodeToString(kotlinx.serialization.serializer<String>(), wire)}).jsonObject.getValue("vectors").jsonArray
                for (row in vectors) {
                    val objectValue = row.jsonObject
                    val bytes = objectValue.getValue("raw").jsonArray.map { it.jsonPrimitive.int.toByte() }.toByteArray()
                    val binary = decodeOutcome(LuxoDecoder(bytes))
                    val json = decodeJSONOutcome(objectValue.getValue("json").jsonObject)
                    check(binary::class == json::class)
                    when (binary) {
                        is Outcome.Success -> check(binary.value.value() == 7L)
                        is Outcome.Failure -> check(binary.reason.value() == "failed")
                        is Outcome.Pending -> Unit
                        is Outcome.Nested -> check(binary.child.isSelected && binary.history.isSelected)
                    }
                }
                val value: OutcomeInput = OutcomeInput.Success(7L)
                check(value.toLuxoJson().toString() == "{\"Success\":{\"value\":7}}")
                check(LuxoEncoder().apply { value.writeLuxo(this) }.bytes().contentEquals(byteArrayOf(1,4,0,1,14,0)))
                check(OutcomeInput.Failure().toLuxoJson().toString() == "{\"Failure\":{}}")
                check(OutcomeInput.Nested(child = LuxoOptional.Present(null)).toLuxoJson().toString() == "{\"Nested\":{\"child\":null}}")
                check(!(decodeJSONOutcome(Json.parseToJsonElement("{\"Success\":{}}").jsonObject) as Outcome.Success).value.isSelected)
                for (bad in listOf("{}", "{\"Unknown\":{}}", "{\"Success\":{},\"Pending\":{}}", "{\"Success\":{\"unknown\":1}}")) {
                    check(runCatching { decodeJSONOutcome(Json.parseToJsonElement(bad).jsonObject) }.isFailure)
                }
                for (bad in listOf(byteArrayOf(0,2,0,0), byteArrayOf(9,2,0,0), byteArrayOf(1,2,0), byteArrayOf(3,2,0,0,0))) {
                    check(runCatching { decodeOutcome(LuxoDecoder(bad)) }.isFailure)
                }
                val unknown = runCatching { decodeJSONOutcome(Json.parseToJsonElement("{\"Success\":{\"value\":\"bad\",\"unknown\":1}}").jsonObject) }.exceptionOrNull()
                check(unknown is IllegalArgumentException && unknown.message == "unknown Outcome.Success field")
                val childError = runCatching { decodeJSONOutcome(Json.parseToJsonElement("{\"Nested\":{\"child\":9}}").jsonObject) }.exceptionOrNull()
                val reversed = runCatching { decodeJSONOutcome(Json.parseToJsonElement("{\"Nested\":{\"history\":9,\"child\":9}}").jsonObject) }.exceptionOrNull()
                check(childError != null && reversed != null && childError::class == reversed::class && childError.message == reversed.message)
                val empty: List<EmptyOutcomeInput> = emptyList()
                check(empty.isEmpty())
                var deep: OutcomeInput = OutcomeInput.Pending()
                repeat(31) { deep = OutcomeInput.Nested(child = LuxoOptional.Present(deep)) }
                deep.toLuxoJson(); LuxoEncoder().also { deep.writeLuxo(it) }
                deep = OutcomeInput.Nested(child = LuxoOptional.Present(deep))
                check(runCatching { deep.toLuxoJson() }.isFailure)
                check(runCatching { LuxoEncoder().also { deep.writeLuxo(it) } }.isFailure)
            }
        """.trimIndent(), extraSources = mapOf("Schema.kt" to LuxoCodegen.genSchemaMap(schema, "probe"),
            "Client.kt" to LuxoCodegen.genClient(schema, "probe")))
    }
    @Test fun `sealed explicit input output and unused usage controls public declarations`() {
        val owners = listOf("Incoming" to TypeUsage.INPUT,"Outgoing" to TypeUsage.OUTPUT,"Dormant" to TypeUsage.UNUSED)
            .associate { (name,usage) -> name to LuxoSealed(name,usage=usage,variants=listOf(LuxoSealedVariant(1,"Pending"))) }
        val source = LuxoCodegen.genTypes(LuxoSchema(sealeds=owners),"probe")
        assertContains(source,"sealed interface Incoming : LuxoBinaryEncodable")
        assertContains(source,"sealed interface Outgoing {")
        assertFalse(source.contains("sealed interface Dormant"))
        assertFalse(source.contains("sealed interface OutgoingInput"))
        compileAndRun("package probe",source,"""
            package probe
            import com.luxo.client.*
            import kotlinx.serialization.json.*
            fun main() {
                val value: Incoming = Incoming.Pending()
                check(LuxoEncoder().apply { value.writeLuxo(this) }.bytes().contentEquals(byteArrayOf(1,2,0,0)))
                check(decodeOutgoing(LuxoDecoder(byteArrayOf(1,2,0,0))) is Outgoing.Pending)
                check(decodeJSONOutgoing(Json.parseToJsonElement("{\"Pending\":{}}").jsonObject) is Outgoing.Pending)
            }
        """.trimIndent())
    }

    @Test fun `sealed model fields and column cells use framed sums and a reachable response graph`() {
        val base = Json.decodeFromString<LuxoSchema>(File("src/test/fixtures/SealedGoSchema.json").readText())
        val wrapper = LuxoTypeDecl("Wrapper",usage=TypeUsage.OUTPUT,fields=listOf(
            LuxoField(1,"loop","Model",typeName="Wrapper",nullable=true),
            LuxoField(2,"result","Sealed",typeName="Outcome"), LuxoField(3,"history","Sealed",typeName="Outcome",isList=true,nullable=true)))
        val schema = base.copy(types=mapOf("Wrapper" to wrapper),apis=base.apis+
            ("readWrapper" to LuxoAPI(9,"readWrapper","test",returnType="Wrapper")))
        val directory = LuxoCodegen.genSchemaMap(schema,"probe")
        assertContains(directory,"responseType = SelectionFieldSchema(0, \"Wrapper\", type = \"Model\"")
        compileAndRun("package probe", LuxoCodegen.genTypes(schema,"probe"), """
            package probe
            import com.luxo.client.*
            import kotlinx.serialization.json.*
            fun main() {
                val sum = byteArrayOf(1,4,0,1,14,0)
                val pending = byteArrayOf(3,2,0,0)
                val list = LuxoEncoder().apply { writeArrayHeader(1);writeBytes(pending) }.bytes()
                val row = LuxoEncoder().apply { writeVarint(0);writeVarint(2);writeBytes(sum)
                    writeVarint(3);writeBool(true);writeRawBytes(list);writeEnd() }.bytes()
                val value = decodeWrapper(LuxoDecoder(row))
                check((value.result.value() as Outcome.Success).value.value()==7L)
                check(value.history.value()!!.single() is Outcome.Pending && !value.loop.isSelected)
                val columns = LuxoEncoder().apply { writeVarint(1);writeVarint(0);writeVarint(2);writeBytes(sum)
                    writeVarint(3);writeBool(true);writeBytes(list);writeEnd() }.bytes()
                val column = decodeColumnarWrapper(columns).single()
                check((column.result.value() as Outcome.Success).value.value()==7L)
                check(column.history.value()!!.single() is Outcome.Pending)
                val duplicate = LuxoEncoder().apply { writeVarint(1);writeVarint(0)
                    writeVarint(2);writeBytes(byteArrayOf(0,2,0,0));writeVarint(2);writeBytes(sum);writeEnd() }.bytes()
                check(runCatching { decodeColumnarWrapper(duplicate) }.isFailure)
                val json = decodeJSONWrapper(Json.parseToJsonElement("{\"result\":{\"Success\":{\"value\":7}},\"history\":[{\"Pending\":{}}]}").jsonObject)
                check(json.history.value()!!.single() is Outcome.Pending)
            }
        """.trimIndent(),extraSources=mapOf("Schema.kt" to directory,"Client.kt" to LuxoCodegen.genClient(schema,"probe")))
    }

    @Test fun `sealed payload defaults required nullable and model framing stay static`() {
        val params = listOf(LuxoParam(1,"child","Model",typeName="Thing"),
            LuxoParam(2,"history","Model",typeName="Thing",isList=true),
            LuxoParam(3,"numbers","Int",isList=true), LuxoParam(4,"state","Enum",typeName="State"),
            LuxoParam(5,"required","String",nullable=true), LuxoParam(6,"defaulted","String",hasDefault=true),
            LuxoParam(7,"optional","String",nullable=true,hasDefault=true))
        val schema = LuxoSchema(types=mapOf("Thing" to LuxoTypeDecl("Thing",usage=TypeUsage.INPUT_OUTPUT,
            fields=listOf(LuxoField(1,"n","Int")))), enums=mapOf("State" to LuxoEnum("State",values=listOf("OPEN"))),
            sealeds=mapOf("Packet" to LuxoSealed("Packet",usage=TypeUsage.INPUT_OUTPUT,
                variants=listOf(LuxoSealedVariant(1,"Record",params)))),
            apis=mapOf("echo" to LuxoAPI(1,"echo","test",returnType="Packet",
                params=listOf(LuxoParam(1,"value","Sealed",typeName="Packet")))))
        val source = LuxoCodegen.genTypes(schema,"probe")
        assertContains(source,"val `required`: String?,")
        assertFalse(source.contains("val `required`: String? ="))
        compileAndRun("package probe", source, """
            package probe
            import com.luxo.client.*
            import kotlinx.serialization.json.*
            fun row(n: Long) = LuxoEncoder().apply { writeVarint(0);writeVarint(1);writeSvarint(n);writeEnd() }.bytes()
            fun main() {
                val value = PacketInput.Record(ThingInput(7),listOf(ThingInput(8)),listOf(1,2),State.Known.OPEN,null)
                val bytes = LuxoEncoder().apply { value.writeLuxo(this) }.bytes()
                val outer = LuxoDecoder(bytes)
                check(outer.readVarint()==1L)
                val dec = LuxoDecoder(outer.readBytes());outer.checkComplete();dec.skipArenaHeader()
                check(dec.nextField() && dec.fieldID==1);dec.skipArenaHeader()
                check(dec.nextField() && dec.fieldID==1 && dec.readInt()==7L);check(!dec.nextField())
                check(dec.nextField() && dec.fieldID==2 && dec.readVarint()==1L);dec.skipArenaHeader()
                check(dec.nextField() && dec.fieldID==1 && dec.readInt()==8L);check(!dec.nextField())
                check(dec.nextField() && dec.fieldID==3 && dec.readArray { dec.readInt() }==listOf(1L,2L))
                check(dec.nextField() && dec.fieldID==4 && dec.readString()=="OPEN")
                check(dec.nextField() && dec.fieldID==5 && !dec.readBool());check(!dec.nextField());dec.checkComplete()
                val json = value.toLuxoJson().jsonObject.getValue("Record").jsonObject
                check(json.getValue("required") is JsonNull && "defaulted" !in json && "optional" !in json)
                val column = LuxoEncoder().apply { writeVarint(1);writeVarint(0);writeVarint(1);writeSvarint(8);writeEnd() }.bytes()
                val output = LuxoEncoder().apply { writeVarint(1);writeDelimited { payload ->
                    payload.writeVarint(4);payload.writeVarint(1);payload.writeBytes(row(7))
                    payload.writeVarint(2);payload.writeBytes(column)
                    payload.writeVarint(3);payload.writeArrayHeader(2);payload.writeSvarint(1);payload.writeSvarint(2)
                    payload.writeVarint(4);payload.writeString("OPEN");payload.writeVarint(5);payload.writeBool(false);payload.writeEnd()
                } }.bytes()
                val result = decodePacket(LuxoDecoder(output)) as Packet.Record
                check(result.child.value().n.value()==7L && result.history.value().single().n.value()==8L)
                check(result.numbers.value()==listOf(1L,2L) && result.state.value()==State.Known.OPEN)
                check(result.required.isSelected && result.required.value()==null && !result.defaulted.isSelected && !result.optional.isSelected)
                check(decodeJSONPacket(buildJsonObject { put("Record",json) }) is Packet.Record)
            }
        """.trimIndent())
    }

    @Test fun `Unicode queries compile and retain original wire names`() {
        val fixture = Json.decodeFromString<LuxoSchema>("""{"models":{},"apis":{"café":{"id":1,"name":"café","module":"test","returnType":"Int","paginated":true,"query":{"filters":[{"field":"𐐀_name","type":"String","operators":["eq"]}],"sorters":["中文2"]}}}}""")
        compileAndRun(KotlinQueryCodegen.generate(fixture, "probe"), LuxoCodegen.genTypes(fixture, "probe"), """
          package probe
          fun main() {
            check(LuxoQuery_café.𐐀_name_eq("coffee").field == "𐐀_name")
            check(LuxoQuery_café.中文2_desc().field == "中文2")
          }
        """.trimIndent())
    }
    @Test fun `overwritten columns still validate scalar arrays and JSON list elements`() {
        val schema = LuxoSchema(types = mapOf("Packet" to LuxoTypeDecl("Packet", usage = TypeUsage.OUTPUT,
            fields = listOf(LuxoField(1, "numbers", "Int", isList = true, nullable = true),
                LuxoField(2, "values", "JSON", isList = true)))))
        compileAndRun("package probe", LuxoCodegen.genTypes(schema, "probe"), """
            package probe
            import com.luxo.client.*
            fun column(field: Int, previous: ByteArray, page: Boolean): ByteArray = LuxoEncoder().apply {
                writeVarint(1); writeVarint(0); writeVarint(field.toLong())
                if (field == 1) writeBool(true)
                writeBytes(previous); writeVarint(field.toLong())
                if (field == 1) writeBool(false) else writeBytes(byteArrayOf(0))
                writeEnd(); if (page) { writeSvarint(1); writeSvarint(1); writeSvarint(1) }
            }.bytes()
            fun main() {
                for (page in listOf(false, true)) for (valid in listOf(false, true)) for (field in 1..2) {
                    val cell = LuxoEncoder().apply {
                        writeArrayHeader(1)
                        if (field == 1) { writeSvarint(1); if (!valid) writeBool(false) }
                        else { val depth = if (valid) 128 else 129; writeString("[".repeat(depth) + "0" + "]".repeat(depth)) }
                    }.bytes()
                    val result = runCatching {
                        val bytes = column(field, cell, page)
                        val value = if (page) decodePaginatedPacket(bytes).items.single() else decodeColumnarPacket(bytes).single()
                        if (field == 1) check(value.numbers.value() == null) else check(value.values.value().isEmpty())
                    }
                    check(result.isSuccess == valid) { "field=${'$'}field page=${'$'}page valid=${'$'}valid ${'$'}result" }
                }
            }
        """.trimIndent())
    }

    @Test fun `generated model readers and writers enforce 64 object layers`() {
        fun fields(child: String) = listOf(LuxoField(1, "value", "Int"),
            LuxoField(2, "child", child, nullable = true), LuxoField(3, "children", child, isList = true),
            LuxoField(4, "payload", "JSON", nullable = true))
        val fixture = LuxoSchema(models = mapOf("Node" to LuxoModel("Node", fields = fields("Branch"), usage = TypeUsage.INPUT_OUTPUT)),
            types = mapOf("Branch" to LuxoTypeDecl("Branch", fields = fields("Node"), usage = TypeUsage.INPUT_OUTPUT)),
            apis = listOf(LuxoAPI(1, "one", "test", returnType = "Node", params = listOf(LuxoParam(1, "value", "Model", typeName = "Node"))),
                LuxoAPI(2, "many", "test", returnType = "Node", returnList = true),
                LuxoAPI(3, "page", "test", returnType = "Node", returnList = true, paginated = true),
                LuxoAPI(4, "watch", "test", returnType = "Node", stream = true)).associateBy { it.name })
        compileAndRun(LuxoCodegen.genClient(fixture, "probe"), LuxoCodegen.genTypes(fixture, "probe"),
            File("src/test/fixtures/ModelDepthConsumer.kt.txt").readText() + "\n" +
                File("benchmark/ModelDepthBenchmark.kt.txt").readText(),
            mapOf("Schema.kt" to LuxoCodegen.genSchemaMap(fixture, "probe")))
    }

    @Test fun `generated JSON scalar list and stream consumers reject excess containers`() {
        val fixture = LuxoSchema(apis = listOf(
            LuxoAPI(1, "one", "test", returnType = "JSON", params = listOf(LuxoParam(1, "value", "JSON"))),
            LuxoAPI(2, "many", "test", returnType = "JSON", returnList = true),
            LuxoAPI(3, "watch", "test", returnType = "JSON", stream = true),
        ).associateBy { it.name })
        compileAndRun(LuxoCodegen.genClient(fixture, "probe"), LuxoCodegen.genTypes(fixture, "probe"), """
            package probe
            import com.luxo.client.*
            import kotlinx.serialization.json.*
            import kotlinx.coroutines.runBlocking
            class Probe(var payload: Any) : Transport {
                override suspend fun call(api: String, params: Map<String, Any?>): Any = payload
                override suspend fun call(api: String, request: GeneratedRequest): Any {
                    request.writeParameters(LuxoEncoder()); return payload
                }
                override suspend fun <T> subscribe(api: String, decoder: (Any) -> T, request: GeneratedRequest,
                    observer: StreamObserver?, handler: (T) -> Unit): () -> Unit {
                    handler(decoder(payload)); return {}
                }
                override fun setSchema(schema: Map<String, APISchemaEntry>) {}
                override fun setMode(mode: TransportMode) {}
                override fun setToken(token: String) {}
            }
            fun main() = runBlocking {
                for (depth in 127..129) for (binary in listOf(false, true)) {
                    val raw = "[".repeat(depth - 1) + "[]" + "]".repeat(depth - 1)
                    val value = Json.parseToJsonElement(raw)
                    val wire = LuxoEncoder().apply { writeString(raw) }.bytes()
                    val probe = Probe(if (binary) wire else value)
                    val client = LuxoClient(probe)
                    val scalar = runCatching { client.one(JsonNull) }
                    check(scalar.isSuccess == (depth <= 128))
                    if (depth <= 128) check(scalar.getOrThrow() == value)
                    val request = runCatching { client.one(value) }
                    check(request.isSuccess == (depth <= 128))
                    var delivered = false
                    val stream = runCatching { client.subscribeWatch { delivered = true; check(it == value) } }
                    check(stream.isSuccess == (depth <= 128) && delivered == (depth <= 128))
                    probe.payload = if (binary) LuxoEncoder().apply { writeArrayHeader(1); writeString(raw) }.bytes()
                        else JsonArray(listOf(value))
                    val list = runCatching { client.many() }
                    check(list.isSuccess == (depth <= 128))
                    if (depth <= 128) check(list.getOrThrow() == listOf(value))
                }
            }
        """.trimIndent(), mapOf("Schema.kt" to LuxoCodegen.genSchemaMap(fixture, "probe")))
    }

    @Test fun `generated JSON fields retain independent depth in both encodings`() {
        val fixture = LuxoSchema(types = mapOf("Packet" to LuxoTypeDecl("Packet", usage = TypeUsage.INPUT_OUTPUT,
            fields = listOf(LuxoField(1, "value", "JSON"), LuxoField(2, "values", "JSON", isList = true)))))
        compileAndRun("package probe", LuxoCodegen.genTypes(fixture, "probe"), """
            package probe
            import com.luxo.client.*
            import kotlinx.serialization.json.*
            fun verify(valid: Boolean, body: () -> Unit) {
                try { body(); check(valid) { "deep generic JSON was accepted" } }
                catch (failure: IllegalArgumentException) { check(!valid) { failure.toString() } }
            }
            fun main() {
                for (depth in 127..129) for (leaf in listOf("0", "[]", "{}", "1e400")) {
                    val count = depth - if (leaf == "[]" || leaf == "{}") 1 else 0
                    val raw = "[".repeat(count) + leaf + "]".repeat(count)
                    val value = Json.parseToJsonElement(raw)
                    val input = PacketInput(value, listOf(value))
                    verify(depth <= 128) {
                        val enc = LuxoEncoder(); input.writeLuxo(enc)
                        val output = decodePacket(LuxoDecoder(enc.bytes()))
                        check(output.value.value() == value && output.values.value() == listOf(value))
                    }
                    verify(depth <= 128) { input.toLuxoJson() }
                    verify(depth <= 128) {
                        val model = buildJsonObject { put("value", value); put("values", JsonArray(listOf(value))) }
                        check(decodeJSONPacket(model).value.value() == value)
                    }
                    for (list in listOf(false, true)) verify(depth <= 128) {
                        val row = LuxoEncoder().apply {
                            writeVarint(0); writeVarint(if (list) 2 else 1)
                            if (list) writeArrayHeader(1)
                            writeString(raw); writeEnd()
                        }
                        decodePacket(LuxoDecoder(row.bytes()))
                    }
                    for (list in listOf(false, true)) verify(depth <= 128) {
                        val cell = if (list) LuxoEncoder().apply { writeArrayHeader(1); writeString(raw) }.bytes()
                            else raw.toByteArray(Charsets.UTF_8)
                        val column = LuxoEncoder().apply {
                            writeVarint(1); writeVarint(0); writeVarint(if (list) 2 else 1)
                            writeBytes(cell); writeEnd()
                        }
                        decodeColumnarPacket(column.bytes())
                    }
                }
            }
        """.trimIndent())
    }

    @Test
    fun `input helper aliases preserve independent business types`() {
        val schema = Json.decodeFromString<LuxoSchema>("""{"models":{"User":{"name":"User","usage":"inputOutput","fields":[{"id":1,"name":"id","type":"Int"},{"id":2,"name":"parent","type":"Model","typeName":"User","nullable":true}]},"LuxoUser":{"name":"LuxoUser","usage":"inputOutput","fields":[{"id":1,"name":"id","type":"Int"}]}},"types":{"UserInput":{"name":"UserInput","usage":"output","fields":[{"id":1,"name":"label","type":"String"}]},"LuxoUserInput2":{"name":"LuxoUserInput2","usage":"output","fields":[{"id":1,"name":"label","type":"String"}]}},"apis":{"readUser":{"id":1,"name":"readUser","module":"test","returnType":"User"},"writeUser":{"id":2,"name":"writeUser","module":"test","returnType":"Int","params":[{"id":1,"name":"input","type":"Model","typeName":"User"}]}}}""")
        compileAndRun(LuxoCodegen.genClient(schema, "probe"), LuxoCodegen.genTypes(schema, "probe"),
            LuxoCodegen.genSchemaMap(schema, "probe") + """
                suspend fun accept(client: LuxoClient, input: LuxoUserInput3): Long = client.writeUser(input = input)
                fun main() {
                    val input = LuxoUserInput3(1L, LuxoUserInput3(2L, null))
                    val encoder = com.luxo.client.LuxoEncoder()
                    input.writeLuxo(encoder)
                    check(encoder.bytes().contentEquals(byteArrayOf(0,1,2,2,1,0,1,4,2,0,0,0)))
                    check(input.parent!!.id == 2L)
                    check(UserInput(label = com.luxo.client.Selected.value("ok")).label.value() == "ok")
                }
            """.trimIndent())
    }

    @Test fun `generated array writers reject unreadable lists before iterating`() {
        val fixture = LuxoSchema(types = mapOf("Input" to LuxoTypeDecl("Input", usage = TypeUsage.INPUT_OUTPUT,
            fields = listOf(LuxoField(1,"flags","Boolean",isList=true)))), apis = mapOf(
            "save" to LuxoAPI(7,"save","test",returnType="Int",params=listOf(
                LuxoParam(1,"flags","Boolean",isList=true),LuxoParam(2,"input","Model",typeName="Input"))),
            "ints" to LuxoAPI(8,"ints","test",returnType="Int",params=listOf(LuxoParam(1,"values","Int",isList=true))),
        ))
        val consumer = """
            fun runGeneratedArrayBoundary() {
                val booleans = object : AbstractList<Boolean>() {
                    override val size: Int = 1_000_001
                    override fun get(index: Int): Boolean = error("oversized boolean list was read")
                }
                val integers = object : AbstractList<Long>() {
                    override val size: Int = 1_000_001
                    override fun get(index: Int): Long = error("oversized integer list was read")
                }
                val requests = listOf(
                    _saveRequest(booleans, InputInput(emptyList())),
                    _saveRequest(emptyList(), InputInput(booleans)),
                    _intsRequest(integers),
                )
                for (request in requests) {
                    var rejected = false
                    try { request.writeParameters(LuxoEncoder()) }
                    catch (failure: LuxoCodecException) { rejected = true }
                    check(rejected) { "oversized generated request was accepted" }
                }
            }
        """.trimIndent()
        compileAndRun(LuxoCodegen.genClient(fixture,"probe") + "\n" + consumer,
            LuxoCodegen.genTypes(fixture,"probe"), LuxoCodegen.genSchemaMap(fixture,"probe") + "\nfun main() { runGeneratedArrayBoundary() }")
    }

    @Test fun `builtin scalar types remain precise beside matching enum names`() {
        val fields = listOf(
            LuxoField(1, "count", "Int"), LuxoField(2, "score", "Float"),
            LuxoField(3, "counts", "Int", isList = true),
            LuxoField(4, "scores", "Float", nullable = true, isList = true),
            LuxoField(5, "state", "Enum", typeName = "Long"),
            LuxoField(6, "kind", "Enum", typeName = "Double"),
        )
        val fixture = LuxoSchema(
            enums = listOf("Long", "Double").associateWith { LuxoEnum(it, listOf("OPEN")) },
            types = mapOf("Record" to LuxoTypeDecl("Record", usage = TypeUsage.INPUT_OUTPUT, fields = fields)),
            apis = listOf(
                LuxoAPI(1, "echo", "test", returnType = "Record", params = listOf(LuxoParam(1, "input", "Model", typeName = "Record"))),
                LuxoAPI(2, "count", "test", returnType = "Int", params = listOf(LuxoParam(1, "value", "Int"))),
                LuxoAPI(3, "score", "test", returnType = "Float", params = listOf(LuxoParam(1, "value", "Float"))),
                LuxoAPI(4, "state", "test", returnType = "Long", params = listOf(LuxoParam(1, "value", "Enum", typeName = "Long"))),
                LuxoAPI(5, "rows", "test", returnType = "Record", returnList = true, paginated = true,
                    query = LuxoQueryCapabilities(filters = listOf(
                        LuxoQueryFilter("count", "Int", listOf("eq")), LuxoQueryFilter("score", "Float", listOf("eq")),
                        LuxoQueryFilter("state", "Long", listOf("eq")),
                    ))),
            ).associateBy { it.name },
        )
        compileAndRun(KotlinQueryCodegen.generate(fixture, "probe"), LuxoCodegen.genTypes(fixture, "probe"), """
            package probe
            import com.luxo.client.*
            fun main() {
                val count = 9007199254740993L
                val input = RecordInput(count, 1.25, listOf(count), listOf(1.25), Long.Known.OPEN, Double.fromValue("FUTURE"))
                val encoder = LuxoEncoder()
                input.writeLuxo(encoder)
                val value = decodeRecord(LuxoDecoder(encoder.bytes()))
                check(value.count.value() == count && value.score.value() == 1.25)
                check(value.counts.value() == listOf(count) && value.scores.value() == listOf(1.25))
                check(value.state.value() == Long.Known.OPEN && value.kind.value().rawValue == "FUTURE")
                check(LuxoQuery_rows.count_eq(count).value == count)
                check(LuxoQuery_rows.score_eq(1.25).value == 1.25)
                check(LuxoQuery_rows.state_eq(Long.Known.OPEN).value == "OPEN")
            }
        """.trimIndent(), mapOf(
            "LuxoClient.kt" to LuxoCodegen.genClient(fixture, "probe"),
            "Schema.kt" to LuxoCodegen.genSchemaMap(fixture, "probe"),
        ))
    }

    @Test fun `valid enum names matching Kotlin primitives initialize and serialize`() {
        val names = listOf("Byte", "Short", "Char", "Long", "Double")
        val fixture = LuxoSchema(enums = names.associateWith { LuxoEnum(it, listOf("OPEN")) })
        val current = LuxoCodegen.genTypes(fixture, "probe")
        val previous = names.fold(current) { code, name -> code.replace(
            "object Serializer : kotlinx.serialization.KSerializer<${name}> {\n        override val descriptor = kotlin.String.serializer().descriptor",
            "object Serializer : kotlinx.serialization.KSerializer<${name}> {\n        override val descriptor = kotlinx.serialization.descriptors.PrimitiveSerialDescriptor(\"$name\", kotlinx.serialization.descriptors.PrimitiveKind.STRING)",
        ) }
        assertNotEquals(current, previous)
        val prefix = "package probe\nimport kotlinx.serialization.json.Json\nfun main() {\n"
        val oldBody = names.joinToString("\n") { name -> """
            run {
                val failure = runCatching { $name.Serializer.descriptor }.exceptionOrNull()
                check(failure is ExceptionInInitializerError)
                check(failure.cause is IllegalArgumentException)
                check(failure.cause?.message.orEmpty().contains("serial descriptor"))
            }
        """.trimIndent() }
        compileAndRun("package probe", previous, prefix + oldBody + "\n}")
        val newBody = names.joinToString("\n") { name -> """
            run {
                for (raw in listOf("OPEN", "FUTURE")) {
                    val value = $name.fromValue(raw)
                    val wire = Json.encodeToString($name.Serializer, value)
                    check(wire == "\"" + raw + "\"")
                    check(Json.decodeFromString($name.Serializer, wire).rawValue == raw)
                }
            }
        """.trimIndent() }
        compileAndRun("package probe", current, prefix + newBody + "\n}")
    }

    @Test fun `generated enum native descriptors preserve JSON and model Binary values`() {
        val fixture = LuxoSchema(
            enums = mapOf("State" to LuxoEnum("State", listOf("OPEN", "name", "rawValue"))),
            types = mapOf("Payload" to LuxoTypeDecl("Payload", usage = TypeUsage.INPUT_OUTPUT, fields = listOf(
                LuxoField(1, "state", "Enum", typeName = "State"),
                LuxoField(2, "optional", "Enum", typeName = "State", nullable = true),
                LuxoField(3, "states", "Enum", typeName = "State", isList = true),
            ))),
            apis = mapOf("echo" to LuxoAPI(1, "echo", "test", returnType = "Payload",
                params = listOf(LuxoParam(1, "input", "Model", typeName = "Payload")))),
        )
        compileAndRun("package probe", LuxoCodegen.genTypes(fixture, "probe"), """
            package probe
            import com.luxo.client.*
            import kotlinx.serialization.json.*
            import kotlinx.serialization.descriptors.PrimitiveKind
            @OptIn(kotlinx.serialization.ExperimentalSerializationApi::class)
            fun main() {
                check(State.Serializer.descriptor.kind == PrimitiveKind.STRING)
                check(State.Serializer.descriptor.serialName == "kotlin.String")
                for (raw in listOf("OPEN", "future-variant", "后续", "quoted\"name")) {
                    val value = State.fromValue(raw)
                    val wire = Json.encodeToString(State.Serializer, value)
                    check(wire == JsonPrimitive(raw).toString())
                    val restored = Json.decodeFromString(State.Serializer, wire)
                    check(restored.rawValue == raw)
                    check((restored is State.Unknown) == (raw != "OPEN"))
                    for (present in listOf(false, true)) {
                        val encoder = LuxoEncoder()
                        PayloadInput(value, if (present) value else null, listOf(value)).writeLuxo(encoder)
                        val decoded = decodePayload(LuxoDecoder(encoder.bytes()))
                        check(decoded.state.value().rawValue == raw)
                        check(decoded.optional.value()?.rawValue == if (present) raw else null)
                        check(decoded.states.value().single().rawValue == raw)
                    }
                }
            }
        """.trimIndent())
    }

    @Test fun `explicit input receivers preserve ordinary generated JVM bytecode`() {
        val fields = listOf(
            LuxoField(1, "name", "String"),
            LuxoField(2, "note", "String", nullable = true),
            LuxoField(3, "labels", "String", isList = true),
            LuxoField(4, "count", "Int"),
        )
        val schema = LuxoSchema(models = mapOf("Input" to LuxoModel(
            "Input", usage = TypeUsage.INPUT, fields = fields,
        )))
        val current = LuxoCodegen.genTypes(schema, "probe")
        val previous = fields.fold(current) { source, field -> source.replace("this.${field.name}", field.name) }
        assertNotEquals(current, previous)
        assertContains(previous, "encoder.writeString(name)")
        val root = File(".tmp").apply { mkdirs() }
        val directory = Files.createTempDirectory(root.toPath(), "receiver-bytecode-").toFile()
        try {
            val source = File(directory, "Types.kt")
            val output = File(directory, "classes")
            val classpath = compilerClasspath()
            val args = listOf("-cp", classpath, "org.jetbrains.kotlin.cli.jvm.K2JVMCompiler",
                "-no-stdlib", "-no-reflect", "-jvm-target", "1.8", "-Werror", "-classpath", classpath,
                "-d", output.path, source.path)
            source.writeText(previous)
            val before = run(directory, args)
            assertEquals(0, before.first, before.second)
            val bytecode = File(output, "probe/Input.class").readBytes()
            source.writeText(current)
            val after = run(directory, args)
            assertEquals(0, after.first, after.second)
            assertContentEquals(bytecode, File(output, "probe/Input.class").readBytes())
        } finally { directory.deleteRecursively() }
    }

    @Test fun `nullable input temporaries cannot replace subsequent field values`() {
        val schema = LuxoSchema(models = mapOf("PairInput" to LuxoModel(
            "PairInput", usage = TypeUsage.INPUT, fields = listOf(
                LuxoField(1, "name", "String", nullable = true),
                LuxoField(2, "_nameValue", "String", nullable = true),
            ),
        )))
        compileAndRun("package probe", LuxoCodegen.genTypes(schema, "probe"), """
            package probe
            import com.luxo.client.*
            fun main() {
                for (first in listOf(null, "first")) {
                    val encoded = LuxoEncoder()
                    PairInput(first, "second").writeLuxo(encoded)
                    val decoded = LuxoDecoder(encoded.bytes())
                    decoded.skipArenaHeader()
                    check(decoded.nextField() && decoded.fieldID == 1)
                    check((if (decoded.readBool()) decoded.readString() else null) == first)
                    check(decoded.nextField() && decoded.fieldID == 2)
                    val second = if (decoded.readBool()) decoded.readString() else null
                    check(second == "second") { "second field replaced by first: " + second }
                    check(!decoded.nextField()); decoded.checkComplete()
                }
            }
        """.trimIndent())
    }

    @Test fun `generated input encoder fields compile and preserve Binary values`() {
        val fixtures = listOf("Scalar", "Optional", "Sequence")
        val schema = LuxoSchema(models = fixtures.associateWith { name ->
            LuxoModel(name, usage = TypeUsage.INPUT, fields = listOf(
                LuxoField(1, "encoder", "String", nullable = name == "Optional", isList = name == "Sequence"),
            ))
        })
        compileAndRun("package probe", LuxoCodegen.genTypes(schema, "probe"), """
            package probe
            import com.luxo.client.*
            fun main() {
                val scalar = LuxoEncoder()
                Scalar("hello").writeLuxo(scalar)
                val scalarValue = LuxoDecoder(scalar.bytes())
                scalarValue.skipArenaHeader()
                check(scalarValue.nextField() && scalarValue.fieldID == 1)
                check(scalarValue.readString() == "hello")
                check(!scalarValue.nextField()); scalarValue.checkComplete()
                for (value in listOf(null, "world")) {
                    val encoded = LuxoEncoder()
                    Optional(value).writeLuxo(encoded)
                    val decoded = LuxoDecoder(encoded.bytes())
                    decoded.skipArenaHeader(); check(decoded.nextField())
                    check((if (decoded.readBool()) decoded.readString() else null) == value)
                    check(!decoded.nextField()); decoded.checkComplete()
                }
                val encoded = LuxoEncoder()
                Sequence(listOf("a", "b")).writeLuxo(encoded)
                val decoded = LuxoDecoder(encoded.bytes())
                decoded.skipArenaHeader(); check(decoded.nextField())
                check(decoded.readArray { decoded.readString() } == listOf("a", "b"))
                check(!decoded.nextField()); decoded.checkComplete()
            }
        """.trimIndent())
    }

    @Test fun `rejects malformed query capabilities`() {
        for (field in listOf(
            LuxoQueryFilter("raw", "JSON", listOf("eq")),
            LuxoQueryFilter("active", "Boolean", listOf("gt")),
            LuxoQueryFilter("id", "Int", emptyList()),
            LuxoQueryFilter("id", "Int", listOf("eq", "eq")),
            LuxoQueryFilter("bad\"", "Int", listOf("eq")),
        )) {
            val schema = LuxoSchema(apis = mapOf("read" to LuxoAPI(1, "read", "x", paginated = true,
                query = LuxoQueryCapabilities(filters = listOf(field)))))
            assertFails { KotlinQueryCodegen.generate(schema, "probe") }
        }
    }
    @Test fun `query builders follow API capabilities`() {
        val schema = Json.decodeFromString<LuxoSchema>("""{"models":{},"enums":{"State":{"name":"State","values":["OPEN"]}},"apis":{
          "listUsers":{"id":1,"name":"listUsers","module":"user","returnType":"Int","returnList":true,"paginated":true,
            "query":{"filters":[{"field":"id","type":"Int","operators":["eq","gt"]},{"field":"state","type":"State","operators":["eq"]}],"sorters":["id"]}},
          "custom":{"id":2,"name":"custom","module":"user","returnType":"Int","returnList":true}
        }}""")
        val source = KotlinQueryCodegen.generate(schema, "probe")
        assertContains(source, "object LuxoQuery_listUsers")
        assertContains(source, "fun id_eq(value: Long)")
        assertContains(source, "fun id_desc()")
        assertFalse(source.contains("id_contains"))
        assertFalse(source.contains("LuxoQuery_custom"))
        compileAndRun(source, LuxoCodegen.genTypes(schema, "probe"))
    }

    @Test
    fun `schema directories compile without selectable fields`() {
        val root = File(".tmp").apply { mkdirs() }
        val directory = Files.createTempDirectory(root.toPath(), "empty-schema-").toFile()
        val classpath = compilerClasspath()
        val fixtures = listOf(
            LuxoSchema(),
            LuxoSchema(apis = mapOf("echo" to LuxoAPI(1, "echo", "fixture", returnType = "Bytes"))),
            LuxoSchema(models = mapOf("Empty" to LuxoModel("Empty", fields = emptyList()))),
        )
        try {
            val sources = fixtures.mapIndexed { index, schema ->
                File(directory, "Schema$index.kt").apply {
                    writeText(LuxoCodegen.genSchemaMap(schema, "probe$index"))
                }.path
            }
            val compiled = run(directory, listOf("-cp", classpath,
                "org.jetbrains.kotlin.cli.jvm.K2JVMCompiler", "-no-stdlib", "-no-reflect",
                "-Werror", "-classpath", classpath, "-d", File(directory, "classes").path) + sources)
            assertEquals(0, compiled.first, compiled.second)
        } finally { directory.deleteRecursively() }
    }

    @Test
    fun `business Unit type does not replace subscription completion`() {
        val schema = LuxoSchema(
            models = mapOf("Unit" to LuxoModel("Unit", fields = listOf(LuxoField(1, "id", "Int")))),
            apis = mapOf("watch" to LuxoAPI(1, "watch", "test", returnType = "Unit", stream = true)),
        )
        compileAndRun(
            LuxoCodegen.genClient(schema, "probe"),
            LuxoCodegen.genTypes(schema, "probe"),
            LuxoCodegen.genSchemaMap(schema, "probe") + "\nfun main() { check(LuxoClient.schema.size == 1) }",
        )
    }

    @Test fun `generated enums preserve case distinct known members`() {
        val schema = LuxoSchema(enums = mapOf("State" to LuxoEnum("State", listOf("Open", "OPEN"))))
        compileAndRun("package probe", LuxoCodegen.genTypes(schema, "probe"), """
            package probe
            fun main() {
                val mixed = State.fromValue("Open")
                val upper = State.fromValue("OPEN")
                check(mixed is State.Known && upper is State.Known && mixed != upper)
                check(mixed.rawValue == "Open" && upper.rawValue == "OPEN")
                check(State.fromValue("open") is State.Unknown)
            }
        """.trimIndent())
    }

    private fun compileAndRun(source: String, types: String, consumerSource: String? = null,
        extraSources: Map<String, String> = emptyMap()) {
        val root = File(".tmp").apply { mkdirs() }
        val directory = Files.createTempDirectory(root.toPath(), "query-generated-").toFile()
        val classpath = compilerClasspath()
        try {
            val queries = File(directory, "Queries.kt").apply { writeText(source) }
            val models = File(directory, "Types.kt").apply { writeText(types) }
            val consumer = File(directory, "Consumer.kt").apply { writeText(consumerSource ?: """
                package probe
                fun main() {
                    val f = LuxoQuery_listUsers.id_eq(9007199254740993L)
                    check(f.field == "id" && f.op == "eq" && f.value == 9007199254740993L)
                    check(LuxoQuery_listUsers.id_desc().order == "desc")
                    check(LuxoQuery_listUsers.state_eq(State.fromValue("OPEN")).value == "OPEN")
                    println(f.value)
                }
            """.trimIndent()) }
            val extra = extraSources.map { (name, body) -> File(directory, name).apply { writeText(body) }.path }
            val args = listOf("-cp", classpath, "org.jetbrains.kotlin.cli.jvm.K2JVMCompiler", "-no-stdlib", "-no-reflect", "-Werror",
                "-classpath", classpath, "-d", File(directory, "classes").path, queries.path, models.path, consumer.path) + extra
            val compiled = run(directory, args)
            assertEquals(0, compiled.first, compiled.second)
            val executed = run(directory, listOf("-cp", File(directory, "classes").path + File.pathSeparator + classpath, "probe.ConsumerKt"))
            assertEquals(0, executed.first, executed.second)
            if (consumerSource != null) return
            assertContains(executed.second, "9007199254740993")
            consumer.writeText("package probe; fun main() { LuxoQuery_listUsers.id_eq(\"42\") }")
            assertNotEquals(0, run(directory, args).first)
        } finally { directory.deleteRecursively() }
    }

    private fun compilerClasspath(): String =
        (System.getProperty("luxo.testRuntimeClasspath") ?: System.getProperty("java.class.path"))
            .split(File.pathSeparator).filter { File(it).exists() }.joinToString(File.pathSeparator)

    private fun run(directory: File, arguments: List<String>): Pair<Int, String> {
        val log = File(directory, "process.log")
        val process = ProcessBuilder(listOf(File(System.getProperty("java.home"), "bin/java").path,
            "-Djava.io.tmpdir=${directory.absolutePath}") + arguments)
            .redirectErrorStream(true).redirectOutput(log).start()
        try {
            check(process.waitFor(60, TimeUnit.SECONDS)) { "query compiler timed out" }
            return process.exitValue() to log.readText()
        } finally { if (process.isAlive) { process.destroyForcibly(); process.waitFor() } }
    }
}
