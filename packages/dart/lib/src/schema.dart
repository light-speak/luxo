const maxSelectionFieldID = 65535;
const maxAPIID = 2147483647;
const maxParamID = maxAPIID - 2;

/// API schema metadata for binary encoding and exact JSON parameter validation.
class APISchemaEntry {
  final int id;
  final List<ParamSchema> params;
  final Map<String, SelectionFieldSchema> fields;
  final Map<String, Map<String, SelectionFieldSchema>> types;
  final JSONResponseSchema? response;
  const APISchemaEntry(
    this.id, [
    this.params = const [],
    this.fields = const {},
    this.types = const {},
    this.response,
  ]);
}

/// Static response shape; generic JSON roots and model objects have separate budgets.
final class JSONResponseSchema {
  final String? modelType;
  final Map<String, Map<String, SelectionFieldSchema>> types;
  final bool isList;
  final bool paginated;

  const JSONResponseSchema({
    this.modelType,
    this.types = const {},
    this.isList = false,
    this.paginated = false,
  });
}

class SelectionFieldSchema {
  final int fieldID;
  final String? typeName;
  final String? type;
  final bool isList;
  final bool nullable;
  const SelectionFieldSchema(
    this.fieldID, [
    this.typeName,
    this.type,
    this.isList = false,
    this.nullable = false,
  ]);
}

class ParamSchema {
  final int fieldID;
  final String name;
  final String type;
  final bool isList;
  final bool nullable;
  final String? typeName;
  const ParamSchema(
    this.fieldID,
    this.name,
    this.type, [
    this.isList = false,
    this.nullable = false,
    this.typeName,
  ]);
}
