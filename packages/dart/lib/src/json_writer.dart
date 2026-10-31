part of 'json.dart';

class _JSONWriter {
  final buffer = StringBuffer();

  void write(Object? value, int depth) {
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
      _list(value, depth + 1);
    } else if (value is Map) {
      _map(value, depth + 1);
    } else if (value is LuxoJSONEncodable) {
      write(value.toJson(), depth + 1);
    } else {
      throw FormatException('Unsupported JSON value: ${value.runtimeType}');
    }
  }

  void writeString(String value) =>
      buffer.write(jsonEncode(requireUnicode(value)));

  void _list(List<Object?> values, int depth) {
    if (depth > 128) throw FormatException('JSON nesting exceeds 128 levels');
    buffer.write('[');
    var first = true;
    for (final value in values) {
      if (!first) buffer.write(',');
      first = false;
      write(value, depth);
    }
    buffer.write(']');
  }

  void _map(Map<Object?, Object?> values, int depth) {
    if (depth > 128) throw FormatException('JSON nesting exceeds 128 levels');
    buffer.write('{');
    var first = true;
    for (final entry in values.entries) {
      if (entry.key is! String)
        throw FormatException('JSON keys must be strings');
      if (!first) buffer.write(',');
      first = false;
      writeString(entry.key as String);
      buffer.write(':');
      write(entry.value, depth);
    }
    buffer.write('}');
  }
}
