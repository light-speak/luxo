import 'dart:convert';
import 'dart:typed_data';

import 'package:fixnum/fixnum.dart' as fixnum;

import '../lib/src/codec.dart';
import '../lib/src/error.dart';
import '../lib/src/error_codec.dart';
import '../lib/src/int64.dart';
import '../lib/src/int64_web.dart' as web;
import '../lib/src/json.dart';
import '../lib/src/uint64.dart';
import '../lib/src/wire_writer.dart';
import 'clock_native.dart'
    if (dart.library.js_interop) 'clock_web.dart'
    as clock;

const _iterations = 500000;
var _sink = 0;

void main(List<String> args) {
  if (args.contains('writers')) {
    _benchmarkWriter();
    _benchmarkSignedWriter();
    print('checksum=$_sink; Web writer algorithms, not native signed codecs');
    return;
  }
  _benchmarkCounter();
  _benchmarkJSON();
  _benchmarkWriter();
  _benchmarkSignedWriter();
  _benchmarkJSONErrors();
  _benchmarkBinaryErrors();
  print(
    'checksum=$_sink; scoped helpers only, not full requests or allocations',
  );
}

void _benchmarkJSONErrors() {
  final values = List<Map<String, Object?>>.generate(
    1024,
    (i) => {
      'error': 'Rejected',
      'code': i + 128,
      'message': 'invalid',
      'traceId': 'trace',
      'data': null,
      'cause': 'detail',
    },
  );
  for (final value in values) {
    final before = _previousJSONError(value);
    final after = decodeJSONError(value, 502);
    if (before.error != after.error ||
        before.code != after.code ||
        before.message != after.message ||
        before.traceId != after.traceId ||
        before.cause != after.cause ||
        before.data != after.data) {
      throw StateError('error envelope changed');
    }
  }
  compareBenchmarks(
    'JSON error envelope only',
    () {
      var result = 0;
      for (var i = 0; i < _iterations; i++) {
        result += _previousJSONError(values[i & 1023]).code;
      }
      return result;
    },
    () {
      var result = 0;
      for (var i = 0; i < _iterations; i++) {
        result += decodeJSONError(values[i & 1023], 502).code;
      }
      return result;
    },
  );
}

// Frozen JSON error construction from the previous HTTP implementation.
LuxoError _previousJSONError(Map<String, Object?> json) => LuxoError(
  (json['error'] ?? 'Unknown') as String,
  (json['code'] ?? 0) as int,
  (json['message'] ?? '') as String,
  json['traceId'] as String?,
  normalizeLuxoJSON(json['data']),
  json['cause'] as String?,
);

void _benchmarkBinaryErrors() {
  final values = List<Uint8List>.generate(
    1024,
    (i) =>
        (LuxoEncoder()
              ..writeFieldInt(1, LuxoInt64.fromInt(i + 128))
              ..writeFieldString(2, 'Rejected')
              ..writeFieldString(3, 'invalid')
              ..writeFieldString(4, 'trace')
              ..writeFieldString(6, 'detail')
              ..writeEnd())
            .bytes(),
  );
  for (final value in values) {
    final before = _previousBinaryError(value, 502);
    final after = decodeBinaryError(value, 502);
    if (before.error != after.error ||
        before.code != after.code ||
        before.message != after.message ||
        before.traceId != after.traceId ||
        before.cause != after.cause ||
        before.data != after.data) {
      throw StateError('binary error envelope changed');
    }
  }
  compareBenchmarks(
    'Binary error envelope only',
    () {
      var result = 0;
      for (var i = 0; i < _iterations; i++) {
        result += _previousBinaryError(values[i & 1023], 502).code;
      }
      return result;
    },
    () {
      var result = 0;
      for (var i = 0; i < _iterations; i++) {
        result += decodeBinaryError(values[i & 1023], 502).code;
      }
      return result;
    },
  );
}

