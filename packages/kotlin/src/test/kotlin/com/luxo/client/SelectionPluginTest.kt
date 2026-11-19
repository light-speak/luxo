package com.luxo.client

import java.io.File
import java.nio.file.Files
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertContains
import kotlin.test.assertNotEquals

class SelectionPluginTest {
    private fun sealedTypes(): String = LuxoCodegen.genTypes(
        kotlinx.serialization.json.Json.decodeFromString<LuxoSchema>(File("src/test/fixtures/SealedGoSchema.json").readText()), "probe")

    @Test
    fun `actual generic Go Page Box and Cursor schema retains automatic field selection`() {
        val schema = kotlinx.serialization.json.Json.decodeFromString<LuxoSchema>(File("src/test/fixtures/GenericGoSchema.json").readText())
        verify("""
            import com.luxo.client.*
            import probe.*
            import kotlinx.coroutines.runBlocking
            import kotlinx.serialization.json.*
            class Fixture : Transport {
                override fun setMode(mode: TransportMode) {}
                override fun setToken(token: String) {}
                override fun setSchema(schema: Map<String, APISchemaEntry>) {}
                override suspend fun call(api: String, params: Map<String, Any?>): Any = error("typed request required")
                override suspend fun call(api: String, request: GeneratedRequest): Any {
                    check(checkNotNull(request.selectionPlan).text == request.selection)
                    println(request.selection)
                    return Json.parseToJsonElement(when (api) {
                        "readPage" -> "{\"items\":[{\"value\":{\"value\":7}}],\"total\":1}"
                        "readCursor" -> "{\"items\":[{\"value\":{\"value\":8}}],\"nextCursor\":null,\"hasMore\":false}"
                        "echoNested" -> "{\"value\":{\"items\":[{\"value\":9}],\"total\":1}}"
                        else -> "[{\"value\":null}]"
                    })
                }
            }
            fun main() = runBlocking {
                val client = probe.LuxoClient(Fixture())
                val page = client.readPage()
                page.items.value().forEach { println(it.value.value().value.value()) }
                println(page.total.value())
                val cursor = client.readCursor()
                cursor.items.value().forEach { println(it.value.value().value.value()) }
                println(cursor.nextCursor.value())
                println(cursor.hasMore.value())
                val input = LuxoBox_d1dc0a950b2f5e29Input(LuxoPage_5411031e24da9aefInput(listOf(CounterInput(9L)),1L,1L,1L))
                val nested = client.echoNested(input).value.value()
                nested.items.value().forEach { println(it.value.value()) }
                println(nested.total.value())
                client.echoLists(listOf(LuxoBox_847cd3e6a7855432Input(null))).forEach { println(it.value.value() == null) }
            }
        """.trimIndent(), listOf("items{value{value}},total", "7", "1", "hasMore,items{value{value}},nextCursor", "8", "null", "false",
            "value{items{value},total}", "9", "1", "value", "true"), mapOf("Types.kt" to LuxoCodegen.genTypes(schema, "probe"),
            "Schema.kt" to LuxoCodegen.genSchemaMap(schema, "probe"), "Client.kt" to LuxoCodegen.genClient(schema, "probe")))
    }

    @Test
    fun `actual sealed case smart casts select tags and concrete payload fields`() {
        verify("""
            import com.luxo.client.*
            import probe.*
            @LuxoSelection fun outcome(select: String? = null, `${'$'}selectionPlan`: PreparedSelection? = null): Outcome {
                check(checkNotNull(`${'$'}selectionPlan`).text == select)
                println(select)
                return Outcome.Success(Selected.value(7L))
            }
            fun main() {
                val row = outcome()
                when (row) {
                    is Outcome.Success -> println(row.value.value())
                    is Outcome.Failure -> println("failure")
                    is Outcome.Pending -> println("pending")
                    is Outcome.Nested -> println("nested")
                }
            }
        """.trimIndent(), listOf("Failure,Nested,Pending,Success{value}", "7"), mapOf("Types.kt" to sealedTypes()))
    }

    @Test
    fun `actual sealed nullable and list metadata selects real case leaves`() {
        verify("""
            import com.luxo.client.*
            import probe.*
            @LuxoSelection fun outcome(select: String? = null): Outcome {
                println(select)
                return Outcome.Nested(Selected.value(null), Selected.value(emptyList()))
            }
            fun main() {
                val row = outcome() as Outcome.Nested
                println(row.child.value() == null)
                println(row.history.value()?.size)
                println(row.history.value()?.isEmpty())
            }
        """.trimIndent(), listOf("Nested{child{Failure,Nested,Pending,Success},history{Failure,Nested,Pending,Success}}", "true", "0", "true"), mapOf("Types.kt" to sealedTypes()))
    }

