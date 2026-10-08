import 'dart:io';

import 'package:luxo_client/src/builder/selection_build.dart';
import 'package:test/test.dart';

import 'select_builder_test.dart' show generateSelectionClient;
import 'select_fixture.dart';

const _minimal = {'lib/main.dart': 'void main() {}', 'asset.txt': 'original'};

void main() {
  test(
    'release output rejects overwrites and escapes before creating directories',
    () async {
      final fixture = await SelectFixture.create(_minimal);
      final outside = await Directory('../../.tmp')
          .createTemp('dart-output-boundary-');
      SelectionBuild? build;
      try {
        build = await SelectionBuild.prepare(fixture.root);
        for (final path in [
          '',
          '../escape',
          '/escape',
          'file:///escape',
          'out?query',
          'out#fragment',
        ]) {
          await expectLater(build.prepareOutput(path), throwsArgumentError);
        }
        await expectLater(build.prepareOutput('asset.txt'), throwsStateError);
        await Link('${fixture.root.path}/linked').create(outside.absolute.path);
        await expectLater(
          build.prepareOutput('linked/new/app'),
          throwsArgumentError,
        );
        expect(await Directory('${outside.path}/new').exists(), isFalse);
        final destination = await build.prepareOutput('.tmp/output/app');
        expect(await destination.exists(), isFalse);
        expect(await destination.parent.exists(), isTrue);
        expect(
          await File('${fixture.root.path}/asset.txt').readAsString(),
          'original',
        );
      } finally {
        await build?.close();
        await fixture.close();
        await outside.delete(recursive: true);
      }
    },
  );
  test('source links and path traversal are rejected without touching outside files', () async {
    final fixture = await SelectFixture.create(_minimal);
    try {
      await expectLater(
        SelectionBuild.prepare(fixture.root, clientPath: '../escape.dart'),
        throwsArgumentError,
      );
      await Link('${fixture.root.path}/linked.txt')
          .create('${fixture.root.path}/asset.txt');
      await expectLater(SelectionBuild.prepare(fixture.root), throwsStateError);
      expect(
        await File('${fixture.root.path}/asset.txt').readAsString(),
        'original',
      );
    } finally {
      await fixture.close();
    }
  });

  test('refresh rejects replaced output links and removes only owned obsolete files', () async {
    final fixture = await SelectFixture.create(_minimal);
    SelectionBuild? build;
    try {
      final graph = File('${fixture.root.path}/.dart_tool/package_graph.json');
      await graph.writeAsString('{"packages":[]}');
      build = await SelectionBuild.prepare(fixture.root);
      expect(
        await File('${build.directory.path}/.dart_tool/package_graph.json')
            .readAsString(),
        await graph.readAsString(),
      );
      final staged = File('${build.directory.path}/asset.txt');
      await staged.delete();
      await Link(staged.path).create('${fixture.root.path}/asset.txt');
      await expectLater(build.refresh(), throwsStateError);
      expect(
        await File('${fixture.root.path}/asset.txt').readAsString(),
        'original',
      );
      await Link(staged.path).delete();
      await build.refresh();
      await File('${fixture.root.path}/asset.txt').delete();
      await build.refresh();
      expect(await staged.exists(), isFalse);
      await build.close();
      await expectLater(build.refresh(), throwsStateError);
    } finally {
      await build?.close();
      await fixture.close();
    }
  });
  test(
    'staged generated client sends per-call selections without changing source',
    () async {
      final elapsed = Stopwatch()..start();
      void phase(String name) => printOnFailure('$name: ${elapsed.elapsed}');
      final source = await File('test/fixtures/select_consumer.dart.txt')
          .readAsString();
      final fixture = await _generatedFixture(source);
      phase('fixture');
      SelectionBuild? build;
      try {
        build = await SelectionBuild.prepare(fixture.root);
        phase('prepare');
        final result = await Process.run(Platform.resolvedExecutable, [
          'bin/verify.dart',
        ], workingDirectory: build.directory.path);
        expect(
          result.exitCode,
          0,
          reason: '${result.stdout}\n${result.stderr}',
        );
        phase('execute');
        expect(
          await File('${fixture.root.path}/bin/verify.dart').readAsString(),
          source,
        );
        final staged = File('${build.directory.path}/lib/label.dart');
        expect(await staged.readAsString(), contains("select: 'name'"));
        final modified = await staged.lastModified();
        await build.refresh();
        phase('unchanged refresh');
        expect(
          await staged.lastModified(),
          modified,
          reason: 'unchanged outputs must preserve timestamps',
        );
        await File('${fixture.root.path}/lib/label.dart').writeAsString(
          fixture.sources['lib/label.dart']!.replaceAll(
            'user.name.value',
            'user.id.value.toString()',
          ),
        );
        await build.refresh();
        phase('changed refresh');
        expect(await staged.readAsString(), contains("select: 'id'"));
        final previous = await staged.readAsString();
        await File('${fixture.root.path}/lib/label.dart')
            .writeAsString('invalid Dart source');
        await expectLater(build.refresh(), throwsStateError);
        expect(
          await staged.readAsString(),
          previous,
          reason: 'failed builds must not publish transformed partial source',
        );
      } finally {
        await build?.close();
        await fixture.close();
      }
    },
  );
  test('CLI executes inferred selections without changing source', () async {
    final source = await File('test/fixtures/select_consumer.dart.txt')
        .readAsString();
    final fixture = await _generatedFixture(source);
    try {
      final cli = await Process.run(Platform.resolvedExecutable, [
        File('bin/build.dart').absolute.path,
        '--project',
        fixture.root.path,
        '--entry',
        'bin/verify.dart',
      ]);
      expect(cli.exitCode, 0, reason: '${cli.stdout}\n${cli.stderr}');
      expect(
        await File('${fixture.root.path}/bin/verify.dart').readAsString(),
        source,
      );
      final scratch = await Directory('${fixture.root.path}/.tmp')
          .list()
          .toList();
      expect(
        scratch.where((entry) => entry.path.contains('luxo-selection-')),
        isEmpty,
      );
    } finally {
      await fixture.close();
    }
  });
  test(
    'AOT artifact retains call-site selections after snapshot cleanup',
    () async {
      final source = await File('test/fixtures/select_consumer.dart.txt')
          .readAsString();
      final fixture = await _generatedFixture(source);
      try {
        final compiled = await Process.run(Platform.resolvedExecutable, [
          File('bin/build.dart').absolute.path,
          '--project',
          fixture.root.path,
          '--entry',
          'bin/verify.dart',
          '--compile',
          'exe',
          '--output',
          '.tmp/release/verify',
        ]);
        expect(
          compiled.exitCode,
          0,
          reason: '${compiled.stdout}\n${compiled.stderr}',
        );
        final artifact = File('${fixture.root.path}/.tmp/release/verify');
        expect(await artifact.exists(), isTrue);
        final release = await Process.run(artifact.path, []);
        expect(
          release.exitCode,
          0,
          reason: '${release.stdout}\n${release.stderr}',
        );
        expect(
          await File('${fixture.root.path}/bin/verify.dart').readAsString(),
          source,
        );
        final scratch = await Directory('${fixture.root.path}/.tmp')
            .list()
            .toList();
        expect(
          scratch.where((entry) => entry.path.contains('luxo-selection-')),
          isEmpty,
        );
      } finally {
        await fixture.close();
      }
    },
    timeout: const Timeout(Duration(seconds: 65)),
  );
}

Future<SelectFixture> _generatedFixture(String source) => SelectFixture.create({
  'lib/label.dart': '''
import 'src/luxo/client.dart';
Future<String> label(LuxoClient client) async {
  final user = await client.getUser();
  return user.name.value;
}
''',
  'lib/identifier.dart': '''
import 'src/luxo/client.dart';
Future<String> identifier(LuxoClient client) async {
  final user = await client.getUser();
  return user.id.value.toString();
}
''',
  'bin/verify.dart': source,
}, prepare: generateSelectionClient);
