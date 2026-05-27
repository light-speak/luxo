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

  /// Writes a fixed 16-byte UUID (no length prefix) from a canonical
  /// "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" string.
  void writeUuid(String v) {
    _ensureCapacity(16);
    parseUuidInto(v, _buf, _pos);
    _pos += 16;
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

  /// Writes field header + fixed 16-byte UUID value.
  void writeFieldUuid(int fieldID, String v) {
    writeVarint(fieldID);
    writeUuid(v);
  }

  // --- Array field writers ---
  // Array layout: [varint count][item0][item1]... (count uses plain varint).

  /// Writes field header + count-prefixed int64 array (zigzag items).
  void writeFieldIntArray(int fieldID, List<int> v) {
    writeVarint(fieldID);
    writeVarint(v.length);
    for (final e in v) {
      writeSvarint(e);
    }
  }

  /// Writes field header + count-prefixed float64 array (fixed64 items).
  void writeFieldFloatArray(int fieldID, List<double> v) {
    writeVarint(fieldID);
    writeVarint(v.length);
    for (final e in v) {
      writeFixed64(e);
    }
  }

  /// Writes field header + count-prefixed string array (length-prefixed items).
  void writeFieldStringArray(int fieldID, List<String> v) {
    writeVarint(fieldID);
    writeVarint(v.length);
    for (final e in v) {
      writeString(e);
    }
  }

  /// Writes field header + count-prefixed bool array (1 byte per item).
  void writeFieldBoolArray(int fieldID, List<bool> v) {
    writeVarint(fieldID);
    writeVarint(v.length);
    for (final e in v) {
      writeBool(e);
    }
  }

  /// Writes field header + count-prefixed UUID array (16-byte items).
  void writeFieldUuidArray(int fieldID, List<String> v) {
    writeVarint(fieldID);
    writeVarint(v.length);
    for (final e in v) {
      writeUuid(e);
    }
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

  /// Reads a fixed 16-byte UUID and formats it as a canonical
  /// "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" string.
  String readUuid() {
    if (_off + 16 > _data.lengthInBytes) {
      error = 'truncated uuid at offset $_off';
      return '';
    }
    final s = formatUuid(_bytes, _off);
    _off += 16;
    return s;
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

  /// Reads a nullable UUID. Returns null if the null marker is present.
  String? readUuidPtr() {
    if (!_readNullableFlag()) return null;
    return readUuid();
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

  /// Reads a count-prefixed bool array.
  List<bool> readBoolArray() {
    final count = _readVarint();
    if (count < 0) {
      error = 'invalid array header at offset $_off';
      return const [];
    }
    final result = <bool>[];
    for (var i = 0; i < count; i++) {
      result.add(readBool());
      if (error != null) return const [];
    }
    return result;
  }

  /// Reads a count-prefixed UUID array (16-byte items → canonical strings).
  List<String> readUuidArray() {
    final count = _readVarint();
    if (count < 0) {
      error = 'invalid array header at offset $_off';
      return const [];
    }
    final result = <String>[];
    for (var i = 0; i < count; i++) {
      result.add(readUuid());
      if (error != null) return const [];
    }
    return result;
  }

  /// Reads a count-prefixed bytes array (length-prefixed items).
  List<Uint8List> readBytesArray() {
    final count = _readVarint();
    if (count < 0) {
      error = 'invalid array header at offset $_off';
      return const [];
    }
    final result = <Uint8List>[];
    for (var i = 0; i < count; i++) {
      result.add(readBytes());
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

  /// Reads [count] fixed 16-byte UUID values as canonical strings.
  List<String> readColumnUuid() {
    final result = List<String>.filled(count, '');
    for (var i = 0; i < count; i++) {
      if (_off + 16 > _data.lengthInBytes) break;
      result[i] = formatUuid(_bytes, _off);
      _off += 16;
    }
    return result;
  }

  /// Reads [count] nullable UUID values (0x00=null, 0x01+16 bytes).
  List<String?> readColumnUuidPtr() {
    final result = List<String?>.filled(count, null);
    for (var i = 0; i < count; i++) {
      if (_off >= _data.lengthInBytes) break;
      if (_bytes[_off++] == 0x00) continue;
      if (_off + 16 > _data.lengthInBytes) break;
      result[i] = formatUuid(_bytes, _off);
      _off += 16;
    }
    return result;
  }

  /// Reads [count] length-prefixed byte blobs (one per cell).
  ///
  /// Used for scalar array columns and nested/federation columns: each cell is
  /// a length-prefixed blob. For scalar arrays the blob holds an inline
  /// [count][items...] array, decoded with a fresh [LuxoDecoder].
  List<Uint8List> readColumnBytes() {
    final result = List<Uint8List>.filled(count, _emptyBytes, growable: false);
    for (var i = 0; i < count; i++) {
      final len = _readVarint();
      if (len < 0 || _off + len > _data.lengthInBytes) break;
      result[i] = Uint8List.sublistView(_bytes, _off, _off + len);
      _off += len;
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

// --- UUID helpers ---

final Uint8List _emptyBytes = Uint8List(0);

const String _hexDigits = '0123456789abcdef';

/// Formats 16 bytes starting at [off] in [bytes] as a canonical lowercase
/// UUID string "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx".
String formatUuid(Uint8List bytes, int off) {
  // 32 hex chars + 4 dashes = 36.
  final out = Uint8List(36);
  var o = 0;
  for (var i = 0; i < 16; i++) {
    if (i == 4 || i == 6 || i == 8 || i == 10) {
      out[o++] = 0x2D; // '-'
    }
    final b = bytes[off + i];
    out[o++] = _hexDigits.codeUnitAt(b >> 4);
    out[o++] = _hexDigits.codeUnitAt(b & 0x0F);
  }
  return String.fromCharCodes(out);
}

/// Parses a canonical 36-char UUID string into 16 bytes written into [dst]
/// starting at [off]. Throws [FormatException] on malformed input.
void parseUuidInto(String s, Uint8List dst, int off) {
  if (s.length != 36) {
    throw FormatException('invalid UUID length: ${s.length}', s);
  }
  var j = 0;
  var i = 0;
  while (i < s.length) {
    final c = s.codeUnitAt(i);
    if (c == 0x2D) {
      i++;
      continue;
    }
    if (i + 1 >= s.length || j >= 16) {
      throw FormatException('malformed UUID', s);
    }
    final hi = _hexNibble(s.codeUnitAt(i));
    final lo = _hexNibble(s.codeUnitAt(i + 1));
    dst[off + j] = (hi << 4) | lo;
    j++;
    i += 2;
  }
  if (j != 16) {
    throw FormatException('malformed UUID', s);
  }
}

int _hexNibble(int c) {
  if (c >= 0x30 && c <= 0x39) return c - 0x30; // 0-9
  if (c >= 0x61 && c <= 0x66) return c - 0x61 + 10; // a-f
  if (c >= 0x41 && c <= 0x46) return c - 0x41 + 10; // A-F
  throw const FormatException('invalid hex digit in UUID');
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
