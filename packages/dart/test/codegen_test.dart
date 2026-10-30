import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:luxo_client/src/codegen.dart';
import 'package:test/test.dart';

import 'support/stream_decode_server.dart';

void main() {
  test(
    'generated nullable benchmark fixtures preserve the wire contract',
    () async {
      final result = await _runDart([
        '--packages=.dart_tool/package_config.json',
        'benchmark/nullable_root_benchmark.dart',
        'verify',
      ]);
      expect(result.exitCode, 0, reason: '${result.stdout}\n${result.stderr}');
      expect(
        result.stdout,
        contains('verified 11 generated nullable root fixtures'),
      );
    },
    timeout: const Timeout(Duration(seconds: 65)),
  );
  for (final target in ['models', 'types', 'apis', 'params']) {
    final limit = target == 'apis'
        ? 2147483647
        : target == 'params'
        ? 2147483645
        : 65535;
    for (final id in [0, -1, limit + 1, 9007199254740991]) {
      test('reject stable $target ID $id before writing', () async {
        await _rejectSchema(_numberedSchema(target, id), 'ID', existing: true);
      });
    }
    test('reject stable duplicate $target IDs', () async {
      await _rejectSchema(
        _numberedSchema(target, 1, duplicate: true),
        'duplicate ID',
      );
    });
  }
  test('internal routes use explicit metadata instead of names', () async {
    final root = Directory('.tmp')..createSync(recursive: true);
    final output = root.createTempSync('internal-routes-');
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    server.listen((request) async {
      request.response.headers.contentType = ContentType.json;
      request.response.write(
        jsonEncode({
          'models': <String, Object>{},
          'apis': {
            for (final entry in {
              'ping': false,
              'privateOperation': true,
            }.entries)
              entry.key: {
                'id': entry.value ? 2 : 1,
                'name': entry.key,
                'module': 'test',
                'returnType': 'Int',
                'internal': entry.value,
              },
          },
        }),
      );
      await request.response.close();
    });
    try {
      await generate(
        endpoint: 'http://${server.address.host}:${server.port}',
        key: 'test',
        outDir: output.path,
      );
      for (final file in ['client.dart', 'schema.dart']) {
        final source = File('${output.path}/$file').readAsStringSync();
        expect(source, contains('ping'));
        expect(source, isNot(contains('privateOperation')));
      }
    } finally {
      await server.close(force: true);
      output.deleteSync(recursive: true);
    }
  });
  test('process budget reports an explicit timeout', () async {
    final root = Directory('.tmp')..createSync(recursive: true);
    final output = root.createTempSync('process-budget-');
    final script = File('${output.path}/hold.dart')
      ..writeAsStringSync(
        'Future<void> main() => Future<void>.delayed(const Duration(minutes: 1));\n',
      );
    try {
      await expectLater(
        _runProcess(Platform.resolvedExecutable, [
          script.path,
        ], timeout: Duration.zero),
        throwsA(
          isA<TimeoutException>().having(
            (error) => error.duration,
            'budget',
            Duration.zero,
          ),
        ),
      );
    } finally {
      output.deleteSync(recursive: true);
    }
  });
  test('process budget preserves successful output', () async {
    final result = await _runProcess(Platform.resolvedExecutable, [
      '--version',
    ]);
    expect(result.exitCode, 0);
    expect('${result.stdout}${result.stderr}', contains('Dart SDK version'));
  });
  for (final target in ['models', 'types', 'params']) {
    for (final name in [null, '', 'Missing']) {
      for (final list in [false, true]) {
        test(
          'reject missing enum declaration $target/$name/list=$list',
          () async {
            await _rejectSchema(
              _enumReferenceSchema(target, name, list),
              'requires a declared enum type',
            );
          },
        );
      }
    }
  }
  for (final list in [false, true]) {
    test('reject undeclared API return list=$list', () async {
      await _rejectSchema({
        'apis': {
          'read': {
            'id': 1,
            'name': 'read',
            'module': 'test',
            'returnType': 'Missing',
            'returnList': list,
          },
        },
      }, 'has an undeclared return type');
    });
    test('reject bare Enum API return list=$list', () async {
      await _rejectSchema({
        'apis': {
          'read': {
            'id': 1,
            'name': 'read',
            'module': 'test',
            'returnType': 'Enum',
            'returnList': list,
          },
        },
      }, 'requires a named enum return type');
    });
  }
  test('reject enum declaration with a different lookup name', () async {
    await _rejectSchema({
      'enums': {
        'State': {
          'name': 'Other',
          'values': ['OPEN'],
        },
      },
    }, 'does not match its name');
  });
  test(
    'reject invalid enum Schema without replacing an existing client',
    () async {
      await _rejectSchema(
        _enumReferenceSchema('params', null, false),
        'requires a declared enum type',
        existing: true,
      );
    },
  );
  for (final values in <List<String>>[
    [],
    ['OPEN', 'OPEN'],
  ]) {
    test('reject invalid enum members $values before writing types', () async {
      await _rejectSchema({
        'enums': {
          'State': {'name': 'State', 'values': values},
        },
      }, 'requires distinct members');
    });
  }
  for (final suffix in ['Known', 'Unknown']) {
    test('reject generated enum $suffix name collisions', () async {
      await _rejectSchema({
        'enums': {
          'State': {
            'name': 'State',
            'values': ['OPEN'],
          },
          'State$suffix': {
            'name': 'State$suffix',
            'values': ['READY'],
          },
        },
      }, 'conflicts with Schema');
    });
  }
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
        expect(client, contains('echoEnum({required TaskStatus value})'));
        expect(types, contains('sealed class TaskStatus'));
        expect(
          types,
          contains('static List<String> \$rawList(List<TaskStatus> values)'),
        );
        expect(client, contains('TaskStatus.\$nullableRawList(nullableList)'));
        expect(types, contains('enum TaskStatusKnown implements TaskStatus'));
        expect(
          types,
          contains('final class TaskStatusUnknown extends TaskStatus'),
        );

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
        expect(types, isNot(contains('Uint8List.fromList(utf8.encode(')));
        expect(
          types,
          contains('encoder.writeBytes(utf8.encode(encodeLuxoJSON('),
        );
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
            'Future<Page<Payload>> searchPayloads({LuxoInt64? page, LuxoInt64? pageSize, String? select, PreparedSelection? \$selectionPlan, List<LuxoFilter>? filters, List<LuxoSorter>? sorters})',
          ),
        );
        expect(
          client,
          contains(
            'Future<Payload> createSnapshot({String? select, PreparedSelection? \$selectionPlan})',
          ),
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
            'Future<void Function()> subscribeWatchPayload({required LuxoInt64 projectId, String? select, PreparedSelection? \$selectionPlan, required void Function(Payload) onData, void Function(LuxoError)? onError, void Function()? onDone})',
          ),
        );
        expect(client, contains("transport.subscribe('watchPayload'"));
        expect(
          client,
          contains("const {}, onData, request: _watchPayloadRequest("),
        );
        expect(
          client,
          contains('class _uploadRequest extends GeneratedRequest'),
        );
        expect(client, contains('encoder.writeDelimited(value.writeLuxo)'));
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
        File('${output.path}/nullable_root_consumer.dart').writeAsStringSync(
          File('test/fixtures/nullable_root_consumer.dart.txt')
              .readAsStringSync(),
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
        if (Platform.environment['LUXO_BENCH_ENUMS'] == '1') {
          await _runEnumBenchmarks(output);
        }
      } finally {
        for (final socket in sockets) await socket.close();
        await server.close(force: true);
        if (output.existsSync()) output.deleteSync(recursive: true);
      }
    },
    timeout: Timeout(
      Duration(
        minutes:
            Platform.environment['LUXO_BENCH_JSON_PROJECTION'] == '1' ||
                Platform.environment['LUXO_BENCH_ENUMS'] == '1'
            ? 10
            : 2,
      ),
    ),
  );
}