// Frozen pre-extraction decoder, including the previous malformed-input behavior.
LuxoError _previousBinaryError(Uint8List data, int statusCode) {
  final dec = LuxoDecoder(data);
  var code = statusCode;
  var name = 'Error';
  var message = 'HTTP $statusCode';
  String? traceId;
  Object? errorData;
  String? cause;
  var seen = 0;
  var ended = false;
  while (dec.offset < data.length) {
    if (!dec.nextField()) {
      ended = dec.error == null;
      break;
    }
    switch (dec.fieldID) {
      case 1:
        code = dec.readInt().toInt();
        seen |= 1;
      case 2:
        name = dec.readString();
        seen |= 2;
      case 3:
        message = dec.readString();
        seen |= 4;
      case 4:
        traceId = dec.readString();
      case 5:
        final raw = utf8.decode(dec.readBytes());
        try {
          errorData = decodeLuxoJSON(raw);
        } catch (_) {
          return _previousParseError(statusCode, 'invalid JSON data');
        }
      case 6:
        cause = dec.readString();
      default:
        return _previousParseError(
          statusCode,
          'unknown binary error field ${dec.fieldID}',
        );
    }
  }
  if (dec.error != null) return _previousParseError(statusCode, dec.error!);
  if (!ended) return _previousParseError(statusCode, 'missing end marker');
  if (dec.offset != data.length)
    return _previousParseError(statusCode, 'trailing bytes');
  if (seen != 7)
    return _previousParseError(statusCode, 'missing required fields');
  return LuxoError(name, code, message, traceId, errorData, cause);
}

LuxoError _previousParseError(int statusCode, String message) => LuxoError(
  'ParseError',
  statusCode,
  'invalid binary error response: $message',
);

void _benchmarkCounter() {
  final before = _PreviousIDs();
  final after = RequestIDs();
  compareBenchmarks(
    'counter',
    () {
      var result = 0;
      for (var i = 0; i < _iterations; i++) {
        result += before.next();
      }
      return result;
    },
    () {
      var result = 0;
      for (var i = 0; i < _iterations; i++) {
        result += after.next() as int;
      }
      return result;
    },
  );
}

void _benchmarkJSON() {
  final values = List<Object>.generate(1024, (i) => i + 128);
  compareBenchmarks(
    'JSON ID validation',
    () {
      var result = 0;
      for (var i = 0; i < _iterations; i++) {
        result += values[i & 1023] as int;
      }
      return result;
    },
    () {
      var result = 0;
      for (var i = 0; i < _iterations; i++) {
        result += uint64FromJSON(values[i & 1023]) as int;
      }
      return result;
    },
  );
}

void _benchmarkWriter() {
  final buffers = List.generate(16, (i) => Uint8List(32 + i * 17));
  final words = List.generate(1024, (i) => (i * 65537, i & 15));
  for (var i = 0; i < words.length; i++) {
    final (low, high) = words[i];
    final buffer = buffers[i & 15];
    final oldBuffer = Uint8List(buffer.length);
    final start = (i >> 4) & 7;
    _checkBytes(
      oldBuffer,
      _previousWords(oldBuffer, start, low, high),
      buffer,
      writeWireWords(buffer, start, low, high),
      start,
    );
  }
  compareBenchmarks(
    'shared Web int64 byte loop',
    () {
      var result = 0;
      for (var i = 0; i < _iterations; i++) {
        final (low, high) = words[i & 1023];
        final buffer = buffers[i & 15];
        final start = (i >> 4) & 7;
        result += _previousWords(buffer, start, low, high) + buffer[start];
      }
      return result;
    },
    () {
      var result = 0;
      for (var i = 0; i < _iterations; i++) {
        final (low, high) = words[i & 1023];
        final buffer = buffers[i & 15];
        final start = (i >> 4) & 7;
        result += writeWireWords(buffer, start, low, high) + buffer[start];
      }
      return result;
    },
  );
}

