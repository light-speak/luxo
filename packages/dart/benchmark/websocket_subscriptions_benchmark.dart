import 'dart:io';

// Compile a scratch copy so the probe can exercise real private dispatch methods
// without adding benchmark hooks or reflection to the shipped transport.
Future<void> main(List<String> args) async {
  if (args.isEmpty || !['exe', 'js', 'wasm'].contains(args.first)) {
    throw ArgumentError('Expected exe, js or wasm');
  }
  final platform = args.first;
  if (args.length == 3 && args[1] == 'presence') {
    await _measurePresence(platform, args[2]);
    return;
  }
  final snapshot =
      args.length == 3 && ['disconnect', 'decode'].contains(args[1]);
  if (!snapshot &&
      (args.length > 2 || (args.length == 2 && args[1] != 'terminal'))) {
    throw ArgumentError(
      'Expected terminal, or disconnect/decode <before-transport.dart>',
    );
  }
  final root = Directory('.tmp')..createSync(recursive: true);
  final directory = root.createTempSync('ws-subscriptions-benchmark-');
  try {
    final entry = _writeProbe(
      directory,
      snapshot
          ? args[1]
          : args.length == 2
          ? 'terminal'
          : 'subscriptions',
      snapshot ? args[2] : null,
    );
    final output = '${directory.absolute.path}/probe.$platform';
    await _run(Platform.resolvedExecutable, [
      'compile',
      platform,
      '--packages=${File('.dart_tool/package_config.json').absolute.path}',
      entry.absolute.path,
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
  } finally {
    _cleanOwned(directory, root);
  }
}

void _cleanOwned(Directory directory, Directory root) {
  if (FileSystemEntity.isLinkSync(directory.path) ||
      directory.parent.resolveSymbolicLinksSync() !=
          root.resolveSymbolicLinksSync()) {
    throw StateError('Refusing cleanup outside owned scratch');
  }
  directory.deleteSync(recursive: true);
}

Future<void> _measurePresence(String platform, String snapshot) async {
  final root = Directory('.tmp')..createSync(recursive: true);
  final directory = root.createTempSync('response-presence-benchmark-');
  try {
    for (final variant in ['before', 'after']) {
      final entry = _writePresenceProbe(
        directory,
        variant,
        File(variant == 'before' ? snapshot : 'lib/src/transport.dart')
            .readAsStringSync(),
      );
      await _run(Platform.resolvedExecutable, [
        'compile',
        platform,
        '--packages=${File('.dart_tool/package_config.json').absolute.path}',
        entry.absolute.path,
        '-o',
        '${directory.absolute.path}/$variant.$platform',
      ]);
    }
    for (var process = 1; process <= 3; process++) {
      final variants = process.isEven
          ? ['after', 'before']
          : ['before', 'after'];
      for (final variant in variants) {
        stdout.writeln('$platform $variant process $process');
        await _run(
          platform == 'exe'
              ? '${directory.absolute.path}/$variant.exe'
              : 'node',
          platform == 'exe' ? [] : [_webRunner(directory, platform, variant)],
        );
      }
    }
  } finally {
    _cleanOwned(directory, root);
  }
}

File _writePresenceProbe(Directory directory, String variant, String source) {
  final support = File('benchmark/websocket_lifecycle_support.dart.txt')
      .readAsStringSync();
  final compare = support.indexOf('Future<void> _compareAsync(');
  final socket = support.indexOf('class _ProbeSocket ');
  if (compare < 0 || socket < compare)
    throw StateError('Unexpected shared probe layout');
  final clockNative = File('benchmark/clock_native.dart').absolute.uri;
  final clockWeb = File('benchmark/clock_web.dart').absolute.uri;
  final probe = File('benchmark/response_presence_probe.dart.txt')
      .readAsStringSync();
  return File('${directory.path}/$variant.dart')..writeAsStringSync(
    "import '$clockNative' if (dart.library.js_interop) '$clockWeb' as clock;\n"
    '${_rewriteImports(source)}\n${support.substring(0, compare)}\n${support.substring(socket)}\n$probe',
  );
}

String _rewriteImports(String source) => source.replaceAllMapped(
  RegExp("'([a-z0-9_]+\\.dart)'"),
  (match) => "'package:luxo_client/src/${match[1]}'",
);

File _writeProbe(Directory directory, String kind, String? before) {
  final source = _rewriteImports(
    File('lib/src/transport.dart').readAsStringSync(),
  );
  final benchmark = File('benchmark/uint64_benchmark.dart').absolute.uri;
  final clockNative = File('benchmark/clock_native.dart').absolute.uri;
  final clockWeb = File('benchmark/clock_web.dart').absolute.uri;
  final probe = File('benchmark/websocket_${kind}_probe.dart.txt')
      .readAsStringSync();
  final baseline = before == null ? '' : _snapshotBaseline(before);
  final support = before == null
      ? ''
      : File('benchmark/websocket_lifecycle_support.dart.txt')
            .readAsStringSync();
  return File('${directory.path}/probe.dart')..writeAsStringSync(
    "import '$benchmark' as benchmark;\n"
    "import '$clockNative' if (dart.library.js_interop) '$clockWeb' as clock;\n"
    '$source\n$baseline\n$support\n$probe',
  );
}

String _snapshotBaseline(String path) {
  final source = File(path).readAsStringSync();
  final start = source.indexOf('class _Subscription {');
  final interfaceStart = source.indexOf('abstract class Transport {');
  final interfaceEnd = source.indexOf('\nvoid _writeRequest(', interfaceStart);
  if (start < 0 || interfaceStart < 0 || interfaceEnd < 0)
    throw ArgumentError('Expected a complete earlier transport snapshot');
  final contract = source
      .substring(interfaceStart, interfaceEnd)
      .replaceFirst(
        'abstract class Transport',
        'abstract class _LegacyTransport',
      );
  final implementation = source
      .substring(start)
      .replaceAll(RegExp(r'\b_Subscription\b'), '_BeforeSubscription')
      .replaceAll(RegExp(r'\bWsTransport\b'), '_BeforeTransport')
      .replaceAll('implements Transport', 'implements _LegacyTransport')
      .replaceAll(RegExp(r'\b_writeSequence\b'), '_writeBeforeSequence');
  return '$contract\n$implementation';
}

String _webRunner(
  Directory directory,
  String platform, [
  String name = 'probe',
]) {
  final runner = File(
    '${directory.path}/run.${platform == 'js' ? 'cjs' : 'mjs'}',
  );
  runner.writeAsStringSync(
    platform == 'js'
        ? "globalThis.self = globalThis; require('./$name.js');\n"
        : "import { readFile } from 'node:fs/promises';\n"
              "import { compile } from './$name.mjs';\n"
              "const compiled = await compile(await readFile(new URL('./$name.wasm', import.meta.url)));\n"
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
