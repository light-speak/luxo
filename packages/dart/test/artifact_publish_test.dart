import 'dart:io';

import 'package:luxo_client/src/builder/artifact_publish.dart';
import 'package:test/test.dart';

Future<Directory> project() async {
  final scratch = Directory('.tmp');
  await scratch.create(recursive: true);
  final root = await scratch.createTemp('prepared-output-');
  addTearDown(() => root.delete(recursive: true));
  return Directory(await root.resolveSymbolicLinks());
}

void main() {
  test('prepared publication preserves the missing artifact error', () async {
    final root = await project();
    final output = await PreparedOutput.prepare(root, 'release/app');
    addTearDown(output.close);
    expect(
      () => publishBuildArtifact(File('${root.path}/missing'), output),
      throwsA(
        isA<FileSystemException>().having(
          (e) => e.osError?.errorCode,
          'error code',
          2,
        ),
      ),
    );
    expect(await output.destination.exists(), isFalse);
  });

  test(
    'prepared publication creates ordinary parents and keeps file bytes',
    () async {
      final root = await project();
      final artifact = File('${root.path}/staged');
      await artifact.writeAsString('compiled artifact');
      final output = await PreparedOutput.prepare(root, 'release/nested/应用');
      addTearDown(output.close);
      expect(await output.destination.exists(), isFalse);
      expect(await output.destination.parent.exists(), isTrue);
      publishBuildArtifact(artifact, output);
      expect(await output.destination.readAsString(), 'compiled artifact');
      expect(() => publishBuildArtifact(artifact, output), throwsStateError);
      output.close();
      output.close();
    },
  );

  test(
    'prepared publication preserves ordinary in-project directory aliases',
    () async {
      final root = await project();
      final real = Directory('${root.path}/real');
      await real.create();
      for (final absolute in [false, true]) {
        final name = absolute ? 'absolute' : 'relative';
        await Link('${root.path}/$name').create(absolute ? real.path : 'real');
        final artifact = File('${root.path}/staged-$name');
        await artifact.writeAsString('artifact-$name');
        final output = await PreparedOutput.prepare(
          root,
          '$name/nested/app-$name',
        );
        try {
          publishBuildArtifact(artifact, output);
          expect(
            await File('${real.path}/nested/app-$name').readAsString(),
            'artifact-$name',
          );
        } finally {
          output.close();
        }
      }
    },
  );

  test(
    'two prepared publishers never replace the first completed artifact',
    () async {
      final root = await project();
      final first = await PreparedOutput.prepare(root, 'release/app');
      final second = await PreparedOutput.prepare(root, 'release/app');
      addTearDown(first.close);
      addTearDown(second.close);
      final a = File('${root.path}/first');
      final b = File('${root.path}/second');
      await a.writeAsString('first build');
      await b.writeAsString('second build');
      publishBuildArtifact(a, first);
      expect(() => publishBuildArtifact(b, second), throwsStateError);
      expect(await first.destination.readAsString(), 'first build');
      expect(await b.readAsString(), 'second build');
    },
  );

  test(
    'closed prepared output rejects publication without changing the artifact',
    () async {
      final root = await project();
      final artifact = File('${root.path}/staged');
      await artifact.writeAsString('compiled artifact');
      final output = await PreparedOutput.prepare(root, 'release/app');
      output.close();
      expect(() => publishBuildArtifact(artifact, output), throwsStateError);
      expect(await artifact.readAsString(), 'compiled artifact');
      expect(await output.destination.exists(), isFalse);
    },
  );

  test(
    'prepared output retains normal Unicode and long directory names',
    () async {
      final root = await project();
      final path = List.filled(3, 'directory-${'x' * 70}').join('/');
      final artifact = File('${root.path}/staged');
      await artifact.writeAsBytes([0, 1, 127, 255]);
      final output = await PreparedOutput.prepare(root, '$path/应用-\u{20000}');
      addTearDown(output.close);
      publishBuildArtifact(artifact, output);
      expect(await output.destination.readAsBytes(), [0, 1, 127, 255]);
    },
  );
  test(
    'prepared output accepts a root-level file and normalized relative names',
    () async {
      final root = await project();
      for (final name in ['app', './release//app']) {
        final output = await PreparedOutput.prepare(root, name);
        final artifact = File('${root.path}/staged');
        await artifact.writeAsString(name);
        try {
          publishBuildArtifact(artifact, output);
          expect(await output.destination.readAsString(), name);
        } finally {
          output.close();
        }
      }
    },
  );

  test(
    'prepared output preserves a project selected through its directory alias',
    () async {
      final root = await project();
      final alias = Link('${root.path}-project-alias');
      await alias.create(root.path);
      addTearDown(alias.delete);
      final output = await PreparedOutput.prepare(
        Directory(alias.path),
        'release/app',
      );
      addTearDown(output.close);
      final artifact = File('${root.path}/staged');
      await artifact.writeAsString('project alias');
      publishBuildArtifact(artifact, output);
      expect(
        await File('${root.path}/release/app').readAsString(),
        'project alias',
      );
    },
  );

  test('prepared output keeps an existing ordinary file unchanged', () async {
    final root = await project();
    final original = File('${root.path}/existing');
    await original.writeAsString('original');
    await expectLater(
      PreparedOutput.prepare(root, 'existing'),
      throwsStateError,
    );
    expect(await original.readAsString(), 'original');
    final valid = await PreparedOutput.prepare(root, 'release/app');
    valid.close();
  });
}
