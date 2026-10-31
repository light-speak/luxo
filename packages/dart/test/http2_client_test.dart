@TestOn('vm')
library;

import 'dart:async';

import 'package:http/http.dart' as http;
import 'package:luxo_client/http2.dart';
import 'package:test/test.dart';

void main() {
  test('reject invalid limits and non-HTTPS endpoints before connecting', () {
    expect(
      () => LuxoHttp2Client(Uri.parse('http://localhost')),
      throwsArgumentError,
    );
    expect(
      () => LuxoHttp2Client(
        Uri.parse('https://localhost'),
        maxConcurrentRequests: 0,
      ),
      throwsArgumentError,
    );
    expect(
      () => LuxoHttp2Client(Uri.parse('https://localhost'), maxBodyBytes: 0),
      throwsArgumentError,
    );
  });

  test('closed clients and foreign origins fail without dialing', () async {
    final client = LuxoHttp2Client(Uri.parse('https://localhost:1'));
    await expectLater(
      client.get(Uri.parse('https://localhost:2')),
      throwsA(isA<http.ClientException>()),
    );
    client.close();
    client.close();
    await expectLater(
      client.get(Uri.parse('https://localhost:1')),
      throwsA(isA<http.ClientException>()),
    );
  });

  test('cancelled requests do not open a connection', () async {
    final client = LuxoHttp2Client(Uri.parse('https://localhost:1'));
    addTearDown(client.close);
    final abort = Completer<void>()..complete();
    await expectLater(
      client.send(
        http.AbortableRequest(
          'POST',
          Uri.parse('https://localhost:1'),
          abortTrigger: abort.future,
        ),
      ),
      throwsA(isA<http.RequestAbortedException>()),
    );
  });
  test(
    'unbounded request streams are rejected without consuming them',
    () async {
      final client = LuxoHttp2Client(Uri.parse('https://localhost:1'));
      addTearDown(client.close);
      final request = http.StreamedRequest(
        'POST',
        Uri.parse('https://localhost:1'),
      );
      await expectLater(
        client.send(request),
        throwsA(isA<http.ClientException>()),
      );
    },
  );
}
