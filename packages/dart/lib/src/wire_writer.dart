import 'dart:typed_data';

/// Shared signed/unsigned LEB128 emission, with no per-byte wide-integer work.
// Preserve caller optimizations without disabling bounds or type checks.
@pragma('dart2js:prefer-inline')
@pragma('wasm:prefer-inline')
int writeWireWords(Uint8List buffer, int offset, int lo, int hi) {
  while (hi != 0 || lo >= 128) {
    buffer[offset++] = (lo & 127) | 128;
    lo = ((lo >>> 7) | (hi << 25)) & 0xffffffff;
    hi >>>= 7;
  }
  buffer[offset++] = lo;
  return offset;
}
