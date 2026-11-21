import 'dart:math';

import 'package:luxo_client/luxo_client.dart';
import 'package:test/test.dart';

void main() {
  group('LuxoInt64 on native and Web', () {
    test('preserves signed boundaries and integers above the safe range', () {
      for (final decimal in [
        '0',
        '1',
        '-1',
        '2147483647',
        '-2147483648',
        '4294967296',
        '4503599627370496',
        '-4503599627370497',
        '9007199254740991',
        '9007199254740992',
        '9007199254740993',
        '-9007199254740993',
        '9223372036854775807',
        '-9223372036854775808',
      ]) {
        final value = LuxoInt64.parse(decimal);
        expect(value.toString(), decimal);
        expect(value.toBigInt(), BigInt.parse(decimal));
        expect(LuxoInt64.fromBigInt(value.toBigInt()), value);
        expect(value.isNegative, decimal.startsWith('-'));
      }
    });

    test('rejects overflow rather than wrapping', () {
      for (final decimal in [
        '9223372036854775808',
        '-9223372036854775809',
        '18446744073709551616',
        '-18446744073709551616',
        '999999999999999999999999999999999999999',
      ]) {
        expect(() => LuxoInt64.parse(decimal), throwsFormatException);
        expect(
          () => LuxoInt64.fromBigInt(BigInt.parse(decimal)),
          throwsFormatException,
        );
      }
    });

    test('requires decimal integer syntax', () {
      for (final invalid in [
        '',
        '-',
        '+1',
        ' 1',
        '1 ',
        '00',
        '-00',
        '01',
        '-01',
        '1.0',
        '1e3',
        '0xff',
        '--1',
        'NaN',
        'Infinity',
        '\u0661',
      ]) {
        expect(() => LuxoInt64.parse(invalid), throwsFormatException);
      }
      expect(LuxoInt64.parse('-0'), LuxoInt64.zero);
    });

    test('int conversion has the same safe range on every platform', () {
      for (final value in [0, 1, -1, 9007199254740991, -9007199254740991]) {
        expect(LuxoInt64.fromInt(value).toInt(), value);
      }
      for (final value in [9007199254740992, -9007199254740992]) {
        expect(() => LuxoInt64.fromInt(value), throwsRangeError);
        expect(() => LuxoInt64.parse('$value').toInt(), throwsRangeError);
      }
      expect(() => LuxoInt64.minValue.toInt(), throwsRangeError);
      expect(() => LuxoInt64.maxValue.toInt(), throwsRangeError);
    });

    test('comparison and equality agree at the signed boundary', () {
      final values = [
        LuxoInt64.minValue,
        LuxoInt64.fromInt(-1),
        LuxoInt64.zero,
        LuxoInt64.fromInt(1),
        LuxoInt64.maxValue,
      ];
      for (var i = 0; i < values.length; i++) {
        for (var j = 0; j < values.length; j++) {
          expect(values[i].compareTo(values[j]).sign, i.compareTo(j).sign);
          expect(values[i] == values[j], i == j);
        }
        expect(values[i].hashCode, LuxoInt64.parse('${values[i]}').hashCode);
      }
      expect(LuxoInt64.minValue.toString(), '-9223372036854775808');
      expect(LuxoInt64.maxValue.toString(), '9223372036854775807');
    });

    test('deterministic full-width round trips against BigInt', () {
      final random = Random(64);
      final modulus = BigInt.one << 64;
      for (var i = 0; i < 10000; i++) {
        var value = BigInt.zero;
        for (var byte = 0; byte < 8; byte++) {
          value = (value << 8) | BigInt.from(random.nextInt(256));
        }
        if (value >= BigInt.one << 63) value -= modulus;
        final encoded = LuxoInt64.fromBigInt(value);
        expect(encoded.toBigInt(), value);
        expect(encoded.toString(), value.toString());
      }
    });
  });
}
