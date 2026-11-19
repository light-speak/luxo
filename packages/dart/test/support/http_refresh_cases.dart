import 'dart:async';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:luxo_client/luxo_client.dart';

import 'websocket_id_cases.dart' show EmptyBinaryRequest;

Future<void> verifyHTTPBinaryPresence() async {
  var present = false, refreshed = 0;
  final transport = HttpTransport(
    'http://localhost/api',
    options: const TransportOptions(mode: TransportMode.binary),
    client: MockClient(
      (_) async => http.Response.bytes(present ? [0] : [], 200),
    ),
    onTokenExpired: () async {
      refreshed++;
      return 'unused';
    },
  );
  transport.setSchema({'health': const APISchemaEntry(1)});
  try {
    for (final generated in [false, true]) {
      final request = generated ? EmptyBinaryRequest(1) : null;
      present = false;
      try {
        await transport.call('health', request: request);
        throw StateError('missing binary HTTP body was accepted');
      } on LuxoError catch (error) {
        if (error.error != 'ParseError' || error.code != 200) rethrow;
      }
      present = true;
      final value = await transport.call('health', request: request);
      if (value is! List<int> || value.length != 1 || value.single != 0) {
        throw StateError('binary null marker was lost: $value');
      }
    }
    if (refreshed != 0) throw StateError('missing body triggered refresh');
  } finally {
    transport.close();
  }
}

Future<void> verifyHTTPRefreshIdentity(TransportMode mode) async {
  for (final replacement in ['different-user', 'original']) {
    for (final duringRefresh in [false, true]) {
      await _verifyIdentityReplacement(mode, replacement, duringRefresh);
    }
  }
}

Future<void> _verifyIdentityReplacement(
  TransportMode mode,
  String replacement,
  bool duringRefresh,
) async {
  final entered = Completer<void>();
  final response = Completer<http.Response>();
  final refresh = Completer<String?>();
  final tokens = <String?>[];
  var refreshes = 0;
  final transport = HttpTransport(
    'http://localhost/api',
    options: TransportOptions(mode: mode, token: 'original'),
    client: MockClient((request) async {
      tokens.add(request.headers['authorization']);
      if (tokens.length != 1) {
        return mode == TransportMode.json
            ? http.Response('{"data":42}', 200)
            : http.Response.bytes([84], 200);
      }
      if (duringRefresh) return _unauthorized(mode);
      entered.complete();
      return response.future;
    }),
    onTokenExpired: () {
      refreshes++;
      if (duringRefresh) entered.complete();
      return refresh.future;
    },
  );
  transport.setSchema({'health': const APISchemaEntry(1)});
  try {
    final outcome = transport
        .call<Object?>('health')
        .then<Object?>((value) => value, onError: (Object error) => error);
    await entered.future;
    transport.setToken(replacement);
    response.complete(_unauthorized(mode));
    refresh.complete('old-user-refreshed');
    final failure = await outcome;
    if (failure is! LuxoError || failure.code != 401 || tokens.length != 1) {
      throw StateError('stale identity refreshed or retried: $mode $failure');
    }
    await transport.call('health');
    if (tokens.last != 'Bearer $replacement' ||
        refreshes != (duringRefresh ? 1 : 0)) {
      throw StateError('explicit identity overwritten: $mode $tokens');
    }
  } finally {
    transport.close();
  }
}

Future<void> verifyHTTPRefreshFailure(
  TransportMode mode,
  String completion,
) async {
  final tokens = <String?>[];
  final callbackError = StateError('refresh unavailable');
  var refreshed = 0;
  final client = MockClient((request) async {
    tokens.add(request.headers['authorization']);
    if (tokens.length == 1) return _unauthorized(mode);
    return mode == TransportMode.json
        ? http.Response('{"data":42}', 200)
        : http.Response.bytes([84], 200);
  });
  final transport = HttpTransport(
    'http://localhost/api',
    client: client,
    options: TransportOptions(mode: mode, token: 'original'),
    onTokenExpired: () {
      refreshed++;
      if (completion == 'throw') throw callbackError;
      return completion == 'null'
          ? Future.value(null)
          : Future.error(callbackError);
    },
  );
  transport.setSchema({'health': APISchemaEntry(1, [])});
  Object? failure;
  try {
    try {
      await transport.call('health');
    } catch (error) {
      failure = error;
    }
    if (completion == 'null') {
      final error = failure;
      if (error is! LuxoError ||
          error.code != 401 ||
          error.error != 'Unauthorized') {
        throw StateError('refresh lost original unauthorized error: $error');
      }
    } else if (!identical(failure, callbackError)) {
      throw StateError('refresh lost callback error: $failure');
    }
    if (tokens.length != 1) throw StateError('failed refresh retried');
    await transport.call('health');
    if (tokens.length != 2 ||
        tokens.any((token) => token != 'Bearer original') ||
        refreshed != 1) {
      throw StateError('failed refresh changed credentials or retried');
    }
  } finally {
    transport.close();
  }
}

