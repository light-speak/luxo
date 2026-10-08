import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:http/http.dart' as http;

import 'error.dart';
import 'error_codec.dart';
import 'codec.dart';
import 'int64.dart';
import 'int64_native.dart'
    if (dart.library.js_interop) 'int64_web.dart'
    as platform;
import 'json.dart';
import 'schema.dart';
import 'schema_ids.dart' show snapshotAPISchema;
import 'socket.dart';
import 'uint64.dart';

export 'error_codec.dart' show decodeBinaryError;
export 'schema.dart';

// JSON maps have no inherited fields; only null needs a second presence lookup.
bool _hasJSONResponseData(Map<String, dynamic> json, Object? payload) =>
    payload != null || json.containsKey('data');

/// Transport mode: json for debugging, binary for production.
enum TransportMode { json, binary }

abstract final class BinaryFrameType {
  static const callRequest = 0x01;
  static const callSuccess = 0x02;
  static const callError = 0x03;
  static const subscribe = 0x10;
  static const unsubscribe = 0x11;
  static const stream = 0x12;
  static const subscribeSuccess = 0x13;
  static const subscribeError = 0x14;
  static const streamComplete = 0x15;
  static const streamError = 0x16;
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

class LuxoFilter implements LuxoJSONEncodable {
  final String field;
  final String op;
  final Object value;
  const LuxoFilter({
    required this.field,
    required this.op,
    required this.value,
  });
  Map<String, Object> toJson() => {'field': field, 'op': op, 'value': value};
}

class LuxoSorter implements LuxoJSONEncodable {
  final String field;
  final String order;
  const LuxoSorter({required this.field, required this.order});
  Map<String, Object> toJson() => {'field': field, 'order': order};
}

/// Generated input values implement direct Luxo binary encoding.
abstract interface class LuxoBinaryEncodable {
  void writeLuxo(LuxoEncoder encoder);
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
void _writeDateTimeParam(LuxoEncoder encoder, dynamic value) {
  if (value is String) {
    encoder.writeDateTime(value);
    return;
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
        for (final value in list.cast<LuxoInt64>()) enc.writeSvarint(value);
      case 'DateTime':
        for (final value in list) _writeDateTimeParam(enc, value);
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
          enc.writeBytes(
            Uint8List.fromList(utf8.encode(encodeLuxoJSON(value))),
          );
        }
      case 'Model':
        for (final value in list) {
          _writeStructuredParam(enc, pm, value);
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
      enc.writeSvarint(v as LuxoInt64);
    case 'DateTime':
      _writeDateTimeParam(enc, v);
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
      enc.writeBytes(Uint8List.fromList(utf8.encode(encodeLuxoJSON(v))));
    case 'Model':
      _writeStructuredParam(enc, pm, v);
    default:
      throw LuxoError(
        'ConfigError',
        0,
        'unsupported binary param type: ${pm.type}',
      );
  }
}

void _writeStructuredParam(LuxoEncoder enc, ParamSchema param, dynamic value) {
  if (value is! LuxoBinaryEncodable) {
    throw LuxoError(
      'ConfigError',
      0,
      '${param.typeName ?? 'structured parameter'} must implement LuxoBinaryEncodable',
    );
  }
  enc.writeDelimited(value.writeLuxo);
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

  Never _fail(String message) =>
      throw LuxoError('ConfigError', 0, '$message at position $offset');
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
        'ConfigError',
        0,
        'unknown selected field: ${field.name}',
      );
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

void _writeFieldMask(LuxoEncoder enc, APISchemaEntry meta, Object? select) {
  if (meta.fields.isEmpty) {
    enc.writeVarint(0);
    return;
  }
  if (select is! String || select.trim().isEmpty) {
    throw LuxoError(
      'ConfigError',
      0,
      r'$select is required for structured responses',
    );
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
      'ConfigError',
      0,
      r'$filters must contain at most 1000 entries',
    );
  }
  enc.writeVarint(_binaryFiltersFieldID);
  enc.writeVarint(value.length);
  for (var i = 0; i < value.length; i++) {
    final item = value[i];
    if (item is LuxoFilter) {
      _writeFilter(enc, item.field, item.op, item.value, i);
    } else if (item is Map && item['field'] is String && item['op'] is String) {
      _writeFilter(
        enc,
        item['field'] as String,
        item['op'] as String,
        item['value'],
        i,
      );
    } else {
      throw LuxoError('ConfigError', 0, 'invalid \$filters entry at index $i');
    }
  }
}

void _writeTypedFilters(LuxoEncoder enc, List<LuxoFilter> filters) {
  if (filters.length > 1000) {
    throw LuxoError(
      'ConfigError',
      0,
      r'$filters must contain at most 1000 entries',
    );
  }
  enc.writeVarint(_binaryFiltersFieldID);
  enc.writeVarint(filters.length);
  for (var i = 0; i < filters.length; i++) {
    final filter = filters[i];
    _writeFilter(enc, filter.field, filter.op, filter.value, i);
  }
}

void _writeFilter(
  LuxoEncoder enc,
  String field,
  String op,
  Object? value,
  int index,
) {
  final operatorID = _filterOperatorIDs[op];
  if (field.isEmpty || operatorID == null || !_validFilterValue(value)) {
    throw LuxoError(
      'ConfigError',
      0,
      'invalid \$filters entry at index $index',
    );
  }
  enc.writeString(field);
  enc.writeVarint(operatorID);
  enc.writeString(_filterValueText(value));
}

void _writeSorters(LuxoEncoder enc, dynamic value) {
  if (value is! List || value.length > 100) {
    throw LuxoError(
      'ConfigError',
      0,
      r'$sorters must contain at most 100 entries',
    );
  }
  enc.writeVarint(_binarySortersFieldID);
  enc.writeVarint(value.length);
  for (var i = 0; i < value.length; i++) {
    final item = value[i];
    if (item is LuxoSorter) {
      _writeSorter(enc, item.field, item.order, i);
    } else if (item is Map &&
        item['field'] is String &&
        item['order'] is String) {
      _writeSorter(enc, item['field'] as String, item['order'] as String, i);
    } else {
      throw LuxoError('ConfigError', 0, 'invalid \$sorters entry at index $i');
    }
  }
}

void _writeTypedSorters(LuxoEncoder enc, List<LuxoSorter> sorters) {
  if (sorters.length > 100) {
    throw LuxoError(
      'ConfigError',
      0,
      r'$sorters must contain at most 100 entries',
    );
  }
  enc.writeVarint(_binarySortersFieldID);
  enc.writeVarint(sorters.length);
  for (var i = 0; i < sorters.length; i++) {
    final sorter = sorters[i];
    _writeSorter(enc, sorter.field, sorter.order, i);
  }
}

void _writeSorter(LuxoEncoder enc, String field, String order, int index) {
  if (field.isEmpty || (order != 'asc' && order != 'desc')) {
    throw LuxoError(
      'ConfigError',
      0,
      'invalid \$sorters entry at index $index',
    );
  }
  enc.writeString(field);
  enc.writeBool(order == 'desc');
}

bool _validFilterValue(dynamic value) =>
    value is String ||
    value is bool ||
    platform.isWireInt64(value) ||
    (value is num && value.isFinite);

String _filterValueText(dynamic value) {
  if (value is bool) return value ? 'true' : 'false';
  return value.toString();
}

/// Transport interface — implemented by HTTP and WebSocket.
abstract class GeneratedRequest {
  int get apiID;
  String? get selection => null;
  void writeParameters(LuxoEncoder encoder);
  List<LuxoFilter>? get filters => null;
  List<LuxoSorter>? get sorters => null;
  Map<String, dynamic> toJSONParameters();
}

Map<String, dynamic>? _requestJSON(
  Map<String, dynamic>? params,
  GeneratedRequest? request,
) {
  if (request == null) return params;
  _checkRequestArguments(params);
  return request.toJSONParameters();
}

void _checkRequestArguments(Map<String, dynamic>? params) {
  if (params != null && params.isNotEmpty) {
    throw LuxoError(
      'ConfigError',
      0,
      'cannot combine generated and dynamic parameters',
    );
  }
}

abstract class Transport {
  Future<T> call<T>(
    String api, {
    Map<String, dynamic>? params,
    GeneratedRequest? request,
    T Function(dynamic)? decoder,
  });

