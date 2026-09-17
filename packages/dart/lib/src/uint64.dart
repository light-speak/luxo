import 'dart:typed_data';

import 'error.dart';
import 'int64_validation.dart';
import 'wire_writer.dart';

final _maximum = (BigInt.one << 64) - BigInt.one;
final _safeMaximum = BigInt.from(maxSafeInteger);
final _wordMask = BigInt.from(0xffffffff);

/// Canonical IDs use int in the portable safe range, BigInt only above it.
/// Input comes from decodeLuxoJSON with preserveNumberSyntax enabled.
Object? uint64FromJSON(Object? value) {
  if (value is int) return value >= 0 && value <= maxSafeInteger ? value : null;
  if (value is! BigInt || value.isNegative || value > _maximum) return null;
  return value <= _safeMaximum ? value.toInt() : value;
}

/// A typed small counter avoids repeated Object-to-int checks on JS/Wasm.
/// This state is allocated once per transport, never once per request.
final class RequestIDs {
  int _small = 0;
  BigInt? _wide;

  RequestIDs([Object initial = 0]) {
    final value = uint64FromJSON(initial);
    if (value == null) throw RangeError('Invalid initial request ID');
    if (value is int)
      _small = value;
    else
      _wide = value as BigInt;
  }

  Object next() {
    if (_wide == null && _small < maxSafeInteger) return ++_small;
    return _advanceWide();
  }

  BigInt _advanceWide() {
    final value = _wide ?? BigInt.from(_small);
    if (value == _maximum) {
      throw LuxoError(
        'RequestIDExhausted',
        0,
        'WebSocket request IDs exhausted',
      );
    }
    return _wide = value + BigInt.one;
  }
}

Object uint64FromWords(int low, int high) => high <= 0x1fffff
    ? high * 4294967296 + low
    : (BigInt.from(high) << 32) | BigInt.from(low);

/// The encoder reserves ten bytes; validation happens before buffer mutation.
int writeWireUint64(Uint8List buffer, int offset, BigInt value) {
  if (value.isNegative || value > _maximum) throw RangeError('Invalid uint64');
  return writeWireWords(
    buffer,
    offset,
    (value & _wordMask).toInt(),
    (value >> 32).toInt(),
  );
}
