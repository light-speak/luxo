import 'dart:convert';
import 'dart:typed_data';

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

/// Generated models write their known fields without an intermediate projection.
abstract interface class LuxoJSONWritable implements LuxoJSONEncodable {
  void $writeJSON(LuxoJSONOutput output);
}

/// Generated sums retain their owner and nesting budget during typed writing.
abstract interface class LuxoSealedJSONWritable implements LuxoJSONWritable {
  String get $sealedType;
  void $writeSealedJSON(LuxoJSONOutput output, int depth);
}

/// Narrow output capability used by generated JSON writers.
abstract interface class LuxoJSONOutput {
  void writeRaw(String source);
  void writeJSON(Object? value);
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
Object? decodeLuxoJSON(
  String source, {
  bool preserveNumberSyntax = false,
  JSONResponseSchema? response,
}) => _JSONReader(requireUnicode(source)).read(response: response);

/// Transport metadata does not consume the independently bounded data payload.
Map<String, Object?> decodeLuxoJSONEnvelope(
  String source, {
  JSONResponseSchema? response,
  bool errorResponse = false,
}) =>
    _JSONReader(requireUnicode(source))
        .readEnvelope(response: response, errorResponse: errorResponse);

/// Binary fields contain JSON values, not documents with optional encoding markers.
Object? decodeLuxoJSONBytes(List<int> bytes) {
  if (bytes.length >= 3 &&
      bytes[0] == 0xef &&
      bytes[1] == 0xbb &&
      bytes[2] == 0xbf) {
    throw const FormatException('Binary JSON must not start with a BOM');
  }
  return decodeLuxoJSON(utf8.decode(bytes));
}

String encodeLuxoJSON(Object? value, {bool opaque = false}) {
  final writer = _JSONWriter();
  writer.write(value, 0, typedModels: !opaque);
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

/// Internal transport decoder: a null resolution discards only validated source.
final class JSONSocketDecoder {
  final JSONResponseSchema? Function(Map<String, Object?>) resolve;
  final _frames = <int>[];
  List<int>? _overflow;
  int _depth = 0;

  JSONSocketDecoder(this.resolve);

  Map<String, Object?> decode(String source) {
    try {
      return _JSONReader(requireUnicode(source)).readSocketEnvelope(this);
    } finally {
      _depth = 0;
      _overflow = null;
    }
  }

  int get _close =>
      _depth <= 128 ? _frames[_depth - 1] : _overflow![_depth - 129];

  void _push(int close) {
    final frames = _depth < 128 ? _frames : (_overflow ??= []);
    final index = _depth < 128 ? _depth : _depth - 128;
    if (index == frames.length) {
      frames.add(close);
    } else {
      frames[index] = close;
    }
    _depth++;
  }
}

/// Internal signal emitted only after the complete socket message is valid JSON.
final class JSONModelDepthError extends FormatException {
  final Map<String, Object?> envelope;
  JSONModelDepthError(this.envelope) : super('Model nesting exceeds 64 levels');
}

final class _JSONModelDepthFailure extends FormatException {
  _JSONModelDepthFailure(String source, int offset)
    : super('Model nesting exceeds 64 levels', source, offset);
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

/// A Schema list does not consume any of its JSON elements' container budget.
List<Object?> normalizeLuxoJSONList(List<Object?> values) {
  for (final value in values) _normalizeJSON(value, 0);
  return values;
}

Object? _normalizeJSON(Object? value, int depth) {
  if (value == null || value is bool) return value;
  if (value is num) {
    if (!value.isFinite) throw FormatException('Non-finite JSON number');
    return value;
  }
  if (value is LuxoJSONNumber) return value;
  if (value is String) return requireUnicode(value);
  if (value is List) {
    if (depth >= 128) throw FormatException('JSON nesting exceeds 128 levels');
    for (final item in value) _normalizeJSON(item, depth + 1);
    return value;
  }
  if (value is Map<String, Object?>) {
    _normalizeJSONMap<String>(value, depth);
  } else if (value is Map) {
    _normalizeJSONMap<Object?>(value, depth);
  }
  return value;
}

@pragma('vm:prefer-inline')
void _normalizeJSONMap<K>(Map<K, Object?> value, int depth) {
  if (depth >= 128) throw FormatException('JSON nesting exceeds 128 levels');
  for (final key in value.keys) {
    if (key is! String) throw FormatException('JSON keys must be strings');
    requireUnicode(key);
    _normalizeJSON(value[key], depth + 1);
  }
}
