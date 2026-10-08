import 'dart:convert';
import 'dart:io';

import 'package:build/build.dart';
import 'package:luxo_client/src/builder/select_builder.dart';
import 'package:luxo_client/src/codegen.dart';
import 'package:test/test.dart';

import 'select_fixture.dart';

const modelSource = '''
import 'package:luxo_client/luxo_client.dart';
class User(final Selected<int> id, final Selected<String> name);
class LuxoClient {
  Future<User> getUser() async => User(Selected.value(1), Selected.value('Ada'));
}
''';

void main() {
  test('builder declares its output under the generated client directory', () {
    expect(selectHintsBuilder(BuilderOptions({})).buildExtensions, {
      r'lib/$lib$': ['lib/src/luxo/_select_hints.g.dart'],
    });
  });

  test(
    'builder resolves types, merges files and scans user luxo directories',
    () async {
      final fixture = await SelectFixture.create({
        'lib/src/luxo/client.dart': modelSource,
        'lib/first.dart': '''
import 'src/luxo/client.dart';
Future<void> first(LuxoClient client) async {
  final user = await client.getUser();
  print(user.name.value);
}
''',
        'lib/luxo/second.dart': '''
import '../src/luxo/client.dart';
Future<void> second(LuxoClient client) async {
  final user = await client.getUser();
  print(user.id.value);
}
''',
      });
      addTearDown(fixture.close);
      final step = SelectBuildStep(fixture);
      await selectHintsBuilder(BuilderOptions({})).build(step);
      expect(fixture.resolvedReads, 3);
      expect(step.outputs.values.single, contains("'getUser': 'id,name'"));
      expect(step.outputs.values.single, isNot(contains('{value}')));
    },
  );

  test(
    'builder visits part files exactly once and ignores its generated output',
    () async {
      final fixture = await SelectFixture.create({
        'lib/src/luxo/client.dart': modelSource,
        'lib/page.dart': '''
import 'src/luxo/client.dart';
part 'detail.dart';
Future<void> page(LuxoClient client) async {
  final user = await client.getUser();
  print(user.id.value);
}
''',
        'lib/detail.dart': '''
part of 'page.dart';
Future<void> detail(LuxoClient client) async {
  final user = await client.getUser();
  print(user.name.value);
}
''',
        'lib/src/luxo/_select_hints.g.dart': 'this is intentionally not Dart;',
      });
      addTearDown(fixture.close);
      final step = SelectBuildStep(fixture);
      await selectHintsBuilder(BuilderOptions({})).build(step);
      expect(fixture.resolvedReads, 3);
      expect(step.outputs.values.single, contains("'getUser': 'id,name'"));
    },
  );

  test(
    'missing resolved AST fails explicitly without writing empty hints',
    () async {
      final fixture = await SelectFixture.create({
        'lib/src/luxo/client.dart': modelSource,
      });
      addTearDown(fixture.close);
      fixture.omitAST = true;
      final step = SelectBuildStep(fixture);
      await expectLater(
        selectHintsBuilder(BuilderOptions({})).build(step),
        throwsA(isA<StateError>()),
      );
      expect(step.outputs, isEmpty);
    },
  );

  test(
    'another generated client cannot add fields to this client hints',
    () async {
      final fixture = await SelectFixture.create({
        'lib/src/luxo/client.dart': modelSource,
        'lib/other/client.dart': modelSource,
        'lib/page.dart': '''
import 'src/luxo/client.dart' as primary;
import 'other/client.dart' as secondary;
Future<void> page(primary.LuxoClient first, secondary.LuxoClient second) async {
  final a = await first.getUser();
  print(a.id.value);
  final b = await second.getUser();
  print(b.name.value);
}
''',
      });
      addTearDown(fixture.close);
      final step = SelectBuildStep(fixture);
      await selectHintsBuilder(BuilderOptions({})).build(step);
      expect(step.outputs.values.single, contains("'getUser': 'id'"));
    },
  );

  test('generated client ignores API-wide hints and uses injected call-site selections', () async {
    final fixture = await SelectFixture.create({
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
      'bin/verify.dart': await File('test/fixtures/select_consumer.dart.txt')
          .readAsString(),
    }, prepare: generateSelectionClient);
    addTearDown(fixture.close);
    final step = SelectBuildStep(fixture);
    await selectHintsBuilder(BuilderOptions({})).build(step);
    expect(step.outputs.values.single, contains("'getUser': 'id,name'"));
    for (final entry in step.outputs.entries) {
      await File('${fixture.root.path}/${entry.key.path}')
          .writeAsString(entry.value);
    }
    final generated = await File(
      '${fixture.root.path}/lib/src/luxo/client.dart',
    ).readAsString();
    expect(generated, isNot(contains('selectHints')));
    await fixture.runDart([
      File('bin/build.dart').absolute.path,
      '--project',
      fixture.root.path,
      '--entry',
      'bin/verify.dart',
    ]);
  });
}

Future<void> generateSelectionClient(Directory root) async {
  final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
  final response = server.first.then((request) async {
    request.response.headers.contentType = ContentType.json;
    request.response.write(
      jsonEncode({
        'models': {
          'User': {
            'name': 'User',
            'usage': 'output',
            'fields': [
              {'id': 1, 'name': 'id', 'type': 'Int'},
              {'id': 2, 'name': 'name', 'type': 'String'},
            ],
          },
        },
        'apis': {
          'getUser': {
            'id': 1,
            'name': 'getUser',
            'module': 'user',
            'returnType': 'User',
          },
        },
      }),
    );
    await request.response.close();
  });
  try {
    await generate(
      endpoint: 'http://${server.address.host}:${server.port}/luvia',
      key: 'fixture',
      outDir: '${root.path}/lib/src/luxo',
    );
    await response;
  } finally {
    await server.close(force: true);
  }
}
