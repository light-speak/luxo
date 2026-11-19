import 'dart:convert';
import 'dart:io';

import 'package:luxo_client/src/builder/selection_build.dart';
import 'package:test/test.dart';

import 'select_fixture.dart';

const _minimal = {'lib/main.dart': 'void main() {}', 'asset.txt': 'original'};

void main() {
  test('generated aggregate sources retain fields over real HTTP', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    final selections = <String>[];
    final serving = _serveCollections(server, selections);
    final source = _aggregateMain.replaceAll(
      'ENDPOINT',
      'http://127.0.0.1:${server.port}/luvia',
    );
    SelectFixture? fixture;
    SelectionBuild? build;
    try {
      fixture = await _collectionFixture(source);
      build = await SelectionBuild.prepare(fixture.root);
      await fixture.runDart([
        'lib/main.dart',
      ], workingDirectory: build.directory.path);
      expect(selections, List.filled(8, 'id,name'));
      expect(
        await File('${fixture.root.path}/lib/main.dart').readAsString(),
        source,
      );
    } finally {
      await build?.close();
      await fixture?.close();
      await server.close(force: true);
      await serving;
    }
  });

  test(
    'generated SDK override and explicit opaque selection preserve behavior',
    () async {
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      final selections = <String>[];
      final serving = _serveCollections(server, selections);
      final source = _opaqueSelectionMain.replaceAll(
        'ENDPOINT',
        'http://127.0.0.1:${server.port}/luvia',
      );
      SelectFixture? fixture;
      SelectionBuild? build;
      try {
        fixture = await _collectionFixture(source);
        build = await SelectionBuild.prepare(fixture.root);
        await fixture.runDart([
          'lib/main.dart',
        ], workingDirectory: build.directory.path);
        expect(selections, ['name', 'id,name']);
        expect(
          await File('${fixture.root.path}/lib/main.dart').readAsString(),
          source,
        );
      } finally {
        await build?.close();
        await fixture?.close();
        await server.close(force: true);
        await serving;
      }
    },
  );

  for (final expression in [
    'print(users.first.displayName)',
    'final read = users.first.label; print(read())',
    'print(users.first.alias.value)',
    'consume([users.first])',
    "consume({'user': users.first})",
    'consume((user: users.first,))',
    'consume(() => users.first)',
    r'print("${users.first}")',
    'throw users.first',
    'final rows = <User?>[null]; rows[0] = users.first',
    'stored = users.first',
    'final holder = Holder(); holder.user = users.first',
  ]) {
    test('generated build rejects opaque model member: $expression', () async {
      final fixture = await _collectionFixture('''
import 'package:luxo_client/luxo_client.dart';
import 'src/luxo/client.dart';
import 'src/luxo/types.dart';
void consume(Object value) { print(value); }
User? stored;
class Holder { User? user; }
extension Labels on User {
  String get displayName => name.value;
  Selected<String> get alias => name;
  String label() => name.value;
}
Future<void> inspect(LuxoClient client) async {
  final users = await client.searchUsers();
  print(users.first.id.value);
  $expression;
}
''');
      try {
        await expectLater(
          SelectionBuild.prepare(fixture.root),
          throwsA(
            isA<StateError>().having(
              (error) => error.message,
              'message',
              contains('Cannot infer selection'),
            ),
          ),
        );
      } finally {
        await fixture.close();
      }
    });
  }

  test(
    'generated collection results send every field consumed downstream',
    () async {
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      final selections = <String>[];
      final serving = _serveCollections(server, selections);
      final source = _collectionMain.replaceAll(
        'ENDPOINT',
        'http://127.0.0.1:${server.port}/luvia',
      );
      SelectFixture? fixture;
      SelectionBuild? build;
      try {
        fixture = await _collectionFixture(source);
        build = await SelectionBuild.prepare(fixture.root);
        await fixture.runDart([
          'lib/main.dart',
        ], workingDirectory: build.directory.path);
        expect(selections, [...List.filled(6, 'id,name'), 'name']);
        expect(
          await File('${fixture.root.path}/lib/main.dart').readAsString(),
          source,
        );
      } finally {
        await build?.close();
        await fixture?.close();
        await server.close(force: true);
        await serving;
      }
    },
  );

  for (final expression in [
    'users.where(matches).map((user) => user.id.value).toList()',
    'users.first.id.value.toString() + users.first.map()',
    'users.where((user) => user.id.value.toString() == "1").map((user) => user).first.name.value',
  ]) {
    test('collection builds reject untraceable fields: $expression', () async {
      final fixture = await _collectionFixture('''
import 'src/luxo/client.dart';
import 'src/luxo/types.dart';
bool matches(User user) => user.name.value.isNotEmpty;
extension Label on User { String map() => name.value; }
Future<void> inspect(LuxoClient client) async {
  final users = await client.searchUsers();
  print($expression);
}
''');
      try {
        await expectLater(
          SelectionBuild.prepare(fixture.root),
          throwsA(
            isA<StateError>().having(
              (error) => error.message,
              'message',
              contains('Cannot infer selection'),
            ),
          ),
        );
      } finally {
        await fixture.close();
      }
    });
  }

  test(
    'generated stream callback wrappers survive the build overlay',
    () async {
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      final serving = _serveWrappedStream(server);
      final source = _wrappedStreamMain.replaceAll(
        'ENDPOINT',
        'ws://127.0.0.1:${server.port}/luvia',
      );
      SelectFixture? fixture;
      SelectionBuild? build;
      try {
        fixture = await SelectFixture.create(
          {'lib/main.dart': source},
          prepare: (root) =>
              generateSelectionClient(root, schema: _streamSchema),
        );
        build = await SelectionBuild.prepare(fixture.root);
        Future<void> serverMustRemainActive() async {
          await serving;
          throw StateError('stream server stopped before the client');
        }

        await Future.any([
          fixture.runDart([
            'lib/main.dart',
          ], workingDirectory: build.directory.path),
          serverMustRemainActive(),
        ]);
        expect(
          await File('${fixture.root.path}/lib/main.dart').readAsString(),
          source,
        );
      } finally {
        await build?.close();
        await fixture?.close();
        await server.close(force: true);
        await serving;
      }
    },
  );
  test(
    'Flutter generated plugin links are excluded from source snapshots',
    () async {
      final fixture = await SelectFixture.create(_minimal);
      SelectionBuild? build;
      try {
        for (final path in [
          'ios/.symlinks',
          'macos/Pods',
          'linux/flutter/ephemeral',
          'windows/flutter/ephemeral/.plugin_symlinks',
        ]) {
          final directory = Directory('${fixture.root.path}/$path');
          await directory.create(recursive: true);
          await Link('${directory.path}/plugin').create(fixture.root.path);
        }
        build = await SelectionBuild.prepare(fixture.root);
        expect(
          await File('${build.directory.path}/asset.txt').readAsString(),
          'original',
        );
        expect(
          await Directory('${build.directory.path}/ios/.symlinks').exists(),
          isFalse,
        );
      } finally {
        await build?.close();
        await fixture.close();
      }
    },
  );
  for (final usePart in [false, true]) {
    test('hidden client types receive plan imports (part: $usePart)', () async {
      const client = r'''import 'package:luxo_client/luxo_client.dart';
class User(final Selected<int> id);
class LuxoClient {
  static PreparedSelection $prepareSelection(String text) => PreparedSelection(text);
  Future<User> getUser(int id, {String? select, PreparedSelection? $selectionPlan}) async {
    if ($selectionPlan == null || $selectionPlan.text != select) throw StateError('missing plan');
    return User(Selected.value(id));
  }
}

LuxoClient makeClient() => LuxoClient();
''';
      const main = "import 'factory.dart' show makeClient; part 'calls.dart';";
      const calls = r'''part of 'main.dart';
final $luxoSelectionClient0 = 42;
Future<void> main() async {
  final user = await makeClient().getUser(1);
  if (user.id.value != 1) throw StateError('wrong result');
  final next = await makeClient().getUser(2);
  if (next.id.value != 2) throw StateError('wrong second result');
}
''';
      final sources = {
        'lib/main.dart': usePart
            ? main
            : "import 'factory.dart' show makeClient;" +
                  calls.replaceFirst("part of 'main.dart';", ''),
        if (usePart) 'lib/calls.dart': calls,
      };
      final fixture = await SelectFixture.create({
        'lib/src/luxo/client.dart': client,
        'lib/factory.dart': "export 'src/luxo/client.dart' show makeClient;",
        ...sources,
      });
      SelectionBuild? build;
      try {
        build = await SelectionBuild.prepare(fixture.root);
        await fixture.runDart([
          'lib/main.dart',
        ], workingDirectory: build.directory.path);
        final root = await File('${build.directory.path}/lib/main.dart')
            .readAsString();
        expect(
          RegExp(r'as \$luxoSelectionClient0_').allMatches(root).length,
          1,
        );
        for (final entry in sources.entries) {
          expect(
            await File('${fixture.root.path}/${entry.key}').readAsString(),
            entry.value,
          );
          final staged = await File('${build.directory.path}/${entry.key}')
              .readAsString();
          expect(staged.split('\n').length, entry.value.split('\n').length);
        }
        await build.refresh();
        await fixture.runDart([
          'lib/main.dart',
        ], workingDirectory: build.directory.path);
      } finally {
        await build?.close();
        await fixture.close();
      }
    });
  }
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
    'generated Binary calls reuse isolated plans and preserve manual selection',
    () async {
      final source = await File('test/fixtures/select_binary_consumer.dart.txt')
          .readAsString();
      final fixture = await _generatedFixture(source);
      SelectionBuild? build;
      try {
        build = await SelectionBuild.prepare(fixture.root);
        for (final arguments in [
          <String>[],
          ['--network'],
        ]) {
          final result = await Process.run(Platform.resolvedExecutable, [
            'bin/verify.dart',
            ...arguments,
          ], workingDirectory: build.directory.path);
          expect(
            result.exitCode,
            0,
            reason: '$arguments: ${result.stdout}\n${result.stderr}',
          );
        }
        expect(
          await File('${fixture.root.path}/bin/verify.dart').readAsString(),
          source,
        );
      } finally {
        await build?.close();
        await fixture.close();
      }
    },
  );
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

