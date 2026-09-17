@TestOn('vm')
library;

import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:luxo_client/luxo_client.dart';
import 'package:luxo_client/src/transport.dart' show wsTransportWithSequence;
import 'package:test/test.dart';

void main() {
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
        () => _verifyMalformedJSON(mode, body),
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

    test(
      '$mode decode failure terminates only the damaged subscription',
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
              for (final value in [7, 9, 7, 9]) _data(socket, mode, id, value);
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
                if (value != 7) throw const FormatException('invalid sample');
                return value;
              },
              onError: (error) {
                errors++;
                failure.complete(error);
              },
              onDone: () => fail('decode error became normal completion'),
            );
            expect((await failure.future).error, 'DecodeError');
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
            socket.add([0x15, 128]);
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

  test(
    'retired and malformed Binary subscription frames are ignored',
    () async {
      await _withSocket(
        TransportMode.binary,
        (socket, message) {
          final (_, cancel) = _control(message);
          if (cancel) return;
          for (final frame in <List<int>>[
            [0x10, 1],
            [0x11, 1],
            [0x17, 1],
            [0xff, 1],
            [7, 1],
            [6, 1, 99],
            [8, 1],
            [0x13, 1, 0],
            [0x13, 0x81],
            [0x13, 0x81, ...List.filled(8, 128), 2],
            [0x13, 0x81, ...List.filled(9, 128), 0],
          ]) {
            socket.add(frame);
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

Future<void> _verifyMalformedJSON(TransportMode mode, String body) async {
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
