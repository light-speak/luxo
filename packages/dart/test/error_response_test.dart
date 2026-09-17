import 'dart:convert';
import 'dart:typed_data';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:luxo_client/luxo_client.dart';
import 'package:test/test.dart';

void main() {
  test(
    'JSON and binary errors retain nested numeric tokens verbatim',
    () async {
      for (final token in [
        '-0',
        '1E+03',
        '123456789012345678901234567890',
        '0.12345678901234567890123456789',
        '1e400',
        '1e-400',
      ]) {
        final text = '{"n":$token,"items":[$token,null]}';
        final error = await _jsonError(
          '{"error":"Rejected","code":400,"message":"invalid","data":$text}',
        );
        final binary = _errorHeader(LuxoInt64.fromInt(400))
          ..writeFieldString(5, text)
          ..writeEnd();
        expect(encodeLuxoJSON(error.data), text);
        expect(
          encodeLuxoJSON(decodeBinaryError(binary.bytes(), 502).data),
          text,
        );
      }
    },
  );

  test(
    'JSON error fields fail explicitly instead of leaking cast errors',
    () async {
      final valid = <String, Object?>{
        'error': 'Rejected',
        'code': 400,
        'message': 'invalid',
      };
      for (final field in ['error', 'code', 'message', 'traceId', 'cause']) {
        final values = field == 'code'
            ? <Object?>[
                null,
                true,
                '400',
                1.5,
                BigInt.parse('9007199254740992'),
              ]
            : <Object?>[true, 400, <Object?>[], <String, Object?>{}];
        for (final value in values) {
          await _expectJSONError(
            encodeLuxoJSON({...valid, field: value}),
            'ParseError',
            502,
          );
        }
      }
      for (final field in ['code', 'message']) {
        final missing = {...valid}..remove(field);
        await _expectJSONError(encodeLuxoJSON(missing), 'ParseError', 502);
      }
      for (final field in ['error', 'message']) {
        await _expectJSONError(
          encodeLuxoJSON({...valid, field: null}),
          'ParseError',
          502,
        );
      }
      for (final token in [
        '400.0',
        '4e2',
        '9223372036854775808',
        '-9007199254740992',
      ]) {
        await _expectJSONError(
          '{"error":"Rejected","code":$token,"message":"invalid"}',
          'ParseError',
          502,
        );
      }
    },
  );

  test(
    'JSON error codes retain the existing portable int domain exactly',
    () async {
      for (final value in [0, -1, 400, -9007199254740991, 9007199254740991]) {
        await _expectJSONError(
          '{"error":"Rejected","code":$value,"message":"invalid"}',
          'Rejected',
          value,
        );
      }
      await _expectJSONError(
        '{"error":"Rejected","code":-0,"message":"invalid"}',
        'Rejected',
        0,
      );
    },
  );

  test(
    'JSON error data preserves wide integers and raw fractional tokens',
    () async {
      final error = await _jsonError(
        '{"error":"Rejected","code":400,"message":"invalid","traceId":"trace",'
        '"data":{"id":9223372036854775807,"ratio":1.5},"cause":"detail"}',
      );
      expect(
        encodeLuxoJSON(error.data),
        '{"id":9223372036854775807,"ratio":1.5}',
      );
      expect((error.data as Map)['ratio'], isA<LuxoJSONNumber>());
      expect(error.traceId, 'trace');
      expect(error.cause, 'detail');
    },
  );

  test('binary errors reject unrepresentable codes without throwing', () {
    for (final value in [
      '9007199254740992',
      '-9007199254740992',
      '9223372036854775807',
      '-9223372036854775808',
    ]) {
      final encoder = _errorHeader(LuxoInt64.parse(value))..writeEnd();
      _expectBinaryParseError(encoder.bytes());
    }
  });

  test('binary error optional fields reject malformed UTF-8 and JSON', () {
    for (final field in [2, 3, 4, 5, 6]) {
      final encoder = LuxoEncoder()..writeFieldInt(1, LuxoInt64.fromInt(400));
      if (field != 2) encoder.writeFieldString(2, 'Rejected');
      if (field != 3) encoder.writeFieldString(3, 'invalid');
      encoder
        ..writeVarint(field)
        ..writeBytes(Uint8List.fromList([255]))
        ..writeEnd();
      _expectBinaryParseError(encoder.bytes());
    }
    final encoder = _errorHeader(LuxoInt64.fromInt(400))
      ..writeVarint(5)
      ..writeBytes(Uint8List.fromList(utf8.encode('{')))
      ..writeEnd();
    _expectBinaryParseError(encoder.bytes());
  });

  test('binary error fields cannot be duplicated or unknown', () {
    for (var field = 1; field <= 6; field++) {
      final encoder = _errorHeader(LuxoInt64.fromInt(400));
      if (field >= 4) _writeErrorField(encoder, field);
      _writeErrorField(encoder, field);
      encoder.writeEnd();
      _expectBinaryParseError(encoder.bytes());
    }
    final encoder = _errorHeader(LuxoInt64.fromInt(400))
      ..writeVarint(7)
      ..writeEnd();
    _expectBinaryParseError(encoder.bytes());
  });

  test('binary errors preserve codes, structured data and optional fields', () {
    for (final value in [0, -1, 400, -9007199254740991, 9007199254740991]) {
      final encoder = _errorHeader(LuxoInt64.fromInt(value))
        ..writeFieldString(4, 'trace')
        ..writeVarint(5)
        ..writeBytes(
          Uint8List.fromList(
            utf8.encode('{"id":9223372036854775807,"ratio":1.5}'),
          ),
        )
        ..writeFieldString(6, 'detail')
        ..writeEnd();
      final error = decodeBinaryError(encoder.bytes(), 502);
      expect(error.error, 'Rejected');
      expect(error.code, value);
      expect(error.traceId, 'trace');
      expect(error.cause, 'detail');
      expect(
        encodeLuxoJSON(error.data),
        '{"id":9223372036854775807,"ratio":1.5}',
      );
      expect((error.data as Map)['ratio'], isA<LuxoJSONNumber>());
    }
  });
}

Future<LuxoError> _jsonError(String body) async {
  final transport = HttpTransport(
    'http://localhost/luvia',
    client: MockClient((_) async => http.Response(body, 502)),
  );
  try {
    await transport.call<Object>('echo');
    throw StateError('Expected an error response');
  } on LuxoError catch (error) {
    return error;
  } finally {
    transport.close();
  }
}

Future<void> _expectJSONError(String body, String name, int code) async {
  final error = await _jsonError(body);
  expect(error.error, name, reason: body);
  expect(error.code, code, reason: body);
}

LuxoEncoder _errorHeader(LuxoInt64 code) => LuxoEncoder()
  ..writeFieldInt(1, code)
  ..writeFieldString(2, 'Rejected')
  ..writeFieldString(3, 'invalid');

void _writeErrorField(LuxoEncoder encoder, int field) {
  if (field == 1) {
    encoder.writeFieldInt(field, LuxoInt64.fromInt(401));
  } else {
    encoder.writeFieldString(field, field == 5 ? '{}' : 'text');
  }
}

void _expectBinaryParseError(Uint8List bytes) {
  final error = decodeBinaryError(bytes, 502);
  expect(error.error, 'ParseError');
  expect(error.code, 502);
}