  /// Decode before delivery; decoding failure terminates only this subscription.
  /// Application callback errors must not be reported as protocol failures.
  Future<void Function()> subscribe<T>(
    String api,
    Map<String, dynamic> params,
    void Function(T) onData, {
    required T Function(Object?) decoder,
    GeneratedRequest? request,
    void Function(LuxoError)? onError,
    void Function()? onDone,
  });
  void setSchema(Map<String, APISchemaEntry> schema);
  void setMode(TransportMode mode);
  void setToken(String token);
  void close();
}

void _writeRequest(
  LuxoEncoder encoder,
  APISchemaEntry schema,
  Map<String, dynamic>? params, [
  GeneratedRequest? request,
]) {
  if (request != null) {
    _checkRequestArguments(params);
    if (request.apiID != schema.id) {
      throw LuxoError(
        'ConfigError',
        0,
        'generated request API ID differs from registered schema',
      );
    }
    encoder.writeVarint(request.apiID);
    _writeFieldMask(encoder, schema, request.selection);
    request.writeParameters(encoder);
    final filters = request.filters;
    final sorters = request.sorters;
    if (filters != null) _writeTypedFilters(encoder, filters);
    if (sorters != null) _writeTypedSorters(encoder, sorters);
    encoder.writeEnd();
    return;
  }
  encoder.writeVarint(schema.id);
  _writeFieldMask(encoder, schema, params?[r'$select']);
  if (params != null) {
    for (final param in schema.params) {
      if (params.containsKey(param.name)) {
        encodeParam(encoder, param, params[param.name]);
      }
    }
  }
  _writeListControls(encoder, params);
  encoder.writeEnd();
}

// --- HTTP Transport ---

/// HTTP-based transport for Flutter / Dart server.
/// Uses HTTP/2 when available (dart:io HttpClient supports h2).
class HttpTransport implements Transport {
  final String endpoint;
  final Map<String, String> _headers = {
    'content-type': 'application/json',
    'x-luxo-mode': 'json',
  };
  final Map<String, String> _binaryHeaders = {
    'content-type': 'application/x-luxo',
    'x-luxo-mode': 'binary',
  };
  final http.Client _client;
  final Duration timeout;
  final Future<String?> Function()? onTokenExpired;
  TransportMode _mode;
  Map<String, APISchemaEntry> _schema;
  final _jsonRequests = JSONRequestEncoder();

