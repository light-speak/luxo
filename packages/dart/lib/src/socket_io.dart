import 'dart:io';

class LuxoSocket {
  final WebSocket _socket;

  LuxoSocket(this._socket);

  Stream<dynamic> get messages => _socket;

  void add(Object data) => _socket.add(data);

  Future<void> close() => _socket.close();
}

Future<LuxoSocket> connectSocket(String url) async {
  return LuxoSocket(await WebSocket.connect(url));
}
