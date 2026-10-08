import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:luxo_client/src/builder/flutter_commands.dart';
import 'package:test/test.dart';

import 'select_fixture.dart';

void main() {
  for (final nativeExit in <int?>[null, 0, 7]) {
    test(
      'Flutter CLI cleans its snapshot after EOF or native exit $nativeExit',
      () async {
        final fixture = await SelectFixture.create({
          'lib/main.dart': 'void main() {}',
          'fake-flutter':
              '#!/bin/sh\nprintf "READY\\n"\n'
              '${nativeExit == null ? '' : 'exit $nativeExit\n'}'
              r'while IFS= read -r line; do printf "%s\n" "$line"; done'
              '\n'
              'printf "EOF\\n"\n',
        });
        Process? child;
        Future<int>? exited;
        try {
          final executable = '${fixture.root.path}/fake-flutter';
          final permission = await Process.run('chmod', ['+x', executable]);
          expect(permission.exitCode, 0);
          child = await Process.start(Platform.resolvedExecutable, [
            File('bin/flutter.dart').absolute.path,
            '--project',
            fixture.root.path,
            '--flutter',
            executable,
          ]);
          exited = child.exitCode;
          final ready = Completer<void>();
          final lines = <String>[];
          final output = child.stdout
              .transform(utf8.decoder)
              .transform(const LineSplitter())
              .listen((line) {
                lines.add(line);
                if (line == 'READY') ready.complete();
              })
              .asFuture<void>();
          final errors = child.stderr.transform(utf8.decoder).join();
          await ready.future.timeout(const Duration(seconds: 20));
          const command = '[{"id":1,"method":"app.stop"}]';
          if (nativeExit == null) {
            child.stdin.writeln(command);
            await child.stdin.close();
          }
          expect(
            await exited.timeout(const Duration(seconds: 3)),
            nativeExit ?? 0,
          );
          await output;
          expect(await errors, isEmpty);
          if (nativeExit == null)
            expect(lines, containsAllInOrder([command, 'EOF']));
          expect(
            await Directory('${fixture.root.path}/.tmp').list().toList(),
            isEmpty,
          );
        } finally {
          child?.kill(ProcessSignal.sigterm);
          await exited;
          await child?.stdin.close();
          await fixture.close();
        }
      },
      skip: Platform.isWindows
          ? 'The fake Flutter executable uses a POSIX shell'
          : false,
    );
  }
  test(
    'restart waits for transformation; other commands pass through',
    () async {
      final commands = StreamController<String>();
      final ready = Completer<void>();
      final forwarded = <String>[];
      final responses = <String>[];
      final done = forwardFlutterCommands(
        commands.stream,
        refresh: () => ready.future,
        send: forwarded.add,
        respond: responses.add,
      );
      commands.add('[{"id":1,"method":"app.restart","params":{}}]');
      await Future<void>.delayed(Duration.zero);
      expect(forwarded, isEmpty);
      ready.complete();
      commands.add('[{"id":2,"method":"app.stop"}]');
      await commands.close();
      await done;
      expect(forwarded, hasLength(2));
      expect(responses, isEmpty);
    },
  );

  test('failed refresh rejects restart without killing the session', () async {
    final forwarded = <String>[];
    final responses = <String>[];
    await forwardFlutterCommands(
      Stream.fromIterable([
        '[{"id":7,"method":"app.restart"}]',
        '[{"id":8,"method":"app.stop"}]',
      ]),
      refresh: () async => throw StateError('invalid source'),
      send: forwarded.add,
      respond: responses.add,
    );
    expect(forwarded, ['[{"id":8,"method":"app.stop"}]']);
    final response = (jsonDecode(responses.single) as List).single as Map;
    expect(response['id'], 7);
    expect(response['error'], contains('invalid source'));
  });
}
