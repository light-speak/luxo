import 'dart:typed_data';

import 'package:luxo_client/luxo_client.dart';
import 'package:test/test.dart';

void main() {
  test('DateTime parameters and responses preserve exact nanoseconds', () {
    for (final (text, seconds, nanos) in [
      ('1970-01-01T00:00:00.000000001Z', 0, 1),
      ('1969-12-31T23:59:59.999999999Z', -1, 999999999),
      ('9999-12-31T23:59:59.999999999Z', 253402300799, 999999999),
    ]) {
      final wire = LuxoEncoder()
        ..writeSvarint(LuxoInt64.fromInt(seconds))
        ..writeVarint(nanos);
      expect(LuxoDecoder(wire.bytes()).readDateTimeChecked(), text);
      final parameter = LuxoEncoder();
      encodeParam(parameter, const ParamSchema(1, 'value', 'DateTime'), text);
      expect(parameter.bytes(), [1, ...wire.bytes()]);
    }
  });

  test('DateTime rejects truncated or out-of-range nanos', () {
    for (final bytes in [
      [0],
      [0, 128],
      [0, 128, 148, 235, 220, 3],
    ]) {
      expect(
        () => LuxoDecoder(Uint8List.fromList(bytes)).readDateTimeChecked(),
        throwsFormatException,
      );
    }
  });
}
