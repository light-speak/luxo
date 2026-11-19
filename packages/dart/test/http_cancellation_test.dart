import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:luxo_client/luxo_client.dart';
import 'package:test/test.dart';

void main() {
  for (final mode in TransportMode.values) {
    for (final generated in [false, true]) {
      for (final headersReceived in [false, true]) {
        test(
          'deadline never fails early in $mode generated=$generated headers=$headersReceived',
          () async {
            const timeout = Duration(milliseconds: 1);
            final elapsed = <Duration>[];
            for (var i = 0; i < 32; i++) {
              final client = _PendingClient(headersReceived);
              final transport = HttpTransport(
                'https://example.test/luvia',
                client: client,
                options: TransportOptions(mode: mode),
                timeout: timeout,
              )..setSchema({'echo': APISchemaEntry(1, [])});
              final request = generated ? _ClosingRequest(() {}) : null;
              final clock = Stopwatch()..start();
              try {
                await transport.call<Object?>('echo', request: request);
                fail('stalled request returned data');
              } on LuxoError catch (error) {
                elapsed.add(clock.elapsed);
                expect(error.error, 'TimeoutError');
                if (client.calls > 0) await client.aborted.future;
                expect(client.abortCount, client.calls);
                expect(client.active, 0);
              } finally {
                transport.close();
              }
            }
            expect(
              elapsed.where((value) => value < timeout),
              isEmpty,
              reason:
                  'a pending call must receive its entire configured budget',
            );
          },
        );
      }
    }
    test('refresh cannot fail before the shared deadline in $mode', () async {
      const timeout = Duration(milliseconds: 1);
      final encoder = LuxoEncoder()
        ..writeFieldInt(1, LuxoInt64.fromInt(401))
        ..writeFieldString(2, 'Unauthorized')
        ..writeFieldString(3, 'expired')
        ..writeEnd();
      final body = mode == TransportMode.json
          ? utf8.encode(
              '{"error":"Unauthorized","code":401,"message":"expired"}',
            )
          : encoder.bytes();
      var refreshes = 0;
      final elapsed = <Duration>[];
      for (var i = 0; i < 32; i++) {
        final transport = HttpTransport(
          'https://example.test/luvia',
          client: MockClient((_) async => http.Response.bytes(body, 401)),
          options: TransportOptions(mode: mode),
          timeout: timeout,
          onTokenExpired: () {
            refreshes++;
            return Completer<String?>().future;
          },
        )..setSchema({'echo': APISchemaEntry(1, [])});
        final clock = Stopwatch()..start();
        try {
          await transport.call<Object?>('echo');
          fail('stalled refresh returned data');
        } on LuxoError catch (error) {
          elapsed.add(clock.elapsed);
          expect(error.error, 'TimeoutError');
        } finally {
          transport.close();
        }
      }
      expect(refreshes, greaterThan(0));
      expect(elapsed.where((value) => value < timeout), isEmpty);
    });

    test('nonpositive deadlines never send or overflow in $mode', () async {
      for (final timeout in [
        Duration.zero,
        const Duration(microseconds: -1),
        const Duration(microseconds: -0x8000000000000000),
      ]) {
        var calls = 0;
        final transport = HttpTransport(
          'https://example.test/luvia',
          client: MockClient((_) async {
            calls++;
            return http.Response('{"data":1}', 200);
          }),
          options: TransportOptions(mode: mode),
          timeout: timeout,
        )..setSchema({'echo': APISchemaEntry(1, [])});
        try {
          await expectLater(
            transport.call<Object?>('echo'),
            throwsA(
              isA<LuxoError>().having(
                (error) => error.error,
                'error',
                'TimeoutError',
              ),
            ),
          );
          expect(calls, 0);
        } finally {
          transport.close();
        }
      }
    });

    test(
      'encoding failures remain asynchronous and unchanged in $mode',
      () async {
        final failure = StateError('encoding failed');
        final client = MockClient((_) async {
          fail('invalid encoding reached the backend');
        });
        final transport = HttpTransport(
          'https://example.test/luvia',
          client: client,
          options: TransportOptions(mode: mode),
        )..setSchema({'echo': APISchemaEntry(1, [])});
        addTearDown(transport.close);
        late Future<Object?> pending;
        expect(
          () => pending = transport.call<Object?>(
            'echo',
            request: _ClosingRequest(() => throw failure),
          ),
          returnsNormally,
        );
        await expectLater(pending, throwsA(same(failure)));
      },
    );

    test('close during generated encoding prevents sending in $mode', () async {
      var calls = 0;
      final client = MockClient((request) async {
        calls++;
        return http.Response('{"data":true}', 200);
      });
      final transport = HttpTransport(
        'https://example.test/luvia',
        client: client,
        options: TransportOptions(mode: mode),
      )..setSchema({'echo': APISchemaEntry(1, [])});
      addTearDown(transport.close);
      await expectLater(
        transport.call<Object?>(
          'echo',
          request: _ClosingRequest(transport.close),
        ),
        throwsA(
          isA<LuxoError>().having(
            (error) => error.error,
            'error',
            'NetworkError',
          ),
        ),
      );
      expect(calls, 0);
    });

    test(
      'timeout leaves sibling calls and the client usable in $mode',
      () async {
        final client = _PendingClient(false, allowFollowing: true);
        final transport = HttpTransport(
          'https://example.test/luvia',
          client: client,
          options: TransportOptions(mode: mode),
          timeout: const Duration(milliseconds: 50),
        )..setSchema({'echo': APISchemaEntry(1, [])});
        addTearDown(transport.close);
        final ended = expectLater(
          transport.call<Object?>('echo'),
          throwsA(isA<LuxoError>()),
        );
        final expected = mode == TransportMode.json ? 1 : [2];
        expect(await transport.call<Object?>('echo'), expected);
        await ended;
        await client.aborted.future.timeout(const Duration(seconds: 1));
        expect(client.abortCount, 1);
        expect(client.closed, isFalse);
        expect(await transport.call<Object?>('echo'), expected);
      },
    );
    for (final headersReceived in [false, true]) {
      test('timeout aborts $mode after headers=$headersReceived', () async {
        final client = _PendingClient(headersReceived);
        final transport = HttpTransport(
          'https://example.test/luvia',
          client: client,
          options: TransportOptions(mode: mode),
          timeout: const Duration(milliseconds: 20),
        )..setSchema({'echo': APISchemaEntry(1, [])});
        addTearDown(transport.close);

        await expectLater(
          transport.call<Object?>('echo'),
          throwsA(
            isA<LuxoError>().having(
              (error) => error.error,
              'error',
              'TimeoutError',
            ),
          ),
        );
        await client.aborted.future.timeout(const Duration(seconds: 1));
        expect(client.abortCount, 1, reason: 'deadline must abort the request');
        expect(client.active, 0, reason: 'timeout must release the stream');
      });
    }
    test(
      'close settles a pending $mode request and rejects new calls',
      () async {
        final client = _PendingClient(false);
        final transport = HttpTransport(
          'https://example.test/luvia',
          client: client,
          options: TransportOptions(mode: mode),
        )..setSchema({'echo': APISchemaEntry(1, [])});
        final pending = transport.call<Object?>('echo');
        final ended = expectLater(pending, throwsA(isA<LuxoError>()));
        await Future<void>.delayed(Duration.zero);
        transport.close();
        transport.close();
        await ended.timeout(const Duration(seconds: 1));
        await expectLater(
          transport.call<Object?>('echo'),
          throwsA(isA<LuxoError>()),
        );
        expect(client.active, 0);
      },
    );
  }
}

