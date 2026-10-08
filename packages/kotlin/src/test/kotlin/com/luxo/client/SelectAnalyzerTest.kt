package com.luxo.client

import java.io.File
import java.nio.charset.CharacterCodingException
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertIs

class SelectAnalyzerTest {
    @Test
    fun `enum raw values do not become model subfields`() {
        val fixture = LuxoSchema(
            enums = mapOf("State" to LuxoEnum("State", listOf("OPEN"))),
            models = mapOf("Record" to LuxoModel("Record", fields = listOf(
                LuxoField(1, "state", "Enum", typeName = "State"),
                LuxoField(2, "states", "Enum", typeName = "State", isList = true),
            ))),
            apis = mapOf("getRecord" to LuxoAPI(1, "getRecord", "test", returnType = "Record")),
        )
        val hints = SelectAnalyzer.analyzeSources(sequenceOf("""
            fun render() {
                val record = client.getRecord()
                println(record.state.value().rawValue)
                println(record.states.value().map { it.rawValue })
            }
        """.trimIndent()), fixture)
        assertEquals("state, states", hints["getRecord"])
    }

    private val schema = LuxoSchema(
        models = mapOf(
            "Node" to LuxoModel(
                name = "Node",
                fields = listOf(
                    LuxoField(1, "name", "String"),
                    LuxoField(2, "posts", "Post", typeName = "Post", isList = true, relation = true),
                ),
            ),
            "Post" to LuxoModel(
                name = "Post",
                fields = listOf(
                    LuxoField(1, "title", "String"),
                    LuxoField(2, "content", "String"),
                ),
            ),
        ),
        apis = mapOf(
            "lookupNode" to LuxoAPI(1, "lookupNode", "node", returnType = "Node"),
            "searchNodes" to LuxoAPI(2, "searchNodes", "node", returnType = "Node", returnList = true),
            "browseNodes" to LuxoAPI(3, "browseNodes", "node", returnType = "Node", returnList = true, paginated = true),
        ),
    )

    @Test
    fun `uses exact schema APIs and return types without naming conventions`() {
        val hints = SelectAnalyzer.analyzeSources(
            sequenceOf(
                """
                fun render() {
                    val node = client.lookupNode()
                    println(node.name)
                    println(node.posts.map { it.title })
                    println(node.posts.map { it.content })
                    client.searchNodes().map { it.name }
                    val ghost = client.getGhost()
                    println(ghost.secret)
                }
                """.trimIndent(),
            ),
            schema,
        )

        assertEquals("name, posts { content, title }", hints["lookupNode"])
        assertEquals("name", hints["searchNodes"])
        assertFalse(hints.containsKey("getGhost"))
    }

    @Test
    fun `tracks multiline calls and named lambda parameters from syntax tree`() {
        val hints = SelectAnalyzer.analyzeSources(
            sequenceOf(
                """
                fun render() {
                    val node = client
                        .lookupNode()
                    println(node.name)

                    client.searchNodes().map { result ->
                        println(result.name)
                        result.posts.map { post -> post.title }
                    }
                }
                """.trimIndent(),
            ),
            schema,
        )

        assertEquals("name", hints["lookupNode"])
        assertEquals("name, posts { title }", hints["searchNodes"])
    }

    @Test
    fun `tracks fields through selected value wrappers`() {
        val hints = SelectAnalyzer.analyzeSources(
            sequenceOf(
                """
                fun render() {
                    val node = client.lookupNode()
                    println(node.name.value())
                    node.posts.value().forEach { println(it.title.value()) }
                }
                """.trimIndent(),
            ),
            schema,
        )

        assertEquals("name, posts { title }", hints["lookupNode"])
    }

    @Test
    fun `ignores comments strings and shadowed variables`() {
        val hints = SelectAnalyzer.analyzeSources(
            sequenceOf(
                """
                fun render() {
                    val node = client.lookupNode()
                    println(node.name)
                    // node.posts.map { it.title }
                    val example = "node.posts.map { it.content }"
                    run {
                        val node = localNode()
                        println(node.posts.map { it.title })
                    }
                    println(example)
                }
                """.trimIndent(),
            ),
            schema,
        )

        assertEquals("name", hints["lookupNode"])
    }

    @Test
    fun `tracks paginated items but ignores page metadata`() {
        val hints = SelectAnalyzer.analyzeSources(
            sequenceOf(
                """
                fun render() {
                    val page = client.browseNodes()
                    page.items.map { node -> println(node.name) }
                    println(page.total)
                }
                """.trimIndent(),
            ),
            schema,
        )

        assertEquals("name", hints["browseNodes"])
    }

    @Test
    fun `tracks stable context parameter syntax without compiler opt ins`() {
        val hints = SelectAnalyzer.analyzeSources(
            sequenceOf(
                """
                context(client: LuxoClient)
                suspend fun render() {
                    val node = client.lookupNode()
                    println(node.name.value())
                    node.posts.value().forEach { println(it.title.value()) }
                }
                """.trimIndent(),
            ),
            schema,
        )

        assertEquals("name, posts { title }", hints["lookupNode"])
    }

    @Test
    fun `rejects invalid Kotlin syntax`() {
        assertFailsWith<IllegalArgumentException> {
            SelectAnalyzer.analyzeSources(sequenceOf("fun broken("), schema)
        }
    }