    @Test
    fun `Unicode selections retain source annotations and prepared wire names`() {
        verify("""
            import com.luxo.client.*
            class Café(@get:LuxoSelectionField("café") val café: String,
                @get:LuxoSelectionField("𐐀_name") val 𐐀_name: String)
            @LuxoSelection fun café(select: String? = null,
                `${'$'}selectionPlan`: PreparedSelection? = null): Café {
                check(select == "café,𐐀_name")
                check(checkNotNull(`${'$'}selectionPlan`).text == select)
                return Café("coffee", "letter")
            }
            fun main() {
                val row = café()
                println(row.café)
                println(row.𐐀_name)
            }
        """.trimIndent(), listOf("coffee", "letter"))
    }
    @Test
    fun `aggregate containers and closure returns cannot hide model escape`() {
        val declarations = """
            import com.luxo.client.*
            class User(@get:LuxoSelectionField("id") val id: Long,
                @get:LuxoSelectionField("name") val name: String)
            @LuxoSelection fun getUser(select: String? = null) = User(1, "Ada")
            fun consume(value: Any) { println(value) }
        """.trimIndent()
        for (usage in listOf("val rows = listOf(user); consume(rows)",
            "val row = mapOf(\"user\" to user); consume(row)",
            "consume(Pair(user, 1))", "consume({ user })")) {
            verify(declarations + "\nfun main() { val user = getUser(); println(user.id); $usage }", null)
        }
    }

    @Test
    fun `bound opaque member references cannot hide model field reads`() {
        verify(boundReferenceSource("user::label"), null)
    }

    @Test
    fun `bound opaque extension references cannot hide model field reads`() {
        verify(boundReferenceSource("user::extensionLabel"), null)
    }

    @Test
    fun `bound opaque property references cannot hide model field reads`() {
        verify(boundReferenceSource("user::name", "read.get()"), null)
    }

    @Test
    fun `explicit selections retain bound member and property references`() {
        verify(boundReferenceSource("user::label", selection = "id,name"), listOf("1", "Ada"))
        verify(boundReferenceSource("user::name", "read.get()", "id,name"), listOf("1", "Ada"))
    }

    private fun boundReferenceSource(reference: String, read: String = "read()", selection: String? = null): String {
        val declarations = """
            import com.luxo.client.*
            class User(@get:LuxoSelectionField("id") val id: Long,
                @get:LuxoSelectionField("name") val name: String) {
                fun label(): String = name
            }
            fun User.extensionLabel(): String = name
            @LuxoSelection fun getUser(select: String? = null) = User(1, "Ada")
        """.trimIndent()
        val call = if (selection == null) "getUser()" else "getUser(select = \"$selection\")"
        return declarations + "\nfun main() { val user = $call; println(user.id); " +
            "val read = $reference; println($read) }"
    }

    @Test
    fun `compiler hoists distinct plans and repeated calls reuse identity`() {
        verify("""
            import com.luxo.client.*
            class User(@get:LuxoSelectionField("id") val id: Long)
            val seen = mutableListOf<PreparedSelection>()
            @LuxoSelection fun getUser(select: String? = null,
                `${'$'}selectionPlan`: PreparedSelection? = null): User {
                val plan = checkNotNull(`${'$'}selectionPlan`)
                check(plan.text == select)
                seen += plan
                return User(42)
            }
            @LuxoSelection fun subscribeUser(select: String? = null,
                `${'$'}selectionPlan`: PreparedSelection? = null, onData: (User) -> Unit): () -> Unit {
                val plan = checkNotNull(`${'$'}selectionPlan`)
                check(plan.text == select)
                seen += plan
                onData(User(42))
                return {}
            }
            fun watch() { subscribeUser { check(it.id == 42L) }() }
            fun read(): Long { val user = getUser(); return user.id }
            fun main() {
                check(read() == 42L)
                check(read() == 42L)
                val other = getUser()
                check(other.id == 42L)
                check(seen[0] === seen[1])
                check(seen[0] !== seen[2])
                watch()
                watch()
                check(seen[3] === seen[4])
                check(seen[3] !== seen[0])
                println("plans reused and isolated")
            }
        """.trimIndent(), listOf("plans reused and isolated"))
    }

    @Test
    fun `compiler injects selections per resolved call and preserves explicit selection`() {
        verify("""
                import com.luxo.client.LuxoSelection
                import com.luxo.client.LuxoSelectionField
                class User(@get:LuxoSelectionField("id") val id: Long,
                    @get:LuxoSelectionField("name") val name: String)
                class Client {
                    @LuxoSelection fun getUser(select: String? = null): User {
                        println(select)
                        return User(42, "Ada")
                    }
                }
                class Other { fun getUser(): User = User(9, "Other") }
                fun main() {
                    val client = Client()
                    val first = client.getUser()
                    println(first.id)
                    val second = client.getUser()
                    val alias = second
                    println(alias.name)
                    println(client.getUser(select = "id,name").name)
                    println(Other().getUser().name)
                }
            """.trimIndent(), listOf("id", "42", "name", "Ada", "id,name", "Ada", "Other"))
    }

