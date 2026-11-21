import 'dart:convert';
import 'dart:io';

import 'package:analyzer/dart/analysis/results.dart';
import 'package:analyzer/diagnostic/diagnostic.dart';

import '../lib/src/builder/select_analyzer.dart';
import '../test/select_fixture.dart';

// Run from packages/dart. Resolution and fixture creation are outside timing.
const _models = '''
import 'package:luxo_client/luxo_client.dart';
class User(final Selected<LuxoInt64> id, final Selected<String> name);
class Comment(final Selected<User> author);
class Post(final Selected<List<Comment>> comments);
class LuxoClient {
  Future<Page<User>> getPage() async => throw UnimplementedError();
  Future<User> getUser() async => throw UnimplementedError();
  Future<List<User>> getUsers() async => throw UnimplementedError();
  Future<Post> getPost() async => throw UnimplementedError();
}
bool matches(User user) => user.name.value.isNotEmpty;
void consume(Object value) { print(value); }
extension UserLabel on User { String map() => name.value; }
extension ExtraLabels on User {
  String get displayName => name.value;
  Selected<String> get alias => name;
  String label() => name.value;
}
class Holder { Holder(User user) { print(user.name.value); } }
extension PageLabels on Page<User> { String get firstName => items.first.name.value; }
class CustomClient extends LuxoClient {
  Future<User> local({String? select}) async => throw UnimplementedError();
}
''';
const _predicate = "(user) => user.id.value.toString() == '1'";
const _users = 'final users = await client.getUsers();';
const _user = 'final user = await client.getUser();';
typedef _Result = ({Map<String, String> hints, bool escaped});
typedef _Sample = ({String name, String body, _Result expected});
_Result? _sink;

_Result _expected(String api, String fields, {bool escaped = false}) =>
    (hints: {api: fields}, escaped: escaped);

List<_Sample> _controls() => [
  (
    name: 'ordinary1',
    body: '$_user print(user.name.value);',
    expected: _expected('getUser', 'name'),
  ),
  (
    name: 'ordinary32',
    body: List.generate(
      32,
      (i) => 'final user$i = await client.getUser(); print(user$i.name.value);',
    ).join('\n'),
    expected: _expected('getUser', 'name'),
  ),
  (
    name: 'map',
    body: '$_users print(users.map((user) => user.name.value).toList());',
    expected: _expected('getUsers', 'name'),
  ),
  (name: 'empty', body: 'print(1);', expected: (hints: {}, escaped: false)),
];

List<_Sample> _collections() => [
  (
    name: 'whereMap',
    body:
        '$_users print(users.where($_predicate).map((user) => user.name.value).toList());',
    expected: _expected('getUsers', 'id,name'),
  ),
  (
    name: 'firstWhere',
    body: '$_users print(users.firstWhere($_predicate).name.value);',
    expected: _expected('getUsers', 'id,name'),
  ),
  (
    name: 'alias',
    body:
        '$_users final active = users.where($_predicate); print(active.first.name.value);',
    expected: _expected('getUsers', 'id,name'),
  ),
  (
    name: 'indexed',
    body: '$_users print(users.where($_predicate).toList()[0].name.value);',
    expected: _expected('getUsers', 'id,name'),
  ),
  (
    name: 'nested',
    body: 'final post = await client.getPost(); print(post.comments.value.where((comment) => comment.author.value.id.value.toString() == "1").first.author.value.name.value);',
    expected: _expected('getPost', 'comments{author{id,name}}'),
  ),
];

List<_Sample> _escapes() => [
  (
    name: 'externalPredicate',
    body:
        '$_users print(users.where(matches).map((user) => user.id.value).toList());',
    expected: _expected('getUsers', 'id', escaped: true),
  ),
  (
    name: 'modelNamedMap',
    body: '$_user print(user.id.value); print(user.map());',
    expected: _expected('getUser', 'id', escaped: true),
  ),
  (
    name: 'modelReturn',
    body:
        '$_users print(users.where($_predicate).map((user) => user).first.name.value);',
    expected: _expected('getUsers', 'id', escaped: true),
  ),
];

