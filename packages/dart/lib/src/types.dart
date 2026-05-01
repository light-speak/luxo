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
      items: (json['items'] as List).map((e) => fromJson(e as Map<String, dynamic>)).toList(),
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

  const LuxoSchema({required this.models, required this.apis});

  factory LuxoSchema.fromJson(Map<String, dynamic> json) {
    final models = <String, LuxoModel>{};
    final apis = <String, LuxoAPI>{};
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
    return LuxoSchema(models: models, apis: apis);
  }
}

class LuxoModel {
  final String name;
  final List<LuxoField> fields;

  const LuxoModel({required this.name, required this.fields});

  factory LuxoModel.fromJson(Map<String, dynamic> json) => LuxoModel(
        name: json['name'] as String,
        fields: (json['fields'] as List).map((e) => LuxoField.fromJson(e as Map<String, dynamic>)).toList(),
      );
}

class LuxoField {
  final int id;
  final String name;
  final String type;
  final bool nullable;
  final bool isList;

  const LuxoField({
    required this.id,
    required this.name,
    required this.type,
    this.nullable = false,
    this.isList = false,
  });

  factory LuxoField.fromJson(Map<String, dynamic> json) => LuxoField(
        id: json['id'] as int,
        name: json['name'] as String,
        type: json['type'] as String,
        nullable: json['nullable'] as bool? ?? false,
        isList: json['isList'] as bool? ?? false,
      );
}

class LuxoAPI {
  final int id;
  final String name;
  final String module;
  final String? returnType;
  final bool returnList;
  final bool paginated;
  final List<LuxoParam> params;

  const LuxoAPI({
    required this.id,
    required this.name,
    required this.module,
    this.returnType,
    this.returnList = false,
    this.paginated = false,
    this.params = const [],
  });

  factory LuxoAPI.fromJson(Map<String, dynamic> json) => LuxoAPI(
        id: json['id'] as int,
        name: json['name'] as String,
        module: json['module'] as String,
        returnType: json['returnType'] as String?,
        returnList: json['returnList'] as bool? ?? false,
        paginated: json['paginated'] as bool? ?? false,
        params: (json['params'] as List?)?.map((e) => LuxoParam.fromJson(e as Map<String, dynamic>)).toList() ?? [],
      );
}

class LuxoParam {
  final int id;
  final String name;
  final String type;

  const LuxoParam({required this.id, required this.name, required this.type});

  factory LuxoParam.fromJson(Map<String, dynamic> json) => LuxoParam(
        id: json['id'] as int,
        name: json['name'] as String,
        type: json['type'] as String,
      );
}
