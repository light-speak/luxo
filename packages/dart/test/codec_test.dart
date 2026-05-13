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
