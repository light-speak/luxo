import 'schema.dart';

/// Shared by code generation and transport registration, never by request encoding.
void checkSchemaID(
  int id,
  int maximum,
  Set<int> seen,
  String owner,
  String name,
) {
  if (id < 1 || id > maximum) {
    throw FormatException('$owner.$name: ID must be from 1 to $maximum');
  }
  if (!seen.add(id)) throw FormatException('$owner.$name: duplicate ID $id');
}

final _selectionSnapshots = Expando<bool>();

bool isSelectionSnapshot(APISchemaEntry schema) =>
    _selectionSnapshots[schema] == true;

typedef _Fields = Map<String, SelectionFieldSchema>;
typedef _Types = Map<String, _Fields>;

/// Own immutable metadata while preserving shared type directories within one registration.
Map<String, APISchemaEntry> snapshotAPISchema(
  Map<String, APISchemaEntry> schema,
) {
  final seen = <int>{};
  final snapshots = _SchemaSnapshots();
  return Map.unmodifiable(
    schema.map((name, entry) {
      final id = entry.id;
      checkSchemaID(id, maxAPIID, seen, 'API', name);
      final copy = APISchemaEntry(
        id,
        _snapshotParams(entry.params, name),
        snapshots.fields(entry.fields, 'API $name'),
        snapshots.types(entry.types),
        snapshots.response(entry.response),
      );
      _selectionSnapshots[copy] = true;
      return MapEntry(name, copy);
    }),
  );
}

List<ParamSchema> _snapshotParams(List<ParamSchema> params, String api) {
  final seen = <int>{};
  final names = params.length > 1 ? <String>{} : null;
  final owner = 'API $api parameter';
  return List.unmodifiable(
    params.map((param) {
      final copy = ParamSchema(
        param.fieldID,
        param.name,
        param.type,
        param.isList,
        param.nullable,
        param.typeName,
      );
      checkSchemaID(copy.fieldID, maxParamID, seen, owner, copy.name);
      if (names != null && !names.add(copy.name)) {
        throw FormatException('$owner: duplicate name ${copy.name}');
      }
      return copy;
    }),
  );
}

final class _SchemaSnapshots {
  final _fieldCopies = Map<_Fields, _Fields>.identity();
  final _typeCopies = Map<_Types, _Types>.identity();

  JSONResponseSchema? response(JSONResponseSchema? value) {
    if (value == null) return null;
    final directory = types(value.types);
    if (value.modelType != null && !directory.containsKey(value.modelType)) {
      throw FormatException('missing JSON model schema: ${value.modelType}');
    }
    return JSONResponseSchema(
      modelType: value.modelType,
      types: directory,
      isList: value.isList,
      paginated: value.paginated,
    );
  }

  _Fields fields(_Fields fields, String owner) {
    final cached = _fieldCopies[fields];
    if (cached != null) return cached;
    final seen = <int>{};
    final copy = Map<String, SelectionFieldSchema>.unmodifiable(
      fields.map((name, field) {
        final value = SelectionFieldSchema(
          field.fieldID,
          field.typeName,
          field.type,
          field.isList,
          field.nullable,
        );
        checkSchemaID(value.fieldID, maxSelectionFieldID, seen, owner, name);
        return MapEntry(name, value);
      }),
    );
    _fieldCopies[fields] = copy;
    return copy;
  }

  _Types types(_Types types) {
    final cached = _typeCopies[types];
    if (cached != null) return cached;
    final copy = Map<String, _Fields>.unmodifiable(
      types.map((name, value) => MapEntry(name, fields(value, 'type $name'))),
    );
    _typeCopies[types] = copy;
    return copy;
  }
}
