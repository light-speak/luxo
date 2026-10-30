import 'dart:typed_data';

import '../lib/src/codec.dart';

void main() {
  for (final size in [0, 1, 128, 1022, 4096, 65536]) {
    final bytes = Uint8List(size);
    final reusable = LuxoEncoder(64);
    final iterations = size > 4096 ? 10000 : 100000;
    for (final reuse in [false, true]) {
      var checksum = 0;
      for (var round = 0; round < 4; round++) {
        final clock = Stopwatch()..start();
        for (var i = 0; i < iterations; i++) {
          final encoder = reuse ? reusable : LuxoEncoder(64);
          encoder.reset();
          encoder.writeBytes(bytes);
          checksum += encoder.bytes().length;
        }
        clock.stop();
        if (round > 0) {
          print(
            '$size/$reuse ${clock.elapsedMicroseconds * 1000 / iterations} ns $checksum',
          );
        }
      }
    }
  }
}
