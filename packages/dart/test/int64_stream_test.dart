@TestOn('vm')
library;

import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:luxo_client/luxo_client.dart';
import 'package:luxo_client/src/transport.dart' show wsTransportWithSequence;
import 'package:test/test.dart';

import 'support/websocket_id_cases.dart';

void main() {
  test(
    'JSON validates call and subscription integers before writing frames',
    () async {
      var sent = 0;
      await _withRawSocket(
        TransportMode.json,
        (socket, message) {
          sent++;
          final body = decodeLuxoJSON(message as String) as Map;
          socket.add(
            body.containsKey(r'$sub')
                ? '{"\$sub":${body[r'$id']},"ok":true}'
                : '{"\$id":${body[r'$id']},"data":true}',
          );
        },
        (transport) async {
          transport.setSchema({
            'echo': const APISchemaEntry(7, [ParamSchema(1, 'id', 'Int')]),
          });
          for (final subscription in [false, true]) {
            await expectLater(
              subscription
                  ? transport.subscribe(
                      'echo',
                      {'id': true},
                      (_) {},
                      decoder: (value) => value,
                    )
                  : transport.call<Object>('echo', params: {'id': true}),
              throwsA(
                isA<LuxoError>().having((e) => e.error, 'error', 'ConfigError'),
              ),
            );
          }
          expect(sent, 0);
        },
      );
    },
  );

  for (final mode in TransportMode.values) {
    test(
      '$mode retired call leaves other calls usable on real socket',
      () async {
        final peers = <WebSocket>{};
        final ids = <int>[];
        await _withRawSocket(
          mode,
          (socket, message) {
            peers.add(socket);
            final id = mode == TransportMode.json
                ? ((decodeLuxoJSON(message as String) as Map)[r'$id'] as num)
                      .toInt()
                : _readUnsigned(
                    Uint8List.fromList(message as List<int>),
                    1,
                  ).$1.toInt();
            ids.add(id);
            if (id != 3) return;
            if (mode == TransportMode.json) {
              socket.add(r'{"$id":1,"data":999}');
              socket.add(
                r'{"$id":1,"error":"Gone","code":400,"message":"late"}',
              );
            } else {
              socket.add([BinaryFrameType.callSuccess, 1, 99]);
              socket.add([BinaryFrameType.callError, 1, 255]);
            }
            for (final response in [(3, 84), (2, 42)]) {
              if (mode == TransportMode.json) {
                socket.add('{"\$id":${response.$1},"data":${response.$2}}');
              } else {
                final enc = LuxoEncoder()
                  ..writeSvarint(LuxoInt64.fromInt(response.$2));
                socket.add([
                  BinaryFrameType.callSuccess,
                  response.$1,
                  ...enc.bytes(),
                ]);
              }
            }
          },
          (transport) async {
            Future<LuxoInt64> call() => transport.call(
              'echo',
              decoder: (value) => value is Uint8List
                  ? LuxoDecoder(value).readIntChecked()
                  : luxoInt64FromJSON(value),
            );
            await expectLater(
              call(),
              throwsA(
                isA<LuxoError>().having(
                  (error) => error.error,
                  'error',
                  'TimeoutError',
                ),
              ),
            );
            final results = await Future.wait([call(), call()]);
            expect(results, [LuxoInt64.fromInt(42), LuxoInt64.fromInt(84)]);
            expect(ids, [1, 2, 3]);
            expect(peers, hasLength(1));
          },
        );
      },
    );
  }

  for (final mode in TransportMode.values) {
    for (final subscription in [false, true]) {
      test(
        '$mode malformed error completes ${subscription ? 'subscription' : 'call'}',
        () async {
          await _withRawSocket(
            mode,
            (socket, message) {
              if (mode == TransportMode.json) {
                socket.add(
                  '{"${subscription ? r'$sub' : r'$id'}":1,'
                  '"error":"Rejected","code":9223372036854775807,"message":"invalid"}',
                );
              } else {
                final payload = LuxoEncoder()
                  ..writeFieldInt(1, LuxoInt64.maxValue)
                  ..writeFieldString(2, 'Rejected')
                  ..writeFieldString(3, 'invalid')
                  ..writeEnd();
                socket.add([
                  subscription
                      ? BinaryFrameType.subscribeError
                      : BinaryFrameType.callError,
                  1,
                  ...payload.bytes(),
                ]);
              }
            },
            (transport) async {
              await expectLater(
                subscription
                    ? transport.subscribe(
                        'echo',
                        const {},
                        (_) {},
                        decoder: (value) => value,
                      )
                    : transport.call<Object>('echo'),
                throwsA(
                  isA<LuxoError>().having(
                    (e) => e.error,
                    'error',
                    'ParseError',
                  ),
                ),
              );
            },
          );
        },
      );
    }
  }

  test('VM preserves full-width WebSocket IDs on real connections', () async {
    await _withRawSocket(TransportMode.json, (socket, message) {
      _respond(
        socket,
        message,
        message is String ? TransportMode.json : TransportMode.binary,
        LuxoInt64.maxValue,
      );
    }, (transport) => verifyWebSocketIDs(transport.endpoint));
  });

  for (final mode in TransportMode.values) {
    test('$mode exhaustion preserves the pending maximum-ID error response', () async {
      final received = Completer<WebSocket>();
      final maximum = BigInt.parse('18446744073709551615');
      await _withRawSocket(
        mode,
        (socket, message) {
          final id = message is String
              ? BigInt.parse(
                  (decodeLuxoJSON(message) as Map)[r'$id'].toString(),
                )
              : _readUnsigned(Uint8List.fromList(message as List<int>), 1).$1;
          expect(id, maximum);
          received.complete(socket);
        },
        (transport) async {
          final pending = expectLater(
            transport.call<Object>('echo'),
            throwsA(
              isA<LuxoError>()
                  .having((e) => e.error, 'error', 'NotFound')
                  .having((e) => e.code, 'code', 404),
            ),
          );
          final socket = await received.future;
          for (var i = 0; i < 2; i++) {
            await expectLater(
              transport.call<Object>('echo'),
              throwsA(
                isA<LuxoError>().having(
                  (e) => e.error,
                  'error',
                  'RequestIDExhausted',
                ),
              ),
            );
          }
          if (mode == TransportMode.json) {
            socket.add('{"\$id":-1,"data":1}');
            socket.add(
              '{"\$id":$maximum,"error":"NotFound","code":404,"message":"missing"}',
            );
          } else {
            final payload = LuxoEncoder()
              ..writeFieldInt(1, LuxoInt64.fromInt(404))
              ..writeFieldString(2, 'NotFound')
              ..writeFieldString(3, 'missing')
              ..writeEnd();
            socket.add([
              BinaryFrameType.callError,
              ..._unsignedWire(maximum),
              ...payload.bytes(),
            ]);
          }
          await pending;
        },
        initialSequence: maximum - BigInt.one,
      );
    });

    test('$mode matches full-width out-of-order responses', () async {
      final received = <BigInt>[];
      await _withRawSocket(
        mode,
        (socket, message) {
          final id = message is String
              ? BigInt.parse(
                  (decodeLuxoJSON(message) as Map)[r'$id'].toString(),
                )
              : _readUnsigned(Uint8List.fromList(message as List<int>), 1).$1;
          received.add(id);
          if (received.length != 2) return;
          for (var i = 1; i >= 0; i--) {
            socket.add(
              mode == TransportMode.json
                  ? '{"\$id":${received[i]},"data":${i + 1}}'
                  : [
                      BinaryFrameType.callSuccess,
                      ..._unsignedWire(received[i]),
                      i + 1,
                    ],
            );
          }
        },
        (transport) async {
          final responses = await Future.wait([
            transport.call<Object>('echo'),
            transport.call<Object>('echo'),
          ]);
          expect(received.map((id) => id.toString()), [
            '9223372036854775807',
            '9223372036854775808',
          ]);
          expect(
            responses,
            mode == TransportMode.json
                ? [1, 2]
                : [
                    [1],
                    [2],
                  ],
          );
        },
        initialSequence: BigInt.parse('9223372036854775806'),
      );
    });
  }

  test('JSON call parameters cannot override request metadata', () async {
    await _withRawSocket(
      TransportMode.json,
      (socket, message) {
        final request =
            decodeLuxoJSON(message as String) as Map<String, Object?>;
        socket.add(
          '{"\$id":1,"data":${request[r'$id'] == 1 && request[r'$api'] == 'echo'}}',
        );
      },
      (transport) async {
        expect(
          await transport.call<bool>(
            'echo',
            params: {r'$id': 7, r'$api': 'wrong'},
          ),
          isTrue,
        );
      },
    );
  });

  test(
    'truncated binary IDs retire the connection before later replies',
    () async {
      await _withRawSocket(
        TransportMode.binary,
        (socket, message) {
          socket.add([BinaryFrameType.callSuccess, 0x81]);
          socket.add([BinaryFrameType.callSuccess, 1, 42]);
        },
        (transport) async {
          await expectLater(
            transport.call<Uint8List>('echo'),
            throwsA(
              isA<LuxoError>().having(
                (error) => error.error,
                'error',
                'ParseError',
              ),
            ),
          );
        },
      );
    },
  );

  test(
    'overflowing and malformed binary headers retire pending requests',
    () async {
      for (final bytes in <List<int>>[
        [],
        [2],
        [2, 0x81],
        [0x82, 0, 1, 1],
        [255, 1, 1],
        [2, 0x81, ...List.filled(8, 128), 2, 1],
        [2, 0x81, ...List.filled(9, 128), 0, 1],
      ]) {
        await _withRawSocket(
          TransportMode.binary,
          (socket, message) {
            socket.add(bytes);
            socket.add([2, 1, 42]);
          },
          (transport) async {
            await expectLater(
              transport.call<Uint8List>('echo'),
              throwsA(
                isA<LuxoError>().having(
                  (error) => error.error,
                  'error',
                  'ParseError',
                ),
              ),
              reason: '$bytes',
            );
          },
        );
      }
    },
  );

  test('invalid JSON ID values cannot resolve a pending request', () async {
    await _withRawSocket(
      TransportMode.json,
      (socket, message) {
        for (final id in [
          'true',
          'null',
          '"1"',
          '1.0',
          '1e0',
          '-0',
          '-1',
          '[]',
          '{}',
          '18446744073709551616',
        ]) {
          socket.add('{"\$id":$id,"data":1}');
        }
        socket.add('{"data":1}');
        socket.add('{"\$id":1,"data":42}');
      },
      (transport) async {
        expect(await transport.call<int>('echo'), 42);
      },
    );
  });

  test('binary subscription acknowledgement requires empty payload', () async {
    await _withRawSocket(
      TransportMode.binary,
      (socket, message) {
        final bytes = message as List<int>;
        socket.add(
          bytes.first == BinaryFrameType.subscribe
              ? [BinaryFrameType.subscribeSuccess, bytes[1], 0]
              : [BinaryFrameType.callSuccess, bytes[1], 42],
        );
      },
      (transport) async {
        await expectLater(
          transport.subscribe('echo', {}, (_) {}, decoder: (value) => value),
          throwsA(
            isA<LuxoError>().having((e) => e.error, 'error', 'ParseError'),
          ),
        );
        expect(await transport.call<Uint8List>('echo'), [42]);
      },
    );
  });

  test(
    'invalid subscription acknowledgement cannot start a subscription',
    () async {
      await _withRawSocket(
        TransportMode.json,
        (socket, message) {
          final request =
              decodeLuxoJSON(message as String) as Map<String, Object?>;
          if (!request.containsKey(r'$sub')) return;
          socket.add('{"\$sub":${request[r'$id']},"ok":false}');
        },
        (transport) async {
          await expectLater(
            transport.subscribe('echo', {}, (_) {}, decoder: (value) => value),
            throwsA(
              isA<LuxoError>().having(
                (error) => error.error,
                'error',
                'TimeoutError',
              ),
            ),
          );
        },
      );
    },
  );

  for (final mode in TransportMode.values) {
    test(
      'WebSocket $mode keeps int64 calls and stream payloads exact',
      () async {
        final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
        final sockets = <WebSocket>[];
        final value = LuxoInt64.maxValue;
        server.listen((request) async {
          final socket = await WebSocketTransformer.upgrade(request);
          sockets.add(socket);
          socket.listen((message) => _respond(socket, message, mode, value));
        });
        final transport = WsTransport(
          'ws://${server.address.host}:${server.port}',
          options: TransportOptions(mode: mode),
          autoReconnect: false,
          timeout: const Duration(seconds: 3),
        );
        transport.setSchema({
          'echo': const APISchemaEntry(7, [ParamSchema(1, 'value', 'Int')]),
        });
        LuxoInt64 decode(Object? data) => data is Uint8List
            ? LuxoDecoder(data).readIntChecked()
            : luxoInt64FromJSON(data);
        try {
          final result = await transport.call(
            'echo',
            params: {'value': value},
            decoder: decode,
          );
          expect(result, value);
          final received = Completer<LuxoInt64>();
          final unsubscribe = await transport.subscribe(
            'echo',
            {'value': value},
            (data) => received.complete(decode(data)),
            decoder: (value) => value,
          );
          expect(
            await received.future.timeout(const Duration(seconds: 3)),
            value,
          );
          unsubscribe();
        } finally {
          transport.close();
          for (final socket in sockets) await socket.close();
          await server.close(force: true);
        }
      },
    );
  }
  for (final compiler in ['js', 'wasm']) {
    test(
      'compiled $compiler WebSocket preserves integers and reports disconnects',
      () async {
        final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
        final sockets = <WebSocket>[];
        final temporaryRoot = Directory('../../.tmp')
          ..createSync(recursive: true);
        final directory = temporaryRoot.createTempSync('dart-web-socket-');
        try {
          server.listen((request) async {
            if (request.uri.path == '/stalled-handshake') return;
            final socket = await WebSocketTransformer.upgrade(request);
            sockets.add(socket);
            socket.listen((message) {
              if (request.uri.path == '/missing-binary-body') {
                for (final reply in binaryPresenceReplies(message))
                  socket.add(reply);
                return;
              }
              _respond(
                socket,
                message,
                message is String ? TransportMode.json : TransportMode.binary,
                LuxoInt64.maxValue,
              );
            });
          });
          final entry = File('${directory.path}/consumer.dart');
          entry.writeAsStringSync(
            File('test/fixtures/socket_consumer.dart.txt')
                .readAsStringSync()
                .replaceAll(
                  '__HTTP_REFRESH_CASES__',
                  File('test/support/http_refresh_cases.dart').absolute.uri
                      .toString(),
                )
                .replaceAll(
                  '__WEBSOCKET_ID_CASES__',
                  File('test/support/websocket_id_cases.dart').absolute.uri
                      .toString(),
                ),
          );
          final output = '${directory.absolute.path}/consumer.$compiler';
          final compiled = await Process.run(Platform.resolvedExecutable, [
            'compile',
            compiler,
            '--packages=${File('.dart_tool/package_config.json').absolute.path}',
            entry.absolute.path,
            '-o',
            output,
          ]);
          expect(
            compiled.exitCode,
            0,
            reason: '${compiled.stdout}\n${compiled.stderr}',
          );
          final program = _webRunner(directory, compiler);
          final result = await Process.run('node', [
            program,
            'ws://${server.address.host}:${server.port}',
          ]);
          expect(
            result.exitCode,
            0,
            reason: '${result.stdout}\n${result.stderr}',
          );
          expect(result.stdout, contains('web socket protocol verified'));
        } finally {
          for (final socket in sockets) await socket.close();
          await server.close(force: true);
          final root = temporaryRoot.resolveSymbolicLinksSync();
          if (directory.resolveSymbolicLinksSync().startsWith(
            '$root${Platform.pathSeparator}',
          )) {
            directory.deleteSync(recursive: true);
          }
        }
      },
      timeout: const Timeout(Duration(seconds: 90)),
    );
  }
}

