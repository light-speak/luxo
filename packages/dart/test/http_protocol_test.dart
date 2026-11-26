import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:luxo_client/luxo_client.dart';
import 'package:test/test.dart';

void main() {
  test(
    'response envelope does not consume the JSON payload depth budget',
    () async {
      for (final leaf in ['0', '[]', '{}']) {
        for (final depth in [127, 128, 129]) {
          final payload = '${'[' * depth}$leaf${']' * depth}';
          var accepted = true;
          try {
            decodeLuxoJSONBytes(utf8.encode(payload));
          } on FormatException {
            accepted = false;
          }
          for (final status in [200, 400]) {
            final prefix = status == 200
                ? ''
                : '"error":"Custom","code":400,"message":"failed",';
            final transport = HttpTransport(
              'http://localhost/api',
              options: const TransportOptions(mode: TransportMode.json),
              client: MockClient(
                (_) async => http.Response('{$prefix"data":$payload}', status),
              ),
            );
            try {
              if (!accepted) {
                await expectLater(
                  transport.call<Object?>('echo'),
                  _error('ParseError', status),
                );
              } else if (status == 200) {
                expect(
                  encodeLuxoJSON(await transport.call<Object?>('echo')),
                  payload,
                );
              } else {
                await expectLater(
                  transport.call<Object?>('echo'),
                  throwsA(
                    isA<LuxoError>()
                        .having((e) => e.error, 'error', 'Custom')
                        .having((e) => encodeLuxoJSON(e.data), 'data', payload),
                  ),
                );
              }
            } finally {
              transport.close();
            }
          }
        }
      }
    },
  );
  test(
    'closed transport does not refresh a late unauthorized response',
    () async {
      for (final mode in TransportMode.values) {
        var refreshes = 0;
        late HttpTransport transport;
        final client = MockClient((request) async {
          transport.close();
          return _errorResponse(mode);
        });
        transport = HttpTransport(
          'https://example.test/api',
          client: client,
          options: TransportOptions(mode: mode),
          onTokenExpired: () async {
            refreshes++;
            return 'late-token';
          },
        )..setSchema({'echo': APISchemaEntry(1, [])});
        addTearDown(transport.close);
        await expectLater(
          transport.call<Object?>('echo'),
          _error('CancelledError', 0),
        );
        expect(refreshes, 0, reason: 'closed call must not start user refresh');
      }
    },
  );
  test(
    'non-object envelopes never refresh and do not poison later calls',
    () async {
      for (final status in [200, 401, 500]) {
        for (final body in [
          '[]',
          '[{"data":true}]',
          'null',
          'true',
          '42',
          '9223372036854775807',
          '1.234567890123456789',
          '"data"',
          '{}',
        ]) {
          var calls = 0, refreshes = 0;
          final transport = HttpTransport(
            'http://localhost/api',
            options: const TransportOptions(mode: TransportMode.json),
            client: MockClient((_) async {
              calls++;
              return calls == 1
                  ? http.Response(body, status)
                  : http.Response('{"data":null}', 200);
            }),
            onTokenExpired: () async {
              refreshes++;
              return 'must-not-refresh';
            },
          );
          try {
            await expectLater(
              transport.call<Object?>('echo'),
              _error('ParseError', status),
            );
            expect(refreshes, 0);
            expect(calls, 1);
            expect(await transport.call<Object?>('echo'), isNull);
            expect(calls, 2);
          } finally {
            transport.close();
          }
        }
      }
    },
  );
  for (final mode in TransportMode.values) {
    for (final lateUnauthorized in [false, true]) {
      test(
        '$mode three requests share refresh with a late 401=$lateUnauthorized',
        () async {
          final initial = List.generate(3, (_) => Completer<http.Response>());
          final sent = <String?>[];
          final allSent = Completer<void>();
          final refreshStarted = Completer<void>();
          final refresh = Completer<String?>();
          var initialRequests = 0, refreshes = 0;
          final transport = HttpTransport(
            'http://localhost/api',
            client: MockClient((request) async {
              final token = request.headers['authorization'];
              sent.add(token);
              if (token == 'Bearer expired') {
                final index = initialRequests++;
                if (index == 2) allSent.complete();
                return initial[index].future;
              }
              return _success(mode);
            }),
            options: TransportOptions(mode: mode, token: 'expired'),
            onTokenExpired: () {
              if (++refreshes == 1) refreshStarted.complete();
              return refresh.future;
            },
          )..setSchema({'echo': const APISchemaEntry(7)});
          addTearDown(transport.close);

          final outcomes = List.generate(
            3,
            (_) => transport
                .call<Object?>('echo')
                .then<Object?>((_) => null, onError: (Object error) => error),
          );
          await allSent.future;
          initial[0].complete(_errorResponse(mode));
          initial[1].complete(_errorResponse(mode));
          if (!lateUnauthorized) initial[2].complete(_errorResponse(mode));
          await refreshStarted.future;
          // Drain the already delivered responses while the shared callback is held.
          await Future<void>.delayed(Duration.zero);
          refresh.complete('renewed');
          if (lateUnauthorized) {
            expect(await outcomes[0], isNull);
            initial[2].complete(_errorResponse(mode));
          }
          final results = await Future.wait(outcomes);
          expect(
            [
              refreshes,
              results.where((value) => value == null).length,
              results
                  .whereType<LuxoError>()
                  .map((value) => value.code)
                  .toList(),
            ],
            [1, 3, <int>[]],
          );
          expect(sent, [
            'Bearer expired',
            'Bearer expired',
            'Bearer expired',
            'Bearer renewed',
            'Bearer renewed',
            'Bearer renewed',
          ]);
        },
      );
    }
  }

  for (final mode in TransportMode.values) {
    for (final replaceCredential in [false, true]) {
      test('$mode refresh callback settles after reentrant identity change: '
          'replace=$replaceCredential', () async {
        final refresh = Completer<String?>();
        final sent = <String?>[];
        var refreshes = 0;
        late final HttpTransport transport;
        transport = HttpTransport(
          'http://localhost/api',
          client: MockClient((request) async {
            final token = request.headers['authorization'];
            sent.add(token);
            return token == 'Bearer replacement'
                ? _success(mode)
                : _errorResponse(mode);
          }),
          options: TransportOptions(mode: mode, token: 'expired'),
          onTokenExpired: () {
            refreshes++;
            if (replaceCredential) {
              transport.setToken('replacement');
            } else {
              transport.close();
            }
            return refresh.future;
          },
        )..setSchema({'echo': const APISchemaEntry(7)});
        addTearDown(transport.close);

        await expectLater(
          transport.call<Object?>('echo'),
          replaceCredential
              ? _error('Unauthorized', 401)
              : _error('CancelledError', 0),
        ).timeout(const Duration(seconds: 2));
        expect(sent, ['Bearer expired']);

        if (replaceCredential) {
          refresh.complete('stale-result');
          await Future<void>.delayed(Duration.zero);
          await transport.call<Object?>('echo');
          expect(sent, ['Bearer expired', 'Bearer replacement']);
        } else {
          refresh.completeError(StateError('late callback error'));
          await Future<void>.delayed(Duration.zero);
          expect(sent, ['Bearer expired']);
        }
        expect(refreshes, 1);
      });
    }

    test('$mode close settles all waiters on one stalled refresh', () async {
      final initial = List.generate(2, (_) => Completer<http.Response>());
      final sent = Completer<void>();
      final started = Completer<void>();
      final refresh = Completer<String?>();
      var requests = 0, refreshes = 0;
      final transport = HttpTransport(
        'http://localhost/api',
        client: MockClient((_) async {
          final index = requests++;
          if (requests == initial.length) sent.complete();
          return initial[index].future;
        }),
        options: TransportOptions(mode: mode, token: 'expired'),
        onTokenExpired: () {
          if (++refreshes == 1) started.complete();
          return refresh.future;
        },
      )..setSchema({'echo': const APISchemaEntry(7)});
      addTearDown(transport.close);

      final waiters = List.generate(
        initial.length,
        (_) => expectLater(
          transport.call<Object?>('echo').timeout(const Duration(seconds: 2)),
          _error('CancelledError', 0),
        ),
      );
      await sent.future;
      for (final response in initial) {
        response.complete(_errorResponse(mode));
      }
      await started.future;
      await Future<void>.delayed(Duration.zero);
      expect(refreshes, 1);
      transport.close();
      await Future.wait(waiters);

      refresh.complete('late-result');
      await Future<void>.delayed(Duration.zero);
      expect(requests, initial.length);
    });
  }

  for (final mode in TransportMode.values) {
    test('$mode a timed-out waiter leaves the shared refresh usable', () async {
      final started = Completer<void>();
      final refresh = Completer<String?>();
      var refreshes = 0;
      final transport = HttpTransport(
        'http://localhost/api',
        timeout: const Duration(milliseconds: 600),
        client: MockClient((request) async {
          return request.headers['authorization'] == 'Bearer renewed'
              ? _success(mode)
              : _errorResponse(mode);
        }),
        options: TransportOptions(mode: mode, token: 'expired'),
        onTokenExpired: () {
          if (++refreshes == 1) started.complete();
          return refresh.future;
        },
      )..setSchema({'echo': const APISchemaEntry(7)});
      addTearDown(transport.close);

      final first = expectLater(
        transport.call<Object?>('echo'),
        _error('TimeoutError', 0),
      );
      await started.future;
      await Future<void>.delayed(const Duration(milliseconds: 300));
      final second = transport.call<Object?>('echo');
      await first;
      refresh.complete('renewed');
      await second;
      expect(refreshes, 1);
    });

    test(
      '$mode a late 401 can refresh after its sole peer times out',
      () async {
        final started = Completer<void>();
        final lateSent = Completer<void>();
        final late = Completer<http.Response>();
        final obsolete = Completer<String?>();
        final sent = <String?>[];
        var refreshes = 0;
        final transport = HttpTransport(
          'http://localhost/api',
          timeout: const Duration(milliseconds: 600),
          client: MockClient((request) async {
            final token = request.headers['authorization'];
            sent.add(token);
            if (sent.length == 1) return _errorResponse(mode);
            if (sent.length == 2) {
              lateSent.complete();
              return late.future;
            }
            return _success(mode);
          }),
          options: TransportOptions(mode: mode, token: 'expired'),
          onTokenExpired: () {
            if (++refreshes != 1) return Future.value('renewed');
            started.complete();
            return obsolete.future;
          },
        )..setSchema({'echo': const APISchemaEntry(7)});
        addTearDown(transport.close);

        final first = expectLater(
          transport.call<Object?>('echo'),
          _error('TimeoutError', 0),
        );
        await started.future;
        await Future<void>.delayed(const Duration(milliseconds: 300));
        final pending = transport.call<Object?>('echo');
        await lateSent.future;
        await first;
        obsolete.complete('obsolete');
        late.complete(_errorResponse(mode));
        await pending;
        expect(refreshes, 2);
        expect(sent, ['Bearer expired', 'Bearer expired', 'Bearer renewed']);
      },
    );

    for (final replacement in ['replacement', 'expired']) {
      test(
        '$mode invalidates the shared refresh on setToken($replacement)',
        () async {
          final started = List.generate(2, (_) => Completer<void>());
          final refresh = List.generate(2, (_) => Completer<String?>());
          final sent = <String?>[];
          var refreshes = 0;
          final transport = HttpTransport(
            'http://localhost/api',
            client: MockClient((request) async {
              final token = request.headers['authorization'];
              sent.add(token);
              return token == 'Bearer renewed'
                  ? _success(mode)
                  : _errorResponse(mode);
            }),
            options: TransportOptions(mode: mode, token: 'expired'),
            onTokenExpired: () {
              final index = refreshes++;
              started[index].complete();
              return refresh[index].future;
            },
          )..setSchema({'echo': const APISchemaEntry(7)});
          addTearDown(transport.close);

          final stale = List.generate(
            2,
            (_) => expectLater(
              transport.call<Object?>('echo'),
              _error('Unauthorized', 401),
            ),
          );
          await started[0].future;
          await Future<void>.delayed(Duration.zero);
          transport.setToken(replacement);
          await Future.wait(stale).timeout(const Duration(seconds: 2));

          final fresh = transport.call<Object?>('echo');
          await started[1].future;
          refresh[0].complete('stale-result');
          refresh[1].complete('renewed');
          await fresh;
          expect(sent, [
            'Bearer expired',
            'Bearer expired',
            'Bearer $replacement',
            'Bearer renewed',
          ]);
        },
      );
    }
  }
  for (final mode in TransportMode.values) {
    for (final throws in [false, true]) {
      test(
        '$mode late 401 shares settled refresh failure, throws=$throws',
        () async {
          final initial = List.generate(3, (_) => Completer<http.Response>());
          final allSent = Completer<void>();
          final refreshStarted = Completer<void>();
          final refresh = Completer<String?>();
          final failure = StateError('refresh failed');
          var issued = 0, refreshes = 0;
          final transport = HttpTransport(
            'http://localhost/api',
            client: MockClient((request) async {
              if (request.headers['authorization'] == 'Bearer renewed') {
                return _success(mode);
              }
              final index = issued++;
              if (index == 2) allSent.complete();
              return index < initial.length
                  ? initial[index].future
                  : _errorResponse(mode);
            }),
            options: TransportOptions(mode: mode, token: 'expired'),
            onTokenExpired: () {
              if (++refreshes != 1) return Future.value('renewed');
              refreshStarted.complete();
              return refresh.future;
            },
          )..setSchema({'echo': const APISchemaEntry(7)});
          addTearDown(transport.close);

          final stale = List.generate(
            3,
            (_) => transport
                .call<Object?>('echo')
                .then<Object?>((_) => null, onError: (Object error) => error),
          );
          await allSent.future;
          initial[0].complete(_errorResponse(mode));
          initial[1].complete(_errorResponse(mode));
          await refreshStarted.future;
          await Future<void>.delayed(Duration.zero);
          if (throws) {
            refresh.completeError(failure);
          } else {
            refresh.complete(null);
          }
          await stale.first;
          initial[2].complete(_errorResponse(mode));
          final results = await Future.wait(stale);
          for (final result in results) {
            if (throws) {
              expect(result, same(failure));
            } else {
              expect(
                result,
                isA<LuxoError>().having((e) => e.code, 'code', 401),
              );
            }
          }
          expect(refreshes, 1);
          await transport.call<Object?>('echo');
          expect(refreshes, 2);
        },
      );
    }
  }
  for (final mode in TransportMode.values) {
    for (final close in [false, true]) {
      test(
        '$mode settled refresh loses to same-turn invalidation, close=$close',
        () async {
          final started = Completer<void>();
          final refresh = Completer<String?>.sync();
          final sent = <String?>[];
          final transport = HttpTransport(
            'http://localhost/api',
            client: MockClient((request) async {
              final token = request.headers['authorization'];
              sent.add(token);
              return token == 'Bearer replacement'
                  ? _success(mode)
                  : _errorResponse(mode);
            }),
            options: TransportOptions(mode: mode, token: 'expired'),
            onTokenExpired: () {
              started.complete();
              return refresh.future;
            },
          )..setSchema({'echo': const APISchemaEntry(7)});
          addTearDown(transport.close);

          final pending = expectLater(
            transport.call<Object?>('echo'),
            close ? _error('CancelledError', 0) : _error('Unauthorized', 401),
          );
          await started.future;
          refresh.complete('obsolete');
          if (close) {
            transport.close();
          } else {
            transport.setToken('replacement');
          }
          await pending;
          expect(sent, ['Bearer expired']);
          if (!close) {
            await transport.call<Object?>('echo');
            expect(sent, ['Bearer expired', 'Bearer replacement']);
          }
        },
      );
    }

    test('$mode synchronous String refresh error keeps its stack', () async {
      const failure = 'refresh threw a String';
      const marker = 'refresh-callback-test-stack';
      final stack = StackTrace.fromString(marker);
      var sends = 0;
      final transport = HttpTransport(
        'http://localhost/api',
        client: MockClient((_) async {
          sends++;
          return _errorResponse(mode);
        }),
        options: TransportOptions(mode: mode, token: 'expired'),
        onTokenExpired: () => Error.throwWithStackTrace(failure, stack),
      )..setSchema({'echo': const APISchemaEntry(7)});
      addTearDown(transport.close);

      await transport
          .call<Object?>('echo')
          .then<void>(
            (_) => fail('refresh error was accepted as a token'),
            onError: (Object error, StackTrace trace) {
              expect(error, same(failure));
              expect(trace.toString(), contains(marker));
            },
          );
      expect(sends, 1);
    });
  }
  for (final mode in [TransportMode.json, TransportMode.binary]) {
    test('$mode stale 401 cannot refresh a replacement identity', () async {
      final entered = Completer<void>();
      final response = Completer<http.Response>();
      var refreshes = 0;
      final transport = HttpTransport(
        'http://localhost/api',
        options: TransportOptions(mode: mode, token: 'original-token'),
        client: MockClient((_) {
          entered.complete();
          return response.future;
        }),
        onTokenExpired: () async {
          refreshes++;
          return 'old-user-refreshed';
        },
      );
      try {
        transport.setSchema({'echo': const APISchemaEntry(7)});
        final failure = expectLater(
          transport.call<Object?>('echo'),
          _error('Unauthorized', 401),
        );
        await entered.future;
        transport.setToken('different-user');
        response.complete(_errorResponse(mode));
        await failure;
        expect(refreshes, 0);
      } finally {
        transport.close();
      }
    });
    for (final replacement in ['different-user', 'original-token']) {
      test('$mode late refresh cannot overwrite $replacement', () async {
        final entered = Completer<void>();
        final refresh = Completer<String?>();
        final requests = <http.Request>[];
        final transport = HttpTransport(
          'http://localhost/api',
          options: TransportOptions(mode: mode, token: 'original-token'),
          client: MockClient((request) async {
            requests.add(request);
            return requests.length == 1 ? _errorResponse(mode) : _success(mode);
          }),
          onTokenExpired: () {
            entered.complete();
            return refresh.future;
          },
        );
        try {
          transport.setSchema({'echo': const APISchemaEntry(7)});
          final result = transport
              .call<Object?>('echo')
              .then<Object?>(
                (value) => value,
                onError: (Object error) => error,
              );
          await entered.future;
          transport.setToken(replacement);
          refresh.complete('old-user-refreshed');
          final outcome = await result;
          final callsBefore = requests.length;
          await transport.call<Object?>('echo');
          expect(requests.last.headers['authorization'], 'Bearer $replacement');
          expect(callsBefore, 1);
          expect(outcome, isA<LuxoError>().having((e) => e.code, 'code', 401));
        } finally {
          transport.close();
        }
      });
    }
  }

  test('HTTP status and error presence override JSON data', () async {
    for (final (status, body, name) in [
      (
        200,
        '{"data":true,"error":null,"code":401,"message":"bad"}',
        'ParseError',
      ),
      (
        200,
        '{"data":true,"error":false,"code":401,"message":"bad"}',
        'ParseError',
      ),
      (
        200,
        '{"data":true,"error":"Unauthorized","code":401,"message":"bad"}',
        'Unauthorized',
      ),
      (201, '{"data":true}', 'ParseError'),
      (403, '{"data":true}', 'ParseError'),
      (500, '{"data":true}', 'ParseError'),
    ]) {
      var calls = 0, refreshes = 0;
      final transport = HttpTransport(
        'http://localhost/api',
        client: MockClient((_) async {
          calls++;
          return calls == 1
              ? http.Response(body, status)
              : http.Response('{"data":true}', 200);
        }),
        onTokenExpired: () async {
          refreshes++;
          return 'unused';
        },
      );
      try {
        await expectLater(
          transport.call<Object?>('echo'),
          _error(name, name == 'Unauthorized' ? 401 : status),
        );
        expect(calls, 1);
        expect(refreshes, 0);
        expect(await transport.call<Object?>('echo'), true);
        expect(calls, 2);
      } finally {
        transport.close();
      }
    }
  });

  test('success requires data while explicit null remains valid', () async {
    var body = '{}', refreshes = 0, calls = 0;
    final transport = HttpTransport(
      'http://localhost/luvia',
      client: MockClient((_) async {
        calls++;
        return http.Response(body, 200);
      }),
      onTokenExpired: () async {
        refreshes++;
        return 'unused';
      },
    );
    try {
      for (final missing in ['{}', '{"ok":true}', '{"value":42}']) {
        body = missing;
        await expectLater(
          transport.call<Object?>('echo'),
          _error('ParseError', 200),
        );
      }
      for (final data in [null, false, 0, '', [], <String, Object?>{}]) {
        body = jsonEncode({'data': data});
        expect(await transport.call<Object?>('echo'), data);
      }
      expect(refreshes, 0);
      expect(calls, 9);
    } finally {
      transport.close();
    }
  });

  test('malformed Unicode success and error bodies fail without token refresh', () async {
    for (final status in [200, 401, 500]) {
      for (final value in [
        r'"\ud800"',
        r'{"\udfff":1}',
        r'{"x":"\ud800","x":"valid"}',
      ]) {
        var refreshes = 0;
        final transport = HttpTransport(
          'http://localhost/luvia',
          client: MockClient(
            (_) async => http.Response(
              '{"error":"Rejected","code":$status,"message":"bad","data":$value}',
              status,
            ),
          ),
          onTokenExpired: () async {
            refreshes++;
            return 'unused';
          },
        );
        try {
          await expectLater(
            transport.call<Object?>('echo'),
            _error('ParseError', status),
          );
          expect(refreshes, 0);
        } finally {
          transport.close();
        }
      }
    }
    final transport = HttpTransport(
      'http://localhost/luvia',
      client: MockClient((_) async => http.Response(r'{"data":"\ud800"}', 200)),
    );
    try {
      await expectLater(
        transport.call<Object?>('echo'),
        _error('ParseError', 200),
      );
    } finally {
      transport.close();
    }
  });
  for (final mode in TransportMode.values) {
    test(
      '$mode cache overload preserves identity without retry or refresh',
      () async {
        var calls = 0, refreshes = 0;
        final transport = HttpTransport(
          'http://localhost/luvia',
          options: TransportOptions(mode: mode),
          client: MockClient((_) async {
            calls++;
            return calls == 1
                ? _errorResponse(
                    mode,
                    status: 503,
                    code: 503,
                    name: 'CacheOriginBusy',
                    message: 'Cache origin capacity exhausted',
                  )
                : _success(mode);
          }),
          onTokenExpired: () async {
            refreshes++;
            return 'must-not-refresh';
          },
        );
        try {
          transport.setSchema({'echo': const APISchemaEntry(7)});
          await expectLater(
            transport.call<Object?>('echo'),
            throwsA(
              isA<LuxoError>()
                  .having((error) => error.error, 'name', 'CacheOriginBusy')
                  .having((error) => error.code, 'code', 503)
                  .having(
                    (error) => error.message,
                    'message',
                    'Cache origin capacity exhausted',
                  ),
            ),
          );
          expect(calls, 1);
          expect(refreshes, 0);
          final result = await transport.call<Object?>('echo');
          expect(
            result,
            mode == TransportMode.json ? 42 : Uint8List.fromList([84]),
          );
          expect(calls, 2);
        } finally {
          transport.close();
        }
      },
    );
    test('$mode rejects malformed input before HTTP send', () async {
      var calls = 0;
      final transport = HttpTransport(
        'http://localhost/luvia',
        options: TransportOptions(mode: mode),
        client: MockClient((_) async {
          calls++;
          return _success(mode);
        }),
      );
      transport.setSchema({
        'echo': const APISchemaEntry(1, [ParamSchema(1, 'value', 'String')]),
      });
      try {
        await expectLater(
          transport.call<Object?>('echo', params: {'value': '\ud800'}),
          throwsFormatException,
        );
        expect(calls, 0);
      } finally {
        transport.close();
      }
    });
    test('$mode network failures never refresh or resend', () async {
      var calls = 0, refreshes = 0;
      final transport = HttpTransport(
        'http://localhost/luvia',
        options: TransportOptions(mode: mode),
        client: MockClient((_) async {
          calls++;
          throw http.ClientException('connection closed');
        }),
        onTokenExpired: () async {
          refreshes++;
          return 'must-not-be-used';
        },
      );
      try {
        transport.setSchema({'echo': const APISchemaEntry(7)});
        await expectLater(
          transport.call<Object?>('echo'),
          _error('NetworkError', 0),
        );
        expect(calls, 1);
        expect(refreshes, 0);
      } finally {
        transport.close();
      }
    });

    test(
      '$mode headers match the payload and cannot be overridden by casing',
      () async {
        for (final headers in [
          <String, String>{},
          {'Content-Type': 'text/plain', 'X-Luxo-Mode': 'wrong'},
          {'content-type': 'text/plain', 'x-luxo-mode': 'wrong'},
          {'CoNtEnT-TyPe': 'text/plain', 'X-LuXo-MoDe': 'wrong'},
        ]) {
          headers.addAll({'X-Custom': 'original', 'aUtHoRiZaTiOn': 'ignored'});
          final sent = <http.Request>[];
          final transport = HttpTransport(
            'http://localhost/luvia',
            options: TransportOptions(
              mode: mode,
              token: 'initial',
              headers: headers,
            ),
            client: MockClient((request) async {
              sent.add(request);
              return _success(mode);
            }),
          );
          try {
            transport.setSchema({'echo': const APISchemaEntry(7)});
            headers['X-Custom'] = 'mutated';
            await transport.call<Object?>('echo');
            transport.setToken('renewed');
            await transport.call<Object?>('echo');
            expect(sent, hasLength(2));
            for (final request in sent) {
              _expectHeaders(request, mode);
              expect(request.headers['x-custom'], 'original');
            }
            expect(sent.first.headers['authorization'], 'Bearer initial');
            expect(sent.last.headers['authorization'], 'Bearer renewed');
          } finally {
            transport.close();
          }
        }
      },
    );

    test(
      '$mode malformed 401 never refreshes or resends the request',
      () async {
        for (final response in _malformedErrors(mode)) {
          var calls = 0, refreshes = 0;
          final transport = HttpTransport(
            'http://localhost/luvia',
            options: TransportOptions(mode: mode),
            client: MockClient((_) async {
              calls++;
              return response;
            }),
            onTokenExpired: () async {
              refreshes++;
              return 'must-not-be-used';
            },
          );
          try {
            transport.setSchema({'echo': const APISchemaEntry(7)});
            await expectLater(
              transport.call<Object?>('echo'),
              _error('ParseError', 401),
            );
            expect(refreshes, 0, reason: response.bodyBytes.toString());
            expect(calls, 1);
          } finally {
            transport.close();
          }
        }
      },
    );

    test(
      '$mode a valid 401 refreshes once and keeps its original request mode',
      () async {
        var refreshes = 0;
        final requests = <http.Request>[];
        late final HttpTransport transport;
        transport = HttpTransport(
          'http://localhost/luvia',
          options: TransportOptions(mode: mode, token: 'old'),
          client: MockClient((request) async {
            requests.add(request);
            return requests.length == 1 ? _errorResponse(mode) : _success(mode);
          }),
          onTokenExpired: () async {
            refreshes++;
            transport.setMode(
              mode == TransportMode.json
                  ? TransportMode.binary
                  : TransportMode.json,
            );
            return 'new';
          },
        );
        try {
          transport.setSchema({'echo': const APISchemaEntry(7)});
          await transport.call<Object?>('echo');
          expect(refreshes, 1);
          expect(requests, hasLength(2));
          for (final request in requests) _expectHeaders(request, mode);
          expect(requests.first.headers['authorization'], 'Bearer old');
          expect(requests.last.headers['authorization'], 'Bearer new');
        } finally {
          transport.close();
        }
      },
    );

    test('$mode retries only matching unauthorized status and code', () async {
      for (final (status, code) in [(403, 401), (401, 403), (401, 0)]) {
        var refreshes = 0, calls = 0;
        final transport = HttpTransport(
          'http://localhost/luvia',
          options: TransportOptions(mode: mode),
          client: MockClient((_) async {
            calls++;
            return _errorResponse(mode, status: status, code: code);
          }),
          onTokenExpired: () async {
            refreshes++;
            return 'must-not-be-used';
          },
        );
        try {
          transport.setSchema({'echo': const APISchemaEntry(7)});
          await expectLater(
            transport.call<Object?>('echo'),
            _error(
              code == 0 ? 'ParseError' : 'Unauthorized',
              code == 0 ? status : code,
            ),
          );
          expect(refreshes, 0);
          expect(calls, 1);
        } finally {
          transport.close();
        }
      }
    });

    test(
      '$mode refresh termination preserves errors and never loops',
      () async {
        for (final action in [
          'absent',
          'decline',
          'retry',
          'throw',
          'malformed',
        ]) {
          var calls = 0, refreshes = 0;
          final failure = StateError('refresh failed');
          final transport = HttpTransport(
            'http://localhost/luvia',
            options: TransportOptions(mode: mode),
            client: MockClient((_) async {
              calls++;
              return action == 'malformed' && calls == 2
                  ? _malformedErrors(mode).first
                  : _errorResponse(mode);
            }),
            onTokenExpired: action == 'absent'
                ? null
                : () async {
                    refreshes++;
                    if (action == 'throw') throw failure;
                    return action == 'decline' ? null : 'new';
                  },
          );
          try {
            transport.setSchema({'echo': const APISchemaEntry(7)});
            await expectLater(
              transport.call<Object?>('echo'),
              action == 'throw'
                  ? throwsA(same(failure))
                  : _error(
                      action == 'malformed' ? 'ParseError' : 'Unauthorized',
                      401,
                    ),
            );
            expect(calls, action == 'retry' || action == 'malformed' ? 2 : 1);
            expect(refreshes, action == 'absent' ? 0 : 1);
          } finally {
            transport.close();
          }
        }
      },
    );
  }

  test(
    'HTTP subscription and unknown binary API fail before sending',
    () async {
      var calls = 0;
      final transport = HttpTransport(
        'http://localhost/luvia',
        options: const TransportOptions(mode: TransportMode.binary),
        client: MockClient((_) async {
          calls++;
          return _success(TransportMode.binary);
        }),
      );
      try {
        expect(
          () => transport.subscribe(
            'echo',
            {},
            (_) {},
            decoder: (value) => value,
          ),
          _error('ConfigError', 0),
        );
        await expectLater(
          transport.call<Object?>('echo'),
          _error('ConfigError', 0),
        );
        expect(calls, 0);
      } finally {
        transport.close();
      }
    },
  );

  test(
    'mode switches and tokens cannot leave stale protocol headers',
    () async {
      final modes = [
        TransportMode.json,
        TransportMode.binary,
        TransportMode.json,
      ];
      final requests = <http.Request>[];
      final transport = HttpTransport(
        'http://localhost/luvia',
        options: const TransportOptions(
          headers: {'AUTHORIZATION': 'Bearer custom'},
        ),
        client: MockClient((request) async {
          requests.add(request);
          return _success(modes[requests.length - 1]);
        }),
      );
      try {
        transport.setSchema({'echo': const APISchemaEntry(7)});
        for (final mode in modes) {
          transport.setMode(mode);
          await transport.call<Object?>('echo');
          _expectHeaders(requests.last, mode);
          expect(requests.last.headers['authorization'], 'Bearer custom');
        }
      } finally {
        transport.close();
      }
    },
  );

  test(
    'JSON non-success status cannot return a successful data envelope',
    () async {
      for (final status in [201, 301, 400, 403, 500]) {
        final transport = HttpTransport(
          'http://localhost/luvia',
          client: MockClient((_) async => http.Response('{"data":42}', status)),
        );
        try {
          await expectLater(
            transport.call<Object?>('echo'),
            _error('ParseError', status),
          );
        } finally {
          transport.close();
        }
      }
    },
  );

  test(
    'JSON invalid UTF-8 cannot refresh or return replacement characters',
    () async {
      for (final status in [200, 401]) {
        for (final charset in ['utf-8', 'iso-8859-1']) {
          final prefix = status == 200
              ? '{"data":"'
              : '{"error":"Unauthorized","code":401,"message":"';
          var refreshes = 0;
          final transport = HttpTransport(
            'http://localhost/luvia',
            client: MockClient(
              (_) async => http.Response.bytes(
                [...utf8.encode(prefix), 255, ...utf8.encode('"}')],
                status,
                headers: {'content-type': 'application/json; charset=$charset'},
              ),
            ),
            onTokenExpired: () async {
              refreshes++;
              return 'must-not-be-used';
            },
          );
          try {
            await expectLater(
              transport.call<Object?>('echo'),
              _error('ParseError', status),
            );
            expect(refreshes, 0);
          } finally {
            transport.close();
          }
        }
      }
    },
  );

  test(
    'JSON preserves UTF-8 text even when a charset header disagrees',
    () async {
      for (final status in [200, 401]) {
        for (final charset in ['utf-8', 'iso-8859-1']) {
          const message = '任务已完成 · café';
          final body = status == 200
              ? '{"data":"$message"}'
              : '{"error":"Unauthorized","code":401,"message":"$message"}';
          var refreshes = 0;
          final transport = HttpTransport(
            'http://localhost/luvia',
            client: MockClient(
              (_) async => http.Response.bytes(
                utf8.encode(body),
                status,
                headers: {'content-type': 'application/json; charset=$charset'},
              ),
            ),
            onTokenExpired: () async {
              refreshes++;
              return null;
            },
          );
          try {
            if (status == 200) {
              expect(await transport.call<String>('echo'), message);
            } else {
              await expectLater(
                transport.call<Object?>('echo'),
                throwsA(
                  isA<LuxoError>()
                      .having((error) => error.error, 'name', 'Unauthorized')
                      .having((error) => error.message, 'message', message),
                ),
              );
            }
            expect(refreshes, status == 401 ? 1 : 0);
          } finally {
            transport.close();
          }
        }
      }
    },
  );
}

