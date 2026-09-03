import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';
import 'package:http/http.dart' as http;
import 'error.dart';
import 'codec.dart';
import 'socket.dart';

/// Transport mode: json for debugging, binary for production.
enum TransportMode { json, binary }

abstract final class BinaryFrameType {
  static const callRequest = 0x01;
  static const callSuccess = 0x02;
  static const callError = 0x03;
  static const subscribe = 0x04;
  static const unsubscribe = 0x05;
  static const stream = 0x06;
  static const subscribeSuccess = 0x07;
  static const subscribeError = 0x08;
}

const _binaryFiltersFieldID = 0x7ffffffe;
const _binarySortersFieldID = 0x7fffffff;
const _filterOperatorIDs = <String, int>{
  'eq': 1,
  'ne': 2,
  'gt': 3,
  'gte': 4,
  'lt': 5,
  'lte': 6,
  'contains': 7,
  'startswith': 8,
  'endswith': 9,
  'match': 10,
};

class LuxoFilter {
  final String field;
  final String op;
  final Object value;
  const LuxoFilter(
      {required this.field, required this.op, required this.value});
  Map<String, Object> toJson() => {'field': field, 'op': op, 'value': value};
}

class LuxoSorter {
  final String field;
  final String order;
  const LuxoSorter({required this.field, required this.order});
  Map<String, Object> toJson() => {'field': field, 'order': order};
}

/// API schema metadata for binary encoding.
class APISchemaEntry {
  final int id;
  final List<ParamSchema> params;
  final Map<String, SelectionFieldSchema> fields;
  final Map<String, Map<String, SelectionFieldSchema>> types;
  const APISchemaEntry(
    this.id, [
    this.params = const [],
    this.fields = const {},
    this.types = const {},
  ]);
}

class SelectionFieldSchema {
  final int fieldID;
  final String? typeName;
  const SelectionFieldSchema(this.fieldID, [this.typeName]);
}

class ParamSchema {
  final int fieldID;
  final String name;
  final String type;
  final bool isList;
  final bool nullable;
  const ParamSchema(
    this.fieldID,
    this.name,
    this.type, [
    this.isList = false,
    this.nullable = false,
  ]);
}

/// Transport options.
class TransportOptions {
  final String? token;
  final TransportMode mode;
  final Map<String, String>? headers;
  const TransportOptions({
    this.token,
    this.mode = TransportMode.json,
    this.headers,
  });
}

/// Encodes a single API param onto [enc] using its schema metadata.
/// Handles scalar and list ([T]) params. UUID is encoded as fixed 16 bytes.
int _toUnixSeconds(dynamic value) {
  if (value is String) {
    final dt = DateTime.parse(value).toUtc();
    return dt.millisecondsSinceEpoch ~/ 1000;
  }
  throw FormatException('invalid DateTime param: ${value.runtimeType}');
}

void encodeParam(LuxoEncoder enc, ParamSchema pm, dynamic v) {
  enc.writeVarint(pm.fieldID);
  if (pm.nullable) {
    if (v == null) {
      enc.writeNull();
      return;
    }
    enc.writePresent();
  } else if (v == null) {
    throw LuxoError(
      'ConfigError',
      0,
      'parameter field ${pm.fieldID} is not nullable',
    );
  }
  if (pm.isList) {
    final list = v as List;
    enc.writeVarint(list.length);
    switch (pm.type) {
      case 'Int' || 'Duration':
        for (final value in list.cast<int>()) enc.writeSvarint(value);
      case 'DateTime':
        for (final value in list) enc.writeSvarint(_toUnixSeconds(value));
      case 'Float':
        for (final value in list) enc.writeFixed64((value as num).toDouble());
      case 'String' || 'Enum' || 'Decimal':
        for (final value in list.cast<String>()) enc.writeString(value);
      case 'Boolean':
        for (final value in list.cast<bool>()) enc.writeBool(value);
      case 'UUID':
        for (final value in list.cast<String>()) enc.writeUuid(value);
      case 'Bytes':
        for (final value in list.cast<Uint8List>()) enc.writeBytes(value);
      case 'JSON':
        for (final value in list) {
          enc.writeBytes(Uint8List.fromList(utf8.encode(jsonEncode(value))));
        }
      default:
        throw LuxoError(
          'ConfigError',
          0,
          'unsupported binary list param type: ${pm.type}',
        );
    }
    return;
  }
  switch (pm.type) {
    case 'Int' || 'Duration':
      enc.writeSvarint(v as int);
    case 'DateTime':
      enc.writeSvarint(_toUnixSeconds(v));
    case 'Float':
      enc.writeFixed64((v as num).toDouble());
    case 'String' || 'Enum' || 'Decimal':
      enc.writeString(v as String);
    case 'Boolean':
      enc.writeBool(v as bool);
    case 'UUID':
      enc.writeUuid(v as String);
    case 'Bytes':
      enc.writeBytes(v as Uint8List);
    case 'JSON':
      enc.writeBytes(Uint8List.fromList(utf8.encode(jsonEncode(v))));
    default:
      throw LuxoError(
        'ConfigError',
        0,
        'unsupported binary param type: ${pm.type}',
      );
  }
}

