import 'package:luxo_client/luxo_client.dart';

import 'uint64_benchmark.dart' show compareBenchmarks;

const _iterations = 10000;
Object? _sink;

// Concrete type arguments match generated code; generic constructor benchmarks
// would add type-argument dispatch that the generated expressions do not have.
List<String> _previousStrings(List values) => values.cast<String>();
List<bool> _previousBooleans(List values) => values.cast<bool>();
List<String> _strings(List values) => List<String>.from(values);
List<bool> _booleans(List values) => List<bool>.from(values);
List<String> _mapStrings(List values) =>
    values.map((value) => value as String).toList();
List<bool> _mapBooleans(List values) =>
    values.map((value) => value as bool).toList();
List<String> _generateStrings(List values) => List<String>.generate(
  values.length,
  (index) => values[index] as String,
  growable: true,
);
List<bool> _generateBooleans(List values) => List<bool>.generate(
  values.length,
  (index) => values[index] as bool,
  growable: true,
);

void main() => _run('checked', luxoStringListFromJSON, luxoBooleanListFromJSON);

void compareCandidates() {
  _run('from', _strings, _booleans);
  _run('map', _mapStrings, _mapBooleans);
  _run('generate', _generateStrings, _generateBooleans);
}

void _run(
  String method,
  List<String> Function(List) strings,
  List<bool> Function(List) booleans,
) {
  for (final count in [0, 1, 16, 128]) {
    _compare<String>(
      '$method String',
      count,
      ['', '发布 café', 'false'],
      (value) => value.length,
      _previousStrings,
      strings,
    );
    _compare<bool>(
      '$method Boolean',
      count,
      [true, false],
      (value) => value ? 1 : 0,
      _previousBooleans,
      booleans,
    );
  }
  print(
    'sink present: ${_sink != null}; list validation/materialization only; allocations not measured',
  );
}

void _compare<T>(
  String name,
  int count,
  List<T> values,
  int Function(T) weight,
  List<T> Function(List) previous,
  List<T> Function(List) current,
) {
  // Match the erased element type produced by the actual JSON decoder.
  final source = decodeLuxoJSON(
    encodeLuxoJSON(
      List<T>.generate(count, (index) => values[index % values.length]),
    ),
  ) as List;
  List<T> before() => previous(source);
  List<T> after() => current(source);
  if (encodeLuxoJSON(before()) != encodeLuxoJSON(after()))
    throw StateError('Values changed');
  for (final traverse in [false, true]) {
    compareBenchmarks(
      '$name $count ${traverse ? 'decode+consume' : 'decode'}',
      () => _batch(before, weight, traverse),
      () => _batch(after, weight, traverse),
      iterations: _iterations,
      includeRange: true,
    );
  }
}

int _batch<T>(
  List<T> Function() decode,
  int Function(T) weight,
  bool traverse,
) {
  var total = 0;
  for (var index = 0; index < _iterations; index++) {
    final result = decode();
    _sink = result;
    if (traverse) {
      for (final item in result) {
        total += weight(item);
      }
    } else {
      total += result.length;
    }
  }
  return total;
}
