import 'dart:convert';
import 'dart:math';
import 'dart:typed_data';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:luxo_client/luxo_client.dart';
import 'package:luxo_client/src/json.dart'
    show JSONRequestEncoder, LuxoSealedJSONWritable, LuxoJSONOutput;
import 'package:test/test.dart';

void main() {
  test(
    'sealed JSON map inputs preserve strict payloads, depth and caller values',
    () {
      const types = {
        '@sealed:Outcome': {
          'Done': SelectionFieldSchema(1, 'Outcome.Done', 'Model'),
          'Next': SelectionFieldSchema(2, 'Outcome.Next', 'Model'),
        },
        'Outcome.Done': {'child': SelectionFieldSchema(1, 'Child', 'Model')},
        'Outcome.Next': {
          'child': SelectionFieldSchema(1, 'Outcome', 'Sealed', false, true),
        },
        'Child': {'value': SelectionFieldSchema(1, null, 'Int')},
      };
      final writer = JSONRequestEncoder({
        'echo': const APISchemaEntry(
          1,
          [ParamSchema(1, 'value', 'Sealed', false, false, 'Outcome')],
          {},
          types,
        ),
      });
      final child = {'value': LuxoInt64.fromInt(7)};
      final input = {
        'Done': {'child': child},
      };
      expect(jsonDecode(writer.encode('echo', {'value': input})), {
        r'$api': 'echo',
        'value': {
          'Done': {
            'child': {'value': 7},
          },
        },
      });
      expect(child['value'], LuxoInt64.fromInt(7));
      expect(
        writer.encode('echo', {
          'value': {'Done': _SealedPayload(child)},
        }),
        contains('"value":7'),
      );
      for (final value in [
        null,
        {},
        {'Done': {}, 'Next': {}},
        {'Future': {}},
        {
          'Done': {'extra': 1},
        },
      ]) {
        expect(
          () => writer.encode('echo', {'value': value}),
          throwsA(isA<LuxoError>()),
        );
      }
      expect(
        () => writer.encode('echo', {'value': const _DifferentSealedOwner()}),
        throwsA(
          isA<LuxoError>().having(
            (e) => e.message,
            'message',
            contains('wrong sealed owner'),
          ),
        ),
      );
      Object nested = {
        'Next': {'child': null},
      };
      for (var i = 1; i < 32; i++) {
        nested = {
          'Next': {'child': nested},
        };
      }
      expect(() => writer.encode('echo', {'value': nested}), returnsNormally);
      nested = {
        'Next': {'child': nested},
      };
      expect(
        () => writer.encode('echo', {'value': nested}),
        throwsA(isA<LuxoError>()),
      );
      const response = JSONResponseSchema(
        modelType: '@sealed:Outcome',
        types: types,
      );
      expect(
        () => decodeLuxoJSON(jsonEncode(nested), response: response),
        throwsFormatException,
      );
      for (final raw in [
        '42',
        '[]',
        '{',
        '{"Done"}',
        '{"Done":null}',
        '{"Done":{}',
      ]) {
        expect(
          () => decodeLuxoJSON(raw, response: response),
          throwsFormatException,
          reason: raw,
        );
      }
      expect(
        () => decodeLuxoJSON('{"Future":{}}', response: response),
        throwsFormatException,
      );
      expect(
        () => decodeLuxoJSON('{"Done":{"extra":1}}', response: response),
        throwsFormatException,
      );
      expect(
        () => decodeLuxoJSON(
          '{"Done":{}}',
          response: const JSONResponseSchema(modelType: '@sealed:Missing'),
        ),
        throwsFormatException,
      );
    },
  );
  group('JSON Bytes parameters', () {
    test(
      'preserves scalar/list and nullable contracts across schema plans',
      () {
        final bytes = Uint8List.fromList([0, 255]);
        for (final list in [false, true]) {
          for (final nullable in [false, true]) {
            final writer = JSONRequestEncoder({
              'bytes': APISchemaEntry(7, [
                ParamSchema(1, 'value', 'Bytes', list, nullable),
              ]),
            });
            expect(
              decodeLuxoJSON(
                writer.encode('bytes', {
                  'value': list ? [bytes] : bytes,
                }),
              ),
              {
                r'$api': 'bytes',
                'value': list ? ['AP8='] : 'AP8=',
              },
            );
            if (nullable) {
              expect(decodeLuxoJSON(writer.encode('bytes', {'value': null})), {
                r'$api': 'bytes',
                'value': null,
              });
            } else {
              expect(
                () => writer.encode('bytes', {'value': null}),
                throwsA(
                  isA<LuxoError>().having(
                    (error) => error.error,
                    'error',
                    'ConfigError',
                  ),
                ),
              );
            }
            if (list) {
              expect(
                () => writer.encode('bytes', {
                  'value': [null],
                }),
                _exactParameterFailure(
                  'value[0]',
                  'must be Uint8List or a Base64 string',
                ),
              );
            }
          }
        }
      },
    );

    final schema = {
      'bytes': APISchemaEntry(
        7,
        [
          const ParamSchema(1, 'value', 'Bytes'),
          const ParamSchema(2, 'values', 'Bytes', true, true),
          const ParamSchema(3, 'node', 'Node'),
          const ParamSchema(4, 'json', 'JSON'),
        ],
        const {},
        {
          'Node': {
            'value': SelectionFieldSchema(1, null, 'Bytes'),
            'values': SelectionFieldSchema(2, null, 'Bytes', true, true),
          },
        },
      ),
    };
    test('writes Base64 for scalar, list and nested values without changing generic JSON', () {
      final bytes = Uint8List.fromList([0, 127, 128, 255]);
      final writer = JSONRequestEncoder(schema);
      expect(
        decodeLuxoJSON(
          writer.encode('bytes', {
            'value': bytes,
            'values': [bytes, Uint8List(0)],
            'node': {
              'value': bytes,
              'values': [Uint8List(0), bytes],
            },
            'json': bytes,
          }),
        ),
        {
          r'$api': 'bytes',
          'value': 'AH+A/w==',
          'values': ['AH+A/w==', ''],
          'node': {
            'value': 'AH+A/w==',
            'values': ['', 'AH+A/w=='],
          },
          'json': [0, 127, 128, 255],
        },
      );
    });
    test(
      'preserves byte-view bounds, null lists and existing encoded text',
      () {
        final bytes = Uint8List.fromList([99, 0, 255, 88]);
        final view = Uint8List.sublistView(bytes, 1, 3);
        final writer = JSONRequestEncoder(schema);
        expect(
          decodeLuxoJSON(
            writer.encode('bytes', {
              'value': view,
              'values': null,
              'node': {'value': 'AP8=', 'values': null},
            }),
          ),
          {
            r'$api': 'bytes',
            'value': 'AP8=',
            'values': null,
            'node': {'value': 'AP8=', 'values': null},
          },
        );
        expect(bytes, [99, 0, 255, 88]);
        expect(writer.encode('bytes', const {}), '{"\$api":"bytes"}');
      },
    );
    test('rejects unsupported Bytes values with their full parameter path', () {
      final writer = JSONRequestEncoder(schema);
      for (final value in [
        null,
        42,
        true,
        <int>[0, 255],
        {},
      ]) {
        expect(
          () => writer.encode('bytes', {'value': value}),
          _exactParameterFailure(
            'value',
            'must be Uint8List or a Base64 string',
          ),
        );
      }
      expect(
        () => writer.encode('bytes', {
          'values': [Uint8List(0), null],
        }),
        _exactParameterFailure(
          'values[1]',
          'must be Uint8List or a Base64 string',
        ),
      );
      expect(
        () => writer.encode('bytes', {
          'node': {'value': false},
        }),
        _exactParameterFailure(
          'node.value',
          'must be Uint8List or a Base64 string',
        ),
      );
    });
  });

  test('JSON parameter failures retain complete paths and recover', () {
    final cases = <(List<(String, bool)>, String)>[
      ([('root', false)], 'root'),
      ([('', false)], ''),
      ([('root', true)], 'root[11]'),
      ([('', true)], '[11]'),
      ([('root', false), ('leaf', false)], 'root.leaf'),
      ([('root', false), ('', false)], 'root'),
      ([('', false), ('leaf', false)], '.leaf'),
      ([('', false), ('', false), ('leaf', false)], '..leaf'),
      ([('', false), ('', false), ('', false)], ''),
      ([('root', false), ('', false), ('leaf', false)], 'root..leaf'),
      ([('root', false), ('[x]', false)], 'root[x]'),
      ([('root', false), ('', false), ('[x]', false)], 'root[x]'),
      ([('root', false), ('[x]', false), ('leaf', false)], 'root[x].leaf'),
      ([('root', true), ('', false), ('leaf', true)], 'root[11]..leaf[11]'),
      ([('', true), ('', true), ('', true)], '[11][11][11]'),
      ([('字段😀', false), ('\u0000"\\.', false)], '字段😀.\u0000"\\.'),
    ];
    cases.add((List.filled(17, ('', false)), ''));
    cases.add((List.filled(17, ('', true)), List.filled(17, '[11]').join()));
    cases.add((List.filled(65, ('', false)), ''));
    cases.add((List.filled(65, ('', true)), List.filled(65, '[11]').join()));
    final bracket = '[${'x' * 64}]';
    cases.add((
      [('root', false), ...List.filled(64, (bracket, false))],
      'root${List.filled(64, bracket).join()}',
    ));
    final longName = '字段${'λ' * 128}';
    cases.add((
      [...List.filled(17, ('', false)), (longName, false), ('id', false)],
      '${'.' * 17}$longName.id',
    ));
    for (final (fields, path) in cases) {
      final (writer, params) = _pathRequest(fields);
      expect(
        () => writer.encode('paths', params),
        _exactParameterFailure(path, 'must be LuxoInt64'),
        reason: fields.toString(),
      );
      expect(writer.encode('paths', const {}), '{"\$api":"paths"}');
    }
  });

  test('JSON parameter failures retain deep object and list paths', () {
    for (final length in [1, 2, 8, 9, 16, 17, 64, 65]) {
      for (final list in [false, true]) {
        for (final name in ['node', '字段😀${'λ' * 64}']) {
          final fields = List.filled(length, (name, list));
          final (writer, params) = _pathRequest(fields);
          final path = List.filled(
            length,
            '$name${list ? '[11]' : ''}',
          ).join('.');
          expect(
            () => writer.encode('paths', params),
            _exactParameterFailure(path, 'must be LuxoInt64'),
          );
          expect(writer.encode('paths', const {}), '{"\$api":"paths"}');
        }
      }
    }
  });

  test('JSON parameter failures preserve path promotion boundaries', () {
    for (final remaining in [7, 8, 9]) {
      for (final length in [127, 128, 129]) {
        final fields = [
          ...List.filled(remaining + 1, ('node', false)),
          ('x' * (length - 3), false),
          ('id', false),
        ];
        final (writer, params) = _pathRequest(fields);
        expect(
          () => writer.encode('paths', params),
          _exactParameterFailure(
            fields.map((e) => e.$1).join('.'),
            'must be LuxoInt64',
          ),
        );
        expect(writer.encode('paths', const {}), '{"\$api":"paths"}');
      }
    }
  });

  test('JSON request keys preserve quoting through root and model plans', () {
    for (final key in [
      '',
      'plain',
      'k' * 63,
      'k' * 64,
      'k' * 65,
      '字段😀',
      'quoted"',
      'slash${String.fromCharCode(92)}',
      'control${String.fromCharCode(0)}',
      'long_${'x' * 4096}',
    ]) {
      expect(
        JSONRequestEncoder().encode('keys', {key: 'value'}),
        jsonEncode({r'$api': 'keys', key: 'value'}),
      );
      final writer = JSONRequestEncoder({
        'keys': APISchemaEntry(
          7,
          [ParamSchema(1, 'input', 'Model', false, false, 'Payload')],
          const {},
          {
            'Payload': {key: const SelectionFieldSchema(1, null, 'Int')},
          },
        ),
      });
      expect(
        writer.encode('keys', {
          'input': {key: LuxoInt64.fromInt(7)},
        }),
        jsonEncode({
          r'$api': 'keys',
          'input': {key: 7},
        }),
      );
    }
  });
  test('request model64 and each JSON128 retain independent budgets', () {
    const types = {
      'Node': {
        'child': SelectionFieldSchema(1, 'Node', 'Model', false, true),
        'children': SelectionFieldSchema(2, 'Node', 'Model', true, true),
        'metadata': SelectionFieldSchema(3, null, 'JSON'),
        'jsons': SelectionFieldSchema(4, null, 'JSON', true),
      },
    };
    final writer = JSONRequestEncoder()
      ..setSchema({
        'echo': APISchemaEntry(
          7,
          [
            ParamSchema(1, 'value', 'Model', false, false, 'Node'),
            ParamSchema(2, 'values', 'Model', true, false, 'Node'),
            ParamSchema(3, 'raw', 'JSON'),
            ParamSchema(4, 'raws', 'JSON', true),
          ],
          const {},
          types,
        ),
      });
    Object? json(int layers) {
      Object? value;
      for (var i = 0; i < layers; i++) value = [value];
      return value;
    }

    Map<String, Object?> node(int layers, bool lists, {int jsonDepth = 128}) {
      var value = <String, Object?>{
        'metadata': json(jsonDepth),
        'jsons': [json(jsonDepth)],
        'children': [],
      };
      for (var i = 1; i < layers; i++)
        value = lists
            ? {
                'children': [value],
              }
            : {'child': value};
      return value;
    }

    for (final lists in [false, true]) {
      for (final layers in [63, 64]) {
        expect(
          () => writer.encode('echo', {
            'value': node(layers, lists),
            'values': [node(layers, lists), node(layers, lists)],
          }),
          returnsNormally,
        );
      }
      expect(
        () => writer.encode('echo', {'value': node(65, lists)}),
        _invalid('value'),
      );
      expect(
        () => writer.encode('echo', {
          'values': [node(65, lists)],
        }),
        _invalid('values[0]'),
      );
      expect(
        () => writer.encode('echo', {'value': node(64, lists, jsonDepth: 129)}),
        throwsFormatException,
      );
    }
    expect(
      () => writer.encode('echo', {
        'raw': json(128),
        'raws': [json(128), json(128)],
      }),
      returnsNormally,
    );
    expect(
      () => writer.encode('echo', {'raw': json(129)}),
      throwsFormatException,
    );
    expect(
      () => writer.encode('echo', {
        'raws': [json(129)],
      }),
      throwsFormatException,
    );
    final cycle = <String, Object?>{};
    cycle['child'] = cycle;
    expect(() => writer.encode('echo', {'value': cycle}), _invalid('value'));
    expect(
      writer.encode('echo', {'value': <String, Object?>{}}),
      contains('"value":{}'),
    );
  });

  test('JSON request writing preserves host Float signs and extremes', () {
    final writer = JSONRequestEncoder()
      ..setSchema({
        'echo': APISchemaEntry(7, [
          ParamSchema(1, 'scalar', 'Float'),
          ParamSchema(2, 'items', 'Float', true),
          ParamSchema(3, 'raw', 'JSON'),
        ]),
      });
    for (final value in [0.0, -0.0, 5e-324, -5e-324, double.maxFinite]) {
      final text = writer.encode('echo', {
        'scalar': value,
        'items': [value],
        'raw': {'nested': value},
      });
      final decoded = decodeLuxoJSON(text) as Map<String, Object?>;
      for (final encoded in [
        decoded['scalar'],
        (decoded['items'] as List).single,
        (decoded['raw'] as Map)['nested'],
        decodeLuxoJSON(encodeLuxoJSON(value)),
      ]) {
        final actual = luxoDoubleFromJSON(encoded);
        expect(actual, value);
        expect(actual.isNegative, value.isNegative, reason: text);
      }
    }
  });

  test('DateTime input failures preserve parameter and list paths', () {
    for (final list in [false, true]) {
      final writer = JSONRequestEncoder()
        ..setSchema({
          'echo': APISchemaEntry(7, [ParamSchema(1, 'at', 'DateTime', list)]),
        });
      for (final invalid in <Object?>[
        42,
        null,
        true,
        'invalid',
        '2026-01-01T00:00:00.1234567890Z',
      ]) {
        expect(
          () => writer.encode('echo', {
            'at': list ? [invalid] : invalid,
          }),
          _invalid(list ? 'at[0]' : 'at'),
        );
      }
      const valid = '2026-01-01T00:00:00.123456789+01:00';
      expect(
        writer.encode('echo', {
          'at': list ? [valid] : valid,
        }),
        contains(valid),
      );
    }
  });
  test('typed JSON projection preserves integers without a generic JSON conversion', () {
    final writer = JSONRequestEncoder()..setSchema({'echo': _recursiveSchema});
    final input = _TypedInput({'id': LuxoInt64.maxValue});
    expect(
      writer.encode('echo', {'value': input}),
      '{"\$api":"echo","value":{"id":9223372036854775807}}',
    );
    expect(input.calls, 1);
    expect(
      () => writer.encode('echo', {
        'value': _TypedInput({'id': true}),
      }),
      _invalid('value.id'),
    );
  });
  test(
    'JSON rejects the same invalid schema integers as Binary before sending',
    () async {
      var sent = 0;
      final transport = HttpTransport(
        'http://localhost/luvia',
        client: MockClient((request) async {
          sent++;
          return http.Response('{"data":true}', 200);
        }),
      );
      addTearDown(transport.close);
      for (final type in ['Int', 'Duration']) {
        for (final list in [false, true]) {
          final param = ParamSchema(1, 'value', type, list);
          transport.setSchema({
            'echo': APISchemaEntry(7, [param]),
          });
          for (final invalid in <Object?>[
            1.0,
            1.5,
            true,
            '1',
            null,
            <Object?>[],
            <String, Object?>{},
            BigInt.one,
          ]) {
            final value = list ? [invalid] : invalid;
            expect(
              () => encodeParam(LuxoEncoder(), param, value),
              throwsA(anything),
            );
            await expectLater(
              transport.call<Object>('echo', params: {'value': value}),
              _invalid('value'),
            );
          }
        }
      }
      expect(sent, 0);
    },
  );

  test('JSON validates nested supplied integer fields', () async {
    var sent = 0;
    final transport = HttpTransport(
      'http://localhost/luvia',
      client: MockClient((request) async {
        sent++;
        return http.Response('{"data":true}', 200);
      }),
    );
    addTearDown(transport.close);
    transport.setSchema({'echo': _recursiveSchema});
    await expectLater(
      transport.call<Object>(
        'echo',
        params: {
          'value': {
            'children': [
              {},
              {
                'next': {'id': true},
              },
            ],
          },
        },
      ),
      _invalid('value.children[1].next.id'),
    );
    expect(sent, 0);
  });

  test(
    'signed boundaries stay unquoted for scalar and nullable list parameters',
    () {
      for (final type in ['Int', 'Duration']) {
        final encoder = JSONRequestEncoder()
          ..setSchema({
            'echo': APISchemaEntry(7, [
              ParamSchema(1, 'value', type),
              ParamSchema(2, 'list', type, true, true),
            ]),
          });
        for (final value in [
          LuxoInt64.minValue,
          LuxoInt64.zero,
          LuxoInt64.parse('9007199254740993'),
          LuxoInt64.maxValue,
        ]) {
          expect(
            encoder.encode('echo', {
              'value': value,
              'list': [value],
            }),
            '{"\$api":"echo","value":$value,"list":[$value]}',
          );
        }
        expect(
          encoder.encode('echo', {'list': null}),
          '{"\$api":"echo","list":null}',
        );
        expect(
          encoder.encode('echo', {'list': <Object?>[]}),
          '{"\$api":"echo","list":[]}',
        );
        expect(
          () => encoder.encode('echo', {'list': LuxoInt64.zero}),
          _invalid('list'),
        );
        expect(
          () => encoder.encode('echo', {
            'list': [LuxoInt64.zero, null],
          }),
          _invalid('list[1]'),
        );
        expect(encoder.encode('echo', null), '{"\$api":"echo"}');
      }
    },
  );

  test(
    'recursive input, opaque JSON and unknown fields keep their distinct types',
    () {
      final value = <String, Object?>{
        'id': LuxoInt64.maxValue,
        'next': {'id': LuxoInt64.minValue, 'next': null},
        'children': <Object?>[],
        'metadata': {'id': 1.5},
        'ratio': 1.5,
        'unknown': true,
      };
      final encoder = JSONRequestEncoder()
        ..setSchema({'echo': _recursiveSchema});
      expect(
        encoder.encode('echo', {'value': value}),
        encodeLuxoJSON({r'$api': 'echo', 'value': value}),
      );
      for (final invalid in [null, true, 1.5, 'object', <Object?>[]]) {
        expect(
          () => encoder.encode('echo', {'value': invalid}),
          _invalid('value'),
        );
      }
      expect(
        () => encoder.encode('echo', {
          'value': {
            'children': [null],
          },
        }),
        _invalid('value.children[0]'),
      );
      expect(
        () => encoder.encode('echo', {
          'value': {1: 'invalid'},
        }),
        _invalid('value'),
      );
      expect(
        () => encoder.encode('echo', {
          'value': {'ratio': double.infinity},
        }),
        throwsFormatException,
      );
    },
  );

  test('missing field metadata fails only for supplied typed fields', () {
    for (final name in [null, 'Missing']) {
      final encoder = JSONRequestEncoder()
        ..setSchema({
          'echo': APISchemaEntry(1, [
            ParamSchema(1, 'value', 'Model', false, true, name),
          ]),
        });
      expect(encoder.encode('echo', {'value': null}), contains('"value":null'));
      expect(() => encoder.encode('echo', {'value': {}}), _invalid('value'));
    }
    final encoder = JSONRequestEncoder()
      ..setSchema({
        'echo': const APISchemaEntry(
          1,
          [ParamSchema(1, 'value', 'Model', false, false, 'Partial')],
          {},
          {
            'Partial': {
              'unknown': SelectionFieldSchema(1),
              'child': SelectionFieldSchema(2, 'Missing'),
            },
          },
        ),
      });
    expect(
      () => encoder.encode('echo', {
        'value': {'unknown': LuxoInt64.zero},
      }),
      _invalid('value.unknown'),
    );
    expect(
      () => encoder.encode('echo', {
        'value': {'child': {}},
      }),
      _invalid('value.child'),
    );
    expect(encoder.encode('echo', {'value': {}}), contains('"value":{}'));
  });

  test('named models and shared recursive type maps compile without eager recursion', () {
    final encoder = JSONRequestEncoder()
      ..setSchema({
        'echo': APISchemaEntry(
          1,
          const [ParamSchema(1, 'value', 'Input')],
          const {},
          _recursiveSchema.types,
        ),
        'other': _recursiveSchema,
        'empty': const APISchemaEntry(2),
        'floating': const APISchemaEntry(3, [ParamSchema(1, 'value', 'Float')]),
      });
    for (final api in ['echo', 'other']) {
      expect(encoder.encode(api, {'value': {}}), contains('"value":{}'));
      expect(
        () => encoder.encode(api, {
          'value': {'id': false},
        }),
        _invalid('value.id'),
      );
    }
    expect(encoder.encode('floating', {'value': 1.5}), contains('1.5'));
    expect(encoder.encode('empty', {}), '{"\$api":"empty"}');
  });

  test('input projections run once and their integer fields are validated', () {
    final encoder = JSONRequestEncoder()..setSchema({'echo': _recursiveSchema});
    final input = _Input({
      'id': LuxoInt64.maxValue,
      'next': _Input({'id': LuxoInt64.minValue}),
    });
    expect(
      encoder.encode('echo', {'value': input}),
      contains('9223372036854775807'),
    );
    expect(input.calls, 1);
    expect((input.value['next'] as _Input).calls, 1);
    final invalid = _Input({'id': true});
    expect(
      () => encoder.encode('echo', {'value': invalid}),
      _invalid('value.id'),
    );
    expect(invalid.calls, 1);
  });

  test(
    'recursive models use 64 object layers without counting empty lists',
    () {
      final encoder = JSONRequestEncoder()
        ..setSchema({'echo': _recursiveSchema});
      final cycle = <String, Object?>{};
      cycle['next'] = cycle;
      expect(
        () => encoder.encode('echo', {'value': cycle}),
        _invalid('value.next'),
      );
      final input = _Input({});
      input.value['next'] = input;
      expect(
        () => encoder.encode('echo', {'value': input}),
        _invalid('value.next'),
      );
      Object? projected = _Input({});
      for (var i = 0; i < 127; i++) {
        projected = {'next': projected};
      }
      expect(
        () => encoder.encode('echo', {'value': projected}),
        _invalid('nesting exceeds 64 levels'),
      );
      for (var depth = 60; depth <= 66; depth++) {
        for (final leaf in [
          <String, Object?>{},
          <String, Object?>{'children': <Object?>[]},
        ]) {
          var value = leaf;
          for (var i = 0; i < depth; i++) {
            value = {'next': value};
          }
          final params = {'value': value};
          if (depth <= 63) {
            expect(
              encoder.encode('echo', params),
              encodeLuxoJSON({r'$api': 'echo', ...params}),
            );
          } else {
            expect(
              () => encoder.encode('echo', params),
              _invalid('nesting exceeds 64 levels'),
            );
          }
        }
      }
    },
  );

  test(
    'request metadata cannot be overridden or confused with signed parameters',
    () {
      final encoder = JSONRequestEncoder();
      final params = <String, Object?>{
        r'$api': double.nan,
        r'$id': double.infinity,
        r'$sub': double.negativeInfinity,
        r'$select': 'id',
      };
      for (final subscription in [false, true]) {
        final wire = encoder.encode('echo', params, subscription: subscription);
        expect(decodeLuxoJSON(wire), {
          subscription ? r'$sub' : r'$api': 'echo',
          r'$select': 'id',
        });
      }
      final maxID = BigInt.parse('18446744073709551615');
      expect(
        encoder.encode('echo', params, sequence: maxID),
        contains('"\$id":18446744073709551615'),
      );
      expect(
        encoder.encode('echo', {'value': LuxoInt64.minValue}, sequence: 0),
        contains('"\$id":0'),
      );
      expect(
        () => encoder.encode('echo', {'value': double.nan}),
        throwsFormatException,
      );
    },
  );

  test('random valid nested requests match the existing writer exactly', () {
    final random = Random(64);
    final encoder = JSONRequestEncoder()..setSchema({'echo': _recursiveSchema});
    for (var i = 0; i < 200; i++) {
      final params = {
        'value': {
          'id': LuxoInt64.fromInt(random.nextInt(1000000)),
          'children': List.generate(
            random.nextInt(10),
            (_) => {
              'id': LuxoInt64.maxValue,
              'ratio': random.nextDouble(),
              'next': null,
              'unknown "\n': 'escape " \\ \t\n\r Unicode 汉',
            },
          ),
        },
      };
      expect(
        encoder.encode('echo', params),
        encodeLuxoJSON({r'$api': 'echo', ...params}),
      );
    }
  });

  test(
    'schema registration snapshots mutable metadata for JSON and Binary',
    () async {
      var sent = 0;
      final transport = HttpTransport(
        'http://localhost/luvia',
        client: MockClient((request) async {
          sent++;
          return http.Response('{"data":true}', 200);
        }),
      );
      addTearDown(transport.close);
      final params = [const ParamSchema(1, 'value', 'Int')];
      final schema = {'echo': APISchemaEntry(1, params)};
      transport.setSchema(schema);
      params[0] = const ParamSchema(1, 'value', 'Float');
      for (final mode in TransportMode.values) {
        transport.setMode(mode);
        await expectLater(
          transport.call<Object>('echo', params: {'value': 1.5}),
          throwsA(anything),
        );
      }
      expect(sent, 0);
      transport.setMode(TransportMode.json);
      transport.setSchema(schema);
      expect(
        await transport.call<bool>('echo', params: {'value': 1.5}),
        isTrue,
      );
      transport.setSchema({});
      expect(
        await transport.call<bool>('echo', params: {'value': true}),
        isTrue,
      );
      expect(sent, 2);
    },
  );

  test(
    'HTTP full-width JSON and local errors retain their actual meaning',
    () async {
      final transport = HttpTransport(
        'http://localhost/luvia',
        client: MockClient((request) async {
          expect(request.body, '{"\$api":"echo","value":9223372036854775807}');
          return http.Response('{"data":9223372036854775807}', 200);
        }),
      );
      addTearDown(transport.close);
      transport.setSchema({
        'echo': const APISchemaEntry(1, [ParamSchema(1, 'value', 'Int')]),
      });
      expect(
        await transport.call<LuxoInt64>(
          'echo',
          params: {'value': LuxoInt64.maxValue, r'$api': 'wrong'},
          decoder: luxoInt64FromJSON,
        ),
        LuxoInt64.maxValue,
      );
      await expectLater(
        transport.call<Object>('echo', params: {'opaque': double.infinity}),
        throwsFormatException,
      );
    },
  );
}