Future<void> _withRawSocket(
  TransportMode mode,
  void Function(WebSocket socket, Object? message) respond,
  Future<void> Function(WsTransport transport) verify, {
  Object initialSequence = 0,
}) async {
  final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
  final sockets = <WebSocket>[];
  server.listen((request) async {
    final socket = await WebSocketTransformer.upgrade(request);
    sockets.add(socket);
    socket.listen((message) => respond(socket, message));
  });
  final transport = wsTransportWithSequence(
    'ws://${server.address.host}:${server.port}',
    initialSequence,
    options: TransportOptions(mode: mode),
    timeout: const Duration(milliseconds: 250),
  );
  transport.setSchema({'echo': const APISchemaEntry(7)});
  try {
    await verify(transport);
  } finally {
    transport.close();
    for (final socket in sockets) await socket.close();
    await server.close(force: true);
  }
}

String _webRunner(Directory directory, String compiler) {
  // Inject a browser-adapter event fault, not a simulated TCP server response.
  const socketFault = '''
const NativeWebSocket = globalThis.WebSocket;
globalThis.WebSocket = class extends NativeWebSocket {
  droppedSubscription = false;
  failedCancellation = false;
  send(data) {
    if (this.url.endsWith('/call-send-fault')) {
      if (!this.failedCall) {
        this.failedCall = true;
        throw new Error('injected call send failure');
      }
      const json = typeof data === 'string' ? JSON.parse(data) : null;
      const bytes = json ? null : new Uint8Array(data.buffer ?? data, data.byteOffset ?? 0, data.byteLength);
      if ((json ? json.\$id : bytes[1]) !== 2) throw new Error('unexpected request identity');
      const late = json ? '{"\$id":1,"error":"Gone","code":400,"message":"late"}' : new Uint8Array([3, 1, 255]).buffer;
      this.dispatchEvent(new MessageEvent('message', { data: late }));
    }
    if (this.url.endsWith('/timeout-cancel')) {
      const json = typeof data === 'string' ? JSON.parse(data) : null;
      const tag = json ? 0 : new Uint8Array(data.buffer ?? data, data.byteOffset ?? 0, data.byteLength)[0];
      if ((json ? json.\$sub !== undefined : tag === 0x10) && !this.droppedSubscription) {
        this.droppedSubscription = true;
        return;
      }
      if ((json ? json.\$unsub !== undefined : tag === 0x11) && !this.failedCancellation) {
        this.failedCancellation = true;
        throw new Error('cancellation send failed');
      }
    }
    if (this.url.endsWith('/adapter-fault')) {
      queueMicrotask(() => this.dispatchEvent(new MessageEvent('message', { data: 42 })));
    } else {
      super.send(data);
    }
  }
};
''';
  if (compiler == 'js') {
    final runner = File('${directory.path}/run.cjs');
    // dart2js targets browsers; mirror the test runner's Node global alias.
    runner.writeAsStringSync(
      "$socketFault\nglobalThis.self = globalThis;\nrequire('./consumer.js');\n",
    );
    return runner.absolute.path;
  }
  final runner = File('${directory.path}/run.mjs');
  runner.writeAsStringSync('''
$socketFault
import { readFile } from 'node:fs/promises';
import { compile } from './consumer.mjs';
const compiled = await compile(await readFile(new URL('./consumer.wasm', import.meta.url)));
const instance = await compiled.instantiate({});
instance.invokeMain();
''');
  return runner.absolute.path;
}

