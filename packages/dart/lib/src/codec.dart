import 'dart:convert';
import 'dart:typed_data';

/// Luxo binary wire format encoder.
///
/// Wire format matches the Go implementation exactly:
/// - Varint: unsigned LEB128 (7 bits per byte, high bit = continuation)
/// - Svarint: zigzag encoding then varint
/// - Fixed64: 8 bytes little-endian IEEE 754 float64
/// - String: varint length + UTF-8 bytes
/// - Bool: varint 0 or 1
/// - Nullable: 1 byte flag (0x00=null, 0x01=present) + value if present
/// - Field: varint fieldID + value
/// - End marker: 0x00 (fieldID 0)
class LuxoEncoder {
  Uint8List _buf;
  int _pos;

  /// Creates an encoder with optional initial capacity (default 1024).
  LuxoEncoder([int capacity = 1024])
      : _buf = Uint8List(capacity),
        _pos = 0;

  void _ensureCapacity(int needed) {
    final required = _pos + needed;
    if (required <= _buf.length) return;
    var newLen = _buf.length;
    while (newLen < required) {
      newLen <<= 1;
    }
    final newBuf = Uint8List(newLen);
    newBuf.setRange(0, _pos, _buf);
    _buf = newBuf;
  }

  /// Writes an unsigned varint (LEB128).
  void writeVarint(int v) {
    // Max 10 bytes for 64-bit varint.
    _ensureCapacity(10);
    var uv = v; // Dart int is 64-bit signed, treat bit pattern as unsigned.
    while (uv & ~0x7F != 0) {
      _buf[_pos++] = (uv & 0x7F) | 0x80;
      // Logical right shift: Dart's >> is arithmetic for negative ints,
      // so we use >>> (triple shift) which is unsigned.
      uv = uv >>> 7;
    }
    _buf[_pos++] = uv & 0x7F;
  }

  /// Writes a signed varint using zigzag encoding.
  void writeSvarint(int v) {
    writeVarint((v << 1) ^ (v >> 63));
  }

  /// Writes a float64 as 8 bytes little-endian.
  void writeFixed64(double v) {
    _ensureCapacity(8);
    final bd = ByteData(8);
    bd.setFloat64(0, v, Endian.little);
    _buf.setRange(_pos, _pos + 8, bd.buffer.asUint8List());
    _pos += 8;
  }

  /// Writes a length-prefixed UTF-8 string.
  void writeString(String v) {
    final encoded = utf8.encode(v);
    writeVarint(encoded.length);
    _ensureCapacity(encoded.length);
    _buf.setRange(_pos, _pos + encoded.length, encoded);
    _pos += encoded.length;
  }

  /// Writes a boolean as varint (0 or 1).
  void writeBool(bool v) {
    _ensureCapacity(1);
    _buf[_pos++] = v ? 1 : 0;
  }

  /// Writes length-prefixed raw bytes.
  void writeBytes(Uint8List v) {
    writeVarint(v.length);
    _ensureCapacity(v.length);
    _buf.setRange(_pos, _pos + v.length, v);
    _pos += v.length;
  }

  /// Writes a null marker (0x00).
  void writeNull() {
    _ensureCapacity(1);
    _buf[_pos++] = 0x00;
  }

  /// Writes a present marker (0x01).
  void writePresent() {
    _ensureCapacity(1);
    _buf[_pos++] = 0x01;
  }

  // --- Field writers ---

  /// Writes field header (fieldID) + signed int value.
  void writeFieldInt(int fieldID, int v) {
    writeVarint(fieldID);
    writeSvarint(v);
  }

  /// Writes field header + float64 value.
  void writeFieldFloat(int fieldID, double v) {
    writeVarint(fieldID);
    writeFixed64(v);
  }

  /// Writes field header + string value.
  void writeFieldString(int fieldID, String v) {
    writeVarint(fieldID);
    writeString(v);
  }

  /// Writes field header + bool value.
  void writeFieldBool(int fieldID, bool v) {
    writeVarint(fieldID);
    writeBool(v);
  }

