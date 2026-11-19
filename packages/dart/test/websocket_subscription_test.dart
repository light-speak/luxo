@TestOn('vm')
library;

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:luxo_client/luxo_client.dart';
import 'package:luxo_client/src/socket.dart' show connectSocket;
import 'package:luxo_client/src/transport.dart' show wsTransportWithSequence;
import 'package:test/test.dart';

import 'support/websocket_id_cases.dart';
import 'support/schema_request_cases.dart';

class _UnreadableDecoderFailure {
  @override
  String toString() => throw StateError('exception formatting failed');
}

class _UnreadableTypedDecoderFailure extends LuxoError {
  const _UnreadableTypedDecoderFailure()
    : super('RangeError', 0, 'invalid sample');
  @override
  String get message => throw StateError('exception message accessor failed');
}

const _handshakeTimeout = Duration(milliseconds: 100);

class _ControlledTimer implements Timer {
  final void Function() _callback;
  bool _active = true;
  int _tick = 0;

  _ControlledTimer(this._callback);

  void expire() {
    if (!_active) return;
    _active = false;
    _tick = 1;
    _callback();
  }

  @override
  void cancel() => _active = false;
  @override
  bool get isActive => _active;
  @override
  int get tick => _tick;
}

Future<void> _withTransportDeadlines(
  Duration timeout,
  Future<void> Function(List<_ControlledTimer>) run,
) {
  final deadlines = <_ControlledTimer>[];
  return runZoned(
    () => run(deadlines),
    zoneSpecification: ZoneSpecification(
      createTimer: (self, parent, zone, duration, callback) {
        if (duration <= Duration.zero ||
            duration > timeout ||
            deadlines.any((timer) => timer.isActive)) {
          return parent.createTimer(zone, duration, callback);
        }
        // Each controlled phase owns one timer; native dial timers still run.
        final timer = _ControlledTimer(zone.bindCallbackGuarded(callback));
        deadlines.add(timer);
        return timer;
      },
    ),
  );
}

