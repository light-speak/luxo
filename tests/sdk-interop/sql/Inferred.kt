import com.luxo.client.*
import probe.LuxoClient
import kotlinx.coroutines.runBlocking

private fun verifyNames(values: List<String>) {
    check(values.sorted() == (1..128).map { "item-$it" }.sorted())
}

private suspend fun selectedSites(client: LuxoClient) {
    val mapped = client.selectedProducts()
    verifyNames(mapped.map { row -> check(row.price.value() > 0); row.name.value() })
    val first = client.selectedProducts()
    check(first.map { row -> check(row.price.value() > 0); row.name.value() }.first() == "item-1")
    val source = client.selectedProducts()
    val alias = source
    verifyNames(alias.map { row -> check(row.price.value() > 0); row.name.value() })
    val indexed = client.selectedProducts()
    check(indexed.map { it.name.value() }.first() == "item-1")
    val last = client.selectedProducts()
    check(last.map { it.name.value() }.last() == "item-128")
}

private suspend fun businessSites(client: LuxoClient) {
    // Keep field accesses at distinct resolved call sites; opaque model helpers
    // would require explicit selections and could not prove automatic inference.
    val priced = client.pricedProducts()
    verifyNames(priced.map { it.name.value() })
    val privateRows = client.privateProducts()
    verifyNames(privateRows.map { it.name.value() })
    val called = client.calledProducts()
    verifyNames(called.map { it.name.value() })
    val calledPrivate = client.calledPrivateProducts()
    verifyNames(calledPrivate.map { it.name.value() })
    val nested = client.nestedProducts()
    verifyNames(nested.map { it.name.value() })
    val own = client.ownProducts()
    verifyNames(own.map { it.name.value() })
    val ownPrivate = client.ownPrivateProducts()
    verifyNames(ownPrivate.map { it.name.value() })
    val combined = client.combinedProducts()
    verifyNames(combined.map { it.name.value() })
    val forwarded = client.forwardedProducts()
    verifyNames(forwarded.map { it.name.value() })
    verifyNames(client.scalarCalledProducts())
    val aliasedOwn = client.aliasedOwnProducts()
    verifyNames(aliasedOwn.map { it.name.value() })
}

fun main(args: Array<String>) = runBlocking {
    val http = sqlHTTPClient(args[1])
    try {
        for (mode in listOf(TransportMode.JSON, TransportMode.BINARY)) {
            val client = LuxoClient(OkHttpTransport(args[0], client=http, mode=mode))
            selectedSites(client)
            businessSites(client)
        }
        println("Kotlin inferred JVM: thirty-two JSON/Binary H2 calls passed")
    } finally {
        http.dispatcher.executorService.shutdownNow()
        http.connectionPool.evictAll()
    }
}