(JSONRequestEncoder, Map<String, Object?>) _pathRequest(
  List<(String, bool)> fields,
) {
  final types = <String, Map<String, SelectionFieldSchema>>{};
  Object? value = true;
  for (var i = fields.length - 1; i >= 0; i--) {
    final (name, list) = fields[i];
    final leaf = i == fields.length - 1;
    if (list) {
      value = [
        ...List<Object?>.generate(11, (_) => leaf ? LuxoInt64.zero : {}),
        value,
      ];
    }
    if (i > 0) {
      types['Path${i - 1}'] = {
        name: SelectionFieldSchema(
          1,
          leaf ? null : 'Path$i',
          leaf ? 'Int' : 'Model',
          list,
        ),
      };
      value = <String, Object?>{name: value};
    }
  }
  final (name, list) = fields.first;
  final writer = JSONRequestEncoder({
    'paths': APISchemaEntry(
      7,
      [
        ParamSchema(
          1,
          name,
          fields.length == 1 ? 'Int' : 'Model',
          list,
          false,
          fields.length == 1 ? null : 'Path0',
        ),
      ],
      const {},
      types,
    ),
  });
  return (writer, {name: value});
}

Matcher _exactParameterFailure(String path, String reason) => throwsA(
  isA<LuxoError>()
      .having((e) => e.error, 'error', 'ConfigError')
      .having((e) => e.code, 'code', 0)
      .having(
        (e) => e.message,
        'message',
        'invalid JSON parameter $path: $reason',
      ),
);

