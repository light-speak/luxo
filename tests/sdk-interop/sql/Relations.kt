import com.luxo.client.*
import probe.LuxoClient
import kotlinx.coroutines.runBlocking

private val articleViews = listOf("article-1:alice", "article-2:alice", "article-3:bob",
                                "article-4:-", "article-5:zero", "article-6:-")

private fun same(actual: List<String>, expected: List<String>) {
    check(actual == expected) { "relationship values differ: $actual" }
}

private suspend fun verifyRelations(client: LuxoClient) {
    val rows = client.articleRows()
    same(rows.map { "${it.title.value()}:${it.author.value()?.name?.value() ?: "-"}" }, articleViews)
    val people = client.peopleRows()
    same(people.map { person ->
        "${person.name.value()}:${person.articles.value().map { it.title.value() }.sorted().joinToString("|")}"
    }, listOf("zero:article-5", "alice:article-1|article-2", "bob:article-3", "empty:"))
    for (number in 1..4) {
        val page = client.articlePage(page=number.toLong(), pageSize=2)
        check(page.total == 6L && page.page == number.toLong() && page.pageSize == 2L)
        same(page.items.map { "${it.title.value()}:${it.author.value()?.name?.value() ?: "-"}" },
             articleViews.drop((number - 1) * 2).take(2))
    }
    val boxed = client.boxedArticlePage(page=2, pageSize=2)
    check(boxed.total == 6L && boxed.page == 2L && boxed.pageSize == 2L)
    same(boxed.items.map { "${it.title.value()}:${it.author.value()?.name?.value() ?: "-"}" }, articleViews.drop(2).take(2))
    val explicit = client.articlePage(pageSize=2, select="title")
    check(explicit.total == 6L && explicit.page == 1L && explicit.pageSize == 2L)
    same(explicit.items.map { it.title.value() }, listOf("article-1", "article-2"))
    val names = client.peopleRows(select="name")
    same(names.map { it.name.value() }, listOf("zero", "alice", "bob", "empty"))
}

fun main(args: Array<String>) = runBlocking {
    val http = sqlHTTPClient(args[1])
    try {
        for (mode in listOf(TransportMode.JSON, TransportMode.BINARY)) {
            verifyRelations(LuxoClient(OkHttpTransport(args[0], client=http, mode=mode)))
        }
        println("Kotlin: eighteen inferred/explicit relationship and pagination calls passed")
    } finally {
        http.dispatcher.executorService.shutdownNow()
        http.connectionPool.evictAll()
    }
}