void _benchmarkSignedWriter() {
  for (final wide in [false, true]) {
    final buffers = List.generate(16, (i) => Uint8List(32 + i * 17));
    final decimals = List.generate(1024, (i) {
      final value = wide ? (BigInt.one << 62) + BigInt.from(i) : BigInt.from(i);
      return (i.isEven ? value : -value).toString();
    });
    final previous = decimals.map(fixnum.Int64.parseInt).toList();
    final current = decimals.map(web.LuxoInt64.parse).toList();
    for (var i = 0; i < decimals.length; i++) {
      final buffer = buffers[i & 15];
      final oldBuffer = Uint8List(buffer.length);
      final start = (i >> 4) & 7;
      _checkBytes(
        oldBuffer,
        _previousSignedWire(oldBuffer, start, previous[i]),
        buffer,
        web.writeWireInt64(buffer, start, current[i]),
        start,
      );
    }
    compareBenchmarks(
      wide ? 'Web signed wide' : 'Web signed small',
      () {
        var result = 0;
        for (var i = 0; i < _iterations; i++) {
          final buffer = buffers[i & 15];
          final start = (i >> 4) & 7;
          result +=
              _previousSignedWire(buffer, start, previous[i & 1023]) +
              buffer[start];
        }
        return result;
      },
      () {
        var result = 0;
        for (var i = 0; i < _iterations; i++) {
          final buffer = buffers[i & 15];
          final start = (i >> 4) & 7;
          result +=
              web.writeWireInt64(buffer, start, current[i & 1023]) +
              buffer[start];
        }
        return result;
      },
    );
  }
}

void _checkBytes(
  Uint8List before,
  int oldEnd,
  Uint8List after,
  int newEnd,
  int start,
) {
  if (oldEnd != newEnd) throw StateError('writer changed length');
  for (var i = start; i < oldEnd; i++) {
    if (before[i] != after[i]) throw StateError('writer changed bytes');
  }
}

// Frozen Web signed writer before extraction; includes original zigzag and byte loop.
int _previousSignedWire(Uint8List buffer, int offset, fixnum.Int64 value) {
  final low = value.toInt32().toInt().toUnsigned(32);
  final high = value.shiftRightUnsigned(32).toInt();
  final sign = (high & 0x80000000) == 0 ? 0 : -1;
  var lo = ((low << 1) ^ sign) & 0xffffffff;
  var hi = (((high << 1) | (low >>> 31)) ^ sign) & 0xffffffff;
  while (hi != 0 || lo >= 128) {
    buffer[offset++] = (lo & 127) | 128;
    lo = ((lo >>> 7) | (hi << 25)) & 0xffffffff;
    hi >>>= 7;
  }
  buffer[offset++] = lo;
  return offset;
}

// Frozen unbounded counter; compare object fields on both sides.
class _PreviousIDs {
  int value = 0;
  int next() => ++value;
}

// Frozen pre-extraction loop; no new production helper is used by this baseline.
int _previousWords(Uint8List buffer, int offset, int lo, int hi) {
  while (hi != 0 || lo >= 128) {
    buffer[offset++] = (lo & 127) | 128;
    lo = ((lo >>> 7) | (hi << 25)) & 0xffffffff;
    hi >>>= 7;
  }
  buffer[offset++] = lo;
  return offset;
}

double _measure(int Function() operation, int iterations) {
  final start = clock.nowNanoseconds();
  _sink = (_sink + operation()) & 0xffffffff;
  return (clock.nowNanoseconds() - start) / iterations;
}

void compareBenchmarks(
  String name,
  int Function() before,
  int Function() after, {
  int iterations = _iterations,
  bool includeRange = false,
}) {
  for (var i = 0; i < 8; i++) {
    _measure(before, iterations);
    _measure(after, iterations);
  }
  final previous = <double>[];
  final current = <double>[];
  for (var i = 0; i < 20; i++) {
    if (i.isEven) {
      previous.add(_measure(before, iterations));
      current.add(_measure(after, iterations));
    } else {
      current.add(_measure(after, iterations));
      previous.add(_measure(before, iterations));
    }
  }
  previous.sort();
  current.sort();
  final oldMedian = (previous[9] + previous[10]) / 2;
  final newMedian = (current[9] + current[10]) / 2;
  print(
    '$name: ${oldMedian.toStringAsFixed(3)} -> ${newMedian.toStringAsFixed(3)} ns/op checksum=$_sink',
  );
  if (includeRange) {
    print(
      '  before [${previous.first.toStringAsFixed(3)}, ${previous.last.toStringAsFixed(3)}]; '
      'after [${current.first.toStringAsFixed(3)}, ${current.last.toStringAsFixed(3)}]',
    );
  }
}
