import 'dart:typed_data';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:luxo_client/luxo_client.dart';
import 'package:test/test.dart';

class _Request extends GeneratedRequest {
  @override
  List<LuxoFilter> get filters => [_Filter()];
  @override
  List<LuxoSorter> get sorters => [_Sorter()];
  @override
  int get apiID => 7;
  @override
  void writeParameters(LuxoEncoder encoder) {
    encoder.writeVarint(3);
    encoder.writeSvarint(LuxoInt64.parse('9007199254740993'));
  }

  @override
  Map<String, dynamic> toJSONParameters() => throw StateError('JSON on Binary');
}

class _Filter extends LuxoFilter {
  _Filter()
    : super(field: 'id', op: 'eq', value: LuxoInt64.parse('9007199254740993'));
  @override
  Map<String, Object> toJson() => throw StateError('filter converted to Map');
}

class _Sorter extends LuxoSorter {
  _Sorter() : super(field: 'id', order: 'desc');
  @override
  Map<String, Object> toJson() => throw StateError('sorter converted to Map');
}

class _ControlsRequest extends _Request {
  @override
  final List<LuxoFilter> filters;
  @override
  final List<LuxoSorter> sorters;
  _ControlsRequest({this.filters = const [], this.sorters = const []});
}

class _SelectedRequest extends _ControlsRequest {
  @override
  final String? selection;
  _SelectedRequest(this.selection);
}

void main() {
  test('generated requests keep selection local to each call', () async {
    final sent = <Uint8List>[];
    final transport = HttpTransport(
      'http://localhost/luvia',
      options: const TransportOptions(mode: TransportMode.binary),
      client: MockClient((request) async {
        sent.add(request.bodyBytes);
        return http.Response.bytes([84], 200);
      }),
    );
    transport.setSchema({
      'getUser': APISchemaEntry(7, const [], const {
        'id': SelectionFieldSchema(1),
        'name': SelectionFieldSchema(2),
      }),
    });
    try {
      for (final selection in ['id', 'name', 'id,name', 'id']) {
        await transport.call<Object?>(
          'getUser',
          request: _SelectedRequest(selection),
        );
      }
      expect(sent[0], sent[3]);
      expect(sent[0].take(4), [7, 2, 1, 1]);
      expect(sent[1].take(4), [7, 2, 1, 2]);
      expect(sent[2].take(4), [7, 2, 1, 3]);
      for (final selection in [null, '', ' ', 'unknown']) {
        await expectLater(
          transport.call<Object?>(
            'getUser',
            request: _SelectedRequest(selection),
          ),
          throwsA(isA<LuxoError>()),
        );
      }
      expect(sent, hasLength(4));
    } finally {
      transport.close();
    }
  });
  test('typed controls reject invalid inputs before sending', () async {
    var sends = 0;
    final transport = HttpTransport(
      'http://localhost/luvia',
      options: TransportOptions(mode: TransportMode.binary),
      client: MockClient((request) async {
        sends++;
        return http.Response.bytes([84], 200);
      }),
    );
    transport.setSchema({'getUser': APISchemaEntry(7)});
    final valid = LuxoFilter(
      field: 'id',
      op: 'eq',
      value: LuxoInt64.fromInt(1),
    );
    final cases = [
      _ControlsRequest(
        filters: [LuxoFilter(field: 'score', op: 'eq', value: double.nan)],
      ),
      _ControlsRequest(
        filters: [LuxoFilter(field: '', op: 'eq', value: true)],
      ),
      _ControlsRequest(
        filters: [LuxoFilter(field: 'id', op: 'bad', value: true)],
      ),
      _ControlsRequest(filters: List.filled(1001, valid)),
      _ControlsRequest(
        sorters: [LuxoSorter(field: 'id', order: 'bad')],
      ),
      _ControlsRequest(
        sorters: List.filled(101, LuxoSorter(field: 'id', order: 'asc')),
      ),
    ];
    try {
      for (final request in cases) {
        await expectLater(
          transport.call<Object?>('getUser', request: request),
          throwsA(isA<LuxoError>()),
        );
      }
      expect(sends, 0);
    } finally {
      transport.close();
    }
  });
  test('generated Binary request bypasses parameter schema and JSON', () async {
    Uint8List? sent;
    final transport = HttpTransport(
      'http://localhost/luvia',
      options: TransportOptions(mode: TransportMode.binary),
      client: MockClient((request) async {
        sent = request.bodyBytes;
        return http.Response.bytes([84], 200);
      }),
    );
    transport.setSchema({'getUser': APISchemaEntry(7)});
    try {
      await transport.call<Object?>('getUser', request: _Request());
      final generated = sent;
      transport.setSchema({
        'getUser': APISchemaEntry(7, [ParamSchema(3, 'id', 'Int')]),
      });
      await transport.call<Object?>(
        'getUser',
        params: {
          'id': LuxoInt64.parse('9007199254740993'),
          r'$filters': [
            {
              'field': 'id',
              'op': 'eq',
              'value': LuxoInt64.parse('9007199254740993'),
            },
          ],
          r'$sorters': [
            {'field': 'id', 'order': 'desc'},
          ],
        },
      );
      expect(generated, sent);
    } finally {
      transport.close();
    }
  });
}
