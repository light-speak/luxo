@TestOn('vm')
library;

import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:luxo_client/luxo_client.dart';
import 'package:test/test.dart';

void main() {
  test(
    'oversized unfinished HTTP response cancels without closing siblings',
    () async {
      final server = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
      final received = Completer<void>();
      final disconnected = Completer<void>();
      final sockets = <Socket>[];
      var calls = 0;
      final listener = server.listen((socket) {
        sockets.add(socket);
        var answered = false;
        var oversized = false;
        socket.listen(
          (_) {
            if (answered) return;
            answered = true;
            oversized = calls++ == 0;
            if (oversized) {
              socket.write(
                'HTTP/1.1 200 OK\r\nContent-Type: application/x-luxo\r\nTransfer-Encoding: chunked\r\n\r\n41\r\n',
              );
              socket.add(Uint8List(65));
              socket.write('\r\n');
              received.complete();
            } else {
              socket.write(
                'HTTP/1.1 200 OK\r\nContent-Type: application/x-luxo\r\nContent-Length: 1\r\nConnection: close\r\n\r\n',
              );
              socket.add([42]);
              unawaited(socket.close());
            }
          },
          onDone: () {
            socket.destroy();
            if (oversized && !disconnected.isCompleted) disconnected.complete();
          },
        );
      });
      final transport = HttpTransport(
        'http://127.0.0.1:${server.port}/luvia',
        options: const TransportOptions(
          mode: TransportMode.binary,
          maxResponseBytes: 64,
        ),
        timeout: const Duration(seconds: 3),
      )..setSchema({'echo': APISchemaEntry(1, [])});
      addTearDown(() async {
        transport.close();
        for (final socket in sockets) {
          socket.destroy();
        }
        await listener.cancel();
        await server.close();
      });
      final rejected = expectLater(
        transport.call<Uint8List>('echo'),
        throwsA(
          isA<LuxoError>().having(
            (error) => error.error,
            'error',
            'ParseError',
          ),
        ),
      );
      await received.future.timeout(const Duration(seconds: 2));
      expect(await transport.call<Uint8List>('echo'), [42]);
      await rejected;
      await disconnected.future.timeout(const Duration(seconds: 2));
      expect(await transport.call<Uint8List>('echo'), [42]);
    },
  );
  for (final mode in TransportMode.values) {
    test('native $mode timeout releases the real socket', () async {
      final server = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
      final disconnected = Completer<void>();
      final received = Completer<void>();
      final sockets = <Socket>[];
      final listener = server.listen((socket) {
        sockets.add(socket);
        socket.listen(
          (_) {
            if (!received.isCompleted) received.complete();
          },
          onDone: () {
            socket.destroy();
            if (!disconnected.isCompleted) disconnected.complete();
          },
        );
      });
      final transport = HttpTransport(
        'http://127.0.0.1:${server.port}/luvia',
        options: TransportOptions(mode: mode),
        timeout: const Duration(milliseconds: 250),
      )..setSchema({'echo': APISchemaEntry(1, [])});
      addTearDown(() async {
        transport.close();
        for (final socket in sockets) {
          socket.destroy();
        }
        await listener.cancel();
        await server.close();
      });
      final ended = expectLater(
        transport.call<Object?>('echo'),
        throwsA(
          isA<LuxoError>().having(
            (error) => error.error,
            'error',
            'TimeoutError',
          ),
        ),
      );
      await received.future.timeout(const Duration(seconds: 2));
      await ended;
      // Verify cancellation before close() can release the connection.
      await disconnected.future.timeout(const Duration(seconds: 2));
    });
  }
}
