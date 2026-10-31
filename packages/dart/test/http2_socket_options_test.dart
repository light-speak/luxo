@TestOn('vm')
library;

import 'dart:io';
import 'dart:typed_data';

import 'package:luxo_client/src/http2_client.dart' show configureHttp2Socket;
import 'package:test/test.dart';

void main() {
  test('HTTP/2 TCP setup disables Nagle before TLS framing', () async {
    final server = await RawServerSocket.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(server.close);
    final peer = server.first;
    final socket = await RawSocket.connect(server.address, server.port);
    final accepted = await peer;
    addTearDown(socket.close);
    addTearDown(accepted.close);
    // IPPROTO_TCP and TCP_NODELAY have these values on supported native targets.
    int noDelay() => ByteData.sublistView(
      socket.getRawOption(RawSocketOption.fromInt(6, 1, 0)),
    ).getInt32(0, Endian.host);
    expect(socket.setOption(SocketOption.tcpNoDelay, false), isTrue);
    expect(noDelay(), 0);
    configureHttp2Socket(socket);
    expect(noDelay(), isNot(0));
    await socket.close();
    expect(() => configureHttp2Socket(socket), throwsA(isA<SocketException>()));
  });
}