    @Test
    fun `file analyzer preserves valid Unicode and accepts schema document BOM`() = withCodegenDirectory("kotlin-analysis-") { root ->
        val source = File(root, "Usage.kt")
        val text = """
            // 发布检查 café ﻿�
            context(client: LuxoClient)
            suspend fun render() {
                println(client.lookupNode().name.value())
                println("精度 ﻿�")
            }
        """.trimIndent()
        source.writeText(text)
        val snapshot = File(root, "schema.json")
        val hints = File(root, "generated/SelectHints.kt")
        for (bom in listOf("", "\uFEFF")) {
            snapshot.writeText(bom + Json.encodeToString(schema))
            SelectAnalyzerCli.main(arrayOf(root.path, hints.path, "example", snapshot.path))
            assertEquals(LuxoCodegen.genSelectHints("example", mapOf("lookupNode" to "name")), hints.readText())
            assertEquals(text, source.readText())
            assertEquals(bom + Json.encodeToString(schema), snapshot.readText())
        }
    }

    @Test
    fun `file analyzer rejects malformed snapshot bytes before creating or replacing hints`() = withCodegenDirectory("kotlin-analysis-") { root ->
        val model = schema.models.getValue("Node").copy(description = "<invalid>")
        val document = Json.encodeToString(schema.copy(models = schema.models + ("Node" to model)))
        val snapshot = File(root, "schema.json")
        for (existing in listOf(false, true)) {
            for ((index, bytes) in invalidUTF8.withIndex()) {
                val hints = File(root, "hints-$existing-$index.kt")
                if (existing) hints.writeText("existing hints")
                snapshot.writeBytes(injectInvalidBytes(document, bytes))
                val error = assertFailsWith<IllegalArgumentException> {
                    SelectAnalyzerCli.main(arrayOf(root.path, hints.path, "example", snapshot.path))
                }
                assertContains(error.message.orEmpty(), snapshot.path)
                assertIs<CharacterCodingException>(error.cause)
                if (existing) assertEquals("existing hints", hints.readText()) else assertFalse(hints.exists())
            }
        }
    }

    @Test
    fun `file analyzer rejects malformed source bytes before creating or replacing hints`() = withCodegenDirectory("kotlin-analysis-") { root ->
        val templates = listOf("// <invalid>\nfun render() = println(client.lookupNode().name)",
            "fun render() { println(client.lookupNode().name); println(\"<invalid>\") }")
        val source = File(root, "Usage.kt")
        for ((templateIndex, template) in templates.withIndex()) {
            for ((index, bytes) in invalidUTF8.withIndex()) {
                for (existing in listOf(false, true)) {
                    val hints = File(root, "generated/hints-$templateIndex-$index-$existing.kt")
                    if (existing) { hints.parentFile.mkdirs(); hints.writeText("existing hints") }
                    source.writeBytes(injectInvalidBytes(template, bytes))
                    val error = assertFailsWith<IllegalArgumentException> {
                        SelectAnalyzer.analyze(root.path, hints.path, "example", schema)
                    }
                    assertContains(error.message.orEmpty(), source.path)
                    assertIs<CharacterCodingException>(error.cause)
                    if (existing) assertEquals("existing hints", hints.readText()) else assertFalse(hints.exists())
                }
            }
        }
    }

    @Test
    fun `file analyzer still excludes generated and test sources before reading bytes`() = withCodegenDirectory("kotlin-analysis-") { root ->
        File(root, "Usage.kt").writeText("fun render() = println(client.lookupNode().name)")
        for (path in listOf("generated/Bad.kt", "nested/generated/Bad.kt", "test/Bad.kt", "nested/test/Bad.kt")) {
            File(root, path).apply { parentFile.mkdirs(); writeBytes(byteArrayOf(-1)) }
        }
        val hints = File(root, "generated/SelectHints.kt")
        SelectAnalyzer.analyze(root.path, hints.path, "example", schema)
        assertEquals(LuxoCodegen.genSelectHints("example", mapOf("lookupNode" to "name")), hints.readText())
    }

    private val invalidUTF8 = listOf(byteArrayOf(-1), byteArrayOf(-64, -128), byteArrayOf(-19, -96, -128),
        byteArrayOf(-12, -112, -128, -128), byteArrayOf(-30, -126))

    @Test
    fun `file analyzer rejects escaped incomplete characters without replacing hints`() = withCodegenDirectory("kotlin-analysis-") { root ->
        val model = schema.models.getValue("Node").copy(description = "<invalid>")
        val document = Json.encodeToString(schema.copy(models = schema.models + ("Node" to model)))
        val snapshot = File(root, "schema.json")
        val hints = File(root, "hints.kt")
        for (text in listOf("\\uD800", "\\uDC00", "\\uD800\\u0061")) {
            hints.writeText("existing hints")
            snapshot.writeText(document.replace("<invalid>", text))
            assertFailsWith<IllegalArgumentException> {
                SelectAnalyzerCli.main(arrayOf(root.path, hints.path, "example", snapshot.path))
            }
            assertEquals("existing hints", hints.readText())
        }
    }

    private fun injectInvalidBytes(text: String, bytes: ByteArray): ByteArray {
        val parts = text.split("<invalid>", limit = 2)
        check(parts.size == 2)
        return parts[0].toByteArray() + bytes + parts[1].toByteArray()
    }
}
