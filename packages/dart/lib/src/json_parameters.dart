part of 'json.dart';

/// Transport-internal encoder; compile metadata once, validate while writing.
final class JSONRequestEncoder {
  Map<String, _JSONObject> _plans = {};

  void setSchema(Map<String, APISchemaEntry> schema) {
    final plans = <String, _JSONObject>{};
    final compilers =
        Map<
          Map<String, Map<String, SelectionFieldSchema>>,
          _JSONPlanCompiler
        >.identity();
    for (final entry in schema.entries) {
      final compiler = compilers.putIfAbsent(
        entry.value.types,
        () => _JSONPlanCompiler(entry.value.types),
      );
      final fields = <String, _JSONField>{};
      for (final param in entry.value.params) {
        final field = compiler.field(
          param.type,
          param.typeName,
          param.isList,
          param.nullable,
        );
        if (field != null) fields[param.name] = field;
      }
      if (fields.isNotEmpty) plans[entry.key] = _JSONObject(fields);
    }
    for (final compiler in compilers.values) compiler.finish();
    _plans = plans;
  }

  String encode(
    String api,
    Map<String, Object?>? params, {
    Object? sequence,
    bool subscription = false,
  }) {
    final writer = _JSONWriter();
    writer.buffer.write(subscription ? '{"\$sub":' : '{"\$api":');
    writer.write(api, 1);
    if (sequence != null) {
      writer.buffer.write(',"\$id":');
      writer.write(sequence, 1);
    }
    final fields = _plans[api]?.fields;
    try {
      for (final entry in (params ?? const <String, Object?>{}).entries) {
        if (entry.key == r'$api' || entry.key == r'$id' || entry.key == r'$sub')
          continue;
        writer.buffer.write(',');
        writer.writeString(entry.key);
        writer.buffer.write(':');
        writer.parameter(entry.value, fields?[entry.key], entry.key, 1);
      }
    } on _JSONParameterFailure catch (error) {
      throw LuxoError(
        'ConfigError',
        0,
        'invalid JSON parameter ${error.path}: ${error.reason}',
      );
    }
    writer.buffer.write('}');
    return writer.buffer.toString();
  }
}

final class _JSONField {
  final _JSONType type;
  final bool isList;
  final bool nullable;
  const _JSONField(this.type, this.isList, this.nullable);
}

sealed class _JSONType {
  const _JSONType();
}

final class _JSONInteger extends _JSONType {
  const _JSONInteger();
}

final class _JSONDateTime extends _JSONType {
  const _JSONDateTime();
}

final class _JSONObject extends _JSONType {
  // Only the compiler populates this table; requests never mutate metadata.
  final Map<String, _JSONField> fields;
  _JSONObject(this.fields);
}

final class _JSONMissing extends _JSONType {
  final String name;
  const _JSONMissing(this.name);
}

final class _JSONPlanCompiler {
  final Map<String, Map<String, SelectionFieldSchema>> types;
  final _objects = <String, _JSONType>{};
  final _pending = <(_JSONObject, Map<String, SelectionFieldSchema>)>[];
  _JSONPlanCompiler(this.types);

  _JSONField? field(String? type, String? name, bool list, bool nullable) {
    final _JSONType plan;
    if (type == 'Int' || type == 'Duration') {
      plan = const _JSONInteger();
    } else if (type == 'DateTime') {
      plan = const _JSONDateTime();
    } else if (type == 'Model') {
      plan = object(name ?? '<unknown>');
    } else if ((name ?? type) case final String model
        when types.containsKey(model) || type == null) {
      plan = object(model);
    } else if (type == null) {
      plan = const _JSONMissing('field type metadata');
    } else {
      return null;
    }
    return _JSONField(plan, list, nullable);
  }

  _JSONType object(String name) {
    final cached = _objects[name];
    if (cached != null) return cached;
    final definition = types[name];
    if (definition == null) return _JSONMissing(name);
    final plan = _JSONObject({});
    _objects[name] = plan;
    _pending.add((plan, definition));
    return plan;
  }

  void finish() {
    // Iteration bounds schema recursion without a compiler stack limit.
    for (var i = 0; i < _pending.length; i++) {
      final (plan, definition) = _pending[i];
      for (final entry in definition.entries) {
        final metadata = entry.value;
        final item = field(
          metadata.type,
          metadata.typeName,
          metadata.isList,
          metadata.nullable,
        );
        if (item != null) plan.fields[entry.key] = item;
      }
    }
  }
}