class _SelectedField {
  final String name;
  final List<_SelectedField>? children;
  const _SelectedField(this.name, [this.children]);
}

class _SelectionParser {
  final String input;
  int offset = 0;
  _SelectionParser(this.input);

  List<_SelectedField> parse() {
    final fields = _parseList(false, 0);
    _skipSpaces();
    if (offset != input.length) _fail("unexpected '${input[offset]}'");
    return fields;
  }

  List<_SelectedField> _parseList(bool nested, int depth) {
    if (depth >= 32) _fail('selection depth exceeds 32');
    final fields = <_SelectedField>[];
    final names = <String>{};
    while (true) {
      _skipSpaces();
      if (offset >= input.length || (nested && input[offset] == '}')) break;
      final name = _readIdentifier();
      if (name.isEmpty) _fail('expected field name');
      if (!names.add(name)) _fail("duplicate field '$name'");
      _skipSpaces();
      List<_SelectedField>? children;
      if (offset < input.length && input[offset] == '{') {
        offset++;
        children = _parseList(true, depth + 1);
        if (children.isEmpty) _fail("empty selection for '$name'");
        _skipSpaces();
        if (offset >= input.length || input[offset] != '}') {
          _fail("missing '}' for '$name'");
        }
        offset++;
      }
      fields.add(_SelectedField(name, children));
      _skipSpaces();
      if (offset >= input.length || input[offset] != ',') break;
      offset++;
    }
    return fields;
  }

  String _readIdentifier() {
    final start = offset;
    if (offset >= input.length || !_identifierStart(input.codeUnitAt(offset))) {
      return '';
    }
    offset++;
    while (offset < input.length && _identifierPart(input.codeUnitAt(offset))) {
      offset++;
    }
    return input.substring(start, offset);
  }

  void _skipSpaces() {
    while (offset < input.length && input.codeUnitAt(offset) <= 32) offset++;
  }

  Never _fail(String message) => throw LuxoError(
        'ConfigError',
        0,
        '$message at position $offset',
      );
}

bool _identifierStart(int code) =>
    code == 95 || (code >= 65 && code <= 90) || (code >= 97 && code <= 122);

bool _identifierPart(int code) =>
    _identifierStart(code) || (code >= 48 && code <= 57);

Uint8List _encodeSelectionNode(
  List<_SelectedField> selected,
  Map<String, SelectionFieldSchema> fields,
  Map<String, Map<String, SelectionFieldSchema>> types,
) {
  var mask = Uint8List(0);
  final children = <({int fieldID, Uint8List data})>[];
  for (final field in selected) {
    final meta = fields[field.name];
    if (meta == null) {
      throw LuxoError(
          'ConfigError', 0, 'unknown selected field: ${field.name}');
    }
    mask = fieldMaskSet(mask, meta.fieldID);
    if (field.children == null) continue;
    final nestedFields = meta.typeName == null ? null : types[meta.typeName];
    if (nestedFields == null) {
      throw LuxoError(
        'ConfigError',
        0,
        'field ${field.name} does not support nested selection',
      );
    }
    children.add((
      fieldID: meta.fieldID,
      data: _encodeSelectionNode(field.children!, nestedFields, types),
    ));
  }
  children.sort((a, b) => a.fieldID.compareTo(b.fieldID));
  final node = LuxoEncoder();
  node.writeVarint(mask.length);
  node.writeRawBytes(mask);
  for (final child in children) {
    node.writeVarint(child.fieldID);
    node.writeVarint(child.data.length);
    node.writeRawBytes(child.data);
  }
  return node.bytes();
}

