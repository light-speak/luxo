import '../lib/src/codec.dart';
import 'uint64_benchmark.dart' show compareBenchmarks;

const _iterations = 20000;
const _values = [
  '1969-12-31T23:59:59Z',
  '1970-01-01T00:00:00Z',
  '2026-09-15T23:30:00Z',
  '2038-01-19T03:14:07Z',
];

void main() {
  for (final value in _values) {
    if (_previousSeconds(value) != dateTimeUnixSecondsFromString(value)) {
      throw StateError('The equivalent-input performance fixture changed');
    }
  }
  compareBenchmarks(
    'DateTime string to seconds',
    () => _run(_previousSeconds),
    () => _run(dateTimeUnixSecondsFromString),
    iterations: _iterations,
    includeRange: true,
  );
  print('Whole-second valid values only; no allocation or full-request claim.');
}

int _run(int Function(String) convert) {
  var result = 0;
  for (var i = 0; i < _iterations; i++) {
    result = (result + convert(_values[i & 3])) & 0xffffffff;
  }
  return result;
}

// Frozen truncating implementation is equivalent only for whole seconds here.
int _previousSeconds(String value) =>
    DateTime.parse(value).millisecondsSinceEpoch ~/ 1000;
