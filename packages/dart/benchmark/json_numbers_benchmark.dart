import 'dart:convert';

import 'package:luxo_client/luxo_client.dart';
import 'package:luxo_client/src/json.dart' show JSONRequestEncoder;

import 'uint64_benchmark.dart' show compareBenchmarks;

typedef JSONNumberCodec = ({
  Object? Function(String) read,
  Object? Function(String) readTyped,
  Object? Function(Object?) normalize,
  String Function(Object?) write,
  Object Function(Object?) integer,
  double Function(Object?) floating,
});

const _iterations = 1000;
Object? _sink;

void main() => measureJSONArrayNullability();

/// Construct warmed failure operations without timing schema or input creation.
Map<String, (String Function(), int)> jsonParameterFailureCases() {
  final fixtures = <String, (int, bool, String, bool)>{
    for (final depth in [0, 1, 2, 8, 15, 16, 64, 65])
      for (final list in [false, true])
        'error${depth}${list ? 'Lists' : 'Objects'}': (
          depth,
          list,
          'next',
          false,
        ),
    'errorUnicode64': (64, false, '字段😀', false),
    'errorLongName64': (64, false, '字段${'λ' * 64}', false),
    'errorLongName8': (8, false, '字段${'λ' * 64}', false),
    'errorLongName16': (16, false, '字段${'λ' * 64}', false),
    for (final remaining in [7, 8])
      for (final length in [127, 129])
        'errorCopy${length}Depth$remaining': (
          remaining + 2,
          false,
          'x' * (length - 3),
          false,
        ),
    'errorEmptyName2': (2, false, '', false),
    'error65AndRecovery': (65, false, 'next', true),
    'error65ListsAndRecovery': (65, true, 'next', true),
  };
  return {
    for (final entry in fixtures.entries)
      entry.key: (
        _parameterFailureOperation(entry.value),
        entry.value.$1 < 8 ? 1000 : 100,
      ),
  };
}

String Function() _parameterFailureOperation(
  (int, bool, String, bool) fixture,
) {
  final (depth, list, name, recovery) = fixture;
  final (writer, params, path) = _parameterFailureFixture(depth, list, name);
  final reason = depth > 64
      ? 'model nesting exceeds 64 levels'
      : 'must be LuxoInt64';
  final message = 'invalid JSON parameter $path: $reason';
  return () {
    try {
      writer.encode('paths', params);
    } on LuxoError catch (error) {
      if (error.error != 'ConfigError' ||
          error.code != 0 ||
          error.message != message) {
        throw StateError('Unexpected parameter failure: $error');
      }
      if (!recovery) return error.message;
      final next = writer.encode('paths', const {});
      if (next != '{"\$api":"paths"}') throw StateError('Failed recovery');
      return '${error.message}\n$next';
    }
    throw StateError('Expected parameter failure');
  };
}

(JSONRequestEncoder, Map<String, Object?>, String) _parameterFailureFixture(
  int depth,
  bool list,
  String name,
) {
  Object? value = depth == 0 ? true : <String, Object?>{'id': true};
  for (var level = 1; level < depth; level++) {
    value = <String, Object?>{
      name: list ? _parameterFailureList(value) : value,
    };
  }
  final writer = JSONRequestEncoder({
    'paths': APISchemaEntry(
      7,
      [
        ParamSchema(
          1,
          'value',
          depth == 0 ? 'Int' : 'Model',
          list,
          false,
          'Node',
        ),
      ],
      const {},
      {
        'Node': {
          name: SelectionFieldSchema(1, 'Node', 'Model', list),
          'id': const SelectionFieldSchema(2, null, 'Int'),
        },
      },
    ),
  });
  var path = 'value${list ? '[11]' : ''}';
  for (var level = 0; level < (depth > 64 ? 64 : depth - 1); level++) {
    final index = depth > 64 && level == 63 ? '[0]' : '[11]';
    path += '.$name${list ? index : ''}';
  }
  if (depth > 0 && depth <= 64) path += '.id';
  return (
    writer,
    {'value': list ? _parameterFailureList(value, integer: depth == 0) : value},
    path,
  );
}

List<Object?> _parameterFailureList(Object? value, {bool integer = false}) => [
  ...List<Object?>.generate(
    11,
    (_) => integer ? LuxoInt64.zero : <String, Object?>{},
  ),
  value,
];

/// Compare emitted list readers on non-null inputs accepted by both versions.
void measureJSONArrayNullability() {
  for (final count in [1, 16, 128]) {
    final values = List<Object>.generate(
      count,
      (index) => {
        'n': BigInt.parse('123456789012345678901234567890'),
        'i': index,
      },
    );
    final encoder = LuxoEncoder()..writeVarint(values.length);
    for (final value in values) {
      encoder.writeBytes(utf8.encode(encodeLuxoJSON(value)));
    }
    final bytes = encoder.bytes();
    final cases = <String, (Object? Function(), Object? Function())>{
      'binary list $count': (
        () =>
            LuxoDecoder(bytes)
                .readBytesArrayChecked()
                .map((v) => decodeLuxoJSON(utf8.decode(v)) as Object)
                .toList(),
        () =>
            LuxoDecoder(bytes)
                .readBytesArrayChecked()
                .map((v) => decodeLuxoJSON(utf8.decode(v)))
                .toList(),
      ),
      'JSON field list $count': (
        () => values.map((e) => normalizeLuxoJSON(e) as Object).toList(),
        () => values.map(normalizeLuxoJSON).toList(),
      ),
      'JSON API list + traversal $count': (
        () => _readJSONList((normalizeLuxoJSON(values) as List).cast<Object>()),
        () =>
            _readJSONList((normalizeLuxoJSON(values) as List).cast<Object?>()),
      ),
    };
    for (final entry in cases.entries) {
      final (before, after) = entry.value;
      if (encodeLuxoJSON(before()) != encodeLuxoJSON(after())) {
        throw StateError('Comparable JSON list changed');
      }
      _compare(entry.key, before, after);
    }
  }
  print('sink present: ${_sink != null}; no network or allocation measurement');
}

