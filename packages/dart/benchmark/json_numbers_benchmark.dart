import 'dart:convert';

import 'package:luxo_client/luxo_client.dart';

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

Object _typed(JSONNumberCodec codec, String text) {
  final value = codec.readTyped(text) as Map<String, Object?>;
  return (codec.integer(value['id']), codec.floating(value['amount']));
}

void _compare(
  String name,
  Object? Function() before,
  Object? Function() after,
) {
  compareBenchmarks(
    name,
    () => _repeat(before),
    () => _repeat(after),
    iterations: _iterations,
    includeRange: true,
  );
}

int _repeat(Object? Function() read) {
  for (var index = 0; index < _iterations; index++) _sink = read();
  return _sink == null ? 0 : 1;
}
