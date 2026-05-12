package com.luxo.client

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.json.Json

class PageTest {

    @Test
    fun `page data class properties`() {
        val page = Page(
            items = listOf("a", "b", "c"),
            total = 10,
            page = 1,
            pageSize = 3,
        )

        assertEquals(listOf("a", "b", "c"), page.items)
        assertEquals(10, page.total)
        assertEquals(1, page.page)
        assertEquals(3, page.pageSize)
    }

    @Test
    fun `page serialization round-trip`() {
        val json = """{"items":["x","y"],"total":5,"page":2,"pageSize":2}"""
        val page = Json.decodeFromString<Page<String>>(json)
        assertEquals(listOf("x", "y"), page.items)
        assertEquals(5, page.total)
        assertEquals(2, page.page)
        assertEquals(2, page.pageSize)

        val encoded = Json.encodeToString(Page.serializer(String.serializer()), page)
        assertEquals(json, encoded)
    }
}

class LuxoSchemaTest {

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