    @Test
    fun `opaque model escape fails even when another field is read locally`() {
        verify("""
            import com.luxo.client.LuxoSelection
            import com.luxo.client.LuxoSelectionField
            class User(@get:LuxoSelectionField("id") val id: Long)
            @LuxoSelection fun getUser(select: String? = null) = User(1)
            fun consume(user: User) { println(user.id) }
            fun main() {
                val user = getUser()
                println(user.id)
                consume(user)
            }
        """.trimIndent(), null)
    }

    @Test
    fun `selected nested fields collections and subscription callbacks belong to their calls`() {
        verify("""
            import com.luxo.client.*
            class Address(@get:LuxoSelectionField("city") val city: Selected<String>)
            class User(@get:LuxoSelectionField("id") val id: Selected<Long>,
                @get:LuxoSelectionField("address") val address: Selected<Address>)
            fun user() = User(Selected.value(42L), Selected.value(Address(Selected.value("Tokyo"))))
            @LuxoSelection fun getUser(select: String? = null): User { println(select); return user() }
            @LuxoSelection fun listUsers(select: String? = null): List<User> { println(select); return listOf(user()) }
            @LuxoSelection fun subscribeUser(select: String? = null, onData: (User) -> Unit): () -> Unit {
                println(select); onData(user()); return {}
            }
            fun main() {
                val model = getUser()
                println(model.address.value().city.value())
                println(listUsers().map { it.id.value() }.single())
                val cancel = subscribeUser { println(it.id.value()) }
                subscribeUser { println(it.address.value().city.value()) }()
                cancel()
            }
        """.trimIndent(), listOf("address{city}", "Tokyo", "id", "42", "id", "42", "address{city}", "Tokyo"))
    }

    @Test
    fun `framework page items and scalar metadata retain separate selections`() {
        verify("""
            import com.luxo.client.*
            class User(@get:LuxoSelectionField("id") val id: Selected<Long>,
                @get:LuxoSelectionField("name") val name: Selected<String>)
            @LuxoSelection fun page(select: String? = null): Page<User> {
                println(select)
                return Page(listOf(User(Selected.value(42L), Selected.value("Ada"))), 1, 1, 2)
            }
            fun main() {
                val first = page()
                check(first.total == 1L && first.page == 1L && first.pageSize == 2L)
                println(first.items.map { it.id.value() }.single())
                val second = page()
                val alias = second.items
                println(alias.map { it.name.value() }.single())
            }
        """.trimIndent(), listOf("id", "42", "name", "Ada"))
    }

    @Test
    fun `nullable selected model access keeps its guarded child fields`() {
        verify(nullableDeclarations + """
            fun main() {
                val present = getUser(true)
                println(present.address.value()?.city?.value() ?: "missing")
                val absent = getUser(false)
                val address = absent.address.value()
                println(address?.city?.value() ?: "missing")
            }
        """.trimIndent(), listOf("address{city}", "Tokyo", "address{city}", "missing"))
    }

    @Test
    fun `null probes without child projection and opaque equality still require explicit selection`() {
        for (usage in listOf(
            "println(user.address.value() == null)",
            "println(user.address.value()?.city?.value()); println(user.address.value() == Address(Selected.value(\"Other\")))",
            "println(user.address.value()?.city?.value()); println(user.address.value()?.equals(null))",
        )) {
            verify(nullableDeclarations + "\nfun main() { val user = getUser(true); $usage }", null)
        }
    }

    @Test
    fun `business page properties and framework page copies remain opaque`() {
        verify("""
            import com.luxo.client.*
            class User(@get:LuxoSelectionField("id") val id: Selected<Long>)
            class Page<T>(val items: List<T>)
            @LuxoSelection fun page(select: String? = null) = Page(listOf(User(Selected.value(42L))))
            fun main() { val result = page(); println(result.items.map { it.id.value() }) }
        """.trimIndent(), null)
        verify("""
            import com.luxo.client.*
            class User(@get:LuxoSelectionField("id") val id: Selected<Long>)
            @LuxoSelection fun page(select: String? = null) = Page(listOf(User(Selected.value(42L))), 1, 1, 2)
            fun main() { val result = page(); println(result.items.map { it.id.value() }); println(result.copy()) }
        """.trimIndent(), null)
    }

    private val nullableDeclarations = """
        import com.luxo.client.*
        class Address(@get:LuxoSelectionField("city") val city: Selected<String>) {
            override fun equals(other: Any?): Boolean = error("opaque equality invoked")
            override fun hashCode(): Int = 0
        }
        class User(@get:LuxoSelectionField("address") val address: Selected<Address?>)
        @LuxoSelection fun getUser(present: Boolean, select: String? = null): User {
            println(select)
            return User(Selected.value(if (present) Address(Selected.value("Tokyo")) else null))
        }

    """.trimIndent() + "\n"

