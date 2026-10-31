import 'dart:io';

/// Controlled malformed payloads; only the test client's small IDs are accepted.
Future<void> acceptDecodeSocket(
  HttpRequest request,
  List<WebSocket> sockets,
) async {
  final socket = await WebSocketTransformer.upgrade(request);
  sockets.add(socket);
  socket.listen((message) {
    if (message is String) {
      _json(
        socket,
        message,
        request.uri.path.endsWith('/business'),
        request.uri.path.endsWith('/projection'),
      );
    } else {
      _binary(
        socket,
        message as List<int>,
        request.uri.path.endsWith('/business'),
        request.uri.path.endsWith('/projection'),
      );
    }
  });
}

void _json(WebSocket socket, String message, bool business, bool projection) {
  // This fixture only needs the exact small envelope ID and control kind.
  final id = RegExp(r'"\$id":(\d+)').firstMatch(message)?.group(1);
  if (message.contains(r'"$unsub"')) return;
  if (id == null) throw StateError('missing fixture request ID');
  if (!message.contains(r'"$sub"')) {
    socket.add('{"\$id":$id,"data":7}');
    return;
  }
  socket.add('{"\$sub":$id,"ok":true}');
  if (id == '1' && !business) {
    final invalid = projection ? '[{}]' : 'true';
    socket.add('{"\$stream":$id,"data":$invalid}');
    socket.add('{"\$stream":$id,"data":$invalid}');
    socket.add('{"\$end":$id,"ok":true}');
  } else {
    socket.add('{"\$stream":$id,"data":7}');
    if (business) {
      socket.add('{"\$stream":$id,"data":7}');
      socket.add('{"\$end":$id,"ok":true}');
    }
  }
}

void _binary(
  WebSocket socket,
  List<int> message,
  bool business,
  bool projection,
) {
  if (message.length < 2 || message[1] >= 128)
    throw StateError('invalid fixture header');
  final tag = message[0], id = message[1];
  if (tag == 0x11) return;
  if (tag == 1) {
    socket.add([2, id, 14]);
    return;
  }
  if (tag != 0x10) throw StateError('unexpected fixture frame');
  socket.add([0x13, id]);
  if (id == 1 && !business) {
    final invalid = projection ? [1, 0, 0] : [128];
    socket.add([0x12, id, ...invalid]);
    socket.add([0x12, id, ...invalid]);
    socket.add([0x15, id]);
  } else {
    socket.add([0x12, id, 14]);
    if (business) {
      socket.add([0x12, id, 14]);
      socket.add([0x15, id]);
    }
  }
}
