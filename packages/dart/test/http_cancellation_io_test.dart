@TestOn('vm')
library;

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:isolate';
import 'dart:typed_data';

import 'package:luxo_client/luxo_client.dart';
import 'package:test/test.dart';

void main() {
  test('encoded HTTP calls release original inputs before the reply', () async {
    final config = (await Isolate.packageConfig)!.toFilePath();
    final fixture = File('test/fixtures/request_lifetime.dart').absolute;
    expect(fixture.existsSync(), isTrue);
    final child = await Process.start(Platform.resolvedExecutable, [
      '--enable-vm-service=0',
      '--packages=$config',
      fixture.path,
    ]);
    final output = utf8.decoder.bind(child.stdout).join();
    final errors = utf8.decoder.bind(child.stderr).join();
    var reaped = false;
    try {
      final code = await child.exitCode.timeout(const Duration(seconds: 15));
      reaped = true;
      final text = await output;
      expect(code, 0, reason: '$text\n${await errors}');
      const prefix = 'REQUEST_LIFETIME_RESULT ';
      final line = text
          .split('\n')
          .singleWhere((line) => line.startsWith(prefix));
      final result =
          jsonDecode(line.substring(prefix.length)) as Map<String, Object?>;
      expect(result['complete'], isTrue);
      expect(result['calls'], 4);
      expect(result['owned_server_closed'], isTrue);
      expect(result['owned_service_closed'], isTrue);
      final cases = (result['cases'] as List<Object?>)
          .cast<Map<String, Object?>>();
      expect(cases.length, 4);
      for (final value in cases) {
        expect(value['gc_controls'], isTrue);
        expect(value['before_response'], {
          'input': false,
          'bytes': false,
        }, reason: '$value');
        expect(value['after_completion'], {
          'input': false,
          'bytes': false,
        }, reason: '$value');
      }
    } finally {
      if (!reaped) {
        child.kill(ProcessSignal.sigkill);
        await child.exitCode.timeout(const Duration(seconds: 5));
      }
    }
  });

  for (final mode in TransportMode.values) {
    for (final generated in [false, true]) {
      for (final scenario in _ReplayScenario.values) {
        test(
          'real HTTP POST replay $mode $scenario generated=$generated',
          () async {
            await _verifyPostReplay(mode, generated, scenario);
          },
        );
      }
    }
  }
  test(
    'business redirects never forward credentials or request bodies',
    _verifyRedirectIsolation,
  );
  test(
    'oversized unfinished HTTP response cancels without closing siblings',
    () async {
      final server = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
      final received = Completer<void>();
      final disconnected = Completer<void>();
      final sockets = <Socket>[];
      var calls = 0;
      final listener = server.listen((socket) {
        sockets.add(socket);
        var answered = false;
        var oversized = false;
        socket.listen(
          (_) {
            if (answered) return;
            answered = true;
            oversized = calls++ == 0;
            if (oversized) {
              socket.write(
                'HTTP/1.1 200 OK\r\nContent-Type: application/x-luxo\r\nTransfer-Encoding: chunked\r\n\r\n41\r\n',
              );
              socket.add(Uint8List(65));
              socket.write('\r\n');
              received.complete();
            } else {
              socket.write(
                'HTTP/1.1 200 OK\r\nContent-Type: application/x-luxo\r\nContent-Length: 1\r\nConnection: close\r\n\r\n',
              );
              socket.add([42]);
              unawaited(socket.close());
            }
          },
          onDone: () {
            socket.destroy();
            if (oversized && !disconnected.isCompleted) disconnected.complete();
          },
        );
      });
      final transport = HttpTransport(
        'http://127.0.0.1:${server.port}/luvia',
        options: const TransportOptions(
          mode: TransportMode.binary,
          maxResponseBytes: 64,
        ),
        timeout: const Duration(seconds: 3),
      )..setSchema({'echo': APISchemaEntry(1, [])});
      addTearDown(() async {
        transport.close();
        for (final socket in sockets) {
          socket.destroy();
        }
        await listener.cancel();
        await server.close();
      });
      final rejected = expectLater(
        transport.call<Uint8List>('echo'),
        throwsA(
          isA<LuxoError>().having(
            (error) => error.error,
            'error',
            'ParseError',
          ),
        ),
      );
      await received.future.timeout(const Duration(seconds: 2));
      expect(await transport.call<Uint8List>('echo'), [42]);
      await rejected;
      await disconnected.future.timeout(const Duration(seconds: 2));
      expect(await transport.call<Uint8List>('echo'), [42]);
    },
  );
  for (final mode in TransportMode.values) {
    test('native $mode timeout releases the real socket', () async {
      final server = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
      final disconnected = Completer<void>();
      final received = Completer<void>();
      final sockets = <Socket>[];
      final listener = server.listen((socket) {
        sockets.add(socket);
        socket.listen(
          (_) {
            if (!received.isCompleted) received.complete();
          },
          onDone: () {
            socket.destroy();
            if (!disconnected.isCompleted) disconnected.complete();
          },
        );
      });
      final transport = HttpTransport(
        'http://127.0.0.1:${server.port}/luvia',
        options: TransportOptions(mode: mode),
        timeout: const Duration(milliseconds: 250),
      )..setSchema({'echo': APISchemaEntry(1, [])});
      addTearDown(() async {
        transport.close();
        for (final socket in sockets) {
          socket.destroy();
        }
        await listener.cancel();
        await server.close();
      });
      final ended = expectLater(
        transport.call<Object?>('echo'),
        throwsA(
          isA<LuxoError>().having(
            (error) => error.error,
            'error',
            'TimeoutError',
          ),
        ),
      );
      await received.future.timeout(const Duration(seconds: 2));
      await ended;
      // Verify cancellation before close() can release the connection.
      await disconnected.future.timeout(const Duration(seconds: 2));
    });
  }
}

