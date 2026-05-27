import 'dart:convert';
import 'dart:typed_data';
import 'package:test/test.dart';
import 'package:luxo_client/src/codec.dart';

void main() {
  group('LuxoEncoder/LuxoDecoder', () {
    group('writeVarint/readVarint round-trip', () {
      // readInt uses zigzag, so for unsigned varint round-trip we use
      // the field-based approach: writeVarint then decode via nextField pattern.
      // But since _readVarint is private, we test via writeFieldInt/readInt (zigzag).

      test('zero', () {
        final enc = LuxoEncoder();
        enc.writeVarint(0);
        enc.writeVarint(0); // end marker implicitly

        // Verify the encoded byte for 0 is a single 0x00 byte
        final bytes = enc.bytes();
        expect(bytes[0], equals(0));
      });

      test('small values', () {
        final enc = LuxoEncoder();
        enc.writeVarint(1);
        final bytes = enc.bytes();
        expect(bytes[0], equals(1));
      });

      test('127 (max single-byte varint)', () {
        final enc = LuxoEncoder();
        enc.writeVarint(127);
        final bytes = enc.bytes();
        expect(bytes[0], equals(127));
        expect(bytes.length, equals(1));
      });

      test('128 (two-byte varint)', () {
        final enc = LuxoEncoder();
        enc.writeVarint(128);
        final bytes = enc.bytes();
        expect(bytes.length, equals(2));
        expect(bytes[0], equals(0x80)); // 128 & 0x7F | 0x80
        expect(bytes[1], equals(0x01)); // 128 >> 7
      });

      test('300 (two-byte varint)', () {
        final enc = LuxoEncoder();
        enc.writeVarint(300);
        final bytes = enc.bytes();
        // 300 = 0b100101100 -> byte0: 0101100 | 0x80 = 0xAC, byte1: 10 = 0x02
        expect(bytes[0], equals(0xAC));
        expect(bytes[1], equals(0x02));
      });

      test('large value 0x7FFFFFFF', () {
        final enc = LuxoEncoder();
        enc.writeVarint(0x7FFFFFFF);
        final bytes = enc.bytes();
        // Should require 5 bytes
        expect(bytes.length, equals(5));
      });
    });

    group('writeSvarint/readSvarint (zigzag) round-trip', () {
      void testRoundTrip(int value) {
        final enc = LuxoEncoder();
        // Use field pattern: fieldID + svarint
        enc.writeFieldInt(1, value);
        enc.writeEnd();

        final dec = LuxoDecoder(enc.bytes());
        expect(dec.nextField(), isTrue);
        expect(dec.fieldID, equals(1));
        expect(dec.readInt(), equals(value));
        expect(dec.nextField(), isFalse); // end marker
      }

      test('zero', () => testRoundTrip(0));
      test('positive 1', () => testRoundTrip(1));
      test('negative -1', () => testRoundTrip(-1));
      test('positive 42', () => testRoundTrip(42));
      test('negative -42', () => testRoundTrip(-42));
      test('large positive', () => testRoundTrip(2147483647));
      test('large negative', () => testRoundTrip(-2147483648));
      test('very large positive', () => testRoundTrip(9007199254740991)); // JS max safe int
      test('very large negative', () => testRoundTrip(-9007199254740991));
    });

    group('writeFixed64/readFixed64 round-trip', () {
      void testRoundTrip(double value) {
        final enc = LuxoEncoder();
        enc.writeFieldFloat(1, value);
        enc.writeEnd();

        final dec = LuxoDecoder(enc.bytes());
        expect(dec.nextField(), isTrue);
        expect(dec.fieldID, equals(1));
        expect(dec.readFloat(), equals(value));
        expect(dec.nextField(), isFalse);
      }

      test('zero', () => testRoundTrip(0.0));
      test('positive', () => testRoundTrip(3.14159));
      test('negative', () => testRoundTrip(-2.71828));
      test('large float', () => testRoundTrip(1.7976931348623157e+308));
      test('small float', () => testRoundTrip(5e-324));
      test('negative zero', () {
        final enc = LuxoEncoder();
        enc.writeFieldFloat(1, -0.0);
        enc.writeEnd();
        final dec = LuxoDecoder(enc.bytes());
        dec.nextField();
        final result = dec.readFloat();
        expect(result.isNegative, isTrue);
        expect(result, equals(0.0));
      });
    });

    group('writeString/readString round-trip', () {
      void testRoundTrip(String value) {
        final enc = LuxoEncoder();
        enc.writeFieldString(1, value);
        enc.writeEnd();

        final dec = LuxoDecoder(enc.bytes());
        expect(dec.nextField(), isTrue);
        expect(dec.fieldID, equals(1));
        expect(dec.readString(), equals(value));
        expect(dec.nextField(), isFalse);
      }

      test('empty string', () => testRoundTrip(''));
      test('hello', () => testRoundTrip('hello'));
      test('unicode', () => testRoundTrip('hello world'));
      test('chinese', () => testRoundTrip('你好世界'));
      test('emoji', () => testRoundTrip('🎉🚀'));
      test('long string', () => testRoundTrip('a' * 1000));
      test('special chars', () => testRoundTrip('line1\nline2\ttab'));
    });

    group('writeBool/readBool round-trip', () {
      void testRoundTrip(bool value) {
        final enc = LuxoEncoder();
        enc.writeFieldBool(1, value);
        enc.writeEnd();

        final dec = LuxoDecoder(enc.bytes());
        expect(dec.nextField(), isTrue);
        expect(dec.fieldID, equals(1));
        expect(dec.readBool(), equals(value));
        expect(dec.nextField(), isFalse);
      }

      test('true', () => testRoundTrip(true));
      test('false', () => testRoundTrip(false));
    });

    group('writeBytes/readBytes round-trip', () {
      test('empty bytes', () {
        final enc = LuxoEncoder();
        enc.writeFieldBytes(1, Uint8List(0));
        enc.writeEnd();

        final dec = LuxoDecoder(enc.bytes());
        expect(dec.nextField(), isTrue);
        expect(dec.readBytes(), equals(Uint8List(0)));
      });

      test('non-empty bytes', () {
        final data = Uint8List.fromList([0x01, 0x02, 0xFF, 0x00, 0xAB]);
        final enc = LuxoEncoder();
        enc.writeFieldBytes(1, data);
        enc.writeEnd();

        final dec = LuxoDecoder(enc.bytes());
        expect(dec.nextField(), isTrue);
        expect(dec.readBytes(), equals(data));
      });
    });

    group('multiple fields', () {
      test('mixed types in sequence', () {
        final enc = LuxoEncoder();
        enc.writeFieldInt(1, 42);
        enc.writeFieldString(2, 'hello');
        enc.writeFieldBool(3, true);
        enc.writeFieldFloat(4, 3.14);
        enc.writeEnd();

        final dec = LuxoDecoder(enc.bytes());

        expect(dec.nextField(), isTrue);
        expect(dec.fieldID, equals(1));
        expect(dec.readInt(), equals(42));

        expect(dec.nextField(), isTrue);
        expect(dec.fieldID, equals(2));
        expect(dec.readString(), equals('hello'));

        expect(dec.nextField(), isTrue);
        expect(dec.fieldID, equals(3));
        expect(dec.readBool(), isTrue);

        expect(dec.nextField(), isTrue);
        expect(dec.fieldID, equals(4));
        expect(dec.readFloat(), closeTo(3.14, 0.0001));

        expect(dec.nextField(), isFalse);
      });
    });

    group('nullable readers', () {
      test('null int', () {
        final enc = LuxoEncoder();
        enc.writeVarint(1); // fieldID
        enc.writeNull();
        enc.writeEnd();

        final dec = LuxoDecoder(enc.bytes());
        expect(dec.nextField(), isTrue);
        expect(dec.readIntPtr(), isNull);
      });

      test('present int', () {
        final enc = LuxoEncoder();
        enc.writeVarint(1); // fieldID
        enc.writePresent();
        enc.writeSvarint(99);
        enc.writeEnd();

        final dec = LuxoDecoder(enc.bytes());
        expect(dec.nextField(), isTrue);
        expect(dec.readIntPtr(), equals(99));
      });

      test('null string', () {
        final enc = LuxoEncoder();
        enc.writeVarint(1);
        enc.writeNull();
        enc.writeEnd();

        final dec = LuxoDecoder(enc.bytes());
        expect(dec.nextField(), isTrue);
        expect(dec.readStringPtr(), isNull);
      });

      test('present string', () {
        final enc = LuxoEncoder();
        enc.writeVarint(1);
        enc.writePresent();
        enc.writeString('test');
        enc.writeEnd();

        final dec = LuxoDecoder(enc.bytes());
        expect(dec.nextField(), isTrue);
        expect(dec.readStringPtr(), equals('test'));
      });
    });

    group('encoder reset', () {
      test('reset allows reuse', () {
        final enc = LuxoEncoder();
        enc.writeVarint(42);
        enc.reset();
        enc.writeVarint(7);
        final bytes = enc.bytes();
        expect(bytes.length, equals(1));
        expect(bytes[0], equals(7));
      });
    });

    group('encoder capacity growth', () {
      test('handles data larger than initial capacity', () {
        final enc = LuxoEncoder(4); // tiny initial capacity
        enc.writeString('a' * 100);
        final dec = LuxoDecoder(enc.bytes());
        expect(dec.readString(), equals('a' * 100));
      });
    });

    group('UUID 16-byte fixed', () {
      const sample = '550e8400-e29b-41d4-a716-446655440000';
      const zero = '00000000-0000-0000-0000-000000000000';
      const upper = 'AB12CD34-EF56-7890-ABCD-EF1234567890';

      test('round-trip canonical UUID is 16 bytes on the wire', () {
        final enc = LuxoEncoder();
        enc.writeFieldUuid(1, sample);
        enc.writeEnd();
        final bytes = enc.bytes();
        // fieldID(1 byte) + 16 bytes + end(1 byte) = 18
        expect(bytes.length, equals(18));

        final dec = LuxoDecoder(bytes);
        expect(dec.nextField(), isTrue);
        expect(dec.fieldID, equals(1));
        expect(dec.readUuid(), equals(sample));
        expect(dec.nextField(), isFalse);
      });

      test('all-zero UUID', () {
        final enc = LuxoEncoder();
        enc.writeFieldUuid(1, zero);
        enc.writeEnd();
        final dec = LuxoDecoder(enc.bytes());
        dec.nextField();
        expect(dec.readUuid(), equals(zero));
      });

      test('uppercase input is normalized to lowercase canonical', () {
        final enc = LuxoEncoder();
        enc.writeFieldUuid(1, upper);
        enc.writeEnd();
        final dec = LuxoDecoder(enc.bytes());
        dec.nextField();
        expect(dec.readUuid(), equals(upper.toLowerCase()));
      });

      test('exact wire bytes match raw 16-byte layout', () {
        final enc = LuxoEncoder();
        enc.writeUuid(sample); // 550e8400-e29b-41d4-a716-446655440000
        final bytes = enc.bytes();
        expect(bytes.length, equals(16));
        expect(bytes[0], equals(0x55));
        expect(bytes[1], equals(0x0e));
        expect(bytes[2], equals(0x84));
        expect(bytes[3], equals(0x00));
        expect(bytes[15], equals(0x00));
      });

      test('nullable UUID null', () {
        final enc = LuxoEncoder();
        enc.writeVarint(1);
        enc.writeNull();
        enc.writeEnd();
        final dec = LuxoDecoder(enc.bytes());
        dec.nextField();
        expect(dec.readUuidPtr(), isNull);
      });

      test('nullable UUID present', () {
        final enc = LuxoEncoder();
        enc.writeVarint(1);
        enc.writePresent();
        enc.writeUuid(sample);
        enc.writeEnd();
        final dec = LuxoDecoder(enc.bytes());
        dec.nextField();
        expect(dec.readUuidPtr(), equals(sample));
      });

      test('invalid length throws', () {
        final enc = LuxoEncoder();
        expect(() => enc.writeUuid('too-short'), throwsFormatException);
      });

      test('invalid hex digit throws', () {
        final enc = LuxoEncoder();
        expect(() => enc.writeUuid('zz0e8400-e29b-41d4-a716-446655440000'),
            throwsFormatException);
      });

      test('truncated wire data sets error', () {
        final dec = LuxoDecoder(Uint8List.fromList([0x01, 0x02, 0x03]));
        expect(dec.readUuid(), equals(''));
        expect(dec.error, isNotNull);
      });
    });

    group('scalar array fields (row format)', () {
      test('int array round-trip', () {
        final enc = LuxoEncoder();
        enc.writeFieldIntArray(1, [1, -2, 300, 0]);
        enc.writeEnd();
        final dec = LuxoDecoder(enc.bytes());
        expect(dec.nextField(), isTrue);
        expect(dec.readIntArray(), equals([1, -2, 300, 0]));
        expect(dec.nextField(), isFalse);
      });

      test('empty int array', () {
        final enc = LuxoEncoder();
        enc.writeFieldIntArray(1, const []);
        enc.writeEnd();
        final dec = LuxoDecoder(enc.bytes());
        dec.nextField();
        expect(dec.readIntArray(), isEmpty);
      });

      test('string array round-trip', () {
        final enc = LuxoEncoder();
        enc.writeFieldStringArray(1, ['a', '你好', '']);
        enc.writeEnd();
        final dec = LuxoDecoder(enc.bytes());
        dec.nextField();
        expect(dec.readStringArray(), equals(['a', '你好', '']));
      });

      test('float array round-trip', () {
        final enc = LuxoEncoder();
        enc.writeFieldFloatArray(1, [1.5, -2.25]);
        enc.writeEnd();
        final dec = LuxoDecoder(enc.bytes());
        dec.nextField();
        expect(dec.readFloatArray(), equals([1.5, -2.25]));
      });

      test('bool array round-trip', () {
        final enc = LuxoEncoder();
        enc.writeFieldBoolArray(1, [true, false, true]);
        enc.writeEnd();
        final dec = LuxoDecoder(enc.bytes());
        dec.nextField();
        expect(dec.readBoolArray(), equals([true, false, true]));
      });

      test('UUID array round-trip', () {
        const a = '550e8400-e29b-41d4-a716-446655440000';
        const b = '00000000-0000-0000-0000-000000000001';
        final enc = LuxoEncoder();
        enc.writeFieldUuidArray(1, [a, b]);
        enc.writeEnd();
        final dec = LuxoDecoder(enc.bytes());
        dec.nextField();
        expect(dec.readUuidArray(), equals([a, b]));
      });

      test('bytes array round-trip', () {
        final enc = LuxoEncoder();
        final items = [
          Uint8List.fromList([1, 2, 3]),
          Uint8List(0),
          Uint8List.fromList([0xFF]),
        ];
        enc.writeVarint(1); // fieldID
        enc.writeVarint(items.length);
        for (final it in items) {
          enc.writeBytes(it);
        }
        enc.writeEnd();
        final dec = LuxoDecoder(enc.bytes());
        dec.nextField();
        expect(dec.readBytesArray(), equals(items));
      });
    });
  });

  group('ColumnarDecoder', () {
    test('decode 2 records with int, string, float columns', () {
      final bb = BytesBuilder();
      // count = 2
      _writeVarint(bb, 2);
      // Column 1: fieldID=1, int values [42, -7]
      _writeVarint(bb, 1);
      _writeSvarint(bb, 42);
      _writeSvarint(bb, -7);
      // Column 2: fieldID=2, string values ["hello", "world"]
      _writeVarint(bb, 2);
      _writeString(bb, 'hello');
      _writeString(bb, 'world');
      // Column 3: fieldID=3, float values [3.14, 2.718]
      _writeVarint(bb, 3);
      _writeFixed64(bb, 3.14);
      _writeFixed64(bb, 2.718);
      // End marker
      bb.addByte(0x00);

      final dec = ColumnarDecoder(Uint8List.fromList(bb.toBytes()));
      expect(dec.count, equals(2));

      expect(dec.nextColumn(), isTrue);
      expect(dec.fieldID, equals(1));
      final ints = dec.readColumnInt();
      expect(ints, equals([42, -7]));

      expect(dec.nextColumn(), isTrue);
      expect(dec.fieldID, equals(2));
      final strings = dec.readColumnString();
      expect(strings, equals(['hello', 'world']));

      expect(dec.nextColumn(), isTrue);
      expect(dec.fieldID, equals(3));
      final floats = dec.readColumnFloat();
      expect(floats[0], closeTo(3.14, 0.0001));
      expect(floats[1], closeTo(2.718, 0.0001));

      expect(dec.nextColumn(), isFalse);
    });

    test('empty list (count=0)', () {
      final bb = BytesBuilder();
      _writeVarint(bb, 0);
      bb.addByte(0x00); // end marker immediately

      final dec = ColumnarDecoder(Uint8List.fromList(bb.toBytes()));
      expect(dec.count, equals(0));
      expect(dec.nextColumn(), isFalse);
    });

    test('nullable columns', () {
      final bb = BytesBuilder();
      // count = 3
      _writeVarint(bb, 3);
      // Column 1: fieldID=1, nullable int [null, 99, null]
      _writeVarint(bb, 1);
      bb.addByte(0x00); // null
      bb.addByte(0x01); _writeSvarint(bb, 99); // present
      bb.addByte(0x00); // null
      // Column 2: fieldID=2, nullable string [null, "hi", ""]
      _writeVarint(bb, 2);
      bb.addByte(0x00); // null
      bb.addByte(0x01); _writeString(bb, 'hi'); // present
      bb.addByte(0x01); _writeString(bb, ''); // present empty
      // End marker
      bb.addByte(0x00);

      final dec = ColumnarDecoder(Uint8List.fromList(bb.toBytes()));
      expect(dec.count, equals(3));

      expect(dec.nextColumn(), isTrue);
      expect(dec.fieldID, equals(1));
      final nullableInts = dec.readColumnIntPtr();
      expect(nullableInts, equals([null, 99, null]));

      expect(dec.nextColumn(), isTrue);
      expect(dec.fieldID, equals(2));
      final nullableStrings = dec.readColumnStringPtr();
      expect(nullableStrings, equals([null, 'hi', '']));

      expect(dec.nextColumn(), isFalse);
    });

    test('bool column', () {
      final bb = BytesBuilder();
      _writeVarint(bb, 3);
      _writeVarint(bb, 1); // fieldID=1
      _writeVarint(bb, 1); // true
      _writeVarint(bb, 0); // false
      _writeVarint(bb, 1); // true
      bb.addByte(0x00);

      final dec = ColumnarDecoder(Uint8List.fromList(bb.toBytes()));
      expect(dec.count, equals(3));
      expect(dec.nextColumn(), isTrue);
      expect(dec.readColumnBool(), equals([true, false, true]));
      expect(dec.nextColumn(), isFalse);
    });

    test('offset getter works after decoding', () {
      final bb = BytesBuilder();
      _writeVarint(bb, 1);
      _writeVarint(bb, 1); // fieldID=1
      _writeSvarint(bb, 10); // one int
      bb.addByte(0x00);

      final dec = ColumnarDecoder(Uint8List.fromList(bb.toBytes()));
      expect(dec.nextColumn(), isTrue);
      dec.readColumnInt();
      expect(dec.nextColumn(), isFalse);
      // offset should be at end
      expect(dec.offset, equals(bb.length));
    });

    test('UUID column (16 bytes each)', () {
      const a = '550e8400-e29b-41d4-a716-446655440000';
      const b = 'ffffffff-ffff-ffff-ffff-ffffffffffff';
      final bb = BytesBuilder();
      _writeVarint(bb, 2); // count=2
      _writeVarint(bb, 1); // fieldID=1
      _writeUuid(bb, a);
      _writeUuid(bb, b);
      bb.addByte(0x00); // end

      final dec = ColumnarDecoder(Uint8List.fromList(bb.toBytes()));
      expect(dec.count, equals(2));
      expect(dec.nextColumn(), isTrue);
      expect(dec.readColumnUuid(), equals([a, b]));
      expect(dec.nextColumn(), isFalse);
    });

    test('nullable UUID column', () {
      const a = '11111111-1111-1111-1111-111111111111';
      final bb = BytesBuilder();
      _writeVarint(bb, 3); // count=3
      _writeVarint(bb, 1); // fieldID=1
      bb.addByte(0x00); // null
      bb.addByte(0x01); _writeUuid(bb, a); // present
      bb.addByte(0x00); // null
      bb.addByte(0x00); // end

      final dec = ColumnarDecoder(Uint8List.fromList(bb.toBytes()));
      expect(dec.nextColumn(), isTrue);
      expect(dec.readColumnUuidPtr(), equals([null, a, null]));
      expect(dec.nextColumn(), isFalse);
    });

    test('scalar array column — each cell is a length-prefixed [count][items] blob', () {
      // Column where each cell is a string-array blob: [count][string...]
      final cell0 = BytesBuilder();
      _writeVarint(cell0, 2); // 2 items
      _writeString(cell0, 'x');
      _writeString(cell0, 'y');
      final cell1 = BytesBuilder();
      _writeVarint(cell1, 0); // empty array

      final bb = BytesBuilder();
      _writeVarint(bb, 2); // count=2 rows
      _writeVarint(bb, 1); // fieldID=1
      // cell 0 as length-prefixed blob
      final c0 = cell0.toBytes();
      _writeVarint(bb, c0.length);
      bb.add(c0);
      // cell 1 as length-prefixed blob
      final c1 = cell1.toBytes();
      _writeVarint(bb, c1.length);
      bb.add(c1);
      bb.addByte(0x00); // end

      final dec = ColumnarDecoder(Uint8List.fromList(bb.toBytes()));
      expect(dec.nextColumn(), isTrue);
      final blobs = dec.readColumnBytes();
      expect(blobs.length, equals(2));
      // Decode each cell blob with a row decoder.
      expect(LuxoDecoder(blobs[0]).readStringArray(), equals(['x', 'y']));
      expect(LuxoDecoder(blobs[1]).readStringArray(), isEmpty);
      expect(dec.nextColumn(), isFalse);
    });

    test('readSvarint public method', () {
      final bb = BytesBuilder();
      _writeVarint(bb, 0); // count=0
      bb.addByte(0x00); // end marker
      // Append extra svarint for pagination
      _writeSvarint(bb, -42);

      final dec = ColumnarDecoder(Uint8List.fromList(bb.toBytes()));
      expect(dec.nextColumn(), isFalse);
      expect(dec.readSvarint(), equals(-42));
    });
  });

  group('fieldMask utilities', () {
    test('set and check single field', () {
      var mask = Uint8List(0);
      mask = fieldMaskSet(mask, 0);
      expect(fieldMaskHas(mask, 0), isTrue);
      expect(fieldMaskHas(mask, 1), isFalse);
    });

    test('set and check multiple fields', () {
      var mask = Uint8List(0);
      mask = fieldMaskSet(mask, 3);
      mask = fieldMaskSet(mask, 7);
      mask = fieldMaskSet(mask, 15);
      expect(fieldMaskHas(mask, 3), isTrue);
      expect(fieldMaskHas(mask, 7), isTrue);
      expect(fieldMaskHas(mask, 15), isTrue);
      expect(fieldMaskHas(mask, 0), isFalse);
      expect(fieldMaskHas(mask, 8), isFalse);
    });

    test('fieldMaskHas returns false for out-of-range', () {
      final mask = Uint8List(1);
      expect(fieldMaskHas(mask, 100), isFalse);
    });

    test('mask grows as needed', () {
      var mask = Uint8List(0);
      mask = fieldMaskSet(mask, 32);
      expect(mask.length, greaterThanOrEqualTo(5));
      expect(fieldMaskHas(mask, 32), isTrue);
    });
  });
}

// --- Test helpers for building columnar binary data ---

void _writeVarint(BytesBuilder bb, int v) {
  var uv = v;
  while (uv & ~0x7F != 0) {
    bb.addByte((uv & 0x7F) | 0x80);
    uv = uv >>> 7;
  }
  bb.addByte(uv & 0x7F);
}

void _writeSvarint(BytesBuilder bb, int v) {
  _writeVarint(bb, (v << 1) ^ (v >> 63));
}

void _writeFixed64(BytesBuilder bb, double v) {
  final bd = ByteData(8);
  bd.setFloat64(0, v, Endian.little);
  bb.add(bd.buffer.asUint8List());
}

void _writeString(BytesBuilder bb, String v) {
  final encoded = utf8.encode(v);
  _writeVarint(bb, encoded.length);
  bb.add(encoded);
}

void _writeUuid(BytesBuilder bb, String v) {
  final hex = v.replaceAll('-', '');
  for (var i = 0; i < 16; i++) {
    bb.addByte(int.parse(hex.substring(i * 2, i * 2 + 2), radix: 16));
  }
}