void _writeFieldMask(
  LuxoEncoder enc,
  APISchemaEntry meta,
  Map<String, dynamic>? params,
) {
  final select = params?[r'$select'];
  if (select is! String || select.trim().isEmpty || meta.fields.isEmpty) {
    enc.writeVarint(0);
    return;
  }
  final mask = _encodeSelectionNode(
    _SelectionParser(select).parse(),
    meta.fields,
    meta.types,
  );
  enc.writeVarint(mask.length);
  enc.writeRawBytes(mask);
}

void _writeListControls(LuxoEncoder enc, Map<String, dynamic>? params) {
  if (params == null) return;
  if (params.containsKey(r'$filters')) _writeFilters(enc, params[r'$filters']);
  if (params.containsKey(r'$sorters')) _writeSorters(enc, params[r'$sorters']);
}

void _writeFilters(LuxoEncoder enc, dynamic value) {
  if (value is! List || value.length > 1000) {
    throw LuxoError(
        'ConfigError', 0, r'$filters must contain at most 1000 entries');
  }
  enc.writeVarint(_binaryFiltersFieldID);
  enc.writeVarint(value.length);
  for (var i = 0; i < value.length; i++) {
    final item = value[i];
    final map = item is LuxoFilter ? item.toJson() : item;
    if (map is! Map ||
        map['field'] is! String ||
        (map['field'] as String).isEmpty ||
        map['op'] is! String ||
        !_filterOperatorIDs.containsKey(map['op']) ||
        !_validFilterValue(map['value'])) {
      throw LuxoError('ConfigError', 0, 'invalid \$filters entry at index $i');
    }
    enc.writeString(map['field'] as String);
    enc.writeVarint(_filterOperatorIDs[map['op']]!);
    enc.writeString(_filterValueText(map['value']));
  }
}

void _writeSorters(LuxoEncoder enc, dynamic value) {
  if (value is! List || value.length > 100) {
    throw LuxoError(
        'ConfigError', 0, r'$sorters must contain at most 100 entries');
  }
  enc.writeVarint(_binarySortersFieldID);
  enc.writeVarint(value.length);
  for (var i = 0; i < value.length; i++) {
    final item = value[i];
    final map = item is LuxoSorter ? item.toJson() : item;
    final order = map is Map ? map['order'] : null;
    if (map is! Map ||
        map['field'] is! String ||
        (map['field'] as String).isEmpty ||
        (order != 'asc' && order != 'desc')) {
      throw LuxoError('ConfigError', 0, 'invalid \$sorters entry at index $i');
    }
    enc.writeString(map['field'] as String);
    enc.writeBool(order == 'desc');
  }
}

bool _validFilterValue(dynamic value) =>
    value is String || value is bool || (value is num && value.isFinite);

String _filterValueText(dynamic value) {
  if (value is bool) return value ? 'true' : 'false';
  return value.toString();
}

LuxoError decodeBinaryError(Uint8List data, int statusCode) {
  final dec = LuxoDecoder(data);
  var code = statusCode;
  var name = 'Error';
  var message = 'HTTP $statusCode';
  String? traceId;
  dynamic errorData;
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
        code = dec.readInt();
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
          errorData = jsonDecode(raw);
        } catch (_) {
          return _binaryParseError(statusCode, 'invalid JSON data');
        }
      case 6:
        cause = dec.readString();
      default:
        return _binaryParseError(
          statusCode,
          'unknown binary error field ${dec.fieldID}',
        );
    }
  }
  if (dec.error != null) return _binaryParseError(statusCode, dec.error!);
  if (!ended) return _binaryParseError(statusCode, 'missing end marker');
  if (dec.offset != data.length) {
    return _binaryParseError(statusCode, 'trailing bytes');
  }
  if (seen != 7) {
    return _binaryParseError(statusCode, 'missing required fields');
  }
  return LuxoError(name, code, message, traceId, errorData, cause);
}

