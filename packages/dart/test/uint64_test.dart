import 'dart:math';
import 'dart:typed_data';

import 'package:luxo_client/luxo_client.dart';
import 'package:luxo_client/src/uint64.dart';
import 'package:luxo_client/src/wire_writer.dart';
import 'package:test/test.dart';

void main() {
  test('writer inlining retains runtime buffer bounds checks', () {
    for (final capacity in [0, 1, 9]) {
      expect(
        () => writeWireWords(Uint8List(capacity), 0, 0xffffffff, 0xffffffff),
        throwsRangeError,
      );
    }
    expect(() => writeWireWords(Uint8List(10), -1, 0, 0), throwsRangeError);
  });

  test('word writer preserves every width and surrounding buffer bytes', () {
    final maximum = (BigInt.one << 64) - BigInt.one;
    final mask = BigInt.from(0xffffffff);
    for (var width = 0; width <= 64; width++) {
      for (final delta in [-1, 0, 1]) {
        final value = (BigInt.one << width) + BigInt.from(delta);
        if (value < BigInt.zero || value > maximum) continue;
        final reference = <int>[];
        var remaining = value;
        do {
          final byte = (remaining & BigInt.from(127)).toInt();
          remaining >>= 7;
          reference.add(remaining == BigInt.zero ? byte : byte | 128);
        } while (remaining != BigInt.zero);
        for (final start in [0, 3, 7]) {
          final buffer = Uint8List(24)..fillRange(0, 24, 0x5a);
          final end = writeWireWords(
            buffer,
            start,
            (value & mask).toInt(),
            (value >> 32).toInt(),
          );
          expect(end, start + reference.length, reason: '$value at $start');
          expect(buffer.sublist(start, end), reference);
          expect(buffer.sublist(0, start), everyElement(0x5a));
          expect(buffer.sublist(end), everyElement(0x5a));
        }
      }
    }
  });

  test('JSON request IDs preserve the complete uint64 range', () {
    for (final token in [
      '0',
      '1',
      '127',
      '128',
      '4294967295',
      '9007199254740991',
      '9007199254740992',
      '9007199254740993',
      '9223372036854775807',
      '9223372036854775808',
      '18446744073709551615',
    ]) {
      final value = uint64FromJSON(
        decodeLuxoJSON(token, preserveNumberSyntax: true),
      );
      expect(value.toString(), token);
      expect(encodeLuxoJSON(value), token);
      final reference = BigInt.parse(token);
      expect(value is int, reference <= BigInt.from(9007199254740991));
    }
    expect(uint64FromJSON(BigInt.one), 1);
  });

  test('noninteger, signed and overflowing request IDs are rejected', () {
    for (final token in [
      'null',
      'true',
      'false',
      '-1',
      '-0',
      '-0.0',
      '1.0',
      '1e0',
      '"1"',
      '[]',
      '{}',
      '18446744073709551616',
    ]) {
      expect(
        uint64FromJSON(decodeLuxoJSON(token, preserveNumberSyntax: true)),
        isNull,
        reason: token,
      );
    }
    expect(uint64FromJSON(BigInt.from(-1)), isNull);
    expect(uint64FromJSON(9007199254740992), isNull);
  });

  test('counter transitions to BigInt and fails without wrapping', () {
    final ids = RequestIDs(9007199254740990);
    expect(ids.next(), 9007199254740991);
    expect(ids.next(), BigInt.parse('9007199254740992'));
    expect(ids.next(), BigInt.parse('9007199254740993'));
    final maximum = BigInt.parse('18446744073709551615');
    final last = RequestIDs(maximum - BigInt.one);
    expect(last.next(), maximum);
    for (var i = 0; i < 2; i++) {
      expect(
        () => last.next(),
        throwsA(
          isA<LuxoError>().having(
            (e) => e.error,
            'error',
            'RequestIDExhausted',
          ),
        ),
      );
    }
  });

  test('counter initialization validates its representation', () {
    expect(RequestIDs().next(), 1);
    expect(RequestIDs(BigInt.one).next(), 2);
    for (final invalid in [-1, BigInt.from(-1), BigInt.one << 64, '1']) {
      expect(() => RequestIDs(invalid), throwsRangeError);
    }
  });

  test('uint64 writer validates boundaries before emitting bytes', () {
    final encoder = LuxoEncoder(0)..writeVarint(2);
    for (final invalid in [BigInt.from(-1), BigInt.one << 64]) {
      expect(() => encoder.writeUint64(invalid), throwsRangeError);
      expect(encoder.bytes(), [2]);
    }
    encoder.writeUint64((BigInt.one << 64) - BigInt.one);
    expect(encoder.bytes(), [2, ...List.filled(9, 255), 1]);
  });

  test(
    'two-word uint64 arithmetic matches an independent BigInt reference',
    () {
      final random = Random(64313);
      for (var i = 0; i < 10000; i++) {
        final low = random.nextInt(65536) * 65536 + random.nextInt(65536);
        final high = random.nextInt(65536) * 65536 + random.nextInt(65536);
        final reference = (BigInt.from(high) << 32) | BigInt.from(low);
        expect(uint64FromWords(low, high).toString(), reference.toString());
        final encoder = LuxoEncoder()..writeUint64(reference);
        final expected = <int>[];
        var remaining = reference;
        do {
          var byte = (remaining & BigInt.from(127)).toInt();
          remaining >>= 7;
          if (remaining != BigInt.zero) byte |= 128;
          expected.add(byte);
        } while (remaining != BigInt.zero);
        expect(encoder.bytes(), Uint8List.fromList(expected));
      }
    },
  );
}