  /// Writes field header + raw bytes value.
  void writeFieldBytes(int fieldID, Uint8List v) {
    writeVarint(fieldID);
    writeBytes(v);
  }

  /// Writes the end marker (fieldID 0).
  void writeEnd() {
    _ensureCapacity(1);
    _buf[_pos++] = 0x00;
  }

  /// Returns a trimmed view of the encoded bytes.
  Uint8List bytes() {
    return Uint8List.sublistView(_buf, 0, _pos);
  }

  /// Resets the encoder for reuse without reallocating.
  void reset() {
    _pos = 0;
  }
}

/// Luxo binary wire format decoder.
///
/// Usage (generated code):
/// ```dart
/// final dec = LuxoDecoder(data);
/// while (dec.nextField()) {
///   switch (dec.fieldID) {
///     case 1: event.taskId = dec.readInt();
///     case 2: event.name = dec.readString();
///   }
/// }
/// ```
class LuxoDecoder {
  final ByteData _data;
  final Uint8List _bytes;
  int _off;

  /// The current field ID after calling [nextField].
  int fieldID = 0;

  /// Any decoding error encountered.
  String? error;

  /// Creates a decoder from raw bytes. No copy is made.
  LuxoDecoder(Uint8List data)
      : _data = ByteData.sublistView(data),
        _bytes = data,
        _off = 0;

  /// Skips the arena header (totalStringLen varint) that prefixes each model's binary data.
  void skipArenaHeader() {
    _readVarint();
  }

  /// Advances to the next field. Returns false at end or on error.
  bool nextField() {
    if (error != null || _off >= _data.lengthInBytes) return false;
    final result = _readVarint();
    if (result < 0) {
      error = 'truncated varint at offset $_off';
      return false;
    }
    fieldID = result;
    return fieldID != 0; // 0 = end marker
  }

  /// Reads a signed int64 (zigzag decoded).
  int readInt() {
    final uv = _readVarint();
    // Zigzag decode: (uv >>> 1) ^ -(uv & 1)
    return (uv >>> 1) ^ -(uv & 1);
  }

  /// Reads a float64 (8 bytes little-endian).
  double readFloat() {
    if (_off + 8 > _data.lengthInBytes) {
      error = 'invalid fixed64 at offset $_off';
      return 0.0;
    }
    final v = _data.getFloat64(_off, Endian.little);
    _off += 8;
    return v;
  }

  /// Reads a length-prefixed UTF-8 string.
  String readString() {
    final length = _readVarint();
    if (length < 0 || _off + length > _data.lengthInBytes) {
      error = 'invalid string at offset $_off';
      return '';
    }
    final s = utf8.decode(Uint8List.sublistView(_bytes, _off, _off + length));
    _off += length;
    return s;
  }

  /// Reads a boolean (varint 0 or 1).
  bool readBool() {
    final v = _readVarint();
    return v != 0;
  }

  /// Reads length-prefixed raw bytes. Returns a view into the input buffer.
  Uint8List readBytes() {
    final length = _readVarint();
    if (length < 0 || _off + length > _data.lengthInBytes) {
      error = 'invalid bytes at offset $_off';
      return Uint8List(0);
    }
    final v = Uint8List.sublistView(_bytes, _off, _off + length);
    _off += length;
    return v;
  }

  // --- Nullable readers ---

  /// Reads a nullable int. Returns null if the null marker is present.
  int? readIntPtr() {
    if (!_readNullableFlag()) return null;
    return readInt();
  }

  /// Reads a nullable float64.
  double? readFloatPtr() {
    if (!_readNullableFlag()) return null;
    return readFloat();
  }

  /// Reads a nullable string.
  String? readStringPtr() {
    if (!_readNullableFlag()) return null;
    return readString();
  }

  /// Reads a nullable bool.
  bool? readBoolPtr() {
    if (!_readNullableFlag()) return null;
    return readBool();
  }

  // --- Array readers ---

  /// Reads a count-prefixed int64 array.
  List<int> readIntArray() {
    final count = _readVarint();
    if (count < 0) {
      error = 'invalid array header at offset $_off';
      return const [];
    }
    final result = <int>[];
    for (var i = 0; i < count; i++) {
      result.add(readInt());
      if (error != null) return const [];
    }
    return result;
  }

