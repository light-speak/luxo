import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:luxo_client/src/codegen.dart';
import 'package:test/test.dart';

import 'support/stream_decode_server.dart';

void main() {
  test(
    'generated client preserves binary list, Bytes, JSON, and field IDs',
    () async {
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      final output = Directory('.tmp/codegen_test');
      if (output.existsSync()) output.deleteSync(recursive: true);

      final sockets = <WebSocket>[];
      final schemaServed = Completer<void>();
      server.listen((request) async {
        if (WebSocketTransformer.isUpgradeRequest(request)) {
          await acceptDecodeSocket(request, sockets);
          return;
        }
        expect(request.uri.queryParameters.containsKey(r'$schema'), isTrue);
        expect(request.uri.queryParameters.containsKey('key'), isFalse);
        expect(request.headers.value('X-Introspection-Key'), 'test');
        request.response.headers.contentType = ContentType.json;
        request.response.write(jsonEncode(_schema));
        await request.response.close();
        schemaServed.complete();
      });

      try {
        await generate(
          endpoint: 'http://${server.address.host}:${server.port}/luxo',
          key: 'test',
          outDir: output.path,
        );
        await schemaServed.future;
        if (Platform.environment['LUXO_JSON_PROJECTION_BASELINE'] == '1') {
          for (final name in ['types.dart', 'client.dart']) {
            File('.tmp/dart-json-projection-before/$name')
                .copySync('${output.path}/$name');
          }
        }

        final types = File('${output.path}/types.dart').readAsStringSync();
        final schema = File('${output.path}/schema.dart').readAsStringSync();
        final client = File('${output.path}/client.dart').readAsStringSync();
        if (Platform.environment['LUXO_CAPTURE_JSON_PROJECTION'] == '1') {
          _captureProjectionBaseline(output);
        }

        expect(client, contains('echoInt({required LuxoInt64 value})'));
        expect(client, contains('echoDuration({required LuxoInt64 value})'));
        expect(client, contains('echoEnum({required String value})'));

        expect(types, contains("import 'dart:typed_data';"));
        expect(types, contains('final Selected<Uint8List> data;'));
        expect(types, contains('final Selected<Object> metadata;'));
        expect(types, contains('final Selected<List<Object?>> jsons;'));
        expect(client, contains('Future<List<Object?>> arrayJSON()'));
        expect(types, contains('final Selected<List<Uint8List>> chunks;'));
        expect(types, contains('dec.readBool() ? dec.readIntArray() : null'));
        expect(types, contains('List<Uint8List?>? _nullableIntegers'));
        expect(types, contains('_nullableIntegers = dec.readColumnBytesPtr()'));
        expect(
          types,
          contains(
            '_nullableIntegers[i] == null ? null : LuxoDecoder(_nullableIntegers[i]!).readIntArrayChecked()',
          ),
        );
        expect(types, contains('final Selected<Child> child;'));
        expect(types, contains('class ChildInput'));
        expect(types, contains('final ChildInput child;'));
        expect(types, contains('Child.fromJson'));
        expect(
          types,
          contains("if (data.isSelected) 'data': base64Encode(data.value)"),
        );
        expect(
          types,
          contains("if (child.isSelected) 'child': child.value.toJson()"),
        );
        expect(types, isNot(contains('Uint8List(0)')));
        expect(types, contains('dec.readBytes()'));
        expect(types, contains('decodeLuxoJSON(utf8.decode(dec.readBytes()))'));
        expect(
          schema,
          contains("ParamSchema(1, 'chunks', 'Bytes', true, false)"),
        );
        expect(
          schema,
          contains(
            "'child': SelectionFieldSchema(4, 'Child', 'Model', false, false)",
          ),
        );
        expect(
          schema,
          contains(
            "ParamSchema(3, 'input', 'Model', false, false, 'CreateInput')",
          ),
        );
        expect(
          schema,
          contains(
            "'name': SelectionFieldSchema(1, null, 'String', false, false)",
          ),
        );
        expect(
          schema,
          contains("luxoSelectionTypes['Payload']!, luxoSelectionTypes"),
        );
        expect(client, contains('required List<Uint8List> chunks'));
        expect(client, contains('required Object metadata'));
        expect(client, contains('required CreateInput input'));
        expect(
          types,
          contains('class CreateInput implements LuxoBinaryEncodable'),
        );
        expect(types, contains('void writeLuxo(LuxoEncoder encoder)'));
        expect(types, contains('child.writeLuxo(encoder)'));
        expect(client, contains('required String? note'));
        expect(
          client,
          contains(
            'LuxoOptional<String> caption = const LuxoOptional<String>.absent()',
          ),
        );
        expect(
          client,
          contains("if (caption.isPresent) 'caption': caption.value"),
        );
        expect(client, isNot(contains('..nextField()')));
        expect(
          types,
          contains('List<Payload> decodeColumnarPayload(Uint8List data)'),
        );
        expect(
          types,
          contains('Page<Payload> decodePaginatedPayload(Uint8List data)'),
        );
        expect(client, contains('decodeColumnarPayload(d)'));
        expect(client, contains('decodePaginatedPayload(d)'));
        expect(client, contains('List<LuxoFilter>? filters'));
        expect(client, contains("if (filters != null) r'\$filters': filters"));
        expect(
          client,
          contains(
            'Future<Page<Payload>> searchPayloads({LuxoInt64? page, LuxoInt64? pageSize, String? select, List<LuxoFilter>? filters, List<LuxoSorter>? sorters})',
          ),
        );
        expect(
          client,
          contains('Future<Payload> createSnapshot({String? select})'),
        );
        expect(
          client,
          isNot(contains('createSnapshot(Map<String, dynamic> input)')),
        );
        expect(client, contains('Future<Payload> upload({'));
        expect(client, contains('String? select'));
        expect(
          client,
          contains(
            'Future<void Function()> subscribeWatchPayload({required LuxoInt64 projectId, String? select, required void Function(Payload) onData, void Function(LuxoError)? onError, void Function()? onDone})',
          ),
        );
        expect(client, contains("transport.subscribe('watchPayload'"));
        expect(client, contains('}, onData, decoder:'));
        expect(client, isNot(contains('(d) => onData(')));
        expect(client, contains('onError: onError, onDone: onDone'));
        expect(
          client,
          contains(
            'decoder: (d) => d is Uint8List ? decodePayload(LuxoDecoder(d)) : Payload.fromJson(d as Map<String, dynamic>)',
          ),
        );

        File('${output.path}/int64_consumer_test.dart').writeAsStringSync(
          File('test/fixtures/int64_consumer.dart.txt').readAsStringSync(),
        );
        File('${output.path}/response_consumer.dart').writeAsStringSync(
          File('test/fixtures/response_consumer.dart.txt').readAsStringSync(),
        );
        File('${output.path}/json_kind_consumer.dart').writeAsStringSync(
          File('test/fixtures/json_kind_consumer.dart.txt').readAsStringSync(),
        );
        File('${output.path}/json_projection_consumer.dart').writeAsStringSync(
          File('test/fixtures/json_projection_consumer.dart.txt')
              .readAsStringSync(),
        );
        File('${output.path}/stream_isolation_consumer.dart').writeAsStringSync(
          File('test/fixtures/stream_isolation_consumer.dart.txt')
              .readAsStringSync()
              .replaceAll(
                '__STREAM_ENDPOINT__',
                'ws://${server.address.host}:${server.port}',
              ),
        );
        final analysis = await _runDart([
          'analyze',
          '--fatal-infos',
          output.path,
        ]);
        expect(
          analysis.exitCode,
          0,
          reason: '${analysis.stdout}\n${analysis.stderr}',
        );
        final consumer = await _runDart([
          'test',
          '-p',
          'vm,node',
          '-c',
          'kernel',
          '-c',
          'dart2js',
          '-c',
          'dart2wasm',
          '-j',
          '1',
          '${output.path}/int64_consumer_test.dart',
          '--reporter',
          'expanded',
        ]);
        expect(
          consumer.exitCode,
          0,
          reason: '${consumer.stdout}\n${consumer.stderr}',
        );
        if (Platform.environment['LUXO_BENCH_JSON_PROJECTION'] == '1') {
          await _runProjectionBenchmarks(output);
        }
      } finally {
        for (final socket in sockets) await socket.close();
        await server.close(force: true);
        if (output.existsSync()) output.deleteSync(recursive: true);
      }
    },
    timeout: Timeout(
      Duration(
        minutes: Platform.environment['LUXO_BENCH_JSON_PROJECTION'] == '1'
            ? 10
            : 2,
      ),
    ),
  );
}

