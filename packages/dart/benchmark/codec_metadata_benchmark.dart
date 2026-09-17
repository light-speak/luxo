import 'dart:convert';
import 'dart:typed_data';

import '../lib/src/codec.dart';
import '../lib/src/int64.dart';
import '../lib/src/int64_native.dart'
    if (dart.library.js_interop) '../lib/src/int64_web.dart'
    as int64wire;
import '../lib/src/json.dart';
import '../lib/src/wire_reader.dart';
import 'uint64_benchmark.dart' show compareBenchmarks;

part 'codec_metadata_baseline.dart';

const _iterations = 2000;
Object? _metadataSink;

enum _Kind { array, cell, boolean, integer, floating, string }

void main() {
  final row = LuxoEncoder()..writeVarint(0);
  for (final id in [1, 128, 16384, 2097152]) {
    row.writeFieldInt(id, LuxoInt64.minValue);
  }
  row.writeEnd();
  final rowData = row.bytes();
  _compare(
    'row: 4 fields',
    () => _rowBefore(rowData),
    () => _rowAfter(rowData),
  );
  for (final count in [1, 16, 128]) {
    for (final kind in _Kind.values) {
      final data = _fixture(count, kind);
      _compare(
        '${kind.name}: $count',
        () => _before(data, kind),
        () => _after(data, kind),
      );
    }
  }
  _benchmarkCompleteScalars();
  print(
    'Decoder construction, arrays and cells only; not network, allocations or release gates.',
  );
}

typedef _ScalarReader = Object Function(LuxoDecoder);

final _scalarCases = <(String, LuxoEncoder, _ScalarReader, _ScalarReader)>[
  (
    'Int',
    LuxoEncoder()..writeSvarint(LuxoInt64.minValue),
    (d) => d.readInt(),
    (d) => d.readIntChecked(),
  ),
  (
    'Float',
    LuxoEncoder()..writeFixed64(1.5),
    (d) => d.readFloat(),
    (d) => d.readFloatChecked(),
  ),
  (
    'String',
    LuxoEncoder()..writeString('发布'),
    (d) => d.readString(),
    (d) => d.readStringChecked(),
  ),
  (
    'Bool',
    LuxoEncoder()..writeBool(true),
    (d) => d.readBool(),
    (d) => d.readBoolChecked(),
  ),
  (
    'DateTime',
    LuxoEncoder()..writeSvarint(LuxoInt64.zero),
    (d) => d.readDateTime(),
    (d) => d.readDateTimeChecked(),
  ),
  (
    'UUID',
    LuxoEncoder()..writeUuid('00112233-4455-6677-8899-aabbccddeeff'),
    (d) => d.readUuid(),
    (d) => d.readUuidChecked(),
  ),
  (
    'Bytes',
    LuxoEncoder()..writeBytes(Uint8List.fromList([1, 255])),
    (d) => d.readBytes(),
    (d) => d.readBytesChecked(),
  ),
];
void _benchmarkCompleteScalars() {
  for (final (name, encoder, before, after) in _scalarCases) {
    final bytes = encoder.bytes();
    _compare(
      'complete $name',
      () => before(LuxoDecoder(bytes)),
      () => after(LuxoDecoder(bytes)),
    );
  }
}

Uint8List _fixture(int count, _Kind kind) {
  final encoder = LuxoEncoder()..writeVarint(count);
  final column =
      kind == _Kind.integer || kind == _Kind.floating || kind == _Kind.string;
  if (column) {
    encoder
      ..writeVarint(0)
      ..writeVarint(1);
  }
  for (var index = 0; index < count; index++) {
    switch (kind) {
      case _Kind.array || _Kind.cell || _Kind.integer:
        encoder.writeSvarint(
          index.isEven ? LuxoInt64.minValue : LuxoInt64.maxValue,
        );
      case _Kind.boolean:
        encoder.writeBool(index.isOdd);
      case _Kind.floating:
        encoder.writeFixed64(index + 0.5);
      case _Kind.string:
        encoder.writeString(index.isEven ? '' : '发布检查');
    }
  }
  if (column) encoder.writeEnd();
  return encoder.bytes();
}

Object _before(Uint8List data, _Kind kind) => switch (kind) {
  _Kind.array || _Kind.cell => _PreviousRow(data).readIntArray(),
  _Kind.boolean => _PreviousRow(data).readBoolArray(),
  _Kind.integer => (_PreviousColumn(data)..nextColumn()).readColumnInt(),
  _Kind.floating => (_PreviousColumn(data)..nextColumn()).readColumnFloat(),
  _Kind.string => (_PreviousColumn(data)..nextColumn()).readColumnString(),
};

Object _after(Uint8List data, _Kind kind) => switch (kind) {
  _Kind.array => LuxoDecoder(data).readIntArray(),
  _Kind.cell => LuxoDecoder(data).readIntArrayChecked(),
  _Kind.boolean => LuxoDecoder(data).readBoolArray(),
  _Kind.integer => (ColumnarDecoder(data)..nextColumn()).readColumnInt(),
  _Kind.floating => (ColumnarDecoder(data)..nextColumn()).readColumnFloat(),
  _Kind.string => (ColumnarDecoder(data)..nextColumn()).readColumnString(),
};

int _rowBefore(Uint8List data) {
  final decoder = _PreviousRow(data)..skipArenaHeader();
  var result = 0;
  while (decoder.nextField()) {
    result += decoder.fieldID + (decoder.readInt().isNegative ? 1 : 0);
  }
  if (decoder.error != null) throw StateError(decoder.error!);
  return result;
}

int _rowAfter(Uint8List data) {
  final decoder = LuxoDecoder(data)..skipArenaHeader();
  var result = 0;
  while (decoder.nextField()) {
    result += decoder.fieldID + (decoder.readInt().isNegative ? 1 : 0);
  }
  if (decoder.error != null) throw StateError(decoder.error!);
  return result;
}

int _repeat(Object Function() operation) {
  var checksum = 0;
  for (var i = 0; i < _iterations; i++) {
    final value = operation();
    _metadataSink = value;
    checksum += switch (value) {
      List<Object?>() => value.length,
      int() => value,
      _ => 1,
    };
  }
  return checksum & 0xffffffff;
}

void _compare(String name, Object Function() before, Object Function() after) {
  final expected = encodeLuxoJSON(before());
  if (expected != encodeLuxoJSON(after())) {
    throw StateError('$name: unequal valid output');
  }
  compareBenchmarks(
    name,
    () => _repeat(before),
    () => _repeat(after),
    iterations: _iterations,
    includeRange: true,
  );
  if (encodeLuxoJSON(_metadataSink) != expected) {
    throw StateError('$name: measured result changed');
  }
}
