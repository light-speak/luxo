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
