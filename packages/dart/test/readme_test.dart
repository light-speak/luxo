@TestOn('vm')
library;

import 'dart:convert';
import 'dart:io';
import 'dart:isolate';

import 'package:build/build.dart';
import 'package:luxo_client/src/builder/select_builder.dart';
import 'package:luxo_client/src/codegen.dart';
import 'package:test/test.dart';

import 'select_fixture.dart';

const _sections = {
  'Quick Start': 'quickStart',
  'HTTP Transport': 'http',
  'WebSocket — Real-time Subscriptions': 'websocket',
  'Binary Mode': 'binary',
  '401 Auto-Refresh': 'refresh',
  'Field Selection — End to End': 'selection',
  'Binary Codec': 'codec',
  'Code Generation': 'generated',
  'Compile-time Field Tracking': 'tracking',
  'JSON parameter precision': 'jsonNumbers',
};

void main() {
  test(
    'README Dart snippets compile and run with generated client and hints',
    _verifyReadme,
    timeout: const Timeout(Duration(minutes: 2)),
  );
  for (final scalar in ['Bytes', 'JSON']) {
    test(
      'codegen retains conversion helpers for a type-only $scalar schema',
      () => _verifyConversionImport(scalar),
    );
  }
  for (final scalar in [
    'Int',
    'Bytes',
    'JSON',
    'TaskStatus',
    'empty',
    'internal',
  ]) {
    test(
      'minimal $scalar client passes strict analysis',
      () => _verifyMinimalClient(scalar),
    );
  }
  for (final scalar in ['String', 'Bytes', 'JSON']) {
    test(
      'input-only $scalar client passes strict analysis',
      () => _verifyMinimalClient(scalar, input: true),
    );
  }
}

Future<void> _verifyMinimalClient(String scalar, {bool input = false}) async {
  final fixture = await SelectFixture.create(
    {},
    prepare: (root) =>
        _generateClient(root, schema: _minimalSchema(scalar, input: input)),
  );
  addTearDown(fixture.close);
  await fixture.runDart(['analyze', '--fatal-infos', 'lib']);
}

Map<String, Object?> _minimalSchema(String scalar, {required bool input}) => {
  'enums': {
    'TaskStatus': {
      'name': 'TaskStatus',
      'values': ['OPEN', 'DONE'],
    },
  },
  if (input)
    'types': {
      'Payload': {
        'name': 'Payload',
        'usage': 'input',
        'fields': [
          {'id': 1, 'name': 'value', 'type': scalar},
        ],
      },
    },
  'apis': {
    if (scalar != 'empty')
      for (final stream in [false, true])
        '${scalar == 'internal' ? 'svc:' : ''}${stream ? 'watchValue' : 'readValue'}': {
          'id': stream ? 2 : 1,
          'name':
              '${scalar == 'internal' ? 'svc:' : ''}${stream ? 'watchValue' : 'readValue'}',
          'module': 'fixture',
          'returnType': input
              ? 'Int'
              : scalar == 'internal'
              ? 'JSON'
              : scalar,
          'stream': stream,
          if (input)
            'params': [
              {
                'id': 1,
                'name': 'input',
                'type': 'Model',
                'typeName': 'Payload',
              },
            ],
        },
  },
};

Future<void> _verifyReadme() async {
  final readme = await File('README.md').readAsString();
  final fixture = await SelectFixture.create({
    'pubspec.yaml': 'name: fixture\nenvironment:\n  sdk: ">=3.13.0 <4.0.0"\n',
    'lib/readme.dart': _sections.values
        .map((name) => "export 'example_$name.dart';")
        .join('\n'),
    for (final entry in _sections.entries)
      'lib/example_${entry.value}.dart': _example(
        readme,
        entry.key,
        entry.value,
      ),
    'bin/verify_test.dart': await File('test/fixtures/readme_consumer.dart.txt')
        .readAsString(),
  }, prepare: _generateClient);
  addTearDown(fixture.close);
  for (final name in _sections.values) {
    await fixture.unit('lib/example_$name.dart');
  }
  final step = SelectBuildStep(fixture);
  await selectHintsBuilder(BuilderOptions({})).build(step);
  expect(step.outputs.values.single, contains("'getUser': 'name'"));
  for (final entry in step.outputs.entries) {
    await File('${fixture.root.path}/${entry.key.path}')
        .writeAsString(entry.value);
  }
  await fixture.runDart([
    'analyze',
    '--fatal-infos',
    'lib',
    'bin/verify_test.dart',
  ]);
  final testLibrary = await Isolate.resolvePackageUri(
    Uri.parse('package:test/test.dart'),
  );
  await fixture.runDart([
    '--packages=.dart_tool/package_config.json',
    testLibrary!.resolve('../bin/test.dart').toFilePath(),
    '-r',
    'expanded',
    'bin/verify_test.dart',
  ]);
}

