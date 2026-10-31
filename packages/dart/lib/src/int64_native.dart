import 'dart:typed_data';

import 'int64_validation.dart';
import 'wire_reader.dart';

bool isWireInt64(Object? value) => value is int;

/// A signed 64-bit integer. Native storage uses Dart int directly.
///
/// Use [parse] or [fromBigInt] for the full range. [fromInt] and [toInt]
/// enforce the portable safe-integer range, identically on native and Web.
/// This extension type adds no runtime wrapper around its representation.
extension type const LuxoInt64._(int _value) implements Object {
  LuxoInt64.fromInt(int value) : _value = requireSafeInteger(value);

  factory LuxoInt64.parse(String value) {
    validateInt64Decimal(value);
    return LuxoInt64._(int.parse(value));
  }

  factory LuxoInt64.fromBigInt(BigInt value) =>
      LuxoInt64.parse(value.toString());

  static final zero = LuxoInt64._(0);
  static final minValue = LuxoInt64._(-9223372036854775807 - 1);
  static final maxValue = LuxoInt64._(9223372036854775807);

  bool get isNegative => _value.isNegative;

  int toInt() => requireSafeInteger(_value);

  BigInt toBigInt() => BigInt.from(_value);

  int compareTo(LuxoInt64 other) => _value.compareTo(other._value);
}

/// Writes directly into an encoder's reserved ten-byte capacity.
int writeWireInt64(Uint8List buffer, int offset, LuxoInt64 value) {
  var unsigned = (value._value << 1) ^ (value._value >> 63);
  while (unsigned & ~127 != 0) {
    buffer[offset++] = (unsigned & 127) | 128;
    unsigned >>>= 7;
  }
  buffer[offset++] = unsigned;
  return offset;
}

LuxoInt64 readWireInt64(WireReader reader) {
  var value = 0;
  var shift = 0;
  while (reader.wireOffset < reader.wireBytes.length) {
    final byte = reader.wireBytes[reader.wireOffset++];
    value |= (byte & 127) << shift;
    if (byte < 128) {
      if (shift == 63 && byte > 1) break;
      return LuxoInt64._((value >>> 1) ^ -(value & 1));
    }
    shift += 7;
    if (shift >= 64) break;
  }
  reader.error ??= 'invalid int64 varint at offset ${reader.wireOffset}';
  return LuxoInt64.zero;
}
