/// Runs Flutter's machine protocol against an isolated selection build.
import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:luxo_client/src/builder/flutter_commands.dart';
import 'package:luxo_client/src/builder/selection_build.dart';

Future<void> main(List<String> arguments) async {
  SelectionBuild? build;
  try {
    final options = _options(arguments);
    build = await SelectionBuild.prepare(
      Directory(options.project),
      clientPath: options.client,
    );
    exitCode = await _run(build, options.flutter, options.arguments);
  } catch (error) {
    stderr.writeln('[luxo] $error');
    exitCode = 1;
  } finally {
    await build?.close();
  }
}

Future<int> _run(
  SelectionBuild build,
  String flutter,
  List<String> arguments,
) async {
  final child = await Process.start(flutter, [
    'run',
    '--machine',
    '--no-pub',
    ...arguments,
  ], workingDirectory: build.directory.path);
  final output = child.stdout.listen(stdout.add).asFuture<void>();
  final errors = child.stderr.listen(stderr.add).asFuture<void>();
  final commands = StreamController<String>();
  final input = stdin
      .transform(utf8.decoder)
      .transform(const LineSplitter())
      .listen(commands.add, onError: commands.addError, onDone: commands.close);
  final forwarding = _forwardInput(commands.stream, build, child.stdin);
  final signals = <StreamSubscription<ProcessSignal>>[];
  if (!Platform.isWindows) {
    for (final signal in [ProcessSignal.sigint, ProcessSignal.sigterm]) {
      signals.add(signal.watch().listen((signal) => child.kill(signal)));
    }
  }
  try {
    final code = await child.exitCode;
    await Future.wait([output, errors]);
    return code;
  } finally {
    await input.cancel();
    if (!commands.isClosed) await commands.close();
    await forwarding;
    for (final signal in signals) {
      await signal.cancel();
    }
  }
}

Future<void> _forwardInput(
  Stream<String> commands,
  SelectionBuild build,
  IOSink target,
) async {
  try {
    await forwardFlutterCommands(
      commands,
      refresh: build.refresh,
      send: target.writeln,
      respond: stdout.writeln,
    );
  } finally {
    await target.close();
  }
}

({String project, String client, String flutter, List<String> arguments})
_options(List<String> args) {
  var project = Directory.current.path;
  var client = 'lib/src/luxo/client.dart';
  var flutter = 'flutter';
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
      case '--flutter':
        flutter = args[++i];
      default:
        throw ArgumentError('Unknown option: ${args[i]}');
    }
  }
  if (passed.any((arg) => arg == '--pub' || arg == '--no-machine')) {
    throw ArgumentError('Staged execution requires --no-pub and --machine');
  }
  return (
    project: project,
    client: client,
    flutter: flutter,
    arguments: passed,
  );
}
