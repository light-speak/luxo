import 'dart:async';
import 'dart:io';

import 'deadline.dart';

class LuxoSocket {
  final WebSocket _socket;

  LuxoSocket(this._socket);

  Stream<dynamic> get messages => _socket;

  int? get closeCode => _socket.closeCode;

  void add(Object data) => _socket.add(data);

  Future<void> close() => _socket.close();
}

Future<LuxoSocket> connectSocket(
  String url, {
  Duration timeout = const Duration(seconds: 30),
}) async {
  final client = HttpClient();
  var opening = true;
  try {
    final connection = WebSocket.connect(url, customClient: client).then((
      socket,
    ) {
      // A detached socket can complete after the handshake deadline wins.
      if (!opening) {
        unawaited(socket.close());
        throw StateError('WebSocket connection expired');
      }
      return LuxoSocket(socket);
    });
    return await withDeadline(connection, timeout);
  } finally {
    opening = false;
    // Upgraded sockets are detached; failed TCP/TLS/HTTP handshakes remain owned.
    client.close(force: true);
  }
}
