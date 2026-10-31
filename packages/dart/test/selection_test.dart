import 'dart:typed_data';

import 'package:test/test.dart';
import 'package:luxo_client/src/codec.dart';
import 'package:luxo_client/src/error.dart';
import 'package:luxo_client/src/selection.dart';
import 'package:luxo_client/src/schema.dart';
import 'package:luxo_client/src/schema_ids.dart';

APISchemaEntry schema(int id) => snapshotAPISchema({
  'api': APISchemaEntry(
    1,
    [],
    {'user': SelectionFieldSchema(1, 'User')},
    {
      'User': {'id': SelectionFieldSchema(id), 'name': SelectionFieldSchema(2)},
    },
  ),
})['api']!;

List<int> encode(APISchemaEntry meta, String text, [PreparedSelection? plan]) {
  final encoder = LuxoEncoder();
  writeSelectionMask(encoder, meta, text, plan: plan);
  return encoder.bytes();
}

void main() {
  test('rejects trailing separators in dynamic and prepared selections', () {
    for (final text in [
      'user,',
      'user,  ',
      'user{id,}',
      'user{id, }',
      'user{id},',
    ]) {
      expect(() => PreparedSelection(text), throwsA(isA<LuxoError>()));
      expect(() => encode(schema(1), text), throwsA(isA<LuxoError>()));
    }
  });
  test('sparse maximum IDs and nested nodes preserve exact masks', () {
    final meta = schema(65535);
    const text = 'user{name,id}';
    final leaf = LuxoEncoder()
      ..writeVarint(8192)
      ..writeRawBytes(fieldMaskSet(fieldMaskSet(Uint8List(0), 2), 65535));
    final node = LuxoEncoder()
      ..writeVarint(1)
      ..writeRawBytes(Uint8List.fromList([1]))
      ..writeVarint(1)
      ..writeVarint(leaf.bytes().length)
      ..writeRawBytes(leaf.bytes());
    final expected = LuxoEncoder()
      ..writeVarint(node.bytes().length)
      ..writeRawBytes(node.bytes());
    final plan = PreparedSelection(text);
    expect(encode(meta, text), expected.bytes());
    expect(encode(meta, text, plan), expected.bytes());
    expect(encode(meta, text, plan), expected.bytes());
  });

  test('prepared selections preserve wire and isolate Schema identities', () {
    final plan = PreparedSelection('user{id}');
    for (final meta in [schema(1), schema(3)]) {
      final expected = encode(meta, plan.text);
      for (var i = 0; i < 3; i++) {
        final bytes = encode(meta, plan.text, plan);
        expect(bytes, expected);
        bytes.fillRange(0, bytes.length, 255);
      }
    }
  });
  test('manual text overrides the prepared selection', () {
    final meta = schema(1);
    final plan = PreparedSelection('user{id}');
    expect(encode(meta, 'user{name}', plan), encode(meta, 'user{name}'));
  });
  test('mutable external metadata is never cached', () {
    final fields = {'id': const SelectionFieldSchema(1)};
    final meta = APISchemaEntry(1, [], fields);
    final plan = PreparedSelection('id');
    final before = encode(meta, 'id', plan);
    fields['id'] = const SelectionFieldSchema(2);
    expect(encode(meta, 'id', plan), isNot(before));
  });
  test('prepared selections retain syntax and field validation', () {
    expect(() => PreparedSelection('user{'), throwsA(anything));
    expect(() => PreparedSelection('id,id'), throwsA(anything));
    expect(
      () => encode(schema(1), 'missing', PreparedSelection('missing')),
      throwsA(anything),
    );
  });
}