    @Test
    fun `nullable wrappers casts and branch results cannot hide opaque model reads`() {
        for (usage in listOf(
            "val user=getUser(); println(user.address.value()?.city?.value()); consumeSelected(user.address.value()?.child)",
            "val user=getUser(); println(user.address.value()?.city?.value()); val selected=user.address.value()?.child; consumeSelected(selected)",
            "val user=getUser(); consumeSelected(user.address.value()?.child)",
            "val user=getUser(); println(user.address.value()?.city?.value()); consumeAddress(if (args.isNotEmpty()) user.address.value() else null)",
            "val user=getUser(); println(user.address.value()?.city?.value()); consumeSelected(if (args.isNotEmpty()) user.address.value()?.child else null)",
            "val user=getUser(); println(user.address.value()?.city?.value()); consumeAny(user.address.value() as Any?)",
            "val one=getUser(); val two=getUser(); println(one.address.value()?.city?.value()); println(two.address.value()?.city?.value()); consumeAddress(if (args.isNotEmpty()) one.address.value() else two.address.value())",
            "val user=getUser(); println(user.address.value()?.city?.value()); consumeAddress(user.address.value() ?: error(\"missing\"))",
        )) {
            verify(guardedModelProgram(usage), null)
        }
    }

    @Test
    fun `same origin branches preserve nullable scalar leaf selection`() {
        verify(guardedModelProgram("""
            val user = getUser()
            val first = user.address.value()
            val chosen = if (args.isNotEmpty()) first else first
            println(chosen?.city?.value())
        """.trimIndent()), listOf("SELECT:address{city}", "Tokyo"))
    }

    @Test
    fun `origin or null branches preserve nullable scalar leaf selection`() {
        verify(guardedModelProgram("""
            val user = getUser()
            val chosen = if (args.isEmpty()) user.address.value() else null
            println(chosen?.city?.value())
        """.trimIndent()), listOf("SELECT:address{city}", "Tokyo"))
    }

    @Test
    fun `elvis with nonreturning branch preserves nullable scalar leaf selection`() {
        verify(guardedModelProgram("""
            val user = getUser()
            val address = user.address.value() ?: error("missing")
            println(address.city.value())
        """.trimIndent()), listOf("SELECT:address{city}", "Tokyo"))
    }

    @Test
    fun `scalar alternatives boxed as Any do not escape their model origins`() {
        verify("""
            import com.luxo.client.*
            class ScalarUser(@get:LuxoSelectionField("id") val id: Selected<Long>,
                @get:LuxoSelectionField("name") val name: Selected<String>)
            @LuxoSelection fun scalarUser(select: String? = null): ScalarUser {
                println("SELECT:" + select)
                val id = if (select?.contains("id") == true) Selected.value(42L) else Selected.unselected<Long>()
                val name = if (select?.contains("name") == true) Selected.value("Ada") else Selected.unselected<String>()
                return ScalarUser(id, name)
            }
            fun main(args: Array<String>) {
                val user = scalarUser()
                val first: Any = if (args.isEmpty()) user.id.value() else "fallback"
                println(first)
                val one = scalarUser()
                val two = scalarUser()
                val second: Any = if (args.isNotEmpty()) one.id.value() else two.name.value()
                println(second)
            }
        """.trimIndent(), listOf("SELECT:id", "42", "SELECT:id", "SELECT:name", "Ada"))
    }

    @Test
    fun `nullable interface branches cannot hide opaque model consumption`() {
        verify(guardedModelProgram("""
            val user = getUser()
            println(user.address.value()?.city?.value())
            val marker: Marker? = user.address.value()
            consumeMarker(if (args.isNotEmpty()) marker else null)
        """.trimIndent()), null)
    }

    private fun guardedModelProgram(body: String): String = guardedModelDeclarations +
        "\nfun main(args: Array<String>) { $body }\n"

    private val guardedModelDeclarations = """
        import com.luxo.client.*
        interface Marker
        class Leaf(@get:LuxoSelectionField("secret") val secret: Selected<String>)
        class Address(@get:LuxoSelectionField("city") val city: Selected<String>,
            @get:LuxoSelectionField("child") val child: Selected<Leaf?>) : Marker
        class User(@get:LuxoSelectionField("address") val address: Selected<Address?>)
        @LuxoSelection fun getUser(select: String? = null): User {
            println("SELECT:" + select)
            val city = if (select?.contains("city") == true) Selected.value("Tokyo") else Selected.unselected<String>()
            val secret = if (select?.contains("secret") == true) Selected.value("private") else Selected.unselected<String>()
            return User(Selected.value(Address(city, Selected.value(Leaf(secret)))))
        }
        fun consumeSelected(value: Selected<Leaf?>?) { println(value?.value()?.secret?.value()) }
        fun consumeAddress(value: Address?) { println(value?.child?.value()?.secret?.value()) }
        fun consumeAny(value: Any?) { consumeAddress(value as Address?) }
        fun consumeMarker(value: Marker?) { consumeAddress(value as Address?) }

    """.trimIndent() + "\n"

