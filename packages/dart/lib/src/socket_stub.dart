class LuxoSocket {
  Stream<dynamic> get messages => const Stream.empty();

  void add(Object data) =>
      throw UnsupportedError('WebSocket is not supported on this platform');

  Future<void> close() async {}
}

Future<LuxoSocket> connectSocket(String url) {
  throw UnsupportedError('WebSocket is not supported on this platform');
}
