import 'dart:typed_data';

import '../lib/src/codec.dart';
import '../lib/src/int64.dart';
import '../lib/src/json.dart';
import '../lib/src/schema_ids.dart';
import '../lib/src/transport.dart';
import 'uint64_benchmark.dart' show compareBenchmarks;

const _iterations = 2000;
const _encodingIterations = 100000;
typedef _Types = Map<String, Map<String, SelectionFieldSchema>>;

void main() {
  for (final (apis, fields, types) in [(1, 2, 0), (16, 16, 0), (16, 16, 16)]) {
    final schema = _fixture(apis, fields, types);
    final before = _Registration(), after = _Registration();
    final value = LuxoInt64.fromInt(7);
    compareBenchmarks(
      'register $apis APIs x $fields fields + $types shared types',
      () => _batch(() => before.register(_previousSnapshot(schema))),
      () => _batch(() => after.register(snapshotAPISchema(schema))),
      iterations: _iterations,
      includeRange: true,
    );
    final previous = before.encodeBytes(value),
        current = after.encodeBytes(value);
    if (previous.length != current.length ||
        List.generate(
          previous.length,
          (i) => i,
        ).any((i) => previous[i] != current[i])) {
      throw StateError('wire changed');
    }
    compareBenchmarks(
      'encode control $apis APIs x $fields fields + $types shared types',
      () => _batch(() => before.encode(value), iterations: _encodingIterations),
      () => _batch(() => after.encode(value), iterations: _encodingIterations),
      iterations: _encodingIterations,
      includeRange: true,
    );
  }
}

int _batch(int Function() operation, {int iterations = _iterations}) {
  var result = 0;
  for (var i = 0; i < iterations; i++) result += operation();
  return result;
}

final class _Registration {
  var schema = <String, APISchemaEntry>{};
  final encoder = JSONRequestEncoder();
  int register(Map<String, APISchemaEntry> value) {
    encoder.setSchema(value);
    schema = value;
    return schema.length;
  }

  int encode(LuxoInt64 value) => encodeBytes(value).length;

  Uint8List encodeBytes(LuxoInt64 value) {
    final output = LuxoEncoder();
    encodeParam(output, schema['api1']!.params.single, value);
    return output.bytes();
  }
}

Map<String, APISchemaEntry> _fixture(int apis, int fields, int types) {
  final selections = {
    for (var i = 1; i <= fields; i++)
      'field$i': SelectionFieldSchema(i, null, 'Int'),
  };
  final shared = {for (var i = 0; i < types; i++) 'Type$i': selections};
  return {
    for (var i = 1; i <= apis; i++)
      'api$i': APISchemaEntry(
        i,
        const [ParamSchema(1, 'value', 'Int')],
        selections,
        shared,
      ),
  };
}

// Frozen prior container-only snapshot; use only immutable, legal benchmark inputs.
Map<String, APISchemaEntry> _previousSnapshot(
  Map<String, APISchemaEntry> schema,
) {
  final types = Map<_Types, _Types>.identity();
  return schema.map(
    (name, entry) => MapEntry(
      name,
      APISchemaEntry(
        entry.id,
        List.unmodifiable(entry.params),
        Map.unmodifiable(entry.fields),
        types.putIfAbsent(
          entry.types,
          () => Map.unmodifiable(
            entry.types.map(
              (name, fields) => MapEntry(
                name,
                Map<String, SelectionFieldSchema>.unmodifiable(fields),
              ),
            ),
          ),
        ),
      ),
    ),
  );
}
