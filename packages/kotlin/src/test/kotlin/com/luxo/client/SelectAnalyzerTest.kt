package com.luxo.client

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse

class SelectAnalyzerTest {
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
    fun `rejects invalid Kotlin syntax`() {
        assertFailsWith<IllegalArgumentException> {
            SelectAnalyzer.analyzeSources(sequenceOf("fun broken("), schema)
        }
    }
}
