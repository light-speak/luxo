import 'dart:convert';
import 'dart:typed_data';

import 'package:luxo_client/src/codec.dart';
import 'package:luxo_client/src/error_codec.dart';
import 'package:luxo_client/src/int64.dart';
import 'package:test/test.dart';

const _texts = [
  '',
  'ascii',
  '任务 café',
  '\u0000',
  '\uFEFF',
  '\uFEFF\uFEFF任务',
  '\uFFFD',
  '\uFEC0',
  '\u{10FFFF}',
];

const _malformed = <List<int>>[
  [0x80],
  [0xff],
  [0xc0, 0x80],
  [0xc2],
  [0xc2, 0x41],
  [0xe0, 0x80, 0x80],
  [0xed, 0xa0, 0x80],
  [0xef, 0xbb],
  [0xf0, 0x80, 0x80, 0x80],
  [0xf4, 0x90, 0x80, 0x80],
  [0xf5, 0x80, 0x80, 0x80],
  [0xef, 0xbb, 0xbf, 0xff],
];

Uint8List _field(List<int> bytes) =>
    (LuxoEncoder()..writeBytes(Uint8List.fromList(bytes))).bytes();

ColumnarDecoder _column(List<int> bytes, {bool nullable = false}) =>
    ColumnarDecoder(
      Uint8List.fromList([1, 0, 1, if (nullable) 1, ..._field(bytes), 0]),
    )..nextColumn();

void main() {
  test(
    'row, nullable, array and column strings preserve every valid character',
    () {
      for (final text in _texts) {
        final bytes = utf8.encode(text);
        final row = LuxoDecoder(Uint8List.fromList([..._field(bytes), 1]));
        expect(row.readString(), text);
        expect(row.readBool(), isTrue);
        row.checkComplete();
        final nullable = LuxoDecoder(Uint8List.fromList([1, ..._field(bytes)]));
        expect(nullable.readStringPtr(), text);
        nullable.checkComplete();
        final array = LuxoDecoder(Uint8List.fromList([1, ..._field(bytes)]));
        expect(array.readStringArrayChecked(), [text]);
        final column = _column(bytes);
        expect(column.readColumnString(), [text]);
        expect(column.nextColumn(), isFalse);
        expect(column.error, isNull);
        expect(column.wireOffset, column.wireBytes.length);
        final nullableColumn = _column(bytes, nullable: true);
        expect(nullableColumn.readColumnStringPtr(), [text]);
        expect(nullableColumn.nextColumn(), isFalse);
        expect(nullableColumn.error, isNull);
        expect(nullableColumn.wireOffset, nullableColumn.wireBytes.length);
      }
    },
  );

  test('malformed UTF-8 retains the first decoder error and stops reading', () {
    for (final bytes in _malformed) {
      final row = LuxoDecoder(Uint8List.fromList([..._field(bytes), 0, 1]));
      expect(row.readString(), '');
      expect(row.error, contains('UTF-8'));
      final error = row.error;
      final offset = row.offset;
      expect(row.readString(), '');
      expect(row.readBool(), isFalse);
      expect(row.readBytes(), isEmpty);
      expect(row.offset, offset);
      expect(row.error, error);
      expect(row.checkComplete, throwsFormatException);
      final nullable = LuxoDecoder(Uint8List.fromList([1, ..._field(bytes)]));
      nullable.readStringPtr();
      expect(nullable.checkComplete, throwsFormatException);
      for (final optional in [false, true]) {
        final column = _column(bytes, nullable: optional);
        expect(
          optional ? column.readColumnStringPtr() : column.readColumnString(),
          isEmpty,
        );
        expect(column.error, contains('UTF-8'));
        expect(column.nextColumn(), isFalse);
        expect(column.error, isNotNull);
      }
    }
  });

  test(
    'bad string arrays discard partial results and stop before the next item',
    () {
      final bytes = Uint8List.fromList([3, 1, 65, 1, 255, 1, 66]);
      final row = LuxoDecoder(bytes);
      expect(row.readStringArray(), isEmpty);
      expect(row.checkComplete, throwsFormatException);
      var calls = 0;
      final callback = LuxoDecoder(bytes);
      expect(
        callback.readArray(() {
          calls++;
          return callback.readString();
        }),
        isEmpty,
      );
      expect(calls, 2);
      expect(callback.checkComplete, throwsFormatException);

      for (final nullable in [false, true]) {
        final column = ColumnarDecoder(
          Uint8List.fromList([
            3,
            0,
            1,
            if (nullable) 1,
            1,
            65,
            if (nullable) 1,
            1,
            255,
            if (nullable) 1,
            1,
            66,
            0,
          ]),
        )..nextColumn();
        expect(
          nullable ? column.readColumnStringPtr() : column.readColumnString(),
          isEmpty,
        );
        expect(column.error, isNotNull);
      }
    },
  );

  test(
    'all string prefixes reject truncation and arbitrary Bytes remain opaque',
    () {
      for (final text in _texts) {
        final bytes = _field(utf8.encode(text));
        for (var end = 0; end < bytes.length; end++) {
          final row = LuxoDecoder(Uint8List.sublistView(bytes, 0, end));
          expect(row.readStringChecked, throwsFormatException);
        }
      }
      for (final bytes in _malformed) {
        final row = LuxoDecoder(_field(bytes));
        expect(row.readBytesChecked(), bytes);
      }
    },
  );

  test('binary error strings preserve U+FEFF while JSON string content stays intact', () {
    final encoded = LuxoEncoder()
      ..writeFieldInt(1, LuxoInt64.fromInt(400))
      ..writeFieldString(2, '\uFEFFRejected')
      ..writeFieldString(3, '\uFEFF\uFFFD')
      ..writeFieldString(4, '\uFEFFtrace')
      ..writeFieldString(5, '{"value":"\uFEFF\uFFFD"}')
      ..writeFieldString(6, '\uFEFFcause')
      ..writeEnd();
    final error = decodeBinaryError(encoded.bytes(), 400);
    expect(error.error, '\uFEFFRejected');
    expect(error.message, '\uFEFF\uFFFD');
    expect(error.traceId, '\uFEFFtrace');
    expect(error.cause, '\uFEFFcause');
    expect(error.data, {'value': '\uFEFF\uFFFD'});
  });

  test('4352 byte sequences match strict platform decoding without document BOM semantics', () {
    var state = 0x12345678;
    for (var index = 0; index < 4352; index++) {
      final bytes = index < 256 ? [index] : <int>[];
      if (index >= 256) {
        for (var i = 0; i < 2 + index % 4; i++) {
          state = (state * 1664525 + 1013904223) % 4294967296;
          bytes.add(state ~/ 16777216);
        }
      }
      String? expected;
      try {
        // The ASCII prefix makes U+FEFF field content, not a document BOM.
        expected = utf8.decode([65, ...bytes]).substring(1);
      } on FormatException {
        expected = null;
      }
      final row = LuxoDecoder(_field(bytes));
      final actual = row.readString();
      if (expected == null) {
        expect(actual, '');
        expect(row.checkComplete, throwsFormatException);
      } else {
        expect(actual, expected);
        row.checkComplete();
      }
    }
  });
}