(String, String) _prepareProjectionBenchmark(Directory output) {
  for (final name in ['types', 'client']) {
    final source = File('.tmp/dart-json-projection-before/$name.dart')
        .readAsStringSync();
    if (source.contains(r'$fromJsonItem'))
      throw StateError('Expected the pre-projection snapshot');
    File('${output.path}/${name}_before.dart').writeAsStringSync(
      source.replaceAll("'types.dart'", "'types_before.dart'"),
    );
  }
  final script = '${output.path}/projection_benchmark.dart';
  File(script).writeAsStringSync(
    File('benchmark/json_projection_benchmark.dart.txt').readAsStringSync(),
  );
  final test = '${output.path}/projection_benchmark_test.dart';
  File(test).writeAsStringSync(
    "import 'package:test/test.dart';\nimport 'projection_benchmark.dart' as bench;\nvoid main() { test('projection benchmark', bench.main); }\n",
  );
  return (script, test);
}

Future<void> _runProjectionBenchmarks(Directory output) async {
  final (script, test) = _prepareProjectionBenchmark(output);
  final analysis = await _runDart(['analyze', '--fatal-infos', output.path]);
  expect(
    analysis.exitCode,
    0,
    reason: '${analysis.stdout}\n${analysis.stderr}',
  );
  final compile = await _runDart([
    'compile',
    'exe',
    script,
    '-o',
    '${output.path}/projection-bench',
  ]);
  expect(compile.exitCode, 0, reason: '${compile.stdout}\n${compile.stderr}');
  for (final platform in ['aot', 'dart2js', 'dart2wasm']) {
    for (var round = 1; round <= 3; round++) {
      final result = platform == 'aot'
          ? await _runProcess('${output.path}/projection-bench', [])
          : await _runDart([
              'test',
              '-p',
              'node',
              '-c',
              platform,
              test,
              '--reporter',
              'expanded',
            ]);
      expect(result.exitCode, 0, reason: '${result.stdout}\n${result.stderr}');
      print('projection benchmark $platform round $round\n${result.stdout}');
    }
  }
}

