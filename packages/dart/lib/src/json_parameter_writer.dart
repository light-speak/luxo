part of 'json.dart';

extension _JSONParameterWriting on _JSONWriter {
  void parameter(Object? value, _JSONField? field, String name, int depth) {
    if (field == null) {
      write(value, depth);
      return;
    }
    try {
      if (value == null && field.nullable) {
        buffer.write('null');
      } else if (field.isList) {
        if (value is! List) throw _JSONParameterFailure('must be a list');
        _typedList(value, field.type, depth + 1);
      } else {
        _typedValue(value, field.type, depth);
      }
    } on _JSONParameterFailure catch (error) {
      error.prepend(name);
      rethrow;
    }
  }

  void _typedValue(Object? value, _JSONType type, int depth) {
    if (depth > 128) throw _JSONParameterFailure('nesting exceeds 128 levels');
    switch (type) {
      case _JSONDateTime():
        writeString(luxoDateTimeFromJSON(value));
      case _JSONInteger():
        if (!platform.isWireInt64(value))
          throw _JSONParameterFailure('must be LuxoInt64');
        buffer.write(value);
      case _JSONMissing(:final name):
        throw _JSONParameterFailure('missing schema for $name');
      case _JSONObject():
        if (value is LuxoJSONEncodable) {
          _typedValue(
            value is LuxoJSONParameters
                ? value.toLuxoParameters()
                : value.toJson(),
            type,
            depth + 1,
          );
        } else if (value is Map) {
          _typedMap(value, type.fields, depth + 1);
        } else {
          throw _JSONParameterFailure('must be an object');
        }
    }
  }

  void _typedList(List<Object?> values, _JSONType type, int depth) {
    if (depth > 128) throw _JSONParameterFailure('nesting exceeds 128 levels');
    buffer.write('[');
    for (var i = 0; i < values.length; i++) {
      if (i != 0) buffer.write(',');
      try {
        _typedValue(values[i], type, depth);
      } on _JSONParameterFailure catch (error) {
        error.prepend('[$i]');
        rethrow;
      }
    }
    buffer.write(']');
  }

  void _typedMap(
    Map<Object?, Object?> values,
    Map<String, _JSONField> fields,
    int depth,
  ) {
    if (depth > 128) throw _JSONParameterFailure('nesting exceeds 128 levels');
    buffer.write('{');
    var first = true;
    for (final entry in values.entries) {
      final key = entry.key;
      if (key is! String)
        throw _JSONParameterFailure('object keys must be strings');
      if (!first) buffer.write(',');
      first = false;
      writeString(key);
      buffer.write(':');
      parameter(entry.value, fields[key], key, depth);
    }
    buffer.write('}');
  }
}

final class _JSONParameterFailure implements Exception {
  final String reason;
  String path = '';
  _JSONParameterFailure(this.reason);

  void prepend(String segment) {
    path = path.isEmpty
        ? segment
        : '$segment${path.startsWith('[') ? '' : '.'}$path';
  }
}
