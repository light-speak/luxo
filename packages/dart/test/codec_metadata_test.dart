import 'dart:typed_data';

import 'package:luxo_client/src/codec.dart';
import 'package:luxo_client/src/int64.dart';
import 'package:test/test.dart';

final _rowReaders = <String, List<Object?> Function(LuxoDecoder)>{
  'Int': (d) => d.readIntArray(),
  'Float': (d) => d.readFloatArray(),
  'String': (d) => d.readStringArray(),
  'Bool': (d) => d.readBoolArray(),
  'UUID': (d) => d.readUuidArray(),
  'Bytes': (d) => d.readBytesArray(),
  'DateTime': (d) => d.readDateTimeArray(),
};
final _columnReaders = <String, List<Object?> Function(ColumnarDecoder)>{
  'Int': (d) => d.readColumnInt(),
  'Float': (d) => d.readColumnFloat(),
  'String': (d) => d.readColumnString(),
  'Bool': (d) => d.readColumnBool(),
  'UUID': (d) => d.readColumnUuid(),
  'Bytes': (d) => d.readColumnBytes(),
  'DateTime': (d) => d.readColumnDateTime(),
  'IntPtr': (d) => d.readColumnIntPtr(),
  'FloatPtr': (d) => d.readColumnFloatPtr(),
  'StringPtr': (d) => d.readColumnStringPtr(),
  'BoolPtr': (d) => d.readColumnBoolPtr(),
  'UUIDPtr': (d) => d.readColumnUuidPtr(),
  'BytesPtr': (d) => d.readColumnBytesPtr(),
  'DateTimePtr': (d) => d.readColumnDateTimePtr(),
};

// BigInt is an independent fixture encoder, not the runtime metadata reader.
List<int> _unsigned(BigInt value) {
  final bytes = <int>[];
  do {
    final part = (value & BigInt.from(127)).toInt();
    value >>= 7;
    bytes.add(part | (value == BigInt.zero ? 0 : 128));
  } while (value != BigInt.zero);
  return bytes;
}

Uint8List _batch(BigInt count, [List<int> payload = const [], int arena = 0]) =>
    Uint8List.fromList([
      ..._unsigned(count),
      ..._unsigned(BigInt.from(arena)),
      ...payload,
    ]);

