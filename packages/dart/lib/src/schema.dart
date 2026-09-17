/// API schema metadata for binary encoding and exact JSON parameter validation.
class APISchemaEntry {
  final int id;
  final List<ParamSchema> params;
  final Map<String, SelectionFieldSchema> fields;
  final Map<String, Map<String, SelectionFieldSchema>> types;
  const APISchemaEntry(
    this.id, [
    this.params = const [],
    this.fields = const {},
    this.types = const {},
  ]);
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