class _ObservedHttpTransport extends HttpTransport {
  bool closed = false;
  bool updatedAfterClose = false;

  _ObservedHttpTransport(
    TransportMode mode,
    http.Client client,
    Future<String?> Function() refresh,
  ) : super(
        'http://localhost/api',
        client: client,
        options: TransportOptions(mode: mode),
        onTokenExpired: refresh,
      );

  @override
  void setToken(String token) {
    if (closed) updatedAfterClose = true;
    super.setToken(token);
  }

  @override
  void close() {
    closed = true;
    super.close();
  }
}

Future<void> verifyHTTPRefreshCloseSettlement(TransportMode mode) async {
  for (var delay = 0; delay < 9; delay++) {
    final entered = Completer<void>();
    final refresh = Completer<String?>();
    final client = MockClient((_) async => _unauthorized(mode));
    final transport = _ObservedHttpTransport(mode, client, () {
      entered.complete();
      return refresh.future;
    });
    transport.setSchema({'health': APISchemaEntry(1, [])});
    final observed = transport
        .call('health')
        .then<void>((_) {}, onError: (Object _) {});
    try {
      await entered.future;
      refresh.complete('late-token');
      void closeAfter(int remaining) {
        if (remaining == 0) {
          transport.close();
        } else {
          scheduleMicrotask(() => closeAfter(remaining - 1));
        }
      }

      closeAfter(delay);
      await observed;
      if (transport.updatedAfterClose) {
        throw StateError(
          'refresh changed credentials after close: $mode delay=$delay',
        );
      }
    } finally {
      transport.close();
    }
  }
}

Future<void> verifyHTTPRefreshClose(TransportMode mode, bool failLate) async {
  final entered = Completer<void>();
  final refresh = Completer<String?>();
  var calls = 0;
  final client = MockClient((request) async {
    calls++;
    return _unauthorized(mode);
  });
  final transport = HttpTransport(
    'http://localhost/api',
    client: client,
    options: TransportOptions(mode: mode),
    onTokenExpired: () {
      entered.complete();
      return refresh.future;
    },
  );
  transport.setSchema({'health': APISchemaEntry(1, [])});
  var finished = false;
  Object? failure;
  final observed = transport
      .call('health')
      .then<void>(
        (_) {
          finished = true;
        },
        onError: (Object error) {
          finished = true;
          failure = error;
        },
      );
  try {
    await entered.future;
    transport.close();
    await Future<void>.delayed(Duration.zero);
    final error = failure;
    if (!finished || error is! LuxoError || error.error != 'CancelledError') {
      throw StateError(
        'close did not cancel refresh: finished=$finished error=$error',
      );
    }
  } finally {
    if (failLate) {
      refresh.completeError(StateError('late refresh failure'));
    } else {
      refresh.complete('late-token');
    }
    await observed;
    transport.close();
  }
  if (calls != 1) throw StateError('closed refresh resent a request: $calls');
}

http.Response _unauthorized(TransportMode mode) {
  if (mode == TransportMode.json) {
    return http.Response(
      '{"code":401,"error":"Unauthorized","message":"expired"}',
      401,
    );
  }
  final error = LuxoEncoder()
    ..writeFieldInt(1, LuxoInt64.fromInt(401))
    ..writeFieldString(2, 'Unauthorized')
    ..writeFieldString(3, 'expired')
    ..writeEnd();
  return http.Response.bytes(error.bytes(), 401);
}