int _readJSONList(List<Object?> values) {
  var sum = 0;
  for (final value in values) {
    sum += (value! as Map<String, Object?>)['i']! as int;
  }
  return sum;
}

/// The caller supplies unmodified before/after snapshots compiled together.
void measureJSONNumbers(JSONNumberCodec before, JSONNumberCodec after) {
  const row =
      '{"id":9223372036854775807,"amount":1.25,"nested":{"ratio":0.125}}';
  final fixtures = {
    'smallInteger': '{"id":9223372036854775807}',
    'smallFraction': '{"amount":1.25}',
    'nested16': '[${List.filled(16, row).join(',')}]',
  };
  for (final entry in fixtures.entries) {
    final text = entry.value;
    if (before.write(before.read(text)) != after.write(after.read(text))) {
      throw StateError('Comparable finite fixture changed');
    }
    _compare(
      'generic read ${entry.key}',
      () => before.read(text),
      () => after.read(text),
    );
    _compare(
      'envelope generic field ${entry.key}',
      () => before.normalize(before.readTyped(text)),
      () => after.normalize(after.readTyped(text)),
    );
    _compare(
      'forward ${entry.key}',
      () => before.write(before.read(text)),
      () => after.write(after.read(text)),
    );
  }
  _compare(
    'typed Int and Float response',
    () => _typed(before, row),
    () => _typed(after, row),
  );
  print(
    'sink present: ${_sink != null}; parsing only, no network or allocation measurements',
  );
}

/// Compare unchanged JSON trees with caller-supplied runtime snapshots.
void measureJSONNormalization(
  Object? Function(Object?) before,
  Object? Function(Object?) after,
) {
  final fixtures = <String, Object?>{};
  for (final broad in [false, true]) {
    for (final depth in [64, 128]) {
      Object? value = true;
      for (var index = 0; index < depth; index++) {
        value = broad
            ? <Object?, Object?>{'next': value}
            : <String, Object?>{'next': value};
      }
      fixtures['${broad ? 'broad' : 'typed'} map $depth'] = value;
    }
  }
  fixtures['wide map 1000'] = <String, Object?>{
    for (var index = 0; index < 1000; index++) 'key$index': true,
  };
  for (final entry in fixtures.entries) {
    if (!identical(before(entry.value), entry.value) ||
        !identical(after(entry.value), entry.value)) {
      throw StateError('Normalization must preserve the original JSON tree');
    }
    _compare(
      'normalize ${entry.key}',
      () => before(entry.value),
      () => after(entry.value),
    );
  }
}

Object _typed(JSONNumberCodec codec, String text) {
  final value = codec.readTyped(text) as Map<String, Object?>;
  return (codec.integer(value['id']), codec.floating(value['amount']));
}

/// Supply fixed before/after codecs; check key quoting before measuring.
void measureJSONKeyWrites(
  String Function(Object?) before,
  String Function(Object?) after,
) {
  final fixtures = <String, (Object?, int)>{
    'short ASCII key': ({'fieldName': true}, _iterations),
    '64 code unit key': ({'k' * 64: true}, _iterations),
    '65 code unit key': ({'k' * 65: true}, _iterations),
    'escaped key': (
      {
        String.fromCharCodes([34, 92, 0, 31]): true,
      },
      _iterations,
    ),
    'Unicode key': ({'字段😀': true}, _iterations),
    'ASCII key 72KiB': ({'a' * 73728: true}, 10),
    'Unicode key 72KiB': ({'汉' * 24576: true}, 10),
    'tail escape key 72KiB': (
      {'${'a' * 73727}${String.fromCharCode(10)}': true},
      10,
    ),
    'string fields 128': (
      <String, Object?>{
        for (var index = 0; index < 128; index++) 'key$index': 'value$index',
      },
      _iterations,
    ),
  };
  for (final entry in fixtures.entries) {
    final (value, count) = entry.value;
    if (before(value) != after(value)) {
      throw StateError('JSON key wire changed');
    }
    _compare(
      'write ${entry.key}',
      () => before(value),
      () => after(value),
      iterations: count,
    );
  }
}

void _compare(
  String name,
  Object? Function() before,
  Object? Function() after, {
  int iterations = _iterations,
}) {
  compareBenchmarks(
    name,
    () => _repeat(before, iterations),
    () => _repeat(after, iterations),
    iterations: iterations,
    includeRange: true,
  );
}

int _repeat(Object? Function() read, int iterations) {
  for (var index = 0; index < iterations; index++) _sink = read();
  return _sink == null ? 0 : 1;
}
