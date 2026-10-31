@TestOn('browser || node')
library;

import 'dart:js_interop';
import 'dart:js_interop_unsafe';
import 'dart:typed_data';

import 'package:luxo_client/src/socket.dart' show connectSocket;
import 'package:luxo_client/src/socket_web.dart' show LuxoSocket;
import 'package:test/test.dart';
import 'package:web/web.dart' as web;

void main() {
  test(
    'web socket preserves text and sends only the typed-data view',
    () async {
      final fixture = SocketFixture();
      final socket = LuxoSocket(fixture.socket);
      addTearDown(socket.close);
      socket.add('9223372036854775807');
      expect((fixture.sent as JSString).toDart, '9223372036854775807');
      final bytes = Uint8List.fromList([99, 1, 2, 3, 88]);
      socket.add(Uint8List.sublistView(bytes, 1, 4));
      expect((fixture.sent as JSUint8Array).toDart, [1, 2, 3]);
      expect(fixture.socket.binaryType, 'arraybuffer');
    },
  );

  test(
    'web socket decodes text and ArrayBuffer messages and completes on close',
    () async {
      final fixture = SocketFixture();
      final socket = LuxoSocket(fixture.socket);
      addTearDown(socket.close);
      final done = expectLater(
        socket.messages,
        emitsInOrder([
          '-9223372036854775808',
          [1, 2, 255],
          emitsDone,
        ]),
      );
      fixture.message('-9223372036854775808'.toJS);
      fixture.message(Uint8List.fromList([1, 2, 255]).buffer.toJS);
      fixture.closeFromServer();
      await done;
    },
  );

  test('local close finishes the stream and removes event listeners', () async {
    final fixture = SocketFixture();
    final socket = LuxoSocket(fixture.socket);
    final done = expectLater(socket.messages, emitsDone);
    await socket.close();
    await socket.close();
    fixture.message('late'.toJS);
    await done;
    expect(fixture.closeCalls, 1);
    expect(() => socket.add('late'), throwsStateError);
  });

  test(
    'closing an unconsumed socket does not wait for a stream listener',
    () async {
      final fixture = SocketFixture();
      final socket = LuxoSocket(fixture.socket);
      await socket.close().timeout(const Duration(seconds: 1));
      expect(fixture.closeCalls, 1);
    },
  );

  test(
    'canceling the message subscription closes the browser socket',
    () async {
      final fixture = SocketFixture();
      final socket = LuxoSocket(fixture.socket);
      final subscription = socket.messages.listen((_) {});
      await subscription.cancel();
      expect(fixture.closeCalls, 1);
      await socket.close();
    },
  );

  test(
    'connection failure is reported instead of selecting the platform stub',
    () async {
      await expectLater(
        connectSocket('ws://127.0.0.1:0'),
        throwsA(isA<StateError>()),
      );
    },
  );
  test('unsupported message data produces an explicit stream error', () async {
    final fixture = SocketFixture();
    final socket = LuxoSocket(fixture.socket);
    addTearDown(socket.close);
    final done = expectLater(socket.messages, emitsError(isA<StateError>()));
    fixture.message(42.toJS);
    await done;
  });
}

/// Real JS EventTarget with a deterministic WebSocket-shaped send/close boundary.
class SocketFixture {
  final _target = web.EventTarget();
  JSAny? sent;
  int closeCalls = 0;

  SocketFixture() {
    _target.setProperty(
      'send'.toJS,
      ((JSAny data) {
        sent = data;
      }).toJS,
    );
    _target.setProperty(
      'close'.toJS,
      (() {
        closeCalls++;
      }).toJS,
    );
  }

  web.WebSocket get socket => _target as web.WebSocket;
  void message(JSAny data) => _target.dispatchEvent(
    web.MessageEvent('message', web.MessageEventInit(data: data)),
  );
  void closeFromServer() => _target.dispatchEvent(web.Event('close'));
}