LuxoError _binaryParseError(int statusCode, String message) => LuxoError(
      'ParseError',
      statusCode,
      'invalid binary error response: $message',
    );

/// Transport interface — implemented by HTTP and WebSocket.
abstract class Transport {
  Future<T> call<T>(
    String api, {
    Map<String, dynamic>? params,
    T Function(dynamic)? decoder,
  });
  Future<void Function()> subscribe(
    String api,
    Map<String, dynamic> params,
    void Function(dynamic) onData,
  );
  void setSchema(Map<String, APISchemaEntry> schema);
  void setMode(TransportMode mode);
  void setToken(String token);
  void close();
}

// --- HTTP Transport ---

/// HTTP-based transport for Flutter / Dart server.
/// Uses HTTP/2 when available (dart:io HttpClient supports h2).
class HttpTransport implements Transport {
  final String endpoint;
  final Map<String, String> _headers = {'Content-Type': 'application/json'};
  final http.Client _client;
  final Duration timeout;
  final Future<String?> Function()? onTokenExpired;
  TransportMode _mode;
  Map<String, APISchemaEntry> _schema;

  HttpTransport(
    this.endpoint, {
    TransportOptions? options,
    http.Client? client,
    this.timeout = const Duration(seconds: 30),
    this.onTokenExpired,
  })  : _client = client ?? http.Client(),
        _mode = options?.mode ?? TransportMode.json,
        _schema = {} {
    if (options?.headers != null) _headers.addAll(options!.headers!);
    if (options?.token != null)
      _headers['Authorization'] = 'Bearer ${options!.token}';
  }

  @override
  void setSchema(Map<String, APISchemaEntry> schema) => _schema = schema;
  @override
  void setMode(TransportMode mode) => _mode = mode;
  @override
  void setToken(String token) => _headers['Authorization'] = 'Bearer $token';
  @override
  void close() => _client.close();

  @override
  Future<T> call<T>(
    String api, {
    Map<String, dynamic>? params,
    T Function(dynamic)? decoder,
  }) async {
    final result = _mode == TransportMode.binary
        ? await _binaryCall(api, params)
        : await _jsonCall(api, params);
    return decoder == null ? result as T : decoder(result);
  }

  @override
  Future<void Function()> subscribe(
    String api,
    Map<String, dynamic> params,
    void Function(dynamic) onData,
  ) {
    throw LuxoError(
      'ConfigError',
      0,
      'subscriptions require a WebSocket endpoint',
    );
  }

  Future<dynamic> _jsonCall(
    String api,
    Map<String, dynamic>? params, {
    bool isRetry = false,
  }) async {
    final body = <String, dynamic>{r'$api': api};
    if (params != null) body.addAll(params);

    final http.Response resp;
    try {
      resp = await _client
          .post(Uri.parse(endpoint), headers: _headers, body: jsonEncode(body))
          .timeout(timeout);
    } on TimeoutException {
      throw LuxoError('TimeoutError', 0, 'request timed out after $timeout');
    } catch (e) {
      throw LuxoError('NetworkError', 0, e.toString());
    }

    // 401 auto-refresh: call onTokenExpired, retry once with new token.
    if (resp.statusCode == 401 && !isRetry && onTokenExpired != null) {
      final newToken = await onTokenExpired!();
      if (newToken != null) {
        setToken(newToken);
        return _jsonCall(api, params, isRetry: true);
      }
    }

    final Map<String, dynamic> json;
    try {
      json = jsonDecode(resp.body) as Map<String, dynamic>;
    } catch (e) {
      throw LuxoError('ParseError', resp.statusCode, 'invalid JSON: $e');
    }

    if (json.containsKey('error')) {
      throw LuxoError(
        (json['error'] ?? 'Unknown') as String,
        (json['code'] ?? 0) as int,
        (json['message'] ?? '') as String,
        json['traceId'] as String?,
        json['data'],
        json['cause'] as String?,
      );
    }
    return json['data'];
  }