enum _ReplayScenario { timeout, unavailable, lostResponse, refresh }

typedef _ReplayCapture = ({Uint8List body, String? token, int? port});
typedef _ReplayPeer = ({
  HttpServer server,
  StreamSubscription<HttpRequest> listener,
});

class _ReplayParameters extends GeneratedRequest {
  @override
  int get apiID => 1;
  @override
  void writeParameters(LuxoEncoder encoder) {}
  @override
  Map<String, Object?> toJSONParameters() => {};
}

Future<void> _verifyPostReplay(
  TransportMode mode,
  bool generated,
  _ReplayScenario scenario,
) async {
  final requests = <_ReplayCapture>[];
  final peer = await _replayPeer(requests, mode, scenario);
  var refreshes = 0;
  final transport = HttpTransport(
    'http://127.0.0.1:${peer.server.port}/api',
    options: TransportOptions(mode: mode, token: 'original'),
    timeout: const Duration(seconds: 3),
    onTokenExpired: () async {
      refreshes++;
      return 'renewed';
    },
  )..setSchema({'echo': APISchemaEntry(1, [])});
  final parameters = generated ? _ReplayParameters() : null;
  Future<Object?> call() =>
      transport.call<Object?>('echo', request: parameters);
  try {
    await _assertReplayPolicy(scenario, call, requests);
    expect(refreshes, scenario == _ReplayScenario.refresh ? 1 : 0);
    final previous = requests.length;
    await call();
    expect(requests, hasLength(previous + 1));
  } finally {
    transport.close();
    await peer.listener.cancel();
    await peer.server.close(force: true);
  }
}

Future<void> _assertReplayPolicy(
  _ReplayScenario scenario,
  Future<Object?> Function() call,
  List<_ReplayCapture> requests,
) async {
  if (scenario == _ReplayScenario.lostResponse) {
    await call();
    await expectLater(
      call(),
      throwsA(isA<LuxoError>().having((e) => e.error, 'error', 'NetworkError')),
    );
    expect(requests, hasLength(2));
    expect(requests[1].port, requests[0].port);
  } else if (scenario == _ReplayScenario.refresh) {
    await call();
    expect(requests, hasLength(2));
    expect(requests.map((request) => request.token), [
      'Bearer original',
      'Bearer renewed',
    ]);
    expect(requests[1].body, requests[0].body);
  } else {
    final code = scenario == _ReplayScenario.timeout ? 408 : 503;
    await expectLater(
      call(),
      throwsA(
        isA<LuxoError>()
            .having((e) => e.error, 'error', 'Unavailable')
            .having((e) => e.code, 'code', code),
      ),
    );
    expect(requests, hasLength(1));
  }
}

