import 'dart:async';
import 'dart:typed_data';

import 'package:http/http.dart' as http;
import 'package:luxo_client/src/buffered_http_response.dart';
import 'package:luxo_client/src/transport.dart';
import 'package:test/test.dart';

class _BufferedClient extends http.BaseClient {
  final bodies = <Uint8List>[];

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final bytes = Uint8List.fromList([bodies.length + 1, 2, 3]);
    bodies.add(bytes);
    return BufferedHTTPResponse(
      bytes,
      200,
      request: request,
      headers: const {'x-response': 'owned'},
    );
  }
}

class _FailingListen extends Stream<List<int>> {
  final Object failure;
  _FailingListen(this.failure);

  @override
  StreamSubscription<List<int>> listen(
    void Function(List<int>)? onData, {
    Function? onError,
    void Function()? onDone,
    bool? cancelOnError,
  }) => throw failure;
}

void main() {
  test('synchronous stream setup errors remain Future errors', () async {
    final failure = StateError('listen failed');
    late Future<http.Response> result;
    expect(
      () => result = collectHTTPResponse(
        http.StreamedResponse(_FailingListen(failure), 200),
        64,
      ),
      returnsNormally,
    );
    await expectLater(result, throwsA(same(failure)));
  });

  test(
    'response stream errors cancel once and preserve the original error',
    () async {
      var cancelled = 0;
      final source = StreamController<List<int>>(
        onCancel: () {
          cancelled++;
        },
      );
      final failure = StateError('body failed');
      final result = collectHTTPResponse(
        http.StreamedResponse(source.stream, 200),
        64,
      );
      final rejected = expectLater(result, throwsA(same(failure)));
      source.add([1, 2]);
      source.addError(failure);
      await rejected;
      expect(cancelled, 1);
      await source.close();
    },
  );
  test(
    'standard HTTP consumers still receive one single-subscription body',
    () async {
      final bytes = Uint8List.fromList([4, 5, 6]);
      final response = BufferedHTTPResponse(
        bytes,
        201,
        request: http.Request('POST', Uri.parse('https://localhost')),
        headers: const {'x-response': 'owned'},
      );
      expect(identical(response.stream, response.stream), isTrue);
      final collected = await http.Response.fromStream(response);
      expect(collected.bodyBytes, bytes);
      expect(collected.statusCode, 201);
      expect(collected.headers['x-response'], 'owned');
      expect(() => response.stream.listen((_) {}), throwsStateError);
    },
  );

  test(
    'complete owned body reaches Binary caller without another copy',
    () async {
      final client = _BufferedClient();
      final transport = HttpTransport(
        'https://localhost/luvia',
        client: client,
        options: const TransportOptions(mode: TransportMode.binary),
      )..setSchema({'read': APISchemaEntry(1, [])});
      addTearDown(transport.close);
      final first = await transport.call<Uint8List>('read');
      final second = await transport.call<Uint8List>('read');
      transport.close();
      expect(identical(first, client.bodies[0]), isTrue);
      expect(identical(second, client.bodies[1]), isTrue);
      first[0] = 99;
      expect(second, [2, 2, 3]);
    },
  );
}