    @Test
    fun `vararg escapes and unapplied API references fail compilation`() {
        val declarations = """
            import com.luxo.client.*
            class User(@get:LuxoSelectionField("id") val id: Long)
            @LuxoSelection fun getUser(select: String? = null) = User(1)
        """.trimIndent()
        verify(declarations + "\nfun main() { val user = getUser(); println(user.id); println(listOf(user)) }", null)
        verify(declarations + "\nfun main() { val callable = ::getUser; println(callable(null).id) }", null)
    }

    @Test
    fun `real generated client sends per-call selection over HTTP Binary`() {
        val schema = LuxoSchema(
            models = mapOf("User" to LuxoModel("User", fields = listOf(
                LuxoField(1, "id", "Int"), LuxoField(2, "name", "String")))),
            apis = mapOf("getUser" to LuxoAPI(1, "getUser", "user", returnType = "User")),
        )
        val sources = mapOf("Types.kt" to LuxoCodegen.genTypes(schema, "probe"),
            "Schema.kt" to LuxoCodegen.genSchemaMap(schema, "probe"),
            "LuxoClient.kt" to LuxoCodegen.genClient(schema, "probe"))
        verify("""
            import com.luxo.client.*
            import kotlinx.coroutines.runBlocking
            import com.sun.net.httpserver.HttpServer
            import java.net.InetSocketAddress
            import okhttp3.OkHttpClient
            import probe.LuxoClient
            var expectedPlan: PreparedSelection? = null
            class Wire(private val real: Transport) : Transport by real {
                private var schema = emptyMap<String, APISchemaEntry>()
                override suspend fun call(api: String, params: Map<String, Any?>): Any = error("dynamic path")
                override suspend fun call(api: String, request: GeneratedRequest): Any {
                    if (expectedPlan != null) check(request.selectionPlan === expectedPlan)
                    else check(request.selectionPlan != null)
                    check(request.toJSONParameters() == mapOf("\${'$'}select" to request.selection))
                    val metadata = schema.getValue(api)
                    val bytes = LuxoBinaryProtocol.encodeRequest(metadata, request)
                    check(bytes.contentEquals(LuxoBinaryProtocol.encodeRequest(metadata, mapOf("\${'$'}select" to request.selection))))
                    println(request.selection)
                    return real.call(api, request)
                }
                override fun setSchema(schema: Map<String, APISchemaEntry>) {
                    this.schema = schema; real.setSchema(schema)
                }
            }
            fun main() = runBlocking {
                val server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
                server.createContext("/luvia") { exchange ->
                    try {
                        val bytes = exchange.requestBody.use { it.readBytes() }
                        check(bytes.size == 5 && bytes[0] == 1.toByte() && bytes[1] == 2.toByte() &&
                            bytes[2] == 1.toByte() && bytes[4] == 0.toByte())
                        val mask = bytes[3].toInt()
                        check(mask == 1 || mask == 2)
                        val encoder = LuxoEncoder()
                        encoder.writeVarint(if (mask == 2) 3L else 0L)
                        if (mask == 1) encoder.writeFieldInt(1, 42L)
                        else encoder.writeFieldString(2, "Ada")
                        encoder.writeEnd()
                        val body = encoder.bytes()
                        exchange.sendResponseHeaders(200, body.size.toLong())
                        exchange.responseBody.use { it.write(body) }
                    } catch (error: Exception) {
                        exchange.sendResponseHeaders(500, -1)
                    } finally { exchange.close() }
                }
                server.start()
                val http = OkHttpClient()
                try {
                    val endpoint = "http://127.0.0.1:" + server.address.port + "/luvia"
                    val client = LuxoClient(Wire(OkHttpTransport(endpoint, client = http, mode = TransportMode.BINARY)))
                    val first = client.getUser()
                    println(first.id.value())
                    val second = client.getUser()
                    println(second.name.value())
                    expectedPlan = PreparedSelection("id")
                    val planned = client.getUser(`${'$'}selectionPlan` = expectedPlan)
                    println(planned.id.value())
                    val overridden = client.getUser(select = "name", `${'$'}selectionPlan` = expectedPlan)
                    println(overridden.name.value())
                } finally {
                    http.dispatcher.executorService.shutdownNow()
                    http.connectionPool.evictAll()
                    server.stop(0)
                }
            }
        """.trimIndent(), listOf("id", "42", "name", "Ada", "id", "42", "name", "Ada"), sources)
    }

    @Test
    fun `mutable aliases assignments interpolation and spreads cannot hide model escape`() {
        for (usage in listOf(
            "var alias = row; println(alias.name.value())",
            "var destination: CoverageRow? = null; destination = row; println(destination?.name?.value())",
            "println(\"row=${'$'}row\")",
            "consume(row)",
        )) {
            verify(coverageModelDeclarations + "\nfun main() { val row = getRow(); println(row.id.value()); $usage }", null)
        }
        verify(coverageModelDeclarations + """
            @LuxoSelection fun rows(select: String? = null): Array<CoverageRow> {
                println(select)
                return arrayOf(coverageRow())
            }
            fun main() {
                val values = rows()
                println(values.map { it.id.value() })
                consume(*values)
            }
        """.trimIndent(), null)
    }

