import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:luxo_client/luxo_client.dart' as sdk;

Future<void> acceptModelDepthSocket(
  HttpRequest request,
  List<WebSocket> sockets,
  void Function() received,
) async {
  final socket = await WebSocketTransformer.upgrade(request);
  sockets.add(socket);
  socket.listen((message) {
    received();
    if (message is String) {
      final fields = jsonDecode(message) as Map<String, dynamic>;
      final nodes = fields['control'] as int;
      final json = '${'[' * 128}null${']' * 128}';
      final node =
          '${'{"child":' * (nodes - 1)}{"metadata":$json}${'}' * (nodes - 1)}';
      socket.add('{"\$id":${fields[r'$id']},"data":{"node":$node}}');
      return;
    }
    final decoder = sdk.LuxoDecoder(Uint8List.fromList(message as List<int>));
    if (decoder.readUnsigned() != 1) throw FormatException('expected call');
    final sequence = decoder.readUnsigned();
    if (decoder.readUnsigned() != 1) throw FormatException('expected read API');
    decoder.readBytes();
    var nodes = 1;
    while (decoder.nextField()) {
      switch (decoder.fieldID) {
        case 1:
          decoder.readBytes();
        case 2:
          nodes = decoder.readInt().toInt();
        default:
          throw FormatException('unknown model fixture parameter');
      }
    }
    decoder.checkComplete();
    final header = sdk.LuxoEncoder()
      ..writeVarint(2)
      ..writeVarint(sequence);
    socket.add([...header.bytes(), ...modelDepthResponse(nodes)]);
  });
}

List<int> modelDepthResponse(int nodes) {
  final data = utf8.encode('${'[' * 128}null${']' * 128}');
  var row =
      (sdk.LuxoEncoder()
            ..writeVarint(0)
            ..writeVarint(3)
            ..writePresent()
            ..writeBytes(data)
            ..writeEnd())
          .bytes();
  for (var i = 1; i < nodes; i++) {
    row =
        (sdk.LuxoEncoder()
              ..writeVarint(0)
              ..writeVarint(1)
              ..writePresent()
              ..writeBytes(row)
              ..writeEnd())
            .bytes();
  }
  return (sdk.LuxoEncoder()
        ..writeVarint(0)
        ..writeVarint(1)
        ..writeBytes(row)
        ..writeEnd())
      .bytes();
}

/// Controlled malformed payloads; only the test client's small IDs are accepted.
Future<void> acceptDecodeSocket(
  HttpRequest request,
  List<WebSocket> sockets,
) async {
  final socket = await WebSocketTransformer.upgrade(request);
  sockets.add(socket);
  socket.listen((message) {
    if (message is String) {
      if (request.uri.path.endsWith('/depth')) {
        _jsonDepth(socket, message);
        return;
      }
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

void _jsonDepth(WebSocket socket, String message) {
  if (message.contains(r'"$unsub"')) return;
  final id = RegExp(r'"\$id":(\d+)').firstMatch(message)!.group(1);
  final json128 = '${'[' * 128}null${']' * 128}';
  final model = '{"metadata":$json128}';
  final list =
      message.contains('"listPayloads"') ||
      message.contains('"watchPayloadList"');
  final data = message.contains('"projection"')
      ? '${'{"child":' * 64}{"child":null}${'}' * 64}'
      : message.contains('"scalarInt"')
      ? '7'
      : message.contains('"listPayloadPage"')
      ? '{"items":[$model],"total":1,"page":1,"pageSize":10}'
      : list
      ? '[$model]'
      : model;
  if (message.contains(r'"$sub"')) {
    socket.add('{"\$sub":$id,"ok":true}');
    socket.add('{"data":$data,"\$stream":$id}');
  } else {
    socket.add('{"\$id":9999,"data":$data,"\$id":$id}');
  }
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