Future<SelectFixture> _collectionFixture(String source) => SelectFixture.create(
  {'lib/main.dart': source},
  prepare: (root) => generateSelectionClient(root, schema: _collectionSchema),
);

const _collectionSchema = <String, Object?>{
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
    'searchUsers': {
      'id': 1,
      'name': 'searchUsers',
      'module': 'user',
      'returnType': 'User',
      'returnList': true,
    },
  },
};

Future<void> _serveCollections(
  HttpServer server,
  List<String> selections,
) async {
  await for (final request in server) {
    final body = jsonDecode(
      await utf8.decoder.bind(request).join(),
    ) as Map<String, dynamic>;
    final select = body[r'$select'] as String? ?? '';
    selections.add(select);
    request.response.headers.contentType = ContentType.json;
    request.response.write(
      jsonEncode({
        'data': [
          {
            if (select.split(',').contains('id')) 'id': 1,
            if (select.split(',').contains('name')) 'name': 'Ada',
          },
        ],
      }),
    );
    await request.response.close();
  }
}

const _aggregateMain = r'''
import 'package:luxo_client/luxo_client.dart';
import 'src/luxo/client.dart';
import 'src/luxo/types.dart';
String consume(List<User> rows) => rows.first.name.value;
Future<String> list(LuxoClient client) async {
  final user = (await client.searchUsers()).first;
  if (user.id.value.toString() != '1') throw StateError('id');
  final rows = [user]; return rows.first.name.value;
}
Future<String> map(LuxoClient client) async {
  final user = (await client.searchUsers()).first;
  if (user.id.value.toString() != '1') throw StateError('id');
  final row = {'user': user}; return row['user']!.name.value;
}
Future<String> record(LuxoClient client) async {
  final user = (await client.searchUsers()).first;
  if (user.id.value.toString() != '1') throw StateError('id');
  final row = (user: user,); return row.user.name.value;
}
Future<String> nested(LuxoClient client) async {
  final user = (await client.searchUsers()).first;
  if (user.id.value.toString() != '1') throw StateError('id');
  final row = (items: [{'user': user}],); return row.items[0]['user']!.name.value;
}
Future<String> mapped(LuxoClient client) async {
  final user = (await client.searchUsers()).first;
  if (user.id.value.toString() != '1') throw StateError('id');
  final rows = [(user: user,)]; return rows.map((row) => row.user.name.value).single;
}
Future<String> manual(LuxoClient client) async {
  final user = (await client.searchUsers(select: 'id,name')).first;
  if (user.id.value.toString() != '1') throw StateError('id');
  return consume([user]);
}
Future<String> interpolatedKey(LuxoClient client) async {
  final user = (await client.searchUsers()).first;
  if (user.id.value.toString() != '1') throw StateError('id');
  final key = 'user'; final row = {'$key': user}; return row['user']!.name.value;
}
Future<String> interpolatedIndex(LuxoClient client) async {
  final user = (await client.searchUsers()).first;
  if (user.id.value.toString() != '1') throw StateError('id');
  final key = 'user'; final row = {'user': user}; return row['$key']!.name.value;
}
Future<void> main() async {
  final client = LuxoClient.create('ENDPOINT');
  client.setMode(TransportMode.json);
  try {
    for (final read in [list, map, record, nested, mapped, manual, interpolatedKey, interpolatedIndex]) {
      if (await read(client) != 'Ada') throw StateError('lost aggregate field');
    }
  } finally { client.close(); }
}
''';

