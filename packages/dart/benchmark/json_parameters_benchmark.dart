import '../lib/src/int64.dart';
import '../lib/src/json.dart';
import '../lib/src/schema.dart';
import 'uint64_benchmark.dart' show compareBenchmarks;

const _iterations = 1000;
String _jsonResult = '';

void main(List<String> arguments) {
  if (arguments.contains('projection')) {
    _benchmarkProjection();
    return;
  }
  final ids = [
    LuxoInt64.zero,
    LuxoInt64.fromInt(-1),
    LuxoInt64.fromInt(1),
    LuxoInt64.parse('9007199254740993'),
    LuxoInt64.minValue,
    LuxoInt64.maxValue,
  ];
  _compare(
    'one integer',
    const APISchemaEntry(1, [ParamSchema(1, 'value', 'Int')]),
    [
      for (final id in ids) {'value': id},
    ],
  );
  final inputs = List.generate(
    16,
    (i) => _Input(
      ids[i % ids.length],
      i + 0.25,
      'input-$i',
      i.isEven ? null : 'present',
    ),
  );
  const records = APISchemaEntry(
    1,
    [ParamSchema(1, 'value', 'Model', true, false, 'Input')],
    {},
    {
      'Input': {
        'id': SelectionFieldSchema(1, null, 'Int'),
        'ratio': SelectionFieldSchema(2, null, 'Float'),
        'name': SelectionFieldSchema(3, null, 'String'),
        'note': SelectionFieldSchema(4, null, 'String', false, true),
      },
    },
  );
  _compare('16 manual records', records, [
    {'value': inputs.map((input) => input.toJson()).toList()},
  ]);
  _compare('16 generated-style inputs', records, [
    {'value': inputs},
  ]);
  print(
    'Full JSON requests; excludes schema/fixture construction, network and allocation measurements.',
  );
}

void _compare(
  String name,
  APISchemaEntry schema,
  List<Map<String, Object?>> params,
) {
  final unchecked = JSONRequestEncoder();
  final checked = JSONRequestEncoder()..setSchema({'echo': schema});
  for (final input in params) {
    final previous = _previous(input);
    if (unchecked.encode('echo', input) != previous ||
        checked.encode('echo', input) != previous) {
      throw StateError('valid request bytes changed');
    }
  }
  compareBenchmarks(
    '$name: pre-fix vs integrated validation',
    () => _repeat(params, _previous),
    () => _repeat(params, (input) => checked.encode('echo', input)),
    iterations: _iterations,
  );
  compareBenchmarks(
    '$name: same builder unchecked vs checked',
    () => _repeat(params, (input) => unchecked.encode('echo', input)),
    () => _repeat(params, (input) => checked.encode('echo', input)),
    iterations: _iterations,
  );
}

int _repeat(
  List<Map<String, Object?>> inputs,
  String Function(Map<String, Object?>) encode,
) {
  var sum = 0;
  for (var i = 0; i < _iterations; i++) {
    _jsonResult = encode(inputs[i % inputs.length]);
    sum += _jsonResult.length;
  }
  return sum;
}

// Frozen pre-fix HTTP body construction, sharing the unchanged generic JSON writer.
String _previous(Map<String, Object?> params) {
  final body = <String, Object?>{r'$api': 'echo'};
  body.addAll(params);
  return encodeLuxoJSON(body);
}

class _Input implements LuxoJSONEncodable {
  final LuxoInt64 id;
  final double ratio;
  final String name;
  final String? note;
  const _Input(this.id, this.ratio, this.name, this.note);
  @override
  Map<String, Object?> toJson() => {
    'id': id,
    'ratio': ratio,
    'name': name,
    if (note != null) 'note': note,
  };
}

void _benchmarkProjection() {
  const schema = APISchemaEntry(
    1,
    [ParamSchema(1, 'value', 'Model', true, false, 'Input')],
    {},
    {
      'Input': {
        'id': SelectionFieldSchema(1, null, 'Int'),
        'ratio': SelectionFieldSchema(2, null, 'Float'),
        'name': SelectionFieldSchema(3, null, 'String'),
        'note': SelectionFieldSchema(4, null, 'String', false, true),
      },
    },
  );
  for (final count in [1, 16]) {
    final inputs = List.generate(
      count,
      (i) => _GeneratedInput(
        i.isEven ? LuxoInt64.maxValue : LuxoInt64.minValue,
        i + 0.25,
        'input-$i',
        null,
      ),
    );
    final params = <String, Object?>{'value': inputs};
    final checked = JSONRequestEncoder()..setSchema({'echo': schema});
    final expected = _previous(params);
    if (checked.encode('echo', params) != expected)
      throw StateError('projection changed JSON');
    compareBenchmarks(
      '$count inputs: former unchecked BigInt projection vs typed validated projection',
      () => _repeat([params], _previous),
      () => _repeat([params], (input) => checked.encode('echo', input)),
      iterations: _iterations,
      includeRange: true,
    );
    if (_jsonResult != expected)
      throw StateError('measured JSON result changed');
  }
  print(
    'Full JSON requests, not allocations/network. Former strict generated-input path failed; unchecked baseline is not a correctness-equivalent implementation.',
  );
}

final class _GeneratedInput extends _Input implements LuxoJSONParameters {
  const _GeneratedInput(super.id, super.ratio, super.name, super.note);
  @override
  Map<String, Object?> toJson() => {
    'id': id.toBigInt(),
    'ratio': ratio,
    'name': name,
    if (note != null) 'note': note,
  };
  @override
  Map<String, Object?> toLuxoParameters() => super.toJson();
}
