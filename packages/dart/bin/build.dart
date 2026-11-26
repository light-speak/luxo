/// Executes a Dart entry point from an isolated, call-site-transformed snapshot.
import 'dart:io';

import 'package:luxo_client/src/builder/artifact_publish.dart';
import 'package:luxo_client/src/builder/dart_program.dart';
import 'package:luxo_client/src/builder/native_uri.dart';
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
    final file = File.fromUri(
      nativeFileUri(build.directory.uri.resolveUri(entry)),
    );
    if (!file.path.endsWith('.dart') || !await file.exists()) {
      throw ArgumentError('Dart entry does not exist: ${options.entry}');
    }
    exitCode = options.compile == null
        ? await _launch(build, file, options.arguments)
        : await _compile(build, file, options.output!);
  } on DartCompilerExit catch (failure) {
    exitCode = failure.status;
  } catch (error) {
    stderr.writeln('[luxo] $error');
    exitCode = 1;
  } finally {
    await build?.close();
  }
}

Future<int> _run(SelectionBuild build, List<String> arguments) =>
    withDartProcess(
      Platform.resolvedExecutable,
      arguments,
      build.directory,
      ProcessStartMode.inheritStdio,
      (child) => child.exitCode,
    );

Future<int> _launch(
  SelectionBuild build,
  File entry,
  List<String> arguments,
) async {
  final kernel = await compileVerifiedDartProgram(
    build.directory,
    entry,
    aot: false,
  );
  return _run(build, [
    '--script-uri-override=${entry.uri}',
    kernel.uri.toString(),
    ...arguments,
  ]);
}

Future<int> _compile(SelectionBuild build, File entry, String output) async {
  final destination = await build.prepareOutput(output);
  try {
    final artifact = File('${build.directory.path}/.tmp/luxo-release');
    await artifact.parent.create(recursive: true);
    final kernel = await compileVerifiedDartProgram(
      build.directory,
      entry,
      aot: true,
    );
    final status = await _run(build, [
      'compile',
      'exe',
      kernel.path,
      '-o',
      artifact.path,
    ]);
    if (status == 0) publishBuildArtifact(artifact, destination);
    return status;
  } finally {
    destination.close();
  }
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
