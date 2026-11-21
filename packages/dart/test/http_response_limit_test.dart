import 'dart:async';
import 'dart:typed_data';

import 'package:http/http.dart' as http;
import 'package:luxo_client/luxo_client.dart';
import 'package:test/test.dart';

class _ResponseClient extends http.BaseClient {
  final Future<http.StreamedResponse> Function(http.BaseRequest) respond;
  _ResponseClient(this.respond);
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) =>
      respond(request);
}

void main() {
  for (final limit in [0, -1, 0x80000000]) {
    test('rejects invalid HTTP response limit $limit', () {
      expect(
        () => HttpTransport(
          'https://example.test',
          options: TransportOptions(maxResponseBytes: limit),
        ),
        throwsArgumentError,
      );
    });
  }
  for (final declared in [false, true]) {
    test(
      'rejects before EOF, cancels only oversized response; declared=$declared',
      () async {
        final aborted = Completer<void>();
        final cancelled = Completer<void>();
        final stream = StreamController<List<int>>(
          onCancel: () => cancelled.complete(),
        );
        var calls = 0;
        final client = _ResponseClient((request) async {
          if (calls++ != 0)
            return http.StreamedResponse(Stream.value([42]), 200);
          unawaited(
            (request as http.AbortableRequest).abortTrigger!.then(
              (_) => aborted.complete(),
            ),
          );
          if (!declared) stream.add(Uint8List(65));
          return http.StreamedResponse(
            stream.stream,
            200,
            contentLength: declared ? 65 : null,
          );
        });
        final transport = HttpTransport(
          'https://example.test',
          client: client,
          options: const TransportOptions(
            mode: TransportMode.binary,
            maxResponseBytes: 64,
          ),
        )..setSchema({'read': APISchemaEntry(1, [])});
        addTearDown(transport.close);
        await expectLater(
          transport.call<Uint8List>('read'),
          throwsA(
            isA<LuxoError>().having(
              (error) => error.error,
              'error',
              'ParseError',
            ),
          ),
        );
        await aborted.future;
        await cancelled.future;
        expect(await transport.call<Uint8List>('read'), [42]);
        await stream.close();
      },
    );
  }
  test(
    'accepts exact limit, counts chunks and ignores understated length',
    () async {
      var calls = 0;
      final client = _ResponseClient(
        (_) async => http.StreamedResponse(
          Stream.fromIterable(
            calls++ == 0
                ? [
                    [1, 2],
                    [3, 4],
                  ]
                : [
                    [1, 2],
                    [3, 4, 5],
                  ],
          ),
          200,
          contentLength: 1,
        ),
      );
      final transport = HttpTransport(
        'https://example.test',
        client: client,
        options: const TransportOptions(
          mode: TransportMode.binary,
          maxResponseBytes: 4,
        ),
      )..setSchema({'read': APISchemaEntry(1, [])});
      addTearDown(transport.close);
      expect(await transport.call<Uint8List>('read'), [1, 2, 3, 4]);
      await expectLater(
        transport.call<Uint8List>('read'),
        throwsA(
          isA<LuxoError>().having(
            (error) => error.error,
            'error',
            'ParseError',
          ),
        ),
      );
    },
  );
}
