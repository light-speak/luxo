part of 'json.dart';

class _JSONWriter implements LuxoJSONOutput {
  final buffer = StringBuffer();

  @override
  void writeRaw(String source) => buffer.write(source);

  @override
  void writeJSON(Object? value) => write(value, 0);

  void write(Object? value, int depth, {bool typedModels = false}) {
    if (depth > 128) throw FormatException('JSON nesting exceeds 128 levels');
    if (value == null || value is bool) {
      buffer.write(value);
    } else if (value is String) {
      writeString(value);
    } else if (value is BigInt || value is LuxoJSONNumber) {
      buffer.write(value);
    } else if (platform.isWireInt64(value)) {
      buffer.write(value);
    } else if (value is num) {
      if (!value.isFinite) throw FormatException('Non-finite JSON number');
      buffer.write(value);
    } else if (value is List) {
      _list(value, depth + 1, typedModels);
    } else if (value is Map) {
      _map(value, depth + 1, typedModels);
    } else if (typedModels && value is LuxoJSONWritable) {
      value.$writeJSON(this);
    } else if (value is LuxoJSONEncodable) {
      write(value.toJson(), depth, typedModels: typedModels);
    } else {
      throw FormatException('Unsupported JSON value: ${value.runtimeType}');
    }
  }

  void writeString(String value) =>
      buffer.write(jsonEncode(requireUnicode(value)));

  void writeKey(String value) {
    if (_plainASCIIKey(value)) {
      buffer.write('"');
      buffer.write(value);
      buffer.write('\":');
    } else {
      writeString(value);
      buffer.write(':');
    }
  }

  bool _plainASCIIKey(String value) {
    final length = value.length;
    if (length > 64) return false;
    for (var index = 0; index < length; index++) {
      final code = value.codeUnitAt(index);
      if (code < 32 || code > 127 || code == 34 || code == 92) return false;
    }
    return true;
  }

  void _list(List<Object?> values, int depth, bool typedModels) {
    if (depth > 128) throw FormatException('JSON nesting exceeds 128 levels');
    buffer.write('[');
    var first = true;
    for (final value in values) {
      if (!first) buffer.write(',');
      first = false;
      write(value, depth, typedModels: typedModels);
    }
    buffer.write(']');
  }

  void _map(Map<Object?, Object?> values, int depth, bool typedModels) {
    if (depth > 128) throw FormatException('JSON nesting exceeds 128 levels');
    buffer.write('{');
    var first = true;
    for (final entry in values.entries) {
      if (entry.key is! String)
        throw FormatException('JSON keys must be strings');
      if (!first) buffer.write(',');
      first = false;
      writeKey(entry.key as String);
      write(entry.value, depth, typedModels: typedModels);
    }
    buffer.write('}');
  }
}
