import 'int64.dart';
import 'json.dart';
import 'schema.dart' show maxSelectionFieldID, maxAPIID, maxParamID;
import 'schema_ids.dart' show checkSchemaID;

/// Three-state optional argument used by patch APIs.
///
/// `absent` leaves the field unchanged, while `present(null)` explicitly
/// writes a database NULL for nullable fields.
class LuxoOptional<T> {
  final bool isPresent;
  final T? value;

  const LuxoOptional.absent() : isPresent = false, value = null;

  const LuxoOptional.present(this.value) : isPresent = true;
}

/// A field-selected output value.
///
/// [Selected.unselected] means the field was not requested. For nullable
/// fields, [Selected.value] may contain null to represent a selected null.
class Selected<T> {
  final bool isSelected;
  final T? _value;

  const Selected.unselected() : isSelected = false, _value = null;

  const Selected.value(T value) : isSelected = true, _value = value;

  T get value {
    if (!isSelected) {
      throw StateError('field was not selected');
    }
    return _value as T;
  }
}

enum TypeUsage { input, output, inputOutput, unused }

TypeUsage? _typeUsageFromJson(Object? value) => switch (value) {
  'input' => TypeUsage.input,
  'output' => TypeUsage.output,
  'inputOutput' => TypeUsage.inputOutput,
  'unused' => TypeUsage.unused,
  _ => null,
};

/// Paginated list response.
class Page<T> {
  final List<T> items;
  final LuxoInt64 total;
  final LuxoInt64 page;
  final LuxoInt64 pageSize;

  const Page({
    required this.items,
    required this.total,
    required this.page,
    required this.pageSize,
  });

  /// Parse from JSON map with a per-item decoder.
  factory Page.fromJson(
    Map<String, dynamic> json,
    T Function(Map<String, dynamic>) fromJson,
  ) {
    return Page(
      items: (json['items'] as List)
          .map((e) => fromJson(e as Map<String, dynamic>))
          .toList(),
      total: luxoInt64FromJSON(json['total']),
      page: luxoInt64FromJSON(json['page']),
      pageSize: luxoInt64FromJSON(json['pageSize']),
    );
  }
}

/// Luxo schema definition (from introspection).
class LuxoSchema {
  final Map<String, LuxoModel> models;
  final Map<String, LuxoAPI> apis;
  final Map<String, LuxoEnum> enums;
  final Map<String, LuxoTypeDecl> types;

  const LuxoSchema({
    required this.models,
    required this.apis,
    this.enums = const {},
    this.types = const {},
  });

  /// Validate the complete stable ID directory before generating files.
  void validateIDs() {
    for (final model in models.values) {
      _validateFieldIDs(model.fields, 'model ${model.name}');
    }
    for (final type in types.values) {
      _validateFieldIDs(type.fields, 'type ${type.name}');
    }
    final seen = <int>{};
    for (final api in apis.values) {
      checkSchemaID(api.id, maxAPIID, seen, 'API', api.name);
      final params = <int>{};
      for (final param in api.params) {
        checkSchemaID(
          param.id,
          maxParamID,
          params,
          'API ${api.name}',
          param.name,
        );
      }
    }
  }

  factory LuxoSchema.fromJson(Map<String, dynamic> json) {
    final models = <String, LuxoModel>{};
    final apis = <String, LuxoAPI>{};
    final enums = <String, LuxoEnum>{};
    final types = <String, LuxoTypeDecl>{};
    if (json['models'] != null) {
      for (final e in (json['models'] as Map<String, dynamic>).entries) {
        models[e.key] = LuxoModel.fromJson(e.value as Map<String, dynamic>);
      }
    }
    if (json['apis'] != null) {
      for (final e in (json['apis'] as Map<String, dynamic>).entries) {
        apis[e.key] = LuxoAPI.fromJson(e.value as Map<String, dynamic>);
      }
    }
    if (json['enums'] != null) {
      for (final e in (json['enums'] as Map<String, dynamic>).entries) {
        enums[e.key] = LuxoEnum.fromJson(e.value as Map<String, dynamic>);
      }
    }
    if (json['types'] != null) {
      for (final e in (json['types'] as Map<String, dynamic>).entries) {
        types[e.key] = LuxoTypeDecl.fromJson(e.value as Map<String, dynamic>);
      }
    }
    return LuxoSchema(models: models, apis: apis, enums: enums, types: types);
  }
}

void _validateFieldIDs(List<LuxoField> fields, String owner) {
  final seen = <int>{};
  for (final field in fields) {
    checkSchemaID(field.id, maxSelectionFieldID, seen, owner, field.name);
  }
}

class LuxoEnum {
  final String name;
  final List<String> values;
  const LuxoEnum({required this.name, required this.values});
  factory LuxoEnum.fromJson(Map<String, dynamic> json) => LuxoEnum(
    name: json['name'] as String,
    values: (json['values'] as List).cast<String>(),
  );
}

class LuxoTypeDecl {
  final String name;
  final TypeUsage? usage;
  final List<LuxoField> fields;
  const LuxoTypeDecl({required this.name, this.usage, required this.fields});
  factory LuxoTypeDecl.fromJson(Map<String, dynamic> json) => LuxoTypeDecl(
    name: json['name'] as String,
    usage: _typeUsageFromJson(json['usage']),
    fields: (json['fields'] as List)
        .map((e) => LuxoField.fromJson(e as Map<String, dynamic>))
        .toList(),
  );
}

