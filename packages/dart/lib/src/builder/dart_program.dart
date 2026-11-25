import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:math';

import 'native_uri.dart';

final _frontendBoundary = RegExp(r'^result ([0-9a-f-]{36})$');

/// A completed frontend process returned an unsuccessful status.
final class DartCompilerExit implements Exception {
  final int status;

  const DartCompilerExit(this.status);
}

/// Build and check the program that will execute, using the compiler's selected
/// source URIs before the SDK converts them to native paths.
Future<File> compileVerifiedDartProgram(
  Directory directory,
  File entry, {
  required bool aot,
}) async {
  _requireExactDartSource(entry.uri);
  final sdk = File(Platform.resolvedExecutable).parent.parent;
  final packages = File('${directory.path}/.dart_tool/package_config.json');
  final kernel = await _reserveKernel(entry.parent);
  final kernelName = kernel.uri.pathSegments.last;
  final result = await withDartProcess(
    '${sdk.path}/bin/dartaotruntime${Platform.isWindows ? '.exe' : ''}',
    [
      '${sdk.path}/bin/snapshots/frontend_server_aot.dart.snapshot',
      '--target',
      'vm',
      '--sdk-root',
      '${sdk.path}/lib/_internal',
      '--platform',
      aot ? 'vm_platform_product.dill' : 'vm_platform_strong.dill',
      '--packages',
      packages.uri.toString(),
      '--print-incremental-dependencies',
      '--output-dill',
      kernelName,
      '--no-aot',
      // Leave AOT transformations to dart compile exe after verifying which
      // files its product platform and environment selected.
      if (aot) ...[
        '--no-support-mirrors',
        '--no-embed-source-text',
        '-Ddart.vm.product=true',
        '-Ddart.vm.asan=false',
        '-Ddart.vm.msan=false',
        '-Ddart.vm.tsan=false',
      ],
      entry.uri.toString(),
    ],
    entry.parent,
    ProcessStartMode.normal,
    (child) async {
      final output = child.stdout.transform(systemEncoding.decoder).join();
      final errors = stderr.addStream(child.stderr);
      final status = await child.exitCode;
      await errors;
      return (status: status, output: await output);
    },
  );
  if (result.status != 0) {
    _throwCompilerExit(result.status, result.output, entry, aot: aot);
  }
  _verifySources(result.output, entry.uri, kernelName);
  return kernel;
}

Never _throwCompilerExit(
  int status,
  String output,
  File entry, {
  required bool aot,
}) {
  if (status < 0) throw DartCompilerExit(status);

  final lines = const LineSplitter().convert(output);
  final boundary = lines.isEmpty
      ? null
      : _frontendBoundary.firstMatch(lines.first)?.group(1);
  final framed =
      lines.length >= 2 && boundary != null && lines.last == boundary;
  final diagnostics = framed ? lines.sublist(1, lines.length - 1) : lines;
  for (final line in diagnostics) {
    stderr.writeln(line);
  }
  // The VM reports an absent main as 253. The batch frontend reports the
  // same condition as 254, with a single primary diagnostic and its hint.
  final entryName = entry.uri.pathSegments.last;
  if (!aot &&
      status == 254 &&
      framed &&
      diagnostics.length == 2 &&
      diagnostics[0] == "$entryName: Error: No 'main' method found." &&
      diagnostics[1] == "Try adding a method named 'main' to your program.") {
    throw const DartCompilerExit(253);
  }
  throw DartCompilerExit(status);
}

Future<File> _reserveKernel(Directory parent) async {
  // Keep the VM's native asset base and Dart compile's bin-folder detection at
  // the same directory as the original launch entry.
  final source = Random.secure();
  final id = List.generate(
    16,
    (_) => source.nextInt(256),
  ).map((value) => value.toRadixString(16).padLeft(2, '0')).join();
  return File('${parent.path}/.luxo-$id.dill').create(exclusive: true);
}

void _verifySources(String output, Uri entry, String kernel) {
  final lines = const LineSplitter().convert(output);
  final match = lines.isEmpty
      ? null
      : _frontendBoundary.firstMatch(lines.first);
  final boundary = match?.group(1);
  if (boundary == null)
    throw StateError('Dart frontend did not report dependencies');
  final start = lines.indexOf(boundary, 1);
  if (start < 0 || lines.last != '$boundary $kernel 0') {
    throw StateError(
      'Dart frontend did not confirm the compiled program:\n$output',
    );
  }
  for (var i = 1; i < start; i++) {
    stderr.writeln(lines[i]);
  }
  var foundEntry = false;
  for (var i = start + 1; i < lines.length - 1; i++) {
    final line = lines[i];
    final uri = line.startsWith('+') ? Uri.tryParse(line.substring(1)) : null;
    if (uri == null || !uri.hasScheme) {
      throw StateError('Invalid Dart frontend source URI: $line');
    }
    _requireExactDartSource(uri);
    if (uri == entry) foundEntry = true;
  }
  if (!foundEntry)
    throw StateError('Dart frontend did not report the selected entry');
}

void _requireExactDartSource(Uri uri) {
  if (uri.scheme == 'file' && nativeFileUri(uri) != uri) {
    throw StateError(
      'Dart SDK cannot resolve the exact selected source URI: $uri',
    );
  }
}

Future<T> withDartProcess<T>(
  String executable,
  List<String> arguments,
  Directory directory,
  ProcessStartMode mode,
  Future<T> Function(Process) run,
) async {
  final child = await Process.start(
    executable,
    arguments,
    workingDirectory: directory.path,
    mode: mode,
  );
  final signals = <StreamSubscription<ProcessSignal>>[];
  if (!Platform.isWindows) {
    for (final signal in [ProcessSignal.sigint, ProcessSignal.sigterm]) {
      signals.add(signal.watch().listen((signal) => child.kill(signal)));
    }
  }
  try {
    return await run(child);
  } finally {
    for (final signal in signals) {
      await signal.cancel();
    }
  }
}