Map<String, Object?> _numberedSchema(
  String target,
  int id, {
  bool duplicate = false,
}) {
  final fields = [
    {'id': id, 'name': 'value', 'type': 'Int'},
    if (duplicate) {'id': id, 'name': 'other', 'type': 'Int'},
  ];
  if (target == 'apis' || target == 'params') {
    return {
      'apis': {
        'read': {
          'id': target == 'apis' ? id : 1,
          'name': 'read',
          'module': 'test',
          'returnType': 'Int',
          if (target == 'params') 'params': fields,
        },
        if (target == 'apis' && duplicate)
          'other': {
            'id': id,
            'name': 'other',
            'module': 'test',
            'returnType': 'Int',
          },
      },
    };
  }
  return {
    target: {
      'Payload': {'name': 'Payload', 'fields': fields},
    },
  };
}

Map<String, Object?> _enumReferenceSchema(
  String target,
  String? name,
  bool list,
) {
  final field = {
    'id': 1,
    'name': 'state',
    'type': 'Enum',
    if (name != null) 'typeName': name,
    'isList': list,
    'nullable': list,
  };
  return target == 'params'
      ? {
          'apis': {
            'read': {
              'id': 1,
              'name': 'read',
              'module': 'test',
              'returnType': 'Int',
              'params': [field],
            },
          },
        }
      : {
          target: {
            'Payload': {
              'name': 'Payload',
              'fields': [field],
            },
          },
        };
}