void main() {
  test(
    'WS accepts whitespace after response data on the real socket',
    () => _withTransportDeadlines(const Duration(milliseconds: 250), (
      deadlines,
    ) async {
      await _withSocket(
        TransportMode.json,
        (socket, message) {
          final fields = jsonDecode(message as String) as Map;
          socket.add(
            ' { "\$id":${fields[r'$id']}, "data": {"metadata":true} \r\n } \n',
          );
        },
        (transport) async {
          expect(await transport.call<Object?>('echo'), {'metadata': true});
        },
      );
      expect(deadlines, isNotEmpty);
      expect(deadlines.every((timer) => !timer.isActive), isTrue);
    }),
  );
  const nodeResponse = JSONResponseSchema(
    modelType: 'Node',
    types: {
      'Node': {
        'child': SelectionFieldSchema(1, 'Node', 'Model'),
        'metadata': SelectionFieldSchema(2, null, 'JSON'),
      },
    },
  );
  final json128 = '${'[' * 128}null${']' * 128}';
  final model65 = '${'{"child":' * 64}{}${'}' * 64}';
  for (final mode in TransportMode.values) {
    test(
      'WS correlated call errors retain generic data and peer lifecycle: $mode',
      () async {
        var first = true;
        await _withSocket(
          mode,
          (socket, message) {
            final id = message is String
                ? (jsonDecode(message) as Map)[r'$id'] as int
                : (message as List<int>)[1];
            if (first) {
              socket.add(
                mode == TransportMode.json
                    ? '{"data":$model65,"\$id":$id,"error":"Rejected","code":422,"message":"rejected"}'
                    : Uint8List.fromList([3, id, ..._terminalError()]),
              );
            } else {
              socket.add(
                mode == TransportMode.json
                    ? '{"\$id":$id,"data":42}'
                    : Uint8List.fromList([2, id, 42]),
              );
            }
            first = false;
          },
          (transport) async {
            transport.setSchema({
              'echo': const APISchemaEntry(7, [], {}, {}, nodeResponse),
            });
            await expectLater(
              transport.call<Object?>('echo'),
              throwsA(
                isA<LuxoError>()
                    .having((error) => error.error, 'error', 'Rejected')
                    .having((error) => error.data, 'data', isA<Map>()),
              ),
            );
            expect(await transport.call<int>('echo', decoder: _value), 42);
          },
        );
      },
    );
  }
  for (final subscription in [false, true]) {
    test(
      'WS response and request snapshots precede connection: sub=$subscription',
      () async {
        final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
        final entered = Completer<void>(), release = Completer<void>();
        final peers = <WebSocket>[];
        server.listen((request) async {
          entered.complete();
          await release.future;
          final socket = await WebSocketTransformer.upgrade(request);
          peers.add(socket);
          socket.listen((message) {
            expect(message, isA<String>());
            final fields = jsonDecode(message as String) as Map;
            if (fields.containsKey(r'$unsub')) return;
            final id = fields[r'$id'];
            if (subscription) socket.add('{"\$sub":$id,"ok":true}');
            socket.add(
              '{"data":{"metadata":$json128},"${subscription ? r'$stream' : r'$id'}":$id}',
            );
          });
        });
        final transport =
            WsTransport(
              'ws://${server.address.host}:${server.port}',
              autoReconnect: false,
            )..setSchema({
              'echo': const APISchemaEntry(7, [], {}, {}, nodeResponse),
            });
        final data = Completer<Object?>();
        try {
          final Future<Object?> pending = subscription
              ? transport.subscribe<Object?>(
                  'echo',
                  {},
                  data.complete,
                  decoder: (value) => value,
                )
              : transport.call<Object?>('echo');
          await entered.future;
          transport.setSchema({'echo': const APISchemaEntry(8)});
          transport.setMode(TransportMode.binary);
          release.complete();
          final result = await pending;
          if (subscription) {
            expect(await data.future, isA<Map>());
            (result as void Function())();
          } else {
            expect(result, isA<Map>());
          }
        } finally {
          transport.close();
          for (final socket in peers) await socket.close();
          await server.close(force: true);
        }
      },
    );
  }
  test(
    'WS pre-ACK and cancelled data is discarded using syntax only',
    () async {
      final cancelled = Completer<void>();
      final received = Completer<Object?>();
      await _withSocket(
        TransportMode.json,
        (socket, message) {
          final fields = jsonDecode(message as String) as Map;
          final id = fields[r'$id'];
          if (fields.containsKey(r'$unsub')) {
            final retired = fields[r'$unsub'];
            socket.add(
              '{"data":${'[' * 2000}0${']' * 2000},"\$stream":$retired}',
            );
            cancelled.complete();
          } else if (fields.containsKey(r'$sub')) {
            socket.add('{"data":{"metadata":$json128},"\$stream":$id}');
            socket.add('{"\$sub":$id,"ok":true}');
            socket.add('{"\$stream":$id,"data":{"metadata":true}}');
          } else {
            socket.add('{"\$id":$id,"data":true}');
          }
        },
        (transport) async {
          var deliveries = 0;
          final cancel = await transport.subscribe<Object?>('echo', {}, (
            value,
          ) {
            deliveries++;
            received.complete(value);
          }, decoder: (value) => value);
          expect(await received.future, {'metadata': true});
          cancel();
          await cancelled.future;
          expect(await transport.call<bool>('echo'), isTrue);
          expect(deliveries, 1);
        },
      );
    },
  );
  for (final route in [r'$id', r'$stream', r'$unknown']) {
    test('WS malformed syntax below model65 closes all work: $route', () async {
      await _withSocket(
        TransportMode.json,
        (socket, message) {
          final fields = jsonDecode(message as String) as Map;
          final id = fields[r'$id'];
          if (route == r'$stream') socket.add('{"\$sub":$id,"ok":true}');
          socket.add(
            '{"data":${'{"child":' * 65}{"metadata":[1,]}${'}' * 65},"$route":$id}',
          );
        },
        (transport) async {
          transport.setSchema({
            'echo': const APISchemaEntry(7, [], {}, {}, nodeResponse),
          });
          if (route == r'$stream') {
            final failed = Completer<LuxoError>();
            await transport.subscribe<Object?>(
              'echo',
              {},
              (_) => fail('invalid delivery'),
              decoder: (value) => value,
              onError: failed.complete,
            );
            expect((await failed.future).error, 'ParseError');
          } else {
            await expectLater(
              transport.call<Object?>('echo'),
              _error('ParseError'),
            );
          }
        },
      );
    });
  }
  for (final mode in TransportMode.values) {
    test(
      'WS wrong-mode call frame leaves the matching operation active: $mode',
      () async {
        await _withSocket(
          mode,
          (socket, message) {
            final id = message is String
                ? (jsonDecode(message) as Map)[r'$id']
                : (message as List)[1];
            socket.add(
              mode == TransportMode.json
                  ? Uint8List.fromList([2, id as int, 13])
                  : '{"data":13,"\$id":$id}',
            );
            socket.add(
              mode == TransportMode.json
                  ? '{"\$id":$id,"data":42}'
                  : Uint8List.fromList([2, id as int, 42]),
            );
          },
          (transport) async {
            expect(await transport.call<int>('echo', decoder: _value), 42);
          },
        );
      },
    );
  }
  test(
    'WS response schema survives replacement for calls and streams',
    () async {
      late WsTransport active;
      await _withSocket(
        TransportMode.json,
        (socket, message) {
          final fields = jsonDecode(message as String) as Map;
          if (fields.containsKey(r'$unsub')) return;
          active.setSchema({'echo': const APISchemaEntry(7)});
          final id = fields[r'$id'];
          if (fields.containsKey(r'$sub')) {
            socket.add('{"\$sub":$id,"ok":true}');
            socket.add('{"data":{"metadata":$json128},"\$stream":$id}');
          } else {
            socket.add('{"data":{"metadata":$json128},"\$id":$id}');
          }
        },
        (transport) async {
          active = transport;
          for (final decoder in [false, true]) {
            transport.setSchema({
              'echo': const APISchemaEntry(7, [], {}, {}, nodeResponse),
            });
            final value = await transport.call<Map>(
              'echo',
              decoder: decoder ? (value) => value as Map : null,
            );
            expect(value['metadata'], isA<List>());
          }
          transport.setSchema({
            'echo': const APISchemaEntry(7, [], {}, {}, nodeResponse),
          });
          final data = Completer<Map>();
          final cancel = await transport.subscribe<Map>(
            'echo',
            {},
            data.complete,
            decoder: (value) => value as Map,
          );
          expect((await data.future)['metadata'], isA<List>());
          cancel();
        },
      );
    },
  );
  for (final subscription in [false, true]) {
    test(
      'WS model65 isolates operation and validates hidden syntax: sub=$subscription',
      () async {
        final errors = <String>[];
        final failed = Completer<void>();
        var first = true;
        await _withSocket(
          TransportMode.json,
          (socket, message) {
            final fields = jsonDecode(message as String) as Map;
            if (fields.containsKey(r'$unsub')) return;
            final id = fields[r'$id'];
            if (fields.containsKey(r'$sub'))
              socket.add('{"\$sub":$id,"ok":true}');
            final route = fields.containsKey(r'$sub') ? r'$stream' : r'$id';
            socket.add(
              '{"data":${first ? model65 : '{"metadata":true}'},"$route":$id}',
            );
            first = false;
          },
          (transport) async {
            transport.setSchema({
              'echo': const APISchemaEntry(7, [], {}, {}, nodeResponse),
            });
            if (subscription) {
              await transport.subscribe<Object?>(
                'echo',
                {},
                (_) => fail('invalid payload delivered'),
                decoder: (value) => value,
                onError: (error) {
                  errors.add(error.error);
                  failed.complete();
                },
              );
              await failed.future;
              expect(errors, ['DecodeError']);
            } else {
              await expectLater(
                transport.call<Object?>('echo', decoder: (value) => value),
                _error('DecodeError'),
              );
            }
            expect((await transport.call<Map>('echo'))['metadata'], isTrue);
          },
        );
      },
    );
  }
  test(
    'WS ignored data is syntax checked without a generic JSON budget',
    () async {
      await _withSocket(
        TransportMode.json,
        (socket, message) {
          final fields = jsonDecode(message as String) as Map;
          final id = fields[r'$id'];
          socket.add('{"data":{"metadata":$json128},"\$id":9999}');
          socket.add('{"\$id":$id,"data":true}');
        },
        (transport) async {
          expect(await transport.call<bool>('echo'), isTrue);
        },
      );
    },
  );
  for (final subscription in [false, true]) {
    test(
      'WS JSON retains plan during Schema replacement: sub=$subscription',
      () async {
        var sends = 0;
        await _withSocket(
          TransportMode.json,
          (socket, message) {
            sends++;
            final fields =
                jsonDecode(message as String) as Map<String, dynamic>;
            if (fields.containsKey(r'$unsub')) return;
            final id = fields[r'$id'];
            socket.add(
              fields.containsKey(r'$sub')
                  ? '{"\$sub":$id,"ok":true}'
                  : '{"\$id":$id,"data":true}',
            );
          },
          (transport) async {
            transport.setSchema({
              'echo': const APISchemaEntry(7, [ParamSchema(1, 'value', 'Int')]),
            });
            final request = ReplacingSchemaRequest(7, () {
              transport.setSchema({
                'echo': const APISchemaEntry(7, [
                  ParamSchema(1, 'value', 'Float'),
                ]),
              });
            });
            final Future<Object?> pending = subscription
                ? transport.subscribe<Object?>(
                    'echo',
                    {},
                    (_) {},
                    decoder: (v) => v,
                    request: request,
                  )
                : transport.call<Object?>('echo', request: request);
            await expectLater(
              pending,
              throwsA(
                isA<LuxoError>().having((e) => e.error, 'error', 'ConfigError'),
              ),
            );
            expect(sends, 0);
            await transport.call<Object?>('echo', params: {'value': 1.5});
            expect(sends, 1);
          },
        );
      },
    );
  }

  for (final mode in TransportMode.values) {
    for (final subscription in [false, true]) {
      for (final failEncoding in [false, true]) {
        test(
          'closing WS while encoding: $mode sub=$subscription fail=$failEncoding',
          () async {
            await _verifyEncodingClose(mode, subscription, failEncoding);
          },
        );
      }
    }
  }
  test('missing Binary body retires only its operation', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final sockets = <WebSocket>[];
    server.listen((request) async {
      final socket = await WebSocketTransformer.upgrade(request);
      sockets.add(socket);
      socket.listen((message) {
        for (final reply in binaryPresenceReplies(message)) socket.add(reply);
      });
    });
    try {
      await verifyWebSocketBinaryPresence(
        'ws://${server.address.host}:${server.port}',
      );
    } finally {
      for (final socket in sockets) await socket.close();
      await server.close(force: true);
    }
  });
  test('handshake timeout closes the actual pending TCP socket', () async {
    final server = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
    final received = Completer<void>(), disconnected = Completer<void>();
    Socket? peer;
    server.listen((socket) {
      peer = socket;
      socket.listen((_) {
        if (!received.isCompleted) received.complete();
      }, onDone: disconnected.complete);
    });
    try {
      final waiting = expectLater(
        connectSocket(
          'ws://${server.address.host}:${server.port}',
          timeout: const Duration(milliseconds: 100),
        ),
        throwsA(isA<TimeoutException>()),
      );
      await received.future.timeout(const Duration(seconds: 2));
      await waiting;
      await disconnected.future.timeout(const Duration(seconds: 2));
    } finally {
      peer?.destroy();
      await server.close();
    }
  });

  for (final mode in TransportMode.values) {
    test(
      '$mode stalled handshake rejects all waiters and permits retry with controlled deadlines',
      () => _withTransportDeadlines(_handshakeTimeout, (deadlines) async {
        final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
        final received = Completer<void>();
        var attempts = 0;
        WebSocket? socket;
        server.listen((request) async {
          if (++attempts == 1) {
            received.complete();
            return;
          }
          socket = await WebSocketTransformer.upgrade(request);
          socket!.listen((message) {
            final (id, cancel) = _control(message);
            if (!cancel) _ack(socket!, mode, id);
          });
        });
        final transport = WsTransport(
          'ws://${server.address.host}:${server.port}',
          timeout: _handshakeTimeout,
          autoReconnect: false,
        );
        transport.setMode(mode);
        transport.setSchema({'echo': const APISchemaEntry(7, [])});
        try {
          final waiting = Future.wait([
            expectLater(transport.call<Object>('echo'), _error('TimeoutError')),
            expectLater(
              transport.subscribe(
                'echo',
                {},
                (_) {},
                decoder: (value) => value,
              ),
              _error('TimeoutError'),
            ),
          ]);
          await received.future;
          expect(deadlines, hasLength(1));
          // Manual expiry must not violate the transport's monotonic deadline.
          await Zone.root.run(() => Future<void>.delayed(_handshakeTimeout));
          deadlines.single.expire();
          await waiting.timeout(const Duration(seconds: 2));
          expect(attempts, 1);
          final stop = await transport.subscribe(
            'echo',
            {},
            (_) {},
            decoder: (value) => value,
          );
          stop();
          expect(attempts, 2);
          expect(deadlines.every((timer) => !timer.isActive), isTrue);
        } finally {
          transport.close();
          await socket?.close();
          await server.close(force: true);
        }
      }),
    );
  }

  test('JSON stream payload retains its standalone nesting budget', () async {
    final payload = '${'[' * 128}0${']' * 128}';
    final received = Completer<Object?>();
    await _withSocket(
      TransportMode.json,
      (socket, message) {
        final (id, cancel) = _control(message);
        if (cancel) return;
        _ack(socket, TransportMode.json, id);
        socket.add('{"\$stream":$id,"data":$payload}');
      },
      (transport) async {
        final stop = await transport.subscribe(
          'echo',
          {},
          received.complete,
          decoder: (value) => value,
          onError: received.completeError,
        );
        expect(encodeLuxoJSON(await received.future), payload);
        stop();
      },
    );
  });
  for (final mode in TransportMode.values) {
    test('$mode invalid error status isolates its subscription', () async {
      final failed = Completer<LuxoError>(), received = Completer<Object?>();
      WebSocket? original;
      var errors = 0;
      await _withSocket(
        mode,
        (socket, message) {
          original ??= socket;
          expect(identical(original, socket), isTrue);
          final (id, cancel) = _control(message);
          if (cancel) return;
          _ack(socket, mode, id);
          if (id == BigInt.one) {
            final enc = LuxoEncoder()
              ..writeFieldInt(1, LuxoInt64.fromInt(0))
              ..writeFieldString(2, 'Unauthorized')
              ..writeFieldString(3, 'invalid')
              ..writeEnd();
            final Object frame = mode == TransportMode.json
                ? '{"\$end":$id,"error":"Unauthorized","code":0,"message":"invalid"}'
                : [..._header(0x16, id), ...enc.bytes()];
            socket.add(frame);
            socket.add(frame);
          } else {
            _data(socket, mode, id, 42);
          }
        },
        (transport) async {
          final stop = await transport.subscribe(
            'echo',
            {},
            (_) => fail('unexpected data'),
            decoder: (value) => value,
            onDone: () => fail('unexpected completion'),
            onError: (error) {
              errors++;
              failed.complete(error);
            },
          );
          expect((await failed.future).error, 'ParseError');
          final stopPeer = await transport.subscribe(
            'echo',
            {},
            received.complete,
            decoder: (value) => value,
            onError: (_) => fail('peer failed'),
          );
          expect(_value(await received.future), 42);
          expect(errors, 1);
          stop();
          stopPeer();
        },
      );
    });
  }

  _registerLiveTokenRenewal();
  _registerTokenRenewalTests();
  _registerTokenRenewalFailures();
  test('missing JSON data retires only its operation', () async {
    final failed = Completer<LuxoError>(), peer = Completer<Object?>();
    final values = <Object?>[];
    var calls = 0, errors = 0, completed = 0;
    await _withSocket(
      TransportMode.json,
      (socket, message) {
        final body = jsonDecode(message as String) as Map<String, dynamic>;
        if (body.containsKey(r'$api')) {
          socket.add(
            jsonEncode({r'$id': body[r'$id'], if (++calls > 1) 'data': null}),
          );
          return;
        }
        final (id, cancel) = _control(message);
        if (cancel) return;
        _ack(socket, TransportMode.json, id);
        socket.add(jsonEncode({r'$stream': id.toInt(), 'data': null}));
        if (id == BigInt.one) {
          socket.add(r'{"$stream":1}');
          socket.add(r'{"$stream":1}');
          socket.add(r'{"$end":1,"ok":true}');
        }
      },
      (transport) async {
        final stop = await transport.subscribe<Object?>(
          'echo',
          {},
          values.add,
          decoder: (value) => value,
          onError: (error) {
            errors++;
            failed.complete(error);
          },
          onDone: () {
            completed++;
          },
        );
        final stopPeer = await transport.subscribe<Object?>(
          'echo',
          {},
          peer.complete,
          decoder: (value) => value,
        );
        expect((await failed.future).error, 'ParseError');
        expect(await peer.future, null);
        await expectLater(
          transport.call<Object?>('echo'),
          throwsA(
            isA<LuxoError>().having(
              (error) => error.error,
              'error',
              'ParseError',
            ),
          ),
        );
        expect(await transport.call<Object?>('echo'), null);
        expect(values, [null]);
        expect(errors, 1);
        expect(completed, 0);
        stop();
        stopPeer();
      },
    );
  });

  for (final mode in TransportMode.values) {
    test(
      '$mode invalid schema update preserves a working subscription',
      () async {
        await _withSocket(
          mode,
          (socket, message) {
            final (id, cancel) = _control(message);
            if (cancel) return;
            if (message is List<int>) {
              final decoder = LuxoDecoder(Uint8List.fromList(message));
              expect(decoder.readUnsigned(), 0x10);
              decoder.readUnsigned();
              expect(decoder.readUnsigned(), 7);
            }
            _ack(socket, mode, id);
            _data(socket, mode, id, 7);
          },
          (transport) async {
            expect(
              () => transport.setSchema({'echo': const APISchemaEntry(0)}),
              throwsA(isA<FormatException>()),
            );
            final value = Completer<int>();
            final stop = await transport.subscribe(
              'echo',
              {},
              value.complete,
              decoder: _value,
            );
            expect(await value.future, 7);
            stop();
          },
        );
      },
    );
  }
  for (final mode in TransportMode.values) {
    for (final body in [
      '',
      '[1]',
      'null',
      '1',
      r'{"$id":3,"data":',
      r'{"$id":3} trailing',
      r'{"$id":3,"data":"\uD800"}',
      r'{"$sub":01,"ok":true}',
      r'{"$sub":+1,"ok":true}',
      r'{"$id":01,"data":7}',
      r'{"$id":+1,"data":7}',
    ]) {
      test(
        '$mode malformed JSON $body retires all work',
        () => _verifyMalformedMessage(mode, body),
      );
    }
  }
  for (final mode in TransportMode.values) {
    test(
      '$mode decoder close does not notify a retired subscription',
      () async {
        final decoded = Completer<void>();
        var decodes = 0;
        await _withSocket(
          mode,
          (socket, message) {
            final (id, cancel) = _control(message);
            if (!cancel) _repeatedData(socket, mode, id);
          },
          (transport) async {
            final stop = await transport.subscribe<Object?>(
              'echo',
              {},
              (_) => fail('unexpected data'),
              decoder: (_) {
                decodes++;
                transport.close();
                decoded.complete();
                throw const FormatException('closed while decoding');
              },
              onError: (_) => fail('already closed'),
              onDone: () => fail('already closed'),
            );
            await decoded.future;
            expect(decodes, 1);
            stop();
          },
        );
      },
    );

    for (final observe in [false, true]) {
      test('$mode decoder cleanup survives observer=$observe', () async {
        final uncaught = <Object>[];
        await _captureCallbackErrors(
          () => _verifyDecoderObserver(mode, observe),
          uncaught,
        );
        expect(uncaught, observe ? [isA<StateError>()] : isEmpty);
      });
    }

    for (final diagnostic in ['normal', 'toString', 'message']) {
      test(
        '$mode decode failure terminates only the damaged subscription: diagnostic=$diagnostic',
        () async {
          final failure = Completer<LuxoError>(), peer = Completer<int>();
          final cancelled = Completer<void>();
          final received = <int>[];
          var errors = 0, decodes = 0;
          await _withSocket(
            mode,
            (socket, message) {
              final (id, cancel) = _control(message);
              if (cancel) {
                if (id == BigInt.one && !cancelled.isCompleted)
                  cancelled.complete();
                return;
              }
              _ack(socket, mode, id);
              if (id == BigInt.one) {
                for (final value in [7, 9, 7, 9])
                  _data(socket, mode, id, value);
                _end(socket, mode, id);
              } else {
                _data(socket, mode, id, 42);
              }
            },
            (transport) async {
              final stop = await transport.subscribe(
                'echo',
                {},
                received.add,
                decoder: (data) {
                  decodes++;
                  final value = _value(data);
                  if (value != 7) {
                    if (diagnostic == 'toString')
                      throw _UnreadableDecoderFailure();
                    if (diagnostic == 'message')
                      throw const _UnreadableTypedDecoderFailure();
                    throw const FormatException('invalid sample');
                  }
                  return value;
                },
                onError: (error) {
                  errors++;
                  failure.complete(error);
                },
                onDone: () => fail('decode error became normal completion'),
              );
              final error = await failure.future.timeout(
                const Duration(seconds: 2),
              );
              expect(error.error, 'DecodeError');
              if (diagnostic != 'normal')
                expect(error.message, 'invalid subscription payload');
              await cancelled.future;
              stop();
              stop();
              final stopPeer = await transport.subscribe(
                'echo',
                {},
                peer.complete,
                decoder: _value,
              );
              expect(await peer.future, 42);
              expect(received, [7]);
              expect(errors, 1);
              expect(decodes, 2);
              stopPeer();
            },
          );
        },
      );
    }

    test(
      '$mode business exceptions stay outside the decoder boundary',
      () async {
        final failure = StateError('application callback');
        final uncaught = <Object>[];
        final finished = Completer<void>(), done = Completer<void>();
        var calls = 0;
        runZonedGuarded(() async {
          try {
            await _withSocket(
              mode,
              (socket, message) {
                final (id, cancel) = _control(message);
                if (cancel) return;
                _ack(socket, mode, id);
                _data(socket, mode, id, 7);
                _data(socket, mode, id, 7);
                _end(socket, mode, id);
              },
              (transport) async {
                final stop = await transport.subscribe(
                  'echo',
                  {},
                  (_) {
                    calls++;
                    throw failure;
                  },
                  decoder: _value,
                  onError: (_) =>
                      fail('business exception became protocol error'),
                  onDone: finished.complete,
                );
                await finished.future;
                expect(calls, 2);
                expect(uncaught, [failure, failure]);
                stop();
              },
            );
            done.complete();
          } catch (error, stack) {
            done.completeError(error, stack);
          }
        }, (error, _) => uncaught.add(error));
        await done.future;
      },
    );
  }

  for (final mode in TransportMode.values) {
    test(
      '$mode rejected active subscription notifies its observer once',
      () async {
        final finished = Completer<LuxoError>();
        final peer = Completer<void>();
        var notifications = 0;
        await _withSocket(
          mode,
          (socket, message) {
            final (id, cancel) = _control(message);
            if (cancel) return;
            _ack(socket, mode, id);
            if (id == BigInt.one) {
              _reject(socket, mode, id);
              _reject(socket, mode, id);
              _data(socket, mode, id, 99);
            } else {
              _data(socket, mode, id, 42);
            }
          },
          (transport) async {
            final stop = await transport.subscribe(
              'echo',
              {},
              (_) => fail('late data'),
              onDone: () => fail('unexpected done'),
              onError: (error) {
                notifications++;
                finished.complete(error);
              },
              decoder: (value) => value,
            );
            expect((await finished.future).error, 'Rejected');
            stop();
            await transport.subscribe(
              'echo',
              {},
              (_) => peer.complete(),
              decoder: (value) => value,
            );
            await peer.future;
            expect(notifications, 1);
          },
        );
      },
    );

    test(
      '$mode cancellation stays silent when a terminal frame arrives late',
      () async {
        final peer = Completer<void>();
        await _withSocket(
          mode,
          (socket, message) {
            final (id, cancel) = _control(message);
            if (cancel) {
              _end(socket, mode, id);
              _end(socket, mode, id, failure: true);
            } else {
              _ack(socket, mode, id);
              if (id == BigInt.two) _data(socket, mode, id, 42);
            }
          },
          (transport) async {
            final stop = await transport.subscribe(
              'echo',
              {},
              (_) => fail('late data'),
              onDone: () => fail('cancelled done'),
              onError: (_) => fail('cancelled error'),
              decoder: (value) => value,
            );
            stop();
            await transport.subscribe(
              'echo',
              {},
              (_) => peer.complete(),
              decoder: (value) => value,
            );
            await peer.future;
          },
        );
      },
    );

    for (final failure in [false, true]) {
      test(
        '$mode finite stream drains data and terminates once: $failure',
        () async {
          final events = <Object>[];
          final finished = Completer<void>();
          final peer = Completer<int>();
          await _withSocket(
            mode,
            (socket, message) {
              final (id, cancel) = _control(message);
              if (cancel) {
                expect(id, BigInt.two);
                return;
              }
              _ack(socket, mode, id);
              if (id == BigInt.two) {
                _data(socket, mode, id, 42);
                return;
              }
              for (var i = 0; i < 64; i++) _data(socket, mode, id, i);
              _end(socket, mode, id, failure: failure);
              _data(socket, mode, id, 99);
              _end(socket, mode, id, failure: !failure);
            },
            (transport) async {
              final stop = await transport.subscribe(
                'echo',
                {},
                (v) => events.add(_value(v)),
                onDone: () {
                  events.add('done');
                  finished.complete();
                },
                onError: (error) {
                  events.add(error);
                  finished.complete();
                },
                decoder: (value) => value,
              );
              await finished.future;
              stop();
              stop();
              final peerStop = await transport.subscribe(
                'echo',
                {},
                (v) => peer.complete(_value(v)),
                decoder: (value) => value,
              );
              expect(await peer.future, 42);
              expect(events.take(64), List.generate(64, (i) => i));
              expect(events, hasLength(65));
              if (failure) {
                final error = events.last as LuxoError;
                expect(error.error, 'Rejected');
                expect(error.code, 422);
                expect(encodeLuxoJSON(error.data), _terminalData);
              } else {
                expect(events.last, 'done');
              }
              peerStop();
            },
          );
        },
      );

      test(
        '$mode terminal before ACK rejects establishment: $failure',
        () async {
          await _withSocket(
            mode,
            (socket, message) {
              final (id, cancel) = _control(message);
              if (!cancel) _end(socket, mode, id, failure: failure);
            },
            (transport) async {
              await expectLater(
                transport.subscribe(
                  'echo',
                  {},
                  (_) => fail('data'),
                  onDone: () => fail('premature done'),
                  onError: (_) => fail('premature error'),
                  decoder: (value) => value,
                ),
                _error('ParseError'),
              );
            },
          );
        },
      );
    }

    test(
      '$mode terminal validates mode, UInt64 and routing before notifying',
      () async {
        final maximum = BigInt.parse('18446744073709551615');
        final events = <Object>[];
        final finished = Completer<void>();
        await _withSocket(
          mode,
          (socket, message) {
            final (id, cancel) = _control(message);
            expect(cancel, isFalse);
            expect(id, maximum);
            _ack(socket, mode, id);
            final other = mode == TransportMode.json
                ? TransportMode.binary
                : TransportMode.json;
            _end(socket, other, id);
            _end(socket, mode, BigInt.zero);
            for (final field in [r'$sub', r'$stream', r'$id']) {
              socket.add('{"\$end":$id,"$field":$id,"ok":true}');
            }
            for (final token in [
              '-0',
              'true',
              '1.0',
              '"$maximum"',
              '18446744073709551616',
            ]) {
              socket.add('{"\$end":$token,"ok":true}');
            }
            socket.add(_header(0x15, maximum - BigInt.one));
            _data(socket, mode, id, 42);
            _end(socket, mode, id);
          },
          (transport) async {
            await transport.subscribe(
              'echo',
              {},
              (v) => events.add(_value(v)),
              onDone: () {
                events.add('done');
                finished.complete();
              },
              onError: (_) => fail('unexpected error'),
              decoder: (value) => value,
            );
            await finished.future;
            expect(events, [42, 'done']);
          },
          initialSequence: maximum - BigInt.one,
        );
      },
    );

    for (var malformed = 0; malformed < 4; malformed++) {
      test(
        '$mode malformed terminal $malformed fails instead of completing',
        () async {
          final finished = Completer<LuxoError>();
          await _withSocket(
            mode,
            (socket, message) {
              final (id, cancel) = _control(message);
              if (cancel) return;
              _ack(socket, mode, id);
              socket.add(
                mode == TransportMode.json
                    ? [
                        '{"\$end":$id,"ok":false}',
                        '{"\$end":$id,"ok":true,"data":null}',
                        '{"\$end":$id,"error":"Rejected"}',
                        '{"\$end":$id,"error":"Rejected","ok":true}',
                      ][malformed]
                    : [
                        [..._header(0x15, id), 0],
                        _header(0x16, id),
                        [..._header(0x16, id), 0],
                        [..._header(0x16, id), ..._terminalError(), 0],
                      ][malformed],
              );
            },
            (transport) async {
              await transport.subscribe(
                'echo',
                {},
                (_) => fail('data'),
                onDone: () => fail('invalid completion'),
                onError: finished.complete,
                decoder: (value) => value,
              );
              expect((await finished.future).error, 'ParseError');
            },
          );
        },
      );
    }

    test(
      '$mode throwing terminal callback does not retain the subscription',
      () async {
        final callbackError = StateError('callback failed');
        final observed = Completer<Object>();
        final events = <int>[];
        final peer = Completer<void>();
        final done = Completer<void>();
        runZonedGuarded(() async {
          try {
            await _withSocket(
              mode,
              (socket, message) {
                final (id, cancel) = _control(message);
                if (cancel) return;
                _ack(socket, mode, id);
                if (id == BigInt.one) {
                  _end(socket, mode, id);
                  _data(socket, mode, id, 99);
                  _end(socket, mode, id);
                } else {
                  _data(socket, mode, id, 42);
                }
              },
              (transport) async {
                final stop = await transport.subscribe(
                  'echo',
                  {},
                  (v) => events.add(_value(v)),
                  onDone: () => throw callbackError,
                  decoder: (value) => value,
                );
                expect(await observed.future, same(callbackError));
                stop();
                await transport.subscribe(
                  'echo',
                  {},
                  (_) => peer.complete(),
                  decoder: (value) => value,
                );
                await peer.future;
                expect(events, isEmpty);
              },
            );
            done.complete();
          } catch (error, stack) {
            done.completeError(error, stack);
          }
        }, (error, _) => observed.complete(error));
        await done.future;
      },
    );

    for (final start in [
      '0',
      '9007199254740990',
      '9223372036854775806',
      '18446744073709551613',
    ]) {
      test(
        '$mode concurrent subscription IDs after $start remain independent',
        () async {
          final requests = <BigInt>[];
          await _withSocket(
            mode,
            (socket, message) {
              final (id, cancel) = _control(message);
              if (cancel) return;
              requests.add(id);
              if (requests.length != 2) return;
              for (var i = 1; i >= 0; i--) {
                _data(socket, mode, requests[i], 99); // No delivery before ACK.
                _ack(socket, mode, requests[i]);
                _data(socket, mode, requests[i], i + 1);
              }
            },
            (transport) async {
              final values = [Completer<int>(), Completer<int>()];
              final stops = await Future.wait([
                for (var i = 0; i < 2; i++)
                  transport.subscribe(
                    'echo',
                    {},
                    (value) => values[i].complete(_value(value)),
                    decoder: (value) => value,
                  ),
              ]);
              expect(await Future.wait(values.map((value) => value.future)), [
                1,
                2,
              ]);
              final initial = BigInt.parse(start);
              expect(requests, [initial + BigInt.one, initial + BigInt.two]);
              for (final stop in stops) stop();
              if (start == '18446744073709551613') {
                await expectLater(
                  transport.subscribe(
                    'echo',
                    {},
                    (_) {},
                    decoder: (value) => value,
                  ),
                  _error('RequestIDExhausted'),
                );
              }
            },
            initialSequence: BigInt.parse(start),
          );
        },
      );
    }

    test(
      '$mode stale errors, data and cancel handles cannot affect a new subscription',
      () async {
        final cancelled = <BigInt>[];
        final finished = Completer<void>();
        await _withSocket(
          mode,
          (socket, message) {
            final (id, cancel) = _control(message);
            if (cancel) {
              cancelled.add(id);
              if (id == BigInt.two) finished.complete();
              return;
            }
            if (id == BigInt.two) {
              _reject(socket, mode, BigInt.one);
              _data(socket, mode, BigInt.one, 99);
            }
            _ack(socket, mode, id);
            _data(socket, mode, id, id.toInt());
          },
          (transport) async {
            final first = Completer<int>();
            final oldStop = await transport.subscribe(
              'echo',
              {},
              (v) => first.complete(_value(v)),
              decoder: (value) => value,
            );
            expect(await first.future, 1);
            oldStop();
            final second = Completer<int>();
            final newStop = await transport.subscribe(
              'echo',
              {},
              (v) => second.complete(_value(v)),
              decoder: (value) => value,
            );
            oldStop();
            expect(await second.future, 2);
            newStop();
            await finished.future;
            expect(cancelled, [BigInt.one, BigInt.two]);
          },
        );
      },
    );

    test(
      '$mode active subscription keeps its mode after transport mode changes',
      () async {
        final connected = Completer<WebSocket>();
        final cancelled = Completer<void>();
        await _withSocket(
          mode,
          (socket, message) {
            final (id, cancel) = _control(message);
            if (cancel) {
              expect(message is String, mode == TransportMode.json);
              expect(id, BigInt.one);
              cancelled.complete();
            } else {
              _ack(socket, mode, id);
              connected.complete(socket);
            }
          },
          (transport) async {
            final data = Completer<int>();
            final stop = await transport.subscribe(
              'echo',
              {},
              (v) => data.complete(_value(v)),
              decoder: (value) => value,
            );
            final other = mode == TransportMode.json
                ? TransportMode.binary
                : TransportMode.json;
            transport.setMode(other);
            final socket = await connected.future;
            _data(socket, other, BigInt.one, 99);
            _data(socket, mode, BigInt.one, 42);
            expect(await data.future, 42);
            stop();
            await cancelled.future;
          },
        );
      },
    );

    test(
      '$mode timed-out subscription is cancelled and its late ACK is ignored',
      () async {
        final cancelled = Completer<void>();
        await _withSocket(
          mode,
          (socket, message) {
            final (id, cancel) = _control(message);
            if (!cancel) return;
            expect(id, BigInt.one);
            _ack(socket, mode, id);
            _data(socket, mode, id, 99);
            cancelled.complete();
          },
          (transport) async {
            await expectLater(
              transport.subscribe(
                'echo',
                {},
                (_) => fail('late data'),
                decoder: (value) => value,
              ),
              _error('TimeoutError'),
            );
            await cancelled.future;
          },
        );
      },
    );

    test(
      '$mode maximum subscription error survives counter exhaustion',
      () async {
        final maximum = BigInt.parse('18446744073709551615');
        final received = Completer<WebSocket>();
        await _withSocket(
          mode,
          (socket, message) {
            expect(_control(message), (maximum, false));
            received.complete(socket);
          },
          (transport) async {
            final rejected = expectLater(
              transport.subscribe(
                'echo',
                {},
                (_) {},
                decoder: (value) => value,
              ),
              _error('Rejected'),
            );
            final socket = await received.future;
            await expectLater(
              transport.subscribe(
                'echo',
                {},
                (_) {},
                decoder: (value) => value,
              ),
              _error('RequestIDExhausted'),
            );
            _reject(socket, mode, maximum);
            await rejected;
          },
          initialSequence: maximum - BigInt.one,
        );
      },
    );
  }

  test(
    'malformed JSON subscription IDs cannot acknowledge or deliver data',
    () async {
      await _withSocket(
        TransportMode.json,
        (socket, message) {
          final (_, cancel) = _control(message);
          if (cancel) return;
          for (final token in [
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
            '"echo"',
          ]) {
            socket.add('{"\$sub":$token,"ok":true}');
            socket.add('{"\$stream":$token,"data":99}');
          }
          socket.add('{"\$sub":1,"ok":false}');
          _data(socket, TransportMode.json, BigInt.one, 99);
          _ack(socket, TransportMode.json, BigInt.one);
          _data(socket, TransportMode.json, BigInt.one, 42);
        },
        (transport) async {
          final received = Completer<int>();
          final stop = await transport.subscribe(
            'echo',
            {},
            (v) => received.complete(_value(v)),
            decoder: (value) => value,
          );
          expect(await received.future, 42);
          stop();
        },
      );
    },
  );

  test('valid retired Binary subscription frames are ignored', () async {
    await _withSocket(
      TransportMode.binary,
      (socket, message) {
        final (_, cancel) = _control(message);
        if (cancel) return;
        for (final retired in [
          BigInt.zero,
          BigInt.from(999),
          (BigInt.one << 64) - BigInt.one,
        ]) {
          socket.add(_header(0x13, retired));
          socket.add([..._header(0x12, retired), 42]);
          socket.add(_header(0x15, retired));
        }
        _data(socket, TransportMode.binary, BigInt.one, 99);
        _ack(socket, TransportMode.binary, BigInt.one);
        _data(socket, TransportMode.binary, BigInt.one, 42);
      },
      (transport) async {
        final received = Completer<int>();
        final stop = await transport.subscribe(
          'echo',
          {},
          (v) => received.complete(_value(v)),
          decoder: (value) => value,
        );
        expect(await received.future, 42);
        stop();
      },
    );
  });

  for (final mode in TransportMode.values) {
    for (final frame in <List<int>>[
      [],
      [2],
      [2, 128],
      [2, ...List.filled(9, 128), 2],
      [2, ...List.filled(10, 128), 0],
      [0x82, 0, 1],
      [0xff, 1],
      [0x10, 1],
      [0x11, 1],
    ]) {
      test(
        '$mode malformed Binary $frame retires all work',
        () => _verifyMalformedMessage(mode, frame),
      );
    }
  }

  test(
    'correlated Binary ACK with trailing bytes only rejects its subscription',
    () async {
      final received = Completer<int>();
      await _withSocket(
        TransportMode.binary,
        (socket, message) {
          if (message is List<int> && message.first == 1) {
            socket.add([..._header(2, BigInt.from(3)), 42]);
            return;
          }
          final (id, cancel) = _control(message);
          if (cancel) return;
          if (id == BigInt.one) {
            socket.add([0x13, 1, 42]);
          } else {
            socket.add([0x13, 99, 42]);
            _ack(socket, TransportMode.binary, id);
            _data(socket, TransportMode.binary, id, 42);
          }
        },
        (transport) async {
          final failed = expectLater(
            transport.subscribe<Object?>('echo', {}, (_) {}, decoder: (v) => v),
            _error('ParseError'),
          );
          final peer = transport.subscribe<Object?>(
            'echo',
            {},
            (value) => received.complete(_value(value)),
            decoder: (v) => v,
          );
          await failed;
          final stop = await peer;
          expect(await received.future, 42);
          expect(
            await transport.call<Object>('echo'),
            Uint8List.fromList([42]),
          );
          stop();
        },
      );
    },
  );

  test(
    'close rejects pending operations and does not reopen a transport',
    () async {
      final received = Completer<void>();
      var count = 0;
      await _withSocket(
        TransportMode.json,
        (_, _) {
          if (++count == 2) received.complete();
        },
        (transport) async {
          final call = expectLater(
            transport.call<Object>('echo'),
            _error('NetworkError'),
          );
          final subscription = expectLater(
            transport.subscribe('echo', {}, (_) {}, decoder: (value) => value),
            _error('NetworkError'),
          );
          await received.future;
          transport.close();
          await Future.wait([call, subscription]);
          await expectLater(
            transport.subscribe('echo', {}, (_) {}, decoder: (value) => value),
            _error('NetworkError'),
          );
          expect(count, 2);
        },
      );
    },
  );

  test(
    'closing while connecting rejects every waiter without sending a request',
    () async {
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      final waiting = Completer<HttpRequest>();
      server.listen(waiting.complete);
      final transport = WsTransport(
        'ws://${server.address.host}:${server.port}',
        autoReconnect: false,
      );
      WebSocket? socket;
      try {
        final call = expectLater(
          transport.call<Object>('echo'),
          _error('NetworkError'),
        );
        final subscription = expectLater(
          transport.subscribe('echo', {}, (_) {}, decoder: (value) => value),
          _error('NetworkError'),
        );
        final request = await waiting.future;
        transport.close();
        await Future.wait([call, subscription])
            .timeout(const Duration(milliseconds: 250));
        socket = await WebSocketTransformer.upgrade(request);
        final messages = socket.toList();
        expect(await messages.timeout(const Duration(seconds: 3)), isEmpty);
      } finally {
        transport.close();
        await socket?.close();
        await server.close(force: true);
      }
    },
  );

  test('failed connection rejects all waiters and can be retried', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    var attempts = 0;
    WebSocket? socket;
    server.listen((request) async {
      if (++attempts == 1) {
        request.response.statusCode = 503;
        await request.response.close();
      } else {
        socket = await WebSocketTransformer.upgrade(request);
        socket!.listen((message) {
          final (id, cancel) = _control(message);
          if (!cancel) _ack(socket!, TransportMode.json, id);
        });
      }
    });
    final transport = WsTransport(
      'ws://${server.address.host}:${server.port}',
      autoReconnect: false,
    );
    try {
      await Future.wait([
        expectLater(transport.call<Object>('echo'), _error('NetworkError')),
        expectLater(
          transport.subscribe('echo', {}, (_) {}, decoder: (value) => value),
          _error('NetworkError'),
        ),
      ]);
      final stop = await transport.subscribe(
        'echo',
        {},
        (_) {},
        decoder: (value) => value,
      );
      stop();
      expect(attempts, 2);
    } finally {
      transport.close();
      await socket?.close();
      await server.close(force: true);
    }
  });

  test(
    'remote disconnect rejects pending calls and subscription acknowledgements',
    () async {
      var count = 0;
      await _withSocket(
        TransportMode.json,
        (socket, _) {
          if (++count == 2) unawaited(socket.close());
        },
        (transport) async {
          await Future.wait([
            expectLater(transport.call<Object>('echo'), _error('NetworkError')),
            expectLater(
              transport.subscribe(
                'echo',
                {},
                (_) {},
                decoder: (value) => value,
              ),
              _error('NetworkError'),
            ),
          ]);
        },
      );
    },
  );

  for (final mode in TransportMode.values) {
    for (final subscribe in [false, true]) {
      test(
        '$mode close after readiness rejects before sending: $subscribe',
        () async {
          var sent = 0;
          await _withSocket(
            mode,
            (socket, message) {
              sent++;
              final (id, cancel) = _control(message);
              if (!cancel) _ack(socket, mode, id);
            },
            (transport) async {
              await transport.subscribe(
                'echo',
                {},
                (_) {},
                decoder: (value) => value,
              );
              final waiting = subscribe
                  ? transport.subscribe(
                      'echo',
                      {},
                      (_) {},
                      decoder: (value) => value,
                    )
                  : transport.call<Object>('echo');
              transport.close();
              await expectLater(waiting, _error('NetworkError'));
              expect(sent, 1);
            },
          );
        },
      );
    }

    for (final parse in [false, true]) {
      test(
        '$mode disconnect parse=$parse notifies all observers despite reentry and throws',
        () async {
          final callbacks = [
            StateError('first callback'),
            StateError('second callback'),
          ];
          final uncaught = <Object>[];
          final observed = <LuxoError>[];
          final finished = Completer<void>();
          final done = Completer<void>();
          var requests = 0;
          runZonedGuarded(
            () async {
              try {
                await _withSocket(
                  mode,
                  (socket, message) {
                    final (id, cancel) = _control(message);
                    if (cancel) return;
                    _ack(socket, mode, id);
                    if (++requests == 2) {
                      if (parse)
                        socket.add('{');
                      else
                        unawaited(socket.close());
                    }
                  },
                  (transport) async {
                    final stops = await Future.wait([
                      for (var i = 0; i < 2; i++)
                        transport.subscribe(
                          'echo',
                          {},
                          (_) => fail('unexpected data'),
                          onDone: () => fail('unexpected completion'),
                          onError: (error) {
                            observed.add(error);
                            if (i == 0) transport.close();
                            throw callbacks[i];
                          },
                          decoder: (value) => value,
                        ),
                    ]);
                    await finished.future;
                    for (final stop in stops) stop();
                    expect(observed.map((error) => error.error), [
                      parse ? 'ParseError' : 'NetworkError',
                      parse ? 'ParseError' : 'NetworkError',
                    ]);
                    expect(uncaught, callbacks);
                  },
                );
                done.complete();
              } catch (error, stack) {
                done.completeError(error, stack);
              }
            },
            (error, _) {
              uncaught.add(error);
              if (uncaught.length == 2) finished.complete();
            },
          );
          await done.future;
        },
      );
    }

    test(
      '$mode explicit close is silent for established subscriptions',
      () async {
        await _withSocket(
          mode,
          (socket, message) {
            final (id, cancel) = _control(message);
            if (!cancel) _ack(socket, mode, id);
          },
          (transport) async {
            final stop = await transport.subscribe(
              'echo',
              {},
              (_) => fail('unexpected data'),
              onError: (_) => fail('explicit close notified error'),
              onDone: () => fail('explicit close notified completion'),
              decoder: (value) => value,
            );
            transport.close();
            stop();
            await expectLater(
              transport.call<Object>('echo'),
              _error('NetworkError'),
            );
          },
        );
      },
    );

    for (final manualRetry in [false, true]) {
      test(
        '$mode reconnect ends old subscriptions and requires explicit new ones: manual=$manualRetry',
        () async {
          final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
          final sockets = <WebSocket>[];
          var attempts = 0;
          final disconnected = Completer<LuxoError>();
          final reconnected = Completer<void>();
          final restored = Completer<int>();
          final requests = <BigInt>[];
          var errors = 0;
          final other = mode == TransportMode.json
              ? TransportMode.binary
              : TransportMode.json;
          server.listen((request) async {
            expect(request.uri.queryParameters, {
              'environment': 'test',
              'token': 'test-token',
            });
            if (++attempts == 2) {
              request.response.statusCode = 503;
              await request.response.close();
              return;
            }
            final socket = await WebSocketTransformer.upgrade(request);
            sockets.add(socket);
            if (attempts == 3) reconnected.complete();
            socket.listen((message) {
              final responseMode = attempts == 1 ? mode : other;
              expect(message is String, responseMode == TransportMode.json);
              final (id, cancel) = _control(message);
              if (cancel) return;
              requests.add(id);
              _ack(socket, responseMode, id);
              if (attempts == 3) {
                _data(socket, mode, BigInt.one, 99);
                _end(socket, mode, BigInt.one);
                _data(socket, responseMode, id, 42);
              }
            });
          });
          final transport =
              WsTransport(
                  'ws://${server.address.host}:${server.port}?environment=test',
                  options: TransportOptions(mode: mode),
                )
                ..setSchema({'echo': const APISchemaEntry(7)})
                ..setToken('test-token');
          try {
            final stop = await transport.subscribe(
              'echo',
              {},
              (_) => fail('old stream was replayed'),
              onError: (error) {
                errors++;
                disconnected.complete(error);
              },
              onDone: () => fail('disconnect is not normal completion'),
              decoder: (value) => value,
            );
            transport.setMode(other);
            await sockets.first.close();
            expect(
              (await disconnected.future.timeout(const Duration(seconds: 1)))
                  .error,
              'NetworkError',
            );
            if (manualRetry) {
              await expectLater(
                transport.call<Object>('echo'),
                _error('NetworkError'),
              );
            }
            await reconnected.future.timeout(const Duration(seconds: 4));
            final newStop = await transport.subscribe(
              'echo',
              {},
              (value) => restored.complete(_value(value)),
              onError: (_) => fail('new stream was terminated'),
              decoder: (value) => value,
            );
            stop();
            expect(
              await restored.future.timeout(const Duration(seconds: 8)),
              42,
            );
            expect(attempts, 3);
            expect(requests, [BigInt.one, BigInt.two]);
            expect(errors, 1);
            newStop();
          } finally {
            transport.close();
            for (final socket in sockets) await socket.close();
            await server.close(force: true);
          }
        },
      );
    }
  }

  test('missing Binary schema fails without sending a frame', () async {
    await _withSocket(
      TransportMode.binary,
      (_, _) => fail('unexpected frame'),
      (transport) async {
        await expectLater(
          transport.call<Object>('missing'),
          _error('ConfigError'),
        );
        await expectLater(
          transport.subscribe('missing', {}, (_) {}, decoder: (value) => value),
          _error('ConfigError'),
        );
      },
    );
  });

  test(
    'reconnection never revalidates or replays retired subscriptions',
    () async {
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      final sockets = <WebSocket>[];
      server.listen((request) async {
        final socket = await WebSocketTransformer.upgrade(request);
        sockets.add(socket);
        socket.listen((message) {
          if ((message as List<int>).first == 1) {
            unawaited(socket.close());
          } else {
            final (id, cancel) = _control(message);
            if (!cancel) _ack(socket, TransportMode.binary, id);
          }
        });
      });
      final transport = WsTransport(
        'ws://${server.address.host}:${server.port}',
        autoReconnect: false,
        options: const TransportOptions(mode: TransportMode.binary),
      )..setSchema({'echo': const APISchemaEntry(7)});
      try {
        await transport.subscribe(
          'echo',
          {},
          (_) {},
          decoder: (value) => value,
        );
        await expectLater(
          transport.call<Object>('echo'),
          _error('NetworkError'),
        );
        transport.setSchema({});
        await expectLater(
          transport.call<Object>('echo'),
          _error('ConfigError'),
        );
        transport.setSchema({'echo': const APISchemaEntry(7)});
        final stop = await transport.subscribe(
          'echo',
          {},
          (_) {},
          decoder: (value) => value,
        );
        expect(sockets, hasLength(2));
        stop();
      } finally {
        transport.close();
        for (final socket in sockets) await socket.close();
        await server.close(force: true);
      }
    },
  );
}