List<_Sample> _members() => [
  (
    name: 'opaqueGetter',
    body: '$_user print(user.id.value); print(user.displayName);',
    expected: _expected('getUser', 'id', escaped: true),
  ),
  (
    name: 'opaqueSelectedGetter',
    body: '$_user print(user.id.value); print(user.alias.value);',
    expected: _expected('getUser', 'id', escaped: true),
  ),
  (
    name: 'opaqueTearOff',
    body:
        '$_user print(user.id.value); final read = user.label; print(read());',
    expected: _expected('getUser', 'id', escaped: true),
  ),
  (
    name: 'opaqueConstructor',
    body: '$_user print(user.id.value); new Holder(user);',
    expected: _expected('getUser', 'id', escaped: true),
  ),
  (
    name: 'opaqueExtensionOverride',
    body: '$_user print(user.id.value); print(ExtraLabels(user).displayName);',
    expected: _expected('getUser', 'id', escaped: true),
  ),
  (
    name: 'opaquePage',
    body: 'final page = await client.getPage(); print(page.items.first.id.value); print(page.firstName);',
    expected: _expected('getPage', 'id', escaped: true),
  ),
  (
    name: 'ordinarySubclass',
    body: 'final custom = CustomClient(); final remote = await custom.getUser(); print(remote.name.value); final local = await custom.local(); print(local.name.value);',
    expected: _expected('getUser', 'name'),
  ),
];

List<_Sample> _aggregates() => [
  for (final sample in [
    (
      name: 'aggregateRecordWhere',
      body: "final rows = [(user: user,)]; final selected = rows.where((row) => row.user.id.value.toString() == '1').toList(); print(selected.first.user.name.value);",
      escaped: false,
    ),
    (
      name: 'aggregateRecordFirstWhere',
      body: "final rows = [(user: user,)]; print(rows.firstWhere((row) => row.user.id.value.toString() == '1').user.name.value);",
      escaped: false,
    ),
    (
      name: 'aggregateWidenedFirstWhere',
      body: 'final List<Object> rows = [user]; final selected = rows.firstWhere((row) => true) as User; print(selected.name.value);',
      escaped: false,
    ),
    (name: 'aggregateListEscape', body: 'consume([user]);', escaped: true),
    (
      name: 'aggregateMapEscape',
      body: "consume({'user': user});",
      escaped: true,
    ),
    (
      name: 'aggregateRecordEscape',
      body: 'consume((user: user,));',
      escaped: true,
    ),
    (
      name: 'aggregateClosureEscape',
      body: 'consume(() => user);',
      escaped: true,
    ),
    (
      name: 'aggregateForEscape',
      body: 'consume([for (final value in [user]) value]);',
      escaped: true,
    ),
    (
      name: 'aggregateListIndex',
      body: 'final rows = [user]; print(rows[0].name.value);',
      escaped: false,
    ),
    (
      name: 'aggregateMapIndex',
      body: "final row = {'user': user}; print(row['user']!.name.value);",
      escaped: false,
    ),
    (
      name: 'aggregateRecordField',
      body: 'final row = (user: user,); print(row.user.name.value);',
      escaped: false,
    ),
    (
      name: 'aggregateNested',
      body: "final row = (items: [{'user': user}],); print(row.items[0]['user']!.name.value);",
      escaped: false,
    ),
    (
      name: 'aggregateSpread',
      body: 'final original = [user]; final rows = [...original]; print(rows.first.name.value);',
      escaped: false,
    ),
    (
      name: 'aggregateRecordMap',
      body: 'final rows = [(user: user,)]; print(rows.map((row) => row.user.name.value).single);',
      escaped: false,
    ),
    (
      name: 'aggregateRecordSet',
      body: 'final rows = {(user: user,)}; print(rows.map((row) => row.user.name.value).single);',
      escaped: false,
    ),
    (
      name: 'aggregateMapValues',
      body: "final row = {'user': user}; print(row.values.map((value) => value.name.value).single);",
      escaped: false,
    ),
  ])
    (
      name: sample.name,
      body: '$_user print(user.id.value); ${sample.body}',
      expected: _expected(
        'getUser',
        sample.escaped ? 'id' : 'id,name',
        escaped: sample.escaped,
      ),
    ),
];

