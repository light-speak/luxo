part of 'http2_client.dart';

// HTTP/2 headers and DATA can be separate writes; avoid waiting for delayed ACKs.
// This internal helper is not exported from the public HTTP/2 entry point.
void configureHttp2Socket(RawSocket socket) {
  if (!socket.setOption(SocketOption.tcpNoDelay, true)) {
    throw const SocketException('Unable to configure HTTP/2 TCP_NODELAY');
  }
}

// Retaining the raw TCP socket lets deadlines interrupt TLS handshakes.
// This adapter only bridges I/O; HTTP/2 framing stays in package:http2.
class _H2Socket implements StreamConsumer<List<int>> {
  final RawSecureSocket socket;
  late final StreamController<List<int>> _input;
  late final IOSink outgoing;
  Completer<void>? _writable;
  Object? _failure;
  bool _ended = false;
  final closed = Completer<void>();

  _H2Socket(this.socket) {
    _input = StreamController<List<int>>(
      sync: true,
      onPause: () => socket.readEventsEnabled = false,
      onResume: () => socket.readEventsEnabled = true,
      onCancel: close,
    );
    outgoing = IOSink(this);
    // An I/O error is also delivered through the incoming stream.
    unawaited(outgoing.done.catchError((Object _) {}));
    socket.listen(_event, onError: _error, onDone: _end);
  }

  Stream<List<int>> get incoming => _input.stream;

  void _event(RawSocketEvent event) {
    if (_ended) return;
    if (event == RawSocketEvent.read) {
      final bytes = socket.read();
      if (bytes != null) _input.add(bytes);
    } else if (event == RawSocketEvent.write) {
      socket.writeEventsEnabled = false;
      final writable = _writable;
      _writable = null;
      writable?.complete();
    } else if (event == RawSocketEvent.readClosed ||
        event == RawSocketEvent.closed) {
      _end();
    }
  }

  void _error(Object error, StackTrace stack) {
    if (_ended) return;
    _failure = error;
    _input.addError(error, stack);
    _end();
  }

  void _end() {
    if (_ended) return;
    _ended = true;
    closed.complete();
    _failure ??= const SocketException('HTTP/2 socket closed');
    final writable = _writable;
    _writable = null;
    writable?.complete();
    unawaited(_input.close());
    unawaited(socket.close());
  }

  @override
  Future<void> addStream(Stream<List<int>> stream) async {
    await for (final bytes in stream) {
      var offset = 0;
      while (offset < bytes.length) {
        if (_ended) throw _failure!;
        final written = socket.write(bytes, offset);
        offset += written;
        if (written == 0) {
          final ready = Completer<void>();
          _writable = ready;
          socket.writeEventsEnabled = true;
          await ready.future;
        }
      }
    }
  }

  @override
  Future<void> close() async {
    _end();
  }
}
