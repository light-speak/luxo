import 'dart:math';
import 'dart:typed_data';

import 'package:luxo_client/luxo_client.dart';
import 'package:test/test.dart';

void main() {
  test('signed boundaries match the independent zigzag reference', () {
    for (final decimal in [
      '0',
      '1',
      '-1',
      '2147483647',
      '-2147483648',
      '4294967296',
      '4503599627370495',
      '4503599627370496',
      '-4503599627370496',
      '-4503599627370497',
      '9007199254740991',
      '9007199254740992',
      '9007199254740993',
      '-9007199254740993',
      '9223372036854775807',
      '-9223372036854775808',
    ]) {
      final value = LuxoInt64.parse(decimal);
      final wire = referenceWire(BigInt.parse(decimal));
      expect((LuxoEncoder()..writeSvarint(value)).bytes(), wire);
      expect(LuxoDecoder(Uint8List.fromList(wire)).readIntChecked(), value);
    }
  });

  test('int64 wire matches BigInt across native and Web', () {
    final random = Random(64);
    final encoder = LuxoEncoder(1);
    for (var i = 0; i < 10000; i++) {
      var reference = BigInt.zero;
      for (var byte = 0; byte < 8; byte++) {
        reference = (reference << 8) | BigInt.from(random.nextInt(256));
      }
      if (reference >= BigInt.one << 63) reference -= BigInt.one << 64;
      final value = LuxoInt64.fromBigInt(reference);
      encoder.reset();
      encoder.writeSvarint(value);
      expect(encoder.bytes(), referenceWire(reference));
      final decoder = LuxoDecoder(encoder.bytes());
      expect(decoder.readInt().toBigInt(), reference);
      expect(decoder.error, isNull);
      expect(decoder.offset, encoder.bytes().length);
    }
  });

  test('full range survives arrays, nullable values and columns', () {
    final values = [
      LuxoInt64.minValue,
      LuxoInt64.maxValue,
      LuxoInt64.zero,
      LuxoInt64.parse('9007199254740993'),
      LuxoInt64.parse('-9007199254740993'),
    ];
    final array = LuxoEncoder()..writeFieldIntArray(1, values);
    final decoder = LuxoDecoder(array.bytes());
    expect(decoder.nextField(), isTrue);
    expect(decoder.readIntArray(), values);
    final column = LuxoEncoder()
      ..writeVarint(values.length)
      ..writeVarint(0);
    column.writeVarint(1);
    for (final value in values) column.writeSvarint(value);
    final columns = ColumnarDecoder(column.bytes());
    expect(columns.nextColumn(), isTrue);
    expect(columns.readColumnInt(), values);
    expect(columns.error, isNull);
    for (final value in values) {
      final nullable = LuxoEncoder()
        ..writePresent()
        ..writeSvarint(value);
      expect(LuxoDecoder(nullable.bytes()).readIntPtr(), value);
      final nullableColumn = ColumnarDecoder(
        Uint8List.fromList([2, 0, 1, ...nullable.bytes(), 0]),
      );
      expect(nullableColumn.nextColumn(), isTrue);
      expect(nullableColumn.readColumnIntPtr(), [value, null]);
      expect(nullableColumn.error, isNull);
    }
  });

  test('invalid signed varints never become successful values', () {
    final malformed = <List<int>>[
      [],
      [128],
      [...List.filled(9, 128), 2],
      [...List.filled(9, 255), 2],
      List.filled(10, 128),
    ];
    for (final bytes in malformed) {
      final decoder = LuxoDecoder(Uint8List.fromList(bytes));
      decoder.readInt();
      expect(decoder.error, isNotNull);
      final column = ColumnarDecoder(Uint8List.fromList([1, 0, 1, ...bytes]));
      column.nextColumn();
      column.readColumnInt();
      expect(column.error, isNotNull);
    }
  });

  test('metadata varints do not truncate to 32 bits on Web', () {
    for (final value in [
      2147483647,
      4294967295,
      4294967296,
      9007199254740991,
    ]) {
      final encoder = LuxoEncoder()..writeVarint(value);
      final decoder = LuxoDecoder(encoder.bytes());
      expect(decoder.nextField(), isTrue);
      expect(decoder.fieldID, value);
      expect(decoder.error, isNull);
    }
    for (final value in [-1, 9007199254740992]) {
      expect(() => LuxoEncoder().writeVarint(value), throwsRangeError);
    }
    final tooLarge = LuxoDecoder(
      Uint8List.fromList([128, 128, 128, 128, 128, 128, 128, 16]),
    );
    expect(tooLarge.nextField(), isFalse);
    expect(tooLarge.error, isNotNull);
    final truncated = LuxoDecoder(Uint8List.fromList([128]));
    expect(truncated.nextField(), isFalse);
    expect(truncated.error, isNotNull);
  });

  test('zero initial capacity grows without hanging', () {
    final encoder = LuxoEncoder(0)..writeSvarint(LuxoInt64.maxValue);
    expect(encoder.bytes(), referenceWire(LuxoInt64.maxValue.toBigInt()));
  });

  test(
    'complete arrays, huge columns and invalid timestamps reject bad data',
    () {
      final encoder = LuxoEncoder()
        ..writeVarint(1)
        ..writeSvarint(LuxoInt64.minValue);
      expect(LuxoDecoder(encoder.bytes()).readIntArrayChecked(), [
        LuxoInt64.minValue,
      ]);
      encoder.writeEnd();
      expect(
        () => LuxoDecoder(encoder.bytes()).readIntArrayChecked(),
        throwsFormatException,
      );
      final huge = LuxoEncoder()
        ..writeVarint(9007199254740991)
        ..writeVarint(0)
        ..writeVarint(1);
      final column = ColumnarDecoder(huge.bytes())..nextColumn();
      expect(column.readColumnInt(), isEmpty);
      expect(column.error, isNotNull);
      final nullable = ColumnarDecoder(huge.bytes())..nextColumn();
      expect(nullable.readColumnIntPtr(), isEmpty);
      expect(nullable.error, isNotNull);
      for (final seconds in [-8640000000001, 8640000000001]) {
        expect(() => dateTimeStringFromUnixSeconds(seconds), throwsRangeError);
      }
    },
  );
}

List<int> referenceWire(BigInt value) {
  var unsigned = (value << 1) ^ (value >> 63);
  final bytes = <int>[];
  while (unsigned >= BigInt.from(128)) {
    bytes.add((unsigned & BigInt.from(127)).toInt() | 128);
    unsigned >>= 7;
  }
  return bytes..add(unsigned.toInt());
}
