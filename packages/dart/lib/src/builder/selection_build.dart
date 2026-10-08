import 'dart:convert';
import 'dart:io';

import 'package:analyzer/dart/analysis/analysis_context_collection.dart';
import 'package:analyzer/dart/analysis/results.dart';
import 'package:analyzer/diagnostic/diagnostic.dart';

import 'select_rewrite.dart';

/// An owned build snapshot. Source files are never written by this class.
class SelectionBuild {
  final Directory source;
  final Directory directory;
  final String clientPath;
  final Set<String> _outputs = {};
  bool _closed = false;
  bool _refreshing = false;

  SelectionBuild._(this.source, this.directory, this.clientPath);

  /// Reserve no file; validate the destination before invoking the release compiler.
  Future<File> prepareOutput(String output) async {
    if (_closed) throw StateError('Build is closed');
    _relativePath(output);
    final destination = File.fromUri(source.uri.resolve(output));
    if (await FileSystemEntity.type(destination.path, followLinks: false) !=
        FileSystemEntityType.notFound) {
      throw StateError('Output already exists: $output');
    }
    var ancestor = destination.parent;
    while (!await ancestor.exists()) {
      if (ancestor.path == ancestor.parent.path)
        throw ArgumentError('Invalid output parent');
      ancestor = ancestor.parent;
    }
    final parent = await ancestor.resolveSymbolicLinks();
    if (parent != source.path &&
        !parent.startsWith('${source.path}${Platform.pathSeparator}')) {
      throw ArgumentError('Output escapes the project');
    }
    await destination.parent.create(recursive: true);
    return destination;
  }

  static Future<SelectionBuild> prepare(
    Directory source, {
    String clientPath = 'lib/src/luxo/client.dart',
  }) async {
    final root = Directory(await source.resolveSymbolicLinks());
    _relativePath(clientPath);
    final temp = Directory('${root.path}/.tmp');
    await temp.create(recursive: true);
    if (await temp.resolveSymbolicLinks() != temp.absolute.path) {
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
    if (_closed || _refreshing)
      throw StateError('Build is closed or already refreshing');
    _refreshing = true;
    try {
      await _refresh();
    } finally {
      _refreshing = false;
    }
  }

  Future<void> _refresh() async {
    if (await directory.resolveSymbolicLinks() != directory.absolute.path) {
      throw StateError('Build output was replaced by a symlink');
    }
    final files = await _sourceFiles(source);
    final config = await _packageConfig();
    final client = Uri.parse(
      'package:${config.name}/${clientPath.substring(4)}',
    );
    final analysis = AnalysisContextCollection(includedPaths: [source.path]);
    final prepared = <String, List<int>>{};
    try {
      for (final file in files) {
        final relative = source.uri.relativize(file.uri);
        prepared[relative] = await _transformFile(
          file,
          relative,
          analysis,
          client,
        );
      }
    } finally {
      await analysis.dispose();
    }
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
      rewriteSelections(result.unit, original, clientLibrary: client),
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
        package['rootUri'] = directory.uri.toString();
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
    _relativePath(relative);
    final segments = Uri.parse(relative).pathSegments;
    var current = directory.uri;
    for (var i = 0; i < segments.length; i++) {
      current = current.resolveUri(
        Uri(pathSegments: [segments[i], if (i < segments.length - 1) '']),
      );
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

  Future<void> close() async {
    if (_refreshing) throw StateError('Cannot close while preparing a build');
    if (_closed) return;
    final expectedParent = Directory('${source.path}/.tmp');
    if (directory.parent.path != expectedParent.path ||
        await directory.resolveSymbolicLinks() != directory.absolute.path) {
      throw StateError('Refusing to remove an unowned build directory');
    }
    await directory.delete(recursive: true);
    _closed = true;
  }
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

Future<List<File>> _sourceFiles(Directory root) async {
  final files = <File>[];
  Future<void> scan(Directory directory) async {
    await for (final entry in directory.list(followLinks: false)) {
      final name = entry.uri.pathSegments.where((p) => p.isNotEmpty).last;
      if (_excludedDirectories.contains(name)) continue;
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

void _relativePath(String path) {
  final uri = Uri.parse(path);
  if (uri.hasScheme ||
      uri.hasAuthority ||
      uri.hasQuery ||
      uri.hasFragment ||
      path.startsWith('/') ||
      path.contains('\\') ||
      uri.pathSegments.any((p) => p == '..') ||
      path.isEmpty) {
    throw ArgumentError('Expected a project-relative path: $path');
  }
}

extension on Uri {
  String relativize(Uri child) {
    if (!child.toString().startsWith(toString()))
      throw StateError('Source outside project');
    return child.toString().substring(toString().length);
  }
}
