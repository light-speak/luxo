// ignore_for_file: deprecated_member_use

import 'dart:async';
import 'dart:html';
import 'dart:typed_data';

class LuxoSocket {
  final WebSocket _socket;

  LuxoSocket(this._socket);

  Stream<dynamic> get messages => _socket.onMessage.map((event) {
        final data = event.data;
        return data is ByteBuffer ? data.asUint8List() : data;
      });

  void add(Object data) {
    if (data is Uint8List) {
      _socket.sendByteBuffer(data.buffer);
      return;
    }
    _socket.sendString(data as String);
  }

  Future<void> close() async => _socket.close();
}

Future<LuxoSocket> connectSocket(String url) async {
  final socket = WebSocket(url)..binaryType = 'arraybuffer';
  final completer = Completer<void>();
  late StreamSubscription<Event> openSub;
  late StreamSubscription<Event> errorSub;
  openSub = socket.onOpen.listen((_) {
    if (!completer.isCompleted) completer.complete();
    openSub.cancel();
    errorSub.cancel();
  });
  errorSub = socket.onError.listen((_) {
    if (!completer.isCompleted)
      completer.completeError(StateError('WebSocket connection failed'));
    openSub.cancel();
    errorSub.cancel();
  });
  await completer.future;
  return LuxoSocket(socket);
}
