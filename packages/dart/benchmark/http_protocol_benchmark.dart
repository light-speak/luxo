import 'dart:convert';

import 'package:http/http.dart' as http;

import '../lib/src/int64.dart';
import '../lib/src/json.dart';
import 'uint64_benchmark.dart' show compareBenchmarks;

const _iterations = 1000;

void main() {
  _compareResponses('one full-width integer', [
    for (final value in [LuxoInt64.minValue, LuxoInt64.maxValue]) value,
  ]);
  _compareResponses('UTF-8 text', ['任务已完成 · café', 'ready']);
  _compareResponses('16 records', [
    List.generate(
      16,
      (i) => {
        'id': i.isEven ? LuxoInt64.minValue : LuxoInt64.maxValue,
        'name': '任务 $i · café',
        'ratio': i + 0.25,
      },
    ),
  ]);
  _compareHeaders();
  print(
    'Response conversion and header preparation only; excludes HTTP, '
    'transport initialization, Binary request writing and allocations.',
  );
}

void _compareResponses(String name, List<Object> payloads) {
  final responses = [
    for (final payload in payloads)
      for (final suffix in ['', '; charset=utf-8'])
        http.Response.bytes(
          utf8.encode(encodeLuxoJSON({'data': payload})),
          200,
          headers: {'content-type': 'application/json$suffix'},
        ),
  ];
  for (final response in responses) {
    if (encodeLuxoJSON(_previousResponse(response)) !=
        encodeLuxoJSON(_currentResponse(response))) {
      throw StateError('valid JSON response changed');
    }
  }
  compareBenchmarks(
    '$name: Response.body vs strict UTF-8',
    () => _repeatResponses(responses, _previousResponse),
    () => _repeatResponses(responses, _currentResponse),
    iterations: _iterations,
  );
}

// Match the old/current HTTP success conversions without timing network mocks.
Object? _previousResponse(http.Response response) {
  final body = decodeLuxoJSON(
    response.body,
    preserveNumberSyntax: true,
  ) as Map<String, dynamic>;
  return normalizeLuxoJSON(body['data']);
}

Object? _currentResponse(http.Response response) {
  final body = decodeLuxoJSON(
    utf8.decode(response.bodyBytes),
    preserveNumberSyntax: true,
  ) as Map<String, dynamic>;
  return normalizeLuxoJSON(body['data']);
}

int _repeatResponses(
  List<http.Response> responses,
  Object? Function(http.Response) decode,
) {
  var result = 0;
  for (var i = 0; i < _iterations; i++) {
    final value = decode(responses[i % responses.length]);
    if (value is List) {
      for (final row in value.cast<Map<String, dynamic>>()) {
        result += (row['name'] as String).length + row['id'].toString().length;
      }
    } else {
      result += value.toString().length;
    }
  }
  return result;
}

void _compareHeaders() {
  final sources = [
    for (var i = 0; i < 16; i++)
      {'authorization': 'Bearer token-$i', 'x-custom': 'custom-$i'},
  ];
  final cached = sources.map(_mergedHeaders).toList();
  compareBenchmarks(
    'Binary headers: per-request merge vs initialized cache',
    () => _repeatHeaders(sources, _mergedHeaders),
    () => _repeatHeaders(cached, (headers) => headers),
    iterations: _iterations,
  );
}

// Both sides use correct headers: do not benchmark the old wrong content-type
// as if it were a conforming baseline. This isolates per-request map creation.
Map<String, String> _mergedHeaders(Map<String, String> custom) => {
  ...custom,
  'content-type': 'application/x-luxo',
  'x-luxo-mode': 'binary',
};

int _repeatHeaders(
  List<Map<String, String>> headers,
  Map<String, String> Function(Map<String, String>) prepare,
) {
  var result = 0;
  for (var i = 0; i < _iterations; i++) {
    final prepared = prepare(headers[i % headers.length]);
    for (final key in [
      'content-type',
      'x-luxo-mode',
      'authorization',
      'x-custom',
    ]) {
      result += prepared[key]!.length;
    }
  }
  return result;
}