  HttpTransport(
    this.endpoint, {
    TransportOptions? options,
    http.Client? client,
    this.timeout = const Duration(seconds: 30),
    this.onTokenExpired,
  }) : _client = client ?? http.Client(),
       _mode = options?.mode ?? TransportMode.json,
       _schema = {} {
    final headers = options?.headers;
    if (headers != null) {
      for (final entry in headers.entries) {
        final name = entry.key.toLowerCase();
        if (name == 'content-type' || name == 'x-luxo-mode') continue;
        _setHeader(name, entry.value);
      }
    }
    final token = options?.token;
    if (token != null) setToken(token);
  }

  void _setHeader(String name, String value) {
    _headers[name] = value;
    _binaryHeaders[name] = value;
  }

  @override
  void setSchema(Map<String, APISchemaEntry> schema) {
    final snapshot = snapshotAPISchema(schema);
    _jsonRequests.setSchema(snapshot);
    _schema = snapshot;
  }

  @override
  void setMode(TransportMode mode) => _mode = mode;
  @override
  void setToken(String token) => _setHeader('authorization', 'Bearer $token');
  @override
  void close() => _client.close();

  @override
  Future<T> call<T>(
    String api, {
    Map<String, dynamic>? params,
    GeneratedRequest? request,
    T Function(dynamic)? decoder,
  }) async {
    final result = _mode == TransportMode.binary
        ? await _binaryCall(api, params, request: request)
        : await _jsonCall(api, _requestJSON(params, request));
    return decoder == null
        ? (result is Uint8List ? result : normalizeLuxoJSON(result)) as T
        : decoder(result);
  }

