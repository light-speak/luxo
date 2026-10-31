@TestOn('vm')
library;

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:http/http.dart' as http;
import 'package:http/io_client.dart';
import 'package:luxo_client/http2.dart';
import 'package:luxo_client/src/buffered_http_response.dart';
import 'package:luxo_client/luxo_client.dart';
import 'package:test/test.dart';

void main() {
  late Process server;
  late Directory temporary;
  late Uri endpoint;
  late Uri http1;
  late SecurityContext trust;
  late LuxoHttp2Client client;
  setUpAll(() async {
    await Directory('.tmp').create();
    temporary = await Directory('.tmp').createTemp('http2-integration-');
    server = await Process.start(
      'go',
      ['run', 'test/fixtures/http2_server.go'],
      environment: {'GOTMPDIR': temporary.absolute.path},
    );
    unawaited(server.stderr.drain<void>());
    final line = await server.stdout
        .transform(utf8.decoder)
        .transform(const LineSplitter())
        .first
        .timeout(const Duration(seconds: 60));
    final config = jsonDecode(line) as Map<String, dynamic>;
    endpoint = Uri.parse(config['url'] as String);
    http1 = Uri.parse(config['http1'] as String);
    trust = SecurityContext(withTrustedRoots: false)
      ..setTrustedCertificatesBytes(
        utf8.encode(config['certificate'] as String),
      );
  });
  tearDownAll(() async {
    await server.stdin.close();
    await server.exitCode.timeout(const Duration(seconds: 10));
    await temporary.delete(recursive: true);
  });
  setUp(
    () => client = LuxoHttp2Client(
      endpoint,
      securityContext: trust,
      maxConcurrentRequests: 2,
      maxBodyBytes: 1024,
    ),
  );
  tearDown(() => client.close());

  test('H2 hands off complete independent bodies after close', () async {
    final responses = await Future.wait([
      for (final value in [17, 23])
        client.send(http.Request('POST', endpoint)..bodyBytes = [value]),
    ]);
    client.close();
    final first = responses[0] as BufferedHTTPResponse;
    final second = responses[1] as BufferedHTTPResponse;
    expect(first.bodyBytes, [17]);
    expect(second.bodyBytes, [23]);
    expect(identical(first.toResponse().bodyBytes, first.bodyBytes), isTrue);
    first.bodyBytes[0] = 99;
    expect(second.bodyBytes, [23]);
  });

  test(
    'HEAD length describes the representation rather than a missing body',
    () async {
      final response = await client.head(endpoint.resolve('/head'));
      expect(response.statusCode, 200);
      expect(response.headers['content-length'], '5');
      expect(response.bodyBytes, isEmpty);
    },
  );

  test('informational headers do not leak into the final response', () async {
    final response = await client.post(
      endpoint.resolve('/early'),
      body: [7, 8],
    );
    expect(response.statusCode, 200);
    expect(response.bodyBytes, [7, 8]);
    expect(response.headers['x-early'], isNull);
    expect(response.headers['x-final'], 'response');
    expect(response.headers['x-test-protocol'], 'HTTP/2.0');
  });

  test('HTTP/2 selection rejects HTTP/1.1 without an implicit retry', () async {
    final countURL = endpoint.resolve('/http1-count');
    final before = int.parse((await client.get(countURL)).body);
    final onlyH2 = LuxoHttp2Client(http1, securityContext: trust);
    addTearDown(onlyH2.close);
    await expectLater(
      onlyH2.post(http1, body: [1, 2, 3]),
      throwsA(isA<http.ClientException>()),
    );
    expect(int.parse((await client.get(countURL)).body), before);

    final compatible = IOClient(HttpClient(context: trust));
    addTearDown(compatible.close);
    final response = await compatible.post(http1, body: [1, 2, 3]);
    expect(response.statusCode, 200);
    expect(response.headers['x-test-protocol'], 'HTTP/1.1');
    expect(int.parse((await client.get(countURL)).body), before + 1);
  });

  test('slow TLS handshake times out and closes the socket', () async {
    final raw = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
    final disconnected = Completer<void>();
    final sockets = <Socket>[];
    final listener = raw.listen((socket) {
      sockets.add(socket);
      socket.listen(
        (_) {},
        onDone: () {
          socket.destroy();
          if (!disconnected.isCompleted) disconnected.complete();
        },
      );
    });
    final slow = LuxoHttp2Client(
      Uri.parse('https://127.0.0.1:${raw.port}'),
      connectTimeout: const Duration(milliseconds: 100),
    );
    try {
      await expectLater(
        slow.get(slow.endpoint),
        throwsA(isA<http.ClientException>()),
      );
      await disconnected.future.timeout(const Duration(seconds: 2));
    } finally {
      slow.close();
      for (final socket in sockets) {
        socket.destroy();
      }
      await listener.cancel();
      await raw.close();
    }
  });

  test('TLS H2 preserves bytes, concurrent calls and HTTP errors', () async {
    final responses = await Future.wait([
      client.post(endpoint.resolve('/echo'), body: [1, 2, 255]),
      client.post(endpoint.resolve('/error'), body: [3, 4]),
    ]);
    expect(responses.first.headers['x-test-protocol'], 'HTTP/2.0');
    expect(responses.first.bodyBytes, [1, 2, 255]);
    expect(responses.last.statusCode, 401);
    expect(responses.last.bodyBytes, [3, 4]);
  });
  test(
    'flow control preserves concurrent bodies larger than one window',
    () async {
      client.close();
      client = LuxoHttp2Client(endpoint, securityContext: trust);
      final data = Uint8List.fromList(
        List.generate(128 * 1024, (i) => i & 255),
      );
      final responses = await Future.wait(
        List.generate(2, (_) => client.post(endpoint, body: data)),
      );
      for (final response in responses) {
        expect(response.bodyBytes, data);
      }
    },
  );
  test(
    'cancel one stream while siblings and subsequent requests succeed',
    () async {
      final abort = Completer<void>();
      final pending = client.send(
        http.AbortableRequest(
          'POST',
          endpoint.resolve('/slow'),
          abortTrigger: abort.future,
        ),
      );
      final ended = expectLater(
        pending,
        throwsA(isA<http.RequestAbortedException>()),
      );
      expect(
        (await client.post(endpoint.resolve('/echo'), body: [1])).bodyBytes,
        [1],
      );
      abort.complete();
      await ended;
      expect(
        (await client.post(endpoint.resolve('/echo'), body: [2])).bodyBytes,
        [2],
      );
    },
  );
  test('request and response limits release capacity', () async {
    await expectLater(
      client.post(endpoint, body: Uint8List(1025)),
      throwsA(isA<http.ClientException>()),
    );
    await expectLater(
      client.get(endpoint.resolve('/large')),
      throwsA(isA<http.ClientException>()),
    );
    expect((await client.post(endpoint, body: [7])).bodyBytes, [7]);
  });
  test('oversized unfinished response cancels only its stream', () async {
    final initial = await client.post(endpoint, body: [1]);
    final abort = Completer<void>();
    final sibling = expectLater(
      client.send(
        http.AbortableRequest(
          'GET',
          endpoint.resolve('/slow'),
          abortTrigger: abort.future,
        ),
      ),
      throwsA(isA<http.RequestAbortedException>()),
    );
    try {
      await expectLater(
        client.get(endpoint.resolve('/large-open')),
        throwsA(
          isA<http.ClientException>().having(
            (error) => error.message,
            'message',
            contains('exceeds limit'),
          ),
        ),
      ).timeout(const Duration(seconds: 2));
      final cancelled = await client
          .get(endpoint.resolve('/large-cancelled'))
          .timeout(const Duration(seconds: 2));
      expect(cancelled.statusCode, 204);
      final next = await client.post(endpoint, body: [7]);
      expect(next.bodyBytes, [7]);
      expect(
        next.headers['x-test-connection'],
        initial.headers['x-test-connection'],
      );
    } finally {
      abort.complete();
      await sibling;
    }
  });
  test(
    'cancel a partially received body releases its stream and capacity',
    () async {
      final abort = Completer<void>();
      final pending = client.send(
        http.AbortableRequest(
          'POST',
          endpoint.resolve('/slow-body'),
          abortTrigger: abort.future,
        ),
      );
      final ended = expectLater(
        pending,
        throwsA(isA<http.RequestAbortedException>()),
      );
      expect(
        (await client
                .get(endpoint.resolve('/body-started'))
                .timeout(const Duration(seconds: 2)))
            .statusCode,
        204,
      );
      abort.complete();
      await ended.timeout(const Duration(seconds: 2));
      expect(
        (await client
                .get(endpoint.resolve('/body-cancelled'))
                .timeout(const Duration(seconds: 2)))
            .statusCode,
        204,
      );
      final responses = await Future.wait([
        client.post(endpoint, body: [21]),
        client.post(endpoint, body: [22]),
      ]);
      expect(responses.map((response) => response.bodyBytes), [
        [21],
        [22],
      ]);
    },
  );
  test(
    'capacity rejects excess calls and close settles accepted calls',
    () async {
      final a = expectLater(
        client.get(endpoint.resolve('/slow')),
        throwsA(isA<http.ClientException>()),
      );
      final b = expectLater(
        client.get(endpoint.resolve('/slow')),
        throwsA(isA<http.ClientException>()),
      );
      await expectLater(
        client.get(endpoint),
        throwsA(isA<http.ClientException>()),
      );
      client.close();
      await Future.wait([a, b]).timeout(const Duration(seconds: 2));
    },
  );
  test(
    'formal Luxo HTTP transport uses H2 and cancels timed out calls',
    () async {
      final transport = HttpTransport(
        endpoint.resolve('/slow').toString(),
        client: client,
        timeout: const Duration(milliseconds: 100),
        options: const TransportOptions(mode: TransportMode.binary),
      )..setSchema({'echo': APISchemaEntry(1, [])});
      await expectLater(
        transport.call<Object?>('echo'),
        throwsA(
          isA<LuxoError>().having((e) => e.error, 'error', 'TimeoutError'),
        ),
      );
      expect((await client.post(endpoint, body: [8])).bodyBytes, [8]);
      final echo = HttpTransport(
        endpoint.toString(),
        client: client,
        options: const TransportOptions(mode: TransportMode.binary),
      )..setSchema({'echo': APISchemaEntry(1, [])});
      expect(await echo.call<Uint8List>('echo'), [1, 0, 0]);
      transport.close();
    },
  );
}