class LuxoModel {
  final String name;
  final TypeUsage? usage;
  final List<LuxoField> fields;
  final Map<String, LuxoComputedSource> computedSources;

  const LuxoModel({
    required this.name,
    this.usage,
    required this.fields,
    this.computedSources = const {},
  });

  factory LuxoModel.fromJson(Map<String, dynamic> json) => LuxoModel(
    name: json['name'] as String,
    usage: _typeUsageFromJson(json['usage']),
    fields: (json['fields'] as List)
        .map((e) => LuxoField.fromJson(e as Map<String, dynamic>))
        .toList(),
    computedSources:
        (json['computedSources'] as Map<String, dynamic>?)?.map(
          (name, value) => MapEntry(
            name,
            LuxoComputedSource.fromJson(value as Map<String, dynamic>),
          ),
        ) ??
        const {},
  );
}

class LuxoComputedSource {
  final String model;
  final String module;

  const LuxoComputedSource({required this.model, this.module = ''});

  factory LuxoComputedSource.fromJson(Map<String, dynamic> json) =>
      LuxoComputedSource(
        model: json['model'] as String,
        module: json['module'] as String? ?? '',
      );
}

class LuxoField {
  final int id;
  final String name;
  final String type;
  final String? typeName;
  final bool nullable;
  final bool isList;
  final bool relation;

  const LuxoField({
    required this.id,
    required this.name,
    required this.type,
    this.typeName,
    this.nullable = false,
    this.isList = false,
    this.relation = false,
  });

  factory LuxoField.fromJson(Map<String, dynamic> json) => LuxoField(
    id: json['id'] as int,
    name: json['name'] as String,
    type: json['type'] as String,
    typeName: json['typeName'] as String?,
    nullable: json['nullable'] as bool? ?? false,
    isList: json['isList'] as bool? ?? false,
    relation: json['relation'] as bool? ?? false,
  );
}

class LuxoAPI {
  final int id;
  final String name;
  final String module;
  final bool internal;
  final String? returnType;
  final bool returnList;
  final bool returnNullable;
  final bool paginated;
  final int? defaultPageSize;
  final bool stream;
  final List<LuxoParam> params;
  final LuxoQueryCapabilities? query;

  const LuxoAPI({
    required this.id,
    required this.name,
    required this.module,
    this.internal = false,
    this.returnType,
    this.returnList = false,
    this.returnNullable = false,
    this.paginated = false,
    this.defaultPageSize,
    this.stream = false,
    this.params = const [],
    this.query,
  });

  factory LuxoAPI.fromJson(Map<String, dynamic> json) => LuxoAPI(
    id: json['id'] as int,
    name: json['name'] as String,
    module: json['module'] as String,
    internal: json['internal'] as bool? ?? false,
    returnType: json['returnType'] as String?,
    returnList: json['returnList'] as bool? ?? false,
    returnNullable: json['returnNullable'] as bool? ?? false,
    paginated: json['paginated'] as bool? ?? false,
    defaultPageSize: json['defaultPageSize'] as int?,
    stream: json['stream'] as bool? ?? false,
    query: json['query'] == null
        ? null
        : LuxoQueryCapabilities.fromJson(json['query'] as Map<String, dynamic>),
    params:
        (json['params'] as List?)
            ?.map((e) => LuxoParam.fromJson(e as Map<String, dynamic>))
            .toList() ??
        [],
  );
}

class LuxoQueryCapabilities {
  final List<LuxoQueryFilter> filters;
  final List<String> sorters;
  const LuxoQueryCapabilities({
    this.filters = const [],
    this.sorters = const [],
  });
  LuxoQueryCapabilities.fromJson(Map<String, dynamic> json)
    : filters = (json['filters'] as List? ?? [])
          .map((e) => LuxoQueryFilter.fromJson(e as Map<String, dynamic>))
          .toList(),
      sorters = (json['sorters'] as List? ?? []).cast<String>();
}

class LuxoQueryFilter {
  final String field;
  final String type;
  final List<String> operators;
  const LuxoQueryFilter({
    required this.field,
    required this.type,
    required this.operators,
  });
  LuxoQueryFilter.fromJson(Map<String, dynamic> json)
    : field = json['field'] as String,
      type = json['type'] as String,
      operators = (json['operators'] as List).cast<String>();
}

class LuxoParam {
  final int id;
  final String name;
  final String type;
  final String? typeName;
  final bool isList;
  final bool nullable;
  final bool hasDefault;

  const LuxoParam({
    required this.id,
    required this.name,
    required this.type,
    this.typeName,
    this.isList = false,
    this.nullable = false,
    this.hasDefault = false,
  });

  factory LuxoParam.fromJson(Map<String, dynamic> json) => LuxoParam(
    id: json['id'] as int,
    name: json['name'] as String,
    type: json['type'] as String,
    typeName: json['typeName'] as String?,
    isList: json['isList'] as bool? ?? false,
    nullable: json['nullable'] as bool? ?? false,
    hasDefault: json['hasDefault'] as bool? ?? false,
  );
}
