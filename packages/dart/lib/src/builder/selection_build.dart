import 'dart:convert';
import 'dart:io';

import 'package:analyzer/dart/analysis/analysis_context_collection.dart';
import 'package:analyzer/dart/analysis/results.dart';
import 'package:analyzer/diagnostic/diagnostic.dart';

import 'artifact_publish.dart';
import 'native_uri.dart';
import 'select_rewrite.dart';
import 'selection_imports.dart';

/// An owned build snapshot. Source files are never written by this class.
class SelectionBuild {
  final Directory source;
  final Directory directory;
  final String clientPath;
  final Set<String> _outputs = {};
  Set<PreparedOutput>? _preparedOutputs;
  bool _closed = false;
  bool _closing = false;
  bool _refreshing = false;

  SelectionBuild._(this.source, this.directory, this.clientPath);

  /// Keep the validated output directory open until the build is finished.
  Future<PreparedOutput> prepareOutput(String output) async {
    if (_closed || _closing) throw StateError('Build is closed');
    final prepared = await PreparedOutput.prepare(source, output);
    try {
      if (_closed || _closing) throw StateError('Build is closed');
      (_preparedOutputs ??= {}).add(prepared);
      return prepared;
    } catch (_) {
      prepared.close();
      rethrow;
    }
  }

  static Future<SelectionBuild> prepare(
    Directory source, {
    String clientPath = 'lib/src/luxo/client.dart',
  }) async {
    var root = Directory(await source.resolveSymbolicLinks());
    final path = root.path;
    if (path.endsWith(Platform.pathSeparator) && root.parent.path != path) {
      root = Directory(path.substring(0, path.length - 1));
    }
    validateProjectRelativePath(clientPath);
    final temp = Directory.fromUri(nativeFileUri(root.uri.resolve('.tmp')));
    await temp.create(recursive: true);
    if (!await _resolvesToOwnPath(temp)) {
      throw StateError('The build directory must not be a symlink');
    }
    final output = await temp.createTemp('luxo-selection-');
    final build = SelectionBuild._(root, output, clientPath);
    try {
      await build.refresh();
      return build;
    } catch (_) {
      await build.close();
      rethrow;
    }
  }

  /// Re-resolve the current source snapshot; do not reuse stale analyzer sessions.
  Future<void> refresh() async {
    if (_closed || _closing || _refreshing)
      throw StateError('Build is closed or already refreshing');
    _refreshing = true;
    try {
      await _refresh();
    } finally {
      _refreshing = false;
    }
  }

  Future<void> _refresh() async {
    if (!await _resolvesToOwnPath(directory)) {
      throw StateError('Build output was replaced by a symlink');
    }
    final files = await _sourceFiles(source);
    final config = await _packageConfig();
    final client = Uri.parse(
      'package:${config.name}/${clientPath.substring(4)}',
    );
    final analysis = AnalysisContextCollection(includedPaths: [source.path]);
    final prepared = <String, List<int>>{};
    final imports = SelectionImports();
    try {
      for (final file in files) {
        final relative = source.uri.relativize(file.uri);
        prepared[relative] = await _transformFile(
          file,
          relative,
          analysis,
          client,
          imports,
        );
      }
    } finally {
      await analysis.dispose();
    }
    imports.apply(prepared, source.uri);
    prepared['.dart_tool/package_config.json'] = utf8.encode(
      jsonEncode(config.value),
    );
    final graph = File('${source.path}/.dart_tool/package_graph.json');
    if (await graph.exists()) {
      prepared['.dart_tool/package_graph.json'] = await graph.readAsBytes();
    }
    // No output is changed until every source has resolved and transformed successfully.
    for (final entry in prepared.entries) {
      await _writeIfChanged(entry.key, entry.value);
    }
    for (final removed in _outputs.difference(prepared.keys.toSet())) {
      await (await _target(removed)).delete();
    }
    _outputs
      ..clear()
      ..addAll(prepared.keys);
  }

  Future<List<int>> _transformFile(
    File file,
    String relative,
    AnalysisContextCollection analysis,
    Uri client,
    SelectionImports imports,
  ) async {
    final bytes = await file.readAsBytes();
    if (!file.path.endsWith('.dart') || file.path.endsWith('.g.dart'))
      return bytes;
    final session = analysis.contextFor(file.path).currentSession;
    final result = await session.getResolvedUnit(file.path);
    if (result is! ResolvedUnitResult)
      throw StateError('Cannot resolve $relative');
    final errors = result.diagnostics.where(
      (d) => d.severity == Severity.error,
    );
    if (errors.isNotEmpty)
      throw StateError('Invalid source $relative: ${errors.join('; ')}');
    final original = utf8.decode(bytes);
    if (result.content != original)
      throw StateError('Source changed during analysis: $relative');
    return utf8.encode(
      rewriteSelections(
        result.unit,
        original,
        clientLibrary: client,
        library: result.libraryElement,
        hiddenFactory: imports.factory,
      ),
    );
  }