Future<void> _rejectSchema(
  Map<String, Object?> schema,
  String message, {
  bool existing = false,
}) async {
  final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
  final root = Directory('.tmp')..createSync(recursive: true);
  final output = root.createTempSync('enum-schema-');
  final types = File('${output.path}/types.dart');
  if (existing) types.writeAsStringSync('// Existing generated client.\n');
  server.listen((request) async {
    request.response.headers.contentType = ContentType.json;
    request.response.write(jsonEncode(schema));
    await request.response.close();
  });
  try {
    await expectLater(
      generate(
        endpoint: 'http://${server.address.host}:${server.port}/luxo',
        key: 'test',
        outDir: output.path,
      ),
      throwsA(
        isA<FormatException>().having(
          (error) => error.message,
          'message',
          contains(message),
        ),
      ),
    );
    if (existing) {
      expect(types.readAsStringSync(), '// Existing generated client.\n');
      expect(output.listSync(), hasLength(1));
    } else {
      expect(output.listSync(), isEmpty);
    }
  } finally {
    await server.close(force: true);
    output.deleteSync(recursive: true);
  }
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
  await _runGeneratedBenchmarks(output, 'projection', script, test);
}

Future<void> _runEnumBenchmarks(Directory output) async {
  final script = '${output.path}/enum_benchmark.dart';
  File(script).writeAsStringSync(
    File('benchmark/enum_benchmark.dart.txt').readAsStringSync(),
  );
  final test = '${output.path}/enum_benchmark_test.dart';
  File(test).writeAsStringSync(
    "import 'package:test/test.dart';\nimport 'enum_benchmark.dart' as bench;\nvoid main() { test('enum benchmark', bench.main); }\n",
  );
  await _runGeneratedBenchmarks(output, 'enum', script, test);
}