const _opaqueSelectionMain = r'''
import 'package:luxo_client/luxo_client.dart';
import 'src/luxo/client.dart';
import 'src/luxo/types.dart';
extension Labels on User {
  String get displayName => name.value;
  Selected<String> get alias => name;
  String label() => name.value;
}
class CustomClient extends LuxoClient {
  CustomClient(super.transport);
  Future<User> local({String? select}) async =>
      User(name: Selected.value(select ?? 'business'));
  @override Future<List<User>> searchUsers(
      {String? select, PreparedSelection? $selectionPlan}) =>
      super.searchUsers(select: select, $selectionPlan: $selectionPlan);
}
Future<void> main() async {
  final client = CustomClient(HttpTransport('ENDPOINT'));
  client.setMode(TransportMode.json);
  try {
    final inferred = await client.searchUsers();
    if (inferred.first.name.value != 'Ada') throw StateError('inherited API plan');
    final local = await client.local();
    if (local.name.value != 'business') throw StateError('ordinary method changed');
    final explicit = await client.searchUsers(select: 'id,name');
    final read = explicit.first.label;
    if (explicit.first.displayName != 'Ada' || explicit.first.alias.value != 'Ada' || read() != 'Ada') {
      throw StateError('explicit opaque access');
    }
  } finally { client.close(); }
}
''';