void _respond(
  WebSocket socket,
  Object? message,
  TransportMode mode,
  LuxoInt64 value,
) {
  if (mode == TransportMode.json) {
    final request = decodeLuxoJSON(message as String) as Map<String, Object?>;
    if (request.containsKey(r'$unsub')) return;
    if (request[r'$api'] == 'disconnect') {
      unawaited(socket.close());
      return;
    }
    if (request[r'$api'] == 'malformed') {
      socket.add(r'{"$id":3,"data":');
      return;
    }
    expect(luxoInt64FromJSON(request['value']), value);
    if (request.containsKey(r'$sub')) {
      socket.add('{"\$sub":${request[r'$id']},"ok":true}');
      socket.add('{"\$stream":${request[r'$id']},"data":9223372036854775807}');
    } else {
      socket.add('{"\$id":${request[r'$id']},"data":9223372036854775807}');
    }
    return;
  }
  final bytes = Uint8List.fromList(message as List<int>);
  final frame = bytes[0];
  if (frame == BinaryFrameType.unsubscribe) return;
  final header = _readUnsigned(bytes, 1);
  final id = header.$1;
  final decoder = LuxoDecoder(Uint8List.sublistView(bytes, header.$2));
  expect(decoder.nextField(), isTrue);
  if (decoder.fieldID == 8) {
    unawaited(socket.close());
    return;
  }
  if (decoder.fieldID == 9) {
    socket.add(r'{"$id":3,"data":');
    return;
  }
  expect(decoder.nextField(), isFalse); // Empty selection mask.
  expect(decoder.nextField(), isTrue);
  expect(decoder.readInt(), value);
  if (frame == BinaryFrameType.subscribe) {
    socket.add([BinaryFrameType.subscribeSuccess, ..._unsignedWire(id)]);
  }
  final response = LuxoEncoder()..writeSvarint(value);
  socket.add([
    frame == BinaryFrameType.subscribe
        ? BinaryFrameType.stream
        : BinaryFrameType.callSuccess,
    ..._unsignedWire(id),
    ...response.bytes(),
  ]);
}

/// Independent test oracle; production never uses per-byte BigInt arithmetic.
(BigInt, int) _readUnsigned(Uint8List bytes, int offset) {
  var value = BigInt.zero;
  for (var shift = 0; shift < 70; shift += 7) {
    final byte = bytes[offset++];
    value |= BigInt.from(byte & 127) << shift;
    if (byte < 128) return (value, offset);
  }
  throw StateError('invalid fixture varint');
}

List<int> _unsignedWire(BigInt value) {
  final bytes = <int>[];
  do {
    var byte = (value & BigInt.from(127)).toInt();
    value >>= 7;
    if (value != BigInt.zero) byte |= 128;
    bytes.add(byte);
  } while (value != BigInt.zero);
  return bytes;
}
