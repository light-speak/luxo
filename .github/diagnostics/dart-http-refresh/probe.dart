import 'dart:async';
import 'dart:convert';
import 'dart:developer';
import 'dart:io';
import 'dart:isolate';
import 'dart:typed_data';

import 'package:http/http.dart' as http;
import 'package:luxo_client/luxo_client.dart';

enum Scenario { noHook, withHook, refresh, triplet }

const _api = 'echo';
const _oldToken = 'issued-a';
const _newToken = 'issued-b';
const _oldAuth = 'Bearer $_oldToken';
const _newAuth = 'Bearer $_newToken';

Never _fail(String reason) => throw StateError(reason);

final class Wire extends http.BaseClient {
  final TransportMode mode;
  final Scenario scenario;
  final List<int> requestBytes;
  final Uint8List success;
  final Uint8List unauthorized;
  int sends = 0;
  int rejected = 0;
  int refreshes = 0;
  int successes = 0;
  int failures = 0;
  String _auth = _oldAuth;
  bool _awaitingRetry = false;
  int _waveArrivals = 0;
  Completer<void>? _wave;

  Wire(this.mode, this.scenario)
    : requestBytes = mode == TransportMode.json
          ? utf8.encode(r'{"$api":"echo"}')
          : const [7, 0, 0],
      success = Uint8List.fromList(
        mode == TransportMode.json ? utf8.encode('{"data":42}') : const [84],
      ),
      unauthorized = _rejection(mode);

  static Uint8List _rejection(TransportMode mode) {
    if (mode == TransportMode.json) {
      return Uint8List.fromList(
        utf8.encode('{"error":"Unauthorized","code":401,"message":"expired"}'),
      );
    }
    return (LuxoEncoder()
          ..writeFieldInt(1, LuxoInt64.fromInt(401))
          ..writeFieldString(2, 'Unauthorized')
          ..writeFieldString(3, 'expired')
          ..writeEnd())
        .bytes();
  }

  void beginWave() {
    _waveArrivals = 0;
    _wave = Completer<void>();
  }

  void resetCounters() {
    if (_awaitingRetry) _fail('warmup has an unfinished request');
    sends = 0;
    rejected = 0;
    refreshes = 0;
    successes = 0;
    failures = 0;
  }

  Future<String?> renew() async {
    refreshes++;
    if (scenario == Scenario.triplet) {
      // Let the three already-sent 401s reach the production refresh path.
      await Future<void>.delayed(Duration.zero);
      return _newToken;
    }
    if (scenario != Scenario.refresh || !_awaitingRetry) {
      _fail('unexpected refresh callback');
    }
    _auth = _auth == _oldAuth ? _newAuth : _oldAuth;
    return _auth == _newAuth ? _newToken : _oldToken;
  }

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    sends++;
    if (request is! http.AbortableRequest ||
        request.method != 'POST' ||
        request.followRedirects ||
        request.headers['x-luxo-mode'] != mode.name) {
      _fail('wrong production HTTP request');
    }
    final requestBody = request.bodyBytes;
    if (requestBody.length != requestBytes.length)
      _fail('wrong request length');
    for (var i = 0; i < requestBody.length; i++) {
      if (requestBody[i] != requestBytes[i]) _fail('wrong request bytes');
    }
    request.finalize();
    final actual = request.headers['authorization'];
    if (scenario == Scenario.triplet) {
      if (actual == _oldAuth) {
        final barrier = _wave!;
        if (++_waveArrivals == 3) barrier.complete();
        await barrier.future;
        return _response(request, 401, unauthorized);
      }
      if (actual != _newAuth) _fail('wrong triplet retry credential');
      return _response(request, 200, success);
    }
    if (actual != _auth) _fail('wrong sequential credential');
    if (scenario == Scenario.refresh) {
      _awaitingRetry = !_awaitingRetry;
      if (_awaitingRetry) return _response(request, 401, unauthorized);
    }
    return _response(request, 200, success);
  }

  http.StreamedResponse _response(
    http.BaseRequest request,
    int status,
    Uint8List body,
  ) {
    if (status == 401) rejected++;
    return http.StreamedResponse(
      Stream<List<int>>.value(Uint8List.fromList(body)),
      status,
      request: request,
      contentLength: body.length,
    );
  }
}

Future<void> _request(HttpTransport transport, Wire wire) async {
  try {
    final outcome = await transport.call<Object?>(_api);
    if (wire.mode == TransportMode.json) {
      if (outcome != 42) _fail('incorrect JSON response');
    } else if (outcome is! Uint8List ||
        outcome.length != 1 ||
        outcome.single != 84) {
      _fail('incorrect binary response');
    }
    wire.successes++;
  } on LuxoError catch (error) {
    if (wire.scenario != Scenario.triplet ||
        error.code != 401 ||
        error.error != 'Unauthorized' ||
        error.message != 'expired') {
      rethrow;
    }
    wire.failures++;
  }
}

