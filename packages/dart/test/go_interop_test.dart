@TestOn('vm')
library;

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:luxo_client/src/codegen.dart';
import 'package:test/test.dart';

void main() {
  test(
    'fixture cleanup is restricted to owned direct children',
    _verifyCleanup,
  );
  for (final gateway in [false, true]) {
    test(
      gateway
          ? 'generated gateway and TCP RPC preserve numbers, errors and streams on Dart VM, JS and Wasm'
          : 'generated Go service interoperates with Dart VM, JS and Wasm',
      () => _verifyConsumer(gateway),
      timeout: const Timeout(Duration(minutes: 5)),
    );
    test(
      'old Dart SDK preserves future enum members; gateway=$gateway',
      () => _verifyEnumUpgradeConsumer(gateway),
      timeout: const Timeout(Duration(minutes: 5)),
    );
  }
  test(
    'gateway is closed when the Dart consumer fails',
    _verifyConsumerFailure,
  );
}

void _verifyCleanup() {
  final root = Directory('.tmp')..createSync(recursive: true);
  final owned = root.createTempSync('go-interop-cleanup-');
  try {
    final nested = Directory('${owned.path}/nested')..createSync();
    final sibling = Directory('${owned.path}/sibling')..createSync();
    final child = Directory('${nested.path}/child')..createSync();
    final link = Link('${nested.path}/link')..createSync(sibling.absolute.path);
    expect(() => _removeOwnedDirectory(nested, nested), throwsStateError);
    expect(() => _removeOwnedDirectory(sibling, nested), throwsStateError);
    expect(
      () => _removeOwnedDirectory(Directory(link.path), nested),
      throwsStateError,
    );
    expect(nested.existsSync(), isTrue);
    expect(sibling.existsSync(), isTrue);
    expect(link.existsSync(), isTrue);
    _removeOwnedDirectory(child, nested);
    expect(child.existsSync(), isFalse);
  } finally {
    _removeOwnedDirectory(owned, root);
  }
}

Future<void> _verifyConsumerFailure() async {
  String? address;
  final failure = StateError('consumer failed');
  await expectLater(
    _withBuiltFixture((_, binary) async {
      await _withDeployment(binary, true, (endpoint) async {
        address = endpoint;
        throw failure;
      });
    }),
    throwsA(same(failure)),
  );
  final endpoint = Uri.parse(address!);
  Socket? connection;
  try {
    await expectLater(() async {
      connection = await Socket.connect(
        endpoint.host,
        endpoint.port,
        timeout: const Duration(seconds: 1),
      );
    }, throwsA(isA<SocketException>()));
  } finally {
    connection?.destroy();
  }
}

Future<void> _verifyConsumer(bool gateway) async {
  await _withBuiltFixture((core, binary) async {
    final root = Directory('.tmp')..createSync(recursive: true);
    final generated = root.createTempSync('go-interop-');
    try {
      await _withDeployment(binary, gateway, (endpoint) async {
        await generate(
          endpoint: endpoint,
          key: 'sdk-interop',
          outDir: generated.path,
        );
        _writeConsumer(core, generated, endpoint);
        await _verifyPlatforms(generated);
      });
    } finally {
      _removeOwnedDirectory(generated, root);
    }
  });
}

Future<void> _withBuiltFixture(
  Future<void> Function(Directory, String) use, {
  bool futureEnum = false,
}) async {
  final core = Directory('../..').absolute;
  final root = Directory('${core.path}/.tmp')..createSync(recursive: true);
  final output = root.createTempSync('sdk-interop-');
  try {
    await _checked('go', [
      'run',
      './tests/sdk-interop',
      '-out',
      output.path,
      if (futureEnum) '-future-enum',
    ], cwd: core.path);
    final binary = '${output.path}/server';
    await _checked('go', ['build', '-o', binary, output.path], cwd: core.path);
    await use(core, binary);
  } finally {
    _removeOwnedDirectory(output, root);
  }
}

Future<void> _verifyEnumUpgradeConsumer(bool gateway) async {
  await _withBuiltFixture((core, binary) async {
    final root = Directory('.tmp')..createSync(recursive: true);
    final generated = root.createTempSync('go-enum-upgrade-');
    try {
      await _withDeployment(binary, gateway, (endpoint) async {
        await generate(
          endpoint: endpoint,
          key: 'sdk-interop',
          outDir: generated.path,
        );
      });
      final types = File('${generated.path}/types.dart');
      final oldTypes = types.readAsStringSync();
      await _withBuiltFixture((_, nextBinary) async {
        await _withDeployment(nextBinary, gateway, (endpoint) async {
          _writeConsumer(core, generated, endpoint, enumUpgrade: true);
          await _verifyPlatforms(generated);
        });
      }, futureEnum: true);
      expect(types.readAsStringSync(), oldTypes);
    } finally {
      _removeOwnedDirectory(generated, root);
    }
  });
}

