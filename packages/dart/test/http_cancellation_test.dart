import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:luxo_client/luxo_client.dart';
import 'package:test/test.dart';

void main() {
  for (final mode in TransportMode.values) {
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
    if (!headersReceived && !_headers.isCompleted) {
      _headers.completeError(http.ClientException('closed'));
    }
    unawaited(_body.close());
  }
}