Future<void> _verifyConversionImport(String scalar) async {
  final fixture = await SelectFixture.create(
    {},
    prepare: (root) => _generateClient(
      root,
      schema: {
        'types': {
          'Payload': {
            'name': 'Payload',
            'usage': 'inputOutput',
            'fields': [
              {'id': 1, 'name': 'value', 'type': scalar},
            ],
          },
        },
        'apis': {
          'getPayload': {
            'id': 1,
            'name': 'getPayload',
            'module': 'payload',
            'returnType': 'Payload',
          },
        },
      },
    ),
  );
  addTearDown(fixture.close);
  final types = await File('${fixture.root.path}/lib/src/luxo/types.dart')
      .readAsString();
  expect(types, contains("import 'dart:convert';"));
  await fixture.runDart(['analyze', '--fatal-infos', 'lib']);
}

String _codeBlock(String readme, String title) {
  final heading = RegExp(
    '^#{2,3} ${RegExp.escape(title)}\$',
    multiLine: true,
  ).firstMatch(readme);
  expect(heading, isNotNull, reason: 'missing README section: $title');
  final rest = readme.substring(heading!.end);
  final next =
      RegExp(r'^#{2,3} ', multiLine: true).firstMatch(rest)?.start ??
      rest.length;
  final blocks = RegExp(r'```dart\n([\s\S]*?)\n```')
      .allMatches(rest.substring(0, next));
  expect(blocks, hasLength(1), reason: 'compile every Dart example in $title');
  return blocks.single.group(1)!;
}

String _example(String readme, String title, String name) {
  final source = _codeBlock(
    readme,
    title,
  ).replaceAll('package:your_app/', 'package:fixture/');
  final importPattern = RegExp(r"^import [^;]+;\n", multiLine: true);
  final imports = importPattern
      .allMatches(source)
      .map((match) => match.group(0)!)
      .toList();
  if (!imports.any(
    (value) => value.contains('package:luxo_client/luxo_client.dart'),
  )) {
    imports.add("import 'package:luxo_client/luxo_client.dart';\n");
  }
  if (name == 'tracking') imports.add("import 'src/luxo/client.dart';\n");
  final body = source
      .replaceAll(importPattern, '')
      .replaceAll(
        RegExp(
          r"'(?:http|ws)s?://(?:localhost:4000|api\.example\.com)/(?:luvia|ws)'",
        ),
        'endpoint',
      );
  final setup = switch (name) {
    'selection' => 'final transport = HttpTransport(endpoint);',
    'tracking' => 'final client = LuxoClient.create(endpoint);',
    _ => '',
  };
  final cleanup = switch (name) {
    'selection' => 'transport.close();',
    'tracking' => 'client.close();',
    _ => '',
  };
  final exercise = name == 'refresh' ? _refreshInvocation : '';
  return '${imports.join()}\nFuture<void> $name(String endpoint) async {\n$setup\ntry {\n$body\n$exercise\n} finally { $cleanup }\n}';
}

const _refreshInvocation = r'''
final transport = createAuthenticatedTransport(endpoint,
  token: 'expired-token', refreshToken: () async => 'renewed-token');
try {
  await transport.call<Object?>('getUser',
    params: {'id': LuxoInt64.fromInt(1), r'$select': 'name'});
} finally {
  transport.close();
}
''';

Future<void> _generateClient(
  Directory root, {
  Map<String, Object?> schema = _schema,
}) async {
  final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
  final response = () async {
    final request = await server.first;
    request.response.headers.contentType = ContentType.json;
    request.response.write(jsonEncode(schema));
    await request.response.close();
  }();
  try {
    await generate(
      endpoint: 'http://${server.address.host}:${server.port}/luvia',
      key: 'fixture',
      outDir: '${root.path}/lib/src/luxo',
    );
    await response;
  } finally {
    await server.close(force: true);
  }
}

const _schema = {
  'types': {
    'UnusedInput': {
      'name': 'UnusedInput',
      'usage': 'unused',
      'fields': [
        {'id': 1, 'name': 'value', 'type': 'JSON'},
      ],
    },
  },
  'models': {
    'User': {
      'name': 'User',
      'usage': 'output',
      'fields': [
        {'id': 1, 'name': 'id', 'type': 'Int'},
        {'id': 2, 'name': 'name', 'type': 'String'},
        {'id': 3, 'name': 'email', 'type': 'String'},
        {
          'id': 4,
          'name': 'posts',
          'type': 'Model',
          'typeName': 'Post',
          'isList': true,
        },
      ],
    },
    'Post': {
      'name': 'Post',
      'usage': 'output',
      'fields': [
        {'id': 1, 'name': 'id', 'type': 'Int'},
        {'id': 2, 'name': 'title', 'type': 'String'},
      ],
    },
  },
  'apis': {
    'getUser': {
      'id': 1,
      'name': 'getUser',
      'module': 'user',
      'returnType': 'User',
      'params': [
        {'id': 1, 'name': 'id', 'type': 'Int'},
      ],
    },
    'listPosts': {
      'id': 2,
      'name': 'listPosts',
      'module': 'post',
      'returnType': 'Post',
      'paginated': true,
      'params': [
        {'id': 1, 'name': 'page', 'type': 'Int', 'hasDefault': true},
        {'id': 2, 'name': 'pageSize', 'type': 'Int', 'hasDefault': true},
      ],
    },
  },
};
