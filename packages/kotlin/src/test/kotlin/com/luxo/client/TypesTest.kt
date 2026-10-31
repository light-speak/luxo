package com.luxo.client

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue
import kotlin.test.assertFailsWith
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.SerializationException
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.encodeToJsonElement

class PageTest {

    @Test
    fun `page metadata preserves int64 precision in JSON`() {
        for (value in listOf(2147483648L, 4294967296L, 9007199254740993L, Long.MAX_VALUE)) {
            val json = """{"items":["x"],"total":$value,"page":$value,"pageSize":$value}"""
            val page = Json.decodeFromString<Page<String>>(json)
            assertEquals(value, page.total)
            assertEquals(value, page.page)
            assertEquals(value, page.pageSize)
            assertEquals(json, Json.encodeToString(Page.serializer(String.serializer()), page))
        }
    }

    @Test
    fun `page data class properties`() {
        val page = Page(
            items = listOf("a", "b", "c"),
            total = 10,
            page = 1,
            pageSize = 3,
        )

        assertEquals(listOf("a", "b", "c"), page.items)
        assertEquals(10L, page.total)
        assertEquals(1L, page.page)
        assertEquals(3L, page.pageSize)
    }

    @Test
    fun `page serialization round-trip`() {
        val json = """{"items":["x","y"],"total":5,"page":2,"pageSize":2}"""
        val page = Json.decodeFromString<Page<String>>(json)
        assertEquals(listOf("x", "y"), page.items)
        assertEquals(5L, page.total)
        assertEquals(2L, page.page)
        assertEquals(2L, page.pageSize)

        val encoded = Json.encodeToString(Page.serializer(String.serializer()), page)
        assertEquals(json, encoded)
    }
}

class LuxoSchemaTest {

    @Test
    fun `Go introspection metadata is preserved without ignoring unknown fields`() {
        val source = """
        {
          "models":{"Project":{"name":"Project","module":"project","usage":"output","description":"项目","directives":["crud"],
            "fields":[{"id":1,"name":"owner","type":"Model","typeName":"User","relation":true,"computed":true,
              "primaryKey":true,"description":"负责人","directives":["by"],"relationKind":"belongsTo",
              "localKey":"ownerId","remoteKey":"id","targetModule":"user","module":"project","foreignKey":"ownerId"},
              {"id":2,"name":"taskCount","type":"Int","computed":true}],
            "computedSources":{"taskCount":{"model":"Task","module":"project"}}}},
          "enums":{"Role":{"name":"Role","module":"user","values":["ADMIN"],"description":"权限"}},
          "types":{"Precision":{"name":"Precision","module":"precision","usage":"inputOutput","description":"精度",
            "directives":["example"],"fields":[{"id":1,"name":"value","type":"Int","description":"完整整数"}]}},
          "apis":{"echo":{"id":1,"name":"echo","module":"precision","returnType":"Precision",
            "description":"返回输入","directives":["native"],"deprecated":true,"deprecatedReason":"替代示例",
            "params":[{"id":1,"name":"input","type":"Model","typeName":"Precision","description":"输入对象"}]}}
        }
        """.trimIndent()
        val schema = Json.decodeFromString<LuxoSchema>(source)
        assertEquals(Json.parseToJsonElement(source), Json.encodeToJsonElement(schema))
        assertFailsWith<SerializationException> {
            Json.decodeFromString<LuxoSchema>("""{"futureWireEncoding":"unknown"}""")
        }
    }

    @Test
    fun `schema defaults to empty maps`() {
        val schema = LuxoSchema()
        assertEquals(emptyMap(), schema.models)
        assertEquals(emptyMap(), schema.apis)
        assertEquals(emptyMap(), schema.enums)
        assertEquals(emptyMap(), schema.types)
    }

