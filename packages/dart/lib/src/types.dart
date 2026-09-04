/// Three-state optional argument used by patch APIs.
///
/// `absent` leaves the field unchanged, while `present(null)` explicitly
/// writes a database NULL for nullable fields.
class LuxoOptional<T> {
  final bool isPresent;
  final T? value;

  const LuxoOptional.absent()
      : isPresent = false,
        value = null;

  const LuxoOptional.present(this.value) : isPresent = true;
}

/// A field-selected output value.
///
/// [Selected.unselected] means the field was not requested. For nullable
/// fields, [Selected.value] may contain null to represent a selected null.
class Selected<T> {
  final bool isSelected;
  final T? _value;

  const Selected.unselected()
      : isSelected = false,
        _value = null;

  const Selected.value(T value)
      : isSelected = true,
        _value = value;

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
  final int total;
  final int page;
  final int pageSize;

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
      total: json['total'] as int,
      page: json['page'] as int,
      pageSize: json['pageSize'] as int,
    );
  }
}

/// Luxo schema definition (from introspection).
class LuxoSchema {
  final Map<String, LuxoModel> models;
  final Map<String, LuxoAPI> apis;
  final Map<String, LuxoEnum> enums;
  final Map<String, LuxoTypeDecl> types;

  const LuxoSchema(
      {required this.models,
      required this.apis,
      this.enums = const {},
      this.types = const {}});

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

  const LuxoModel({required this.name, this.usage, required this.fields});

  factory LuxoModel.fromJson(Map<String, dynamic> json) => LuxoModel(
        name: json['name'] as String,
        usage: _typeUsageFromJson(json['usage']),
        fields: (json['fields'] as List)
            .map((e) => LuxoField.fromJson(e as Map<String, dynamic>))
            .toList(),
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
  final String? returnType;
  final bool returnList;
  final bool paginated;
  final bool stream;
  final List<LuxoParam> params;

  const LuxoAPI({
    required this.id,
    required this.name,
    required this.module,
    this.returnType,
    this.returnList = false,
    this.paginated = false,
    this.stream = false,
    this.params = const [],
  });

  factory LuxoAPI.fromJson(Map<String, dynamic> json) => LuxoAPI(
        id: json['id'] as int,
        name: json['name'] as String,
        module: json['module'] as String,
        returnType: json['returnType'] as String?,
        returnList: json['returnList'] as bool? ?? false,
        paginated: json['paginated'] as bool? ?? false,
        stream: json['stream'] as bool? ?? false,
        params: (json['params'] as List?)
                ?.map((e) => LuxoParam.fromJson(e as Map<String, dynamic>))
                .toList() ??
            [],
      );
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