void main() {
  test(
    'rejects nonempty zero-column and impossible-row lists before allocation',
    () {
      for (final count in [1, 128, 10_000_000]) {
        for (final payload in <List<int>>[
          [],
          [0],
          [1, 0],
          [0, 1, 2, 3, 4],
          [0x80, 0, 1, 2],
          [0x80, 0x80, 0, 1],
          [0x80, 1, 0],
        ]) {
          final decoder = ColumnarDecoder(_batch(BigInt.from(count), payload));
          expect(decoder.error, isNotNull);
          expect(decoder.nextColumn(), isFalse);
        }
      }
    },
  );

  test('accepts empty lists and nullable columns with multibyte field IDs', () {
    for (final count in [0, 1, 2]) {
      for (final id in count == 0 ? [0] : [1, 128, 16384]) {
        final payload = count == 0
            ? [0]
            : [..._unsigned(BigInt.from(id)), ...List.filled(count, 0), 0];
        final decoder = ColumnarDecoder(_batch(BigInt.from(count), payload));
        expect(decoder.error, isNull);
        if (count > 0) {
          expect(decoder.nextColumn(), isTrue);
          expect(decoder.fieldID, id);
          expect(
            decoder.readColumnIntPtr(),
            List<LuxoInt64?>.filled(count, null),
          );
        }
        expect(decoder.nextColumn(), isFalse);
        expect(decoder.error, isNull);
      }
    }
  });

  test('complete scalar readers reject missing and trailing bytes', () {
    final cases = <(Object Function(LuxoDecoder), List<int>, Object)>[
      ((d) => d.readIntChecked(), [0], LuxoInt64.zero),
      ((d) => d.readFloatChecked(), List.filled(8, 0), 0.0),
      ((d) => d.readStringChecked(), [0], ''),
      ((d) => d.readBoolChecked(), [0], false),
      ((d) => d.readDateTimeChecked(), [0, 0], '1970-01-01T00:00:00Z'),
      (
        (d) => d.readUuidChecked(),
        List.filled(16, 0),
        '00000000-0000-0000-0000-000000000000',
      ),
      ((d) => d.readBytesChecked(), [0], Uint8List(0)),
    ];
    for (final (read, bytes, expected) in cases) {
      expect(read(LuxoDecoder(Uint8List.fromList(bytes))), expected);
      for (final invalid in <List<int>>[
        [],
        [...bytes, 99],
      ]) {
        expect(
          () => read(LuxoDecoder(Uint8List.fromList(invalid))),
          throwsFormatException,
        );
      }
    }
  });

  test(
    'truncated markers and failed readers retain the original cursor and error',
    () {
      final empty = LuxoDecoder(Uint8List(0));
      expect(empty.readBool(), isFalse);
      expect(empty.error, contains('truncated bool'));
      final decoder = LuxoDecoder(Uint8List.fromList([2, 0, 0]));
      decoder.readBool();
      final error = decoder.error;
      expect(decoder.readString(), '');
      expect(decoder.readBytes(), isEmpty);
      expect(
        decoder.readNullable(() => throw StateError('invalid marker')),
        isNull,
      );
      expect(decoder.error, error);
      expect(decoder.offset, 1);
    },
  );

  test('matches 10,000 independently encoded safe metadata values on every platform', () {
    var state = 0x12345678;
    for (var index = 0; index < 10_000; index++) {
      state = (state * 1664525 + 1013904223) % 4294967296;
      final high = state % 2097152;
      state = (state * 1664525 + 1013904223) % 4294967296;
      final id =
          ((BigInt.from(high) << 32) | BigInt.from(state)) >> (index % 53);
      final decoder = LuxoDecoder(Uint8List.fromList([..._unsigned(id), 7]));
      expect(decoder.nextField(), id != BigInt.zero);
      expect(decoder.fieldID, id.toInt());
      expect(decoder.offset, decoder.wireBytes.length - 1);
      expect(decoder.error, isNull);
    }
  });

  test(
    'complete scalar array readers reject inner errors and trailing bytes',
    () {
      final readers = <List<Object?> Function(LuxoDecoder)>[
        (d) => d.readIntArrayChecked(),
        (d) => d.readFloatArrayChecked(),
        (d) => d.readStringArrayChecked(),
        (d) => d.readBoolArrayChecked(),
        (d) => d.readDateTimeArrayChecked(),
        (d) => d.readUuidArrayChecked(),
        (d) => d.readBytesArrayChecked(),
      ];
      for (final read in readers) {
        for (final raw in [
          <int>[],
          [1],
          [0, 99],
          _unsigned(BigInt.from(1_000_001)),
        ]) {
          expect(
            () => read(LuxoDecoder(Uint8List.fromList(raw))),
            throwsFormatException,
          );
        }
        expect(read(LuxoDecoder(Uint8List(1))), isEmpty);
      }
    },
  );

  group('metadata limits', () {
    test('rejects invalid model counts before invoking any callback', () {
      for (final count in [
        BigInt.from(1_000_001),
        BigInt.one << 32,
        BigInt.one << 53,
        (BigInt.one << 64) - BigInt.one,
      ]) {
        final decoder = LuxoDecoder(Uint8List.fromList(_unsigned(count)));
        expect(
          decoder.readArray(() => throw StateError('must not decode')),
          isEmpty,
        );
        expect(decoder.error, isNotNull);
      }
      final decoder = LuxoDecoder(Uint8List.fromList([1]));
      expect(decoder.readArray(() => throw StateError('no payload')), isEmpty);
      expect(decoder.error, isNotNull);
    });

    test('enforces the array cap even if sufficient payload bytes exist', () {
      final prefix = _unsigned(BigInt.from(1_000_001));
      final decoder = LuxoDecoder(
        Uint8List(prefix.length + 1_000_001)..setAll(0, prefix),
      );
      expect(decoder.readBoolArray().length, 0);
      expect(decoder.error, contains('limit'));
    });

    test('accepts the inclusive array limit and preserves the next field', () {
      for (final count in [0, 1, 128, 1_000_000]) {
        final prefix = _unsigned(BigInt.from(count));
        final bytes = Uint8List(prefix.length + count + 1)..setAll(0, prefix);
        bytes[bytes.length - 1] = 7;
        final decoder = LuxoDecoder(bytes);
        expect(decoder.readBoolArray().length, count);
        expect(decoder.error, isNull);
        expect(decoder.nextField(), isTrue);
        expect(decoder.fieldID, 7);
      }
    });

    test('caps column counts before narrowing or allocating', () {
      for (final count in [0, 1, 10_000_000]) {
        final header = _batch(BigInt.from(count));
        final bytes = Uint8List(header.length + count + 2)..setAll(0, header);
        if (count > 0) bytes[header.length] = 1;
        final decoder = ColumnarDecoder(bytes);
        expect(decoder.count, count);
        expect(decoder.error, isNull);
      }
      for (final count in [
        BigInt.from(10_000_001),
        BigInt.one << 32,
        BigInt.one << 63,
      ]) {
        final decoder = ColumnarDecoder(_batch(count));
        expect(decoder.count, 0);
        expect(decoder.error, isNotNull);
        expect(decoder.nextColumn(), isFalse);
      }
    });

    test('uses identical inclusive arena limits in row and column readers', () {
      for (final arena in [0, 1, 67_108_864, 67_108_865, 4294967296]) {
        final row = LuxoDecoder(
          Uint8List.fromList(_unsigned(BigInt.from(arena))),
        )..skipArenaHeader();
        final column = ColumnarDecoder(_batch(BigInt.zero, [], arena));
        expect(row.error == null, arena <= 67_108_864);
        expect(column.error == null, arena <= 67_108_864);
        expect(column.arenaSize, arena <= 67_108_864 ? arena : 0);
      }
    });

    test('rejects malformed metadata and retains the first error', () {
      for (final raw in [
        <int>[],
        [128],
        List<int>.filled(8, 255),
        _unsigned(BigInt.one << 53),
      ]) {
        final row = LuxoDecoder(Uint8List.fromList(raw));
        expect(
          row.readArray(() => throw StateError('invalid header')),
          isEmpty,
        );
        expect(row.error, isNotNull);
        final error = row.error;
        row.readIntArray();
        expect(row.error, error);
        final column = ColumnarDecoder(Uint8List.fromList(raw));
        expect(column.error, isNotNull);
        expect(column.readColumnFloat(), isEmpty);
      }
    });

    test('preserves safe field IDs rather than wrapping or aliasing the end marker', () {
      for (final id in [0, 1, 127, 128, 4294967296, 9007199254740991]) {
        final wire = _unsigned(BigInt.from(id));
        final row = LuxoDecoder(Uint8List.fromList(wire));
        final column = ColumnarDecoder(_batch(BigInt.zero, wire));
        expect(row.nextField(), id != 0);
        expect(column.nextColumn(), id != 0);
        expect(row.fieldID, id);
        expect(column.fieldID, id);
        expect(row.error, isNull);
        expect(column.error, isNull);
      }
      final row = LuxoDecoder(Uint8List.fromList(_unsigned(BigInt.one << 53)));
      expect(row.nextField(), isFalse);
      expect(row.error, contains('overflow'));
    });
  });

  group('payload bounds', () {
    test('Boolean arrays validate each byte and respect the input view', () {
      final storage = Uint8List.fromList([99, 4, 0, 1, 1, 0, 7, 0, 99]);
      final decoder = LuxoDecoder(Uint8List.sublistView(storage, 1, 8));
      final values = decoder.readBoolArray();
      expect(values, [false, true, true, false]);
      values.add(true);
      expect(decoder.nextField(), isTrue);
      expect(decoder.fieldID, 7);
      expect(decoder.nextField(), isFalse);
      expect(decoder.error, isNull);
      for (final marker in [2, 127, 128, 129, 255]) {
        final invalid = LuxoDecoder(Uint8List.fromList([3, 0, 1, marker, 7]));
        expect(invalid.readBoolArray(), isEmpty);
        expect(invalid.error, 'invalid bool marker at offset 3');
        expect(invalid.offset, 4);
        expect(invalid.nextField(), isFalse);
      }
    });

    test(
      'propagates item errors after a valid minimum-size array preflight',
      () {
        for (final (type, payload) in [
          ('Int', [128]),
          ('DateTime', [128]),
          ('String', [2, 65]),
          ('Bytes', [2, 65]),
          ('Bool', [2]),
        ]) {
          final decoder = LuxoDecoder(Uint8List.fromList([1, ...payload]));
          expect(_rowReaders[type]!(decoder), isEmpty);
          expect(decoder.error, isNotNull);
        }
      },
    );

    test(
      'generic array and nullable callbacks preserve values and item errors',
      () {
        final valid = LuxoDecoder(Uint8List.fromList([2, 0, 2]));
        expect(valid.readArray(valid.readInt), [
          LuxoInt64.zero,
          LuxoInt64.fromInt(1),
        ]);
        expect(valid.error, isNull);
        final truncated = LuxoDecoder(Uint8List.fromList([1, 128]));
        expect(truncated.readArray(truncated.readInt), isEmpty);
        expect(truncated.error, isNotNull);
        final nullable = LuxoDecoder(Uint8List.fromList([1, 2]));
        expect(nullable.readNullable(nullable.readInt), LuxoInt64.fromInt(1));
        expect(nullable.error, isNull);
      },
    );

    test('nullable fixed scalars distinguish null, value and truncation', () {
      final float = LuxoEncoder()..writeFixed64(1.5);
      final readers = <(Object? Function(LuxoDecoder), List<int>, Object)>[
        ((d) => d.readFloatPtr(), float.bytes(), 1.5),
        ((d) => d.readBoolPtr(), [1], true),
      ];
      for (final (read, bytes, expected) in readers) {
        final absent = LuxoDecoder(Uint8List(1));
        expect(read(absent), isNull);
        expect(absent.error, isNull);
        final present = LuxoDecoder(Uint8List.fromList([1, ...bytes]));
        expect(read(present), expected);
        expect(present.error, isNull);
        final truncated = LuxoDecoder(Uint8List.fromList([1]));
        read(truncated);
        expect(truncated.error, isNotNull);
      }
    });

    test(
      'nullable columns retain null, empty, false and full-width values',
      () {
        final floating = (LuxoEncoder()..writeFixed64(1.5)).bytes();
        final integer = (LuxoEncoder()..writeSvarint(LuxoInt64.maxValue))
            .bytes();
        final cases = <(String, List<int>, Object)>[
          ('Int', integer, LuxoInt64.maxValue),
          ('Float', floating, 1.5),
          ('String', [0], ''),
          ('Bool', [0], false),
          ('UUID', List.filled(16, 0), '00000000-0000-0000-0000-000000000000'),
          ('Bytes', [0], Uint8List(0)),
          ('DateTime', [0, 0], '1970-01-01T00:00:00Z'),
        ];
        for (final (type, bytes, expected) in cases) {
          final decoder = ColumnarDecoder(
            _batch(BigInt.from(3), [1, 0, 1, ...bytes, 0, 0]),
          );
          expect(decoder.nextColumn(), isTrue);
          expect(_columnReaders['${type}Ptr']!(decoder), [
            null,
            expected,
            null,
          ]);
          expect(decoder.error, isNull);
          expect(decoder.nextColumn(), isFalse);
          expect(decoder.offset, decoder.wireBytes.length);
        }
        final strings = ColumnarDecoder(_batch(BigInt.two, [1, 0, 0, 0]));
        expect(strings.nextColumn(), isTrue);
        expect(strings.readColumnString(), ['', '']);
        expect(strings.error, isNull);
      },
    );

    test(
      'column iteration distinguishes exhausted input from an invalid field ID',
      () {
        final empty = ColumnarDecoder(_batch(BigInt.zero));
        expect(empty.nextColumn(), isFalse);
        expect(empty.error, 'missing column end marker');
        final complete = ColumnarDecoder(_batch(BigInt.zero, [0]));
        expect(complete.nextColumn(), isFalse);
        expect(complete.error, isNull);
        final invalid = ColumnarDecoder(_batch(BigInt.zero, [128]));
        expect(invalid.nextColumn(), isFalse);
        expect(invalid.error, isNotNull);
        final error = invalid.error;
        expect(invalid.nextColumn(), isFalse);
        expect(invalid.error, error);
      },
    );

    for (final entry in _rowReaders.entries) {
      test('${entry.key} array rejects truncated metadata and payload', () {
        for (final raw in [
          <int>[],
          [128],
          [1],
          [2, 0],
        ]) {
          final decoder = LuxoDecoder(Uint8List.fromList(raw));
          expect(entry.value(decoder), isEmpty);
          expect(decoder.error, isNotNull);
        }
      });
    }
    for (final entry in _columnReaders.entries) {
      test(
        '${entry.key} column rejects missing data and accepts empty batches',
        () {
          final missing = ColumnarDecoder(_batch(BigInt.one));
          expect(entry.value(missing), isEmpty);
          expect(missing.error, isNotNull);
          expect(missing.offset, 2);
          final empty = ColumnarDecoder(_batch(BigInt.zero));
          expect(entry.value(empty), isEmpty);
          expect(empty.error, isNull);
          expect(empty.offset, 2);
        },
      );
    }

    test('checks fixed-width columns before reading their first cell', () {
      for (final (type, width) in [('Float', 8), ('UUID', 16)]) {
        final decoder = ColumnarDecoder(
          _batch(BigInt.two, List<int>.filled(width * 2 - 1, 0)),
        );
        expect(_columnReaders[type]!(decoder), isEmpty);
        expect(decoder.error, isNotNull);
        expect(decoder.offset, 2);
      }
    });

    test('checks every present nullable fixed-width cell', () {
      for (final type in ['FloatPtr', 'UUIDPtr']) {
        final decoder = ColumnarDecoder(_batch(BigInt.two, [0, 1, 0]));
        _columnReaders[type]!(decoder);
        expect(decoder.error, isNotNull);
        expect(decoder.offset, lessThanOrEqualTo(5));
      }
    });

    test('checks full blob lengths without advancing past the payload', () {
      for (final length in [
        BigInt.two,
        BigInt.one << 32,
        BigInt.one << 53,
        BigInt.one << 63,
      ]) {
        for (final type in ['String', 'Bytes', 'StringPtr', 'BytesPtr']) {
          final data = _batch(BigInt.one, [
            if (type.endsWith('Ptr')) 1,
            ..._unsigned(length),
            65,
          ]);
          final decoder = ColumnarDecoder(data);
          _columnReaders[type]!(decoder);
          expect(decoder.error, isNotNull, reason: type);
          expect(decoder.offset, lessThanOrEqualTo(data.length));
        }
      }
    });

    test('retains the first error and stops subsequent column readers', () {
      final decoder = ColumnarDecoder(_batch(BigInt.one, [2, 65]));
      decoder.readColumnString();
      final error = decoder.error;
      final offset = decoder.offset;
      expect(error, isNotNull);
      for (final read in _columnReaders.values) {
        expect(read(decoder), isEmpty);
        expect(decoder.error, error);
        expect(decoder.offset, offset);
      }
    });

    test('requires canonical single-byte Boolean and nullable markers', () {
      for (final raw in [
        [2],
        [128, 0],
        [129, 0],
      ]) {
        final decoder = LuxoDecoder(Uint8List.fromList(raw));
        decoder.readBool();
        expect(decoder.error, isNotNull);
        expect(decoder.offset, 1);
      }
      for (final type in ['StringPtr', 'BytesPtr']) {
        final decoder = ColumnarDecoder(_batch(BigInt.two, [1, 2, 1, 0, 0]));
        expect(decoder.nextColumn(), isTrue);
        _columnReaders[type]!(decoder);
        expect(decoder.error, contains('invalid nullable'));
      }
    });

    test(
      'keeps the complete signed Int64 domain independent from metadata limits',
      () {
        final encoder = LuxoEncoder()
          ..writeVarint(2)
          ..writeVarint(0)
          ..writeVarint(1)
          ..writeSvarint(LuxoInt64.minValue)
          ..writeSvarint(LuxoInt64.maxValue)
          ..writeVarint(0);
        final decoder = ColumnarDecoder(encoder.bytes());
        expect(decoder.nextColumn(), isTrue);
        expect(decoder.readColumnInt(), [
          LuxoInt64.minValue,
          LuxoInt64.maxValue,
        ]);
        expect(decoder.nextColumn(), isFalse);
        expect(decoder.error, isNull);
      },
    );
  });
}
