import 'dart:convert';
import 'dart:typed_data';

import 'int64.dart';
import 'int64_native.dart'
    if (dart.library.js_interop) 'int64_web.dart'
    as int64wire;
import 'int64_validation.dart';
import 'wire_reader.dart';
import 'unicode.dart';
import 'uint64.dart' show writeWireUint64;
import 'schema.dart' show maxSelectionFieldID;

Uint8List _encodeString(String value) => utf8.encode(requireUnicode(value));

/// Luxo binary wire format encoder.
///
/// Wire format matches the Go implementation exactly:
/// - Varint: unsigned LEB128 (7 bits per byte, high bit = continuation)
/// - Svarint: zigzag encoding then varint
/// - Fixed64: 8 bytes little-endian IEEE 754 float64
/// - String: varint length + UTF-8 bytes
/// - Bool: one canonical byte, 0 or 1
/// - Nullable: 1 byte flag (0x00=null, 0x01=present) + value if present
/// - Field: varint fieldID + value
/// - End marker: 0x00 (fieldID 0)
class LuxoEncoder {
  Uint8List _buf;
  int _pos;

  /// Creates an encoder with optional initial capacity (default 1024).
  LuxoEncoder([int capacity = 1024]) : _buf = Uint8List(capacity), _pos = 0;

  void _ensureCapacity(int needed) {
    final required = _pos + needed;
    if (required <= _buf.length) return;
    var newLen = _buf.isEmpty ? 64 : _buf.length;
    while (newLen < required) {
      newLen <<= 1;
    }
    final newBuf = Uint8List(newLen);
    newBuf.setRange(0, _pos, _buf);
    _buf = newBuf;
  }

  /// Writes an unsigned varint (LEB128).
  void writeVarint(int v) {
    if (v < 0 || v > maxSafeInteger) {
      throw RangeError.range(v, 0, maxSafeInteger, 'v');
    }
    _ensureCapacity(8);
    while (v >= 128) {
      _buf[_pos++] = (v & 127) | 128;
      v = v <= 0xffffffff ? v >>> 7 : v ~/ 128;
    }
    _buf[_pos++] = v;
  }

  /// Writes a complete unsigned 64-bit value; ordinary metadata uses writeVarint.
  void writeUint64(BigInt v) {
    _ensureCapacity(10);
    _pos = writeWireUint64(_buf, _pos, v);
  }

  /// Writes a signed varint using zigzag encoding.
  void writeSvarint(LuxoInt64 v) {
    _ensureCapacity(10);
    _pos = int64wire.writeWireInt64(_buf, _pos, v);
  }

  /// Writes floor Unix seconds and the exact nanosecond fraction.
  void writeDateTime(String value) {
    final parts = dateTimePartsFromString(value);
    writeSvarint(LuxoInt64.fromInt(parts.seconds));
    writeVarint(parts.nanoseconds);
  }

  /// Writes a float64 as 8 bytes little-endian.
  void writeFixed64(double v) {
    if (!v.isFinite) throw const FormatException('Float must be finite');
    _ensureCapacity(8);
    ByteData.sublistView(_buf).setFloat64(_pos, v, Endian.little);
    _pos += 8;
  }