    @Test
    fun `schema deserialization`() {
        val json = """
        {
            "models": {
                "User": {
                    "name": "User",
                    "usage": "output",
                    "fields": [
                        {"id": 1, "name": "id", "type": "Int"},
                        {"id": 2, "name": "name", "type": "String", "nullable": true}
                    ]
                }
            },
            "enums": {
                "Role": {"name": "Role", "values": ["ADMIN", "USER"]}
            },
            "apis": {},
            "types": {}
        }
        """.trimIndent()

        val schema = Json.decodeFromString<LuxoSchema>(json)

        assertEquals(1, schema.models.size)
        val user = schema.models["User"]!!
        assertEquals("User", user.name)
        assertEquals(TypeUsage.OUTPUT, user.usage)
        assertEquals(2, user.fields.size)
        assertEquals(1, user.fields[0].id)
        assertEquals("id", user.fields[0].name)
        assertEquals("Int", user.fields[0].type)
        assertFalse(user.fields[0].nullable)
        assertTrue(user.fields[1].nullable)

        val role = schema.enums["Role"]!!
        assertEquals("Role", role.name)
        assertEquals(listOf("ADMIN", "USER"), role.values)
    }

    @Test
    fun `stream metadata is preserved during deserialization`() {
        val schema = Json.decodeFromString<LuxoSchema>(
            """{"apis":{"watchUser":{"id":2,"name":"watchUser","module":"user","stream":true}}}""",
        )

        assertTrue(schema.apis.getValue("watchUser").stream)
    }
}

class SelectedTest {

    @Test
    fun `distinguishes unselected selected null and selected value`() {
        val absent = Selected.unselected<String?>()
        val selectedNull = Selected.value<String?>(null)
        val selectedValue = Selected.value<String?>("Luxo")

        assertFalse(absent.isSelected)
        assertFailsWith<IllegalStateException> { absent.value() }
        assertTrue(selectedNull.isSelected)
        assertNull(selectedNull.value())
        assertEquals("Luxo", selectedValue.value())
    }
}

class LuxoFieldTest {

    @Test
    fun `field defaults`() {
        val field = LuxoField(id = 1, name = "test", type = "String")
        assertNull(field.typeName)
        assertFalse(field.nullable)
        assertFalse(field.isList)
        assertFalse(field.relation)
    }

    @Test
    fun `field with all properties`() {
        val field = LuxoField(
            id = 5,
            name = "posts",
            type = "Post",
            typeName = "Post",
            nullable = true,
            isList = true,
            relation = true,
        )
        assertEquals(5, field.id)
        assertEquals("posts", field.name)
        assertEquals("Post", field.type)
        assertEquals("Post", field.typeName)
        assertTrue(field.nullable)
        assertTrue(field.isList)
        assertTrue(field.relation)
    }
}

class LuxoAPITest {

    @Test
    fun `api defaults`() {
        val api = LuxoAPI(id = 1, name = "getUser", module = "user")
        assertNull(api.returnType)
        assertFalse(api.returnList)
        assertFalse(api.paginated)
        assertNull(api.defaultPageSize)
        assertEquals(emptyList(), api.params)
    }

    @Test
    fun `api with params`() {
        val api = LuxoAPI(
            id = 10,
            name = "listUsers",
            module = "user",
            returnType = "User",
            returnList = true,
            paginated = true,
            defaultPageSize = 50,
            params = listOf(
                LuxoParam(id = 1, name = "page", type = "Int"),
                LuxoParam(id = 2, name = "pageSize", type = "Int"),
            ),
        )
        assertEquals(10, api.id)
        assertEquals("listUsers", api.name)
        assertEquals("user", api.module)
        assertEquals("User", api.returnType)
        assertTrue(api.returnList)
        assertTrue(api.paginated)
        assertEquals(50, api.defaultPageSize)
        assertEquals(2, api.params.size)
        assertEquals("page", api.params[0].name)
    }
}

class TransportModeTest {

    @Test
    fun `enum values`() {
        assertEquals(2, TransportMode.entries.size)
        assertEquals(TransportMode.JSON, TransportMode.valueOf("JSON"))
        assertEquals(TransportMode.BINARY, TransportMode.valueOf("BINARY"))
    }
}