  /// Reads a count-prefixed string array.
  List<String> readStringArray() {
    final count = _readVarint();
    if (count < 0) {
      error = 'invalid array header at offset $_off';
      return const [];
    }
    final result = <String>[];
    for (var i = 0; i < count; i++) {
      result.add(readString());
      if (error != null) return const [];
    }
    return result;
  }

  /// Reads a count-prefixed float64 array.
  List<double> readFloatArray() {
    final count = _readVarint();
    if (count < 0) {
      error = 'invalid array header at offset $_off';
      return const [];
    }
    final result = <double>[];
    for (var i = 0; i < count; i++) {
      result.add(readFloat());
      if (error != null) return const [];
    }
    return result;
  }

  /// Current read offset.
  int get offset => _off;

  // --- Internal ---

  /// Reads unsigned varint (LEB128). Returns -1 on error.
  int _readVarint() {
    var v = 0;
    var shift = 0;
    while (_off < _data.lengthInBytes) {
      final b = _bytes[_off++];
      v |= (b & 0x7F) << shift;
      if (b < 0x80) return v;
      shift += 7;
      if (shift >= 64) {
        error = 'varint overflow at offset $_off';
        return -1;
      }
    }
    error = 'truncated varint at offset $_off';
    return -1;
  }

  /// Reads nullable flag byte. Returns true if value is present.
  bool _readNullableFlag() {
    if (_off >= _data.lengthInBytes) {
      error = 'invalid nullable at offset $_off';
      return false;
    }
    return _bytes[_off++] != 0;
  }

  /// Read a nullable nested model using a decoder function.
  T? readNullable<T>(T Function() decode) {
    if (!_readNullableFlag()) return null;
    return decode();
  }

  /// Read an array of items using a decoder function.
  List<T> readArray<T>(T Function() decode) {
    final count = _readVarint();
    return List.generate(count, (_) => decode());
  }
}

/// Columnar binary decoder for Luxo list responses.
///
/// Columnar format:
/// ```
/// [count varint]
/// [fieldID varint][val0][val1]...[valN]  // column 1
/// [fieldID varint][val0][val1]...[valN]  // column 2
/// ...
/// [0x00]  // end marker
/// ```
class ColumnarDecoder {
  final ByteData _data;
  final Uint8List _bytes;
  int _off;

  /// Number of rows in this columnar batch.
  int count;

  /// The current column's field ID after calling [nextColumn].
  int fieldID = 0;

  /// Creates a columnar decoder from raw bytes. Reads the row count varint.
  ColumnarDecoder(Uint8List data)
      : _data = ByteData.sublistView(data),
        _bytes = data,
        _off = 0,
        count = 0 {
    final raw = _readVarint();
    count = raw < 0 ? 0 : raw;
  }

  /// Advances to the next column. Returns false at end marker (0x00) or EOF.
  bool nextColumn() {
    if (_off >= _data.lengthInBytes) return false;
    final id = _readVarint();
    if (id < 0) return false;
    fieldID = id;
    return id != 0;
  }

  /// Reads [count] svarint values (zigzag-encoded int column).
  List<int> readColumnInt() {
    final result = List<int>.filled(count, 0);
    for (var i = 0; i < count; i++) {
      if (_off >= _data.lengthInBytes) break;
      result[i] = _readSvarint();
    }
    return result;
  }

  /// Reads [count] fixed64 float values.
  List<double> readColumnFloat() {
    final result = List<double>.filled(count, 0.0);
    for (var i = 0; i < count; i++) {
      if (_off + 8 > _data.lengthInBytes) break;
      result[i] = _data.getFloat64(_off, Endian.little);
      _off += 8;
    }
    return result;
  }

  /// Reads [count] length-prefixed string values.
  List<String> readColumnString() {
    final result = List<String>.filled(count, '');
    for (var i = 0; i < count; i++) {
      final len = _readVarint();
      if (len <= 0) {
        result[i] = '';
        continue;
      }
      if (_off + len > _data.lengthInBytes) break;
      result[i] = utf8.decode(Uint8List.sublistView(_bytes, _off, _off + len));
      _off += len;
    }
    return result;
  }

