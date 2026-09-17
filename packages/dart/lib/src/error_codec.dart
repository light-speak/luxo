import 'dart:convert';
import 'dart:typed_data';

import 'codec.dart';
import 'error.dart';
import 'int64_validation.dart';
import 'json.dart';
import 'unicode.dart';

/// Decodes an already parsed envelope; successful responses never enter here.
LuxoError decodeJSONError(Map<String, Object?> json, int statusCode) {
  final name = json['error'];
  final message = json['message'];
  final traceId = json['traceId'];
  final cause = json['cause'];
  if (name is! String ||
      message is! String ||
      (traceId != null && traceId is! String) ||
      (cause != null && cause is! String)) {
    return _parseError(statusCode, 'JSON', 'invalid error fields');
  }
  try {
    requireUnicode(name);
    requireUnicode(message);
    if (traceId is String) requireUnicode(traceId);
    if (cause is String) requireUnicode(cause);
    final value = json['code'];
    final code = value is int
        ? requireSafeInteger(value)
        : luxoInt64FromJSON(value).toInt();
    return LuxoError(
      name,
      code,
      message,
      traceId as String?,
      normalizeLuxoJSON(json['data']),
      cause as String?,
    );
  } on FormatException {
    return _parseError(statusCode, 'JSON', 'invalid error text, code or data');
  } on RangeError {
    return _parseError(
      statusCode,
      'JSON',
      'error code exceeds portable int range',
    );
  }
}

/// Malformed payloads resolve correlated calls with ParseError, never a timeout.
LuxoError decodeBinaryError(Uint8List data, int statusCode) {
  try {
    return _readBinaryError(data);
  } on FormatException catch (error) {
    return _parseError(statusCode, 'binary', error.message);
  } on RangeError {
    return _parseError(
      statusCode,
      'binary',
      'error code exceeds portable int range',
    );
  }
}

LuxoError _readBinaryError(Uint8List data) {
  final dec = LuxoDecoder(data);
  var code = 0;
  var name = '', message = '';
  String? traceId, cause;
  Object? errorData;
  var seen = 0;
  var ended = false;
  while (dec.offset < data.length) {
    if (!dec.nextField()) {
      ended = dec.error == null;
      break;
    }
    final field = dec.fieldID;
    if (field > 6 || (seen & (1 << field)) != 0) {
      throw FormatException('unknown or duplicate error field');
    }
    seen |= 1 << field;
    switch (field) {
      case 1:
        code = dec.readInt().toInt();
      case 2:
        name = dec.readString();
      case 3:
        message = dec.readString();
      case 4:
        traceId = dec.readString();
      case 5:
        errorData = decodeLuxoJSON(utf8.decode(dec.readBytes()));
      case 6:
        cause = dec.readString();
    }
  }
  if (dec.error != null) throw FormatException(dec.error!);
  if (!ended) throw FormatException('missing end marker');
  if (dec.offset != data.length) throw FormatException('trailing bytes');
  if ((seen & 14) != 14) throw FormatException('missing required fields');
  return LuxoError(name, code, message, traceId, errorData, cause);
}

LuxoError _parseError(int statusCode, String encoding, String message) =>
    LuxoError(
      'ParseError',
      statusCode,
      'invalid $encoding error response: $message',
    );