  Future<dynamic> _binaryCall(
    String api,
    Map<String, dynamic>? params, {
    bool isRetry = false,
  }) async {
    final meta = _schema[api];
    if (meta == null)
      throw LuxoError(
        'ConfigError',
        0,
        'no schema for "$api" — use LuxoClient.create()',
      );

    final enc = LuxoEncoder();
    enc.writeVarint(meta.id);
    _writeFieldMask(enc, meta, params);
    if (params != null) {
      for (final pm in meta.params) {
        if (!params.containsKey(pm.name)) continue;
        final v = params[pm.name];
        encodeParam(enc, pm, v);
      }
    }
    _writeListControls(enc, params);
    enc.writeEnd();

    final http.Response resp;
    try {
      resp = await _client
          .post(
            Uri.parse(endpoint),
            headers: {
              'Content-Type': 'application/x-luxo',
              'X-Luxo-Mode': 'binary',
              ..._headers,
            },
            body: enc.bytes(),
          )
          .timeout(timeout);
    } on TimeoutException {
      throw LuxoError('TimeoutError', 0, 'request timed out after $timeout');
    } catch (e) {
      throw LuxoError('NetworkError', 0, e.toString());
    }

    // 401 auto-refresh: call onTokenExpired, retry once with new token.
    if (resp.statusCode == 401 && !isRetry && onTokenExpired != null) {
      final newToken = await onTokenExpired!();
      if (newToken != null) {
        setToken(newToken);
        return _binaryCall(api, params, isRetry: true);
      }
    }

    if (resp.statusCode != 200) {
      throw decodeBinaryError(
        Uint8List.fromList(resp.bodyBytes),
        resp.statusCode,
      );
    }
    return Uint8List.fromList(resp.bodyBytes);
  }
}

// --- WebSocket Transport ---

class _Subscription {
  final Map<String, dynamic> params;
  final void Function(dynamic) onData;
  Completer<void Function()>? acknowledgement;

  _Subscription(this.params, this.onData, this.acknowledgement);
}

/// WebSocket transport — persistent connection, multiplexed requests.
/// Supports automatic reconnection with exponential backoff.
class WsTransport implements Transport {
  final String endpoint;
  final Duration timeout;
  TransportMode _mode;
  Map<String, APISchemaEntry> _schema = {};
  String? _token;

  LuxoSocket? _ws;
  int _seq = 0;
  final _pending = <int, Completer<dynamic>>{};
  final _subscriptions = <String, _Subscription>{};
  Completer<void>? _connectCompleter;

  // Reconnection state
  bool _autoReconnect;
  bool _closed = false;
  int _reconnectAttempts = 0;
  static const int _maxBackoffSeconds = 30;
  Timer? _reconnectTimer;

  WsTransport(
    this.endpoint, {
    TransportOptions? options,
    bool autoReconnect = true,
    this.timeout = const Duration(seconds: 30),
  })  : _mode = options?.mode ?? TransportMode.json,
        _token = options?.token,
        _autoReconnect = autoReconnect;

  @override
  void setSchema(Map<String, APISchemaEntry> schema) => _schema = schema;
  @override
  void setMode(TransportMode mode) => _mode = mode;
  @override
  void setToken(String token) => _token = token;

  Future<void> _connect() async {
    if (_ws != null) return;
    if (_connectCompleter != null) return _connectCompleter!.future;

    _connectCompleter = Completer<void>();
    final uri = Uri.parse(endpoint);
    final url = _token == null
        ? endpoint
        : uri.replace(
            queryParameters: {...uri.queryParameters, 'token': _token!},
          ).toString();

    try {
      final ws = await connectSocket(url);
      _ws = ws;
      _reconnectAttempts = 0; // Reset backoff on successful connect
      _listenWs(ws);
      for (final entry in _subscriptions.entries) {
        _sendSubscription(entry.key, entry.value.params);
      }
      _connectCompleter!.complete();
    } catch (e) {
      final error = LuxoError('NetworkError', 0, 'WebSocket failed: $e');
      _connectCompleter!.completeError(error);
      _connectCompleter = null;
      throw error;
    }
    _connectCompleter = null;
  }

