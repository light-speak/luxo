import 'dart:math';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:luxo_client/luxo_client.dart';
import 'package:luxo_client/src/json.dart' show JSONRequestEncoder;
import 'package:test/test.dart';

void main() {
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

  test('recursive values and deep empty objects and lists are bounded', () {
    final encoder = JSONRequestEncoder()..setSchema({'echo': _recursiveSchema});
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
      _invalid('nesting exceeds 128 levels'),
    );
    for (var depth = 120; depth <= 130; depth++) {
      for (final leaf in [
        <String, Object?>{},
        <String, Object?>{'children': <Object?>[]},
      ]) {
        var value = leaf;
        for (var i = 0; i < depth; i++) {
          value = {'next': value};
        }
        final params = {'value': value};
        if (depth <= (leaf.isEmpty ? 126 : 125)) {
          expect(
            encoder.encode('echo', params),
            encodeLuxoJSON({r'$api': 'echo', ...params}),
          );
        } else {
          expect(
            () => encoder.encode('echo', params),
            _invalid('nesting exceeds 128 levels'),
          );
        }
      }
    }
  });

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
