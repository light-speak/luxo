part of 'codec_metadata_benchmark.dart';

// Frozen pre-bounds row/column readers, used only with valid benchmark fixtures.
// Retain cursor/DataView layout and old checks. Integer primitives and unsigned
// metadata parsing are shared and unchanged; this is not a released-SDK baseline.
class _PreviousRow extends WireReader {
  final ByteData _data;
  int fieldID = 0;
  _PreviousRow(Uint8List data)
    : _data = ByteData.sublistView(data),
      super(data);

  void skipArenaHeader() {
    _readVarint();
  }

  int _readVarint() => readUnsigned();
  LuxoInt64 readInt() => int64wire.readWireInt64(this);

  bool nextField() {
    if (error != null || wireOffset >= _data.lengthInBytes) return false;
    final result = _readVarint();
    if (result < 0) {
      error = 'truncated varint at offset $wireOffset';
      return false;
    }
    fieldID = result;
    return fieldID != 0;
  }

  List<LuxoInt64> readIntArray() {
    final count = _readVarint();
    if (count < 0) {
      error = 'invalid array header at offset $wireOffset';
      return const [];
    }
    final result = <LuxoInt64>[];
    for (var i = 0; i < count; i++) {
      result.add(readInt());
      if (error != null) return const [];
    }
    return result;
  }

  bool readBool() {
    final v = _readVarint();
    if (v == 0) return false;
    if (v == 1) return true;
    error ??= 'invalid bool marker at offset ${wireOffset - 1}';
    return false;
  }

  List<bool> readBoolArray() {
    final count = _readVarint();
    if (count < 0) {
      error = 'invalid array header at offset $wireOffset';
      return const [];
    }
    final result = <bool>[];
    for (var i = 0; i < count; i++) {
      result.add(readBool());
      if (error != null) return const [];
    }
    return result;
  }
}

class _PreviousColumn extends WireReader {
  final ByteData _data;
  int count = 0;
  int arenaSize = 0;
  int fieldID = 0;
  _PreviousColumn(Uint8List data)
    : _data = ByteData.sublistView(data),
      super(data) {
    final raw = _readVarint();
    count = raw < 0 ? 0 : raw;
    final arena = _readVarint();
    arenaSize = arena < 0 ? 0 : arena;
  }

  int _readVarint() => readUnsigned();
  LuxoInt64 _readSvarint() => int64wire.readWireInt64(this);

  bool nextColumn() {
    fieldID = _readVarint();
    return fieldID > 0;
  }

  List<LuxoInt64> readColumnInt() {
    if (!_intColumnFits()) return const [];
    final result = List<LuxoInt64>.filled(count, LuxoInt64.zero);
    for (var i = 0; i < count; i++) {
      result[i] = _readSvarint();
      if (error != null) break;
    }
    return result;
  }

  bool _intColumnFits() {
    if (count <= wireBytes.length - wireOffset) return true;
    error ??= 'truncated int64 column at offset $wireOffset';
    return false;
  }

  List<double> readColumnFloat() {
    final result = List<double>.filled(count, 0.0);
    for (var i = 0; i < count; i++) {
      if (wireOffset + 8 > _data.lengthInBytes) break;
      result[i] = _data.getFloat64(wireOffset, Endian.little);
      wireOffset += 8;
    }
    return result;
  }

  List<String> readColumnString() {
    final result = List<String>.filled(count, '');
    for (var i = 0; i < count; i++) {
      final len = _readVarint();
      if (len <= 0) {
        result[i] = '';
        continue;
      }
      if (wireOffset + len > _data.lengthInBytes) break;
      result[i] = utf8.decode(
        Uint8List.sublistView(wireBytes, wireOffset, wireOffset + len),
      );
      wireOffset += len;
    }
    return result;
  }
}
