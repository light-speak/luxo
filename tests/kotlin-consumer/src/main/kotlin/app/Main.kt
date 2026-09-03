package app

import com.luxo.generated.LuxoClient

suspend fun render(client: LuxoClient) {
    val node = client.lookupNode()
    println(node.name)
    node.posts.forEach { println(it.title) }
}
