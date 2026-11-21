import com.luxo.client.*
import probe.LuxoClient
import probe.PrecisionRecord
import probe.NullableRecord
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.*
import java.io.File

private const val selection = "id,whole,ratio,amount,moment,elapsed,token"

private fun verify(row: PrecisionRecord, item: JsonObject, id: Long) {
    fun value(name: String) = item.getValue(name).jsonPrimitive.content
    check(row.id.value() == id)
    check(row.whole.value() == value("whole").toLong())
    check(row.ratio.value().toRawBits() == value("ratioBits").toULong(16).toLong())
    check(row.amount.value() == value("amount"))
    check(row.moment.value() == value("storedMoment"))
    check(row.elapsed.value() == value("elapsed").toLong())
    check(row.token.value() == value("token").lowercase())
}

fun main(args: Array<String>) = runBlocking {
    val cases = Json.parseToJsonElement(File(System.getenv("LUXO_SQL_PRECISION_CORPUS")).readText()).jsonArray.map { it.jsonObject }
    val http = sqlHTTPClient(args[1])
    val client = LuxoClient(OkHttpTransport(args[0], client=http))
    try {
        if (System.getenv("LUXO_SQL_NULLABLE") == "1") {
            verifyNullableStorage(client)
            return@runBlocking
        }
        listOf(TransportMode.JSON, TransportMode.BINARY).forEachIndexed { modeIndex, mode ->
            client.setMode(mode)
            cases.forEachIndexed { index, item ->
                fun value(name: String) = item.getValue(name).jsonPrimitive.content
                val id = (modeIndex * 10 + index + 1).toLong()
                val row = client.storePrecision(id=id, whole=value("whole").toLong(), ratio=value("ratio").toDouble(),
                    amount=value("amount"), moment=value("moment"), elapsed=value("elapsed").toLong(), token=value("token"), select=selection)
                verify(row, item, id)
                verify(checkNotNull(client.readPrecision(id, select=selection)), item, id)
            }
            val rows = client.precisionRecords(select=selection)
            check(rows.size == (modeIndex + 1) * cases.size)
            rows.forEach { verify(it, cases[(it.id.value() % 10).toInt() - 1], it.id.value()) }
            val error = runCatching { client.storePrecision(id=999999, whole=1, ratio=1.0, amount="1",
                moment=cases[0].getValue("moment").jsonPrimitive.content, elapsed=1,
                token=cases[0].getValue("token").jsonPrimitive.content, select=selection) }.exceptionOrNull()
            check(error is LuxoError) { "inexact Duration was not rejected: $error" }
            check(client.readPrecision(999999, select=selection) == null)
            println("Kotlin $mode: six exact persistent records, list and rejected Duration passed")
        }
    } finally {
        http.dispatcher.executorService.shutdownNow()
        http.connectionPool.evictAll()
    }
}

private val nullableCases: List<Pair<Long?, List<Long>?>> = listOf(
    null to null, 0L to emptyList(),
    Long.MAX_VALUE to listOf(Long.MIN_VALUE, 9007199254740993L, Long.MAX_VALUE))
private fun verifyNullable(row: NullableRecord) {
    val expected = nullableCases[(row.id.value() % 10).toInt() - 1]
    check(row.number.value() == expected.first)
    check(row.items.value() == expected.second)
}
private suspend fun verifyNullableStorage(client: LuxoClient) {
    val fields = "id,number,items"
    listOf(TransportMode.JSON, TransportMode.BINARY).forEachIndexed { modeIndex, mode ->
        client.setMode(mode)
        nullableCases.forEachIndexed { index, item ->
            val id = (modeIndex * 10 + index + 1).toLong()
            verifyNullable(client.storeNullableRecord(id=id, number=item.first, items=item.second, select=fields))
            val row = checkNotNull(client.readNullableRecord(id, select=fields))
            check(row.id.value() == id)
            verifyNullable(row)
        }
        val rows = client.nullableRecords(select=fields)
        check(rows.size == (modeIndex + 1) * nullableCases.size)
        rows.forEach(::verifyNullable)
        check(client.readNullableRecord(999999, select=fields) == null)
        println("Kotlin $mode: nullable zero, null, empty/list persistence passed")
    }
}