  @override
  Future<void Function()> subscribe<T>(
    String api,
    Map<String, dynamic> params,
    void Function(T) onData, {
    required T Function(Object?) decoder,
    GeneratedRequest? request,
    void Function(LuxoError)? onError,
    void Function()? onDone,
  }) {
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
    final body = _jsonRequests.encode(api, params);

    final http.Response resp;
    try {
      resp = await _client
          .post(Uri.parse(endpoint), headers: _headers, body: body)
          .timeout(timeout);
    } on TimeoutException {
      throw LuxoError('TimeoutError', 0, 'request timed out after $timeout');
    } catch (e) {
      throw LuxoError('NetworkError', 0, e.toString());
    }

    final json = _jsonEnvelope(resp);
    if (resp.statusCode == 200 && !json.containsKey('error')) {
      final payload = json['data'];
      if (!_hasJSONResponseData(json, payload)) {
        throw LuxoError(
          'ParseError',
          resp.statusCode,
          'missing JSON response data',
        );
      }
      return payload;
    }
    final error = decodeJSONError(json, resp.statusCode);
    final refresh = _refreshAfterUnauthorized(error, resp.statusCode, isRetry);
    if (refresh != null) {
      final newToken = await refresh;
      if (newToken != null) {
        setToken(newToken);
        return _jsonCall(api, params, isRetry: true);
      }
    }

    throw error;
  }

  Map<String, dynamic> _jsonEnvelope(http.Response resp) {
    try {
      return decodeLuxoJSON(
        utf8.decode(resp.bodyBytes),
        preserveNumberSyntax: true,
      ) as Map<String, dynamic>;
    } catch (e) {
      throw LuxoError('ParseError', resp.statusCode, 'invalid JSON: $e');
    }
  }

  Future<String?>? _refreshAfterUnauthorized(
    LuxoError error,
    int status,
    bool isRetry,
  ) {
    if (isRetry ||
        status != 401 ||
        error.code != 401 ||
        error.error == 'ParseError') {
      return null;
    }
    return onTokenExpired?.call();
  }

  Future<dynamic> _binaryCall(
    String api,
    Map<String, dynamic>? params, {
    bool isRetry = false,
    GeneratedRequest? request,
  }) async {
    final meta = _schema[api];
    if (meta == null)
      throw LuxoError(
        'ConfigError',
        0,
        'no schema for "$api" — use LuxoClient.create()',
      );

    final enc = LuxoEncoder();
    _writeRequest(enc, meta, params, request);

    final http.Response resp;
    try {
      resp = await _client
          .post(Uri.parse(endpoint), headers: _binaryHeaders, body: enc.bytes())
          .timeout(timeout);
    } on TimeoutException {
      throw LuxoError('TimeoutError', 0, 'request timed out after $timeout');
    } catch (e) {
      throw LuxoError('NetworkError', 0, e.toString());
    }

    if (resp.statusCode == 200) return Uint8List.fromList(resp.bodyBytes);
    final error = decodeBinaryError(
      Uint8List.fromList(resp.bodyBytes),
      resp.statusCode,
    );
    final refresh = _refreshAfterUnauthorized(error, resp.statusCode, isRetry);
    if (refresh != null) {
      final newToken = await refresh;
      if (newToken != null) {
        setToken(newToken);
        return _binaryCall(api, params, isRetry: true, request: request);
      }
    }

    throw error;
  }
}

// --- WebSocket Transport ---

/// Internal boundary-test factory, excluded from the package entry point.
WsTransport wsTransportWithSequence(
  String endpoint,
  Object initialSequence, {
  TransportOptions? options,
  Duration timeout = const Duration(seconds: 3),
}) {
  return WsTransport(
    endpoint,
    options: options,
    autoReconnect: false,
    timeout: timeout,
  ).._requestIDs = RequestIDs(initialSequence);
}

class _Subscription<T> {
  final TransportMode mode;
  final T Function(Object?) decoder;
  final void Function(T) onData;
  final void Function(LuxoError)? onError;
  final void Function()? onDone;
  Completer<void Function()>? acknowledgement;

  _Subscription(
    this.mode,
    this.decoder,
    this.onData,
    this.acknowledgement, {
    this.onError,
    this.onDone,
  });

