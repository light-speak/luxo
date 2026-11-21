import 'dart:convert';

import 'package:luxo_client/src/codec.dart';
import 'package:luxo_client/src/json.dart';
import 'package:luxo_client/src/transport.dart';
import 'package:test/test.dart';

void main() {
  for (final value in <Object>[
    {
      'name': '中文',
      'nested': [true, null, 42],
    },
    List.generate(128, (i) => {'value': i}),
    {'text': '中文' * 12000},
  ]) {
    test(
      'JSON Binary scalar and list preserve exact bytes ${value.runtimeType}',
      () {
        final scalar = LuxoEncoder();
        encodeParam(scalar, const ParamSchema(7, 'data', 'JSON'), value);
        final expected = LuxoEncoder()
          ..writeVarint(7)
          ..writeBytes(utf8.encode(encodeLuxoJSON(value)));
        expect(scalar.bytes(), expected.bytes());
        final list = LuxoEncoder();
        encodeParam(list, const ParamSchema(8, 'data', 'JSON', true), [
          value,
          null,
        ]);
        final expectedList = LuxoEncoder()
          ..writeVarint(8)
          ..writeVarint(2)
          ..writeBytes(utf8.encode(encodeLuxoJSON(value)))
          ..writeBytes(utf8.encode('null'));
        expect(list.bytes(), expectedList.bytes());
      },
    );
  }
  test('JSON encoding keeps an independent snapshot of mutable input', () {
    final value = {
      'items': [1, 2],
    };
    final encoder = LuxoEncoder();
    encodeParam(encoder, const ParamSchema(1, 'data', 'JSON'), value);
    final snapshot = encoder.bytes().toList();
    value['items']![0] = 99;
    expect(encoder.bytes(), snapshot);
  });
}
