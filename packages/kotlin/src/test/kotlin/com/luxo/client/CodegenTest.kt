package com.luxo.client

import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFalse

class CodegenTest {
    private val schema = LuxoSchema(
        models = mapOf(
            "Payload" to LuxoModel(
                name = "Payload",
                usage = TypeUsage.OUTPUT,
                fields = listOf(
                    LuxoField(1, "id", "Int"),
                    LuxoField(2, "blob", "Bytes"),
                    LuxoField(3, "metadata", "JSON"),
                ),
            ),
        ),
        types = mapOf(
            "CreateInput" to LuxoTypeDecl(
                name = "CreateInput",
                usage = TypeUsage.INPUT,
                fields = listOf(LuxoField(1, "name", "String")),
            ),
        ),
        apis = mapOf(
            "createPayload" to LuxoAPI(
                id = 6,
                name = "createPayload",
                module = "file",
                returnType = "Payload",
                params = listOf(
                    LuxoParam(1, "input", "JSON", typeName = "CreateInput"),
                    LuxoParam(2, "note", "String", nullable = true),
                    LuxoParam(3, "caption", "String", nullable = true, hasDefault = true),
                ),
            ),
            "listPayloads" to LuxoAPI(
                id = 7,
                name = "listPayloads",
                module = "file",
                returnType = "Payload",
                returnList = true,
            ),
            "listPayloadPage" to LuxoAPI(
                id = 8,
                name = "listPayloadPage",
                module = "file",
                returnType = "Payload",
                returnList = true,
                paginated = true,
            ),
            "watchPayload" to LuxoAPI(
                id = 9,
                name = "watchPayload",
                module = "file",
                returnType = "Payload",
                stream = true,
                params = listOf(LuxoParam(1, "projectId", "Int")),
            ),
            "searchPayloads" to LuxoAPI(
                id = 10,
                name = "searchPayloads",
                module = "file",
                returnType = "Payload",
                returnList = true,
                paginated = true,
            ),
            "createSnapshot" to LuxoAPI(
                id = 11,
                name = "createSnapshot",
                module = "file",
                returnType = "Payload",
            ),
        ),
    )

    @Test
    fun `introspection key is sent only in the canonical header`() {
        val connection = LuxoCodegen.introspectionConnection("https://api.example.com/luvia", "secret-key")

        assertEquals("secret-key", connection.getRequestProperty("X-Introspection-Key"))
        assertEquals("\$schema", connection.url.query)
        assertFalse(connection.url.toString().contains("key="))
        connection.disconnect()
    }

    @Test
    fun `generates canonical columnar and paginated decoders`() {
        val types = LuxoCodegen.genTypes(schema, "com.example")
        val client = LuxoCodegen.genClient(schema, "com.example")
        val hints = LuxoCodegen.genSelectHints("com.example", emptyMap())

        assertContains(types, "fun decodeColumnarPayload(data: ByteArray): List<Payload>")
        assertContains(types, "fun decodePaginatedPayload(data: ByteArray): Page<Payload>")
        assertContains(types, "import com.luxo.client.ColumnarDecoder")
        assertContains(types, "import com.luxo.client.Page")
        assertContains(types, "val id: Selected<Long> = Selected.unselected()")
        assertContains(types, "Selected.value(dec.readInt())")
        assertFalse(types.contains("?: 0L"))
        assertContains(types, "fun decodeJSONPayload(data: JsonObject): Payload")
        assertContains(types, "Base64.getDecoder().decode")
        assertContains(types, "dec.readColumnBytes()")
        assertContains(types, "Json.parseToJsonElement")
        assertFalse(types.contains("!!"))
        assertContains(client, "decodeColumnarPayload(data)")
        assertContains(client, "decodePaginatedPayload(data)")
        assertContains(client, "input: CreateInput")
        assertContains(client, "Json.encodeToJsonElement(input)")
        assertContains(client, "note: String?")
        assertContains(client, "caption: LuxoOptional<String> = LuxoOptional.Absent")
        assertContains(client, "is LuxoOptional.Present -> callParams[\"caption\"] = caption.value")
        assertContains(client, "filters: List<LuxoFilter>? = null")
        assertContains(client, "callParams[\"\\\$filters\"] = filters.map")
        assertContains(client, "suspend fun subscribeWatchPayload(projectId: Long, select: String? = null, onData: (Payload) -> Unit): () -> Unit")
        assertContains(client, "transport.subscribe(\"watchPayload\", callParams)")
        assertContains(client, "onData(decode_watchPayload(data))")
        assertContains(client, "endpoint.startsWith(\"ws://\") || endpoint.startsWith(\"wss://\")")
        assertContains(client, "LuxoWebSocket(endpoint, token = token)")
        assertContains(client, "suspend fun searchPayloads(page: Long? = null, pageSize: Long? = null, select: String? = null, filters: List<LuxoFilter>? = null, sorters: List<LuxoSorter>? = null)")
        assertContains(client, "suspend fun createSnapshot(select: String? = null): Payload")
        assertFalse(client.contains("createSnapshot(input:"))
        assertContains(client, "suspend fun createPayload(input: CreateInput, note: String?, caption: LuxoOptional<String> = LuxoOptional.Absent, select: String? = null)")
        assertContains(hints, "object SelectHints")
        assertContains(hints, "emptyMap()")
        assertFalse(client.contains("try { SelectHints.hints[api] }"))
        assertFalse(client.contains("!!"))
    }

    @Test
    fun `separates strict input DTOs from selected output models`() {
        val shared = LuxoSchema(
            models = emptyMap(),
            types = mapOf(
                "Profile" to LuxoTypeDecl(
                    name = "Profile",
                    usage = TypeUsage.UNUSED,
                    fields = listOf(
                        LuxoField(1, "name", "String"),
                        LuxoField(2, "bio", "String", nullable = true),
                    ),
                ),
            ),
            apis = mapOf(
                "updateProfile" to LuxoAPI(
                    id = 1,
                    name = "updateProfile",
                    module = "profile",
                    returnType = "Profile",
                    params = listOf(LuxoParam(1, "profile", "Model", typeName = "Profile")),
                ),
            ),
        )

        val types = LuxoCodegen.genTypes(shared, "com.example")
        val client = LuxoCodegen.genClient(shared, "com.example")

        assertContains(types, "data class Profile(")
        assertContains(types, "val name: Selected<String> = Selected.unselected()")
        assertContains(types, "val bio: Selected<String?> = Selected.unselected()")
        assertContains(types, "@Serializable\ndata class ProfileInput(")
        assertContains(types, "val name: String")
        assertContains(types, "val bio: String? = null")
        assertContains(client, "profile: ProfileInput")
        assertContains(client, "): Profile")
    }
}
