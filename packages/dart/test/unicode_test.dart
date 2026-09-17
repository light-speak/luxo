import 'dart:convert';
import 'dart:typed_data';

import 'package:luxo_client/luxo_client.dart';
import 'package:luxo_client/src/error_codec.dart';
import 'package:luxo_client/src/json.dart' show JSONRequestEncoder;
import 'package:test/test.dart';

const _malformed = [
  '\ud800',
  '\udbff',
  '\udc00',
  '\udfff',
  '\ud800x',
  '\udc00\ud800',
];
const _valid = [
  '',
  'ascii',
  '東京',
  '\ud800\udc00',
  '\udbff\udfff',
  '\ufffd',
  '\u0000\ufeff',
  '\ud7ff\ue000\uffff',
  r'\uD800',
  'e\u0301',
];

void main() {
  test('rejects incomplete host strings and keys without replacing them', () {
    for (final text in _malformed) {
      for (final value in [
        text,
        [text],
        {'value': text},
        {text: 1},
        {
          'items': [
            {text: text},
          ],
        },
      ]) {
        expect(() => encodeLuxoJSON(value), throwsFormatException);
        expect(() => normalizeLuxoJSON(value), throwsFormatException);
      }
      expect(
        () => luxoStringListFromJSON(['valid', text]),
        throwsFormatException,
      );
      expect(() => encodeLuxoJSON(_Encodable(text)), throwsFormatException);
      final writer = JSONRequestEncoder();
      expect(() => writer.encode(text, null), throwsFormatException);
      expect(() => writer.encode('echo', {text: 1}), throwsFormatException);
      writer.setSchema({
        'echo': const APISchemaEntry(
          1,
          [ParamSchema(1, 'input', 'Model', false, false, 'Input')],
          {},
          {
            'Input': {'id': SelectionFieldSchema(1, null, 'Int')},
          },
        ),
      });
      expect(
        () => writer.encode('echo', {
          'input': {text: 1},
        }),
        throwsFormatException,
      );
    }
  });

  test(
    'rejects escaped, literal, mixed and overwritten incomplete characters',
    () {
      for (final text in _malformed) {
        for (final token in ['"$text"', jsonEncode(text)]) {
          for (final raw in [
            token,
            '{$token:1}',
            '{"value":$token,"value":"valid"}',
          ]) {
            expect(() => decodeLuxoJSON(raw), throwsFormatException);
          }
        }
      }
      for (final raw in ['"\\ud800\udc00"', '"\ud800\\udc00"']) {
        expect(() => decodeLuxoJSON(raw), throwsFormatException);
      }
    },
  );

  test('preserves complete characters and leaves opaque Bytes untouched', () {
    for (final text in _valid) {
      final value = {
        text: [text, BigInt.parse('123456789012345678901234567890')],
      };
      expect(normalizeLuxoJSON(value), same(value));
      expect(decodeLuxoJSON(encodeLuxoJSON(value)), value);
      expect(decodeLuxoJSON(jsonEncode(text)), text);
      final encoder = LuxoEncoder()..writeString(text);
      final decoder = LuxoDecoder(encoder.bytes());
      expect(decoder.readStringChecked(), text);
    }
    final bytes = Uint8List.fromList([0xed, 0xa0, 0x80, 0xff]);
    expect(
      LuxoDecoder((LuxoEncoder()..writeBytes(bytes)).bytes())
          .readBytesChecked(),
      bytes,
    );
  });

  test('failed String field and array writes preserve the previous buffer', () {
    for (final text in _malformed) {
      for (final write in <void Function(LuxoEncoder)>[
        (encoder) => encoder.writeString(text),
        (encoder) => encoder.writeFieldString(2, text),
        (encoder) => encoder.writeFieldStringArray(3, ['valid', text, 'after']),
      ]) {
        final encoder = LuxoEncoder()..writeFieldInt(1, LuxoInt64.fromInt(42));
        final prefix = Uint8List.fromList(encoder.bytes());
        expect(() => write(encoder), throwsFormatException);
        expect(encoder.bytes(), prefix);
        encoder.writeString('next');
        expect(encoder.bytes().length, greaterThan(prefix.length));
      }
    }
  });

  test('rejects malformed direct error fields and Binary JSON error data', () {
    final valid = <String, Object?>{
      'error': 'Rejected',
      'code': 400,
      'message': 'bad',
    };
    for (final field in ['error', 'message', 'traceId', 'cause', 'data']) {
      expect(
        decodeJSONError({...valid, field: '\ud800'}, 502).error,
        'ParseError',
      );
    }
    for (final raw in [
      r'{"value":"\ud800"}',
      r'{"value":"\ud800","value":"valid"}',
    ]) {
      final encoder = LuxoEncoder()
        ..writeFieldInt(1, LuxoInt64.fromInt(400))
        ..writeFieldString(2, 'Rejected')
        ..writeFieldString(3, 'bad')
        ..writeFieldString(5, raw)
        ..writeEnd();
      expect(decodeBinaryError(encoder.bytes(), 502).error, 'ParseError');
    }
  });

  test(
    'deterministic mixtures agree with independent platform string decoding',
    () {
      const fragments = [
        'a',
        r'\\',
        r'\"',
        r'\n',
        r'\u0000',
        r'\uD800',
        r'\uDC00',
        r'\udbff',
        r'\udfff',
        r'\u0061',
        r'\uD7FF',
        r'\uE000',
        '東京',
        '\ud800',
        '\udc00',
        '\ud800\udc00',
      ];
      var state = 0x51a7c0de;
      for (var sample = 0; sample < 4096; sample++) {
        var value = '';
        for (var index = 0; index < 1 + sample % 8; index++) {
          state = (state * 1664525 + 1013904223) % 4294967296;
          value += fragments[state ~/ 268435456];
        }
        final token = '"$value"';
        var valid = _preservedByUTF8(token);
        try {
          valid = valid && _preservedByUTF8(jsonDecode(token) as String);
        } on FormatException {
          valid = false;
        }
        for (final raw in [
          token,
          '{$token:1}',
          '{"value":$token,"value":"valid"}',
        ]) {
          if (valid)
            expect(() => decodeLuxoJSON(raw), returnsNormally);
          else
            expect(() => decodeLuxoJSON(raw), throwsFormatException);
        }
      }
    },
  );
}

// An ASCII prefix prevents the platform decoder from stripping a content BOM.
bool _preservedByUTF8(String value) =>
    utf8.decode(utf8.encode('a$value')) == 'a$value';

final class _Encodable implements LuxoJSONEncodable {
  final String value;
  _Encodable(this.value);
  @override
  Map<String, Object?> toJson() => {'value': value};
}
