package com.luxo.client;

import java.util.Collections;

/** Preserve ordinary Java callers of the original four-field metadata API. */
public final class SchemaSourceCompatibility {
    private SchemaSourceCompatibility() {}
    public static void verify() {
        APISchemaEntry entry = new APISchemaEntry(7, Collections.emptyList(), Collections.emptyMap(), Collections.emptyMap());
        entry = entry.copy(8, entry.getParams(), entry.getFields(), entry.getTypes());
        LuxoSchema schema = new LuxoSchema(Collections.emptyMap(), Collections.emptyMap(), Collections.emptyMap(), Collections.emptyMap());
        schema = schema.copy(schema.getModels(), schema.getApis(), schema.getEnums(), schema.getTypes());
        if (entry.getId() != 8 || !schema.getModels().isEmpty()) throw new AssertionError();
    }
}