    @Test
    fun `callback variables require explicit selection and malformed callback metadata fails safely`() {
        val subscription = coverageModelDeclarations + """
            @LuxoSelection fun subscribe(select: String? = null, onData: (CoverageRow) -> Unit) {
                println(select)
                onData(coverageRow())
            }
        """.trimIndent()
        verify(subscription + "\nfun main() { val callback: (CoverageRow) -> Unit = { println(it.id.value()) }; subscribe(onData = callback) }", null)
        verify(subscription + "\nfun main() { val callback: (CoverageRow) -> Unit = { println(it.id.value()) }; subscribe(select = \"id\", onData = callback) }", listOf("id", "42"))
        for (parameters in listOf("()", "(CoverageRow, CoverageRow)")) {
            val invoke = if (parameters == "()") "onData()" else "onData(coverageRow(), coverageRow())"
            val callback = if (parameters == "()") "{ println(42) }" else "{ left, right -> println(left.id.value() + right.id.value()) }"
            verify(coverageModelDeclarations + "\n@LuxoSelection fun subscribe(select: String? = null, onData: $parameters -> Unit) { $invoke }\nfun main() { subscribe(onData = $callback) }", null)
        }
    }

    @Test
    fun `nullable scalar probes and exact descendant paths retain independent selection rules`() {
        verify(nullableDeclarations + """
            fun main() {
                val user = getUser(true)
                val city = user.address.value()?.city?.value()
                println(city == null)
                println(city === null)
            }
        """.trimIndent(), listOf("address{city}", "false", "false"))
        verify("""
            import com.luxo.client.*
            class Optional(@get:LuxoSelectionField("value") val value: Selected<String?>)
            @LuxoSelection fun optional(select: String? = null): Optional {
                println(select)
                return Optional(Selected.value(null))
            }
            fun main() {
                val result = optional()
                println(result.value.value() == null)
                println(result.value.value() === null)
            }
        """.trimIndent(), listOf("value", "true", "true"))
        verify(coverageModelDeclarations + """
            @LuxoSelection fun maybe(select: String? = null): CoverageRow? {
                println(select)
                return coverageRow()
            }
            fun main() {
                val row = maybe()
                println(row?.id?.value())
                println(row == null)
            }
        """.trimIndent(), listOf("id", "42", "false"))
        val sibling = nullableDeclarations + """
            class PairUser(@get:LuxoSelectionField("id") val id: Selected<Long>,
                @get:LuxoSelectionField("address") val address: Selected<Address?>,
                @get:LuxoSelectionField("backup") val backup: Selected<Address?>)
            @LuxoSelection fun pair(select: String? = null) = PairUser(Selected.value(42L),
                Selected.value(null), Selected.value(Address(Selected.value("Tokyo"))))
            fun main() {
                val user = pair()
                println(user.id.value())
                println(user.address.value() == null)
                println(user.backup.value()?.city?.value())
            }
        """.trimIndent()
        verify(sibling, null)
    }

    @Test
    fun `unknown generic and star projected field shapes require explicit selection`() {
        for (declaration in listOf(
            "class Box<T>(@get:LuxoSelectionField(\"value\") val value: T)",
            "class Box(@get:LuxoSelectionField(\"value\") val value: Any)",
            "class Box(@get:LuxoSelectionField(\"value\") val value: List<*>)",
        )) {
            val value = if (declaration.contains("List<*>")) "listOf(coverageRow())" else "coverageRow()"
            verify(coverageModelDeclarations + "\n$declaration\n@LuxoSelection fun box(select: String? = null) = Box($value)\nfun main() { val result = box(); println(result.value) }", null)
        }
    }

    @Test
    fun `deep immutable aliases and nested type shapes stop inference safely`() {
        val aliases = buildString {
            append("fun main() { val root = getRow(); println(root.id.value()); val alias0 = root; ")
            for (index in 1..130) append("val alias$index = alias${index - 1}; ")
            append("println(alias130.name.value()) }")
        }
        verify(coverageModelDeclarations + "\n" + aliases, null)
        val nested = "List<".repeat(66) + "String" + ">".repeat(66)
        verify(coverageModelDeclarations + """
            class Deep(@get:LuxoSelectionField("values") val values: $nested)
            @LuxoSelection fun deep(select: String? = null) = Deep(emptyList())
            fun main() { println(deep().values.size) }
        """.trimIndent(), null)
    }

