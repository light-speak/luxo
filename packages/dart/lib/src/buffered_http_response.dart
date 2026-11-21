import 'dart:async';
import 'dart:typed_data';

import 'package:http/http.dart' as http;

import 'error.dart';

void checkHTTPResponseSize(int size, int maximum, int status) {
  if (size > maximum) {
    throw LuxoError('ParseError', status, 'HTTP response body exceeds limit');
  }
}

Future<http.Response> collectHTTPResponse(
  http.StreamedResponse response,
  int maximum,
) async {
  final encoding = response.headers['content-encoding'];
  if ((encoding == null || encoding == 'identity') &&
      (response.contentLength ?? 0) > maximum) {
    final subscription = response.stream.listen(null, onError: (Object _) {});
    unawaited(subscription.cancel().catchError((Object _) {}));
    checkHTTPResponseSize(
      response.contentLength!,
      maximum,
      response.statusCode,
    );
  }
  return _collectHTTPResponseStream(response, maximum);
}

Future<http.Response> _collectHTTPResponseStream(
  http.StreamedResponse response,
  int maximum,
) {
  final body = BytesBuilder(copy: false);
  final completed = Completer<http.Response>();
  StreamSubscription<List<int>>? subscription;
  var finished = false;
  void cancel() {
    final current = subscription;
    if (current != null) unawaited(current.cancel().catchError((Object _) {}));
  }

  void fail(Object error, StackTrace stack) {
    if (finished) return;
    finished = true;
    completed.completeError(error, stack);
    cancel();
  }

  subscription = response.stream.listen(
    (chunk) {
      if (finished) return;
      try {
        checkHTTPResponseSize(
          body.length + chunk.length,
          maximum,
          response.statusCode,
        );
        body.add(chunk);
      } catch (error, stack) {
        fail(error, stack);
      }
    },
    onError: fail,
    onDone: () {
      if (finished) return;
      try {
        final value = http.Response.bytes(
          body.takeBytes(),
          response.statusCode,
          headers: response.headers,
          request: response.request,
          isRedirect: response.isRedirect,
          persistentConnection: response.persistentConnection,
          reasonPhrase: response.reasonPhrase,
        );
        finished = true;
        completed.complete(value);
      } catch (error, stack) {
        fail(error, stack);
      }
    },
  );
  if (finished) cancel();
  return completed.future;
}

/// Internal handoff of a complete, independently owned response body.
/// The producer must never mutate or reuse [bodyBytes] after construction.
final class BufferedHTTPResponse extends http.StreamedResponse {
  final Uint8List bodyBytes;

  // Standard http consumers need a stream; the owned-body path does not.
  @override
  late final http.ByteStream stream = http.ByteStream.fromBytes(bodyBytes);

  BufferedHTTPResponse(
    this.bodyBytes,
    int statusCode, {
    required super.request,
    required super.headers,
  }) : super(
         const http.ByteStream(Stream.empty()),
         statusCode,
         contentLength: bodyBytes.length,
       );

  http.Response toResponse() => http.Response.bytes(
    bodyBytes,
    statusCode,
    request: request,
    headers: headers,
    isRedirect: isRedirect,
    persistentConnection: persistentConnection,
    reasonPhrase: reasonPhrase,
  );
}
