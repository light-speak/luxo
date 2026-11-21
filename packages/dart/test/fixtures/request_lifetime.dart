import 'dart:async';
import 'dart:convert';
import 'dart:developer' as developer;
import 'dart:io';
import 'dart:isolate';
import 'dart:typed_data';

import 'package:luxo_client/luxo_client.dart';

final class AuditRequest extends GeneratedRequest {
  final Uint8List payload;
  AuditRequest(this.payload);
  @override
  int get apiID => 7;
  @override
  void writeParameters(LuxoEncoder encoder) {
    encoder.writeVarint(1);
    encoder.writeBytes(payload);
  }

  @override
  Map<String, dynamic> toJSONParameters() => {'payload': payload};
}

final class Probe {
  final Future<Object?> future;
  final WeakReference<Object> input;
  final WeakReference<Uint8List> bytes;
  Probe(this.future, this.input, this.bytes);
}

int decode(dynamic value) => value is Uint8List
    ? LuxoDecoder(value).readIntChecked().toInt()
    : value as int;

@pragma('vm:never-inline')
Probe start(HttpTransport transport, bool generated) {
  final bytes = Uint8List(256 * 1024)..fillRange(0, 256 * 1024, 37);
  if (generated) {
    final request = AuditRequest(bytes);
    return Probe(
      transport.call<int>('write', request: request, decoder: decode),
      WeakReference<Object>(request),
      WeakReference(bytes),
    );
  }
  final params = <String, dynamic>{'payload': bytes};
  return Probe(
    transport.call<int>('write', params: params, decoder: decode),
    WeakReference<Object>(params),
    WeakReference(bytes),
  );
}

@pragma('vm:never-inline')
WeakReference<Uint8List> unused() => WeakReference(Uint8List(1024));

Future<void> main(List<String> args) async {
  final info = await developer.Service.getInfo();
  final service = await GCService.connect(info.serverWebSocketUri!);
  final isolate = developer.Service.getIsolateId(Isolate.current)!;
  final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
  final rows = <Object?>[];
  Completer<void>? entered;
  Completer<void>? released;
  var calls = 0;
  final listener = server.listen((request) async {
    var length = 0;
    await for (final chunk in request) {
      length += chunk.length;
    }
    if (length < 256 * 1024)
      throw StateError('body not fully received: $length');
    calls++;
    entered!.complete();
    await released!.future;
    request.response.statusCode = 200;
    if (request.headers.value('x-luxo-mode') == 'binary') {
      request.response.add([84]);
    } else {
      request.response.write('{"data":42}');
    }
    await request.response.close();
  });
  Future<void> collect() async {
    for (var i = 0; i < 8; i++) {
      await service.collect(isolate);
      await Future<void>.delayed(Duration.zero);
    }
  }

  try {
    for (final mode in TransportMode.values) {
      for (final generated in [false, true]) {
        entered = Completer<void>();
        released = Completer<void>();
        final transport = HttpTransport(
          'http://127.0.0.1:${server.port}',
          options: TransportOptions(mode: mode),
        );
        transport.setSchema({
          'write': const APISchemaEntry(7, [
            ParamSchema(1, 'payload', 'Bytes'),
          ]),
        });
        final probe = start(transport, generated);
        try {
          await entered.future.timeout(const Duration(seconds: 5));
          final negative = unused();
          final strong = Uint8List(12345)..[0] = 97;
          final positive = WeakReference(strong);
          await collect();
          final before = {
            'input': probe.input.target != null,
            'bytes': probe.bytes.target != null,
          };
          if (negative.target != null ||
              !identical(strong, positive.target) ||
              strong[0] != 97)
            throw StateError('invalid GC controls');
          released.complete();
          final value = await probe.future.timeout(const Duration(seconds: 5));
          if (value != 42)
            throw StateError('unexpected decoded response: $value');
          await collect();
          final after = {
            'input': probe.input.target != null,
            'bytes': probe.bytes.target != null,
          };
          final row = {
            'mode': mode.name,
            'generated': generated,
            'before_response': before,
            'after_completion': after,
            'gc_controls': true,
          };
          rows.add(row);
          print(jsonEncode(row));
        } finally {
          if (!released.isCompleted) released.complete();
          transport.close();
        }
      }
    }
  } finally {
    await listener.cancel();
    await server.close(force: true);
    await service.dispose();
    await developer.Service.controlWebServer(
      enable: false,
      silenceOutput: true,
    );
    final result = jsonEncode({
      'complete': rows.length == 4,
      'calls': calls,
      'cases': rows,
      'owned_server_closed': true,
      'owned_service_closed': true,
    });
    if (args.isNotEmpty) File(args.single).writeAsStringSync(result);
    print('REQUEST_LIFETIME_RESULT $result');
  }
}

// The owned child VM collects deterministically without adding test dependencies.
final class GCService {
  final WebSocket socket;
  final StreamIterator<dynamic> messages;
  var id = 0;
  GCService(this.socket) : messages = StreamIterator<dynamic>(socket);
  static Future<GCService> connect(Uri uri) async =>
      GCService(await WebSocket.connect(uri.toString()));
  Future<void> collect(String isolate) async {
    final request = ++id;
    socket.add(
      jsonEncode({
        'jsonrpc': '2.0',
        'id': request,
        'method': 'getAllocationProfile',
        'params': {'isolateId': isolate, 'gc': true},
      }),
    );
    while (await messages.moveNext()) {
      final response =
          jsonDecode(messages.current as String) as Map<String, dynamic>;
      if (response['id'] != request) continue;
      if (response.containsKey('error'))
        throw StateError('GC failed: ${response['error']}');
      if (response['result']['type'] != 'AllocationProfile')
        throw StateError('invalid GC result');
      return;
    }
    throw StateError('VM service closed before GC');
  }

  Future<void> dispose() async {
    await socket.close();
    await messages.cancel();
  }
}
