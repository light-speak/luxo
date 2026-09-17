import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:luxo_client/luxo_client.dart';
import 'package:test/test.dart';
import 'package:fixture/readme.dart' as docs;

void main() {
  test('README transport, codegen and field tracking examples', verify);
}

Future<void> verify() async {
  final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
  final requests = <String>[];
  final output = <String>[];
  final errors = <Object>[];
  server.listen((request) async {
    try {
      await respond(request, requests);
    } catch (error) {
      errors.add(error);
      stderr.writeln(error);
      request.response.statusCode = 500;
      await request.response.close();
    }
  });
  final endpoint = 'http://${server.address.host}:${server.port}';
  try {
    await runZoned(
      () async {
        await docs.quickStart('$endpoint/quick');
        await docs.http('$endpoint/http');
        await docs.websocket(endpoint.replaceFirst('http', 'ws'));
        await docs.binary('$endpoint/binary');
        await docs.refresh('$endpoint/refresh');
        await docs.selection('$endpoint/selection');
        await docs.codec(endpoint);
        await docs.generated('$endpoint/generated');
        await docs.tracking('$endpoint/tracking');
        await docs.jsonNumbers(endpoint);
      },
      zoneSpecification: ZoneSpecification(
        print: (_, _, _, line) => output.add(line),
      ),
    );
    expect(requests, [
      '/quick:getUser',
      '/quick:listPosts',
      '/http:getUser',
      'subscribe:watchPosts',
      '/binary:getUser',
      '/refresh:expired',
      '/refresh:getUser',
      '/selection:getUser',
      '/generated:getUser',
      '/generated:listPosts',
      '/tracking:getUser',
    ]);
    expect(output, contains('42 hello -100 3.14'));
    expect(output, contains('9223372036854775807'));
    expect(output, contains('Ada'));
    expect(output.any((line) => line.contains('First')), isTrue);
    expect(output, contains('1'));
    expect(output, contains('{"amount":0.12345678901234567890123456789}'));
    expect(output, contains('0.12345678901234568'));
  } finally {
    await server.close(force: true);
    expect(errors, isEmpty);
  }
}

Future<void> respond(HttpRequest request, List<String> requests) async {
  if (WebSocketTransformer.isUpgradeRequest(request)) {
    await respondStream(request, requests);
    return;
  }
  final path = request.uri.path;
  if (path == '/binary') {
    expect(request.headers.value('X-Luxo-Mode'), 'binary');
    expect(request.headers.contentType?.mimeType, 'application/x-luxo');
    await request.drain<void>();
    requests.add('$path:getUser');
    final encoder = LuxoEncoder()
      ..writeVarint(3)
      ..writeFieldString(2, 'Ada')
      ..writeEnd();
    request.response.add(encoder.bytes());
  } else {
    expect(request.headers.value('X-Luxo-Mode'), 'json');
    expect(request.headers.contentType?.mimeType, 'application/json');
    final body = decodeLuxoJSON(await utf8.decoder.bind(request).join()) as Map;
    final auth = request.headers.value(HttpHeaders.authorizationHeader);
    if (path == '/refresh' && auth == 'Bearer expired-token') {
      requests.add('$path:expired');
      request.response.statusCode = 401;
      request.response.write(
        '{"error":"Unauthorized","code":401,"message":"expired"}',
      );
    } else {
      if (path == '/refresh') expect(auth, 'Bearer renewed-token');
      if (path == '/http') expect(auth, 'Bearer jwt-token');
      final api = body[r'$api'];
      requests.add('$path:$api');
      request.response.write(
        encodeLuxoJSON({'data': responseData(path, api, body)}),
      );
    }
  }
  await request.response.close();
}

Future<void> respondStream(HttpRequest request, List<String> requests) async {
  final socket = await WebSocketTransformer.upgrade(request);
  try {
    await for (final message in socket) {
      final body = decodeLuxoJSON(message as String) as Map;
      if (body.containsKey(r'$unsub')) break;
      expect(body[r'$sub'], 'watchPosts');
      expect(body[r'$select'], 'id, title');
      requests.add('subscribe:watchPosts');
      socket.add('{"\$sub":${body[r'$id']},"ok":true}');
      socket.add(
        '{"\$stream":${body[r'$id']},"data":{"id":9223372036854775807,"title":"First"}}',
      );
    }
  } finally {
    await socket.close();
  }
}

Object responseData(String path, Object? api, Map body) {
  final select = body[r'$select'];
  expect(
    select,
    isA<String>().having(
      (value) => value.isNotEmpty,
      'explicit selection',
      isTrue,
    ),
  );
  if (api == 'listPosts') {
    expect(body['page'].toString(), '1');
    expect(body['pageSize'].toString(), '20');
    expect(select, 'title');
    return {
      'items': [
        {'title': 'First'},
      ],
      'total': 1,
      'page': 1,
      'pageSize': 20,
    };
  }
  expect(api, 'getUser');
  expect(body['id'].toString(), path == '/quick' ? '9223372036854775807' : '1');
  if (path == '/selection') {
    expect(select, 'name, email, posts { title }');
    return {
      'name': 'Ada',
      'email': 'ada@example.test',
      'posts': [
        {'title': 'First'},
      ],
    };
  }
  if (path == '/quick') {
    expect(select, 'id, name');
    return {'id': LuxoInt64.maxValue, 'name': 'Ada'};
  }
  expect(select, 'name');
  return {'name': 'Ada'};
}
