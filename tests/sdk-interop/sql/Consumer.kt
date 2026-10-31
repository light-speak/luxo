import com.luxo.client.*
import probe.LuxoClient
import kotlinx.coroutines.*
import okhttp3.OkHttpClient
import java.io.File
import java.security.KeyStore
import java.security.cert.CertificateFactory
import javax.net.ssl.*

fun main(args: Array<String>) = runBlocking {
    val certificate = File(args[1]).inputStream().use { CertificateFactory.getInstance("X.509").generateCertificate(it) }
    val store = KeyStore.getInstance(KeyStore.getDefaultType()).apply {
        load(null, null)
        setCertificateEntry("fixture", certificate)
    }
    val factory = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm()).apply { init(store) }
    val manager = factory.trustManagers.filterIsInstance<X509TrustManager>().single()
    val tls = SSLContext.getInstance("TLS").apply { init(null, arrayOf(manager), null) }
    val http = OkHttpClient.Builder().sslSocketFactory(tls.socketFactory, manager).build()
    val client = LuxoClient(OkHttpTransport(args[0], client=http, mode=TransportMode.BINARY))
    val plan = PreparedSelection("name")
    try {
        narrow(client.products(`$selectionPlan`=plan))
        narrow(client.selectedProducts(`$selectionPlan`=plan))
        val full = client.selectedProducts(select="id,name,price,description")
        check(full.size == 128)
        full.forEach { row ->
            val price = row.price.value()
            check(row.id.value() == 9007199254740992L + price)
            check(row.name.value() == "item-$price")
            check(row.description.value() == "x".repeat(4096))
        }
        val first = client.listProducts(pageSize=64, `$selectionPlan`=plan)
        val second = client.listProducts(page=2, pageSize=64, `$selectionPlan`=plan)
        check(first.total == 128L && second.total == 128L)
        check(first.page == 1L && second.page == 2L)
        check(first.pageSize == 64L && second.pageSize == 64L)
        narrow(first.items, 64)
        narrow(second.items, 64)
        check((first.items + second.items).map { it.name.value() }.toSet().size == 128)
        println("Kotlin: five generated H2 calls, 128 rows, exact Int64 and pagination passed")
    } finally {
        http.dispatcher.executorService.shutdownNow()
        http.connectionPool.evictAll()
    }
}

fun narrow(rows: List<probe.Product>, count: Int = 128) {
    check(rows.size == count)
    check(rows.map { it.name.value() }.toSet().size == count)
    rows.forEach {
        check(it.name.value().startsWith("item-"))
        check(!it.id.isSelected && !it.price.isSelected && !it.description.isSelected)
    }
}
