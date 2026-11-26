import 'dart:convert';
import 'dart:io';

import 'package:test/test.dart';

import 'select_fixture.dart';

const _output = '.tmp/release/verify.exe';
const _launcherTimeout = Timeout(Duration(seconds: 65));
const _literalEntry = 'entry-%EF%BB%BF-中文.g.dart';
const _ordinaryEntry = 'choice.g.dart';
const _markedEntry = '\uFEFFchoice.g.dart';
const _unicodeNoMain = 'no_main_中文.g.dart';
const _compilerFailures = {
  'no_main.g.dart': 'int selected() => 1;',
  _unicodeNoMain: 'int selected() => 1;',
  'syntax_error.g.dart': 'void main() { final int value = ; } // 中文',
  'missing_import.g.dart': "import 'missing.dart'; void main() {}",
};
const _entrySource = r'''
import 'dart:io';
void main(List<String> args) {
  print('literal|${File('asset.txt').readAsStringSync()}|${args.join(' ')}');
}
''';

void main() {
  for (final aot in [false, true]) {
    final mode = aot ? 'AOT' : 'JIT';
    for (final source in _compilerFailures.entries) {
      test(
        'launcher $mode preserves compiler exit for ${source.key}',
        () async {
          final fixture = await SelectFixture.create({
            'lib/${source.key}': source.value,
          });
          addTearDown(fixture.close);
          final launched = await Process.run(
            Platform.resolvedExecutable,
            [
              File('bin/build.dart').absolute.path,
              '--project',
              fixture.root.path,
              '--entry',
              'lib/${Uri.encodeComponent(source.key)}',
              if (aot) ...['--compile', 'exe', '--output', _output],
            ],
            stdoutEncoding: utf8,
            stderrEncoding: utf8,
          );
          final missingMain =
              source.key == 'no_main.g.dart' || source.key == _unicodeNoMain;
          final exit = !aot && missingMain ? 253 : 254;
          expect(launched.exitCode, exit, reason: _diagnostics(launched));
          expect(launched.stdout, isEmpty);
          expect(launched.stderr, contains('Error:'));
          if (source.key == _unicodeNoMain) {
            final renderedName = Uri.encodeComponent(_unicodeNoMain);
            expect(
              launched.stderr,
              contains("$renderedName: Error: No 'main' method found."),
            );
          }
          if (source.key == 'syntax_error.g.dart') {
            expect(launched.stderr, contains('// 中文'));
          }
          expect(await File('${fixture.root.path}/$_output').exists(), isFalse);
          await _expectSnapshotClean(fixture);
        },
        timeout: _launcherTimeout,
      );
    }
    test('launcher $mode preserves a literal escaped entry and cwd', () async {
      await _testLauncher(
        aot: aot,
        entry: _literalEntry,
        sources: {'lib/$_literalEntry': _entrySource},
        expected: 'literal|fixture asset|--option 中文 %',
        runtimeArgs: ['--option', '中文 %'],
      );
    }, timeout: _launcherTimeout);

    test('launcher $mode guards a selected marked entry', () async {
      await _testLauncher(
        aot: aot,
        entry: _markedEntry,
        sources: {
          'lib/$_ordinaryEntry': "void main() => print('ordinary');",
          'lib/$_markedEntry': "void main() => print('marked');",
        },
        expected: 'marked',
        selectedMarkedPath: Uri.encodeComponent(_markedEntry),
      );
    }, timeout: _launcherTimeout);

    for (final directive in {
      'active':
          "import '$_ordinaryEntry' if (dart.library.io) '%EF%BB%BF$_ordinaryEntry';",
      'inactive':
          "import '$_ordinaryEntry' if (dart.library.html) '%EF%BB%BF$_ordinaryEntry';",
      'overridden':
          "import '%EF%BB%BF$_ordinaryEntry' if (dart.library.io) '$_ordinaryEntry';",
      'mirrors':
          "import '$_ordinaryEntry' if (dart.library.mirrors) '%EF%BB%BF$_ordinaryEntry';",
    }.entries) {
      final active =
          directive.key == 'active' || (directive.key == 'mirrors' && !aot);
      test(
        'launcher $mode respects ${directive.key} conditional selection',
        () async {
          await _testLauncher(
            aot: aot,
            entry: 'main.g.dart',
            sources: {
              'lib/main.g.dart':
                  '${directive.value}\nvoid main() => print(value);',
              'lib/$_ordinaryEntry': "const value = 'ordinary';",
              'lib/$_markedEntry': "const value = 'marked';",
            },
            expected: active ? 'marked' : 'ordinary',
            selectedMarkedPath: active
                ? Uri.encodeComponent(_markedEntry)
                : null,
          );
        },
        timeout: _launcherTimeout,
      );
    }
  }
}

Future<void> _testLauncher({
  required bool aot,
  required String entry,
  required Map<String, String> sources,
  required String expected,
  String? selectedMarkedPath,
  List<String> runtimeArgs = const [],
}) async {
  final fixture = await SelectFixture.create(
    {'lib/main.dart': 'void unrelated() {}', 'asset.txt': 'fixture asset'},
    prepare: (root) async {
      for (final source in sources.entries) {
        final file = File('${root.path}/${source.key}');
        await file.create(exclusive: true);
        await file.writeAsString(source.value);
      }
    },
  );
  addTearDown(fixture.close);
  final artifact = File('${fixture.root.path}/$_output');
  // The fixture prints UTF-8 on every host.
  final launched = await Process.run(Platform.resolvedExecutable, [
    File('bin/build.dart').absolute.path,
    '--project',
    fixture.root.path,
    '--entry',
    'lib/${Uri.encodeComponent(entry)}',
    if (aot) ...['--compile', 'exe', '--output', _output],
    if (!aot && runtimeArgs.isNotEmpty) ...['--', ...runtimeArgs],
  ], stdoutEncoding: utf8);

  final uriDropsBom = Uri.parse('%EF%BB%BFx').pathSegments.single == 'x';
  if (selectedMarkedPath != null && uriDropsBom) {
    expect(launched.exitCode, isNot(0), reason: _diagnostics(launched));
    expect(launched.stderr, contains(selectedMarkedPath));
    expect(launched.stdout, isNot(contains('ordinary')));
    expect(await artifact.exists(), isFalse);
  } else {
    expect(launched.exitCode, 0, reason: _diagnostics(launched));
    final result = aot
        ? await Process.run(
            artifact.path,
            runtimeArgs,
            workingDirectory: fixture.root.path,
            stdoutEncoding: utf8,
          )
        : launched;
    expect(result.exitCode, 0, reason: _diagnostics(result));
    expect(result.stdout.toString().trim(), expected);
  }

  await _expectSnapshotClean(fixture);
}

Future<void> _expectSnapshotClean(SelectFixture fixture) async {
  final scratch = Directory('${fixture.root.path}/.tmp');
  if (await scratch.exists()) {
    expect(
      await scratch.list().any((item) => item.path.contains('luxo-selection-')),
      isFalse,
    );
  }
}

String _diagnostics(ProcessResult result) =>
    '${result.stdout}\n${result.stderr}';