const _collectionMain = r'''
import 'package:luxo_client/luxo_client.dart';
import 'src/luxo/client.dart';
Future<String> filtered(LuxoClient client) async {
  final users = await client.searchUsers();
  return users.where((user) => user.id.value.toString() == '1').map((user) => user.name.value).join(',');
}
Future<String> first(LuxoClient client) async {
  final users = await client.searchUsers();
  return users.firstWhere((user) => user.id.value.toString() == '1').name.value;
}
Future<String> last(LuxoClient client) async {
  final users = await client.searchUsers();
  return users.lastWhere((user) => user.id.value.toString() == '1').name.value;
}
Future<String> single(LuxoClient client) async {
  final users = await client.searchUsers();
  return users.singleWhere((user) => user.id.value.toString() == '1').name.value;
}
Future<String> indexed(LuxoClient client) async {
  final users = await client.searchUsers();
  return users.where((user) => user.id.value.toString() == '1').toList()[0].name.value;
}
Future<String> alias(LuxoClient client) async {
  final users = await client.searchUsers();
  final active = users.where((user) => user.id.value.toString() == '1');
  return active.first.name.value;
}
Future<String> scalars(LuxoClient client) async {
  final users = await client.searchUsers();
  return users.map((user) => user.name.value).where((name) => name.isNotEmpty).join(',');
}
Future<void> main() async {
  final client = LuxoClient.create('ENDPOINT');
  client.setMode(TransportMode.json);
  try {
    for (final read in [filtered, first, last, single, indexed, alias, scalars]) {
      if (await read(client) != 'Ada') throw StateError('collection result');
    }
  } finally { client.close(); }
}
''';

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

const _streamSchema = <String, Object?>{
  'models': {
    'User': {
      'name': 'User',
      'usage': 'output',
      'fields': [
        {'id': 1, 'name': 'name', 'type': 'String'},
      ],
    },
    'Update': {
      'name': 'Update',
      'usage': 'output',
      'fields': [
        {'id': 1, 'name': 'id', 'type': 'Int'},
        {
          'id': 2,
          'name': 'user',
          'type': 'Model',
          'typeName': 'User',
          'relation': true,
        },
      ],
    },
  },
  'apis': {
    'updates': {
      'id': 1,
      'name': 'updates',
      'module': 'updates',
      'returnType': 'Update',
      'stream': true,
    },
  },
};

const _wrappedStreamMain = r'''
import 'dart:async';
import 'package:luxo_client/luxo_client.dart';
import 'src/luxo/client.dart';
import 'src/luxo/types.dart';
Future<void> main() async {
  final client = LuxoClient.create('ENDPOINT');
  client.setMode(TransportMode.json);
  final done = Completer<void>();
  String? value;
  Object? failure;
  try {
    final cancel = await client.subscribeUpdates(
      onData: (((Update update) { value = update.user.value.name.value; }) as void Function(Update))!,
      onError: (error) { failure = error; if (!done.isCompleted) done.complete(); },
      onDone: () { if (!done.isCompleted) done.complete(); },
    );
    await done.future.timeout(const Duration(seconds: 5));
    cancel();
    if (value != 'selected' || failure != null) throw StateError('lost selected relation: $failure / $value');
  } finally { client.close(); }
}
''';

Future<void> _serveWrappedStream(HttpServer server) async {
  await for (final request in server) {
    final socket = await WebSocketTransformer.upgrade(request);
    try {
      await for (final raw in socket) {
        final message = jsonDecode(raw as String) as Map<String, Object?>;
        expect(message[r'$sub'], 'updates');
        expect(message[r'$select'], 'user{name}');
        final id = message[r'$id'];
        socket.add(jsonEncode({r'$sub': id, 'ok': true}));
        socket.add(
          jsonEncode({
            r'$stream': id,
            'data': {
              'user': {'name': 'selected'},
            },
          }),
        );
        socket.add(jsonEncode({r'$end': id, 'ok': true}));
        break;
      }
    } finally {
      await socket.close();
    }
  }
}