Future<void> _runGeneratedBenchmarks(
  Directory output,
  String name,
  String script,
  String test,
) async {
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
    '${output.path}/$name-bench',
  ]);
  expect(compile.exitCode, 0, reason: '${compile.stdout}\n${compile.stderr}');
  for (final platform in ['aot', 'dart2js', 'dart2wasm']) {
    for (var round = 1; round <= 3; round++) {
      final result = platform == 'aot'
          ? await _runProcess(
              '${output.path}/$name-bench',
              [],
              timeout: const Duration(minutes: 3),
            )
          : await _runProcess(Platform.resolvedExecutable, [
              'test',
              '-p',
              'node',
              '-c',
              platform,
              test,
              '--reporter',
              'expanded',
            ], timeout: const Duration(minutes: 3));
      expect(result.exitCode, 0, reason: '${result.stdout}\n${result.stderr}');
      print('$name benchmark $platform round $round\n${result.stdout}');
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
  List<String> arguments, {
  Duration timeout = const Duration(seconds: 60),
}) async {
  final process = await Process.start(executable, arguments);
  final stdout = process.stdout.transform(utf8.decoder).join();
  final stderr = process.stderr.transform(utf8.decoder).join();
  var expired = false;
  Timer? force;
  final timer = Timer(timeout, () {
    expired = true;
    process.kill(ProcessSignal.sigterm);
    force = Timer(
      const Duration(seconds: 2),
      () => process.kill(ProcessSignal.sigkill),
    );
  });
  try {
    final result = ProcessResult(
      process.pid,
      await process.exitCode,
      await stdout,
      await stderr,
    );
    if (expired) {
      throw TimeoutException(
        'Process $executable exceeded its budget.\n${result.stdout}\n${result.stderr}',
        timeout,
      );
    }
    return result;
  } finally {
    timer.cancel();
    force?.cancel();
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
    'OtherStatus': {
      'name': 'OtherStatus',
      'values': ['class', 'values', 'rawValue', '_hidden', 'OtherStatusKnown'],
    },
  },
  'models': {
    'EnumPayload': {
      'name': 'EnumPayload',
      'usage': 'inputOutput',
      'fields': [
        {'id': 1, 'name': 'status', 'type': 'Enum', 'typeName': 'TaskStatus'},
        {
          'id': 2,
          'name': 'optional',
          'type': 'Enum',
          'typeName': 'TaskStatus',
          'nullable': true,
        },
        {
          'id': 3,
          'name': 'statuses',
          'type': 'Enum',
          'typeName': 'TaskStatus',
          'isList': true,
        },
        {
          'id': 4,
          'name': 'optionalList',
          'type': 'Enum',
          'typeName': 'TaskStatus',
          'isList': true,
          'nullable': true,
        },
      ],
    },
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
        {
          'id': 10,
          'name': 'enums',
          'type': 'Enum',
          'typeName': 'TaskStatus',
          'isList': true,
        },
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
    for (final (index, type) in [
      ..._scalarTypes,
      'Payload',
      'Projection',
    ].indexed)
      for (final array in [false, true])
        for (final stream in [false, true])
          'optional$type${array ? 'Array' : ''}${stream ? 'Stream' : ''}': {
            'id': 1000 + index * 4 + (array ? 2 : 0) + (stream ? 1 : 0),
            'name':
                'optional$type${array ? 'Array' : ''}${stream ? 'Stream' : ''}',
            'module': 'nullable',
            'returnType': type == 'Enum' ? 'TaskStatus' : type,
            'returnList': array,
            'returnNullable': true,
            'stream': stream,
          },
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
    'enumOptions': {
      'id': 304,
      'name': 'enumOptions',
      'module': 'scalar',
      'returnType': 'TaskStatus',
      'params': [
        {
          'id': 1,
          'name': 'nullable',
          'type': 'Enum',
          'typeName': 'TaskStatus',
          'nullable': true,
        },
        {
          'id': 2,
          'name': 'nullableList',
          'type': 'Enum',
          'typeName': 'TaskStatus',
          'isList': true,
          'nullable': true,
        },
        {
          'id': 3,
          'name': 'defaulted',
          'type': 'Enum',
          'typeName': 'TaskStatus',
          'hasDefault': true,
        },
        {
          'id': 4,
          'name': 'nullableDefault',
          'type': 'Enum',
          'typeName': 'TaskStatus',
          'nullable': true,
          'hasDefault': true,
        },
      ],
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
          'returnType': type == 'Enum' ? 'TaskStatus' : type,
          'returnList': array,
          'stream': true,
        },
    for (var index = 0; index < _scalarTypes.length; index++)
      'echo${_scalarTypes[index]}': {
        'id': 200 + index,
        'name': 'echo${_scalarTypes[index]}',
        'module': 'scalar',
        'returnType': _scalarTypes[index] == 'Enum'
            ? 'TaskStatus'
            : _scalarTypes[index],
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
        'returnType': type == 'Enum' ? 'TaskStatus' : type,
      },
    for (final (index, type) in _scalarTypes.indexed)
      'array$type': {
        'id': 101 + index * 2,
        'name': 'array$type',
        'module': 'scalar',
        'returnType': type == 'Enum' ? 'TaskStatus' : type,
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