  void deliver(WsTransport transport, Object id, Object? data) {
    final T value;
    try {
      value = decoder(data);
    } catch (error) {
      transport._failSubscriptionDecoding(id, this, error);
      return;
    }
    // Application exceptions belong to the caller's Zone, not the protocol.
    onData(value);
  }
}

void _writeSequence(LuxoEncoder encoder, Object id) {
  if (id is int) {
    encoder.writeVarint(id);
  } else {
    encoder.writeUint64(id as BigInt);
  }
}

/// WebSocket transport — persistent connection, multiplexed requests.
/// Connections can reconnect; interrupted subscriptions must be created again.
class WsTransport implements Transport {
  final String endpoint;
  final Duration timeout;
  TransportMode _mode;
  Map<String, APISchemaEntry> _schema = {};
  final _jsonRequests = JSONRequestEncoder();
  String? _token;

  LuxoSocket? _ws;
  var _requestIDs = RequestIDs();
  final _pending = <Object, Completer<dynamic>>{};
  var _subscriptions = <Object, _Subscription<Object?>>{};
  Completer<void>? _connecting;

  // Reconnection state
  final bool _autoReconnect;
  bool _closed = false;
  int _reconnectAttempts = 0;
  static const int _maxBackoffSeconds = 30;
  Timer? _reconnectTimer;

  WsTransport(
    this.endpoint, {
    TransportOptions? options,
    bool autoReconnect = true,
    this.timeout = const Duration(seconds: 30),
  }) : _mode = options?.mode ?? TransportMode.json,
       _token = options?.token,
       _autoReconnect = autoReconnect;

  @override
  void setSchema(Map<String, APISchemaEntry> schema) {
    final snapshot = snapshotAPISchema(schema);
    _jsonRequests.setSchema(snapshot);
    _schema = snapshot;
  }

  @override
  void setMode(TransportMode mode) => _mode = mode;
  @override
  void setToken(String token) => _token = token;

  Future<void> _connect() {
    if (_closed)
      return Future.error(LuxoError('NetworkError', 0, 'WebSocket closed'));
    if (_ws != null) return Future.value();
    final pending = _connecting;
    if (pending != null) return pending.future;
    final ready = Completer<void>();
    _connecting = ready;
    unawaited(_open(ready));
    return ready.future;
  }

  Future<void> _open(Completer<void> ready) async {
    LuxoSocket? connected;
    try {
      final uri = Uri.parse(endpoint);
      final url = _token == null
          ? endpoint
          : uri
                .replace(
                  queryParameters: {...uri.queryParameters, 'token': _token!},
                )
                .toString();
      final ws = await connectSocket(url);
      connected = ws;
      if (_closed) {
        throw LuxoError('NetworkError', 0, 'WebSocket closed');
      }
      _ws = ws;
      // A failed manual retry must not cancel the scheduled recovery attempt.
      _reconnectTimer?.cancel();
      _reconnectTimer = null;
      _reconnectAttempts = 0; // Reset backoff on successful connect
      _listenWs(ws);
      ready.complete();
    } catch (e) {
      if (connected != null) {
        if (identical(_ws, connected)) _ws = null;
        unawaited(connected.close());
      }
      if (!ready.isCompleted) {
        ready.completeError(
          LuxoError('NetworkError', 0, 'WebSocket failed: $e'),
        );
      }
    } finally {
      if (identical(_connecting, ready)) _connecting = null;
    }
  }

  void _listenWs(LuxoSocket ws) {
    // Listen for messages and dispatch to pending requests
    ws.messages.listen(
      (data) {
        if (!identical(_ws, ws)) return;
        if (data is List<int>) {
          _handleBinaryResponse(Uint8List.fromList(data));
        } else if (data is String) {
          _handleJsonResponse(data, ws);
        }
      },
      onDone: () => _disconnected(ws),
      onError: (Object _) => _disconnected(ws),
    );
  }

  void _disconnected(LuxoSocket ws, [LuxoError? failure]) {
    if (!identical(_ws, ws)) return;
    _ws = null;
    unawaited(ws.close());
    final error = failure ?? LuxoError('NetworkError', 0, 'WebSocket closed');
    final retired = _retirePending(error);
    _scheduleReconnect();
    for (final subscription in retired.values) {
      final notify = subscription.onError;
      if (subscription.acknowledgement == null && notify != null) {
        // Report user exceptions to the listener's zone without losing peers.
        Zone.current.runUnaryGuarded(notify, error);
      }
    }
  }

