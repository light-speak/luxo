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

Future<Map<String, String>> analyzeSource(String source) async {
  final fixture = await SelectFixture.create({'lib/fixture.dart': source});
  try {
    final unit = await fixture.unit('lib/fixture.dart');
    final analyzer = SelectAnalyzer({
      'User': {'id': 'int', 'name': 'String'},
    });
    unit.unit.accept(analyzer);
    return analyzer.buildSelectStrings();
  } finally {
    await fixture.close();
  }
}