  /// Writes a length-prefixed UTF-8 string.
  void writeString(String v) {
    writeBytes(_encodeString(v));
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

  /// Writes a length-delimited value directly into the current buffer.
  void writeDelimited(void Function(LuxoEncoder encoder) writeValue) {
    final prefixPosition = _pos;
    _ensureCapacity(1);
    _pos++;
    final valuePosition = _pos;
    try {
      writeValue(this);
    } catch (_) {
      _pos = prefixPosition;
      rethrow;
    }
    final valueLength = _pos - valuePosition;
    final prefixLength = _varintLength(valueLength);
    final shift = prefixLength - 1;
    if (shift > 0) {
      _ensureCapacity(shift);
      _buf.setRange(valuePosition + shift, _pos + shift, _buf, valuePosition);
      _pos += shift;
    }
    _writeVarintAt(prefixPosition, valueLength);
  }

  void _writeVarintAt(int position, int value) {
    while (value >= 0x80) {
      _buf[position++] = (value & 0x7f) | 0x80;
      value ~/= 128;
    }
    _buf[position] = value;
  }

  /// Writes raw bytes without a length prefix.
  void writeRawBytes(Uint8List v) {
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
  void writeFieldInt(int fieldID, LuxoInt64 v) {
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
    final encoded = _encodeString(v);
    writeVarint(fieldID);
    writeBytes(encoded);
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
  void writeFieldIntArray(int fieldID, List<LuxoInt64> v) {
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
    final start = _pos;
    try {
      writeVarint(fieldID);
      writeVarint(v.length);
      for (final e in v) {
        writeString(e);
      }
    } catch (_) {
      _pos = start;
      rethrow;
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

  /// Writes field header + count-prefixed byte arrays.
  void writeFieldBytesArray(int fieldID, List<Uint8List> v) {
    writeVarint(fieldID);
    writeVarint(v.length);
    for (final e in v) {
      writeBytes(e);
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

int _varintLength(int value) {
  var length = 1;
  while (value >= 0x80) {
    value ~/= 128;
    length++;
  }
  return length;
}

/// Returns the UTF-8 byte length without allocating an encoded copy.
int luxoUtf8Length(String value) {
  var length = 0;
  for (var i = 0; i < value.length; i++) {
    final code = value.codeUnitAt(i);
    if (code < 0x80) {
      length++;
    } else if (code < 0x800) {
      length += 2;
    } else if (code >= 0xd800 && code <= 0xdbff && i + 1 < value.length) {
      final next = value.codeUnitAt(i + 1);
      if (next >= 0xdc00 && next <= 0xdfff) {
        length += 4;
        i++;
      } else {
        length += 3;
      }
    } else {
      length += 3;
    }
  }
  return length;
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
class LuxoDecoder extends WireReader {
  final ByteData _data;

  /// The current field ID after calling [nextField].
  int fieldID = 0;

  /// Creates a decoder from raw bytes. No copy is made.
  LuxoDecoder(Uint8List data) : _data = ByteData.sublistView(data), super(data);

  /// Skips the arena header (totalStringLen varint) that prefixes each model's binary data.
  void skipArenaHeader() {
    readBoundedUnsigned(maxArenaSize, 'arena size');
  }

  /// Advances to the next field. Returns false at end or on error.
  bool nextField() {
    if (error != null) return false;
    if (wireOffset >= _data.lengthInBytes) {
      error = 'missing row end marker';
      return false;
    }
    final result = _readVarint();
    if (result < 0) return false;
    fieldID = result;
    return fieldID != 0; // 0 = end marker
  }

  /// Reads a signed int64 (zigzag decoded).
  LuxoInt64 readInt() => int64wire.readWireInt64(this);

  /// Decodes a complete scalar response, rejecting invalid or trailing bytes.
  LuxoInt64 readIntChecked() => _checkedValue(readInt());
  double readFloatChecked() => _checkedValue(readFloat());
  String readStringChecked() => _checkedValue(readString());
  bool readBoolChecked() => _checkedValue(readBool());
  String readDateTimeChecked() => _checkedValue(readDateTime());
  String readUuidChecked() => _checkedValue(readUuid());
  Uint8List readBytesChecked() => _checkedValue(readBytes());

  /// Decodes a complete scalar-array response with the same strict boundary.
  List<LuxoInt64> readIntArrayChecked() => _checkedValue(readIntArray());
  List<double> readFloatArrayChecked() => _checkedValue(readFloatArray());
  List<String> readStringArrayChecked() => _checkedValue(readStringArray());
  List<bool> readBoolArrayChecked() => _checkedValue(readBoolArray());
  List<String> readDateTimeArrayChecked() => _checkedValue(readDateTimeArray());
  List<String> readUuidArrayChecked() => _checkedValue(readUuidArray());
  List<Uint8List> readBytesArrayChecked() => _checkedValue(readBytesArray());

  T _checkedValue<T>(T value) {
    checkComplete();
    return value;
  }

  /// Verifies a complete response or length-delimited child, never an input field.
  void checkComplete() {
    if (error != null || wireOffset != wireBytes.length) {
      throw FormatException(error ?? 'trailing binary data');
    }
  }

  /// Reads a DateTime seconds/nanoseconds pair and converts it
  /// to an ISO 8601 UTC string — matching the JSON wire representation
  /// (RFC3339). Keeps binary and JSON decoding type-consistent (both String).
  String readDateTime() => _readDateTime(this);

  /// Reads a nullable DateTime field. Returns null when the null marker is set,
  /// otherwise svarint(unix seconds) converted to an ISO 8601 UTC string.
  String? readDateTimePtr() {
    if (!_readNullableFlag()) return null;
    final value = readDateTime();
    return error == null ? value : null;
  }

  /// Reads a float64 (8 bytes little-endian).
  double readFloat() {
    if (!requireBytes(8, 'fixed64')) return 0.0;
    final v = _data.getFloat64(wireOffset, Endian.little);
    if (!acceptFloat(v)) return 0.0;
    wireOffset += 8;
    return v;
  }

  /// Reads a boolean (one canonical byte, 0 or 1).
  bool readBool() => readCanonicalMarker('bool');

  /// Reads length-prefixed raw bytes. Returns a view into the input buffer.
  Uint8List readBytes() {
    final length = readLength('bytes');
    if (length < 0) return _emptyBytes;
    final v = Uint8List.sublistView(wireBytes, wireOffset, wireOffset + length);
    wireOffset += length;
    return v;
  }

  /// Reads a fixed 16-byte UUID and formats it as a canonical
  /// "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" string.
  String readUuid() {
    if (wireOffset + 16 > _data.lengthInBytes) {
      error ??= 'truncated uuid at offset $wireOffset';
      return '';
    }
    final s = formatUuid(wireBytes, wireOffset);
    wireOffset += 16;
    return s;
  }

  // --- Nullable readers ---

  /// Reads a nullable int. Returns null if the null marker is present.
  LuxoInt64? readIntPtr() {
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
  List<LuxoInt64> readIntArray() {
    final count = readArrayCount();
    final result = <LuxoInt64>[];
    for (var i = 0; i < count; i++) {
      result.add(readInt());
      if (error != null) return const [];
    }
    return result;
  }

  /// Reads a count-prefixed DateTime array (seconds/nanoseconds pairs),
  /// converting each element to an ISO 8601 UTC string to match JSON mode.
  List<String> readDateTimeArray() {
    final count = readArrayCount(2);
    final result = <String>[];
    for (var i = 0; i < count; i++) {
      final value = readDateTime();
      if (error != null) return const [];
      result.add(value);
    }
    return result;
  }

  /// Reads a count-prefixed string array.
  List<String> readStringArray() {
    final count = readArrayCount();
    final result = <String>[];
    for (var i = 0; i < count; i++) {
      result.add(readString());
      if (error != null) return const [];
    }
    return result;
  }

  /// Reads a count-prefixed float64 array.
  List<double> readFloatArray() {
    final count = readArrayCount(8);
    final result = <double>[];
    for (var i = 0; i < count; i++) {
      result.add(readFloat());
      if (error != null) return const [];
    }
    return result;
  }

  /// Reads a count-prefixed bool array.
  List<bool> readBoolArray() {
    final count = readArrayCount();
    final result = <bool>[];
    // Preflight guarantees one byte per item; validate every marker without
    // repeating the scalar reader's cursor/error checks inside the array loop.
    for (var i = 0; i < count; i++) {
      final offset = wireOffset++;
      final marker = wireBytes[offset];
      if (marker > 1) {
        error = 'invalid bool marker at offset $offset';
        return const [];
      }
      result.add(marker == 1);
    }
    return result;
  }

  /// Reads a count-prefixed UUID array (16-byte items → canonical strings).
  List<String> readUuidArray() {
    final count = readArrayCount(16);
    final result = <String>[];
    for (var i = 0; i < count; i++) {
      result.add(readUuid());
      if (error != null) return const [];
    }
    return result;
  }

  /// Reads a count-prefixed bytes array (length-prefixed items).
  List<Uint8List> readBytesArray() {
    final count = readArrayCount();
    final result = <Uint8List>[];
    for (var i = 0; i < count; i++) {
      result.add(readBytes());
      if (error != null) return const [];
    }
    return result;
  }

  /// Current read offset.
  int get offset => wireOffset;

  // --- Internal ---

  int _readVarint() => readUnsigned();

  /// Reads nullable flag byte. Returns true if value is present.
  bool _readNullableFlag() {
    return readCanonicalMarker('nullable');
  }

  /// Read a nullable nested model using a decoder function.
  T? readNullable<T>(T Function() decode) {
    if (!_readNullableFlag()) return null;
    return decode();
  }

  /// Read an array of items using a decoder function.
  List<T> readArray<T>(T Function() decode) {
    final count = readArrayCount();
    final result = <T>[];
    for (var i = 0; i < count; i++) {
      result.add(decode());
      if (error != null) return const [];
    }
    return result;
  }
}

/// Columnar binary decoder for Luxo list responses.
///
/// Columnar format:
/// ```
/// [count varint]
/// [arena size varint]
/// [fieldID varint][val0][val1]...[valN]  // column 1
/// [fieldID varint][val0][val1]...[valN]  // column 2
/// ...
/// [0x00]  // end marker
/// ```
class ColumnarDecoder extends WireReader {
  final ByteData _data;

  /// Number of rows in this columnar batch.
  int count;

  /// Total UTF-8 string bytes advertised by the server for arena allocation.
  int arenaSize;

  /// The current column's field ID after calling [nextColumn].
  int fieldID = 0;

  /// Creates a columnar decoder from raw bytes. Reads the row count varint.
  ColumnarDecoder(Uint8List data)
    : _data = ByteData.sublistView(data),
      count = 0,
      arenaSize = 0,
      super(data) {
    count = readBoundedUnsigned(maxColumnarRecords, 'column count');
    arenaSize = readBoundedUnsigned(maxArenaSize, 'arena size');
    if (error == null && count > 0) {
      if (count > data.length - wireOffset - 2) {
        error = 'insufficient columnar element bytes';
      } else {
        final start = wireOffset;
        var id = data[start];
        if (id >= 0x80) {
          id = _readVarint();
          if (count > data.length - wireOffset - 1) {
            error ??= 'insufficient columnar element bytes';
          }
          wireOffset = start;
        }
        if (id == 0 && error == null) {
          error = 'nonempty list requires at least one column';
        }
      }
    }
  }

  /// Advances to the next column. EOF without an end marker is an error.
  bool nextColumn() {
    if (error != null) return false;
    if (wireOffset >= _data.lengthInBytes) {
      error = 'missing column end marker';
      return false;
    }
    final id = _readVarint();
    if (id < 0) return false;
    fieldID = id;
    return id != 0;
  }

  /// Reads [count] svarint values (zigzag-encoded int column).
  List<LuxoInt64> readColumnInt() {
    if (!requireBytes(count, 'int64 column')) return const [];
    final result = List<LuxoInt64>.filled(count, LuxoInt64.zero);
    for (var i = 0; i < count; i++) {
      result[i] = _readSvarint();
      if (error != null) break;
    }
    return result;
  }

  /// Reads [count] DateTime pairs,
  /// converting each to an ISO 8601 UTC string to match the JSON wire format.
  List<String> readColumnDateTime() {
    if (!requireBytes(count * 2, 'datetime column')) return const [];
    final result = List<String>.filled(count, '');
    for (var i = 0; i < count; i++) {
      result[i] = _readDateTime(this);
      if (error != null) return const [];
    }
    return result;
  }

  /// Reads [count] nullable DateTime pairs (0x00=null, 0x01+seconds+nanos),
  /// each non-null value converted to an ISO 8601 UTC string.
  List<String?> readColumnDateTimePtr() {
    if (!requireBytes(count, 'nullable datetime column')) return const [];
    final result = List<String?>.filled(count, null);
    for (var i = 0; i < count; i++) {
      if (!readCanonicalMarker('nullable')) continue;
      result[i] = _readDateTime(this);
      if (error != null) return const [];
    }
    return result;
  }

  /// Reads [count] fixed64 float values.
  List<double> readColumnFloat() {
    if (!requireBytes(count * 8, 'float column')) return const [];
    final result = List<double>.filled(count, 0.0);
    for (var i = 0; i < count; i++) {
      result[i] = _data.getFloat64(wireOffset, Endian.little);
      if (!acceptFloat(result[i])) return const [];
      wireOffset += 8;
    }
    return result;
  }

  /// Reads [count] length-prefixed string values.
  List<String> readColumnString() {
    if (!requireBytes(count, 'string column')) return const [];
    final result = List<String>.filled(count, '');
    for (var i = 0; i < count; i++) {
      result[i] = readString();
      if (error != null) return const [];
    }
    return result;
  }

  /// Reads [count] boolean values (canonical single-byte 0/1).
  List<bool> readColumnBool() {
    if (!requireBytes(count, 'bool column')) return const [];
    final result = List<bool>.filled(count, false);
    for (var i = 0; i < count; i++) {
      result[i] = readCanonicalMarker('bool');
    }
    return result;
  }

  /// Reads [count] nullable int values (0x00=null, 0x01+svarint).
  List<LuxoInt64?> readColumnIntPtr() {
    if (!requireBytes(count, 'nullable int64 column')) return const [];
    final result = List<LuxoInt64?>.filled(count, null);
    for (var i = 0; i < count; i++) {
      if (!readCanonicalMarker('nullable')) continue;
      result[i] = _readSvarint();
    }
    return result;
  }

  /// Reads [count] nullable float values (0x00=null, 0x01+fixed64).
  List<double?> readColumnFloatPtr() {
    if (!requireBytes(count, 'nullable float column')) return const [];
    final result = List<double?>.filled(count, null);
    for (var i = 0; i < count; i++) {
      if (!readCanonicalMarker('nullable')) continue;
      if (!requireBytes(8, 'float')) return const [];
      result[i] = _data.getFloat64(wireOffset, Endian.little);
      if (!acceptFloat(result[i]!)) return const [];
      wireOffset += 8;
    }
    return result;
  }

  /// Reads [count] nullable string values (0x00=null, 0x01+string).
  List<String?> readColumnStringPtr() {
    if (!requireBytes(count, 'nullable string column')) return const [];
    final result = List<String?>.filled(count, null);
    for (var i = 0; i < count; i++) {
      if (readCanonicalMarker('nullable')) result[i] = readString();
      if (error != null) return const [];
    }
    return result;
  }

  /// Reads [count] nullable booleans (0x00=null, 0x01+single-byte bool).
  List<bool?> readColumnBoolPtr() {
    if (!requireBytes(count, 'nullable bool column')) return const [];
    final result = List<bool?>.filled(count, null);
    for (var i = 0; i < count; i++) {
      if (!readCanonicalMarker('nullable')) continue;
      result[i] = readCanonicalMarker('bool');
    }
    return result;
  }

  /// Reads [count] fixed 16-byte UUID values as canonical strings.
  List<String> readColumnUuid() {
    if (!requireBytes(count * 16, 'uuid column')) return const [];
    final result = List<String>.filled(count, '');
    for (var i = 0; i < count; i++) {
      result[i] = formatUuid(wireBytes, wireOffset);
      wireOffset += 16;
    }
    return result;
  }

  /// Reads [count] nullable UUID values (0x00=null, 0x01+16 bytes).
  List<String?> readColumnUuidPtr() {
    if (!requireBytes(count, 'nullable uuid column')) return const [];
    final result = List<String?>.filled(count, null);
    for (var i = 0; i < count; i++) {
      if (!readCanonicalMarker('nullable')) continue;
      if (!requireBytes(16, 'uuid')) return const [];
      result[i] = formatUuid(wireBytes, wireOffset);
      wireOffset += 16;
    }
    return result;
  }

  /// Reads [count] length-prefixed byte blobs (one per cell).
  ///
  /// Used for scalar array columns and nested/federation columns: each cell is
  /// a length-prefixed blob. For scalar arrays the blob holds an inline
  /// [count][items...] array, decoded with a fresh [LuxoDecoder].
  List<Uint8List> readColumnBytes() {
    if (!requireBytes(count, 'bytes column')) return const [];
    final result = List<Uint8List>.filled(count, _emptyBytes, growable: false);
    for (var i = 0; i < count; i++) {
      final len = readLength('bytes');
      if (len < 0) return const [];
      result[i] = Uint8List.sublistView(
        wireBytes,
        wireOffset,
        wireOffset + len,
      );
      wireOffset += len;
    }
    return result;
  }

  /// Reads [count] nullable byte blobs (0x00=null, 0x01+length+bytes).
  List<Uint8List?> readColumnBytesPtr() {
    if (!requireBytes(count, 'nullable bytes column')) return const [];
    final result = List<Uint8List?>.filled(count, null, growable: false);
    for (var i = 0; i < count; i++) {
      if (!readCanonicalMarker('nullable')) continue;
      final len = readLength('bytes');
      if (len < 0) return const [];
      result[i] = Uint8List.sublistView(
        wireBytes,
        wireOffset,
        wireOffset + len,
      );
      wireOffset += len;
    }
    return result;
  }

  /// Current read position (for reading pagination metadata after 0x00).
  int get offset => wireOffset;

  /// Reads one signed zigzag-encoded varint at current position.
  LuxoInt64 readSvarint() => _readSvarint();

  // --- Internal ---

  LuxoInt64 _readSvarint() => int64wire.readWireInt64(this);

  int _readVarint() => readUnsigned();
}

// --- DateTime helpers ---

final _timestampPattern = RegExp(
  r'^([0-9]{4})-([0-9]{2})-([0-9]{2})[Tt]([0-9]{2}):([0-9]{2}):([0-9]{2})(?:\.([0-9]{1,9}))?([Zz]|[+-]([0-9]{2}):([0-9]{2}))$',
);

String _readDateTime(WireReader reader) {
  if (reader.error != null) return '';
  final seconds = int64wire.readWireInt64(reader);
  final nanos = reader.readBoundedUnsigned(999999999, 'DateTime nanoseconds');
  if (reader.error != null) return '';
  return dateTimeStringFromParts(seconds.toInt(), nanos);
}

/// Keeps the fraction out of DateTime, whose Web representation is millisecond-based.
({int seconds, int nanoseconds}) dateTimePartsFromString(String value) {
  final parts = _timestampPattern.firstMatch(value);
  if (parts == null || parts.end != value.length) {
    throw const FormatException(
      'DateTime requires RFC3339 with at most 9 fractional digits',
    );
  }
  final year = int.parse(parts[1]!);
  final month = int.parse(parts[2]!);
  final day = int.parse(parts[3]!);
  final leap = year % 4 == 0 && (year % 100 != 0 || year % 400 == 0);
  final days = month == 2
      ? (leap ? 29 : 28)
      : (const [4, 6, 9, 11].contains(month) ? 30 : 31);
  if (month < 1 ||
      month > 12 ||
      day < 1 ||
      day > days ||
      int.parse(parts[4]!) > 23 ||
      int.parse(parts[5]!) > 59 ||
      int.parse(parts[6]!) > 59 ||
      int.parse(parts[9] ?? '0') > 23 ||
      int.parse(parts[10] ?? '0') > 59) {
    throw const FormatException('invalid DateTime calendar or offset');
  }
  final whole =
      '${value.substring(0, 19).replaceFirst('t', 'T')}${parts[8]!.replaceFirst('z', 'Z')}';
  final seconds = DateTime.parse(whole).millisecondsSinceEpoch ~/ 1000;
  if (seconds < -62167219200 || seconds > 253402300799)
    throw const FormatException('DateTime out of range');
  final fraction = parts[7];
  final nanos = fraction == null ? 0 : int.parse(fraction.padRight(9, '0'));
  return (seconds: seconds, nanoseconds: nanos);
}

String dateTimeStringFromParts(int seconds, int nanoseconds) {
  if (nanoseconds < 0 || nanoseconds >= 1000000000) {
    throw const FormatException('invalid DateTime nanoseconds');
  }
  final whole = dateTimeStringFromUnixSeconds(seconds);
  if (nanoseconds == 0) return whole;
  var fraction = nanoseconds.toString().padLeft(9, '0');
  var end = fraction.length;
  while (fraction.codeUnitAt(end - 1) == 48) {
    end--;
  }
  fraction = fraction.substring(0, end);
  return '${whole.substring(0, whole.length - 1)}.${fraction}Z';
}

/// Extracts floor Unix seconds after validating the complete exact timestamp.
/// This utility does not encode a complete DateTime wire value by itself.
int dateTimeUnixSecondsFromString(String value) {
  return dateTimePartsFromString(value).seconds;
}

/// Formats a whole-second timestamp. The wire reader uses
/// [dateTimeStringFromParts] to preserve its separate nanosecond component.
String dateTimeStringFromUnixSeconds(int seconds) {
  if (seconds < -62167219200 || seconds > 253402300799)
    throw RangeError.range(seconds, -62167219200, 253402300799, 'seconds');
  final iso = DateTime.fromMillisecondsSinceEpoch(
    seconds * 1000,
    isUtc: true,
  ).toIso8601String();
  // This helper has no fraction; Go's RFC3339 JSON omits the .000 suffix.
  return '${iso.substring(0, iso.length - 5)}Z';
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
  // Enforce dashes at the canonical 8-4-4-4-12 boundaries; malformed
  // 36-char strings with misplaced dashes must not silently encode.
  if (s.codeUnitAt(8) != 0x2D ||
      s.codeUnitAt(13) != 0x2D ||
      s.codeUnitAt(18) != 0x2D ||
      s.codeUnitAt(23) != 0x2D) {
    throw FormatException('invalid UUID format', s);
  }
  var j = 0;
  for (var i = 0; i < s.length;) {
    if (i == 8 || i == 13 || i == 18 || i == 23) {
      i++;
      continue;
    }
    final hi = _hexNibble(s.codeUnitAt(i));
    final lo = _hexNibble(s.codeUnitAt(i + 1));
    dst[off + j++] = (hi << 4) | lo;
    i += 2;
  }
}

int _hexNibble(int c) {
  if (c >= 0x30 && c <= 0x39) return c - 0x30; // 0-9
  if (c >= 0x61 && c <= 0x66) return c - 0x61 + 10; // a-f
  if (c >= 0x41 && c <= 0x46) return c - 0x41 + 10; // A-F
  throw const FormatException('invalid hex digit in UUID');
}

// --- Field mask utilities ---

@pragma('vm:prefer-inline')
@pragma('dart2js:tryInline')
@pragma('wasm:prefer-inline')
void _checkSelectionFieldID(int fieldID) {
  if (fieldID <= 0 || fieldID > maxSelectionFieldID) {
    throw RangeError.range(fieldID, 1, maxSelectionFieldID, 'fieldID');
  }
}

/// Sets a bit in the field mask for the given fieldID. Returns the (possibly grown) mask.
/// Throws RangeError for IDs outside 1..65535 before allocating or mutating.
Uint8List fieldMaskSet(Uint8List mask, int fieldID) {
  _checkSelectionFieldID(fieldID);
  final bit = fieldID - 1;
  final byteIndex = bit >> 3;
  if (byteIndex >= mask.length) {
    // Grow to fit.
    final newMask = Uint8List(byteIndex + 1);
    newMask.setRange(0, mask.length, mask);
    mask = newMask;
  }
  mask[byteIndex] |= 1 << (bit & 7);
  return mask;
}

/// Checks if a bit is set in the field mask for the given fieldID.
/// Throws RangeError for invalid IDs instead of aliasing another bit on Web.
bool fieldMaskHas(Uint8List mask, int fieldID) {
  _checkSelectionFieldID(fieldID);
  final bit = fieldID - 1;
  final byteIndex = bit >> 3;
  if (byteIndex >= mask.length) return false;
  return (mask[byteIndex] & (1 << (bit & 7))) != 0;
}
