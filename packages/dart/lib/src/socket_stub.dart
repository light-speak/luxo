class LuxoSocket {
  int? get closeCode => null;
  Stream<dynamic> get messages => const Stream.empty();

  void add(Object data) =>
      throw UnsupportedError('WebSocket is not supported on this platform');

  Future<void> close() async {}
}

Future<LuxoSocket> connectSocket(
  String url, {
  Duration timeout = const Duration(seconds: 30),
}) {
  throw UnsupportedError('WebSocket is not supported on this platform');
}
