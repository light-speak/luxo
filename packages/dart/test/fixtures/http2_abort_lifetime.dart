import 'dart:async';
import 'dart:convert';
import 'dart:developer' as developer;
import 'dart:io';
import 'dart:isolate';

import 'package:http/http.dart' as http;
import 'package:luxo_client/http2.dart';

import 'request_lifetime.dart' show GCService;

@pragma('vm:never-inline')
Future<WeakReference<http.Request>> sendProbe(
  LuxoHttp2Client client,
  Future<void>? abort, {
  bool oversized = false,
}) async {
  final url = oversized ? client.endpoint.resolve('/large') : client.endpoint;
  final request = abort == null
      ? http.Request('POST', url)
      : http.AbortableRequest('POST', url, abortTrigger: abort);
  request.bodyBytes = [17, 23];
  try {
    final response = await client.send(request);
    final returned = await response.stream.toBytes();
    if (oversized ||
        response.statusCode != 200 ||
        returned.join(',') != '17,23') {
      throw StateError('unexpected response from the HTTP/2 fixture');
    }
  } on http.ClientException catch (error) {
    if (!oversized || !error.message.contains('body exceeds limit')) rethrow;
  }
  return WeakReference(request);
}

Future<void> main(List<String> args) async {
  final service = await GCService.connect(
    (await developer.Service.getInfo()).serverWebSocketUri!,
  );
  final isolate = developer.Service.getIsolateId(Isolate.current)!;
  final client = LuxoHttp2Client(
    Uri.parse(args[0]),
    maxBodyBytes: 1024,
    securityContext: SecurityContext(withTrustedRoots: false)
      ..setTrustedCertificatesBytes(utf8.encode(args[1])),
  );
  final abort = Completer<void>();
  try {
    final probes = [
      await sendProbe(client, null),
      await sendProbe(client, abort.future),
      await sendProbe(client, abort.future, oversized: true),
    ];
    for (var attempt = 0; attempt < 8; attempt++) {
      await service.collect(isolate);
      await Future<void>.delayed(Duration.zero);
      if (probes.every((probe) => probe.target == null)) break;
    }
    final retained = probes.map((probe) => probe.target != null).toList();
    if (abort.isCompleted || retained.any((value) => value)) {
      throw StateError(
        'retained requests [control, success, error]: $retained',
      );
    }
  } finally {
    abort.complete();
    client.close();
    await service.dispose();
    await developer.Service.controlWebServer(
      enable: false,
      silenceOutput: true,
    );
  }
}