class _Input implements LuxoJSONEncodable {
  final Map<String, Object?> value;
  var calls = 0;
  _Input(this.value);
  @override
  Map<String, Object?> toJson() {
    calls++;
    return value;
  }
}

final class _TypedInput extends _Input implements LuxoJSONParameters {
  _TypedInput(super.value);
  @override
  Map<String, Object?> toLuxoParameters() {
    calls++;
    return value;
  }

  @override
  Map<String, Object?> toJson() =>
      throw StateError('must not convert typed parameters to generic JSON');
}

const _recursiveSchema = APISchemaEntry(
  7,
  [ParamSchema(1, 'value', 'Model', false, false, 'Input')],
  {},
  {
    'Input': {
      'id': SelectionFieldSchema(1, null, 'Int'),
      'next': SelectionFieldSchema(2, 'Input', 'Model', false, true),
      'children': SelectionFieldSchema(3, 'Input', 'Model', true),
      'metadata': SelectionFieldSchema(4, null, 'JSON'),
      'ratio': SelectionFieldSchema(5, null, 'Float'),
    },
  },
);

Matcher _invalid(String path) => throwsA(
  isA<LuxoError>()
      .having((e) => e.error, 'error', 'ConfigError')
      .having((e) => e.message, 'path', contains(path)),
);

final class _SealedPayload implements LuxoJSONEncodable {
  final Map<String, Object?> child;
  const _SealedPayload(this.child);
  @override
  Map<String, Object?> toJson() => {'child': child};
}

final class _DifferentSealedOwner implements LuxoSealedJSONWritable {
  const _DifferentSealedOwner();
  @override
  String get $sealedType => 'Other';
  @override
  Map<String, Object?> toJson() =>
      throw StateError('wrong owner was projected');
  @override
  void $writeJSON(LuxoJSONOutput output) =>
      throw StateError('wrong owner was written');
  @override
  void $writeSealedJSON(LuxoJSONOutput output, int depth) =>
      throw StateError('wrong owner was written');
}