  Map<Object, _Subscription<Object?>> _retirePending(LuxoError error) {
    for (final pending in _pending.values) pending.completeError(error);
    _pending.clear();
    final retired = _subscriptions;
    _subscriptions = {};
    for (final subscription in retired.values) {
      subscription.acknowledgement?.completeError(error);
    }
    return retired;
  }

  /// Schedule a reconnection attempt with exponential backoff.
  void _scheduleReconnect() {
    if (_closed || !_autoReconnect || _ws != null || _reconnectTimer != null)
      return;

    final delaySeconds = (1 << _reconnectAttempts).clamp(1, _maxBackoffSeconds);
    if (_reconnectAttempts < 5) _reconnectAttempts++;

    _reconnectTimer = Timer(Duration(seconds: delaySeconds), () async {
      _reconnectTimer = null;
      if (_closed) return;
      try {
        await _connect();
      } catch (_) {
        _scheduleReconnect();
      }
    });
  }

  @override
  Future<T> call<T>(
    String api, {
    Map<String, dynamic>? params,
    T Function(dynamic)? decoder,
    GeneratedRequest? request,
  }) async {
    await _connect();
    final ws = _requireSocket();
    final id = _requestIDs.next();
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
        _writeSequence(enc, id);
        _writeRequest(enc, meta, params, request);
        ws.add(enc.bytes());
      } else {
        final body = _jsonRequests.encode(
          api,
          _requestJSON(params, request),
          sequence: id,
        );
        ws.add(body);
      }
    } catch (_) {
      _pending.remove(id);
      rethrow;
    }