Future<void> _run(int count, HttpTransport transport, Wire wire) async {
  if (wire.scenario == Scenario.triplet) {
    for (var i = 0; i < count; i++) {
      transport.setToken(_oldToken);
      wire.beginWave();
      await Future.wait([
        _request(transport, wire),
        _request(transport, wire),
        _request(transport, wire),
      ]);
    }
    return;
  }
  for (var i = 0; i < count; i++) {
    await _request(transport, wire);
  }
}

final class Allocations {
  final ReceivePort incoming;
  final StreamIterator<dynamic> messages;
  final Isolate observer;
  final SendPort commands;

  Allocations(this.incoming, this.messages, this.observer, this.commands);

  static Future<Allocations> start(String script) async {
    final info = await Service.getInfo();
    final websocket = info.serverWebSocketUri;
    if (websocket == null) _fail('VM service unavailable');
    final incoming = ReceivePort();
    final responses = StreamIterator<dynamic>(incoming);
    Isolate? peer;
    try {
      peer = await Isolate.spawnUri(
        Uri.file(script),
        [websocket.toString(), Service.getIsolateId(Isolate.current)!],
        incoming.sendPort,
        packageConfig: await Isolate.packageConfig,
      );
      if (!await responses.moveNext()) _fail('VM profiler did not start');
      return Allocations(
        incoming,
        responses,
        peer,
        responses.current as SendPort,
      );
    } catch (_) {
      peer?.kill(priority: Isolate.immediate);
      incoming.close();
      await responses.cancel();
      rethrow;
    }
  }

  Future<List<int>> profile(bool reset) async {
    commands.send(reset);
    if (!await messages.moveNext()) _fail('VM profiler stopped');
    return (messages.current as List).cast<int>();
  }

  Future<void> close() async {
    observer.kill(priority: Isolate.immediate);
    incoming.close();
    await messages.cancel();
  }
}

void _verify(Wire wire, String role, int count) {
  final result = (wire.sends, wire.rejected, wire.successes, wire.failures);
  switch (wire.scenario) {
    case Scenario.noHook || Scenario.withHook:
      if (wire.refreshes != 0 || result != (count, 0, count, 0)) {
        _fail('success fixture work changed: $result');
      }
    case Scenario.refresh:
      if (wire.refreshes != count || result != (2 * count, count, count, 0)) {
        _fail('single refresh fixture work changed: $result');
      }
    case Scenario.triplet:
      final successes = role == 'A' ? count : 3 * count;
      if (result !=
              (
                3 * count + successes,
                3 * count,
                successes,
                3 * count - successes,
              ) ||
          (role == 'A'
              ? wire.refreshes < count || wire.refreshes > 3 * count
              : wire.refreshes != count)) {
        _fail('three-request diagnostic invariant failed: $result');
      }
  }
}

Future<void> main(List<String> args) async {
  if (args.length < 6)
    _fail('expected scenario codec role operations warmup measurement');
  final scenario = Scenario.values.byName(args[0]);
  final mode = TransportMode.values.byName(args[1]);
  final role = args[2];
  final ops = int.parse(args[3]);
  final warmup = int.parse(args[4]);
  final measure = args[5];
  if (!['A', 'B'].contains(role) ||
      ops <= 0 ||
      warmup < 0 ||
      (scenario == Scenario.triplet
          ? measure != 'diagnostic'
          : measure == 'diagnostic')) {
    _fail('invalid measurement inputs');
  }
  final wire = Wire(mode, scenario);
  final transport = HttpTransport(
    'http://benchmark.invalid/api',
    options: TransportOptions(mode: mode, token: _oldToken),
    client: wire,
    onTokenExpired: scenario == Scenario.noHook ? null : wire.renew,
  )..setSchema({_api: const APISchemaEntry(7)});
  Allocations? observer;
  try {
    if (measure == 'alloc') observer = await Allocations.start(args[6]);
    if (warmup > 0) {
      await _run(warmup, transport, wire);
      _verify(wire, role, warmup);
      wire.resetCounters();
    }
    final before = await observer?.profile(true);
    final timer = measure == 'time' ? (Stopwatch()..start()) : null;
    await _run(ops, transport, wire);
    timer?.stop();
    final after = await observer?.profile(false);
    _verify(wire, role, ops);
    stdout.writeln(
      jsonEncode({
        'probe': 'dart-http-refresh',
        'scenario': scenario.name,
        'codec': mode.name,
        'role': role,
        'ops': ops,
        'warmup': warmup,
        'measure': measure,
        'sends': wire.sends,
        'unauthorized': wire.rejected,
        'successes': wire.successes,
        'failures': wire.failures,
        'refreshes': wire.refreshes,
        if (timer != null)
          'elapsedNs': timer.elapsedTicks * 1000000000 / timer.frequency,
        if (before != null && after != null) ...{
          'managedBytes': after[0] - before[0],
          'managedObjects': after[1] - before[1],
        },
      }),
    );
  } finally {
    await observer?.close();
    transport.close();
  }
}
