import 'dart:convert';
import 'dart:typed_data';

import '../lib/src/codec.dart';
import 'uint64_benchmark.dart' show compareBenchmarks;

void main() {
  final texts = <String, String>{
    'empty': '',
    'ASCII': 'release ready',
    'Unicode': '任务完成 café 東京',
    'U+FFFD': '\uFFFD',
    'U+FEFF': '\uFEFF\uFEFF任务',
    '4KiB Unicode': List.filled(455, '任务好').join(),
  };
  for (final entry in texts.entries) {
    for (final shape in ['row', 'array', 'column', 'nullable column']) {
      _compare(entry.key, entry.value, shape);
    }
  }
  print(
    'Local decoder only; no network or allocation measurement. '
    'U+FEFF baseline drops one character; those outputs differ intentionally.',
  );
}

void _compare(String name, String text, String shape) {
  final count = shape == 'row' ? 1 : 16;
  final values = List.generate(16, (index) => _payload(text, shape, index));
  final iterations = name == '4KiB Unicode' ? 100 : 2000;
  final nullable = shape == 'nullable column';
  for (var index = 0; index < values.length; index++) {
    final expected = List.generate(
      count,
      (i) => nullable && i % 4 == 0
          ? null
          : text.isEmpty
          ? ''
          : '$text$index',
    );
    if (jsonEncode(_read(values[index], shape, false)) !=
        jsonEncode(expected)) {
      throw StateError('current decoded text changed');
    }
    if (name != 'U+FEFF' &&
        jsonEncode(_read(values[index], shape, true)) != jsonEncode(expected)) {
      throw StateError('baseline decoded text changed');
    }
  }
  int repeat(bool previous) {
    var sum = 0;
    for (var i = 0; i < iterations; i++) {
      for (final text in _read(values[i & 15], shape, previous)) {
        sum += text?.length ?? 0;
      }
    }
    return sum;
  }

  compareBenchmarks(
    '$name $shape',
    () => repeat(true),
    () => repeat(false),
    iterations: iterations,
  );
}

List<String?> _read(Uint8List bytes, String shape, bool previous) {
  if (shape.endsWith('column')) {
    final decoder = previous ? _PreviousColumn(bytes) : ColumnarDecoder(bytes);
    if (!decoder.nextColumn()) throw StateError('missing column');
    final result = shape == 'nullable column'
        ? decoder.readColumnStringPtr()
        : decoder.readColumnString();
    if (decoder.nextColumn() ||
        decoder.error != null ||
        decoder.offset != bytes.length) {
      throw StateError('incomplete column');
    }
    return result;
  }
  final decoder = previous ? _PreviousRow(bytes) : LuxoDecoder(bytes);
  return shape == 'row'
      ? [decoder.readStringChecked()]
      : decoder.readStringArrayChecked();
}

Uint8List _payload(String text, String shape, int index) {
  final encoder = LuxoEncoder();
  final column = shape.endsWith('column');
  final nullable = shape == 'nullable column';
  final count = shape == 'row' ? 1 : 16;
  if (shape != 'row') encoder.writeVarint(count);
  if (column)
    encoder
      ..writeVarint(0)
      ..writeVarint(1);
  for (var i = 0; i < count; i++) {
    if (nullable) {
      encoder.writeBool(i % 4 != 0);
      if (i % 4 == 0) continue;
    }
    encoder.writeString(text.isEmpty ? '' : '$text$index');
  }
  if (column) encoder.writeEnd();
  return encoder.bytes();
}

// Freeze the previous readers; all other bounds, arrays and checks are shared.
class _PreviousRow extends LuxoDecoder {
  _PreviousRow(super.bytes);

  @override
  String readString() {
    final length = readLength('string');
    if (length < 0) return '';
    final value = utf8.decode(
      Uint8List.sublistView(wireBytes, wireOffset, wireOffset + length),
    );
    wireOffset += length;
    return value;
  }
}

class _PreviousColumn extends ColumnarDecoder {
  _PreviousColumn(super.bytes);

  @override
  List<String> readColumnString() {
    if (!requireBytes(count, 'string column')) return const [];
    final result = List<String>.filled(count, '');
    for (var i = 0; i < count; i++) {
      final len = readLength('string');
      if (len < 0) return const [];
      if (len == 0) {
        result[i] = '';
        continue;
      }
      result[i] = utf8.decode(
        Uint8List.sublistView(wireBytes, wireOffset, wireOffset + len),
      );
      wireOffset += len;
    }
    return result;
  }

  @override
  List<String?> readColumnStringPtr() {
    if (!requireBytes(count, 'nullable string column')) return const [];
    final result = List<String?>.filled(count, null);
    for (var i = 0; i < count; i++) {
      if (!readCanonicalMarker('nullable')) continue;
      final len = readLength('string');
      if (len < 0) return const [];
      if (len == 0) {
        result[i] = '';
        continue;
      }
      result[i] = utf8.decode(
        Uint8List.sublistView(wireBytes, wireOffset, wireOffset + len),
      );
      wireOffset += len;
    }
    return result;
  }
}