    try {
      final result = await completer.future.timeout(timeout);
      return decoder == null
          ? (result is Uint8List ? result : normalizeLuxoJSON(result)) as T
          : decoder(result);
    } on TimeoutException {
      throw LuxoError('TimeoutError', 0, 'request timed out after $timeout');
    } finally {
      _pending.remove(id);
    }
  }

  void _handleJsonResponse(String data, LuxoSocket ws) {
    final Map<String, dynamic> json;
    try {
      json = decodeLuxoJSON(
        data,
        preserveNumberSyntax: true,
      ) as Map<String, dynamic>;
    } catch (_) {
      _disconnected(
        ws,
        LuxoError('ParseError', 0, 'invalid WebSocket JSON message'),
      );
      return;
    }
    if (json.containsKey(r'$end')) {
      if (json.containsKey(r'$sub') ||
          json.containsKey(r'$stream') ||
          json.containsKey(r'$id'))
        return;
      _handleJsonStreamEnd(json);
      return;
    }
    if (json.containsKey(r'$sub')) {
      final subscription = uint64FromJSON(json[r'$sub']);
      if (subscription == null) return;
      if (!json.containsKey('error') && json['ok'] != true) return;
      _acknowledgeSubscription(
        subscription,
        TransportMode.json,
        json.containsKey('error') ? decodeJSONError(json, 0) : null,
      );
      return;
    }
    if (json.containsKey(r'$stream')) {
      final stream = uint64FromJSON(json[r'$stream']);
      if (stream == null) return;
      final payload = json['data'];
      if (!_hasJSONResponseData(json, payload)) {
        final subscription = _subscriptions[stream];
        if (subscription != null &&
            subscription.mode == TransportMode.json &&
            subscription.acknowledgement == null) {
          _failSubscription(
            stream,
            subscription,
            LuxoError('ParseError', 0, 'missing JSON response data'),
          );
        }
        return;
      }
      _deliverSubscription(stream, TransportMode.json, payload);
      return;
    }
    final id = uint64FromJSON(json[r'$id']);
    if (id == null) return;
    final c = _pending.remove(id);
    if (c == null) return;
    if (json.containsKey('error')) {
      c.completeError(decodeJSONError(json, 0));
    } else {
      final payload = json['data'];
      if (_hasJSONResponseData(json, payload)) {
        c.complete(payload);
      } else {
        c.completeError(
          LuxoError('ParseError', 0, 'missing JSON response data'),
        );
      }
    }
  }

  void _handleJsonStreamEnd(Map<String, dynamic> json) {
    final id = uint64FromJSON(json[r'$end']);
    if (id == null) return;
    final failure = json.containsKey('error');
    final error = failure && !json.containsKey('ok')
        ? decodeJSONError(json, 0)
        : !failure && json['ok'] == true && !json.containsKey('data')
        ? null
        : LuxoError('ParseError', 0, 'invalid stream completion');
    _finishSubscription(id, TransportMode.json, error);
  }

  void _handleBinaryResponse(Uint8List data) {
    if (data.length < 2) return;
    final frameType = data[0];
    if (frameType != BinaryFrameType.callSuccess &&
        frameType != BinaryFrameType.callError &&
        frameType != BinaryFrameType.stream &&
        frameType != BinaryFrameType.subscribeSuccess &&
        frameType != BinaryFrameType.subscribeError &&
        frameType != BinaryFrameType.streamComplete &&
        frameType != BinaryFrameType.streamError)
      return;
    var low = 0;
    var high = 0;
    for (var i = 0; i < 10 && i + 1 < data.length; i++) {
      final byte = data[i + 1];
      if (i == 9 && byte > 1) return;
      final payload = byte & 127;
      if (i < 4) {
        low |= payload << (i * 7);
      } else if (i == 4) {
        low = (low | (payload << 28)) & 0xffffffff;
        high = payload >>> 4;
      } else {
        high |= payload << (i * 7 - 32);
      }
      if (byte < 128) {
        _dispatchBinaryResponse(
          data,
          frameType,
          uint64FromWords(low, high),
          i + 2,
        );
        return;
      }
    }
  }

  void _dispatchBinaryResponse(
    Uint8List data,
    int frameType,
    Object id,
    int off,
  ) {
    if (frameType == BinaryFrameType.subscribeSuccess ||
        frameType == BinaryFrameType.subscribeError) {
      if (frameType == BinaryFrameType.subscribeSuccess && off != data.length)
        return;
      _acknowledgeSubscription(
        id,
        TransportMode.binary,
        frameType == BinaryFrameType.subscribeError
            ? decodeBinaryError(Uint8List.sublistView(data, off), 0)
            : null,
      );
      return;
    }
    if (frameType == BinaryFrameType.stream) {
      _deliverSubscription(
        id,
        TransportMode.binary,
        Uint8List.sublistView(data, off),
      );
      return;
    }
    // The frame whitelist has already rejected unknown tags before ID decoding.
    if (frameType >= BinaryFrameType.streamComplete) {
      _finishBinarySubscription(data, frameType, id, off);
      return;
    }
    final c = _pending.remove(id);
    if (c == null) return;
    if (frameType == BinaryFrameType.callError) {
      c.completeError(decodeBinaryError(Uint8List.sublistView(data, off), 0));
    } else {
      c.complete(Uint8List.sublistView(data, off));
    }
  }

  void _finishBinarySubscription(
    Uint8List data,
    int frameType,
    Object id,
    int off,
  ) {
    final error = frameType == BinaryFrameType.streamError
        ? decodeBinaryError(Uint8List.sublistView(data, off), 0)
        : off == data.length
        ? null
        : LuxoError('ParseError', 0, 'trailing stream completion bytes');
    _finishSubscription(id, TransportMode.binary, error);
  }

  @override
  Future<void Function()> subscribe<T>(
    String api,
    Map<String, dynamic> params,
    void Function(T) onData, {
    required T Function(Object?) decoder,
    void Function(LuxoError)? onError,
    void Function()? onDone,
    GeneratedRequest? request,
  }) async {
    await _connect();
    final ws = _requireSocket();
    final id = _requestIDs.next();
    final acknowledgement = Completer<void Function()>();
    final subscription = _Subscription<T>(
      _mode,
      decoder,
      onData,
      acknowledgement,
      onError: onError,
      onDone: onDone,
    );
    _subscriptions[id] = subscription;
    try {
      _sendSubscription(ws, id, api, params, subscription.mode, request);
    } catch (error, stackTrace) {
      _subscriptions.remove(id);
      acknowledgement.completeError(error, stackTrace);
    }
    try {
      return await acknowledgement.future.timeout(timeout);
    } on TimeoutException {
      _unsubscribe(id);
      throw LuxoError(
        'TimeoutError',
        0,
        'subscription timed out after $timeout',
      );
    }
  }

  void _failSubscriptionDecoding(
    Object id,
    _Subscription<Object?> subscription,
    Object cause,
  ) {
    if (!identical(_subscriptions[id], subscription)) return;
    final error = LuxoError(
      'DecodeError',
      0,
      cause is LuxoError ? cause.message : cause.toString(),
    );
    _failSubscription(id, subscription, error);
  }

  void _failSubscription(
    Object id,
    _Subscription<Object?> subscription,
    LuxoError error,
  ) {
    if (!identical(_subscriptions[id], subscription)) return;
    // Local termination does not depend on the advisory cancellation being sent.
    try {
      _unsubscribe(id);
    } catch (_) {
      /* Socket events handle connection failures. */
    }
    subscription.onError?.call(error);
  }

  void _deliverSubscription(Object id, TransportMode mode, Object? data) {
    final subscription = _subscriptions[id];
    if (subscription == null ||
        subscription.mode != mode ||
        subscription.acknowledgement != null)
      return;
    subscription.deliver(this, id, data);
  }

  void _acknowledgeSubscription(
    Object id,
    TransportMode mode,
    LuxoError? error,
  ) {
    final subscription = _subscriptions[id];
    if (subscription == null || subscription.mode != mode) return;
    final acknowledgement = subscription.acknowledgement;
    if (error != null) {
      _subscriptions.remove(id);
      acknowledgement?.completeError(error);
      if (acknowledgement == null) subscription.onError?.call(error);
      return;
    }
    if (acknowledgement == null) return;
    subscription.acknowledgement = null;
    acknowledgement.complete(() => _unsubscribe(id));
  }

  void _finishSubscription(Object id, TransportMode mode, LuxoError? error) {
    final subscription = _subscriptions[id];
    if (subscription == null || subscription.mode != mode) return;
    _subscriptions.remove(id);
    final acknowledgement = subscription.acknowledgement;
    if (acknowledgement != null) {
      acknowledgement.completeError(
        LuxoError('ParseError', 0, 'stream ended before acknowledgement'),
      );
    } else if (error != null) {
      subscription.onError?.call(error);
    } else {
      subscription.onDone?.call();
    }
  }

  void _unsubscribe(Object id) {
    final subscription = _subscriptions.remove(id);
    if (subscription == null || _ws == null) return;
    if (subscription.mode == TransportMode.binary) {
      final enc = LuxoEncoder();
      enc.writeVarint(BinaryFrameType.unsubscribe);
      _writeSequence(enc, id);
      _ws!.add(enc.bytes());
    } else {
      _ws!.add(encodeLuxoJSON({r'$unsub': id}));
    }
  }

  LuxoSocket _requireSocket() =>
      _ws ?? (throw LuxoError('NetworkError', 0, 'WebSocket closed'));

  void _sendSubscription(
    LuxoSocket ws,
    Object id,
    String api,
    Map<String, dynamic> params,
    TransportMode mode,
    GeneratedRequest? request,
  ) {
    if (mode == TransportMode.json) {
      ws.add(
        _jsonRequests.encode(
          api,
          _requestJSON(params, request),
          sequence: id,
          subscription: true,
        ),
      );
      return;
    }
    final meta = _schema[api];
    if (meta == null) throw LuxoError('ConfigError', 0, 'no schema for "$api"');
    final enc = LuxoEncoder();
    enc.writeVarint(BinaryFrameType.subscribe);
    _writeSequence(enc, id);
    _writeRequest(enc, meta, params, request);
    ws.add(enc.bytes());
  }

  @override
  void close() {
    _closed = true;
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    final error = LuxoError('NetworkError', 0, 'WebSocket closed');
    final connecting = _connecting;
    _connecting = null;
    if (connecting != null && !connecting.isCompleted)
      connecting.completeError(error);
    _retirePending(error);
    final ws = _ws;
    _ws = null;
    if (ws != null) unawaited(ws.close());
  }
}
