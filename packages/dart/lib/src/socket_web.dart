import 'dart:async';
import 'dart:js_interop';
import 'dart:typed_data';

import 'package:web/web.dart' as web;

class LuxoSocket {
  final web.WebSocket _socket;
  final _messages = StreamController<Object>();
  late final StreamSubscription<web.MessageEvent> _messageSubscription;
  late final StreamSubscription<web.CloseEvent> _closeSubscription;
  bool _closed = false;

  LuxoSocket(this._socket) {
    _socket.binaryType = 'arraybuffer';
    _messageSubscription = _socket.onMessage.listen(_receive);
    _closeSubscription = _socket.onClose.listen((_) {
      unawaited(_finish(closeSocket: false));
    });
    _messages.onCancel = close;
  }

  Stream<Object> get messages => _messages.stream;

  void _receive(web.MessageEvent event) {
    final data = event.data;
    if (data.isA<JSString>()) {
      _messages.add((data as JSString).toDart);
    } else if (data.isA<JSArrayBuffer>()) {
      _messages.add((data as JSArrayBuffer).toDart.asUint8List());
    } else {
      _messages.addError(StateError('unsupported WebSocket message type'));
    }
  }

  void add(Object data) {
    if (_closed) throw StateError('WebSocket is closed');
    if (data is Uint8List) {
      // Preserve the view offset and length; never send its entire backing buffer.
      _socket.send(data.toJS);
      return;
    }
    _socket.send((data as String).toJS);
  }

  Future<void> close() => _finish(closeSocket: true);

  Future<void> _finish({required bool closeSocket}) async {
    if (_closed) return;
    _closed = true;
    if (closeSocket) _socket.close();
    await _messageSubscription.cancel();
    await _closeSubscription.cancel();
    // Stream completion must not wait forever when nobody has listened yet.
    unawaited(_messages.close());
  }
}

Future<LuxoSocket> connectSocket(String url) async {
  final socket = web.WebSocket(url);
  final completer = Completer<void>();
  final openSub = socket.onOpen.listen((_) {
    if (!completer.isCompleted) completer.complete();
  });
  void fail(web.Event _) {
    if (completer.isCompleted) return;
    completer.completeError(StateError('WebSocket connection failed'));
  }

  final errorSub = socket.onError.listen(fail);
  final closeSub = socket.onClose.listen(fail);
  try {
    await completer.future;
    return LuxoSocket(socket);
  } catch (_) {
    socket.close();
    rethrow;
  } finally {
    await openSub.cancel();
    await errorSub.cancel();
    await closeSub.cancel();
  }
}
