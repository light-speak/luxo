package app

import com.luxo.generated.LuxoClient

suspend fun render(client: LuxoClient) {
    val node = client.lookupNode()
    println(node.name.value())
    node.posts.value().forEach { println(it.title.value()) }
}
