import 'dart:typed_data';

import 'package:luxo_client/src/codec.dart';
import 'package:test/test.dart';

Uint8List wire(double value) =>
    (ByteData(8)..setFloat64(0, value, Endian.little)).buffer.asUint8List();

void main() {
  for (final value in [double.nan, double.infinity, double.negativeInfinity]) {
    test('Float rejects $value in writers and readers', () {
      expect(() => LuxoEncoder().writeFixed64(value), throwsFormatException);
      final decoder = LuxoDecoder(wire(value));
      decoder.readFloat();
      expect(decoder.error, contains('finite'));
      final array = LuxoDecoder(Uint8List.fromList([1, ...wire(value)]));
      array.readFloatArray();
      expect(array.error, contains('finite'));
      for (final nullable in [false, true]) {
        final column = ColumnarDecoder(
          Uint8List.fromList([1, 0, 1, if (nullable) 1, ...wire(value), 0]),
        );
        column.nextColumn();
        expect(
          nullable ? column.readColumnFloatPtr() : column.readColumnFloat(),
          isEmpty,
        );
        expect(column.error, contains('finite'));
      }
    });
  }
  test('Float preserves finite edge bits', () {
    for (final value in [
      0.0,
      -0.0,
      double.minPositive,
      -double.minPositive,
      double.maxFinite,
      -double.maxFinite,
    ]) {
      final encoder = LuxoEncoder()..writeFixed64(value);
      expect(encoder.bytes(), wire(value));
      final decoder = LuxoDecoder(encoder.bytes());
      expect(wire(decoder.readFloat()), wire(value));
      expect(decoder.error, isNull);
    }
  });
}