Future<_ReplayPeer> _replayPeer(
  List<_ReplayCapture> requests,
  TransportMode mode,
  _ReplayScenario scenario,
) async {
  final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
  final listener = server.listen((request) async {
    final port = request.connectionInfo?.remotePort;
    final body = BytesBuilder(copy: false);
    await for (final chunk in request) {
      body.add(chunk);
    }
    requests.add((
      body: body.takeBytes(),
      token: request.headers.value(HttpHeaders.authorizationHeader),
      port: port,
    ));
    if (scenario == _ReplayScenario.lostResponse && requests.length == 2) {
      final socket = await request.response.detachSocket(writeHeaders: false);
      socket.destroy();
      return;
    }
    final status =
        requests.length != 1 || scenario == _ReplayScenario.lostResponse
        ? 200
        : switch (scenario) {
            _ReplayScenario.timeout => 408,
            _ReplayScenario.unavailable => 503,
            _ => 401,
          };
    request.response.statusCode = status;
    request.response.headers.set('Retry-After', '0');
    request.response.add(_replayResponse(mode, status));
    await request.response.close();
  });
  return (server: server, listener: listener);
}

List<int> _replayResponse(TransportMode mode, int status) {
  final name = status == 401 ? 'Unauthorized' : 'Unavailable';
  if (mode == TransportMode.json) {
    return utf8.encode(
      status == 200
          ? '{"data":42}'
          : '{"error":"$name","code":$status,"message":"retry later"}',
    );
  }
  if (status == 200) return [84];
  return (LuxoEncoder()
        ..writeFieldInt(1, LuxoInt64.fromInt(status))
        ..writeFieldString(2, name)
        ..writeFieldString(3, 'retry later')
        ..writeEnd())
      .bytes();
}

Future<void> _verifyRedirectIsolation() async {
  final target = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
  final source = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
  var targetRequests = 0, status = 303;
  var location = '';
  var expireNext = false, sourceRequests = 0;
  final targetListener = target.listen((request) async {
    targetRequests++;
    await request.drain<void>();
    request.response.write('{"data":true}');
    await request.response.close();
  });
  final sourceListener = source.listen((request) async {
    sourceRequests++;
    await request.drain<void>();
    if (expireNext) {
      expireNext = false;
      request.response.statusCode = 401;
      if (request.headers.contentType?.mimeType == 'application/json') {
        request.response.headers.contentType = ContentType.json;
        request.response.write(
          '{"error":"Unauthorized","code":401,"message":"expired"}',
        );
      } else {
        final encoder = LuxoEncoder()
          ..writeFieldInt(1, LuxoInt64.fromInt(401))
          ..writeFieldString(2, 'Unauthorized')
          ..writeFieldString(3, 'expired')
          ..writeEnd();
        request.response.add(encoder.bytes());
      }
      await request.response.close();
      return;
    }
    if (request.uri.path == '/capture') {
      targetRequests++;
      request.response.write('{"data":true}');
    } else {
      request.response.statusCode = status;
      request.response.headers.set(HttpHeaders.locationHeader, location);
    }
    await request.response.close();
  });
  try {
    for (final mode in TransportMode.values) {
      var refreshes = 0;
      final transport = HttpTransport(
        'http://127.0.0.1:${source.port}/api',
        options: TransportOptions(
          mode: mode,
          headers: {'X-Project-Key': 'test-only-key'},
        ),
        onTokenExpired: () async {
          refreshes++;
          return 'unused';
        },
      );
      transport.setSchema({
        'echo': APISchemaEntry(1, [ParamSchema(1, 'value', 'String')]),
      });
      try {
        for (final code in [303, 301, 302, 307, 308]) {
          status = code;
          for (final destination in [
            '/capture',
            'http://127.0.0.1:${target.port}/capture',
          ]) {
            location = destination;
            await expectLater(
              transport.call<Object?>(
                'echo',
                params: {'value': 'private-input'},
              ),
              throwsA(
                isA<LuxoError>().having(
                  (error) => error.code,
                  'status',
                  status,
                ),
              ),
            );
            expect(targetRequests, 0);
          }
        }
        expect(refreshes, 0);
        for (final code in [301, 302, 303, 307, 308]) {
          status = code;
          for (final destination in [
            '/capture',
            'http://127.0.0.1:${target.port}/capture',
          ]) {
            location = destination;
            expireNext = true;
            final beforeRequests = sourceRequests;
            final beforeRefreshes = refreshes;
            await expectLater(
              transport.call<Object?>(
                'echo',
                params: {'value': 'private-input'},
              ),
              throwsA(
                isA<LuxoError>().having((error) => error.code, 'status', code),
              ),
            );
            expect(refreshes, beforeRefreshes + 1);
            expect(sourceRequests, beforeRequests + 2);
            expect(targetRequests, 0);
          }
        }
      } finally {
        transport.close();
      }
    }
  } finally {
    await source.close(force: true);
    await target.close(force: true);
    await sourceListener.cancel();
    await targetListener.cancel();
  }
}
