import 'dart:typed_data';

import 'package:fixnum/fixnum.dart';

import 'int64_validation.dart';
import 'wire_reader.dart';
import 'wire_writer.dart';

bool isWireInt64(Object? value) => value is Int64;

/// The Web representation of LuxoInt64; never stores int64 values as double.
extension type const LuxoInt64._(Int64 _value) implements Object {
  LuxoInt64.fromInt(int value) : _value = Int64(requireSafeInteger(value));

  factory LuxoInt64.parse(String value) {
    validateInt64Decimal(value);
    return LuxoInt64._(Int64.parseInt(value));
  }

  factory LuxoInt64.fromBigInt(BigInt value) =>
      LuxoInt64.parse(value.toString());

  static final zero = LuxoInt64._(Int64.ZERO);
  static final minValue = LuxoInt64._(Int64.MIN_VALUE);
  static final maxValue = LuxoInt64._(Int64.MAX_VALUE);

  static final _safeMin = Int64(-maxSafeInteger);
  static final _safeMax = Int64(maxSafeInteger);

  bool get isNegative => _value.isNegative;

  int toInt() {
    if (_value < _safeMin || _value > _safeMax) {
      throw RangeError('Integer outside portable safe-integer range');
    }
    return _value.toInt();
  }

  BigInt toBigInt() => BigInt.parse(_value.toString());

  int compareTo(LuxoInt64 other) => _value.compareTo(other._value);
}

/// Two 32-bit words avoid per-byte BigInt or fixnum arithmetic.
int writeWireInt64(Uint8List buffer, int offset, LuxoInt64 value) {
  final low = value._value.toInt32().toInt().toUnsigned(32);
  final high = value._value.shiftRightUnsigned(32).toInt();
  final sign = (high & 0x80000000) == 0 ? 0 : -1;
  final lo = ((low << 1) ^ sign) & 0xffffffff;
  final hi = (((high << 1) | (low >>> 31)) ^ sign) & 0xffffffff;
  return writeWireWords(buffer, offset, lo, hi);
}

LuxoInt64 readWireInt64(WireReader reader) {
  var lo = 0;
  var hi = 0;
  for (var i = 0; i < 10 && reader.wireOffset < reader.wireBytes.length; i++) {
    final byte = reader.wireBytes[reader.wireOffset++];
    if (i == 9 && byte > 1) break;
    final payload = byte & 127;
    if (i < 4) {
      lo |= payload << (i * 7);
    } else if (i == 4) {
      lo = (lo | (payload << 28)) & 0xffffffff;
      hi = payload >>> 4;
    } else {
      hi |= payload << (i * 7 - 32);
    }
    if (byte < 128) {
      final sign = -(lo & 1);
      final low = (((lo >>> 1) | (hi << 31)) ^ sign) & 0xffffffff;
      final high = ((hi >>> 1) ^ sign).toSigned(32);
      return LuxoInt64._(Int64.fromInts(high, low));
    }
  }
  reader.error ??= 'invalid int64 varint at offset ${reader.wireOffset}';
  return LuxoInt64.zero;
}