  void _listenWs(LuxoSocket ws) {
    // Listen for messages and dispatch to pending requests
    ws.messages.listen(
      (data) {
        if (_mode == TransportMode.binary && data is List<int>) {
          _handleBinaryResponse(Uint8List.fromList(data));
        } else if (data is String) {
          _handleJsonResponse(data);
        }
      },
      onDone: () {
        _ws = null;
        for (final c in _pending.values) {
          c.completeError(LuxoError('NetworkError', 0, 'WebSocket closed'));
        }
        _pending.clear();
        for (final entry in _subscriptions.entries.toList()) {
          final acknowledgement = entry.value.acknowledgement;
          if (acknowledgement == null) continue;
          acknowledgement.completeError(
            LuxoError('NetworkError', 0, 'WebSocket closed'),
          );
          _subscriptions.remove(entry.key);
        }
        _scheduleReconnect();
      },
    );
  }

  /// Schedule a reconnection attempt with exponential backoff.
  void _scheduleReconnect() {
    if (_closed || !_autoReconnect) return;

    final delaySeconds = (1 << _reconnectAttempts).clamp(1, _maxBackoffSeconds);
    _reconnectAttempts++;

    _reconnectTimer = Timer(Duration(seconds: delaySeconds), () async {
      if (_closed) return;
      try {
        await _connect();
      } catch (_) {
        // _connect failed, onDone will fire again or we schedule manually
        _scheduleReconnect();
      }
    });
  }

  @override
  Future<T> call<T>(
    String api, {
    Map<String, dynamic>? params,
    T Function(dynamic)? decoder,
  }) async {
    await _connect();
    final id = ++_seq;
    final completer = Completer<dynamic>();
    _pending[id] = completer;

    try {
      if (_mode == TransportMode.binary) {
        final meta = _schema[api];
        if (meta == null) {
          throw LuxoError('ConfigError', 0, 'no schema for "$api"');
        }

        final enc = LuxoEncoder();
        enc.writeVarint(BinaryFrameType.callRequest);
        enc.writeVarint(id);
        enc.writeVarint(meta.id);
        _writeFieldMask(enc, meta, params);
        if (params != null) {
          for (final pm in meta.params) {
            if (!params.containsKey(pm.name)) continue;
            final v = params[pm.name];
            encodeParam(enc, pm, v);
          }
        }
        _writeListControls(enc, params);
        enc.writeEnd();
        _ws!.add(enc.bytes());
      } else {
        final body = jsonEncode({r'$id': id, r'$api': api, ...?params});
        _ws!.add(body);
      }
    } catch (_) {
      _pending.remove(id);
      rethrow;
    }

    try {
      final result = await completer.future.timeout(timeout);
      return decoder == null ? result as T : decoder(result);
    } on TimeoutException {
      throw LuxoError('TimeoutError', 0, 'request timed out after $timeout');
    } finally {
      _pending.remove(id);
    }
  }

  void _handleJsonResponse(String data) {
    try {
      final json = jsonDecode(data) as Map<String, dynamic>;
      final subscription = json[r'$sub'];
      if (subscription is String) {
        _acknowledgeSubscription(
          subscription,
          json.containsKey('error')
              ? LuxoError(
                  json['error'] as String,
                  (json['code'] ?? 0) as int,
                  (json['message'] ?? '') as String,
                  json['traceId'] as String?,
                  json['data'],
                  json['cause'] as String?,
                )
              : null,
        );
        return;
      }
      final stream = json[r'$stream'];
      if (stream is String) {
        _subscriptions[stream]?.onData(json['data']);
        return;
      }
      final id = json[r'$id'] as int;
      final c = _pending.remove(id);
      if (c == null) return;
      if (json.containsKey('error')) {
        c.completeError(
          LuxoError(
            json['error'] as String,
            (json['code'] ?? 0) as int,
            (json['message'] ?? '') as String,
            json['traceId'] as String?,
            json['data'],
            json['cause'] as String?,
          ),
        );
      } else {
        c.complete(json['data']);
      }
    } catch (_) {}
  }