Future<ResolvedUnitResult> _resolve(SelectFixture fixture, String name) async {
  final path = '${fixture.root.path}/lib/$name.dart';
  final session = fixture.collection.contextFor(path).currentSession;
  final unit = await session.getResolvedUnit(path);
  if (unit is! ResolvedUnitResult) throw StateError('Cannot resolve $name');
  final errors = unit.diagnostics.where(
    (item) => item.severity == Severity.error,
  );
  if (errors.isNotEmpty)
    throw StateError('Invalid $name: ${errors.join('; ')}');
  return unit;
}

_Result _analyze(ResolvedUnitResult unit) {
  final analyzer = SelectAnalyzer({}, clientLibrary: unit.libraryElement.uri);
  unit.unit.accept(analyzer);
  return (
    hints: analyzer.buildSelectStrings(),
    escaped: analyzer.escapedCalls.isNotEmpty,
  );
}

Map<String, Object?> _json(_Result result) => {
  'hints': result.hints,
  'escaped': result.escaped,
};

void _verify(_Result actual, _Result expected) {
  if (jsonEncode(_json(actual)) != jsonEncode(_json(expected))) {
    throw StateError('Selection mismatch: $actual; expected $expected');
  }
}

double _measure(ResolvedUnitResult unit, int count) {
  final watch = Stopwatch()..start();
  for (var i = 0; i < count; i++) {
    _sink = _analyze(unit);
  }
  return watch.elapsedTicks * 1e9 / watch.frequency / count;
}

Future<Map<String, Object?>> _run(
  SelectFixture fixture,
  _Sample sample, {
  required bool smoke,
}) async {
  final unit = await _resolve(fixture, sample.name);
  _verify(_analyze(unit), sample.expected);
  final count = sample.name == 'ordinary32' ? 10 : 100;
  final warm = Stopwatch()..start();
  var batches = 0;
  if (!smoke) {
    do {
      _measure(unit, count);
      batches++;
    } while (batches < 12 || warm.elapsedMilliseconds < 200);
  }
  final warmMs = warm.elapsedMilliseconds;
  final readings = <double>[];
  if (!smoke) {
    for (var i = 0; i < 20; i++) {
      readings.add(_measure(unit, count));
      _verify(_sink!, sample.expected);
    }
  }
  final sorted = [...readings]..sort();
  return {
    'case': sample.name,
    'expected': _json(sample.expected),
    'iterations': count,
    'warmup_batches': batches,
    'warmup_ms': warmMs,
    'samples_ns': readings,
    'median_ns': sorted.isEmpty ? null : sorted[10],
  };
}

Future<void> main(List<String> args) async {
  if (args.any((arg) => arg != '--smoke')) {
    throw ArgumentError(
      'Usage: dart run benchmark/selection_analyzer_benchmark.dart [--smoke]',
    );
  }
  final samples = [
    ..._controls(),
    ..._collections(),
    ..._escapes(),
    ..._members(),
    ..._aggregates(),
  ];
  final fixture = await SelectFixture.create({
    for (final sample in samples)
      'lib/${sample.name}.dart':
          '${_models}Future<void> inspect(LuxoClient client) async { ${sample.body} }',
  });
  try {
    final rows = <Map<String, Object?>>[];
    for (final sample in samples) {
      rows.add(await _run(fixture, sample, smoke: args.contains('--smoke')));
    }
    stdout.writeln(
      jsonEncode({
        'dart': Platform.version,
        'scope': 'Fresh visitor and result over pre-resolved AST; not complete build, allocation, or SDK request runtime.',
        'rows': rows,
      }),
    );
  } finally {
    await fixture.close();
  }
}