Future<void> verifyHTTPRefreshDeadline(TransportMode mode) async {
  final entered = Completer<void>();
  final refresh = Completer<String?>();
  final tokens = <String?>[];
  final transport = HttpTransport(
    'http://localhost/api',
    timeout: const Duration(milliseconds: 100),
    options: TransportOptions(mode: mode, token: 'original'),
    client: MockClient((request) async {
      tokens.add(request.headers['authorization']);
      if (tokens.length == 1) return _unauthorized(mode);
      return mode == TransportMode.json
          ? http.Response('{"data":42}', 200)
          : http.Response.bytes([84], 200);
    }),
    onTokenExpired: () {
      entered.complete();
      return refresh.future;
    },
  );
  transport.setSchema({'health': const APISchemaEntry(1)});
  final outcome = transport
      .call<Object?>('health')
      .then<Object?>((v) => v, onError: (Object e) => e);
  try {
    await entered.future;
    final error = await outcome.timeout(
      const Duration(seconds: 1),
      onTimeout: () => 'hung',
    );
    if (error is! LuxoError || error.error != 'TimeoutError') {
      throw StateError('refresh ignored original deadline: $mode $error');
    }
    refresh.complete('late');
    await Future<void>.delayed(Duration.zero);
    if (tokens.length != 1) throw StateError('expired request retried');
    await transport.call('health');
    if (tokens.last != 'Bearer original')
      throw StateError('late token applied');
  } finally {
    if (!refresh.isCompleted) refresh.complete(null);
    await outcome;
    transport.close();
  }
}

Future<void> verifyHTTPRefreshRetryDeadline(TransportMode mode) async {
  final retryEntered = Completer<void>();
  final lateResponse = Completer<http.Response>();
  var requests = 0;
  final transport = HttpTransport(
    'http://localhost/api',
    timeout: const Duration(milliseconds: 500),
    options: TransportOptions(mode: mode),
    client: MockClient((_) async {
      if (++requests == 1) return _unauthorized(mode);
      retryEntered.complete();
      return lateResponse.future;
    }),
    onTokenExpired: () async {
      await Future<void>.delayed(const Duration(milliseconds: 300));
      return 'renewed';
    },
  );
  transport.setSchema({'health': const APISchemaEntry(1)});
  final outcome = transport
      .call<Object?>('health')
      .then<Object?>((v) => v, onError: (Object e) => e);
  try {
    await retryEntered.future;
    final error = await outcome.timeout(
      const Duration(milliseconds: 350),
      onTimeout: () => 'reset deadline',
    );
    if (error is! LuxoError || error.error != 'TimeoutError') {
      throw StateError('retry received a new timeout budget: $mode $error');
    }
    if (requests != 2) throw StateError('unexpected retry count: $requests');
  } finally {
    lateResponse.complete(http.Response('{"data":42}', 200));
    await outcome;
    transport.close();
  }
}

class _RefreshRequest extends GeneratedRequest {
  final Map<String, dynamic> params;
  int encodes = 0;
  _RefreshRequest(this.params);
  @override
  int get apiID => 1;
  @override
  void writeParameters(LuxoEncoder encoder) {
    encodes++;
    encoder.writeFieldInt(1, params['count'] as LuxoInt64);
  }

  @override
  Map<String, dynamic> toJSONParameters() {
    encodes++;
    return params;
  }
}

Future<void> verifyHTTPRefreshRequestSnapshot(
  TransportMode mode, {
  bool generated = false,
}) async {
  final params = <String, dynamic>{'count': LuxoInt64.fromInt(42)};
  final request = generated ? _RefreshRequest(params) : null;
  final bodies = <List<int>>[];
  late HttpTransport transport;
  transport = HttpTransport(
    'http://localhost/api',
    options: TransportOptions(mode: mode),
    client: MockClient((request) async {
      bodies.add(List<int>.of(request.bodyBytes));
      if (bodies.length == 1) return _unauthorized(mode);
      return mode == TransportMode.json
          ? http.Response('{"data":42}', 200)
          : http.Response.bytes([84], 200);
    }),
    onTokenExpired: () async {
      params['count'] = LuxoInt64.fromInt(99);
      transport.setSchema({
        'health': APISchemaEntry(generated ? 1 : 2, [
          ParamSchema(generated ? 1 : 3, 'count', 'Int'),
        ]),
      });
      return 'renewed';
    },
  );
  transport.setSchema({
    'health': APISchemaEntry(1, [ParamSchema(1, 'count', 'Int')]),
  });
  try {
    await transport.call(
      'health',
      params: generated ? null : params,
      request: request,
    );
    if (bodies.length != 2 || bodies[0].toString() != bodies[1].toString()) {
      throw StateError('refresh changed the original request: $mode $bodies');
    }
    if (request != null && request.encodes != 1)
      throw StateError('request encoded twice on refresh');
    await transport.call(
      'health',
      params: generated ? null : params,
      request: request,
    );
    if (bodies[0].toString() == bodies[2].toString()) {
      throw StateError('new call ignored changed parameters/schema');
    }
  } finally {
    transport.close();
  }
}
