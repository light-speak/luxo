import 'dart:async';

import 'package:analyzer/dart/analysis/utilities.dart';
import 'package:luxo_client/src/builder/select_analyzer.dart';
import 'package:test/test.dart';

import 'select_fixture.dart';

const selectedModels = '''
import 'package:luxo_client/luxo_client.dart';
class User(final Selected<int> id, final Selected<String> name);
class Comment(final Selected<User> author);
class Post(final Selected<User?> author, final Selected<List<Comment>> comments, final Selected<User?> editor);
class LuxoClient {
  Future<User> getUser() async => throw UnimplementedError();
  Future<User> getOwner() async => throw UnimplementedError();
  Future<Post> getPost() async => throw UnimplementedError();
  Future<Page<User>> listUsers() async => throw UnimplementedError();
  Future<List<User>> searchUsers() async => throw UnimplementedError();
}
''';

void main() {
  test('sealed cold controls preserve nullable and conservative public patterns', () async {
    const declarations = r"""
import 'package:luxo_client/luxo_client.dart';
@LuxoSealedProjection('Outcome',['Success','Pending'])
sealed class Outcome { const Outcome(); }
@LuxoSealedProjection('Empty',[])
sealed class Empty { const Empty(); }
class User { final Selected<int> id; final Selected<String> name; const User(this.id,this.name); }
@LuxoSealedCaseProjection('Outcome','Success',1)
final class Success extends Outcome {
 final Selected<Empty?> child;
 final Selected<List<User>> values;
 final Selected<Object?> document;
 const Success(this.child,this.values,this.document);
}
@LuxoSealedCaseProjection('Outcome','Pending',2)
final class Pending extends Outcome { const Pending(); }
class LuxoClient { Future<Outcome> echo() async=>throw UnimplementedError(); }
""";
    final escaped = <String>{};
    final nullable = await analyzeSource('''
$declarations
Future<void> inspect(LuxoClient client) async {
 final row=await client.echo();if(row is Success){if(null==row.child.value)print('nil');}
}
''', escaped: escaped);
    expect(nullable, {'echo': 'Success{child}'});
    expect(escaped, isEmpty);
    final json = await analyzeSource(
      "$declarations\nFuture<void> inspect(LuxoClient client) async { final row=await client.echo();if(row is Success){final map=row.document.value as Map<String,dynamic>;final child=map['value'];if(child==null)print('nil');if(child case Map())print('map');}}",
    );
    expect(json, {'echo': 'Success{document}'});
    final impossibleCase = await analyzeSource(
      "$declarations\nFuture<void> inspect(LuxoClient client) async {final row=await client.echo();if(row is Success){final success=row as Success;final Outcome widened=success;if(widened is Pending)print('cannot run');}}",
    );
    expect(impossibleCase, {'echo': 'Success'});
    for (final body in [
      'if(row case Success(runtimeType: final type)) print(type);',
      'if(row case Success(values: Selected(runtimeType: final type))) print(type);',
      'if(row case Success(values: Selected(value: [final first, ...]))) print(first);',
    ]) {
      final rejected = <String>{};
      await analyzeSource('''
$declarations
Future<void> inspect(LuxoClient client) async { final row=await client.echo();$body }
''', escaped: rejected);
      expect(rejected, contains('echo'), reason: body);
    }
    final ordinary = await analyzeSource(r"""
import 'package:luxo_client/luxo_client.dart';
@Deprecated('ordinary declaration remains legal')
@LuxoSelectionFieldProjection('not-a-case')
class User { final Selected<int> id; final Selected<String> name; const User(this.id,this.name); }
class LuxoClient { Future<User> getUser() async=>throw UnimplementedError(); }
Future<void> inspect(LuxoClient client) async {
 final user=await client.getUser();if(user case User())print(user.id.value);print(user.runtimeType);
}
""");
    expect(ordinary['getUser'], contains('id'));
    expect(ordinary['getUser'], isNot(contains('runtimeType')));
  });
  test('sealed native pattern wrappers preserve case payload origins', () async {
    for (final body in [
      "final text = switch(row) { Success(value: Selected(value: final text)) => text, _ => null }; print(text);",
      "final Success(value: Selected(value: text)) = row as Success; print(text);",
      "if(row case Success(value: Selected(value: (final text)))) print(text);",
      "if(row case Success(value: Selected(value: final text as String))) print(text);",
      "if(row case Success(value: Selected(value: final text?))) print(text);",
      "if(row case Success(value: Selected(value: final text!))) print(text);",
      "if(row case Success(value: Selected(value: 'ok'))) print('matched');",
      "if(row case Success(value: _)) print('matched');",
    ]) {
      final escaped = <String>{};
      final hints = await analyzeSource('''
import 'package:luxo_client/luxo_client.dart';
@LuxoSealedProjection('Outcome',['Success','Pending'])
sealed class Outcome { const Outcome(); }
@LuxoSealedCaseProjection('Outcome','Success',1)
final class Success extends Outcome { final Selected<String?> value; const Success(this.value); }
@LuxoSealedCaseProjection('Outcome','Pending',2)
final class Pending extends Outcome { const Pending(); }
class LuxoClient { Future<Outcome> echo() async=>throw UnimplementedError(); }
Future<void> inspect(LuxoClient client) async { final row=await client.echo(); $body }
''', escaped: escaped);
      expect(hints, {'echo': 'Success{value}'}, reason: body);
      expect(escaped, isEmpty, reason: body);
    }
  });
  test(
    'sealed case promotion, casts and discriminants retain case paths',
    () async {
      final escaped = <String>{};
      final hints = await analyzeSource(r"""
import 'package:luxo_client/luxo_client.dart';
@LuxoSealedProjection('Outcome', ['Success', 'Pending'])
sealed class Outcome { const Outcome(); }
@LuxoSealedCaseProjection('Outcome', 'Success', 1)
final class Success extends Outcome {
 final Selected<String> value;
 const Success(this.value);
}
@LuxoSealedCaseProjection('Outcome', 'Pending', 2)
final class Pending extends Outcome { const Pending(); }
class LuxoClient {
 Future<Outcome> echo() async => throw UnimplementedError();
 Future<Outcome> other() async => throw UnimplementedError();
}
Future<void> inspect(LuxoClient client) async {
 final result = await client.echo();
 if (result is Success) { print(result.value.value); }
 print((result as Success).value.value);
 final pending = await client.other();
 print(pending is Pending);
}
""", escaped: escaped);
      expect(hints, {'echo': 'Success{value}', 'other': 'Pending'});
      expect(escaped, isEmpty);
    },
  );

  test('sealed object patterns retain payload bindings', () async {
    final escaped = <String>{};
    final hints = await analyzeSource(r"""
import 'package:luxo_client/luxo_client.dart';
@LuxoSealedProjection('Outcome', ['Success', 'Pending'])
sealed class Outcome { const Outcome(); }
@LuxoSealedCaseProjection('Outcome', 'Success', 1)
final class Success extends Outcome { final Selected<String> value; const Success(this.value); }
@LuxoSealedCaseProjection('Outcome', 'Pending', 2)
final class Pending extends Outcome { const Pending(); }
class LuxoClient { Future<Outcome> echo() async => throw UnimplementedError(); }
Future<void> inspect(LuxoClient client) async {
 final result = await client.echo();
 switch (result) {
   case Success(value: Selected(value: final text)): print(text);
   case Pending(): break;
 }
}
""", escaped: escaped);
    expect(hints, {'echo': 'Pending,Success{value}'});
    expect(escaped, isEmpty);
  });

  test(
    'sealed nullable and list metadata select only real case leaves',
    () async {
      final escaped = <String>{};
      final hints = await analyzeSource(r"""
import 'package:luxo_client/luxo_client.dart';
@LuxoSealedProjection('Outcome',['Success','Nested'])
sealed class Outcome { const Outcome(); }
@LuxoSealedCaseProjection('Outcome','Success',1)
final class Success extends Outcome { final Selected<String> value;const Success(this.value); }
@LuxoSealedCaseProjection('Outcome','Nested',2)
final class Nested extends Outcome {
 final Selected<Outcome?> child;final Selected<List<Outcome>?> history;
 const Nested(this.child,this.history);
}
class LuxoClient { Future<Outcome> echo() async=>throw UnimplementedError(); }
Future<void> inspect(LuxoClient client) async {
 final row=await client.echo();
 if(row is Nested) {
  if(row.child.value==null) print('nil');
  final history=row.history.value;
  print(history?.length);
 }
}
""", escaped: escaped);
      expect(hints, {
        'echo': 'Nested{child{Nested,Success},history{Nested,Success}}',
      });
      expect(escaped, isEmpty);
    },
  );

  for (final sample in erasedFutureBodies.entries) {
    test('await preserves erased model provenance: ${sample.key}', () async {
      final escaped = <String>{};
      final hints = await analyzeSource('''
$erasedFutureModels
Future<void> inspect(LuxoClient client) async { ${sample.value} }
''', escaped: escaped);
      final envelope = sample.key == 'envelope object';
      final api = envelope ? 'getEnvelope' : 'getChild';
      expect(hints, {api: envelope ? 'child{city}' : 'city'});
      expect(escaped, {api});
    });
  }

  for (final container in ['Child', 'List<Child>', 'Page<Child>']) {
    final read = switch (container) {
      'Child' => 'row.secret.value',
      'List<Child>' => 'row.first.secret.value',
      _ => 'row.items.first.secret.value',
    };
    test('await retains widened container shape: $container', () async {
      final escaped = <String>{};
      final hints = await analyzeSource('''
${erasedFutureModels.replaceFirst('class LuxoClient {', '''class LuxoClient {
  Future<$container> getRows() async => throw UnimplementedError();''')}
Future<void> inspect(LuxoClient client) async {
  final Future<Object?> widened = client.getRows();
  final row = (await widened) as $container;
  print($read);
}
''', escaped: escaped);
      expect(hints, {'getRows': 'secret'});
      expect(escaped, isEmpty);
    });
  }

  for (final type in ['FutureOr<Child>', 'Child']) {
    test('await unwraps only the actual source wrapper: $type', () async {
      final models = erasedFutureModels.replaceFirst(
        'Future<Child> getChild({String? select}) async',
        '$type getChild({String? select})',
      );
      final escaped = <String>{};
      final hints = await analyzeSource('''
import 'dart:async';
$models
Future<void> inspect(LuxoClient client) async {
  final FutureOr<Object?> future = client.getChild();
  final row = await future;
  print((row as Child).city.value);
  consume(row);
}
''', escaped: escaped);
      expect(hints, {'getChild': 'city'});
      expect(escaped, {'getChild'});
    });
  }

  test('await keeps scalar sources and separate model calls precise', () async {
    final source =
        '''
${erasedFutureModels.replaceFirst('class LuxoClient {', '''class LuxoClient {
  Future<String> label() async => 'label';''')}
Future<void> inspect(LuxoClient client) async {
  final leaked = client.getChild();
  await leaked.then((row) { print(row.city.value); });
  consume(await (leaked as Future<Object?>));
  final Future<Object?> safe = client.getChild();
  await safe.then((row) { print((row as Child).secret.value); });
  print(((await safe) as Child).city.value);
  final Future<Object?> scalar = client.label();
  await scalar.then((label) { print(label); });
  print(await scalar);
  print(await ((await client.getChild()).city.value));
}
''';
    final fixture = await SelectFixture.create({'lib/main.dart': source});
    try {
      final analyzer = SelectAnalyzer({});
      (await fixture.unit('lib/main.dart')).unit.accept(analyzer);
      final calls = analyzer.callSelections.entries.toList();
      expect(calls.map((entry) => entry.key.methodName.name), [
        'getChild',
        'getChild',
        'label',
        'getChild',
      ]);
      expect(calls.map((entry) => entry.value.toSelectString()), [
        'city',
        'city,secret',
        '',
        'city',
      ]);
      expect(analyzer.escapedCalls, {calls.first.key});
    } finally {
      await fixture.close();
    }
  });

  for (final body in [
    r"final rows = {'$key': user}; print(rows['user']!.name.value);",
    r"final rows = {'user': user}; print(rows['$key']!.name.value);",
  ]) {
    test(
      'aggregate interpolated keys remain possible matches: $body',
      () async {
        final escaped = <String>{};
        final hints = await analyzeSource('''
$selectedModels
Future<void> inspect(LuxoClient client, String key) async {
  final user = await client.getUser(); print(user.id.value); $body
}
''', escaped: escaped);
        expect(hints, {'getUser': 'id,name'});
        expect(escaped, isEmpty);
      },
    );
  }

  for (final body in [
    r"print('$user');",
    'try { throw user; } on User catch (error) { print(error.name.value); }',
    'final holder = Holder(); holder.user = user; print(holder.user!.name.value);',
    'final rows = <User?>[null]; rows[0] = user; print(rows.first!.name.value);',
    "final rows = <String, User>{}; rows['user'] = user; print(rows['user']!.name.value);",
    'stored = user; consumeStored();',
  ]) {
    test('aggregate additional escape boundary: $body', () async {
      final escaped = <String>{};
      await analyzeSource('''
$selectedModels
User? stored;
class Holder { User? user; }
void consumeStored() { print(stored?.name.value); }
Future<void> inspect(LuxoClient client) async {
  final user = await client.getUser(); print(user.id.value); $body
}
''', escaped: escaped);
      expect(escaped, {'getUser'});
    });
  }

  test('aggregate nullable fallback retains both model sources', () async {
    final escaped = <String>{};
    final hints = await analyzeSource('''
$selectedModels
Future<void> inspect(LuxoClient client) async {
  final post = await client.getPost();
  print(post.author.value?.id.value);
  final author = post.author.value ?? post.editor.value;
  print(author?.name.value);
}
''', escaped: escaped);
    expect(hints, {'getPost': 'author{id,name},editor{name}'});
    expect(escaped, isEmpty);
  });

  test(
    'aggregate nullable assignment and scalar interpolation stay precise',
    () async {
      final escaped = <String>{};
      final hints = await analyzeSource('''
$selectedModels
Future<void> inspect(LuxoClient client) async {
  final post = await client.getPost();
  var author = post.author.value;
  author ??= post.editor.value;
  print('name: \${author?.name.value}');
}
''', escaped: escaped);
      expect(hints, {'getPost': 'author{name},editor{name}'});
      expect(escaped, isEmpty);
    },
  );

  for (final body in [
    'final rows = [(user: user,)]; final selected = rows.where((row) => row.user.id.value > 0).toList(); print(selected.first.user.name.value);',
    'final rows = [(user: user,)]; print(rows.firstWhere((row) => row.user.id.value > 0).user.name.value);',
    'final List<Object> rows = [user]; final selected = rows.firstWhere((row) => true) as User; print(selected.name.value);',
  ]) {
    test('aggregate preserving result retains model type: $body', () async {
      final escaped = <String>{};
      final hints = await analyzeSource('''
$selectedModels
Future<void> inspect(LuxoClient client) async {
  final user = await client.getUser(); print(user.id.value); $body
}
''', escaped: escaped);
      expect(hints, {'getUser': 'id,name'});
      expect(escaped, isEmpty);
    });
  }

  for (final body in [
    'final rows = {user}; print(rows.first.name.value);',
    'final row = (user,); print(row.\$1.name.value);',
    "final row = (items: [{'user': user}],); print(row.items[0]['user']!.name.value);",
    'final original = [user]; final rows = [...original]; print(rows.first.name.value);',
    "final original = {'user': user}; final row = {...original}; print(row['user']!.name.value);",
    'final rows = [(user: user,)]; print(rows.map((row) => row.user.name.value).toList());',
    'final rows = [(user: user,)]; for (final row in rows) { print(row.user.name.value); }',
    'final rows = [if (true) user]; print(rows.first.name.value);',
    'final rows = [?user]; print(rows.first.name.value);',
    'final rows = {(user: user,)}; print(rows.map((row) => row.user.name.value).single);',
    'final rows = [for (final row in [user]) (user: row,)]; print(rows.first.user.name.value);',
    'final users = await client.searchUsers(); final rows = [...users]; final row = (rows,); print(row.\$1.first.name.value);',
    "final row = {'user': user}; print(row.values.map((value) => value.name.value).toList());",
  ]) {
    test('aggregate nested source control: $body', () async {
      final escaped = <String>{};
      final hints = await analyzeSource('''
$selectedModels
Future<void> inspect(LuxoClient client) async {
  final user = await client.getUser();
  print(user.id.value);
  $body
}
''', escaped: escaped);
      expect(
        hints,
        body.contains('searchUsers')
            ? {'getUser': 'id', 'searchUsers': 'name'}
            : {'getUser': 'id,name'},
      );
      expect(escaped, isEmpty);
    });
  }

  test(
    'aggregate collection-for arguments escape after binding their element',
    () async {
      final escaped = <String>{};
      await analyzeSource('''
$selectedModels
void consume(List<User> rows) { print(rows.first.name.value); }
Future<void> inspect(LuxoClient client) async {
  final users = await client.searchUsers();
  print(users.first.id.value);
  consume([for (final user in users) user]);
}
''', escaped: escaped);
      expect(escaped, {'searchUsers'});
    },
  );

  test('aggregate literal map keys keep independent API selections', () async {
    final escaped = <String>{};
    final hints = await analyzeSource('''
$selectedModels
Future<void> inspect(LuxoClient client) async {
  final first = await client.getUser();
  final second = await client.getOwner();
  final rows = {'first': first, 'second': second};
  print(rows['first']!.name.value); print(rows['second']!.id.value);
}
''', escaped: escaped);
    expect(hints, {'getUser': 'name', 'getOwner': 'id'});
    expect(escaped, isEmpty);
  });

  test('aggregate integer map keys keep independent API selections', () async {
    final escaped = <String>{};
    final hints = await analyzeSource('''
$selectedModels
Future<void> inspect(LuxoClient client) async {
  final first = await client.getUser(); final second = await client.getOwner();
  final rows = {1: first, 2: second};
  print(rows[1]!.name.value); print(rows[2]!.id.value);
}
''', escaped: escaped);
    expect(hints, {'getUser': 'name', 'getOwner': 'id'});
    expect(escaped, isEmpty);
  });

  test('aggregate unknown map spreads retain known field sources', () async {
    final escaped = <String>{};
    final hints = await analyzeSource('''
$selectedModels
Future<void> inspect(LuxoClient client, Map<String, User> other) async {
  final user = await client.getUser(); print(user.id.value);
  final rows = {'user': user, ...other}; print(rows['user']!.name.value);
}
''', escaped: escaped);
    expect(hints, {'getUser': 'id,name'});
    expect(escaped, isEmpty);
  });

  test(
    'aggregate conditional aliases retain both possible containers',
    () async {
      final escaped = <String>{};
      final hints = await analyzeSource('''
$selectedModels
Future<void> inspect(LuxoClient client, bool flag) async {
  final user = await client.getUser();
  final owner = await client.getOwner();
  final row = flag ? (user: user,) : (user: owner,);
  print(row.user.name.value);
  final direct = flag ? [user] : await client.searchUsers();
  print(direct.first.id.value);
}
''', escaped: escaped);
      expect(hints, {
        'getUser': 'id,name',
        'getOwner': 'name',
        'searchUsers': 'id',
      });
      expect(escaped, isEmpty);
    },
  );

  test(
    'aggregate unknown getters and reused loop variables retain escape',
    () async {
      for (final body in [
        "final row = {'user': user}; print(row.entries);",
        'var current = user; consume([for (current in [user]) current]);',
        'final rows = [for (var i = 0; i < 1; i++) user]; consume(rows);',
      ]) {
        final escaped = <String>{};
        await analyzeSource('''
$selectedModels
void consume(Object value) { print(value); }
Future<void> inspect(LuxoClient client) async {
  final user = await client.getUser(); print(user.id.value); $body
}
''', escaped: escaped);
        expect(escaped, {'getUser'}, reason: body);
      }
    },
  );

  for (final sample in [
    (name: 'list', body: 'consumeList([user]);'),
    (name: 'list alias', body: 'final rows = [user]; consumeList(rows);'),
    (name: 'map', body: "consumeMap({'user': user});"),
    (name: 'map alias', body: "final row = {'user': user}; consumeMap(row);"),
    (name: 'record', body: 'consumeRecord((user: user,));'),
    (
      name: 'record alias',
      body: 'final row = (user: user,); consumeRecord(row);',
    ),
    (name: 'closure', body: 'consumeCallback(() => user);'),
  ]) {
    test('aggregate opaque argument retains escape: ${sample.name}', () async {
      final escaped = <String>{};
      await analyzeSource('''
$selectedModels
void consumeList(List<User> rows) { print(rows.first.name.value); }
void consumeMap(Map<String, User> row) { print(row['user']!.name.value); }
void consumeRecord(({User user}) row) { print(row.user.name.value); }
void consumeCallback(User Function() read) { print(read().name.value); }
Future<void> inspect(LuxoClient client) async {
  final user = await client.getUser();
  print(user.id.value);
  ${sample.body}
}
''', escaped: escaped);
      expect(escaped, {'getUser'});
    });
  }

  for (final sample in [
    (
      name: 'list index',
      body: 'final rows = [user]; print(rows[0].name.value);',
    ),
    (
      name: 'list first',
      body: 'final rows = [user]; print(rows.first.name.value);',
    ),
    (
      name: 'list map',
      body: 'final rows = [user]; print(rows.map((row) => row.name.value).toList());',
    ),
    (
      name: 'map index',
      body: "final row = {'user': user}; print(row['user']!.name.value);",
    ),
    (
      name: 'record field',
      body: 'final row = (user: user,); print(row.user.name.value);',
    ),
  ]) {
    test(
      'aggregate local access retains precise fields: ${sample.name}',
      () async {
        final escaped = <String>{};
        final hints = await analyzeSource('''
$selectedModels
Future<void> inspect(LuxoClient client) async {
  final user = await client.getUser();
  print(user.id.value);
  ${sample.body}
}
''', escaped: escaped);
        expect(hints, {'getUser': 'id,name'});
        expect(escaped, isEmpty);
      },
    );
  }

  test(
    'model wrapper presence and page metadata keep precise fields',
    () async {
      final escaped = <String>{};
      final hints = await analyzeSource('''
$selectedModels
Future<void> inspect(LuxoClient client) async {
  final post = await client.getPost();
  print(post.comments.value.first.author.value.id.value);
  print(post.author.isSelected); print(post.comments.isSelected);
  final page = await client.listUsers();
  print(page.items.first.name.value);
  print(page.total); print(page.page); print(page.pageSize);
}
''', escaped: escaped);
      expect(escaped, isEmpty);
      expect(hints, {'getPost': 'comments{author{id}}', 'listUsers': 'name'});
    },
  );

  test('extension getters named like core metadata still escape', () async {
    final escaped = <String>{};
    await analyzeSource('''
$selectedModels
extension Labels on User { int get length => name.value.length; }
Future<void> inspect(LuxoClient client) async {
  final user = await client.getUser();
  print(user.id.value); print(user.length);
}
''', escaped: escaped);
    expect(escaped, {'getUser'});
  });

  for (final access in [
    'new Holder(user)',
    'print(Labels(user).displayName)',
  ]) {
    test('opaque argument positions retain model escape: $access', () async {
      final escaped = <String>{};
      await analyzeSource('''
$selectedModels
class Holder { Holder(User user) { print(user.name.value); } }
extension Labels on User { String get displayName => name.value; }
Future<void> inspect(LuxoClient client) async {
  final user = await client.getUser();
  print(user.id.value);
  $access;
}
''', escaped: escaped);
      expect(escaped, {'getUser'});
    });
  }

  for (final access in [
    'print(user.displayName)',
    'final read = user.label; print(read())',
    'print(user.alias.value)',
  ]) {
    test('opaque model member requires explicit selection: $access', () async {
      final escaped = <String>{};
      await analyzeSource('''
$selectedModels
extension Labels on User {
  String get displayName => name.value;
  Selected<String> get alias => name;
  String label() => name.value;
}
Future<void> inspect(LuxoClient client) async {
  final user = await client.getUser();
  print(user.id.value);
  $access;
}
''', escaped: escaped);
      expect(escaped, {'getUser'});
    });
  }

  for (final access in ['print(page.firstName)', 'print(page.label())']) {
    test('opaque page member requires explicit selection: $access', () async {
      final escaped = <String>{};
      await analyzeSource('''
$selectedModels
extension Labels on Page<User> {
  String get firstName => items.first.name.value;
  String label() => items.first.name.value;
}
Future<void> inspect(LuxoClient client) async {
  final page = await client.listUsers();
  print(page.items.first.id.value);
  $access;
}
''', escaped: escaped);
      expect(escaped, {'listUsers'});
    });
  }

  test('core and SDK metadata do not escape the selected model', () async {
    final escaped = <String>{};
    final hints = await analyzeSource('''
$selectedModels
Future<void> inspect(LuxoClient client) async {
  final user = await client.getUser();
  print(user.id.value);
  print(user.hashCode); print(user.runtimeType); print(user.name.isSelected);
  final users = await client.searchUsers();
  print(users.first.name.value);
  print(users.length); print(users.isEmpty); print(users.isNotEmpty);
  final page = await client.listUsers();
  print(page.items.first.id.value);
  print(page.total); print(page.page); print(page.pageSize);
}
''', escaped: escaped);
    expect(escaped, isEmpty);
    expect(hints, {
      'getUser': 'id,name',
      'searchUsers': 'name',
      'listUsers': 'id',
    });
  });

  for (final expression in [
    'users.where((user) => user.id.value > 0).map((user) => user.name.value).toList()',
    'users.firstWhere((user) => user.id.value > 0).name.value',
    'users.lastWhere((user) => user.id.value > 0).name.value',
    'users.singleWhere((user) => user.id.value > 0).name.value',
    'users.where((user) => user.id.value > 0).toList()[0].name.value',
  ]) {
    test('collection result keeps its selected element: $expression', () async {
      final hints = await analyzeSource('''
$selectedModels
Future<void> inspect(LuxoClient client) async {
  final users = await client.searchUsers();
  print($expression);
}
''');
      expect(hints['searchUsers']?.split(',').toSet(), {'id', 'name'});
    });
  }

  test('collection alias keeps the original selected element', () async {
    final hints = await analyzeSource('''
$selectedModels
Future<void> inspect(LuxoClient client) async {
  final users = await client.searchUsers();
  final active = users.where((user) => user.id.value > 0);
  print(active.first.name.value);
}
''');
    expect(hints['searchUsers']?.split(',').toSet(), {'id', 'name'});
  });

  test(
    'collection results retain nested paths and page item sources',
    () async {
      final hints = await analyzeSource('''
$selectedModels
Future<void> inspect(LuxoClient client) async {
  final post = await client.getPost();
  print(post.comments.value.where((comment) => comment.author.value.id.value > 0)
      .first.author.value.name.value);
  final page = await client.listUsers();
  print(page.items.firstWhere((user) => user.id.value > 0).name.value);
}
''');
      expect(hints, {
        'getPost': 'comments{author{id,name}}',
        'listUsers': 'id,name',
      });
    },
  );

  test('mapped scalars do not become the original model', () async {
    final hints = await analyzeSource('''
$selectedModels
Future<void> inspect(LuxoClient client) async {
  final users = await client.searchUsers();
  print(users.map((user) => user.name.value).where((name) => name.isNotEmpty).toList());
}
''');
    expect(hints, {'searchUsers': 'name'});
  });

  test(
    'broader declared receivers retain the concrete model field type',
    () async {
      final hints = await analyzeSource('''
$selectedModels
abstract interface class HasOwner { Selected<Object?> get owner; }
class Envelope(final Selected<User> owner) implements HasOwner;
class EnvelopeClient extends LuxoClient {
  Future<Envelope> getEnvelope() async => throw UnimplementedError();
}
Future<void> inspect(EnvelopeClient client) async {
  final HasOwner envelope = await client.getEnvelope();
  print((envelope.owner.value as User).name.value);
}
''');
      expect(hints, {'getEnvelope': 'owner{name}'});
    },
  );

  test(
    'generic resolved getters keep their substituted selected field type',
    () async {
      final hints = await analyzeSource('''
$selectedModels
class Envelope<T>(final Selected<T> owner);
class EnvelopeClient extends LuxoClient {
  Future<Envelope<User>> getEnvelope() async => throw UnimplementedError();
}
Future<void> inspect(EnvelopeClient client) async {
  final envelope = await client.getEnvelope();
  print(envelope.owner.value.name.value);
}
''');
      expect(hints, {'getEnvelope': 'owner{name}'});
    },
  );

  for (final declaration in [
    'class List extends LuxoClient {}',
    'abstract class List extends LuxoClient implements Iterable<User> {}',
  ]) {
    test(
      'collection lookalikes preserve client identity: $declaration',
      () async {
        final hints = await analyzeSource('''
import 'package:luxo_client/luxo_client.dart';
class User(final Selected<String> name);
class LuxoClient {
  Future<User> getUser() async => throw UnimplementedError();
}
$declaration
Future<void> inspect(List client) async {
  final user = await client.getUser();
  print(user.name.value);
}
''');
        expect(hints, {'getUser': 'name'});
      },
    );
  }

  test(
    'configured client library rejects an unrelated LuxoClient declaration',
    () async {
      final hints = await analyzeSource('''
$selectedModels
Future<void> inspect(LuxoClient client) async {
  final user = await client.getUser();
  print(user.name.value);
}
''', clientLibrary: Uri.parse('package:unrelated/client.dart'));
      expect(hints, isEmpty);
    },
  );

  test('unknown collection predicates prevent a partial selection', () async {
    final escaped = <String>{};
    await analyzeSource('''
$selectedModels
bool matches(User user) => user.name.value.isNotEmpty;
Future<void> inspect(LuxoClient client) async {
  final users = await client.searchUsers();
  print(users.where(matches).map((user) => user.id.value).toList());
}
''', escaped: escaped);
    expect(escaped, {'searchUsers'});
  });

  test('model methods named like collection methods still escape', () async {
    final escaped = <String>{};
    await analyzeSource('''
$selectedModels
extension UserLabel on User { String map() => name.value; }
Future<void> inspect(LuxoClient client) async {
  final user = await client.getUser();
  print(user.id.value);
  print(user.map());
}
''', escaped: escaped);
    expect(escaped, {'getUser'});
  });

  for (final callback in ['(user) => user', '(user) { return user; }']) {
    test(
      'model-returning callbacks prevent partial selection: $callback',
      () async {
        final escaped = <String>{};
        await analyzeSource('''
$selectedModels
Future<void> inspect(LuxoClient client) async {
  final users = await client.searchUsers();
  print(users.where((user) => user.id.value > 0).map($callback).first.name.value);
}
''', escaped: escaped);
        expect(escaped, {'searchUsers'});
      },
    );
  }

  final callbacks = [
    '(User user) => print(user.name.value)',
    '((User user) => print(user.name.value))',
    '((User user) => print(user.name.value)) as void Function(User)',
    '((User user) => print(user.name.value))!',
  ];
  for (var index = 0; index < callbacks.length; index++) {
    test('collection callback wrapper $index', () async {
      final hints = await analyzeSource('''
$selectedModels
Future<void> inspect(LuxoClient client) async {
  final users = await client.searchUsers();
  users.forEach(${callbacks[index]});
}
''');
      expect(hints, {'searchUsers': 'name'});
    });
    for (final observer in [false, true]) {
      test('stream callback wrapper $index with observer $observer', () async {
        final hints = await analyzeSource('''
import 'package:luxo_client/luxo_client.dart';
class User(final Selected<int> id, final Selected<String> name);
class LuxoClient {
  Future<void Function()> subscribeUpdates({
    required void Function(User) onData, void Function()? onDone,
  }) async => throw UnimplementedError();
}
void inspect(LuxoClient client, void Function() observer) {
  client.subscribeUpdates(onData: ${callbacks[index]}${observer ? ', onDone: observer' : ''});
}
''');
        expect(hints, {'subscribeUpdates': 'name'});
      });
    }
  }
  test('assignment binds the written local to its API call', () async {
    final hints = await analyzeSource(
      selectedModels +
          '''
Future<void> inspect(LuxoClient client) async {
  late User user;
  user = await client.getUser();
  print(user.name.value);
}
''',
    );
    expect(hints, {'getUser': 'name'});
  });
  test('sealed enum metadata stays outside model field selections', () async {
    final hints = await analyzeSource('''
import 'package:luxo_client/luxo_client.dart';
sealed class State { const State(); String get rawValue; }
enum StateKnown implements State {
  open;
  @override String get rawValue => 'OPEN';
}
final class StateUnknown extends State {
  @override final String rawValue;
  const StateUnknown(this.rawValue);
}
class Card(final Selected<State> state, final Selected<State?> optional,
  final Selected<List<State>> states);
class LuxoClient { Future<Card> getCard() async => throw UnimplementedError(); }
Future<void> inspect(LuxoClient client) async {
  final card = await client.getCard();
  print(card.state.value.rawValue);
  print(card.optional.value?.rawValue);
  print(card.states.value.map((state) => state.rawValue));
}
''');
    expect(hints, {'getCard': 'optional,state,states'});
  });
  test('unresolved AST cannot invent field hints', () {
    final analyzer = SelectAnalyzer({});
    parseString(
      content: '''
Future<void> inspect(dynamic client) async {
  final user = await client.getUser();
  print(user.name);
}
''',
    ).unit.accept(analyzer);
    expect(analyzer.buildSelectStrings(), isEmpty);
  });

  test('selection frontend accepts stable Dart 3.13 primary constructors', () {
    expect(
      () =>
          parseString(content: 'class User(final int id, final String name);'),
      returnsNormally,
    );
  });

  test(
    'resolved selection handles primary constructor fields and return types',
    () async {
      final hints = await analyzeSource('''
class User(final int id, final String name);
class LuxoClient {
  Future<User> getUser() async => User(1, 'Alice');
  User cachedUser() => User(2, 'Bob');
}
void inspectCached(LuxoClient client) {
  final cached = client.cachedUser();
  print(cached.id);
}
Future<void> inspectRemote(LuxoClient client) async {
  final remote = await client.getUser();
  print(remote.name);
}
''');
      expect(hints, {'cachedUser': 'id', 'getUser': 'name'});
    },
  );

  test(
    'nested selections unwrap Selected, nullable paths, lists and aliases',
    () async {
      final hints = await analyzeSource('''
$selectedModels
Future<void> inspect(LuxoClient client) async {
  final post = await client.getPost();
  print(post.author.value?.name.value.length);
  final comment = post.comments.value[0];
  print(comment.author.value.id.value);
  print((post.comments.value[1].author.value).name.value);
}
''');
      expect(hints, {'getPost': 'author{name},comments{author{id,name}}'});
    },
  );

  test('resolved symbols prevent shadowed and unrelated variables contaminating hints', () async {
    final hints = await analyzeSource('''
$selectedModels
Future<void> inspect(LuxoClient client, User local) async {
  final user = await client.getUser();
  print(user.id.value);
  { final user = local; print(user.name.value); }
}
void unrelated(User user) { print(user.name.value); }
class OtherClient { Future<User> getOther() async => throw UnimplementedError(); }
Future<void> other(OtherClient client) async {
  final user = await client.getOther();
  print(user.name.value);
}
''');
    expect(hints, {'getUser': 'id'});
  });

  test(
    'pagination and array traversal select model fields, not wrapper metadata',
    () async {
      final hints = await analyzeSource('''
$selectedModels
Future<void> inspect(LuxoClient client) async {
  final page = await client.listUsers();
  print(page.total);
  print(page.items.first.name.value);
  for (final user in page.items) { print(user.id.value); }
  final users = await client.searchUsers();
  print(users[0].id.value);
  print(users.map((user) => user.name.value).join(','));
}
''');
      expect(hints, {'listUsers': 'id,name', 'searchUsers': 'id,name'});
    },
  );

  test(
    'inline awaits and future callbacks preserve their API source',
    () async {
      final hints = await analyzeSource('''
$selectedModels
Future<void> inspect(LuxoClient client) async {
  print((await client.getUser()).id.value);
  await client.getUser().then((user) { print(user.name.value); });
}
''');
      expect(hints, {'getUser': 'id,name'});
    },
  );

  test('field trees merge without sharing mutable nodes and serialize deterministically', () {
    final first = FieldNode.root()..addChild('name');
    final second = FieldNode.root();
    second.addChild('author').addChild('name');
    second.addChild('id');
    first.merge(second);
    second.children['author']!.addChild('id');
    expect(first.toSelectString(), 'author{name},id,name');
    expect(first.maxDepth(), 2);
  });

  test(
    'conditional sources, repeated aliases and client subtypes stay typed',
    () async {
      final hints = await analyzeSource('''
$selectedModels
class CustomClient extends LuxoClient {}
Future<void> inspect(CustomClient client, bool owner) async {
  var user = await client.getUser();
  user = user;
  if (owner) user = await client.getOwner();
  print(user.name.value);
  final candidate = owner ? await client.getUser() : await client.getOwner();
  print((candidate as User).id.value);
  final post = await client.getPost();
  print(post.author.value!.name.value);
  var nested = post.author.value;
  nested = nested;
  if (owner) nested = post.editor.value;
  print(nested?.name.value);
}
''');
      expect(hints, {
        'getUser': 'id,name',
        'getOwner': 'id,name',
        'getPost': 'author{name},editor{name}',
      });
    },
  );

  test('deep selections warn while empty roots produce no hints', () {
    final analyzer = SelectAnalyzer({});
    analyzer.hints['empty'] = FieldNode.root();
    var node = analyzer.hints['deep'] = FieldNode.root();
    for (var i = 0; i < 6; i++) {
      node = node.addChild('field$i');
    }
    final warnings = <String>[];
    final result = runZoned(
      analyzer.buildSelectStrings,
      zoneSpecification: ZoneSpecification(
        print: (_, __, ___, message) => warnings.add(message),
      ),
    );
    expect(result, {'deep': 'field0{field1{field2{field3{field4{field5}}}}}'});
    expect(warnings.single, contains('6-level'));
  });
}

Future<Map<String, String>> analyzeSource(
  String source, {
  Set<String>? escaped,
  Uri? clientLibrary,
}) async {
  final fixture = await SelectFixture.create({'lib/fixture.dart': source});
  try {
    final unit = await fixture.unit('lib/fixture.dart');
    final analyzer = SelectAnalyzer({
      'User': {'id': 'int', 'name': 'String'},
    }, clientLibrary: clientLibrary);
    unit.unit.accept(analyzer);
    escaped?.addAll(analyzer.escapedCalls.map((call) => call.methodName.name));
    return analyzer.buildSelectStrings();
  } finally {
    await fixture.close();
  }
}