void _captureProjectionBaseline(Directory output) {
  final baseline = Directory('.tmp/dart-json-projection-before')
    ..createSync(recursive: true);
  for (final name in ['types.dart', 'client.dart']) {
    final source = File('${output.path}/$name').readAsStringSync();
    final target = File('${baseline.path}/$name');
    if (target.existsSync() && target.readAsStringSync() != source) {
      throw StateError(
        'Projection baseline already exists with different content',
      );
    }
    target.writeAsStringSync(source);
  }
}

Future<ProcessResult> _runDart(List<String> arguments) async {
  return _runProcess(Platform.resolvedExecutable, arguments);
}

Future<ProcessResult> _runProcess(
  String executable,
  List<String> arguments,
) async {
  final process = await Process.start(executable, arguments);
  final stdout = process.stdout.transform(utf8.decoder).join();
  final stderr = process.stderr.transform(utf8.decoder).join();
  final timer = Timer(
    const Duration(seconds: 60),
    () => process.kill(ProcessSignal.sigkill),
  );
  try {
    return ProcessResult(
      process.pid,
      await process.exitCode,
      await stdout,
      await stderr,
    );
  } finally {
    timer.cancel();
  }
}

const _scalarTypes = [
  'Int',
  'Duration',
  'Float',
  'String',
  'Boolean',
  'DateTime',
  'UUID',
  'Bytes',
  'Decimal',
  'Enum',
  'JSON',
];