    @Test
    fun `missing selection and incompatible plan parameters report inference errors`() {
        verify(coverageModelDeclarations + "\n@LuxoSelection fun malformed() = coverageRow()\nfun main() { println(malformed().id.value()) }", null)
        verify(coverageModelDeclarations + """
            @LuxoSelection fun malformed(select: String? = null, `${'$'}selectionPlan`: String? = null) = coverageRow()
            fun main() { println(malformed().id.value()) }
        """.trimIndent(), null)
        verify("""
            import com.luxo.client.*
            @LuxoSelection fun scalar(select: String? = null): Long = 42L
            fun main(args: Array<String>) { println(if (args.isEmpty()) scalar() else null) }
        """.trimIndent(), null)
    }

    @Test
    fun `opaque extension and external page shaped properties require explicit selection`() {
        verify(coverageModelDeclarations + "\nval CoverageRow.items: Long get() = id.value()\nfun main() { val row = getRow(); println(row.id.value()); println(row.items) }", null)
        verify(coverageModelDeclarations + """
            @LuxoSelection fun boxed(select: String? = null) = external.Box(coverageRow())
            fun main() { println(boxed().items.id.value()) }
        """.trimIndent(), null, mapOf("External.kt" to "package external\nclass Box<T>(val items: T)"))
        verify("""
            import com.luxo.client.*
            interface Empty
            @LuxoSelection fun empty(select: String? = null): Empty = object : Empty {}
            fun main() { println(empty()) }
        """.trimIndent(), null)
        verify("""
            import com.luxo.client.*
            @LuxoSelection fun empty(select: String? = null): java.io.Serializable = "value"
            fun main() { println(empty()) }
        """.trimIndent(), null)
        verify("""
            import com.luxo.client.*
            class Plain { @JvmField val tag = 1 }
            @LuxoSelection fun plain(select: String? = null) = Plain()
            fun main() { println(plain().tag) }
        """.trimIndent(), null)
    }

    @Test
    fun `conditional Unit and deeply wrapped null results cannot erase model provenance`() {
        verify(guardedModelProgram("""
            val user = getUser()
            println(user.address.value()?.city?.value())
            consumeAny(if (args.isEmpty()) { } else user.address.value())
        """.trimIndent()), null)
        verify(guardedModelProgram("""
            val user = getUser()
            val chosen = if (args.isEmpty()) user.address.value() else (null as Address?)
            println(chosen?.city?.value())
        """.trimIndent()), listOf("SELECT:address{city}", "Tokyo"))
        verify(guardedModelProgram("""
            val user = getUser()
            val chosen = if (args.isEmpty()) user.address.value() else { val ignored = 1; null }
            println(chosen?.city?.value())
        """.trimIndent()), listOf("SELECT:address{city}", "Tokyo"))
        var deepNull = "null"
        repeat(130) { deepNull = "($deepNull as Any?)" }
        verify(guardedModelProgram("""
            val user = getUser()
            println(user.address.value()?.city?.value())
            consumeAny(if (args.isEmpty()) user.address.value() else $deepNull)
        """.trimIndent()), null)
    }

    @Test
    fun `Java public fields require explicit selection without generated getters`() {
        val declarations = """
            import com.luxo.client.*
            @LuxoSelection fun fields(select: String? = null): SelectionPublicFields {
                println(select)
                return SelectionPublicFields()
            }
        """.trimIndent()
        verify(declarations + "\nfun main() { println(fields().id) }", null)
        verify(declarations + "\nfun main() { println(fields(select = \"id,name\").id) }", listOf("id,name", "42"))
    }

    @Test
    fun `nonreturning API results require explicit selection`() {
        val declarations = """
            import com.luxo.client.*
            @LuxoSelection fun terminal(select: String? = null): Nothing = error("not-called")
        """.trimIndent()
        verify(declarations + "\nfun main() { terminal() }", null)
        verify(declarations + "\n" + """
            fun main() {
                if (System.getProperty("luxo.selection.callTerminal") == "yes") terminal(select = "id")
                println("idle")
            }
        """.trimIndent(), listOf("idle"))
    }

    @Test
    fun `changed dependency generic arity retains conservative field selection`() {
        val directory = Files.createTempDirectory(File(".tmp").apply { mkdirs() }.toPath(), "selection-dependency-").toFile()
        try {
            val dependencies = changedArityDependencies(directory)
            val declarations = """
                import com.luxo.client.*
                @LuxoSelection fun load(select: String? = null): ChangedArityBox<*> {
                    println("SELECT:" + select)
                    return ChangedArityBox.create()
                }
            """.trimIndent()
            verify(declarations + "\nfun main() { val value: Any? = load().value; println(value) }",
                null, dependencies = dependencies)
            verify(declarations + "\nfun main() { val value: Any? = load(select = \"value\").value; println(value) }",
                listOf("SELECT:value", "null"), dependencies = dependencies)
        } finally {
            check(directory.deleteRecursively()) { "Cannot remove selection dependency fixture" }
        }
    }