class _PendingClient extends http.BaseClient {
  final bool headersReceived;
  final bool allowFollowing;
  final _headers = Completer<http.StreamedResponse>();
  final _body = StreamController<List<int>>();
  final aborted = Completer<void>();
  int active = 0;
  int abortCount = 0;
  bool closed = false;
  int calls = 0;

  _PendingClient(this.headersReceived, {this.allowFollowing = false});

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    calls++;
    if (allowFollowing && calls > 1) {
      final body = request.headers['x-luxo-mode'] == 'binary'
          ? [2]
          : utf8.encode('{"data":1}');
      return http.StreamedResponse(Stream.value(body), 200);
    }
    active++;
    if (request is http.AbortableRequest) {
      unawaited(_watchAbort(request));
    }
    if (headersReceived) return http.StreamedResponse(_body.stream, 200);
    return _headers.future;
  }

  Future<void> _watchAbort(http.AbortableRequest request) async {
    await request.abortTrigger;
    if (closed) return;
    abortCount++;
    active--;
    aborted.complete();
    final error = http.RequestAbortedException(request.url);
    if (headersReceived) {
      _body.addError(error);
      unawaited(_body.close());
    } else {
      _headers.completeError(error);
    }
  }

  @override
  void close() {
    if (closed) return;
    closed = true;
    active = 0;
    if (calls > 0 && !headersReceived && !_headers.isCompleted) {
      _headers.completeError(http.ClientException('closed'));
    }
    unawaited(_body.close());
  }
}

class _ClosingRequest extends GeneratedRequest {
  final void Function() close;
  _ClosingRequest(this.close);
  @override
  int get apiID => 1;
  @override
  void writeParameters(LuxoEncoder encoder) => close();
  @override
  Map<String, dynamic> toJSONParameters() {
    close();
    return {};
  }
}
