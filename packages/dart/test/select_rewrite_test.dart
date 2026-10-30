import 'package:luxo_client/src/builder/select_rewrite.dart';
import 'package:test/test.dart';

import 'select_fixture.dart';

const definitions = '''
import 'package:luxo_client/luxo_client.dart';
class User(final Selected<int> id, final Selected<String> name);
class LuxoClient {
  Future<User> getUser(int id, {String? select}) async {
    print(select);
    return User(const Selected.value(1), const Selected.value('test'));
  }
}
''';

void main() {
  test('prefixed client plans are hoisted once in part files', () async {
    final client = definitions
        .replaceFirst(
          'class LuxoClient {',
          r'class LuxoClient { static PreparedSelection $prepareSelection(String text) => PreparedSelection(text);',
        )
        .replaceFirst(
          '{String? select}',
          r'{String? select, PreparedSelection? $selectionPlan}',
        );
    const source = """part of 'main.dart';
Future<void> load(sdk.LuxoClient client) async {
  final first = await client.getUser(1);
  print(first.id.value);
  final second = await client.getUser(2);
  print(second.name.value);
}
""";
    final fixture = await SelectFixture.create({
      'lib/client.dart': client,
      'lib/main.dart':
          "import 'client.dart' as sdk show LuxoClient; part 'calls.dart';",
      'lib/calls.dart': source,
    });
    try {
      final resolved = await fixture.unit('lib/calls.dart');
      final rewritten = rewriteSelections(
        resolved.unit,
        source,
        library: resolved.libraryElement,
      );
      expect(rewritten, contains(r"sdk.LuxoClient.$prepareSelection('id')"));
      expect(rewritten, contains(r"sdk.LuxoClient.$prepareSelection('name')"));
      expect(rewritten, contains(r'$selectionPlan:'));
      expect(rewritten.split('\n').length, source.split('\n').length);
      final rebuilt = await SelectFixture.create({
        'lib/client.dart': client,
        'lib/main.dart':
            "import 'client.dart' as sdk show LuxoClient; part 'calls.dart';",
        'lib/calls.dart': rewritten,
      });
      try {
        final reparsed = await rebuilt.unit('lib/calls.dart');
        expect(
          rewriteSelections(
            reparsed.unit,
            rewritten,
            library: reparsed.libraryElement,
          ),
          rewritten,
          reason: 'prepared output must not accumulate plans on a second pass',
        );
      } finally {
        await rebuilt.close();
      }
    } finally {
      await fixture.close();
    }
  });

  test(
    'same API has isolated call-site selections and source stays unchanged',
    () async {
      const source =
          '''
$definitions
Future<void> main() async {
  final client = LuxoClient();
  final first = await client.getUser(1);
  print(first.id.value);
  final second = await client.getUser(2,);
  print(second.name.value);
}
''';
      final fixture = await SelectFixture.create({'lib/main.dart': source});
      try {
        final resolved = await fixture.unit('lib/main.dart');
        final rewritten = rewriteSelections(resolved.unit, source);
        expect(rewritten, contains("client.getUser(1, select: 'id')"));
        expect(rewritten, contains("client.getUser(2, select: 'name')"));
        expect(rewritten.split('\n').length, source.split('\n').length);
        expect(fixture.sources['lib/main.dart'], source);
      } finally {
        await fixture.close();
      }
    },
  );

  test(
    'explicit selection is preserved and unresolved usage is rejected',
    () async {
      const explicit =
          '''
$definitions
Future<User> load(LuxoClient client) => client.getUser(1, select: 'name');
''';
      final fixture = await SelectFixture.create({'lib/main.dart': explicit});
      try {
        expect(
          rewriteSelections(
            (await fixture.unit('lib/main.dart')).unit,
            explicit,
          ),
          explicit,
        );
      } finally {
        await fixture.close();
      }
      final missing = explicit.replaceAll(", select: 'name'", '');
      final other = await SelectFixture.create({'lib/main.dart': missing});
      try {
        final unit = await other.unit('lib/main.dart');
        expect(() => rewriteSelections(unit.unit, missing), throwsStateError);
      } finally {
        await other.close();
      }
    },
  );

  test(
    'partial local use does not hide a model escaping to an unknown function',
    () async {
      const source =
          '''
$definitions
void elsewhere(User user) { print(user.name.value); }
Future<void> main() async {
  final user = await LuxoClient().getUser(1);
  print(user.id.value);
  elsewhere(user);
}
''';
      final fixture = await SelectFixture.create({'lib/main.dart': source});
      try {
        final unit = await fixture.unit('lib/main.dart');
        expect(() => rewriteSelections(unit.unit, source), throwsStateError);
      } finally {
        await fixture.close();
      }
    },
  );

  test('subscription callbacks have independent selections', () async {
    final source =
        definitions.replaceFirst('  Future<User> getUser', '''
  Future<void Function()> subscribeUser({String? select, required void Function(User) onData}) async => () {};
  Future<User> getUser''') +
        '''
Future<void> main() async {
  final client = LuxoClient();
  await client.subscribeUser(onData: (user) { print(user.id.value); });
  await client.subscribeUser(onData: (user) { print(user.name.value); });
}
''';
    final fixture = await SelectFixture.create({'lib/main.dart': source});
    try {
      final unit = await fixture.unit('lib/main.dart');
      final rewritten = rewriteSelections(unit.unit, source);
      expect(rewritten, contains("print(user.id.value); }, select: 'id'"));
      expect(rewritten, contains("print(user.name.value); }, select: 'name'"));
    } finally {
      await fixture.close();
    }
  });
}
