import '../lib/src/codec.dart';
import '../lib/src/int64.dart';

const _iterations = 2000000;

void main() {
  final values = [
    '0',
    '1',
    '-1',
    '64',
    '-65',
    '2147483647',
    '-2147483648',
    '9007199254740993',
    '-9007199254740993',
    '9223372036854775807',
    '-9223372036854775808',
  ].map(LuxoInt64.parse).toList();
  final wires = [
    for (final v in values) (LuxoEncoder()..writeSvarint(v)).bytes(),
  ];
  final rows = [
    for (final v in values)
      (LuxoEncoder()
            ..writeFieldInt(1, v)
            ..writeFieldString(2, 'hello')
            ..writeEnd())
          .bytes(),
  ];
  final encoder = LuxoEncoder();

  _measure('int64 encode', () {
    var sum = 0;
    for (var i = 0; i < _iterations; i++) {
      encoder.reset();
      encoder.writeSvarint(values[i % values.length]);
      final bytes = encoder.bytes();
      sum ^= bytes.last + bytes.length;
    }
    return sum;
  });
  _measure('int64 decode', () {
    var sum = 0;
    for (var i = 0; i < _iterations; i++) {
      final decoder = LuxoDecoder(wires[i % wires.length]);
      if (decoder.readInt().isNegative) sum ^= 1;
      sum ^= decoder.offset;
    }
    return sum;
  });
  _measure('row encode', () {
    var sum = 0;
    for (var i = 0; i < _iterations; i++) {
      encoder.reset();
      encoder.writeFieldInt(1, values[i % values.length]);
      encoder.writeFieldString(2, 'hello');
      encoder.writeEnd();
      final bytes = encoder.bytes();
      sum ^= bytes[1] + bytes.length;
    }
    return sum;
  });
  _measure('row decode', () {
    var sum = 0;
    for (var i = 0; i < _iterations; i++) {
      final decoder = LuxoDecoder(rows[i % rows.length]);
      while (decoder.nextField()) {
        if (decoder.fieldID == 1) {
          if (decoder.readInt().isNegative) sum ^= 1;
        } else {
          sum ^= decoder.readString().length;
        }
      }
      sum ^= decoder.offset;
    }
    return sum;
  });
}

void _measure(String name, int Function() operation) {
  var checksum = operation();
  final samples = <double>[];
  for (var i = 0; i < 7; i++) {
    final watch = Stopwatch()..start();
    checksum += operation();
    watch.stop();
    samples.add(watch.elapsedTicks * 1e9 / watch.frequency / _iterations);
  }
  samples.sort();
  print(
    '$name: ${samples[3].toStringAsFixed(2)} ns/op '
    '[${samples.first.toStringAsFixed(2)}, ${samples.last.toStringAsFixed(2)}], '
    'checksum=$checksum',
  );
}
