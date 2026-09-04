import 'dart:convert';
import 'dart:io';

import 'package:luxo_client/src/codegen.dart';
import 'package:test/test.dart';

void main() {
  test('generated client preserves binary list, Bytes, JSON, and field IDs',
      () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final output = Directory('.tmp/codegen_test');
    if (output.existsSync()) output.deleteSync(recursive: true);

    final requestFuture = server.first.then((request) async {
      expect(request.uri.queryParameters.containsKey(r'$schema'), isTrue);
      expect(request.uri.queryParameters.containsKey('key'), isFalse);
      expect(request.headers.value('X-Introspection-Key'), 'test');
      request.response.headers.contentType = ContentType.json;
      request.response.write(jsonEncode(_schema));
      await request.response.close();
    });

    try {
      await generate(
        endpoint: 'http://${server.address.host}:${server.port}/luxo',
        key: 'test',
        outDir: output.path,
      );
      await requestFuture;

      final types = File('${output.path}/types.dart').readAsStringSync();
      final schema = File('${output.path}/schema.dart').readAsStringSync();
      final client = File('${output.path}/client.dart').readAsStringSync();

      expect(types, contains("import 'dart:typed_data';"));
      expect(types, contains('final Selected<Uint8List> data;'));
      expect(types, contains('final Selected<Object> metadata;'));
      expect(types, contains('final Selected<List<Uint8List>> chunks;'));
      expect(types, contains('final Selected<Child> child;'));
      expect(types, contains('class ChildInput'));
      expect(types, contains('final ChildInput child;'));
      expect(types, contains('Child.fromJson'));
      expect(types, contains("if (data.isSelected) 'data': base64Encode(data.value)"));
      expect(types, contains("if (child.isSelected) 'child': child.value.toJson()"));
      expect(types, isNot(contains('Uint8List(0)')));
      expect(types, contains('dec.readBytes()'));
      expect(types, contains('jsonDecode(utf8.decode(dec.readBytes()))'));
      expect(
        schema,
        contains("ParamSchema(1, 'chunks', 'Bytes', true, false)"),
      );
      expect(
        schema,
        contains("'child': SelectionFieldSchema(4, 'Child')"),
      );
      expect(schema,
          contains("luxoSelectionTypes['Payload']!, luxoSelectionTypes"));
      expect(client, contains('required List<Uint8List> chunks'));
      expect(client, contains('required Object metadata'));
      expect(client, contains('required CreateInput input'));
      expect(client, contains('required String? note'));
      expect(
        client,
        contains(
          'LuxoOptional<String> caption = const LuxoOptional<String>.absent()',
        ),
      );
      expect(
          client, contains("if (caption.isPresent) 'caption': caption.value"));
      expect(client, isNot(contains('..nextField()')));
      expect(types,
          contains('List<Payload> decodeColumnarPayload(Uint8List data)'));
      expect(types,
          contains('Page<Payload> decodePaginatedPayload(Uint8List data)'));
      expect(client, contains('decodeColumnarPayload(d)'));
      expect(client, contains('decodePaginatedPayload(d)'));
      expect(client, contains('List<LuxoFilter>? filters'));
      expect(client, contains("if (filters != null) r'\$filters': filters"));
      expect(
        client,
        contains(
          'Future<Page<Payload>> searchPayloads({int? page, int? pageSize, String? select, List<LuxoFilter>? filters, List<LuxoSorter>? sorters})',
        ),
      );
      expect(
        client,
        contains('Future<Payload> createSnapshot({String? select})'),
      );
      expect(client,
          isNot(contains('createSnapshot(Map<String, dynamic> input)')));
      expect(client, contains('Future<Payload> upload({'));
      expect(client, contains('String? select'));
      expect(
        client,
        contains(
          'Future<void Function()> subscribeWatchPayload({required int projectId, String? select, required void Function(Payload) onData})',
        ),
      );
      expect(client, contains("transport.subscribe('watchPayload'"));
      expect(
        client,
        contains(
          'onData(d is Uint8List ? decodePayload(LuxoDecoder(d)) : Payload.fromJson(d as Map<String, dynamic>))',
        ),
      );

      final analysis = await Process.run(
        Platform.resolvedExecutable,
        ['analyze', output.path],
      );
      expect(
        analysis.exitCode,
        0,
        reason: '${analysis.stdout}\n${analysis.stderr}',
      );
    } finally {
      await server.close(force: true);
      if (output.existsSync()) output.deleteSync(recursive: true);
    }
  });
}

const _schema = <String, Object>{
  'models': {
    'Child': {
      'name': 'Child',
      'usage': 'unused',
      'fields': [
        {'id': 1, 'name': 'name', 'type': 'String'},
      ],
    },
    'Payload': {
      'name': 'Payload',
      'usage': 'output',
      'fields': [
        {'id': 1, 'name': 'data', 'type': 'Bytes'},
        {'id': 2, 'name': 'metadata', 'type': 'JSON'},
        {'id': 3, 'name': 'chunks', 'type': 'Bytes', 'isList': true},
        {
          'id': 4,
          'name': 'child',
          'type': 'Model',
          'typeName': 'Child',
          'relation': true,
        },
      ],
    },
    'CreateInput': {
      'name': 'CreateInput',
      'usage': 'input',
      'fields': [
        {'id': 1, 'name': 'child', 'type': 'Model', 'typeName': 'Child'},
      ],
    },
  },
  'apis': {
    'upload': {
      'id': 7,
      'name': 'upload',
      'module': 'file',
      'returnType': 'Payload',
      'params': [
        {'id': 1, 'name': 'chunks', 'type': 'Bytes', 'isList': true},
        {'id': 2, 'name': 'metadata', 'type': 'JSON'},
        {'id': 3, 'name': 'input', 'type': 'JSON', 'typeName': 'CreateInput'},
        {'id': 4, 'name': 'note', 'type': 'String', 'nullable': true},
        {
          'id': 5,
          'name': 'caption',
          'type': 'String',
          'nullable': true,
          'hasDefault': true,
        },
      ],
    },
    'listPayloads': {
      'id': 8,
      'name': 'listPayloads',
      'module': 'file',
      'returnType': 'Payload',
      'returnList': true,
    },
    'listPayloadPage': {
      'id': 9,
      'name': 'listPayloadPage',
      'module': 'file',
      'returnType': 'Payload',
      'returnList': true,
      'paginated': true,
    },
    'watchPayload': {
      'id': 10,
      'name': 'watchPayload',
      'module': 'file',
      'returnType': 'Payload',
      'stream': true,
      'params': [
        {'id': 1, 'name': 'projectId', 'type': 'Int'},
      ],
    },
    'searchPayloads': {
      'id': 11,
      'name': 'searchPayloads',
      'module': 'file',
      'returnType': 'Payload',
      'returnList': true,
      'paginated': true,
    },
    'createSnapshot': {
      'id': 12,
      'name': 'createSnapshot',
      'module': 'file',
      'returnType': 'Payload',
    },
  },
};
