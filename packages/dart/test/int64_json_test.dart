import 'dart:convert';
import 'dart:math';

import 'package:luxo_client/luxo_client.dart';
import 'package:luxo_client/src/json.dart'
    show
        JSONSocketDecoder,
        JSONModelDepthError,
        LuxoJSONWritable,
        LuxoJSONOutput,
        normalizeLuxoJSONList;
import 'package:luxo_client/src/error_codec.dart' show decodeJSONError;
import 'package:test/test.dart';

void main() {
  test(
    'JSON string encoding matches the standard codec at escape boundaries',
    () {
      for (final text in _jsonStringSamples()) {
        for (final value in <Object?>[
          text,
          [text, '', text],
          <String, Object?>{text: text, 'after': text},
        ]) {
          expect(encodeLuxoJSON(value), jsonEncode(value));
        }
      }
    },
  );
  test(
    'JSON string encoding rejects incomplete Unicode in values and keys',
    () {
      for (final units in [
        [0xd800],
        [0xdbff],
        [0xdc00],
        [0xdfff],
        [0xd800, 0x61],
        [0xdbff, 0xd800],
        [0xdc00, 0xdfff],
        [0xdc00, 0xd800],
        [0xd800, 0xdc00, 0xd800],
      ]) {
        for (final text in [
          String.fromCharCodes(units),
          'head${String.fromCharCodes(units)}tail',
        ]) {
          for (final value in <Object?>[
            text,
            [text],
            {text: true},
          ]) {
            expect(
              () => encodeLuxoJSON(value),
              throwsA(
                isA<FormatException>().having(
                  (error) => error.message,
                  'message',
                  'String must contain complete Unicode characters',
                ),
              ),
            );
          }
        }
      }
    },
  );
  test(
    'normalization preserves typed and broad maps at container boundaries',
    () {
      for (final broad in [false, true]) {
        for (final frozen in [false, true]) {
          for (final depth in [128, 129]) {
            Object? value = true;
            for (var i = 0; i < depth; i++) {
              value = broad
                  ? <Object?, Object?>{'next': value}
                  : <String, Object?>{'next': value};
              if (frozen) {
                value = broad
                    ? Map<Object?, Object?>.unmodifiable(value as Map)
                    : Map<String, Object?>.unmodifiable(
                        value as Map<String, Object?>,
                      );
              }
            }
            if (depth == 128) {
              expect(identical(normalizeLuxoJSON(value), value), isTrue);
            } else {
              expect(() => normalizeLuxoJSON(value), throwsFormatException);
            }
          }
        }
      }
      for (final value in [
        <Object?, Object?>{null: true},
        <Object?, Object?>{1: true},
      ]) {
        expect(() => normalizeLuxoJSON(value), throwsFormatException);
      }
      expect(normalizeLuxoJSON(<int, Object?>{}), isEmpty);
    },
  );
  test(
    'static JSON writers preserve independent field budgets without projection',
    () {
      Object? raw = true;
      for (var layer = 0; layer < 128; layer++) raw = [raw];
      final value = _StaticJSON(raw);
      final expected = '{"value":${'[' * 128}true${']' * 128}}';
      expect(encodeLuxoJSON(value), expected);
      expect(value.projections, 0);
      expect(value.writes, 1);
      expect(() => encodeLuxoJSON(value, opaque: true), throwsFormatException);
      expect(value.projections, 1);
      expect(value.writes, 1);
      expect(() => encodeLuxoJSON(_StaticJSON([raw])), throwsFormatException);
      final legacy = _ProjectedJSON([true]);
      expect(encodeLuxoJSON(legacy), '{"value":[true]}');
      expect(legacy.projections, 1);
      expect(encodeLuxoJSON(_StaticJSON(null)), '{"value":null}');
    },
  );
  test(
    'typed JSON lists preserve identity and bound every element separately',
    () {
      Object? raw = true;
      for (var layer = 0; layer < 128; layer++) raw = [raw];
      final values = [raw, raw, null];
      final empty = <Object?>[];
      expect(identical(normalizeLuxoJSONList(values), values), isTrue);
      expect(identical(values[0], raw), isTrue);
      expect(identical(normalizeLuxoJSONList(empty), empty), isTrue);
      expect(
        () => normalizeLuxoJSONList([
          [raw],
        ]),
        throwsFormatException,
      );
      expect(identical(normalizeLuxoJSONList(values), values), isTrue);
    },
  );
  test('source slice quotes preserve backslash parity and final routing', () {
    const node = JSONResponseSchema(
      modelType: 'Node',
      types: {
        'Node': {'metadata': SelectionFieldSchema(1, null, 'JSON')},
      },
    );
    final known = JSONSocketDecoder(
      (value) => value[r'$id'] == 1 ? node : null,
    );
    final discard = JSONSocketDecoder((_) => null);
    for (var length = 0; length <= 12; length++) {
      for (final text in [
        '${'\\' * length}"',
        '${'\\' * length}"}],"\$id":2,"data":"fake',
        '汉😀${'\\' * length}',
      ]) {
        final payload = jsonEncode({'metadata': text});
        final source = '{"data":$payload,"\$id":2,"\$id":1}';
        expect(known.decode(source)['data'], {'metadata': text});
        expect(discard.decode(source)['data'], isNull);
        expect(decodeLuxoJSONEnvelope(source, response: node)['data'], {
          'metadata': text,
        });
        for (var end = 0; end < source.length; end++) {
          for (final decoder in [known, discard]) {
            expect(
              () => decoder.decode(source.substring(0, end)),
              throwsFormatException,
              reason: source.substring(0, end),
            );
          }
        }
      }
    }
  });
  test('socket data slices exclude valid trailing JSON whitespace', () {
    final decoder = JSONSocketDecoder((_) => const JSONResponseSchema());
    for (final value in [
      'null',
      'true',
      '123',
      '"text"',
      '[]',
      '{}',
      '{"child":[]}',
    ]) {
      for (final space in [' ', '\t', '\r\n', ' \t\r\n']) {
        final source = ' { "data" : $value$space, "\$id":1 } \n';
        expect(
          decoder.decode(source),
          decodeLuxoJSONEnvelope(source),
          reason: source,
        );
      }
    }
  });
  test(
    'socket plain string runs preserve every escape and control boundary',
    () {
      final discard = JSONSocketDecoder((_) => null);
      final known = JSONSocketDecoder((_) => const JSONResponseSchema());
      for (final length in [0, 1, 3, 4, 5, 7, 8, 9, 127, 128, 129]) {
        for (final prefix in ['汉' * length, 'abcde' * length, '😀' * length]) {
          for (final suffix in ['END', ' [] {} ~', '\n\t"\\', '汉😀\u2028']) {
            final text = '$prefix$suffix';
            final source = '{"data":${jsonEncode(text)}}';
            expect(discard.decode(source)['data'], isNull);
            expect(known.decode(source)['data'], text);
          }
          for (var code = 0; code < 32; code++) {
            expect(
              () => discard.decode(
                '{"data":"$prefix${String.fromCharCode(code)}end"}',
              ),
              throwsFormatException,
            );
          }
          for (final suffix in [r'\uD800', r'\uDC00', r'\uD800\u0061', r'\x']) {
            expect(
              () => discard.decode('{"data":"$prefix$suffix"}'),
              throwsFormatException,
            );
          }
        }
      }
    },
  );
  test(
    'all duplicate data positions retain independent budgets and final value',
    () {
      const node = JSONResponseSchema(
        modelType: 'Node',
        types: {
          'Node': {
            'metadata': SelectionFieldSchema(1, null, 'JSON'),
            'child': SelectionFieldSchema(2, 'Node', 'Model'),
          },
        },
      );
      final socket = JSONSocketDecoder((_) => node);
      for (final count in [1, 2, 3, 5]) {
        final values = List.generate(count, (index) => '{"metadata":$index}');
        String envelope(List<String> values) =>
            '{${values.map((value) => '"data":$value').join(',')}}';
        expect(socket.decode(envelope(values))['data'], {
          'metadata': count - 1,
        });
        expect(
          decodeLuxoJSONEnvelope(envelope(values), response: node)['data'],
          {'metadata': count - 1},
        );
        for (var bad = 0; bad < count; bad++) {
          for (final payload in [
            '{"metadata":${'[' * 129}0${']' * 129}}',
            '${'{"child":' * 64}{}${'}' * 64}',
          ]) {
            final invalid = [...values]..[bad] = payload;
            expect(
              () => socket.decode(envelope(invalid)),
              throwsFormatException,
            );
            expect(
              () => decodeLuxoJSONEnvelope(envelope(invalid), response: node),
              throwsFormatException,
            );
          }
        }
      }
    },
  );
  test('socket syntax discard validates grammar, Unicode and deep frames', () {
    final discard = JSONSocketDecoder((_) => null);
    final known = JSONSocketDecoder((_) => const JSONResponseSchema());
    for (final value in [
      'null',
      'true',
      'false',
      '123',
      '-0',
      '1.25E+2',
      '1e-2',
      '1e400',
      r'"\"\\\/\b\f\n\r\t\u0000\u00e9\uABCD\ud83d\uDE00"',
      '"汉字😀"',
      '[]',
      '{}',
      '[{"x":[1,null,true,false]}]',
    ]) {
      final source = '{"data":$value}';
      expect(discard.decode(source), {'data': null});
      expect(
        encodeLuxoJSON(known.decode(source)),
        encodeLuxoJSON(decodeLuxoJSONEnvelope(source)),
      );
      for (var end = 0; end < source.length; end++) {
        expect(
          () => discard.decode(source.substring(0, end)),
          throwsFormatException,
        );
      }
    }
    for (final value in [
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
      '[1 2]',
      '{"a" 1}',
      '{"a":1 x}',
      'truX',
      'true false',
      'null x',
      '"line\nbreak"',
      r'"\x"',
      r'"\u0"',
      r'"\u00x0"',
      r'"\uD800"',
      r'"\uDC00"',
      r'"\uD800\u0041"',
      r'"\uD800x"',
    ]) {
      expect(
        () => discard.decode('{"data":$value}'),
        throwsFormatException,
        reason: value,
      );
    }
    final deep = '${'[{"v":' * 5000}null${'}]' * 5000}';
    expect(discard.decode('{"data":$deep}'), {'data': null});
    expect(
      () => discard.decode('{"data":${'[' * 5000}0,]${']' * 4999}}'),
      throwsFormatException,
    );
    expect(discard.decode('{"data":0}'), {'data': null});
    final random = Random(128);
    for (var i = 0; i < 500; i++) {
      final source = jsonEncode({
        'data': {
          'n': random.nextDouble(),
          'v': [i, null, random.nextBool()],
          's': '"\\\n汉字',
        },
      });
      expect(discard.decode(source), {'data': null});
      final index = random.nextInt(source.length);
      final mutated = source.replaceRange(
        index,
        index + 1,
        ['[', '}', ',', '"', '\\'][i % 5],
      );
      var valid = true;
      try {
        decodeLuxoJSONEnvelope(mutated);
      } on FormatException {
        valid = false;
      }
      if (valid) {
        expect(() => discard.decode(mutated), returnsNormally);
      } else {
        expect(
          () => discard.decode(mutated),
          throwsFormatException,
          reason: mutated,
        );
      }
    }
  });

  test('socket depth failures follow final metadata and never hide malformed source', () {
    const node = JSONResponseSchema(
      modelType: 'Node',
      types: {
        'Node': {
          'child': SelectionFieldSchema(1, 'Node', 'Model'),
          'metadata': SelectionFieldSchema(2, null, 'JSON'),
        },
      },
    );
    var resolutions = 0;
    final decoder = JSONSocketDecoder((json) {
      resolutions++;
      return json[r'$id'] == 1 ? node : null;
    });
    final model65 = '${'{"child":' * 64}{}${'}' * 64}';
    final json128 = '${'[' * 128}null${']' * 128}';
    expect(
      decoder.decode(
        '{"\$id":2,"data":{"metadata":$json128},"\$id":1}',
      )['data'],
      isA<Map>(),
    );
    expect(
      decoder.decode(
        '{"\$id":1,"data":${'[' * 129}0${']' * 129},"\$id":2}',
      )['data'],
      isNull,
    );
    for (final data in [
      '"data":$model65',
      '"data":$model65,"data":{}',
      '"data":{},"data":$model65',
    ]) {
      expect(
        () => decoder.decode('{"\$id":1,$data}'),
        throwsA(isA<JSONModelDepthError>()),
      );
    }
    expect(
      decoder.decode('{"data":$model65,"\$id":1,"error":"Custom"}')['data'],
      isA<Map>(),
    );
    for (final source in [
      '{"\$id":1,"data":$model65} x',
      '{"\$id":1,"data":${'{"child":' * 65}{"metadata":[1,]}${'}' * 65}}',
      '{"\$id":1,"data":$model65,"data":{"metadata":${'[' * 129}0${']' * 129}}}',
      '{"\$id":2,"data":[1,]}',
    ]) {
      expect(
        () => decoder.decode(source),
        throwsA(
          isA<FormatException>().having(
            (error) => error is JSONModelDepthError,
            'is model depth',
            isFalse,
          ),
        ),
      );
    }
    expect(resolutions, greaterThan(4));
  });
  test('typed envelope source slices preserve grammar and scalar tokens', () {
    const model = JSONResponseSchema(modelType: 'Node', types: {'Node': {}});
    for (final data in [
      'null',
      'true',
      '123',
      '-9007199254740991',
      '-0',
      '1e400',
      '"a\\\"b"',
      '[{"x":"[}\\\\"}]',
      '{"x":"\\uD83D\\uDE00","x":0}',
    ]) {
      final source = '{"id":1,"data":$data,"data":$data,"code":200}';
      expect(
        encodeLuxoJSON(decodeLuxoJSONEnvelope(source, response: model)),
        encodeLuxoJSON(decodeLuxoJSONEnvelope(source)),
      );
      for (var end = 0; end < source.length; end++) {
        expect(
          () =>
              decodeLuxoJSONEnvelope(source.substring(0, end), response: model),
          throwsFormatException,
          reason: source.substring(0, end),
        );
      }
      expect(
        () => decodeLuxoJSONEnvelope('$source trailing', response: model),
        throwsFormatException,
      );
    }
    for (final source in ['{}', '{"id":1}', '{"data":null}', '{"data":{}}']) {
      expect(
        decodeLuxoJSONEnvelope(source, response: model),
        decodeLuxoJSONEnvelope(source),
      );
    }
    expect(
      () => decodeLuxoJSON(
        '{}',
        response: const JSONResponseSchema(
          modelType: 'Node',
          types: {
            'Node': {'child': SelectionFieldSchema(1, 'Missing', 'Model')},
          },
        ),
      ),
      returnsNormally,
    );
    expect(
      () => decodeLuxoJSON(
        '{"child":{}}',
        response: const JSONResponseSchema(
          modelType: 'Node',
          types: {
            'Node': {'child': SelectionFieldSchema(1, 'Missing', 'Model')},
          },
        ),
      ),
      throwsFormatException,
    );
  });
  test('typed response separates JSON roots, object depth and list shells', () {
    const fields = <String, SelectionFieldSchema>{
      'child': SelectionFieldSchema(1, 'Node', 'Model'),
      'metadata': SelectionFieldSchema(2, null, 'JSON'),
      'documents': SelectionFieldSchema(3, null, 'JSON', true),
    };
    const model = JSONResponseSchema(
      modelType: 'Node',
      types: {'Node': fields},
    );
    for (final depth in [127, 128, 129]) {
      final json = '${'[' * depth}null${']' * depth}';
      for (final payload in ['{"metadata":$json}', '{"documents":[$json]}']) {
        if (depth <= 128) {
          expect(decodeLuxoJSON(payload, response: model), isA<Map>());
          expect(
            decodeLuxoJSONEnvelope(
              '{"data":$payload}',
              response: model,
            )['data'],
            isA<Map>(),
          );
        } else {
          expect(
            () => decodeLuxoJSON(payload, response: model),
            throwsFormatException,
          );
        }
      }
    }
    for (final depth in [63, 64, 65]) {
      final payload = '${'{"child":' * (depth - 1)}{}${'}' * (depth - 1)}';
      if (depth <= 64) {
        expect(decodeLuxoJSON(payload, response: model), isA<Map>());
      } else {
        expect(
          () => decodeLuxoJSON(payload, response: model),
          throwsFormatException,
        );
      }
    }
  });

  test(
    'typed envelope uses final error metadata and validates duplicate values',
    () {
      const model = JSONResponseSchema(
        modelType: 'Node',
        types: {
          'Node': {
            'child': SelectionFieldSchema(1, 'Node', 'Model'),
            'metadata': SelectionFieldSchema(2, null, 'JSON'),
          },
        },
      );
      final model65 = '${'{"child":' * 64}{}${'}' * 64}';
      final json128 = '${'[' * 128}null${']' * 128}';
      for (final source in [
        '{"data":$model65,"error":"Custom"}',
        '{"error":"Custom","data":$model65}',
      ]) {
        expect(
          decodeLuxoJSONEnvelope(source, response: model)['data'],
          isA<Map>(),
        );
      }
      expect(
        decodeLuxoJSONEnvelope(
          '{"data":$model65}',
          response: model,
          errorResponse: true,
        )['data'],
        isA<Map>(),
      );
      for (final source in [
        '{"data":$model65,"data":{}}',
        '{"data":{"metadata":$json128},"error":"Custom"}',
        '{"data":{"metadata":$json128},"error":"Custom","data":null}',
        '{"data":{"metadata":${'[' * 129}null${']' * 129}},"data":{}}',
        '{"data":[1,],"data":{}}',
        '{"data":{"child":{},},"data":{}}',
      ]) {
        expect(
          () => decodeLuxoJSONEnvelope(source, response: model),
          throwsFormatException,
          reason: source,
        );
      }
      for (final source in [
        '{"data":{},"data":{"metadata":$json128}}',
        '{"data":{"metadata":$json128},"data":{}}',
      ]) {
        expect(
          decodeLuxoJSONEnvelope(source, response: model)['data'],
          isA<Map>(),
        );
      }
    },
  );

  test('response collections count objects and JSON values independently', () {
    const types = <String, Map<String, SelectionFieldSchema>>{
      'Node': {
        'children': SelectionFieldSchema(1, 'Node', 'Model', true),
        'state': SelectionFieldSchema(2, 'State', 'Enum'),
      },
      'Empty': {},
    };
    final json128 = '${'[' * 128}null${']' * 128}';
    final schemas = [
      const JSONResponseSchema(modelType: 'Node', types: types),
      const JSONResponseSchema(modelType: 'Node', types: types, isList: true),
      const JSONResponseSchema(
        modelType: 'Node',
        types: types,
        paginated: true,
      ),
    ];
    for (final schema in schemas.skip(1)) {
      expect(decodeLuxoJSON('null', response: schema), isNull);
      expect(
        decodeLuxoJSONEnvelope('{"data":null}', response: schema)['data'],
        isNull,
      );
      expect(
        decodeLuxoJSONEnvelope(
          '{"data":[],"data":[]}',
          response: schema,
        )['data'],
        isEmpty,
      );
    }
    expect(
      decodeLuxoJSONEnvelope(
        '{"data":$json128}',
        response: const JSONResponseSchema(),
      )['data'],
      isA<List>(),
    );
    for (final schema in schemas.skip(1)) {
      final malformed = schema.paginated ? '{"items":[1 2]}' : '[1 2]';
      expect(
        () => decodeLuxoJSONEnvelope('{"data":$malformed}', response: schema),
        throwsFormatException,
      );
    }
    for (final depth in [64, 65]) {
      final node =
          '${'{"children":[' * (depth - 1)}{"state":"OPEN","unknown":$json128}${']}' * (depth - 1)}';
      for (final (index, schema) in schemas.indexed) {
        final payload = index == 0
            ? node
            : index == 1
            ? '[$node]'
            : '{"items":[$node],"total":1}';
        if (depth == 64) {
          expect(decodeLuxoJSON(payload, response: schema), isNotNull);
          expect(
            decodeLuxoJSONEnvelope(
              '{"data":$payload,"data":$payload}',
              response: schema,
            )['data'],
            isNotNull,
          );
        } else {
          expect(
            () => decodeLuxoJSON(payload, response: schema),
            throwsFormatException,
          );
        }
      }
    }
    expect(
      decodeLuxoJSON(
        '[$json128]',
        response: const JSONResponseSchema(isList: true),
      ),
      isA<List>(),
    );
    expect(
      decodeLuxoJSON(
        '{"items":[$json128]}',
        response: const JSONResponseSchema(paginated: true),
      ),
      isA<Map>(),
    );
    expect(
      decodeLuxoJSON(
        '{}',
        response: const JSONResponseSchema(modelType: 'Empty', types: types),
      ),
      isEmpty,
    );
    expect(
      () => decodeLuxoJSON(
        '{}',
        response: const JSONResponseSchema(modelType: 'Missing'),
      ),
      throwsFormatException,
    );
    for (final source in ['null', '1', '"text"', '[]']) {
      expect(
        decodeLuxoJSON(source, response: schemas.first),
        decodeLuxoJSON(source),
      );
    }
    for (final value in [
      'null',
      'true',
      '1.25',
      '"a\\\"b"',
      '"\\uD83D\\uDE00"',
      '[]',
      '{}',
    ]) {
      final source = '{"data":$value,"data":$value}';
      expect(
        decodeLuxoJSONEnvelope(
          source,
          response: schemas.first,
        )['data'].toString(),
        decodeLuxoJSON(value).toString(),
      );
    }
    for (final value in [
      '',
      '[}',
      '{]',
      '{"a"',
      '["unterminated',
      '["escape\\',
      '"escape\\',
      '[0',
      '{"a":tru}',
      '{"a":"\\uD800"}',
      '[,]',
      '[1 2]',
      '{"x":1 "y":2}',
      'true false',
    ]) {
      expect(
        () =>
            decodeLuxoJSONEnvelope('{"data":$value}', response: schemas.first),
        throwsFormatException,
        reason: value,
      );
    }
  });

  test('direct generic JSON rejects non-finite numbers at every position', () {
    for (final number in [
      double.nan,
      double.infinity,
      double.negativeInfinity,
    ]) {
      for (final value in <Object?>[
        number,
        [number],
        {'value': number},
      ]) {
        expect(() => normalizeLuxoJSON(value), throwsFormatException);
        expect(
          decodeJSONError({
            'error': 'Custom',
            'code': 400,
            'message': 'failed',
            'data': value,
          }, 400).error,
          'ParseError',
        );
      }
    }
    for (final number in [0.0, -0.0, 5e-324, 1.7976931348623157e308]) {
      expect(normalizeLuxoJSON(number), number);
    }
    final exact = LuxoJSONNumber.parse('1e400');
    expect(identical(normalizeLuxoJSON(exact), exact), isTrue);
  });
  test('direct JSON validation agrees with reader and writer at empty container boundaries', () {
    for (final depth in [127, 128, 129]) {
      for (final leaf in <Object?>[0, <Object?>[], <String, Object?>{}]) {
        for (final objects in [false, true]) {
          Object? value = leaf;
          for (var i = 0; i < depth; i++) {
            value = objects
                ? <String, Object?>{'next': value}
                : <Object?>[value];
          }
          String? encoded;
          try {
            encoded = encodeLuxoJSON(value);
          } on FormatException {
            // The existing encoder defines the accepted container boundary.
          }
          if (encoded == null) {
            expect(
              () => normalizeLuxoJSON(value),
              throwsFormatException,
              reason: 'depth=$depth objects=$objects leaf=$leaf',
            );
          } else {
            expect(identical(normalizeLuxoJSON(value), value), isTrue);
            expect(encodeLuxoJSON(decodeLuxoJSON(encoded)), encoded);
          }
        }
      }
    }
  });
  test('specialized envelopes preserve ordinary object grammar', () {
    for (final source in [
      '{}',
      ' { "data" : null } ',
      '{"data":[],"code":200}',
      '{"data":{"data":true}}',
      '{"data":1,"data":2}',
      '{"d\\u0061ta":false}',
    ]) {
      expect(
        encodeLuxoJSON(decodeLuxoJSONEnvelope(source)),
        encodeLuxoJSON(decodeLuxoJSON(source)),
      );
    }
    for (final source in [
      '{',
      '{,}',
      '{"data"}',
      '{"data":}',
      '{"data":0,}',
      '{"data":0 "code":1}',
      '{data:0}',
      '{"data":01}',
      '{"data":true}{}',
      '{"data": [1,]}',
    ]) {
      expect(() => decodeLuxoJSONEnvelope(source), throwsFormatException);
      expect(() => decodeLuxoJSON(source), throwsFormatException);
    }
  });
  test('envelope depth reset is restricted to the top-level data member', () {
    final payload = '${'[' * 128}0${']' * 128}';
    expect(
      encodeLuxoJSON(decodeLuxoJSONEnvelope('{"data":$payload}')['data']),
      payload,
    );
    for (final source in [
      '{"other":$payload}',
      '{"other":{"data":$payload}}',
      '{"data":{"data":$payload}}',
      '{"data":$payload} false',
      '[$payload]',
      'null',
      '',
    ]) {
      expect(() => decodeLuxoJSONEnvelope(source), throwsFormatException);
    }
    expect(() => decodeLuxoJSON('{"data":$payload}'), throwsFormatException);
    expect(decodeLuxoJSONEnvelope(' {"data":null,"id":9007199254740993} '), {
      'data': null,
      'id': BigInt.parse('9007199254740993'),
    });
  });
  test(
    'Binary JSON values preserve field text and reject document markers',
    () {
      for (final text in ['null', '42', '{}', '[]', '"\uFEFFvalue"']) {
        final bytes = utf8.encode(text);
        expect(encodeLuxoJSON(decodeLuxoJSONBytes(bytes)), text);
        expect(
          () => decodeLuxoJSONBytes([0xef, 0xbb, 0xbf, ...bytes]),
          throwsFormatException,
        );
      }
      for (final bytes in <List<int>>[
        [],
        [0xff],
        [0xef],
        [0xef, 0xbb],
        [0xef, 0xbb, 0xbf],
      ]) {
        expect(() => decodeLuxoJSONBytes(bytes), throwsFormatException);
      }
    },
  );

  test('Binary JSON error data rejects a document BOM', () {
    for (final text in ['null', '42', '{}', '[]', '"value"']) {
      final encoder = LuxoEncoder();
      encoder.writeFieldInt(1, LuxoInt64.fromInt(400));
      encoder.writeFieldString(2, 'Custom');
      encoder.writeFieldString(3, 'failed');
      encoder.writeFieldString(5, '\uFEFF$text');
      encoder.writeEnd();
      expect(decodeBinaryError(encoder.bytes(), 502).error, 'ParseError');
    }
  });

  test('prototype-named error data keys survive both encodings', () {
    const text =
        '{"__proto__":{"marker":"value"},"constructor":{"prototype":{"marker":"value"}},"hasOwnProperty":false,"toJSON":"literal","n":9007199254740993,"precise":1.2300}';
    final envelope = decodeLuxoJSON(
      '{"error":"Custom","code":400,"message":"failed","data":$text}',
    ) as Map<String, Object?>;
    final encoder = LuxoEncoder()
      ..writeFieldInt(1, LuxoInt64.fromInt(400))
      ..writeFieldString(2, 'Custom')
      ..writeFieldString(3, 'failed')
      ..writeFieldString(5, text)
      ..writeEnd();
    for (final error in [
      decodeJSONError(envelope, 502),
      decodeBinaryError(encoder.bytes(), 502),
    ]) {
      expect(error.error, 'Custom');
      final data = error.data as Map<String, Object?>;
      expect(data.containsKey('__proto__'), isTrue);
      expect(data['toJSON'], 'literal');
      expect(encodeLuxoJSON(data), text);
    }
  });

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
      '0,1',
      '0,"extra":true',
      '0,"value":1',
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

Iterable<String> _jsonStringSamples() sync* {
  yield '';
  yield '/<script>__proto__';
  yield 'ascii_' * 4096;
  yield '汉😀' * 4096;
  yield String.fromCharCodes([
    0xd7ff,
    0xe000,
    0xfffe,
    0xffff,
    0x10000,
    0x10ffff,
  ]);
  yield String.fromCharCodes([0x85, 0x2028, 0x2029, 0xfeff]);
  yield String.fromCharCodes([34, 92, 8, 9, 10, 12, 13, 0, 31]) * 1024;
  yield 'tail_${'x' * 32768}${String.fromCharCode(0)}';
  for (final length in [63, 64, 65]) yield 'k' * length;
  yield '${'k' * 63}"';
  yield '${'k' * 63}汉';
  yield '${'k' * 63}${String.fromCharCode(127)}';
  for (var code = 0; code < 256; code++) {
    yield String.fromCharCode(code);
    yield 'head${String.fromCharCode(code)}tail';
  }
  for (var count = 0; count <= 16; count++) {
    final slashes = String.fromCharCode(92) * count;
    yield '$slashes"';
    yield 'head${slashes}tail';
  }
  final random = Random(0x4a534f4e);
  for (var sample = 0; sample < 800; sample++) {
    final scalars = <int>[];
    for (var index = random.nextInt(64); index > 0; index--) {
      final rune = random.nextBool()
          ? random.nextInt(128)
          : random.nextInt(0x110000);
      scalars.add(rune >= 0xd800 && rune <= 0xdfff ? 0xfffd : rune);
    }
    yield String.fromCharCodes(scalars);
  }
}

class _ProjectedJSON implements LuxoJSONEncodable {
  final Object? value;
  int projections = 0;
  _ProjectedJSON(this.value);

  @override
  Map<String, Object?> toJson() {
    projections++;
    return {'value': value};
  }
}

class _StaticJSON extends _ProjectedJSON implements LuxoJSONWritable {
  int writes = 0;
  _StaticJSON(super.value);

  @override
  void $writeJSON(LuxoJSONOutput output) {
    writes++;
    output.writeRaw('{"value":');
    output.writeJSON(value);
    output.writeRaw('}');
  }
}
