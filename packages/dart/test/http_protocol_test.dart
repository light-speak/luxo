import 'dart:convert';
import 'dart:typed_data';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:luxo_client/luxo_client.dart';
import 'package:test/test.dart';

void main() {
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
            _error('Unauthorized', code),
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