const _terminalData =
    '{"integer":123456789012345678901234567890,"fraction":0.12345678901234567890123456789,"zero":-0}';

Uint8List _terminalError() =>
    (LuxoEncoder()
          ..writeFieldInt(1, LuxoInt64.fromInt(422))
          ..writeFieldString(2, 'Rejected')
          ..writeFieldString(3, 'rejected')
          ..writeFieldBytes(5, Uint8List.fromList(utf8.encode(_terminalData)))
          ..writeEnd())
        .bytes();

void _end(
  WebSocket socket,
  TransportMode mode,
  BigInt id, {
  bool failure = false,
}) {
  socket.add(
    mode == TransportMode.json
        ? failure
              ? '{"\$end":$id,"error":"Rejected","code":422,"message":"rejected","data":$_terminalData}'
              : '{"\$end":$id,"ok":true}'
        : failure
        ? [..._header(0x16, id), ..._terminalError()]
        : _header(0x15, id),
  );
}

Matcher _error(String name) =>
    throwsA(isA<LuxoError>().having((e) => e.error, 'error', name));
int _value(dynamic value) => value is Uint8List ? value.single : value as int;

Future<void> _captureCallbackErrors(
  Future<void> Function() run,
  List<Object> uncaught,
) {
  final finished = Completer<void>();
  runZonedGuarded(() async {
    try {
      await run();
      finished.complete();
    } catch (error, stack) {
      finished.completeError(error, stack);
    }
  }, (error, _) => uncaught.add(error));
  return finished.future;
}