  void _handleBinaryResponse(Uint8List data) {
    if (data.isEmpty) return;
    final frameType = data[0];
    int id = 0;
    int shift = 0;
    int off = 1;
    while (off < data.length) {
      final b = data[off++];
      id += (b & 0x7F) * (1 << shift);
      if (b < 0x80) break;
      shift += 7;
    }
    if (frameType == BinaryFrameType.subscribeSuccess ||
        frameType == BinaryFrameType.subscribeError) {
      String? api;
      for (final entry in _schema.entries) {
        if (entry.value.id == id) {
          api = entry.key;
          break;
        }
      }
      if (api != null) {
        _acknowledgeSubscription(
          api,
          frameType == BinaryFrameType.subscribeError
              ? decodeBinaryError(Uint8List.sublistView(data, off), 0)
              : null,
        );
      }
      return;
    }
    if (frameType == BinaryFrameType.stream) {
      String? api;
      for (final entry in _schema.entries) {
        if (entry.value.id == id) {
          api = entry.key;
          break;
        }
      }
      if (api != null) _subscriptions[api]?.onData(data.sublist(off));
      return;
    }
    if (frameType != BinaryFrameType.callSuccess &&
        frameType != BinaryFrameType.callError) return;
    final c = _pending.remove(id);
    if (c == null) return;
    if (frameType == BinaryFrameType.callError) {
      c.completeError(decodeBinaryError(Uint8List.sublistView(data, off), 0));
    } else {
      c.complete(Uint8List.sublistView(data, off));
    }
  }

  @override
  Future<void Function()> subscribe(
    String api,
    Map<String, dynamic> params,
    void Function(dynamic) onData,
  ) async {
    if (_subscriptions.containsKey(api)) {
      throw LuxoError(
        'ConfigError',
        0,
        'already subscribed to "$api" on this transport',
      );
    }
    await _connect();
    final acknowledgement = Completer<void Function()>();
    final subscription = _Subscription(params, onData, acknowledgement);
    _subscriptions[api] = subscription;
    try {
      _sendSubscription(api, params);
    } catch (error, stackTrace) {
      _subscriptions.remove(api);
      acknowledgement.completeError(error, stackTrace);
    }
    try {
      return await acknowledgement.future.timeout(timeout);
    } on TimeoutException {
      if (_subscriptions[api] == subscription) {
        _subscriptions.remove(api);
      }
      throw LuxoError(
        'TimeoutError',
        0,
        'subscription timed out after $timeout',
      );
    }
  }

  void _acknowledgeSubscription(String api, LuxoError? error) {
    final subscription = _subscriptions[api];
    if (subscription == null) return;
    final acknowledgement = subscription.acknowledgement;
    if (error != null) {
      _subscriptions.remove(api);
      acknowledgement?.completeError(error);
      return;
    }
    if (acknowledgement == null) return;
    subscription.acknowledgement = null;
    acknowledgement.complete(() => _unsubscribe(api));
  }

  void _unsubscribe(String api) {
    if (_subscriptions.remove(api) == null || _ws == null) return;
    if (_mode == TransportMode.binary) {
      final meta = _schema[api];
      if (meta == null) return;
      final enc = LuxoEncoder();
      enc.writeVarint(BinaryFrameType.unsubscribe);
      enc.writeVarint(meta.id);
      _ws!.add(enc.bytes());
    } else {
      _ws!.add(jsonEncode({r'$unsub': api}));
    }
  }

  void _sendSubscription(String api, Map<String, dynamic> params) {
    if (_mode == TransportMode.json) {
      _ws!.add(jsonEncode({r'$sub': api, ...params}));
      return;
    }
    final meta = _schema[api];
    if (meta == null) throw LuxoError('ConfigError', 0, 'no schema for "$api"');
    final enc = LuxoEncoder();
    enc.writeVarint(BinaryFrameType.subscribe);
    enc.writeVarint(meta.id);
    _writeFieldMask(enc, meta, params);
    for (final pm in meta.params) {
      if (!params.containsKey(pm.name)) continue;
      final value = params[pm.name];
      encodeParam(enc, pm, value);
    }
    _writeListControls(enc, params);
    enc.writeEnd();
    _ws!.add(enc.bytes());
  }

  @override
  void close() {
    _closed = true;
    _autoReconnect = false;
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    for (final subscription in _subscriptions.values) {
      subscription.acknowledgement?.completeError(
        LuxoError('NetworkError', 0, 'WebSocket closed'),
      );
    }
    _subscriptions.clear();
    _ws?.close();
    _ws = null;
  }
}
