import 'dart:convert';

import 'codec.dart' show dateTimePartsFromString;

import 'error.dart';
import 'int64.dart';
import 'int64_native.dart'
    if (dart.library.js_interop) 'int64_web.dart'
    as platform;
import 'int64_validation.dart';
import 'schema.dart';
import 'unicode.dart';

part 'json_parameters.dart';
part 'json_parameter_writer.dart';
part 'json_reader.dart';
part 'json_writer.dart';

/// Check the typed DateTime boundary without a JSON round trip or date rounding.
String luxoDateTimeFromJSON(Object? value) {
  final text = value as String;
  dateTimePartsFromString(text);
  return text;
}

List<String> luxoDateTimeListFromJSON(Object? value) {
  return (value as List).map(luxoDateTimeFromJSON).toList();
}

/// Generated values expose their JSON shape explicitly, without reflection.
abstract interface class LuxoJSONEncodable {
  Map<String, Object?> toJson();
}

/// Generated request inputs preserve LuxoInt64 and nested typed inputs directly.
/// The public toJson() representation remains independent of transport encoding.
abstract interface class LuxoJSONParameters implements LuxoJSONEncodable {
  Map<String, Object?> toLuxoParameters();
}

/// Preserves a validated number token without converting it to a host Float.
final class LuxoJSONNumber {
  final String literal;
  const LuxoJSONNumber._(this.literal);

  factory LuxoJSONNumber.parse(String literal) {
    final reader = _JSONReader(literal);
    final value = reader._number(raw: true);
    if (reader.offset != literal.length) reader._invalid();
    return value as LuxoJSONNumber;
  }

  LuxoInt64 toInt64() => luxoInt64FromJSON(this);
  double toDouble() => luxoDoubleFromJSON(this);

  @override
  String toString() => literal;
}

/// Large integer tokens become BigInt before any conversion to double.
/// Both legacy modes preserve fractional/exponent syntax and negative zero.
Object? decodeLuxoJSON(String source, {bool preserveNumberSyntax = false}) =>
    _JSONReader(requireUnicode(source)).read();

String encodeLuxoJSON(Object? value) {
  final writer = _JSONWriter();
  writer.write(value, 0);
  return writer.buffer.toString();
}

LuxoInt64 luxoInt64FromJSON(Object? value) {
  if (value is LuxoJSONNumber) {
    if (value.literal.contains('.') ||
        value.literal.contains('e') ||
        value.literal.contains('E')) {
      throw FormatException('Expected an integer JSON token');
    }
    return LuxoInt64.parse(value.literal);
  }
  if (value is BigInt) return LuxoInt64.fromBigInt(value);
  if (value is int) return LuxoInt64.fromInt(value);
  throw FormatException('Expected an integer JSON token');
}

double luxoDoubleFromJSON(Object? value) {
  final number = switch (value) {
    LuxoJSONNumber() => double.parse(value.literal),
    BigInt() => value.toDouble(),
    num() => value.toDouble(),
    _ => throw FormatException('Expected a numeric JSON token'),
  };
  if (!number.isFinite) throw FormatException('Float is outside finite range');
  if (number == 0 &&
      value is LuxoJSONNumber &&
      _nonzeroSignificand(value.literal)) {
    throw FormatException('JSON Float underflow');
  }
  return number;
}

/// Also protects direct generated decoders and custom Transport implementations.
String luxoStringFromJSON(Object? value) => requireUnicode(value as String);

/// Validate eagerly: a lazy cast must not deliver an invalid success value.
List<String> luxoStringListFromJSON(Object? value) {
  final values = value as List;
  return List<String>.generate(
    values.length,
    (index) => luxoStringFromJSON(values[index]),
  );
}

/// Concrete element types avoid generic constructor checks on every element.
List<bool> luxoBooleanListFromJSON(Object? value) {
  final values = value as List;
  return List<bool>.generate(values.length, (index) => values[index] as bool);
}

bool _nonzeroSignificand(String literal) {
  for (var index = 0; index < literal.length; index++) {
    final code = literal.codeUnitAt(index);
    if (code == 101 || code == 69) return false;
    if (code >= 49 && code <= 57) return true;
  }
  return false;
}

/// Checks generic JSON depth/text without converting, copying or mutating numbers.
Object? normalizeLuxoJSON(Object? value) => _normalizeJSON(value, 0);

Object? _normalizeJSON(Object? value, int depth) {
  if (depth > 128) throw FormatException('JSON nesting exceeds 128 levels');
  if (value is LuxoJSONNumber) return value;
  if (value is String) requireUnicode(value);
  if (value is List) {
    for (final item in value) _normalizeJSON(item, depth + 1);
  }
  if (value is Map<String, Object?>) {
    for (final key in value.keys) {
      requireUnicode(key);
      _normalizeJSON(value[key], depth + 1);
    }
  }
  return value;
}
