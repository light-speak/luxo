import 'dart:convert';
import 'dart:typed_data';

import '../lib/src/codec.dart';
import '../lib/src/json.dart';
import 'uint64_benchmark.dart' show compareBenchmarks;

void main() {
  for (final size in [16, 4096, 73728]) {
    final value = {'text': 'a' * size};
    final iterations = size > 4096 ? 100 : 1000;
    final encoder = LuxoEncoder(size + 64);
    int run(bool copy) {
      var result = 0;
      for (var i = 0; i < iterations; i++) {
        encoder.reset();
        final bytes = utf8.encode(encodeLuxoJSON(value));
        encoder.writeBytes(copy ? Uint8List.fromList(bytes) : bytes);
        final output = encoder.bytes();
        result += output.length + output.last;
      }
      return result;
    }

    if (run(true) != run(false)) throw StateError('Wire changed');
    compareBenchmarks(
      'JSON-to-Binary $size bytes',
      () => run(true),
      () => run(false),
      iterations: iterations,
    );
  }
  print('Warm JSON field writing only; allocations and network not measured.');
}
