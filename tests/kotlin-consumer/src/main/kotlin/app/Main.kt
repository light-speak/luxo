package app

import com.luxo.generated.LuxoClient

context(client: LuxoClient)
suspend fun render() {
    val node = client.lookupNode()
    println(node.name.value())
    node.posts.value().forEach { println(it.title.value()) }
}
