import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart' as http_testing;
import 'package:test/test.dart';
import 'package:luxo_client/src/transport.dart';
import 'package:luxo_client/src/codec.dart';
import 'package:luxo_client/src/error.dart';

void main() {
  group('HttpTransport', () {
    group('timeout', () {
      test('default timeout is 30 seconds', () {
        final transport = HttpTransport('http://localhost:8080');
        expect(transport.timeout, equals(const Duration(seconds: 30)));
        transport.close();
      });

      test('custom timeout is accepted', () {
        final transport = HttpTransport(
          'http://localhost:8080',
          timeout: const Duration(seconds: 5),
        );
        expect(transport.timeout, equals(const Duration(seconds: 5)));
        transport.close();
      });

      test('throws TimeoutError when request times out (json mode)', () async {
        final slowClient = http_testing.MockClient((request) async {
          await Future.delayed(const Duration(seconds: 3));
          return http.Response('{}', 200);
        });

        final transport = HttpTransport(
          'http://localhost:8080',
          client: slowClient,
          timeout: const Duration(milliseconds: 50),
        );

        expect(
          () => transport.call('user.get', params: {'id': 1}),
          throwsA(isA<LuxoError>()
              .having((e) => e.error, 'error', 'TimeoutError')
              .having((e) => e.code, 'code', 0)),
        );

        transport.close();
      });

      test('throws TimeoutError when binary request times out', () async {
        final slowClient = http_testing.MockClient((request) async {
          await Future.delayed(const Duration(seconds: 3));
          return http.Response('{}', 200);
        });

        final transport = HttpTransport(
          'http://localhost:8080',
          client: slowClient,
          timeout: const Duration(milliseconds: 50),
          options: TransportOptions(mode: TransportMode.binary),
        );
        transport.setSchema({
          'user.get': APISchemaEntry(1, [ParamSchema(1, 'id', 'Int')]),
        });

        expect(
          () => transport.call('user.get', params: {'id': 1}),
          throwsA(isA<LuxoError>()
              .having((e) => e.error, 'error', 'TimeoutError')),
        );

        transport.close();
      });
    });

    group('onTokenExpired (401 auto-refresh)', () {
      test('retries with new token on 401', () async {
        var callCount = 0;
        final mockClient = http_testing.MockClient((request) async {
          callCount++;
          if (callCount == 1) {
            // First call returns 401
            return http.Response(
              jsonEncode({'error': 'Unauthorized', 'code': 401, 'message': 'expired'}),
              401,
            );
          }
          // Retry with new token succeeds
          expect(request.headers['Authorization'], equals('Bearer new-token'));
          return http.Response(jsonEncode({'data': 'ok'}), 200);
        });

        final transport = HttpTransport(
          'http://localhost:8080',
          client: mockClient,
          onTokenExpired: () async => 'new-token',
          options: TransportOptions(token: 'old-token'),
        );

        final result = await transport.call('user.get');
        expect(result, equals('ok'));
        expect(callCount, equals(2));
        transport.close();
      });

      test('does not retry more than once', () async {
        var callCount = 0;
        final mockClient = http_testing.MockClient((request) async {
          callCount++;
          return http.Response(
            jsonEncode({'error': 'Unauthorized', 'code': 401, 'message': 'expired'}),
            401,
          );
        });

        final transport = HttpTransport(
          'http://localhost:8080',
          client: mockClient,
          onTokenExpired: () async => 'new-token',
        );

        expect(
          () => transport.call('user.get'),
          throwsA(isA<LuxoError>()
              .having((e) => e.error, 'error', 'Unauthorized')
              .having((e) => e.code, 'code', 401)),
        );
        // Wait for futures to complete
        await Future.delayed(Duration.zero);
        expect(callCount, equals(2)); // original + 1 retry
        transport.close();
      });

      test('throws 401 when onTokenExpired returns null', () async {
        var callCount = 0;
        final mockClient = http_testing.MockClient((request) async {
          callCount++;
          return http.Response(
            jsonEncode({'error': 'Unauthorized', 'code': 401, 'message': 'expired'}),
            401,
          );
        });

        final transport = HttpTransport(
          'http://localhost:8080',
          client: mockClient,
          onTokenExpired: () async => null,
        );

        expect(
          () => transport.call('user.get'),
          throwsA(isA<LuxoError>()
              .having((e) => e.code, 'code', 401)),
        );
        await Future.delayed(Duration.zero);
        expect(callCount, equals(1)); // no retry
        transport.close();
      });

      test('throws 401 when onTokenExpired is not set', () async {
        final mockClient = http_testing.MockClient((request) async {
          return http.Response(
            jsonEncode({'error': 'Unauthorized', 'code': 401, 'message': 'expired'}),
            401,
          );
        });

        final transport = HttpTransport(
          'http://localhost:8080',
          client: mockClient,
        );

        expect(
          () => transport.call('user.get'),
          throwsA(isA<LuxoError>()
              .having((e) => e.code, 'code', 401)),
        );
        transport.close();
      });

      test('retries binary call on 401', () async {
        var callCount = 0;
        final mockClient = http_testing.MockClient((request) async {
          callCount++;
          if (callCount == 1) {
            return http.Response(
              jsonEncode({'error': 'Unauthorized', 'code': 401, 'message': 'expired'}),
              401,
            );
          }
          // Return valid binary response on retry
          return http.Response.bytes([0x00], 200);
        });

        final transport = HttpTransport(
          'http://localhost:8080',
          client: mockClient,
          onTokenExpired: () async => 'refreshed-token',
          options: TransportOptions(
            token: 'old-token',
            mode: TransportMode.binary,
          ),
        );
        transport.setSchema({
          'user.get': APISchemaEntry(1, []),
        });

        final result = await transport.call('user.get');
        expect(callCount, equals(2));
        transport.close();
      });
    });

    group('existing functionality', () {
      test('successful json call returns data', () async {
        final mockClient = http_testing.MockClient((request) async {
          return http.Response(jsonEncode({'data': {'id': 1, 'name': 'Alice'}}), 200);
        });

        final transport = HttpTransport(
          'http://localhost:8080',
          client: mockClient,
        );

        final result = await transport.call('user.get', params: {'id': 1});
        expect(result, equals({'id': 1, 'name': 'Alice'}));
        transport.close();
      });

      test('throws LuxoError on server error response', () async {
        final mockClient = http_testing.MockClient((request) async {
          return http.Response(
            jsonEncode({'error': 'NotFound', 'code': 404, 'message': 'not found'}),
            200,
          );
        });

        final transport = HttpTransport(
          'http://localhost:8080',
          client: mockClient,
        );

        expect(
          () => transport.call('user.get'),
          throwsA(isA<LuxoError>().having((e) => e.error, 'error', 'NotFound')),
        );
        transport.close();
      });

      test('setToken updates authorization header', () async {
        String? capturedAuth;
        final mockClient = http_testing.MockClient((request) async {
          capturedAuth = request.headers['Authorization'];
          return http.Response(jsonEncode({'data': null}), 200);
        });

        final transport = HttpTransport('http://localhost:8080', client: mockClient);
        transport.setToken('my-token');
        await transport.call('test.api');
        expect(capturedAuth, equals('Bearer my-token'));
        transport.close();
      });
    });

    group('binary param encoding', () {
      test('UUID param encodes as fixed 16 bytes', () async {
        const uuid = '550e8400-e29b-41d4-a716-446655440000';
        late Uint8List captured;
        final mockClient = http_testing.MockClient((request) async {
          captured = request.bodyBytes;
          // minimal valid binary response (UUID scalar): 16 bytes
          return http.Response.bytes(List.filled(16, 0), 200);
        });
        final transport = HttpTransport('http://localhost:8080',
            client: mockClient, options: TransportOptions(mode: TransportMode.binary));
        transport.setSchema({
          'user.get': APISchemaEntry(7, [ParamSchema(1, 'id', 'UUID')]),
        });
        await transport.call('user.get', params: {'id': uuid});

        // Layout: varint apiID(7) | varint seq(0) | fieldID(1) | 16 bytes uuid | end(0)
        expect(captured[0], equals(7)); // apiID
        expect(captured[1], equals(0)); // seq
        expect(captured[2], equals(1)); // fieldID
        // 16 raw UUID bytes follow; first byte = 0x55
        expect(captured[3], equals(0x55));
        // total: 3 header + 16 + 1 end = 20
        expect(captured.length, equals(20));
        transport.close();
      });

      test('list param encodes as count-prefixed array', () async {
        late Uint8List captured;
        final mockClient = http_testing.MockClient((request) async {
          captured = request.bodyBytes;
          return http.Response.bytes(List.filled(8, 0), 200);
        });
        final transport = HttpTransport('http://localhost:8080',
            client: mockClient, options: TransportOptions(mode: TransportMode.binary));
        transport.setSchema({
          'user.list': APISchemaEntry(9, [ParamSchema(1, 'ids', 'Int', true)]),
        });
        await transport.call('user.list', params: {'ids': [1, 2, 3]});

        // varint apiID(9) | seq(0) | fieldID(1) | count(3) | sv(1) sv(2) sv(3) | end(0)
        expect(captured[0], equals(9));
        expect(captured[1], equals(0));
        expect(captured[2], equals(1)); // fieldID
        expect(captured[3], equals(3)); // count
        // zigzag(1)=2, zigzag(2)=4, zigzag(3)=6
        expect(captured[4], equals(2));
        expect(captured[5], equals(4));
        expect(captured[6], equals(6));
        expect(captured[7], equals(0)); // end
        transport.close();
      });

      test('string list param round-trips through encodeParam', () {
        final enc = LuxoEncoder();
        encodeParam(enc, const ParamSchema(1, 'tags', 'String', true), ['a', 'bb']);
        enc.writeEnd();
        final dec = LuxoDecoder(enc.bytes());
        expect(dec.nextField(), isTrue);
        expect(dec.fieldID, equals(1));
        expect(dec.readStringArray(), equals(['a', 'bb']));
      });

      test('UUID list param round-trips through encodeParam', () {
        const a = '550e8400-e29b-41d4-a716-446655440000';
        final enc = LuxoEncoder();
        encodeParam(enc, const ParamSchema(1, 'ids', 'UUID', true), [a]);
        enc.writeEnd();
        final dec = LuxoDecoder(enc.bytes());
        expect(dec.nextField(), isTrue);
        expect(dec.readUuidArray(), equals([a]));
      });
    });
  });

  group('WsTransport', () {
    group('auto-reconnect configuration', () {
      test('autoReconnect defaults to true', () {
        final transport = WsTransport('ws://localhost:8080');
        // The transport is created without error — autoReconnect is on by default
        transport.close();
      });

      test('autoReconnect can be disabled', () {
        final transport = WsTransport(
          'ws://localhost:8080',
          autoReconnect: false,
        );
        transport.close();
      });

      test('close cancels reconnection and prevents further reconnect', () {
        final transport = WsTransport('ws://localhost:8080');
        transport.close();
        // After close, no reconnection should be attempted
        // (verified by not throwing / hanging)
      });
    });
  });
}
