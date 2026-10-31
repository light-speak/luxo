import 'dart:typed_data';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:luxo_client/src/int64.dart';
import 'package:luxo_client/src/schema_ids.dart';
import 'package:luxo_client/src/transport.dart';
import 'package:test/test.dart';

void main() {
  test('snapshot owns metadata and preserves shared directories', () {
    final fields = {
      'value': const SelectionFieldSchema(1, 'Input', 'Model', true, true),
    };
    final types = {'Input': fields};
    final params = [const ParamSchema(1, 'value', 'Int', true, true)];
    final source = {
      'read': APISchemaEntry(1, params, fields, types),
      'other': APISchemaEntry(2, params, fields, types),
    };
    final snapshot = snapshotAPISchema(source);
    final read = snapshot['read']!;
    expect(identical(read.fields, snapshot['other']!.fields), isTrue);
    expect(identical(read.types, snapshot['other']!.types), isTrue);
    expect(identical(read.fields, read.types['Input']), isTrue);
    expect(identical(read.fields, fields), isFalse);
    expect(read.fields['value']!.isList, isTrue);
    expect(read.fields['value']!.nullable, isTrue);
    expect(read.params.single.isList, isTrue);
    expect(read.params.single.nullable, isTrue);
    fields['value'] = const SelectionFieldSchema(2, null, 'String');
    params.clear();
    expect(read.fields['value']!.fieldID, 1);
    expect(read.params.single.type, 'Int');
    expect(() => read.fields.clear(), throwsUnsupportedError);
    expect(() => read.params.clear(), throwsUnsupportedError);
    expect(() => snapshot.clear(), throwsUnsupportedError);
    final updated = snapshotAPISchema(source);
    expect(updated['read']!.fields['value']!.fieldID, 2);
    expect(updated['read']!.params, isEmpty);
  });

  test('snapshot reads and validates each retained ID once', () {
    final param = _ReadOnceParam();
    final field = _ReadOnceField();
    final entry = _ReadOnceEntry(param, field);
    final snapshot = snapshotAPISchema({'read': entry})['read']!;
    expect(snapshot.id, 1);
    expect(snapshot.params.single.fieldID, 1);
    expect(snapshot.fields['value']!.fieldID, 1);
    expect([entry.reads, param.reads, field.reads], [1, 1, 1]);
  });

  for (final socket in [false, true]) {
    test('runtime IDs and namespaces, socket=$socket', () {
      final Transport transport = socket
          ? WsTransport('ws://localhost/luxo')
          : HttpTransport('http://localhost/luxo');
      addTearDown(transport.close);
      for (final target in ['api', 'param', 'field', 'type']) {
        final maximum = target == 'api'
            ? maxAPIID
            : target == 'param'
            ? maxParamID
            : maxSelectionFieldID;
        for (final id in [0, -1, maximum + 1]) {
          expect(
            () => transport.setSchema(_schema(target, id)),
            throwsA(isA<FormatException>()),
          );
        }
        expect(
          () => transport.setSchema(_schema(target, 1, duplicate: true)),
          throwsA(isA<FormatException>()),
        );
        expect(
          () => transport.setSchema(_schema(target, maximum)),
          returnsNormally,
        );
      }
      expect(() => transport.setSchema({}), returnsNormally);
    });
  }

  for (final mode in TransportMode.values) {
    test('failed registration preserves the $mode snapshot and plan', () async {
      final requests = <List<int>>[];
      final transport = HttpTransport(
        'http://localhost/luxo',
        options: TransportOptions(mode: mode),
        client: MockClient((request) async {
          requests.add(request.bodyBytes);
          return mode == TransportMode.binary
              ? http.Response.bytes([1], 200)
              : http.Response('{"data":true}', 200);
        }),
      );
      addTearDown(transport.close);
      transport.setSchema({
        'read': const APISchemaEntry(1, [ParamSchema(1, 'value', 'Int')]),
      });
      expect(
        () => transport.setSchema({
          'read': APISchemaEntry(9, [_ThrowingParam()]),
        }),
        throwsStateError,
      );
      final result = await transport.call<Object>(
        'read',
        params: {'value': LuxoInt64.fromInt(7)},
      );
      expect(
        mode == TransportMode.binary ? (result as Uint8List).single : result,
        mode == TransportMode.binary ? 1 : true,
      );
      if (mode == TransportMode.binary)
        expect(requests.single, [1, 0, 1, 14, 0]);
    });
  }
}

Map<String, APISchemaEntry> _schema(
  String target,
  int id, {
  bool duplicate = false,
}) {
  final fields = {
    'value': SelectionFieldSchema(id, null, 'Int'),
    if (duplicate) 'other': SelectionFieldSchema(id, null, 'Int'),
  };
  final api = APISchemaEntry(
    target == 'api' ? id : 1,
    target == 'param'
        ? [
            ParamSchema(id, 'value', 'Int'),
            if (duplicate) ParamSchema(id, 'other', 'Int'),
          ]
        : const [],
    target == 'field' ? fields : const {},
    target == 'type' ? {'Input': fields} : const {},
  );
  return {'read': api, if (target == 'api' && duplicate) 'other': api};
}

final class _ThrowingParam extends ParamSchema {
  const _ThrowingParam() : super(1, 'value', 'Int');
  @override
  String get type => throw StateError('metadata read failed');
}

final class _ReadOnceEntry extends APISchemaEntry {
  int reads = 0;
  _ReadOnceEntry(ParamSchema param, SelectionFieldSchema field)
    : super(1, [param], {'value': field});
  @override
  int get id => ++reads == 1 ? 1 : 0;
}

final class _ReadOnceParam extends ParamSchema {
  int reads = 0;
  _ReadOnceParam() : super(1, 'value', 'Int');
  @override
  int get fieldID => ++reads == 1 ? 1 : 0;
}

final class _ReadOnceField extends SelectionFieldSchema {
  int reads = 0;
  _ReadOnceField() : super(1, null, 'Int');
  @override
  int get fieldID => ++reads == 1 ? 1 : 0;
}
