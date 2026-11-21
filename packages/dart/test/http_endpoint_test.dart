import 'dart:typed_data';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:luxo_client/src/error.dart';
import 'package:luxo_client/src/transport.dart';
import 'package:test/test.dart';

void main() {
  test('HTTP JSON and Binary calls reuse the immutable endpoint URI', () async {
    final urls = <Uri>[];
    final client = MockClient((request) async {
      urls.add(request.url);
      return request.headers['x-luxo-mode'] == 'binary'
          ? http.Response.bytes([1], 200)
          : http.Response('{"data":1}', 200);
    });
    final transport = HttpTransport(
      'https://example.test:8443/api%20v1',
      client: client,
    );
    addTearDown(transport.close);
    await transport.call<Object?>('echo');
    transport.setSchema({'echo': APISchemaEntry(1, [])});
    transport.setMode(TransportMode.binary);
    await transport.call<Uint8List>('echo');
    expect(urls, hasLength(2));
    expect(urls.first.toString(), 'https://example.test:8443/api%20v1');
    expect(identical(urls.first, urls.last), isTrue);
  });

  for (final mode in TransportMode.values) {
    test(
      'invalid endpoint remains a call-time NetworkError in $mode',
      () async {
        final transport = HttpTransport(
          'http://[invalid',
          client: MockClient((_) async => fail('must not send')),
          options: TransportOptions(mode: mode),
        );
        addTearDown(transport.close);
        transport.setSchema({'echo': APISchemaEntry(1, [])});
        await expectLater(
          transport.call<Object?>('echo'),
          throwsA(
            isA<LuxoError>().having(
              (error) => error.error,
              'error',
              'NetworkError',
            ),
          ),
        );
      },
    );
  }
}
