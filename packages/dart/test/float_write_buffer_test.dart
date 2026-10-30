import 'package:luxo_client/src/codec.dart';
import 'package:test/test.dart';

void main() {
  final vectors = <double, List<int>>{
    1.5: [0, 0, 0, 0, 0, 0, 248, 63],
    -0.0: [0, 0, 0, 0, 0, 0, 0, 128],
    double.minPositive: [1, 0, 0, 0, 0, 0, 0, 0],
    double.maxFinite: [255, 255, 255, 255, 255, 255, 239, 127],
  };
  for (final entry in vectors.entries) {
    test('Float ${entry.key} writes exact unaligned bytes after growth', () {
      final encoder = LuxoEncoder(1);
      encoder.writeBool(true);
      encoder.writeBool(false);
      encoder.writeBool(true);
      encoder.writeFixed64(entry.key);
      expect(encoder.bytes(), [1, 0, 1, ...entry.value]);
      encoder.reset();
      encoder.writeBool(false);
      encoder.writeFixed64(entry.key);
      expect(encoder.bytes(), [0, ...entry.value]);
    });
  }
  test('invalid Float does not alter prior output or prevent later writes', () {
    final encoder = LuxoEncoder(1)..writeBool(true);
    for (final value in [
      double.nan,
      double.infinity,
      double.negativeInfinity,
    ]) {
      expect(() => encoder.writeFixed64(value), throwsFormatException);
      expect(encoder.bytes(), [1]);
    }
    encoder.writeFixed64(1.5);
    expect(encoder.bytes(), [1, ...vectors[1.5]!]);
  });
}
