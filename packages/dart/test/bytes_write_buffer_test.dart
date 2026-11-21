import 'dart:typed_data';

import 'package:luxo_client/src/codec.dart';
import 'package:test/test.dart';

void main() {
  for (final size in [0, 1, 127, 128, 1022, 1023, 4096, 65536]) {
    test('Bytes preserves length, contents and following fields: $size', () {
      final value = Uint8List.fromList(List.generate(size, (i) => i & 255));
      for (final capacity in [0, 1, 64, 1024]) {
        final encoder = LuxoEncoder(capacity)..writeBytes(value);
        final decoder = LuxoDecoder(encoder.bytes());
        expect(decoder.readBytes(), value);
        expect(decoder.offset, encoder.bytes().length);
        encoder.writeBool(true);
        expect(encoder.bytes().last, 1);
      }
    });
  }
  test('Bytes prefix reservation does not double a sufficient buffer', () {
    final encoder = LuxoEncoder(1024)..writeBytes(Uint8List(1022));
    expect(encoder.bytes().buffer.lengthInBytes, 1024);
  });
  test('Bytes can append a view of the same encoder buffer', () {
    final encoder = LuxoEncoder(8)..writeBytes(Uint8List.fromList([1, 2, 3]));
    final previous = Uint8List.fromList(encoder.bytes());
    encoder.writeBytes(encoder.bytes());
    final decoder = LuxoDecoder(encoder.bytes());
    expect(decoder.readBytes(), [1, 2, 3]);
    expect(decoder.readBytes(), previous);
  });
}
