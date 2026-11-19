import okhttp3.OkHttpClient
import java.io.File
import java.security.KeyStore
import java.security.cert.CertificateFactory
import javax.net.ssl.*

fun sqlHTTPClient(certificateFile: String): OkHttpClient {
    val certificate = File(certificateFile).inputStream().use { CertificateFactory.getInstance("X.509").generateCertificate(it) }
    val store = KeyStore.getInstance(KeyStore.getDefaultType()).apply {
        load(null, null)
        setCertificateEntry("fixture", certificate)
    }
    val factory = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm()).apply { init(store) }
    val manager = factory.trustManagers.filterIsInstance<X509TrustManager>().single()
    val tls = SSLContext.getInstance("TLS").apply { init(null, arrayOf(manager), null) }
    return OkHttpClient.Builder().sslSocketFactory(tls.socketFactory, manager).build()
}