void _removeOwnedDirectory(Directory directory, Directory root) {
  if (FileSystemEntity.isLinkSync(directory.path) ||
      directory.parent.resolveSymbolicLinksSync() !=
          root.resolveSymbolicLinksSync()) {
    throw StateError('Refusing cleanup outside owned fixture scratch');
  }
  directory.deleteSync(recursive: true);
}

Future<void> _verifyPlatforms(Directory generated) async {
  await _checked(Platform.resolvedExecutable, [
    'analyze',
    '--fatal-infos',
    generated.path,
  ]);
  await _checked(Platform.resolvedExecutable, [
    'test',
    '-p',
    'vm',
    '-p',
    'node',
    '-c',
    'kernel',
    '-c',
    'dart2js',
    '-c',
    'dart2wasm',
    '-j',
    '1',
    '${generated.path}/consumer_test.dart',
    '--reporter',
    'expanded',
  ]);
}

Future<void> _withDeployment(
  String binary,
  bool gateway,
  Future<void> Function(String) use,
) async {
  if (!gateway) return _withServer(binary, use);
  await _withServer(
    binary,
    (backend) async {
      await _withServer(
        binary,
        use,
        arguments: ['-gateway', Uri.parse(backend).authority],
      );
    },
    arguments: ['-rpc'],
    scheme: 'tcp',
  );
}

void _writeConsumer(
  Directory core,
  Directory output,
  String endpoint, {
  bool enumUpgrade = false,
}) {
  final fixture = jsonDecode(
    File('${core.path}/pkg/lux/codec/testdata/protocol-v1.json')
        .readAsStringSync(),
  ) as Map<String, Object?>;
  final cases = (fixture['int64Cases']! as List<Object?>)
      .cast<Map<String, Object?>>()
      .map((entry) => entry['value']! as String)
      .toList();
  File('${output.path}/fixture.dart').writeAsStringSync(
    'const endpoint = ${jsonEncode(endpoint)};\n'
    'const integerCases = <String>${jsonEncode(cases)};\n'
    'const jsonNumberTokens = <String>${jsonEncode(fixture['jsonNumberTokens'])};\n'
    'const jsonUnicodeCases = <Map<String, Object>>${jsonEncode(fixture['jsonUnicodeCases'])};\n'
    'const dateTimeCases = <Map<String, Object>>${jsonEncode(fixture['dateTimeCases'])};\n'
    'const jsonNumberConversions = <Map<String, String?>>${jsonEncode(fixture['jsonNumberConversions'])};\n',
  );
  File('test/fixtures/go_consumer.dart.txt')
      .copySync('${output.path}/interop_cases.dart');
  File('${output.path}/consumer_test.dart').writeAsStringSync(
    "import 'interop_cases.dart' as cases;\n"
    'void main() {\n'
    '${enumUpgrade ? '  cases.registerEnumUpgradeTests();' : '  cases.registerSuccessAndStreamTests();\n  cases.registerErrorTests();'}\n'
    '}\n',
  );
}

Future<void> _withServer(
  String executable,
  Future<void> Function(String) use, {
  List<String> arguments = const [],
  String scheme = 'http',
}) async {
  final process = await Process.start(executable, arguments);
  final errors = process.stderr.transform(utf8.decoder).join();
  final lines = StreamIterator(
    process.stdout.transform(utf8.decoder).transform(const LineSplitter()),
  );
  try {
    expect(await lines.moveNext().timeout(const Duration(seconds: 10)), isTrue);
    final endpoint = Uri.parse(lines.current);
    expect(endpoint.scheme, scheme);
    expect(endpoint.host, '127.0.0.1');
    expect(endpoint.userInfo, isEmpty);
    expect(endpoint.port, greaterThan(0));
    await use(endpoint.toString());
  } finally {
    await process.stdin.close();
    final code = await process.exitCode.timeout(
      const Duration(seconds: 10),
      onTimeout: () async {
        process.kill(ProcessSignal.sigkill);
        return process.exitCode;
      },
    );
    await lines.cancel();
    expect(code, 0, reason: await errors);
  }
}

Future<void> _checked(
  String executable,
  List<String> arguments, {
  String? cwd,
}) async {
  final process = await Process.start(
    executable,
    arguments,
    workingDirectory: cwd,
  );
  final stdout = process.stdout.transform(utf8.decoder).join();
  final stderr = process.stderr.transform(utf8.decoder).join();
  final timer = Timer(
    const Duration(minutes: 2),
    () => process.kill(ProcessSignal.sigkill),
  );
  try {
    final code = await process.exitCode;
    final output = '${await stdout}\n${await stderr}';
    expect(code, 0, reason: '$executable ${arguments.join(' ')}\n$output');
    if (arguments.first == 'test') print(output);
  } finally {
    timer.cancel();
  }
}
