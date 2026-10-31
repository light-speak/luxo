import 'package:luxo_client/src/codegen.dart';
import 'package:luxo_client/src/types.dart';
import 'package:test/test.dart';

import 'dart:io';
import 'dart:convert';

void main() {
  test('rejects malformed query capabilities', () {
    for (final field in [
      const LuxoQueryFilter(field: 'raw', type: 'JSON', operators: ['eq']),
      const LuxoQueryFilter(
        field: 'active',
        type: 'Boolean',
        operators: ['gt'],
      ),
      const LuxoQueryFilter(field: 'id', type: 'Int', operators: []),
      const LuxoQueryFilter(field: 'id', type: 'Int', operators: ['eq', 'eq']),
      const LuxoQueryFilter(field: "bad'", type: 'Int', operators: ['eq']),
    ]) {
      final schema = LuxoSchema(
        models: {},
        apis: {
          'read': LuxoAPI(
            id: 1,
            name: 'read',
            module: 'x',
            paginated: true,
            query: LuxoQueryCapabilities(filters: [field]),
          ),
        },
      );
      expect(() => generateQueryBuilders(schema), throwsFormatException);
    }
    expect(
      generateQueryBuilders(const LuxoSchema(models: {}, apis: {})),
      isNot(contains('import')),
    );
  });
  test('generates only advertised query capabilities', () async {
    final document = <String, dynamic>{
      'models': <String, Object>{},
      'enums': {
        'State': {
          'name': 'State',
          'values': ['OPEN'],
        },
      },
      'apis': {
        'listUsers': {
          'id': 1,
          'name': 'listUsers',
          'module': 'user',
          'paginated': true,
          'query': {
            'filters': [
              {
                'field': 'id',
                'type': 'Int',
                'operators': ['eq', 'gt'],
              },
              {
                'field': 'state',
                'type': 'State',
                'operators': ['eq'],
              },
            ],
            'sorters': ['id'],
          },
        },
        'custom': {
          'id': 2,
          'name': 'custom',
          'module': 'user',
          'returnList': true,
        },
      },
    };
    final schema = LuxoSchema.fromJson(document);
    final code = generateQueryBuilders(schema);
    expect(code, contains('class LuxoQuery_listUsers'));
    expect(code, contains('id_eq(LuxoInt64 value)'));
    expect(code, contains('id_desc()'));
    expect(code, isNot(contains('id_contains')));
    expect(code, isNot(contains('LuxoQuery_custom')));
    await _runGenerated(document);
  });
}

Future<void> _runGenerated(Map<String, dynamic> document) async {
  final root = Directory('.tmp')..createSync(recursive: true);
  final directory = root.createTempSync('query-generated-');
  final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
  server.listen((request) async {
    request.response.headers.contentType = ContentType.json;
    request.response.write(jsonEncode(document));
    await request.response.close();
  });
  try {
    await generate(
      endpoint: 'http://127.0.0.1:${server.port}',
      key: 'test',
      outDir: directory.path,
    );
    final probe = File('${directory.path}/main.dart')
      ..writeAsStringSync('''
import 'queries.dart';
import 'types.dart';
import 'package:luxo_client/luxo_client.dart';
void main() {
  final value = LuxoInt64.parse('9007199254740993');
  final filter = LuxoQuery_listUsers.id_eq(value);
  if (filter.field != 'id' || filter.op != 'eq' || filter.value != value) throw StateError('filter');
  final sorter = LuxoQuery_listUsers.id_desc();
  if (sorter.field != 'id' || sorter.order != 'desc') throw StateError('sorter');
  if (LuxoQuery_listUsers.state_eq(State.fromValue('OPEN')).value != 'OPEN') throw StateError('enum');
  print(filter.value);
}
''');
    final arguments = [
      '--packages=${File('.dart_tool/package_config.json').absolute.path}',
      probe.absolute.path,
    ];
    final result = await Process.run(Platform.resolvedExecutable, arguments);
    expect(result.exitCode, 0, reason: '${result.stdout}\n${result.stderr}');
    expect(result.stdout, contains('9007199254740993'));
    probe.writeAsStringSync(
      "import 'queries.dart'; void main() { LuxoQuery_listUsers.id_eq(42); }",
    );
    final invalid = await Process.run(Platform.resolvedExecutable, arguments);
    expect(invalid.exitCode, isNot(0), reason: 'Int must require LuxoInt64');
  } finally {
    await server.close(force: true);
    directory.deleteSync(recursive: true);
  }
}
