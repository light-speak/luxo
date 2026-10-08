import 'dart:convert';
import 'dart:io';

import 'package:luxo_client/src/codegen.dart';

const _declarations = [
  ('integer', 'Int', false),
  ('boolean', 'Boolean', false),
  ('text', 'String', false),
  ('bytes', 'Bytes', false),
  ('integers', 'Int', true),
  ('object', 'Snapshot', false),
  ('objects', 'Snapshot', true),
];

Future<void> main(List<String> arguments) async {
  if (arguments.length != 1 ||
      !['verify', 'exe', 'js', 'wasm'].contains(arguments.single)) {
    throw ArgumentError('Expected verify, exe, js or wasm');
  }
  final root = Directory('.tmp')..createSync(recursive: true);
  final directory = root.createTempSync('nullable-root-benchmark-');
  try {
    await _generate(directory);
    _writeProbe(directory);
    await _run(Platform.resolvedExecutable, [
      'analyze',
      '--fatal-infos',
      directory.path,
    ]);
    if (arguments.single == 'verify') {
      await _run(Platform.resolvedExecutable, [
        '--packages=${File('.dart_tool/package_config.json').absolute.path}',
        '${directory.path}/probe.dart',
        '--verify',
      ]);
    } else {
      await _measure(directory, arguments.single);
    }
  } finally {
    if (FileSystemEntity.isLinkSync(directory.path) ||
        directory.parent.resolveSymbolicLinksSync() !=
            root.resolveSymbolicLinksSync()) {
      throw StateError('Refusing cleanup outside owned scratch');
    }
    directory.deleteSync(recursive: true);
  }
}

Map<String, Object?> _schema() => {
  'models': <String, Object>{},
  'types': {
    'Snapshot': {
      'name': 'Snapshot',
      'usage': 'output',
      'fields': [
        {'id': 1, 'name': 'id', 'type': 'Int'},
        {'id': 2, 'name': 'label', 'type': 'String'},
      ],
    },
  },
  'apis': {
    for (final (index, (name, type, list)) in _declarations.indexed)
      for (final nullable in [false, true])
        '$name${nullable ? 'Nullable' : 'Plain'}': {
          'id': index * 2 + (nullable ? 2 : 1),
          'name': '$name${nullable ? 'Nullable' : 'Plain'}',
          'module': 'benchmark',
          'returnType': type,
          'returnList': list,
          'returnNullable': nullable,
          'stream': true,
        },
  },
};

Future<void> _generate(Directory directory) async {
  final source = jsonEncode(_schema());
  final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
  server.listen((request) async {
    request.response.headers.contentType = ContentType.json;
    request.response.write(source);
    await request.response.close();
  });
  try {
    await generate(
      endpoint: 'http://${server.address.host}:${server.port}',
      key: 'benchmark',
      outDir: directory.path,
    );
  } finally {
    await server.close(force: true);
  }
}

void _writeProbe(Directory directory) {
  final registrations = StringBuffer();
  for (final (name, _, _) in _declarations) {
    for (final nullable in [false, true]) {
      final method =
          '${name[0].toUpperCase()}${name.substring(1)}${nullable ? 'Nullable' : 'Plain'}';
      registrations.writeln('  await client.subscribe$method(onData: (_) {});');
    }
  }
  final source = File('benchmark/nullable_root_probe.dart.txt')
      .readAsStringSync();
  if ('__REGISTER_DECODERS__'.allMatches(source).length != 1) {
    throw StateError('Unexpected registration placeholder count');
  }
  File('${directory.path}/probe.dart').writeAsStringSync(
    source.replaceFirst('// __REGISTER_DECODERS__', registrations.toString()),
  );
}

Future<void> _measure(Directory directory, String platform) async {
  final output = '${directory.absolute.path}/probe.$platform';
  await _run(Platform.resolvedExecutable, [
    'compile',
    platform,
    '--packages=${File('.dart_tool/package_config.json').absolute.path}',
    '${directory.absolute.path}/probe.dart',
    '-o',
    output,
  ]);
  final executable = platform == 'exe' ? output : 'node';
  final arguments = platform == 'exe'
      ? <String>[]
      : [_webRunner(directory, platform)];
  for (var process = 1; process <= 3; process++) {
    stdout.writeln('$platform process $process');
    await _run(executable, arguments);
  }
}

String _webRunner(Directory directory, String platform) {
  final runner = File(
    '${directory.path}/run.${platform == 'js' ? 'cjs' : 'mjs'}',
  );
  runner.writeAsStringSync(
    platform == 'js'
        ? "globalThis.self = globalThis; require('./probe.js');\n"
        : "import { readFile } from 'node:fs/promises';\n"
              "import { compile } from './probe.mjs';\n"
              "const compiled = await compile(await readFile(new URL('./probe.wasm', import.meta.url)));\n"
              'const instance = await compiled.instantiate({}); instance.invokeMain();\n',
  );
  return runner.absolute.path;
}

Future<void> _run(String executable, List<String> arguments) async {
  final result = await Process.run(executable, arguments);
  stdout.write(result.stdout);
  stderr.write(result.stderr);
  if (result.exitCode != 0)
    throw StateError('$executable exited ${result.exitCode}');
}