Future<void> _verifyDecoderObserver(TransportMode mode, bool observe) async {
  final cancelled = Completer<void>(), peer = Completer<int>();
  var decodes = 0, errors = 0;
  await _withSocket(
    mode,
    (socket, message) {
      final (id, cancel) = _control(message);
      if (cancel) {
        if (id == BigInt.one) cancelled.complete();
      } else if (id == BigInt.one) {
        _repeatedData(socket, mode, id);
      } else {
        _ack(socket, mode, id);
        _data(socket, mode, id, 42);
      }
    },
    (transport) async {
      final stop = await transport.subscribe<Object?>(
        'echo',
        {},
        (_) => fail('unexpected data'),
        decoder: (_) {
          decodes++;
          throw LuxoError('RangeError', 0, 'bad integer');
        },
        onError: observe
            ? (error) {
                errors++;
                expect(error.error, 'DecodeError');
                expect(error.message, 'bad integer');
                throw StateError('observer');
              }
            : null,
        onDone: () => fail('unexpected completion'),
      );
      await cancelled.future;
      stop();
      final stopPeer = await transport.subscribe(
        'echo',
        {},
        peer.complete,
        decoder: _value,
      );
      expect(await peer.future, 42);
      expect(decodes, 1);
      expect(errors, observe ? 1 : 0);
      stopPeer();
    },
  );
}