void _expectHeaders(http.Request request, TransportMode mode) {
  expect(
    request.headers['content-type'],
    mode == TransportMode.json ? 'application/json' : 'application/x-luxo',
  );
  expect(request.headers['x-luxo-mode'], mode.name);
  if (mode == TransportMode.json) {
    expect(decodeLuxoJSON(request.body), {r'$api': 'echo'});
  } else {
    expect(request.bodyBytes, [7, 0, 0]);
  }
}

Matcher _error(String name, int code) => throwsA(
  isA<LuxoError>()
      .having((error) => error.error, 'name', name)
      .having((error) => error.code, 'code', code),
);

http.Response _success(TransportMode mode) => mode == TransportMode.json
    ? http.Response('{"data":42}', 200)
    : http.Response.bytes([84], 200);

http.Response _errorResponse(
  TransportMode mode, {
  int status = 401,
  int code = 401,
  String name = 'Unauthorized',
  String message = 'expired',
}) {
  if (mode == TransportMode.json) {
    return http.Response(
      '{"error":"$name","code":$code,"message":"$message"}',
      status,
    );
  }
  final encoder = LuxoEncoder()
    ..writeFieldInt(1, LuxoInt64.fromInt(code))
    ..writeFieldString(2, name)
    ..writeFieldString(3, message)
    ..writeEnd();
  return http.Response.bytes(encoder.bytes(), status);
}

Iterable<http.Response> _malformedErrors(TransportMode mode) sync* {
  if (mode == TransportMode.json) {
    for (final body in [
      '',
      '{',
      '[]',
      '{}',
      '{"data":42}',
      '{"error":"Unauthorized","message":"expired"}',
      '{"error":"Unauthorized","code":true,"message":"expired"}',
      '{"error":"Unauthorized","code":401.0,"message":"expired"}',
      '{"error":"Unauthorized","code":9223372036854775807,"message":"expired"}',
      '{"error":"Unauthorized","code":401,"message":false}',
    ]) {
      yield http.Response(body, 401);
    }
  } else {
    for (final bytes in [
      <int>[],
      [0],
      [1, 0x81],
      utf8.encode('{"error":"Unauthorized","code":401,"message":"expired"}'),
      [..._errorResponse(mode).bodyBytes, 0],
    ]) {
      yield http.Response.bytes(Uint8List.fromList(bytes), 401);
    }
  }
}
