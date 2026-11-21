@TestOn('vm')
library;

import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:test/test.dart';
import 'package:luxo_client/src/codec.dart';
import 'package:luxo_client/src/int64.dart';
import 'package:luxo_client/src/json.dart';

void main() {
  group('DateTime shared text boundaries', () {
    final fixture = jsonDecode(
      File('../../pkg/lux/codec/testdata/protocol-v1.json').readAsStringSync(),
    ) as Map<String, Object?>;
    for (final entry
        in (fixture['dateTimeBoundaryCases']! as List<Object?>)
            .cast<Map<String, Object?>>()) {
      test(entry['name']! as String, () {
        final encoder = LuxoEncoder();
        final input = entry['input']! as String;
        if (entry['canonical'] == null) {
          expect(() => encoder.writeDateTime(input), throwsFormatException);
          expect(encoder.bytes(), isEmpty);
          expect(() => luxoDateTimeFromJSON(input), throwsFormatException);
        } else {
          encoder.writeDateTime(input);
          expect(
            LuxoDecoder(encoder.bytes()).readDateTimeChecked(),
            entry['canonical'],
          );
          expect(luxoDateTimeFromJSON(input), input);
        }
      });
    }
  });
  group('canonical UUID shared fixture', () {
    final fixture = jsonDecode(
      File('../../pkg/lux/codec/testdata/protocol-v1.json').readAsStringSync(),
    ) as Map<String, Object?>;
    final cases = fixture['uuidCanonicalCases']! as List<Object?>;
    test('contains all 11 shared cases', () => expect(cases, hasLength(11)));
    for (final item in cases.cast<Map<String, Object?>>()) {
      test(item['input']! as String, () {
        final encoder = LuxoEncoder();
        final input = item['input']! as String;
        final canonical = item['canonical'] as String?;
        if (canonical == null) {
          expect(() => encoder.writeUuid(input), throwsFormatException);
          expect(encoder.bytes(), isEmpty);
        } else {
          encoder.writeUuid(input);
          expect(encoder.bytes(), hasLength(16));
          expect(LuxoDecoder(encoder.bytes()).readUuid(), canonical);
        }
      });
    }
  });
  group('shared complete Unicode fixture', () {
    final fixture = jsonDecode(
      File('../../pkg/lux/codec/testdata/protocol-v1.json').readAsStringSync(),
    ) as Map<String, Object?>;
    final cases = fixture['jsonUnicodeCases']! as List<Object?>;
    test('contains all 23 shared cases', () => expect(cases, hasLength(23)));
    for (final entry in cases.cast<Map<String, Object?>>()) {
      test(entry['name']! as String, () {
        final raw = entry['raw']! as String;
        if (entry['valid'] == false) {
          expect(() => decodeLuxoJSON(raw), throwsFormatException);
        } else {
          final value = decodeLuxoJSON(raw);
          expect(
            encodeLuxoJSON(decodeLuxoJSON(encodeLuxoJSON(value))),
            encodeLuxoJSON(value),
          );
        }
      });
    }
  });
  group('canonical int64 fixture', () {
    final fixture = jsonDecode(
      File('../../pkg/lux/codec/testdata/protocol-v1.json').readAsStringSync(),
    ) as Map<String, Object?>;
    for (final entry in fixture['int64Cases']! as List<Object?>) {
      final item = entry! as Map<String, Object?>;
      final value = int.parse(item['value']! as String);
      final wire = _fixtureBytes(item['wire']! as String);
      test('exact wire and value $value', () {
        final encoder = LuxoEncoder()..writeSvarint(LuxoInt64.parse('$value'));
        expect(encoder.bytes(), wire);
        final decoder = LuxoDecoder(wire);
        expect(decoder.readInt(), value);
        expect(decoder.error, isNull);
        final containerWire = Uint8List.fromList([1, ...wire]);
        final nullable = LuxoDecoder(containerWire);
        expect(nullable.readIntPtr(), value);
        expect(nullable.error, isNull);
        final array = LuxoDecoder(containerWire);
        expect(array.readIntArray(), [value]);
        expect(array.error, isNull);
        final column = ColumnarDecoder(
          Uint8List.fromList([1, 0, 1, ...wire, 0]),
        );
        expect(column.nextColumn(), isTrue);
        expect(column.readColumnInt(), [value]);
        expect(column.error, isNull);
        final nullableColumn = ColumnarDecoder(
          Uint8List.fromList([1, 0, 1, ...containerWire, 0]),
        );
        expect(nullableColumn.nextColumn(), isTrue);
        expect(nullableColumn.readColumnIntPtr(), [value]);
        expect(nullableColumn.error, isNull);
        expect(jsonDecode('{"value":$value}'), {'value': value});
      });
    }
    for (final wire in fixture['invalidInt64']! as List<Object?>) {
      test('reject invalid int64 $wire', () {
        final bytes = _fixtureBytes(wire! as String);
        final decoder = LuxoDecoder(bytes)..readInt();
        expect(decoder.error, isNotNull);
        final column = ColumnarDecoder(Uint8List.fromList([1, 0, 1, ...bytes]));
        column.nextColumn();
        column.readColumnInt();
        expect(column.error, isNotNull);
      });
    }
  });
}

Uint8List _fixtureBytes(String hex) => Uint8List.fromList([
  for (var i = 0; i < hex.length; i += 2)
    int.parse(hex.substring(i, i + 2), radix: 16),
]);