void _repeatedData(WebSocket socket, TransportMode mode, BigInt id) {
  _ack(socket, mode, id);
  _data(socket, mode, id, 7);
  _data(socket, mode, id, 7);
  _end(socket, mode, id);
}

Future<void> _verifyMalformedMessage(TransportMode mode, Object body) async {
  var messages = 0;
  await _withSocket(mode, (socket, message) {
    if (message is String &&
        (jsonDecode(message) as Map).containsKey(r'$unsub'))
      return;
    if (message is List<int> && message.first == 0x11) return;
    messages++;
    if (messages <= 2) {
      final (id, _) = _control(message);
      _ack(socket, mode, id);
    } else if (messages == 4) {
      socket.add(body);
    } else if (messages == 5) {
      socket.add(
        mode == TransportMode.json
            ? r'{"$id":5,"data":7}'
            : <int>[..._header(0x02, BigInt.from(5)), 7],
      );
    }
  }, (transport) => _assertParseRetirement(transport, mode));
  expect(messages, 5, reason: 'no automatic subscription replay');
}

Future<void> _assertParseRetirement(
  WsTransport transport,
  TransportMode mode,
) async {
  final errors = <LuxoError>[];
  final notified = Completer<void>();
  final stops = <void Function()>[];
  for (var i = 0; i < 2; i++) {
    stops.add(
      await transport.subscribe(
        'echo',
        {},
        (_) => fail('unexpected data'),
        decoder: (value) => value,
        onDone: () => fail('parse failure is not completion'),
        onError: (error) {
          errors.add(error);
          if (errors.length == 2) notified.complete();
        },
      ),
    );
  }
  await Future.wait([
    expectLater(transport.call<Object>('echo'), _error('ParseError')),
    expectLater(
      transport.subscribe(
        'echo',
        {},
        (_) {},
        decoder: (value) => value,
        onError: (_) => fail('unacknowledged observer notified'),
      ),
      _error('ParseError'),
    ),
  ]);
  await notified.future;
  for (final stop in stops) stop();
  final result = await transport.call<Object>('echo');
  expect(mode == TransportMode.json ? result : (result as Uint8List).single, 7);
  expect(errors.map((error) => error.error), ['ParseError', 'ParseError']);
}

