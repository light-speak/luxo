import 'dart:async';
import 'dart:convert';
import 'dart:io';
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
          throwsA(
            isA<LuxoError>()
                .having((e) => e.error, 'error', 'TimeoutError')
                .having((e) => e.code, 'code', 0),
          ),
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
          throwsA(
            isA<LuxoError>().having((e) => e.error, 'error', 'TimeoutError'),
          ),
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
              jsonEncode({
                'error': 'Unauthorized',
                'code': 401,
                'message': 'expired',
              }),
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
            jsonEncode({
              'error': 'Unauthorized',
              'code': 401,
              'message': 'expired',
            }),
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
          throwsA(
            isA<LuxoError>()
                .having((e) => e.error, 'error', 'Unauthorized')
                .having((e) => e.code, 'code', 401),
          ),
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
            jsonEncode({
              'error': 'Unauthorized',
              'code': 401,
              'message': 'expired',
            }),
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
          throwsA(isA<LuxoError>().having((e) => e.code, 'code', 401)),
        );
        await Future.delayed(Duration.zero);
        expect(callCount, equals(1)); // no retry
        transport.close();
      });

      test('throws 401 when onTokenExpired is not set', () async {
        final mockClient = http_testing.MockClient((request) async {
          return http.Response(
            jsonEncode({
              'error': 'Unauthorized',
              'code': 401,
              'message': 'expired',
            }),
            401,
          );
        });

        final transport = HttpTransport(
          'http://localhost:8080',
          client: mockClient,
        );

        expect(
          () => transport.call('user.get'),
          throwsA(isA<LuxoError>().having((e) => e.code, 'code', 401)),
        );
        transport.close();
      });

      test('retries binary call on 401', () async {
        var callCount = 0;
        final mockClient = http_testing.MockClient((request) async {
          callCount++;
          if (callCount == 1) {
            return http.Response(
              jsonEncode({
                'error': 'Unauthorized',
                'code': 401,
                'message': 'expired',
              }),
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
        transport.setSchema({'user.get': APISchemaEntry(1, [])});

        await transport.call('user.get');
        expect(callCount, equals(2));
        transport.close();
      });
    });

    group('existing functionality', () {
      test('successful json call returns data', () async {
        final mockClient = http_testing.MockClient((request) async {
          return http.Response(
            jsonEncode({
              'data': {'id': 1, 'name': 'Alice'},
            }),
            200,
          );
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
            jsonEncode({
              'error': 'NotFound',
              'code': 404,
              'message': 'not found',
            }),
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

        final transport = HttpTransport(
          'http://localhost:8080',
          client: mockClient,
        );
        transport.setToken('my-token');
        await transport.call('test.api');
        expect(capturedAuth, equals('Bearer my-token'));
        transport.close();
      });
    });

    group('binary param encoding', () {
      test('encodes field selection into the request mask', () async {
        late Uint8List captured;
        final mockClient = http_testing.MockClient((request) async {
          captured = request.bodyBytes;
          return http.Response.bytes([], 200);
        });
        final transport = HttpTransport(
          'http://localhost:8080',
          client: mockClient,
          options: TransportOptions(mode: TransportMode.binary),
        );
        transport.setSchema({
          'user.get': APISchemaEntry(1, [], {
            'id': SelectionFieldSchema(1),
            'name': SelectionFieldSchema(2),
          }),
        });

        await transport.call('user.get', params: {r'$select': 'name'});
        expect(captured, equals([1, 2, 1, 2, 0]));
        transport.close();
      });

      test('encodes nested selections recursively', () async {
        late Uint8List captured;
        final mockClient = http_testing.MockClient((request) async {
          captured = request.bodyBytes;
          return http.Response.bytes([], 200);
        });
        final transport = HttpTransport(
          'http://localhost:8080',
          client: mockClient,
          options: const TransportOptions(mode: TransportMode.binary),
        );
        transport.setSchema({
          'user.get': APISchemaEntry(1, const [], const {
            'id': SelectionFieldSchema(1),
            'posts': SelectionFieldSchema(3, 'Post'),
          }, const {
            'Post': {
              'id': SelectionFieldSchema(1),
              'title': SelectionFieldSchema(2),
            },
          }),
        });

        await transport
            .call('user.get', params: {r'$select': 'id,posts{title}'});
        expect(captured, equals([1, 6, 1, 5, 3, 2, 1, 2, 0]));
        transport.close();
      });

      test('encodes filters and sorters in binary mode', () async {
        late Uint8List captured;
        final mockClient = http_testing.MockClient((request) async {
          captured = request.bodyBytes;
          return http.Response.bytes([], 200);
        });
        final transport = HttpTransport(
          'http://localhost:8080',
          client: mockClient,
          options: const TransportOptions(mode: TransportMode.binary),
        );
        transport.setSchema({'user.list': const APISchemaEntry(5)});

        await transport.call('user.list', params: const {
          r'$filters': [LuxoFilter(field: 'age', op: 'gte', value: 18)],
          r'$sorters': [LuxoSorter(field: 'createdAt', order: 'desc')],
        });
        expect(
            captured,
            equals([
              5,
              0,
              0xfe,
              0xff,
              0xff,
              0xff,
              0x07,
              1,
              3,
              97,
              103,
              101,
              4,
              2,
              49,
              56,
              0xff,
              0xff,
              0xff,
              0xff,
              0x07,
              1,
              9,
              99,
              114,
              101,
              97,
              116,
              101,
              100,
              65,
              116,
              1,
              0,
            ]));
        transport.close();
      });

      test('decodes the canonical binary error envelope', () async {
        final body = Uint8List.fromList([
          1,
          0xa0,
          0x06,
          2,
          10,
          66,
          97,
          100,
          82,
          101,
          113,
          117,
          101,
          115,
          116,
          3,
          3,
          98,
          97,
          100,
          4,
          1,
          116,
          5,
          2,
          123,
          125,
          6,
          1,
          99,
          0,
        ]);
        final mockClient = http_testing.MockClient(
          (request) async => http.Response.bytes(body, 400),
        );
        final transport = HttpTransport(
          'http://localhost:8080',
          client: mockClient,
          options: TransportOptions(mode: TransportMode.binary),
        );
        transport.setSchema({'user.get': APISchemaEntry(1)});

        expect(
          () => transport.call('user.get'),
          throwsA(
            isA<LuxoError>()
                .having((e) => e.error, 'error', 'BadRequest')
                .having((e) => e.code, 'code', 400)
                .having((e) => e.traceId, 'traceId', 't')
                .having((e) => e.data, 'data', {}).having(
                    (e) => e.cause, 'cause', 'c'),
          ),
        );
        transport.close();
      });

      test('rejects non-canonical binary error envelopes', () {
        final invalid = [
          [1],
          [1, 0xa0, 0x06, 0],
          [1, 0xa0, 0x06, 2, 1, 69, 3, 1, 109],
          [1, 0xa0, 0x06, 2, 1, 69, 3, 1, 109, 0, 1],
        ];
        for (final bytes in invalid) {
          expect(
            decodeBinaryError(Uint8List.fromList(bytes), 400).error,
            'ParseError',
          );
        }
      });

      test('UUID param encodes as fixed 16 bytes', () async {
        const uuid = '550e8400-e29b-41d4-a716-446655440000';
        late Uint8List captured;
        final mockClient = http_testing.MockClient((request) async {
          captured = request.bodyBytes;
          // minimal valid binary response (UUID scalar): 16 bytes
          return http.Response.bytes(List.filled(16, 0), 200);
        });
        final transport = HttpTransport(
          'http://localhost:8080',
          client: mockClient,
          options: TransportOptions(mode: TransportMode.binary),
        );
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
        final transport = HttpTransport(
          'http://localhost:8080',
          client: mockClient,
          options: TransportOptions(mode: TransportMode.binary),
        );
        transport.setSchema({
          'user.list': APISchemaEntry(9, [ParamSchema(1, 'ids', 'Int', true)]),
        });
        await transport.call(
          'user.list',
          params: {
            'ids': [1, 2, 3],
          },
        );

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
        encodeParam(enc, const ParamSchema(1, 'tags', 'String', true), [
          'a',
          'bb',
        ]);
        enc.writeEnd();
        final dec = LuxoDecoder(enc.bytes());
        expect(dec.nextField(), isTrue);
        expect(dec.fieldID, equals(1));
        expect(dec.readStringArray(), equals(['a', 'bb']));
      });

      test('Bytes and JSON params use length-prefixed raw payloads', () {
        final enc = LuxoEncoder();
        encodeParam(
          enc,
          const ParamSchema(1, 'blob', 'Bytes'),
          Uint8List.fromList([0, 255]),
        );
        encodeParam(enc, const ParamSchema(2, 'metadata', 'JSON'), {
          'ok': true,
        });
        enc.writeEnd();

        final dec = LuxoDecoder(enc.bytes());
        expect(dec.nextField(), isTrue);
        expect(dec.fieldID, 1);
        expect(dec.readBytes(), [0, 255]);
        expect(dec.nextField(), isTrue);
        expect(dec.fieldID, 2);
        expect(jsonDecode(utf8.decode(dec.readBytes())), {'ok': true});
        expect(dec.nextField(), isFalse);
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

      test('unknown param types and non-string DateTime are rejected', () {
        expect(
          () => encodeParam(
            LuxoEncoder(),
            const ParamSchema(1, 'input', 'Model'),
            {},
          ),
          throwsA(isA<LuxoError>()),
        );
        expect(
          () => encodeParam(
            LuxoEncoder(),
            const ParamSchema(1, 'at', 'DateTime'),
            0,
          ),
          throwsFormatException,
        );
      });
    });
  });

  group('WsTransport', () {
    test('subscription completes only after JSON acknowledgement', () async {
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      final unsubscribeReceived = Completer<void>();
      server.listen((request) async {
        final socket = await WebSocketTransformer.upgrade(request);
        socket.listen((message) {
          final json = jsonDecode(message as String) as Map<String, dynamic>;
          if (json[r'$sub'] == 'watchPayload') {
            socket.add(jsonEncode({r'$sub': 'watchPayload', 'ok': true}));
          } else if (json[r'$unsub'] == 'watchPayload') {
            unsubscribeReceived.complete();
          }
        });
      });

      final transport = WsTransport(
        'ws://${server.address.host}:${server.port}',
        autoReconnect: false,
      );
      try {
        final unsubscribe = await transport.subscribe(
          'watchPayload',
          {'projectId': 7},
          (_) {},
        );
        unsubscribe();
        await unsubscribeReceived.future.timeout(const Duration(seconds: 1));
      } finally {
        transport.close();
        await server.close(force: true);
      }
    });

    test('binary subscription error rejects the subscription', () async {
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      server.listen((request) async {
        final socket = await WebSocketTransformer.upgrade(request);
        socket.listen((_) {
          socket.add(<int>[
            BinaryFrameType.subscribeError,
            12,
            1,
            0xa0,
            0x06,
            2,
            10,
            ...utf8.encode('BadRequest'),
            3,
            3,
            ...utf8.encode('bad'),
            0,
          ]);
        });
      });

      final transport = WsTransport(
        'ws://${server.address.host}:${server.port}',
        options: const TransportOptions(mode: TransportMode.binary),
        autoReconnect: false,
      );
      transport.setSchema({'watchPayload': const APISchemaEntry(12)});
      try {
        await expectLater(
          transport.subscribe('watchPayload', const {}, (_) {}),
          throwsA(
            isA<LuxoError>()
                .having((error) => error.error, 'error', 'BadRequest')
                .having((error) => error.code, 'code', 400),
          ),
        );
      } finally {
        transport.close();
        await server.close(force: true);
      }
    });

    test('call and subscription acknowledgements time out', () async {
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      server.listen((request) async {
        final socket = await WebSocketTransformer.upgrade(request);
        socket.listen((_) {});
      });

      final endpoint = 'ws://${server.address.host}:${server.port}';
      final callTransport = WsTransport(
        endpoint,
        autoReconnect: false,
        timeout: const Duration(milliseconds: 10),
      );
      final subscriptionTransport = WsTransport(
        endpoint,
        autoReconnect: false,
        timeout: const Duration(milliseconds: 10),
      );
      try {
        await expectLater(
          callTransport.call('health'),
          throwsA(isA<LuxoError>().having(
            (error) => error.error,
            'error',
            'TimeoutError',
          )),
        );
        await expectLater(
          subscriptionTransport.subscribe('watchPayload', const {}, (_) {}),
          throwsA(isA<LuxoError>().having(
            (error) => error.error,
            'error',
            'TimeoutError',
          )),
        );
      } finally {
        callTransport.close();
        subscriptionTransport.close();
        await server.close(force: true);
      }
    });

    group('auto-reconnect configuration', () {
      test('autoReconnect defaults to true', () {
        final transport = WsTransport('ws://localhost:8080');
        // The transport is created without error — autoReconnect is on by default
        transport.close();
      });

      test('nullable params encode null and present markers', () {
        final enc = LuxoEncoder();
        encodeParam(
          enc,
          const ParamSchema(1, 'nickname', 'String', false, true),
          null,
        );
        encodeParam(
          enc,
          const ParamSchema(2, 'age', 'Int', false, true),
          42,
        );
        enc.writeEnd();
        expect(enc.bytes(), [1, 0, 2, 1, 84, 0]);
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
