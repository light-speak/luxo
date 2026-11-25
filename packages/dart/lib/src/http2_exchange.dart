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

  factory _H2Exchange(LuxoHttp2Client client, http.BaseRequest request) {
    final signal = request is http.AbortableRequest
        ? request.abortTrigger
        : null;
    if (signal != null) {
      return _H2AbortableExchange(
        client,
        request,
        _H2AbortWatch.forSignal(signal),
      );
    }
    return _H2Exchange._(client, request);
  }

  _H2Exchange._(this.client, this.request);

  void start() {
    unawaited(_send());
  }

  void _detachAbort() {}

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
    final previousStatus = status;
    try {
      if (previousStatus == 204) {
        if (message is h2.HeadersStreamMessage ||
            (message is h2.DataStreamMessage && message.bytes.isNotEmpty)) {
          throw http.ClientException(
            'HTTP/2 204 response must not contain content or trailers',
            request.url,
          );
        }
        return;
      }
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
        if (status == null) {
          headers.clear();
        } else if (status == 204 && headers.containsKey('content-length')) {
          throw http.ClientException(
            'HTTP/2 204 response must not have Content-Length',
            request.url,
          );
        }
      } else if (message is h2.DataStreamMessage) {
        if (previousStatus == null ||
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
    _detachAbort();
    client._release(this);
    final bytes = body.takeBytes();
    result.complete(
      BufferedHTTPResponse(bytes, code, headers: headers, request: request),
    );
  }

  void abort() => fail(http.RequestAbortedException(request.url));

  void fail(Object error) {
    if (finished) return;
    finished = true;
    _detachAbort();
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

class _H2AbortableExchange extends _H2Exchange {
  final _H2AbortWatch _watch;

  _H2AbortableExchange(super.client, super.request, this._watch) : super._();

  @override
  void start() {
    _watch.add(this);
    if (!finished) super.start();
  }

  @override
  void _detachAbort() => _watch.remove(this);
}

// A shared abort future can outlive many exchanges. One listener per signal
// owns only the pending exchanges, and a single pending exchange needs no set.
class _H2AbortWatch {
  static final _signals = Expando<_H2AbortWatch>();
  _H2Exchange? _first;
  Set<_H2Exchange>? _others;
  bool _aborted = false;

  static _H2AbortWatch forSignal(Future<void> signal) {
    final previous = _signals[signal];
    if (previous != null) return previous;
    final watch = _H2AbortWatch();
    _signals[signal] = watch;
    unawaited(watch._wait(signal));
    return watch;
  }

  void add(_H2Exchange exchange) {
    if (_aborted) {
      exchange.abort();
    } else if (_first == null) {
      _first = exchange;
    } else {
      (_others ??= {}).add(exchange);
    }
  }

  void remove(_H2Exchange exchange) {
    if (identical(_first, exchange)) {
      _first = null;
    } else {
      _others?.remove(exchange);
      if (_others?.isEmpty ?? false) _others = null;
    }
  }

  Future<void> _wait(Future<void> signal) async {
    try {
      await signal;
    } catch (_) {
      // A signal that completes with an error still cancels its exchanges.
    }
    _aborted = true;
    final first = _first;
    final others = _others;
    _first = null;
    _others = null;
    first?.abort();
    if (others != null) {
      for (final exchange in others) {
        exchange.abort();
      }
    }
  }
}
