part of 'json.dart';

extension _JSONParameterWriting on _JSONWriter {
  void parameter(Object? value, _JSONField? field, String name, int depth) {
    if (field == null) {
      write(value, 0);
      return;
    }
    try {
      if (value == null && field.nullable) {
        buffer.write('null');
      } else if (field.isList) {
        if (value is! List) throw _JSONParameterFailure('must be a list');
        _typedList(value, field.type, depth);
      } else {
        _typedValue(value, field.type, depth);
      }
    } on _JSONParameterFailure catch (error) {
      error.prepend(name);
      rethrow;
    }
  }

  void _typedValue(Object? value, _JSONType type, int depth) {
    switch (type) {
      case _JSONGeneric():
        write(value, 0);
      case _JSONDateTime():
        if (value is! String)
          throw _JSONParameterFailure('must be an RFC3339 string');
        try {
          dateTimePartsFromString(value);
        } on FormatException catch (error) {
          throw _JSONParameterFailure(error.message);
        }
        writeString(value);
      case _JSONInteger():
        if (!platform.isWireInt64(value))
          throw _JSONParameterFailure('must be LuxoInt64');
        buffer.write(value);
      case _JSONBytes():
        _typedBytes(value);
      case _JSONMissing(:final name):
        throw _JSONParameterFailure('missing schema for $name');
      case _JSONSealed():
        _typedSealed(value, type, depth);
      case _JSONObject():
        if (depth >= 64)
          throw _JSONParameterFailure('model nesting exceeds 64 levels');
        if (value is LuxoJSONEncodable) {
          _typedMap(
            value is LuxoJSONParameters
                ? value.toLuxoParameters()
                : value.toJson(),
            type.fields,
            depth + 1,
            strict: type.strict,
          );
        } else if (value is Map) {
          _typedMap(value, type.fields, depth + 1, strict: type.strict);
        } else {
          throw _JSONParameterFailure('must be an object');
        }
    }
  }

  void _typedSealed(Object? value, _JSONSealed type, int depth) {
    if (depth >= 64)
      throw _JSONParameterFailure('model nesting exceeds 64 levels');
    if (value is LuxoSealedJSONWritable) {
      if (value.$sealedType != type.owner)
        throw _JSONParameterFailure('wrong sealed owner');
      value.$writeSealedJSON(this, depth);
      return;
    }
    if (value is! Map || value.length != 1)
      throw _JSONParameterFailure('sealed value must have exactly one case');
    final entry = value.entries.single;
    final field = type.cases[entry.key];
    if (field == null)
      throw _JSONParameterFailure('unknown sealed case ${entry.key}');
    buffer.write('{');
    writeKey(entry.key as String);
    parameter(entry.value, field, entry.key as String, depth + 1);
    buffer.write('}');
  }

  void _typedBytes(Object? value) {
    if (value is Uint8List) {
      buffer.write('"');
      buffer.write(base64Encode(value));
      buffer.write('"');
    } else if (value is String) {
      // Existing JSON projections already contain their Base64 wire text.
      writeString(value);
    } else {
      throw _JSONParameterFailure('must be Uint8List or a Base64 string');
    }
  }

  void _typedList(List<Object?> values, _JSONType type, int depth) {
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
    int depth, {
    bool strict = false,
  }) {
    buffer.write('{');
    var first = true;
    for (final entry in values.entries) {
      final key = entry.key;
      if (key is! String)
        throw _JSONParameterFailure('object keys must be strings');
      if (strict && !fields.containsKey(key))
        throw _JSONParameterFailure('unknown sealed payload field $key');
      if (!first) buffer.write(',');
      first = false;
      writeKey(key);
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
