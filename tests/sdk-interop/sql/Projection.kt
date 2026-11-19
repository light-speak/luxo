import com.luxo.client.*
import probe.LuxoClient
import kotlinx.coroutines.*
import java.io.File

fun main(args: Array<String>) = runBlocking {
    val http = sqlHTTPClient(args[1])
    val client = LuxoClient(OkHttpTransport(args[0], client=http, mode=TransportMode.BINARY))
    val plan = PreparedSelection("name")
    try {
        measure(client, plan)
    } finally {
        http.dispatcher.executorService.shutdownNow()
        http.connectionPool.evictAll()
    }
}

private val expectedNames = (1..128).map { "item-$it" }.sorted()

private fun verify(rows: List<probe.Product>) {
    check(rows.size == 128)
    check(rows.map { it.name.value() }.sorted() == expectedNames)
    rows.forEach {
        check(!it.id.isSelected && !it.price.isSelected && !it.description.isSelected)
    }
}

private suspend fun measure(client: LuxoClient, plan: PreparedSelection) {
    val paths: List<suspend () -> List<probe.Product>> = listOf(
        { client.products(`$selectionPlan`=plan) },
        { client.selectedProducts(`$selectionPlan`=plan) },
    )
    repeat(20) { paths.forEach { verify(it()) } }
    val output = StringBuilder("batch,path,iteration,microseconds\n")
    repeat(3) { batch ->
        repeat(50) { iteration ->
            val order = if ((batch + iteration) % 2 == 0) listOf(0, 1) else listOf(1, 0)
            order.forEach { index ->
                val start = System.nanoTime()
                val rows = paths[index]()
                val elapsed = (System.nanoTime() - start) / 1000.0
                verify(rows)
                output.append(batch).append(',')
                    .append(if (index == 0) "fullSQL" else "selectedSQL").append(',')
                    .append(iteration).append(',').append(elapsed).append('\n')
            }
        }
    }
    File(System.getenv("LUXO_SQL_SCRATCH"), "projection-kotlin.csv").writeText(output.toString())
    println("Kotlin projection comparison: 340 verified calls, single concurrency, no allocation claim")
}
