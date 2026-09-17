import 'dart:convert';
import 'dart:typed_data';

// Match Go codec limits; these do not narrow signed business Int64 values.
const maxArrayElements = 1_000_000;
const maxColumnarRecords = 10_000_000;
const maxArenaSize = 64 * 1024 * 1024;

/// Shared row/column decoder state; inheritance adds no cursor allocation.
class WireReader {
  final Uint8List wireBytes;
  int wireOffset = 0;
  String? error;

  WireReader(this.wireBytes);

  bool acceptFloat(double value) {
    if (value.isFinite) return true;
    error ??= 'non-finite Float at offset $wireOffset';
    return false;
  }

  int readBoundedUnsigned(int limit, String kind) {
    if (error != null) return 0;
    final value = readUnsigned();
    if (value < 0) return 0;
    if (value <= limit) return value;
    error = '$kind $value exceeds limit $limit';
    return 0;
  }

  bool requireBytes(int length, String kind) {
    if (error != null) return false;
    if (length <= wireBytes.length - wireOffset) return true;
    error = 'truncated $kind at offset $wireOffset';
    return false;
  }

  int readLength(String kind) {
    if (error != null) return -1;
    final length = readUnsigned();
    return requireBytes(length, kind) ? length : -1;
  }

  /// Reads field text, preserving U+FEFF rather than treating it as a BOM.
  @pragma('vm:prefer-inline')
  @pragma('dart2js:tryInline')
  @pragma('wasm:prefer-inline')
  String readString() {
    final length = readLength('string');
    if (length <= 0) return '';
    return _readStringContent(length);
  }

  // Keep exception handling outside the small empty-string path.
  String _readStringContent(int length) {
    final start = wireOffset;
    final end = start + length;
    try {
      final value = const Utf8Decoder().convert(wireBytes, start, end);
      wireOffset = end;
      // The platform decoder drops exactly one leading UTF-8 BOM. Restore it
      // only for fields; HTTP/JSON document decoding keeps its own semantics.
      if (length >= 3 &&
          wireBytes[start] == 0xef &&
          wireBytes[start + 1] == 0xbb &&
          wireBytes[start + 2] == 0xbf) {
        return '\uFEFF$value';
      }
      return value;
    } on FormatException {
      error = 'invalid UTF-8 string at offset $start';
      return '';
    }
  }

  int readArrayCount([int minimumWidth = 1]) {
    final count = readBoundedUnsigned(maxArrayElements, 'array count');
    return requireBytes(count * minimumWidth, 'array') ? count : 0;
  }

  bool readCanonicalMarker(String kind) {
    if (error != null) return false;
    if (wireOffset >= wireBytes.length) {
      error = 'truncated $kind marker at offset $wireOffset';
      return false;
    }
    final offset = wireOffset;
    final marker = wireBytes[wireOffset++];
    if (marker == 0) return false;
    if (marker == 1) return true;
    error = 'invalid $kind marker at offset $offset';
    return false;
  }

  /// Metadata must be an exactly representable, non-negative Dart int.
  int readUnsigned() {
    if (wireOffset < wireBytes.length) {
      final first = wireBytes[wireOffset++];
      if (first < 128) return first;
      var value = first & 127;
      var factor = 128;
      for (var i = 1; i < 8 && wireOffset < wireBytes.length; i++) {
        final byte = wireBytes[wireOffset++];
        if (i == 7 && byte > 15) {
          error ??= 'metadata varint overflow at offset $wireOffset';
          return -1;
        }
        value += (byte & 127) * factor;
        if (byte < 128) return value;
        factor *= 128;
      }
    }
    error ??= 'truncated varint at offset $wireOffset';
    return -1;
  }
}