  /// Reads [count] boolean values (varint 0/1).
  List<bool> readColumnBool() {
    final result = List<bool>.filled(count, false);
    for (var i = 0; i < count; i++) {
      if (_off >= _data.lengthInBytes) break;
      final v = _readVarint();
      if (v < 0) break; // truncated
      result[i] = v != 0;
    }
    return result;
  }

  /// Reads [count] nullable int values (0x00=null, 0x01+svarint).
  List<int?> readColumnIntPtr() {
    final result = List<int?>.filled(count, null);
    for (var i = 0; i < count; i++) {
      if (_off >= _data.lengthInBytes) break;
      if (_bytes[_off++] == 0x00) continue;
      result[i] = _readSvarint();
    }
    return result;
  }

  /// Reads [count] nullable float values (0x00=null, 0x01+fixed64).
  List<double?> readColumnFloatPtr() {
    final result = List<double?>.filled(count, null);
    for (var i = 0; i < count; i++) {
      if (_off >= _data.lengthInBytes) break;
      if (_bytes[_off++] == 0x00) continue;
      if (_off + 8 > _data.lengthInBytes) break;
      result[i] = _data.getFloat64(_off, Endian.little);
      _off += 8;
    }
    return result;
  }

  /// Reads [count] nullable string values (0x00=null, 0x01+string).
  List<String?> readColumnStringPtr() {
    final result = List<String?>.filled(count, null);
    for (var i = 0; i < count; i++) {
      if (_off >= _data.lengthInBytes) break;
      if (_bytes[_off++] == 0x00) continue;
      final len = _readVarint();
      if (len <= 0) {
        result[i] = '';
        continue;
      }
      if (_off + len > _data.lengthInBytes) break;
      result[i] = utf8.decode(Uint8List.sublistView(_bytes, _off, _off + len));
      _off += len;
    }
    return result;
  }

  /// Reads [count] nullable boolean values (0x00=null, 0x01+varint).
  List<bool?> readColumnBoolPtr() {
    final result = List<bool?>.filled(count, null);
    for (var i = 0; i < count; i++) {
      if (_off >= _data.lengthInBytes) break;
      if (_bytes[_off++] == 0x00) continue;
      result[i] = _readVarint() != 0;
    }
    return result;
  }

  /// Current read position (for reading pagination metadata after 0x00).
  int get offset => _off;

  /// Reads one signed zigzag-encoded varint at current position.
  int readSvarint() => _readSvarint();

  // --- Internal ---

  int _readSvarint() {
    final uv = _readVarint();
    return (uv >>> 1) ^ -(uv & 1);
  }

  /// Reads unsigned varint (LEB128). Returns -1 on error.
  int _readVarint() {
    var v = 0;
    var shift = 0;
    while (_off < _data.lengthInBytes) {
      final b = _bytes[_off++];
      v |= (b & 0x7F) << shift;
      if (b < 0x80) return v;
      shift += 7;
      if (shift >= 64) return -1;
    }
    return -1;
  }
}

// --- Field mask utilities ---

/// Sets a bit in the field mask for the given fieldID. Returns the (possibly grown) mask.
Uint8List fieldMaskSet(Uint8List mask, int fieldID) {
  final byteIndex = fieldID >> 3; // fieldID / 8
  if (byteIndex >= mask.length) {
    // Grow to fit.
    final newMask = Uint8List(byteIndex + 1);
    newMask.setRange(0, mask.length, mask);
    mask = newMask;
  }
  mask[byteIndex] |= 1 << (fieldID & 7); // fieldID % 8
  return mask;
}

/// Checks if a bit is set in the field mask for the given fieldID.
bool fieldMaskHas(Uint8List mask, int fieldID) {
  final byteIndex = fieldID >> 3;
  if (byteIndex >= mask.length) return false;
  return (mask[byteIndex] & (1 << (fieldID & 7))) != 0;
}