final _schema = <String, Object>{
  'types': {
    'Projection': {
      'name': 'Projection',
      'usage': 'output',
      'fields': [
        {'id': 1, 'name': 'label', 'type': 'String', 'nullable': true},
        {
          'id': 2,
          'name': 'child',
          'type': 'Model',
          'typeName': 'Projection',
          'nullable': true,
        },
        {
          'id': 3,
          'name': 'children',
          'type': 'Model',
          'typeName': 'Projection',
          'isList': true,
        },
        {
          'id': 4,
          'name': 'models',
          'type': 'Model',
          'typeName': 'Payload',
          'isList': true,
        },
      ],
    },
  },
  'enums': {
    'TaskStatus': {
      'name': 'TaskStatus',
      'values': ['OPEN', 'DONE'],
    },
  },
  'models': {
    'OptionalArrays': {
      'name': 'OptionalArrays',
      'usage': 'output',
      'fields': [
        {
          'id': 1,
          'name': 'nullableIntegers',
          'type': 'Int',
          'isList': true,
          'nullable': true,
        },
        {
          'id': 2,
          'name': 'texts',
          'type': 'String',
          'isList': true,
          'nullable': true,
        },
        {
          'id': 3,
          'name': 'flags',
          'type': 'Boolean',
          'isList': true,
          'nullable': true,
        },
        {
          'id': 4,
          'name': 'child',
          'type': 'Model',
          'typeName': 'ScalarArrays',
          'nullable': true,
        },
      ],
    },
    'ScalarArrays': {
      'name': 'ScalarArrays',
      'usage': 'inputOutput',
      'fields': [
        {'id': 1, 'name': 'integers', 'type': 'Int', 'isList': true},
        {'id': 2, 'name': 'durations', 'type': 'Duration', 'isList': true},
        {'id': 3, 'name': 'floats', 'type': 'Float', 'isList': true},
        {'id': 4, 'name': 'strings', 'type': 'String', 'isList': true},
        {'id': 5, 'name': 'booleans', 'type': 'Boolean', 'isList': true},
        {'id': 6, 'name': 'dates', 'type': 'DateTime', 'isList': true},
        {'id': 7, 'name': 'uuids', 'type': 'UUID', 'isList': true},
        {'id': 8, 'name': 'blobs', 'type': 'Bytes', 'isList': true},
        {'id': 9, 'name': 'decimals', 'type': 'Decimal', 'isList': true},
        {'id': 10, 'name': 'enums', 'type': 'Enum', 'isList': true},
        {'id': 11, 'name': 'jsons', 'type': 'JSON', 'isList': true},
      ],
    },
    'Numbers': {
      'name': 'Numbers',
      'usage': 'inputOutput',
      'fields': [
        {'id': 1, 'name': 'id', 'type': 'Int'},
        {'id': 2, 'name': 'duration', 'type': 'Duration'},
        {'id': 3, 'name': 'values', 'type': 'Int', 'isList': true},
        {'id': 4, 'name': 'optional', 'type': 'Int', 'nullable': true},
        {'id': 5, 'name': 'ratios', 'type': 'Float', 'isList': true},
        {'id': 6, 'name': 'metadata', 'type': 'JSON'},
        {'id': 7, 'name': 'createdAt', 'type': 'DateTime'},
      ],
    },
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
    'projection': {
      'id': 17,
      'name': 'projection',
      'module': 'file',
      'returnType': 'Projection',
    },
    'watchPayloadList': {
      'id': 18,
      'name': 'watchPayloadList',
      'module': 'file',
      'returnType': 'Payload',
      'returnList': true,
      'stream': true,
    },
    'namedEnum': {
      'id': 300,
      'name': 'namedEnum',
      'module': 'scalar',
      'returnType': 'TaskStatus',
    },
    'namedEnums': {
      'id': 301,
      'name': 'namedEnums',
      'module': 'scalar',
      'returnType': 'TaskStatus',
      'returnList': true,
    },
    'implicitInteger': {
      'id': 302,
      'name': 'implicitInteger',
      'module': 'scalar',
    },
    'implicitIntegers': {
      'id': 303,
      'name': 'implicitIntegers',
      'module': 'scalar',
      'returnList': true,
    },
    for (final (index, type) in [..._scalarTypes, 'TaskStatus'].indexed)
      for (final array in [false, true])
        'watch$type${array ? 'Array' : ''}': {
          'id': 400 + index * 2 + (array ? 1 : 0),
          'name': 'watch$type${array ? 'Array' : ''}',
          'module': 'scalar',
          'returnType': type,
          'returnList': array,
          'stream': true,
        },
    for (var index = 0; index < _scalarTypes.length; index++)
      'echo${_scalarTypes[index]}': {
        'id': 200 + index,
        'name': 'echo${_scalarTypes[index]}',
        'module': 'scalar',
        'returnType': _scalarTypes[index],
        'params': [
          {
            'id': 1,
            'name': 'value',
            'type': _scalarTypes[index],
            'typeName': _scalarTypes[index] == 'Enum'
                ? 'TaskStatus'
                : _scalarTypes[index],
          },
        ],
      },
    for (final (index, type) in _scalarTypes.indexed)
      'scalar$type': {
        'id': 100 + index * 2,
        'name': 'scalar$type',
        'module': 'scalar',
        'returnType': type,
      },
    for (final (index, type) in _scalarTypes.indexed)
      'array$type': {
        'id': 101 + index * 2,
        'name': 'array$type',
        'module': 'scalar',
        'returnType': type,
        'returnList': true,
      },
    'echoInteger': {
      'id': 13,
      'name': 'echoInteger',
      'module': 'number',
      'returnType': 'Int',
      'params': [
        {'id': 1, 'name': 'value', 'type': 'Int'},
      ],
    },
    'listDurations': {
      'id': 14,
      'name': 'listDurations',
      'module': 'number',
      'returnType': 'Duration',
      'returnList': true,
    },
    'listFloats': {
      'id': 15,
      'name': 'listFloats',
      'module': 'number',
      'returnType': 'Float',
      'returnList': true,
    },
    'inspectNumbers': {
      'id': 16,
      'name': 'inspectNumbers',
      'module': 'number',
      'returnType': 'Numbers',
      'params': [
        {'id': 1, 'name': 'input', 'type': 'Model', 'typeName': 'Numbers'},
      ],
    },
    'upload': {
      'id': 7,
      'name': 'upload',
      'module': 'file',
      'returnType': 'Payload',
      'params': [
        {'id': 1, 'name': 'chunks', 'type': 'Bytes', 'isList': true},
        {'id': 2, 'name': 'metadata', 'type': 'JSON'},
        {'id': 3, 'name': 'input', 'type': 'Model', 'typeName': 'CreateInput'},
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