Future<void> _withSocket(
  TransportMode mode,
  void Function(WebSocket, Object?) respond,
  Future<void> Function(WsTransport) verify, {
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
  )..setSchema({'echo': const APISchemaEntry(7)});
  try {
    await verify(transport).timeout(const Duration(seconds: 3));
  } finally {
    transport.close();
    for (final socket in sockets) await socket.close();
    await server.close(force: true);
  }
}

// Independent oracle: numeric identifiers are not schema API identifiers.
(BigInt, bool) _control(Object? message) {
  if (message is String) {
    final body = decodeLuxoJSON(message) as Map;
    final cancel = body.containsKey(r'$unsub');
    if (!cancel) expect(body[r'$sub'], 'echo');
    final value = body[cancel ? r'$unsub' : r'$id'];
    expect(value, isNotNull);
    return (BigInt.parse(value.toString()), cancel);
  }
  final bytes = message as List<int>;
  expect(bytes.first, anyOf(0x10, 0x11));
  var id = BigInt.zero;
  var offset = 1;
  for (var shift = 0; shift < 70; shift += 7) {
    final byte = bytes[offset++];
    id |= BigInt.from(byte & 127) << shift;
    if (byte < 128) break;
  }
  final cancel = bytes.first == 0x11;
  expect(bytes.sublist(offset), cancel ? <int>[] : [7, 0, 0]);
  return (id, cancel);
}

