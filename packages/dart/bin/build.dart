/// Executes a Dart entry point from an isolated, call-site-transformed snapshot.
import 'dart:io';
import 'dart:async';

import 'package:luxo_client/src/builder/selection_build.dart';

Future<void> main(List<String> arguments) async {
  SelectionBuild? build;
  try {
    final options = _options(arguments);
    build = await SelectionBuild.prepare(
      Directory(options.project),
      clientPath: options.client,
    );
    final entry = Uri.parse(options.entry);
    if (entry.hasScheme ||
        entry.hasAuthority ||
        options.entry.startsWith('/') ||
        options.entry.contains('\\') ||
        entry.pathSegments.contains('..')) {
      throw ArgumentError('Entry must be a project-relative Dart file');
    }
    final file = File.fromUri(build.directory.uri.resolveUri(entry));
    if (!file.path.endsWith('.dart') || !await file.exists()) {
      throw ArgumentError('Dart entry does not exist: ${options.entry}');
    }
    exitCode = options.compile == null
        ? await _run(build, [file.path, ...options.arguments])
        : await _compile(build, file, options.output!);
  } catch (error) {
    stderr.writeln('[luxo] $error');
    exitCode = 1;
  } finally {
    await build?.close();
  }
}

Future<int> _run(SelectionBuild build, List<String> arguments) async {
  final child = await Process.start(
    Platform.resolvedExecutable,
    arguments,
    workingDirectory: build.directory.path,
    mode: ProcessStartMode.inheritStdio,
  );
  final signals = <StreamSubscription<ProcessSignal>>[];
  if (!Platform.isWindows) {
    for (final signal in [ProcessSignal.sigint, ProcessSignal.sigterm]) {
      signals.add(signal.watch().listen((signal) => child.kill(signal)));
    }
  }
  try {
    return await child.exitCode;
  } finally {
    for (final signal in signals) {
      await signal.cancel();
    }
  }
}

Future<int> _compile(SelectionBuild build, File entry, String output) async {
  final destination = await build.prepareOutput(output);
  final artifact = File('${build.directory.path}/.tmp/luxo-release');
  await artifact.parent.create(recursive: true);
  final status = await _run(build, [
    'compile',
    'exe',
    entry.path,
    '-o',
    artifact.path,
  ]);
  if (status == 0) {
    if (await FileSystemEntity.type(destination.path, followLinks: false) !=
        FileSystemEntityType.notFound) {
      throw StateError('Output appeared during compilation: $output');
    }
    await artifact.rename(destination.path);
  }
  return status;
}

({
  String project,
  String client,
  String entry,
  String? compile,
  String? output,
  List<String> arguments,
})
_options(List<String> args) {
  var project = Directory.current.path;
  var client = 'lib/src/luxo/client.dart';
  String? entry;
  String? compile;
  String? output;
  final passed = <String>[];
  for (var i = 0; i < args.length; i++) {
    if (args[i] == '--') {
      passed.addAll(args.skip(i + 1));
      break;
    }
    if (i + 1 >= args.length)
      throw ArgumentError('Missing value for ${args[i]}');
    switch (args[i]) {
      case '--project':
        project = args[++i];
      case '--client':
        client = args[++i];
      case '--entry':
        entry = args[++i];
      case '--compile':
        compile = args[++i];
      case '--output':
        output = args[++i];
      default:
        throw ArgumentError('Unknown option: ${args[i]}');
    }
  }
  if (entry == null)
    throw ArgumentError(
      'Usage: dart run luxo_client:build --entry bin/main.dart [-- arguments]',
    );
  if ((compile != null &&
          (compile != 'exe' ||
              output == null ||
              output.isEmpty ||
              passed.isNotEmpty)) ||
      (compile == null && output != null)) {
    throw ArgumentError(
      'Use --compile exe --output <project-relative file>, without runtime arguments',
    );
  }
  return (
    project: project,
    client: client,
    entry: entry,
    compile: compile,
    output: output,
    arguments: passed,
  );
}
