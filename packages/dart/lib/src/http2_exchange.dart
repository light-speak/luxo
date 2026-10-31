part of 'http2_client.dart';

class _H2Exchange {
  final LuxoHttp2Client client;
  final http.BaseRequest request;
  final result = Completer<http.StreamedResponse>();
  final body = BytesBuilder(copy: false);
  final headers = <String, String>{};
  h2.ClientTransportStream? stream;
  StreamSubscription<h2.StreamMessage>? subscription;
  int? status;
  bool finished = false;

  _H2Exchange(this.client, this.request);

  void start() {
    final req = request;
    if (req is http.AbortableRequest && req.abortTrigger != null) {
      unawaited(_watchAbort(req.abortTrigger!));
    }
    unawaited(_send());
  }

  Future<void> _watchAbort(Future<void> abort) async {
    try {
      await abort;
    } catch (_) {
      // An erroneous cancellation signal still terminates this request only.
    }
    fail(http.RequestAbortedException(request.url));
  }

  Future<void> _send() async {
    try {
      final outgoing = BytesBuilder(copy: false);
      await for (final chunk in request.finalize()) {
        if (finished) return;
        if (outgoing.length + chunk.length > client.maxBodyBytes) {
          throw http.ClientException(
            'HTTP/2 request body exceeds limit',
            request.url,
          );
        }
        outgoing.add(chunk);
      }
      if (finished) return;
      final connection = await client._getConnection();
      if (finished) return;
      final limit = connection.peerMaxConcurrentStreams;
      if (limit != null && client._streams >= limit) {
        throw http.ClientException(
          'HTTP/2 peer stream capacity exhausted',
          request.url,
        );
      }
      stream = connection.makeRequest(
        _requestHeaders(),
        endStream: outgoing.isEmpty,
      );
      client._streams++;
      subscription = stream!.incomingMessages.listen(
        _receive,
        onError: (Object error) => fail(error),
        onDone: _complete,
      );
      if (outgoing.isNotEmpty)
        stream!.sendData(outgoing.takeBytes(), endStream: true);
    } catch (error) {
      fail(error);
    }
  }

  List<h2.Header> _requestHeaders() {
    final url = request.url;
    final path = url.path.isEmpty ? '/' : url.path;
    return [
      h2.Header.ascii(':method', request.method),
      h2.Header.ascii(':scheme', 'https'),
      h2.Header.ascii(':authority', url.authority),
      h2.Header.ascii(':path', url.hasQuery ? '$path?${url.query}' : path),
      for (final entry in request.headers.entries)
        if (!const {
          'connection',
          'host',
          'transfer-encoding',
          'keep-alive',
          'upgrade',
          'proxy-connection',
        }.contains(entry.key.toLowerCase()))
          h2.Header.ascii(entry.key.toLowerCase(), entry.value),
    ];
  }

  void _receive(h2.StreamMessage message) {
    if (finished) return;
    try {
      if (message is h2.HeadersStreamMessage) {
        for (final header in message.headers) {
          final name = ascii.decode(header.name);
          final value = latin1.decode(header.value);
          if (name == ':status') {
            final code = int.tryParse(value);
            if (code == null || code < 100 || code > 599 || status != null) {
              throw http.ClientException('Invalid HTTP/2 status', request.url);
            }
            if (code >= 200) status = code;
          } else {
            headers.update(
              name,
              (old) => '$old, $value',
              ifAbsent: () => value,
            );
          }
        }
        // Informational fields do not belong to the final response.
        if (status == null) headers.clear();
      } else if (message is h2.DataStreamMessage) {
        if (status == null ||
            body.length + message.bytes.length > client.maxBodyBytes) {
          throw http.ClientException(
            'Missing status or response body exceeds limit',
            request.url,
          );
        }
        body.add(message.bytes);
      }
    } catch (error) {
      fail(error);
    }
  }

  void _complete() {
    if (finished) return;
    final code = status;
    final length = headers['content-length'];
    final noBody = request.method == 'HEAD' || code == 304;
    if (code == null ||
        (noBody && body.length != 0) ||
        (!noBody && length != null && int.tryParse(length) != body.length)) {
      fail(http.ClientException('Incomplete HTTP/2 response', request.url));
      return;
    }
    finished = true;
    client._release(this);
    final bytes = body.takeBytes();
    result.complete(
      BufferedHTTPResponse(bytes, code, headers: headers, request: request),
    );
  }

  void fail(Object error) {
    if (finished) return;
    finished = true;
    stream?.terminate();
    unawaited(subscription?.cancel());
    body.clear();
    client._release(this);
    result.completeError(
      error is http.ClientException
          ? error
          : http.ClientException('$error', request.url),
    );
  }
}
