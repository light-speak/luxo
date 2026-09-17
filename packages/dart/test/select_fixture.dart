import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:analyzer/dart/analysis/analysis_context_collection.dart';
import 'package:analyzer/dart/analysis/results.dart';
import 'package:analyzer/dart/ast/ast.dart';
import 'package:analyzer/dart/element/element.dart';
import 'package:analyzer/diagnostic/diagnostic.dart';
import 'package:build/build.dart';
import 'package:glob/glob.dart';
import 'package:test/test.dart';

/// Uses real resolved Dart libraries without starting a build daemon.
class SelectFixture implements Resolver {
  final Directory root;
  final Map<String, String> sources;
  final AnalysisContextCollection collection;
  final Map<String, ResolvedUnitResult> _units = {};
  int resolvedReads = 0;
  bool omitAST = false;

  SelectFixture._(this.root, this.sources, this.collection);

  static Future<SelectFixture> create(
    Map<String, String> sources, {
    Future<void> Function(Directory)? prepare,
  }) async {
    final temp = Directory('../../.tmp');
    await temp.create(recursive: true);
    final root = await Directory(await temp.resolveSymbolicLinks())
        .createTemp('dart-select-build-');
    try {
      for (final entry in sources.entries) {
        final file = File('${root.path}/${entry.key}');
        await file.parent.create(recursive: true);
        await file.writeAsString(entry.value);
      }
      await _writePackageConfig(root);
      await prepare?.call(root);
      final assets = <String, String>{};
      await for (final file in Directory(
        '${root.path}/lib',
      ).list(recursive: true)) {
        if (file is File && file.path.endsWith('.dart')) {
          assets[file.path.substring(root.path.length + 1)] = await file
              .readAsString();
        }
      }
      final collection = AnalysisContextCollection(
        includedPaths: ['${root.path}/lib'],
      );
      return SelectFixture._(root, assets, collection);
    } catch (_) {
      await root.delete(recursive: true);
      rethrow;
    }
  }

  static Future<void> _writePackageConfig(Directory root) async {
    final current = File('.dart_tool/package_config.json').absolute;
    final config =
        jsonDecode(await current.readAsString()) as Map<String, dynamic>;
    final packages = config['packages'] as List<dynamic>;
    for (final entry in packages.cast<Map<String, dynamic>>()) {
      entry['rootUri'] = current.uri
          .resolve(entry['rootUri'] as String)
          .toString();
    }
    packages.add({
      'name': 'fixture',
      'rootUri': root.uri.toString(),
      'packageUri': 'lib/',
      'languageVersion': '3.13',
    });
    final file = File('${root.path}/.dart_tool/package_config.json');
    await file.parent.create(recursive: true);
    await file.writeAsString(jsonEncode(config));
  }

  Future<ResolvedUnitResult> unit(String path) async {
    final absolute = '${root.path}/$path';
    final session = collection.contextFor(absolute).currentSession;
    final result = await session.getResolvedUnit(absolute);
    expect(result, isA<ResolvedUnitResult>());
    final resolved = result as ResolvedUnitResult;
    expect(
      resolved.diagnostics.where((d) => d.severity == Severity.error),
      isEmpty,
    );
    return resolved;
  }

  @override
  Future<bool> isLibrary(AssetId id) async => !(await unit(id.path)).isPart;

  @override
  Future<LibraryElement> libraryFor(
    AssetId id, {
    bool allowSyntaxErrors = false,
  }) async {
    final path = '${root.path}/${id.path}';
    final session = collection.contextFor(path).currentSession;
    final result = await session.getResolvedLibrary(path);
    expect(result, isA<ResolvedLibraryResult>());
    final library = result as ResolvedLibraryResult;
    for (final unit in library.units) {
      expect(
        unit.diagnostics.where((d) => d.severity == Severity.error),
        isEmpty,
      );
      _units[unit.path] = unit;
    }
    return library.element;
  }

  @override
  Future<AstNode?> astNodeFor(Fragment fragment, {bool resolve = false}) async {
    expect(resolve, isTrue, reason: 'Selection needs resolved static types');
    resolvedReads++;
    if (omitAST) return null;
    return _units[(fragment as LibraryFragment).source.fullName]!.unit;
  }

  Future<void> close() async {
    await collection.dispose();
    await root.delete(recursive: true);
  }

  Future<void> runDart(List<String> arguments) async {
    final process = await Process.start(
      Platform.resolvedExecutable,
      arguments,
      workingDirectory: root.path,
    );
    final stdout = process.stdout.transform(utf8.decoder).join();
    final stderr = process.stderr.transform(utf8.decoder).join();
    final timer = Timer(
      const Duration(seconds: 60),
      () => process.kill(ProcessSignal.sigkill),
    );
    try {
      final status = await process.exitCode;
      expect(status, 0, reason: '${await stdout}\n${await stderr}');
    } finally {
      timer.cancel();
    }
  }

  @override
  dynamic noSuchMethod(Invocation invocation) => throw UnsupportedError(
    'Unexpected resolver call: ${invocation.memberName}',
  );
}

class SelectBuildStep implements BuildStep {
  @override
  final SelectFixture resolver;
  final Map<AssetId, String> outputs = {};

  SelectBuildStep(this.resolver);

  @override
  AssetId get inputId => AssetId('fixture', r'lib/$lib$');

  @override
  Stream<AssetId> findAssets(Glob glob) async* {
    for (final path in resolver.sources.keys) {
      if (glob.matches(path)) yield AssetId('fixture', path);
    }
  }

  @override
  Future<String> readAsString(AssetId id, {Encoding encoding = utf8}) async =>
      resolver.sources[id.path]!;

  @override
  Future<void> writeAsString(
    AssetId id,
    FutureOr<String> contents, {
    Encoding encoding = utf8,
  }) async {
    outputs[id] = await contents;
  }

  @override
  dynamic noSuchMethod(Invocation invocation) =>
      throw UnsupportedError('Unexpected build call: ${invocation.memberName}');
}