List<int> _header(int frame, BigInt id) {
  final bytes = [frame];
  do {
    final byte = (id & BigInt.from(127)).toInt();
    id >>= 7;
    bytes.add(byte | (id == BigInt.zero ? 0 : 128));
  } while (id != BigInt.zero);
  return bytes;
}

void _ack(WebSocket socket, TransportMode mode, BigInt id) => socket.add(
  mode == TransportMode.json ? '{"\$sub":$id,"ok":true}' : _header(0x13, id),
);

void _data(WebSocket socket, TransportMode mode, BigInt id, int value) =>
    socket.add(
      mode == TransportMode.json
          ? '{"\$stream":$id,"data":$value}'
          : [..._header(0x12, id), value],
    );

void _reject(WebSocket socket, TransportMode mode, BigInt id) {
  final payload = LuxoEncoder()
    ..writeFieldInt(1, LuxoInt64.fromInt(400))
    ..writeFieldString(2, 'Rejected')
    ..writeFieldString(3, 'rejected')
    ..writeEnd();
  socket.add(
    mode == TransportMode.json
        ? '{"\$sub":$id,"error":"Rejected","code":400,"message":"rejected"}'
        : [..._header(0x14, id), ...payload.bytes()],
  );
}

void _registerTokenRenewalTests() {
  for (final mode in TransportMode.values) {
    for (final action in ['renew', 'manual', 'close']) {
      test(
        '$mode token renewal $action preserves connection ownership',
        () async {
          final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
          final sockets = <WebSocket>[], tokens = <String?>[];
          final renew = Completer<String?>(),
              started = Completer<void>(),
              ended = Completer<LuxoError>();
          var renewals = 0, errors = 0, newFrames = 0;
          server.listen((request) async {
            tokens.add(request.uri.queryParameters['token']);
            final socket = await WebSocketTransformer.upgrade(request);
            sockets.add(socket);
            socket.listen((message) {
              if (identical(socket, sockets.first)) {
                final (id, cancel) = _control(message);
                if (!cancel) _ack(socket, mode, id);
              } else {
                newFrames++;
                if (message is String) {
                  final data = jsonDecode(message) as Map<String, dynamic>;
                  expect(data[r'$api'], 'echo');
                  socket.add(jsonEncode({r'$id': data[r'$id'], 'data': 42}));
                } else {
                  final decoder = LuxoDecoder(
                    Uint8List.fromList(message as List<int>),
                  );
                  expect(decoder.readUnsigned(), 1);
                  final id = decoder.readUnsigned();
                  socket.add([2, id, 42]);
                }
              }
            });
          });
          final transport = WsTransport(
            'ws://${server.address.host}:${server.port}',
            options: TransportOptions(mode: mode, token: 'old'),
            autoReconnect: false,
            onTokenExpired: () {
              renewals++;
              started.complete();
              return renew.future;
            },
          )..setSchema({'echo': const APISchemaEntry(7)});
          try {
            final stop = await transport.subscribe(
              'echo',
              {},
              (_) => fail('old subscription replayed'),
              decoder: (value) => value,
              onError: (error) {
                errors++;
                ended.complete(error);
              },
            );
            await sockets.first.close(4001, 'access token expired');
            expect(
              (await ended.future.timeout(const Duration(seconds: 1))).code,
              401,
            );
            await started.future.timeout(const Duration(seconds: 1));
            final pending = transport.call<int>('echo', decoder: _value);
            if (action == 'close') {
              final rejected = expectLater(pending, _error('NetworkError'));
              transport.close();
              await rejected;
              renew.complete('late');
            } else {
              if (action == 'manual') transport.setToken('manual');
              renew.complete(action == 'manual' ? 'stale' : 'new');
              expect(await pending.timeout(const Duration(seconds: 1)), 42);
              expect(tokens, ['old', action == 'manual' ? 'manual' : 'new']);
              expect(newFrames, 1);
            }
            stop();
            await Future<void>.delayed(Duration.zero);
            expect(errors, 1);
            expect(renewals, 1);
            if (action == 'close') expect(sockets.length, 1);
          } finally {
            transport.close();
            for (final socket in sockets) {
              await socket.close();
            }
            await server.close(force: true);
          }
        },
      );
    }
  }
}