  Future<({String name, Map<String, dynamic> value})> _packageConfig() async {
    if (!clientPath.startsWith('lib/'))
      throw ArgumentError('Client must be under lib/');
    final file = File('${source.path}/.dart_tool/package_config.json');
    final config =
        jsonDecode(await file.readAsString()) as Map<String, dynamic>;
    final packages = (config['packages'] as List).cast<Map<String, dynamic>>();
    String? name;
    for (final package in packages) {
      final original = file.uri.resolve(package['rootUri'] as String);
      if (original == source.uri) {
        name = package['name'] as String;
        package['rootUri'] = nativeFileUri(directory.uri).toString();
      } else {
        package['rootUri'] = original.toString();
      }
    }
    if (name == null)
      throw StateError('Package config does not identify the source project');
    return (name: name, value: config);
  }

  Future<void> _writeIfChanged(String relative, List<int> bytes) async {
    final target = await _target(relative);
    if (await target.exists()) {
      final previous = await target.readAsBytes();
      if (_sameBytes(previous, bytes)) return;
    }
    await target.parent.create(recursive: true);
    await target.writeAsBytes(bytes, flush: true);
  }

  Future<File> _target(String relative) async {
    validateProjectRelativePath(relative);
    final segments = nativeFileUri(Uri.parse(relative)).pathSegments;
    var current = nativeFileUri(directory.uri);
    for (var i = 0; i < segments.length; i++) {
      final part = Uri(
        pathSegments: [segments[i], if (i < segments.length - 1) ''],
      );
      current = current.resolveUri(nativeFileUri(part));
      if (await FileSystemEntity.type(
            current.toFilePath(),
            followLinks: false,
          ) ==
          FileSystemEntityType.link) {
        throw StateError('Build output contains a symlink: $relative');
      }
    }
    return File.fromUri(current);
  }

  void _closePreparedOutputs() {
    final outputs = _preparedOutputs;
    _preparedOutputs = null;
    if (outputs == null) return;
    final errors = <Object>[];
    for (final output in outputs) {
      try {
        output.close();
      } catch (error) {
        errors.add(error);
      }
    }
    if (errors.length == 1) throw errors.single;
    if (errors.isNotEmpty)
      throw StateError('Could not close prepared outputs: $errors');
  }

  Future<void> close() async {
    if (_refreshing) throw StateError('Cannot close while preparing a build');
    if (_closed) return;
    if (_closing) throw StateError('Build is already closing');
    _closing = true;
    Object? outputError;
    StackTrace? outputStack;
    try {
      try {
        _closePreparedOutputs();
      } catch (error, stack) {
        outputError = error;
        outputStack = stack;
      }
      final expectedParent = source.uri.resolve('.tmp/');
      if (directory.parent.uri != expectedParent ||
          !await _resolvesToOwnPath(directory)) {
        throw StateError('Refusing to remove an unowned build directory');
      }
      await directory.delete(recursive: true);
      _closed = true;
    } catch (error) {
      if (outputError != null) {
        throw StateError('Could not close build: $outputError; $error');
      }
      rethrow;
    } finally {
      _closing = false;
    }
    if (outputError != null) {
      Error.throwWithStackTrace(outputError, outputStack!);
    }
  }
}

Future<bool> _resolvesToOwnPath(Directory directory) async {
  final resolved = await directory.resolveSymbolicLinks();
  return resolved == directory.absolute.path ||
      Uri.directory(resolved) == directory.uri;
}

const _excludedDirectories = {
  '.git',
  '.tmp',
  '.dart_tool',
  'build',
  '.idea',
  '.gradle',
  '.build',
  'node_modules',
};

bool _isFlutterGenerated(String relative) {
  final path = relative.endsWith('/')
      ? relative.substring(0, relative.length - 1)
      : relative;
  return const {
    'ios/.symlinks',
    'ios/Pods',
    'ios/Flutter/ephemeral',
    'macos/.symlinks',
    'macos/Pods',
    'macos/Flutter/ephemeral',
    'linux/flutter/ephemeral',
    'windows/flutter/ephemeral',
  }.contains(path);
}

Future<List<File>> _sourceFiles(Directory root) async {
  final files = <File>[];
  Future<void> scan(Directory directory) async {
    await for (final entry in directory.list(followLinks: false)) {
      final name = nativeFileUri(entry.uri).pathSegments
          .where((p) => p.isNotEmpty)
          .last;
      if (_excludedDirectories.contains(name) ||
          _isFlutterGenerated(root.uri.relativize(entry.uri)))
        continue;
      if (entry is Link)
        throw StateError(
          'Source symlinks require explicit build configuration: ${entry.path}',
        );
      if (entry is Directory) await scan(entry);
      if (entry is File) files.add(entry);
    }
  }

  await scan(root);
  files.sort((a, b) => a.path.compareTo(b.path));
  return files;
}

bool _sameBytes(List<int> a, List<int> b) {
  if (a.length != b.length) return false;
  for (var i = 0; i < a.length; i++) {
    if (a[i] != b[i]) return false;
  }
  return true;
}

extension on Uri {
  String relativize(Uri child) {
    if (!child.toString().startsWith(toString()))
      throw StateError('Source outside project');
    return child.toString().substring(toString().length);
  }
}
