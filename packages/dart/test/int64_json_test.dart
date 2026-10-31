import 'dart:convert';
import 'dart:math';

import 'package:luxo_client/luxo_client.dart';
import 'package:test/test.dart';

void main() {
  test('typed JSON lists reject wrong kinds before exposing any elements', () {
    for (final decode in [luxoStringListFromJSON, luxoBooleanListFromJSON]) {
      for (final value in [null, 1, '[]', true, {}]) {
        expect(() => decode(value), throwsA(isA<TypeError>()));
      }
      for (final value in [null, 1, [], {}, LuxoJSONNumber.parse('1.0')]) {
        expect(() => decode([value]), throwsA(isA<TypeError>()));
      }
    }
    expect(() => luxoStringListFromJSON([false]), throwsA(isA<TypeError>()));
    expect(() => luxoBooleanListFromJSON(['true']), throwsA(isA<TypeError>()));
    expect(
      () => luxoStringListFromJSON(['ok', false]),
      throwsA(isA<TypeError>()),
    );
    expect(
      () => luxoBooleanListFromJSON([true, 'false']),
      throwsA(isA<TypeError>()),
    );
    expect(luxoStringListFromJSON(['', 'false', '发布']), ['', 'false', '发布']);
    expect(luxoBooleanListFromJSON([false, true]), [false, true]);
  });

  test('raw numbers validate syntax and convert only on explicit request', () {
    for (final token in [
      '-0',
      '1.2300',
      '1E+3',
      '123456789012345678901234567890',
      '1e400',
      '1e-400',
    ]) {
      final value = LuxoJSONNumber.parse(token);
      expect(value.literal, token);
      expect(encodeLuxoJSON(value), token);
    }
    for (final token in [
      '',
      ' 1',
      '1 ',
      '01',
      '+1',
      '1.',
      '1e',
      'NaN',
      'Infinity',
      'null',
      'true',
      '"1"',
      '[]',
      '{}',
    ]) {
      expect(() => LuxoJSONNumber.parse(token), throwsFormatException);
    }
    expect(
      LuxoJSONNumber.parse('-9223372036854775808').toInt64(),
      LuxoInt64.minValue,
    );
    for (final token in ['9223372036854775808', '1.0', '1e0']) {
      expect(
        () => LuxoJSONNumber.parse(token).toInt64(),
        throwsFormatException,
      );
    }
    for (final token in ['1e400', '1e-400', '-1e-400']) {
      expect(
        () => LuxoJSONNumber.parse(token).toDouble(),
        throwsFormatException,
      );
    }
    expect(
      LuxoJSONNumber.parse('0.12345678901234567890123456789').toDouble(),
      0.12345678901234568,
    );
    expect(LuxoJSONNumber.parse('0e99999').toDouble(), 0.0);
    expect(LuxoJSONNumber.parse('-0e-400').toDouble().isNegative, isTrue);
  });

  test(
    'generic numeric tokens survive normalization and binary JSON fields',
    () {
      for (final token in [
        '0',
        '-0',
        '1.0',
        '1E+03',
        '9007199254740993',
        '123456789012345678901234567890',
        '0.12345678901234567890123456789',
        '1e400',
        '1e-400',
      ]) {
        final text = '{"n":$token,"items":[$token,null],"nested":{"n":$token}}';
        for (final typed in [false, true]) {
          final value = decodeLuxoJSON(text, preserveNumberSyntax: typed);
          expect(encodeLuxoJSON(value), text, reason: token);
          expect(encodeLuxoJSON(normalizeLuxoJSON(value)), text, reason: token);
        }
        final encoder = LuxoEncoder()..writeString(text);
        final decoder = LuxoDecoder(encoder.bytes());
        expect(
          encodeLuxoJSON(decodeLuxoJSON(decoder.readString())),
          text,
          reason: token,
        );
        expect(decoder.error, isNull);
      }
    },
  );

  test('standard JSON shapes and escapes match the SDK JSON codec', () {
    final random = Random(64);
    for (var i = 0; i < 2000; i++) {
      final value = <String, Object?>{
        'integer': random.nextInt(1000000),
        'float': random.nextDouble() * 1000,
        'boolean': random.nextBool(),
        'null': null,
        'string': 'quotes " \\ \t\n\r Unicode \u00e9 \u6c49',
        'empty': <Object?>[],
        'object': <String, Object?>{},
      };
      final decoded = decodeLuxoJSON(
        ' \t\r\n${jsonEncode(value)} ',
      ) as Map<String, Object?>;
      expect(decoded['float'], isA<LuxoJSONNumber>());
      decoded['float'] = luxoDoubleFromJSON(decoded['float']);
      expect(decoded, value);
      expect(jsonDecode(encodeLuxoJSON(value)), value);
    }
    expect(luxoDoubleFromJSON(decodeLuxoJSON('1E+2')), 100.0);
    expect(luxoDoubleFromJSON(decodeLuxoJSON('1e-2')), 0.01);
    expect(encodeLuxoJSON(decodeLuxoJSON('1e400')), '1e400');
    final negativeZero = decodeLuxoJSON('-0', preserveNumberSyntax: true);
    expect(luxoDoubleFromJSON(negativeZero).isNegative, isTrue);
    expect(luxoInt64FromJSON(negativeZero), LuxoInt64.zero);
    expect(luxoDoubleFromJSON(decodeLuxoJSON('-0')).isNegative, isTrue);
    expect(luxoDoubleFromJSON(0.5), 0.5);
    expect(decodeLuxoJSON('9007199254740991'), 9007199254740991);
    expect(decodeLuxoJSON('-9007199254740991'), -9007199254740991);
    expect(
      encodeLuxoJSON(
        LuxoFilter(field: 'id', op: 'eq', value: LuxoInt64.maxValue),
      ),
      '{"field":"id","op":"eq","value":9223372036854775807}',
    );
    expect(normalizeLuxoJSON(decodeLuxoJSON('1e400')), isA<LuxoJSONNumber>());
    final unchanged = {
      'list': [1, true, null],
    };
    expect(identical(normalizeLuxoJSON(unchanged), unchanged), isTrue);
  });
  test('JSON int64 stays an unquoted, exact numeric token', () {
    for (final decimal in [
      '0',
      '9007199254740993',
      '-9007199254740993',
      '9223372036854775807',
      '-9223372036854775808',
    ]) {
      final value = LuxoInt64.parse(decimal);
      final wire = encodeLuxoJSON({
        'value': value,
        'list': [value, null],
      });
      expect(wire, '{"value":$decimal,"list":[$decimal,null]}');
      final decoded = decodeLuxoJSON(wire) as Map<String, Object?>;
      expect(luxoInt64FromJSON(decoded['value']), value);
      expect(luxoInt64FromJSON((decoded['list'] as List).first), value);
    }
  });

  test('typed integers reject overflow, strings and fractional syntax', () {
    for (final token in [
      '9223372036854775808',
      '-9223372036854775809',
      '1.0',
      '1e0',
      'true',
      'null',
      '"42"',
    ]) {
      final decoded = decodeLuxoJSON(token, preserveNumberSyntax: true);
      expect(() => luxoInt64FromJSON(decoded), throwsFormatException);
    }
    expect(() => luxoInt64FromJSON(9007199254740992), throwsRangeError);
  });

  test('Float accepts numeric tokens without becoming an Int', () {
    for (final token in ['0.5', '-0.0', '1e20', '9223372036854775808']) {
      final decoded = decodeLuxoJSON(token, preserveNumberSyntax: true);
      expect(luxoDoubleFromJSON(decoded), double.parse(token));
    }
    expect(
      () => luxoDoubleFromJSON(decodeLuxoJSON('1e400')),
      throwsFormatException,
    );
    expect(() => luxoDoubleFromJSON('1.0'), throwsFormatException);
    expect(() => encodeLuxoJSON(double.nan), throwsFormatException);
    expect(() => encodeLuxoJSON(double.infinity), throwsFormatException);
  });

  test('generic JSON preserves large integers and raw fractional values', () {
    const wire =
        '{"text":"123 \\\" 9007199254740993","n":1.25,"nested":[1e0,18446744073709551616]}';
    final decoded = decodeLuxoJSON(wire, preserveNumberSyntax: true);
    final normalized = normalizeLuxoJSON(decoded) as Map<String, Object?>;
    expect(normalized['text'], '123 " 9007199254740993');
    expect((normalized['n'] as LuxoJSONNumber).literal, '1.25');
    final nested = normalized['nested'] as List;
    expect((nested[0] as LuxoJSONNumber).literal, '1e0');
    expect(nested[1], BigInt.parse('18446744073709551616'));
    expect(identical(normalized, decoded), isTrue);
    expect(encodeLuxoJSON(normalized), wire);
    final frozen = Map<String, Object?>.unmodifiable({
      'values': List<Object?>.unmodifiable([LuxoJSONNumber.parse('1.2300'), 1]),
    });
    expect(identical(normalizeLuxoJSON(frozen), frozen), isTrue);
    expect(encodeLuxoJSON(frozen), '{"values":[1.2300,1]}');
  });

  test('malformed JSON and excessive nesting are rejected', () {
    for (final invalid in [
      '[1 2]',
      '{"a":1 x}',
      '{"a" 1}',
      'truX',
      '"\\',
      '[1,',
      '{"a":1,',
    ]) {
      expect(() => decodeLuxoJSON(invalid), throwsFormatException);
    }
    for (final invalid in [
      '',
      '01',
      '-01',
      '+1',
      '1.',
      '.1',
      '1e',
      '1e+',
      '[1,]',
      '{"a":1,}',
      '{a:1}',
      '[',
      '{',
      'true false',
      'null x',
      '"unterminated',
      '"\\x"',
      '"line\nbreak"',
      List.filled(130, '[').join() + '0' + List.filled(130, ']').join(),
    ]) {
      expect(
        () => decodeLuxoJSON(invalid),
        throwsFormatException,
        reason: invalid,
      );
    }
    final cycle = <Object?>[];
    cycle.add(cycle);
    expect(() => encodeLuxoJSON(cycle), throwsFormatException);
    expect(() => normalizeLuxoJSON(cycle), throwsFormatException);
    final objectCycle = <String, Object?>{};
    objectCycle['self'] = objectCycle;
    expect(() => encodeLuxoJSON(objectCycle), throwsFormatException);
    for (final brackets in [('[', ']'), ('{"v":', '}')]) {
      final deep =
          List.filled(129, brackets.$1).join() +
          List.filled(129, brackets.$2).join();
      expect(() => decodeLuxoJSON(deep), throwsFormatException);
    }
    expect(() => encodeLuxoJSON({1: 'invalid key'}), throwsFormatException);
    expect(() => encodeLuxoJSON(Object()), throwsFormatException);
  });
}