void _registerTokenRenewalFailures() {
  for (final mode in TransportMode.values) {
    for (final throws in [false, true]) {
      test(
        '$mode concurrent renewal failure throws=$throws settles all waiters',
        () async {
          final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
          final sockets = <WebSocket>[];
          final entered = Completer<void>(), release = Completer<void>();
          var attempts = 0, renewals = 0;
          server.listen((request) async {
            attempts++;
            final socket = await WebSocketTransformer.upgrade(request);
            sockets.add(socket);
            socket.listen((_) {
              unawaited(socket.close(4001, 'expired'));
            });
          });
          final transport = WsTransport(
            'ws://${server.address.host}:${server.port}',
            options: TransportOptions(mode: mode, token: 'old'),
            onTokenExpired: () async {
              renewals++;
              if (!entered.isCompleted) entered.complete();
              await release.future;
              if (throws) throw StateError('refresh rejected');
              return null;
            },
          )..setSchema({'echo': const APISchemaEntry(7)});
          try {
            await expectLater(
              transport.call<Object?>('echo'),
              _error('Unauthorized'),
            );
            await entered.future.timeout(const Duration(seconds: 2));
            final waiters = List.generate(
              8,
              (_) => expectLater(
                transport.call<Object?>('echo'),
                _error('Unauthorized'),
              ),
            );
            release.complete();
            await Future.wait(waiters).timeout(const Duration(seconds: 2));
            expect(renewals, 1);
            await Future<void>.delayed(const Duration(milliseconds: 1100));
            expect(attempts, 1);
          } finally {
            if (!release.isCompleted) release.complete();
            transport.close();
            for (final socket in sockets) {
              await socket.close();
            }
            await server.close(force: true);
          }
        },
      );
    }
  }
  for (final failure in ['null', 'throw', 'missing', 'timeout']) {
    test('token renewal $failure stops retrying expired credentials', () async {
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      final sockets = <WebSocket>[];
      final late = Completer<String?>();
      var attempts = 0;
      server.listen((request) async {
        attempts++;
        final socket = await WebSocketTransformer.upgrade(request);
        sockets.add(socket);
        socket.listen((_) {
          unawaited(socket.close(4001, 'access token expired'));
        });
      });
      final transport = WsTransport(
        'ws://${server.address.host}:${server.port}',
        options: const TransportOptions(token: 'old'),
        timeout: const Duration(seconds: 1),
        onTokenExpired: failure == 'missing'
            ? null
            : () {
                if (failure == 'throw') throw StateError('refresh rejected');
                if (failure == 'timeout') return late.future;
                return Future<String?>.value();
              },
      );
      try {
        await expectLater(
          transport.call<Object?>('echo'),
          _error('Unauthorized'),
        );
        await expectLater(
          transport.call<Object?>('echo'),
          _error('Unauthorized'),
        );
        late.complete('late');
        await Future<void>.delayed(const Duration(milliseconds: 1100));
        expect(attempts, 1);
      } finally {
        transport.close();
        for (final socket in sockets) {
          await socket.close();
        }
        await server.close(force: true);
      }
    });
  }
}

void _registerLiveTokenRenewal() {
  _registerLiveStreamRenewal();
  final endpoint = Platform.environment['LUXO_WS_RENEWAL_URL'];
  test('live Go expiry refresh and reconnect', () async {
    final http = HttpClient();
    try {
      for (final mode in TransportMode.values) {
        final initialRequest = await http.getUrl(Uri.parse('$endpoint/token'));
        final initial = await initialRequest.close();
        expect(initial.statusCode, 200);
        final refresh = initial.headers.value('x-refresh-token')!;
        final token = await utf8.decoder.bind(initial).join();
        final refreshed = Completer<void>();
        final transport = WsTransport(
          '${endpoint!.replaceFirst('http://', 'ws://')}/ws',
          options: TransportOptions(mode: mode, token: token),
          timeout: const Duration(seconds: 5),
          onTokenExpired: () async {
            final request = await http.getUrl(Uri.parse('$endpoint/refresh'));
            request.headers.set('Authorization', 'Bearer $refresh');
            final response = await request.close();
            expect(response.statusCode, 200);
            final value = await utf8.decoder.bind(response).join();
            refreshed.complete();
            return value;
          },
        )..setSchema({'identity': const APISchemaEntry(7)});
        try {
          final first = await transport.call<Object?>('identity');
          expect(first, mode == TransportMode.json ? 42 : [84]);
          await refreshed.future.timeout(const Duration(seconds: 6));
          expect(await transport.call<Object?>('identity'), first);
        } finally {
          transport.close();
        }
      }
    } finally {
      http.close(force: true);
    }
  }, skip: endpoint == null ? 'requires owned Go renewal fixture' : false);
}

Future<({int started, int gateway, int backend})> _liveStreamCounts(
  HttpClient http,
  String endpoint,
) async {
  final request = await http.getUrl(Uri.parse('$endpoint/stats'));
  final response = await request.close();
  expect(response.statusCode, 200);
  final value = jsonDecode(
    await utf8.decoder.bind(response).join(),
  ) as Map<String, Object?>;
  return (
    started: value['started'] as int,
    gateway: value['gateway'] as int,
    backend: value['backend'] as int,
  );
}

Future<void> _waitForLiveStreamCleanup(HttpClient http, String endpoint) async {
  final clock = Stopwatch()..start();
  while (true) {
    final counts = await _liveStreamCounts(http, endpoint);
    if (counts.gateway == 0 && counts.backend == 0) return;
    if (clock.elapsed > const Duration(seconds: 2))
      fail('old subscriptions retained: $counts');
    await Future<void>.delayed(const Duration(milliseconds: 10));
  }
}

void _registerLiveStreamRenewal() {
  final endpoint = Platform.environment['LUXO_WS_RENEWAL_URL'];
  test(
    'live Go gateway stream expiry does not replay subscriptions',
    () async {
      final http = HttpClient();
      try {
        for (final mode in TransportMode.values) {
          await _waitForLiveStreamCleanup(http, endpoint!);
          final before = (await _liveStreamCounts(http, endpoint)).started;
          final initial = await (await http.getUrl(
            Uri.parse('$endpoint/token'),
          )).close();
          expect(initial.statusCode, 200);
          final refresh = initial.headers.value('x-refresh-token')!;
          final token = await utf8.decoder.bind(initial).join();
          final renewed = Completer<void>(), ended = Completer<LuxoError>();
          final first = Completer<Object?>();
          var errors = 0, completed = 0, values = 0;
          final transport =
              WsTransport(
                '${endpoint.replaceFirst('http://', 'ws://')}/ws',
                options: TransportOptions(mode: mode, token: token),
                timeout: const Duration(seconds: 5),
                onTokenExpired: () async {
                  final request = await http.getUrl(
                    Uri.parse('$endpoint/refresh'),
                  );
                  request.headers.set('Authorization', 'Bearer $refresh');
                  final response = await request.close();
                  expect(response.statusCode, 200);
                  final access = await utf8.decoder.bind(response).join();
                  renewed.complete();
                  return access;
                },
              )..setSchema({
                'identity': const APISchemaEntry(7),
                'watch': const APISchemaEntry(8),
              });
          try {
            await transport.subscribe<Object?>(
              'watch',
              {},
              (value) {
                values++;
                if (!first.isCompleted) first.complete(value);
              },
              decoder: (value) => value,
              onError: (error) {
                errors++;
                if (!ended.isCompleted) ended.complete(error);
              },
              onDone: () {
                completed++;
              },
            );
            final expected = mode == TransportMode.json ? 42 : [84];
            expect(
              await first.future.timeout(const Duration(seconds: 5)),
              expected,
            );
            expect(
              (await ended.future.timeout(const Duration(seconds: 6))).code,
              401,
            );
            await renewed.future.timeout(const Duration(seconds: 6));
            expect(await transport.call<Object?>('identity'), expected);
            await _waitForLiveStreamCleanup(http, endpoint);
            expect(
              (await _liveStreamCounts(http, endpoint)).started,
              before + 1,
            );
            final fresh = Completer<Object?>();
            final stop = await transport.subscribe<Object?>(
              'watch',
              {},
              fresh.complete,
              decoder: (value) => value,
              onError: (error) => fresh.completeError(error),
            );
            expect(
              await fresh.future.timeout(const Duration(seconds: 5)),
              expected,
            );
            expect(
              (await _liveStreamCounts(http, endpoint)).started,
              before + 2,
            );
            stop();
            expect(errors, 1);
            expect(completed, 0);
            expect(values, 1);
          } finally {
            transport.close();
          }
        }
      } finally {
        http.close(force: true);
      }
    },
    skip:
        endpoint == null ||
            Platform.environment['LUXO_WS_RENEWAL_STREAM'] != '1'
        ? 'requires owned Go stream renewal fixture'
        : false,
  );
}

class _ClosingWSRequest extends GeneratedRequest {
  _ClosingWSRequest(this.close, this.failEncoding);
  final void Function() close;
  final bool failEncoding;
  @override
  int get apiID => 7;
  void _encode() {
    close();
    if (failEncoding) throw StateError('encoding failed after close');
  }

  @override
  void writeParameters(LuxoEncoder encoder) => _encode();
  @override
  Map<String, dynamic> toJSONParameters() {
    _encode();
    return {};
  }
}

Future<void> _verifyEncodingClose(
  TransportMode mode,
  bool subscription,
  bool failEncoding,
) async {
  final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
  final closed = Completer<void>();
  WebSocket? peer;
  var received = 0;
  server.listen((request) async {
    peer = await WebSocketTransformer.upgrade(request);
    peer!.listen(
      (_) => received++,
      onDone: () {
        if (!closed.isCompleted) closed.complete();
      },
    );
  });
  final transport = WsTransport(
    'ws://${server.address.host}:${server.port}',
    options: TransportOptions(mode: mode),
    autoReconnect: false,
  )..setSchema({'echo': const APISchemaEntry(7)});
  try {
    final request = _ClosingWSRequest(transport.close, failEncoding);
    final Future<Object?> operation = subscription
        ? transport.subscribe<Object?>(
            'echo',
            {},
            (_) => fail('closed handler ran'),
            decoder: (value) => value,
            request: request,
          )
        : transport.call<Object?>('echo', request: request);
    await expectLater(
      operation,
      throwsA(
        isA<LuxoError>().having(
          (error) => error.error,
          'error',
          'NetworkError',
        ),
      ),
    );
    await closed.future.timeout(const Duration(seconds: 2));
    expect(received, 0);
  } finally {
    transport.close();
    await peer?.close();
    await server.close(force: true);
  }
}