    private fun changedArityDependencies(directory: File): List<File> {
        val classpath = System.getProperty("luxo.testRuntimeClasspath")
            .split(File.pathSeparator).filter { File(it).exists() }.joinToString(File.pathSeparator)
        val carrier = File(directory, "ChangedArityCarrier.kt")
        carrier.writeText("interface ChangedArityCarrier<T>")
        val original = compile(directory, classpath, listOf(carrier), "carrier-original", plugin = false)
        assertEquals(0, original.first, original.second)
        val box = File(directory, "ChangedArityBox.java").apply { writeText("""
            import com.luxo.client.LuxoSelectionField;
            public class ChangedArityBox<T extends ChangedArityCarrier<T>> {
                @LuxoSelectionField(name="value") public T getValue() { return null; }
                public static ChangedArityBox<?> create() { return new ChangedArityBox<>(); }
            }
        """.trimIndent()) }
        val javaClasses = File(directory, "box").apply { mkdirs() }
        val compiled = run(directory, listOf("-classpath", File(directory, "carrier-original").path +
            File.pathSeparator + classpath, "-d", javaClasses.path, box.path), tool = "javac")
        assertEquals(0, compiled.first, compiled.second)
        carrier.writeText("interface ChangedArityCarrier<T, U>")
        val replacement = compile(directory, classpath, listOf(carrier), "carrier-replacement", plugin = false)
        assertEquals(0, replacement.first, replacement.second)
        return listOf(File(directory, "carrier-replacement"), javaClasses)
    }

    private val coverageModelDeclarations = """
        import com.luxo.client.*
        class CoverageRow(@get:LuxoSelectionField("id") val id: Selected<Long>,
            @get:LuxoSelectionField("name") val name: Selected<String>)
        fun coverageRow() = CoverageRow(Selected.value(42L), Selected.value("Ada"))
        @LuxoSelection fun getRow(select: String? = null): CoverageRow {
            println(select)
            return coverageRow()
        }
        fun consume(vararg values: CoverageRow) { println(values.map { it.name.value() }) }

    """.trimIndent() + "\n"

    private fun verify(sourceText: String, expected: List<String>?, extras: Map<String, String> = emptyMap(),
        dependencies: List<File> = emptyList()) {
        val directory = Files.createTempDirectory(File(".tmp").apply { mkdirs() }.toPath(), "selection-plugin-").toFile()
        try {
            val source = File(directory, "Probe.kt").apply { writeText(sourceText) }
            val extraFiles = extras.map { (name, content) -> File(directory, name).apply { writeText(content) } }
            var classpath = System.getProperty("luxo.testRuntimeClasspath")
                .split(File.pathSeparator).filter { File(it).exists() }.joinToString(File.pathSeparator)
            if (dependencies.isNotEmpty()) {
                classpath = dependencies.joinToString(File.pathSeparator) { it.path } + File.pathSeparator + classpath
            }
            if (extraFiles.isNotEmpty()) {
                val dependency = compile(directory, classpath, extraFiles, "dependency", plugin = false)
                assertEquals(0, dependency.first, dependency.second)
                classpath = File(directory, "dependency").path + File.pathSeparator + classpath
            }
            val compiled = compile(directory, classpath, listOf(source), "classes", plugin = true)
            if (expected == null) {
                assertNotEquals(0, compiled.first, compiled.second)
                assertContains(compiled.second, "cannot infer Luxo selection", ignoreCase = true)
                return
            }
            assertEquals(0, compiled.first, compiled.second)
            val result = run(directory, listOf("-cp", File(directory, "classes").path + File.pathSeparator + classpath, "ProbeKt"))
            assertEquals(0, result.first, result.second)
            assertEquals(expected, result.second.trim().lines())
        } finally {
            directory.deleteRecursively()
        }
    }

    private fun compile(directory: File, classpath: String, sources: List<File>, output: String, plugin: Boolean): Pair<Int, String> {
        val arguments = listOf("-cp", classpath, "org.jetbrains.kotlin.cli.jvm.K2JVMCompiler",
            "-no-stdlib", "-no-reflect", "-classpath", classpath, "-Xverify-ir=error",
            "-Xfriend-paths=${File("build/classes/kotlin/main").absolutePath}", "-d", File(directory, output).path)
        val plugins = if (plugin) listOf("-Xplugin=${System.getProperty("luxo.testPluginJar")}") else emptyList()
        return run(directory, arguments + plugins + sources.map { it.path })
    }

    private fun run(directory: File, arguments: List<String>, tool: String = "java"): Pair<Int, String> {
        val log = File(directory, "process.log")
        val executable = File(System.getProperty("java.home"), "bin/$tool").path
        val temporaryDirectory = (if (tool == "javac") "-J" else "") + "-Djava.io.tmpdir=${directory.absolutePath}"
        val process = ProcessBuilder(listOf(executable, temporaryDirectory) + arguments)
            .redirectErrorStream(true).redirectOutput(log).start()
        try {
            check(process.waitFor(90, TimeUnit.SECONDS)) { "Compiler test timed out" }
            return process.exitValue() to log.readText()
        } finally {
            if (process.isAlive) { process.destroyForcibly(); process.waitFor() }
        }
    }
}
