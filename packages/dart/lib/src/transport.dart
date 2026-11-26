import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:http/http.dart' as http;

import 'error.dart';
import 'buffered_http_response.dart';
import 'deadline.dart';
import 'error_codec.dart';
import 'codec.dart';
import 'int64.dart';
import 'int64_native.dart'
    if (dart.library.js_interop) 'int64_web.dart'
    as platform;
import 'json.dart';
import 'schema.dart';
import 'selection.dart';
export 'selection.dart' show PreparedSelection;
import 'schema_ids.dart' show snapshotAPISchema;
import 'socket.dart';
import 'uint64.dart';

export 'error_codec.dart' show decodeBinaryError;
export 'schema.dart';

// JSON maps have no inherited fields; only null needs a second presence lookup.
bool _hasJSONResponseData(Map<String, dynamic> json, Object? payload) =>
    payload != null || json.containsKey('data');

LuxoError _missingBinaryResponse([int code = 0]) =>
    LuxoError('ParseError', code, 'missing binary response data');

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
  final int maxResponseBytes;
  final String? token;
  final TransportMode mode;
  final Map<String, String>? headers;
  const TransportOptions({
    this.maxResponseBytes = 2 * 1024 * 1024,
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
    enc.writeArrayHeader(list.length);
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
          enc.writeBytes(utf8.encode(encodeLuxoJSON(value, opaque: true)));
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
      enc.writeBytes(utf8.encode(encodeLuxoJSON(v, opaque: true)));
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
  PreparedSelection? get selectionPlan => null;
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
    writeSelectionMask(
      encoder,
      schema,
      request.selection,
      plan: request.selectionPlan,
    );
    request.writeParameters(encoder);
    final filters = request.filters;
    final sorters = request.sorters;
    if (filters != null) _writeTypedFilters(encoder, filters);
    if (sorters != null) _writeTypedSorters(encoder, sorters);
    encoder.writeEnd();
    return;
  }
  encoder.writeVarint(schema.id);
  writeSelectionMask(encoder, schema, params?[r'$select']);
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

int _responseLimit(int value) {
  if (value <= 0 || value > 0x7fffffff) {
    throw ArgumentError.value(
      value,
      'maxResponseBytes',
      'must be a positive 31-bit integer',
    );
  }
  return value;
}

// --- HTTP Transport ---

Future<T> _decodeHTTPCall<T>(
  Future<dynamic> pending,
  JSONResponseSchema? response,
  T Function(dynamic)? decoder,
) async {
  final result = await pending;
  return decoder == null
      ? (result is Uint8List || response != null
                ? result
                : normalizeLuxoJSON(result))
            as T
      : decoder(result);
}

final class _HTTPCredential {
  final Object? session;
  _HTTPTokenRefresh? refresh;

  _HTTPCredential([this.session]);
}

final class _HTTPRefreshFailure {
  final Object error;
  final StackTrace stack;

  const _HTTPRefreshFailure(this.error, this.stack);
}

final class _HTTPTokenRefresh {
  final result = Completer<Object?>.sync();
  int waiters = 0;
  bool discarded = false;

  void start(Future<String?> Function() callback) {
    unawaited(
      Future<String?>.sync(callback).then<void>(
        // Settle at the end of the callback event. Success needs only its token.
        (token) {
          if (!discarded) result.complete(token);
        },
        onError: (Object error, StackTrace stack) {
          if (!discarded) result.complete(_HTTPRefreshFailure(error, stack));
        },
      ),
    );
  }

  void discard() {
    discarded = true;
    if (!result.isCompleted) {
      // Reserve completion now, but let close/setToken finish before notifying.
      result.complete(Future<Object?>.microtask(() => null));
    }
  }
}

/// HTTP-based transport for Flutter / Dart server.
/// The default native client uses HTTP/1.1; web negotiation is browser-managed.
/// Inject a compatible client to use a different HTTP backend.
class HttpTransport implements Transport {
  final int maxResponseBytes;
  final String endpoint;
  late final Uri _endpointURI = Uri.parse(endpoint);
  final Map<String, String> _headers = {
    'content-type': 'application/json',
    'x-luxo-mode': 'json',
  };
  final Map<String, String> _binaryHeaders = {
    'content-type': 'application/x-luxo',
    'x-luxo-mode': 'binary',
  };
  final http.Client _client;
  final _pendingHTTP = <Completer<void>>{};
  bool _closedHTTP = false;
  var _credential = _HTTPCredential();
  final Duration timeout;
  final Future<String?> Function()? onTokenExpired;
  TransportMode _mode;
  Map<String, APISchemaEntry> _schema;
  var _jsonRequests = JSONRequestEncoder();

  HttpTransport(
    this.endpoint, {
    TransportOptions? options,
    http.Client? client,
    this.timeout = const Duration(seconds: 30),
    this.onTokenExpired,
  }) : maxResponseBytes = _responseLimit(
         options?.maxResponseBytes ?? 2 * 1024 * 1024,
       ),
       _client = client ?? http.Client(),
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
    _jsonRequests = JSONRequestEncoder(snapshot);
    _schema = snapshot;
  }

  @override
  void setMode(TransportMode mode) => _mode = mode;
  @override
  void setToken(String token) {
    final previous = _credential;
    _credential = _HTTPCredential(Object());
    previous.refresh?.discard();
    _setHeader('authorization', 'Bearer $token');
  }

  @override
  void close() {
    if (_closedHTTP) return;
    _closedHTTP = true;
    _credential.refresh?.discard();
    for (final abort in _pendingHTTP) {
      if (!abort.isCompleted) abort.complete();
    }
    _pendingHTTP.clear();
    _client.close();
  }

  void _checkDeadline(Stopwatch clock) {
    if (timeout <= Duration.zero ||
        clock.elapsedMicroseconds >= timeout.inMicroseconds) {
      throw LuxoError('TimeoutError', 0, 'request timed out after $timeout');
    }
  }

  Future<http.Response> _post(
    Map<String, String> headers,
    Object body,
    Stopwatch clock,
  ) async {
    if (_closedHTTP) {
      throw LuxoError('NetworkError', 0, 'transport is closed');
    }
    final abort = Completer<void>();
    _pendingHTTP.add(abort);
    try {
      final request =
          http.AbortableRequest(
              'POST',
              _endpointURI,
              abortTrigger: abort.future,
            )
            ..followRedirects = false
            ..headers.addAll(headers);
      if (body is String) {
        request.body = body;
      } else {
        request.bodyBytes = body as Uint8List;
      }
      _checkDeadline(clock);
      return await withDeadline(
        _readHTTPResponse(request),
        timeout,
        clock: clock,
        onTimeout: () {
          if (!abort.isCompleted) abort.complete();
          throw TimeoutException('request deadline exceeded', timeout);
        },
      );
    } on LuxoError {
      if (!abort.isCompleted) abort.complete();
      rethrow;
    } on TimeoutException {
      throw LuxoError('TimeoutError', 0, 'request timed out after $timeout');
    } on http.RequestAbortedException {
      throw LuxoError('CancelledError', 0, 'request cancelled');
    } catch (error) {
      throw LuxoError('NetworkError', 0, error.toString());
    } finally {
      _pendingHTTP.remove(abort);
    }
  }

  Future<http.Response> _readHTTPResponse(http.BaseRequest request) async {
    final response = await _client.send(request);
    if (response is BufferedHTTPResponse) {
      checkHTTPResponseSize(
        response.bodyBytes.length,
        maxResponseBytes,
        response.statusCode,
      );
      return response.toResponse();
    }
    return collectHTTPResponse(response, maxResponseBytes);
  }

  @override
  Future<T> call<T>(
    String api, {
    Map<String, dynamic>? params,
    GeneratedRequest? request,
    T Function(dynamic)? decoder,
  }) {
    try {
      final clock = Stopwatch()..start();
      final response = _schema[api]?.response;
      final pending = _mode == TransportMode.binary
          ? _binaryCall(_encodeBinaryRequest(api, params, request), clock)
          : _jsonCall(
              _jsonRequests.encode(api, _requestJSON(params, request)),
              clock,
              response: response,
            );
      return _decodeHTTPCall(pending, response, decoder);
    } catch (error, stackTrace) {
      return Future<T>.error(error, stackTrace);
    }
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
    String body,
    Stopwatch clock, {
    bool isRetry = false,
    JSONResponseSchema? response,
  }) async {
    final credential = _credential;
    final resp = await _post(_headers, body, clock);

    final json = _jsonEnvelope(resp, response);
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
    final refresh = _refreshAfterUnauthorized(
      error,
      resp.statusCode,
      isRetry,
      clock,
      credential,
    );
    if (refresh != null && await refresh && _canRetry(credential, clock)) {
      return _jsonCall(body, clock, isRetry: true, response: response);
    }

    throw error;
  }

  Map<String, dynamic> _jsonEnvelope(
    http.Response resp,
    JSONResponseSchema? response,
  ) {
    try {
      return decodeLuxoJSONEnvelope(
        utf8.decode(resp.bodyBytes),
        response: response,
        errorResponse: resp.statusCode != 200,
      );
    } catch (e) {
      throw LuxoError('ParseError', resp.statusCode, 'invalid JSON: $e');
    }
  }

  Future<bool>? _refreshAfterUnauthorized(
    LuxoError error,
    int status,
    bool isRetry,
    Stopwatch clock,
    _HTTPCredential credential,
  ) {
    if (isRetry ||
        status != 401 ||
        error.code != 401 ||
        error.error == 'ParseError' ||
        !identical(credential.session, _credential.session)) {
      return null;
    }
    if (!_canRetry(credential, clock)) return null;
    final callback = onTokenExpired;
    if (callback == null) return null;
    final refresh = credential.refresh ??= _HTTPTokenRefresh();
    return _joinRefresh(refresh, callback, credential, clock);
  }

  bool _canRetry(_HTTPCredential credential, Stopwatch clock) {
    if (_closedHTTP) {
      throw const LuxoError('CancelledError', 0, 'request cancelled');
    }
    _checkDeadline(clock);
    return identical(credential.session, _credential.session);
  }

  Future<bool> _joinRefresh(
    _HTTPTokenRefresh refresh,
    Future<String?> Function() callback,
    _HTTPCredential credential,
    Stopwatch clock,
  ) async {
    if (++refresh.waiters == 1 && !refresh.result.isCompleted) {
      refresh.start(callback);
    }
    try {
      // Closing the transport or replacing credentials settles this flight.
      // The HTTP send/read phase keeps its own request abort trigger.
      final outcome = await withDeadline(
        refresh.result.future,
        timeout,
        clock: clock,
        onTimeout: () {
          throw LuxoError(
            'TimeoutError',
            0,
            'request timed out after $timeout',
          );
        },
      );
      if (!_canRetry(credential, clock) || refresh.discarded) return false;
      final token = outcome is String ? outcome : null;
      if (identical(credential, _credential)) {
        // Already-sent requests retain this result. New requests use a fresh
        // generation so that a declined or failed refresh can be attempted again.
        if (token != null) _setHeader('authorization', 'Bearer $token');
        _credential = _HTTPCredential(credential.session);
      }
      if (outcome is _HTTPRefreshFailure) {
        Error.throwWithStackTrace(outcome.error, outcome.stack);
      }
      return token != null;
    } finally {
      if (--refresh.waiters == 0 && identical(_credential, credential)) {
        // A response still in flight can start the replacement refresh.
        credential.refresh = null;
        refresh.discard();
      }
    }
  }

  Uint8List _encodeBinaryRequest(
    String api,
    Map<String, dynamic>? params,
    GeneratedRequest? request,
  ) {
    final meta = _schema[api];
    if (meta == null)
      throw LuxoError(
        'ConfigError',
        0,
        'no schema for "$api" — use LuxoClient.create()',
      );

    final enc = LuxoEncoder();
    _writeRequest(enc, meta, params, request);

    return enc.bytes();
  }

  Future<dynamic> _binaryCall(
    Uint8List body,
    Stopwatch clock, {
    bool isRetry = false,
  }) async {
    final credential = _credential;
    final resp = await _post(_binaryHeaders, body, clock);

    // The HTTP response owns its collected body; no second copy is needed.
    if (resp.statusCode == 200) {
      if (resp.bodyBytes.isEmpty) throw _missingBinaryResponse(resp.statusCode);
      return resp.bodyBytes;
    }
    final error = decodeBinaryError(resp.bodyBytes, resp.statusCode);
    final refresh = _refreshAfterUnauthorized(
      error,
      resp.statusCode,
      isRetry,
      clock,
      credential,
    );
    if (refresh != null && await refresh && _canRetry(credential, clock)) {
      return _binaryCall(body, clock, isRetry: true);
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

final class _PendingCall {
  final TransportMode mode;
  final JSONResponseSchema? response;
  final completer = Completer<dynamic>();
  _PendingCall(this.mode, this.response);
}

class _Subscription<T> {
  final JSONResponseSchema? response;
  final T Function(Object?) decoder;
  final void Function(T) onData;
  final void Function(LuxoError)? onError;
  final void Function()? onDone;
  Completer<void Function()>? acknowledgement;

  _Subscription(
    TransportMode mode,
    this.decoder,
    this.onData,
    this.acknowledgement, {
    JSONResponseSchema? response,
    this.onError,
    this.onDone,
  }) : response = mode == TransportMode.json
           ? response ?? const JSONResponseSchema()
           : null;

  // Binary payloads do not use JSON response metadata.
  TransportMode get mode =>
      response == null ? TransportMode.binary : TransportMode.json;

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

  /// Separate limits for connection setup, response/ACK and token renewal.
  final Duration timeout;

  /// Refreshes an expired access token before reconnecting; old work is not replayed.
  final Future<String?> Function()? onTokenExpired;
  int _credentialRevision = 0;
  Future<void>? _refreshing;
  Completer<void>? _refreshAbort;
  LuxoError? _authError;
  TransportMode _mode;
  Map<String, APISchemaEntry> _schema = {};
  var _jsonRequests = JSONRequestEncoder();
  String? _token;

  LuxoSocket? _ws;
  var _requestIDs = RequestIDs();
  final _pending = <Object, _PendingCall>{};
  late final _jsonResponses = JSONSocketDecoder(_responseSchemaFor);
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
    this.onTokenExpired,
    this.timeout = const Duration(seconds: 30),
  }) : _mode = options?.mode ?? TransportMode.json,
       _token = options?.token,
       _autoReconnect = autoReconnect;

  @override
  void setSchema(Map<String, APISchemaEntry> schema) {
    final snapshot = snapshotAPISchema(schema);
    _jsonRequests = JSONRequestEncoder(snapshot);
    _schema = snapshot;
  }

  @override
  void setMode(TransportMode mode) => _mode = mode;
  @override
  void setToken(String token) {
    _credentialRevision++;
    _token = token;
    _authError = null;
    final abort = _refreshAbort;
    if (abort != null && !abort.isCompleted) abort.complete();
  }

  Future<void> _connect() {
    if (_closed)
      return Future.error(LuxoError('NetworkError', 0, 'WebSocket closed'));
    final refresh = _refreshing;
    if (refresh != null) return _connectAfterRefresh(refresh);
    final failure = _authError;
    if (failure != null) return Future.error(failure);
    if (_ws != null) return Future.value();
    final pending = _connecting;
    if (pending != null) return pending.future;
    final ready = Completer<void>();
    _connecting = ready;
    unawaited(_open(ready));
    return ready.future;
  }

  Future<void> _connectAfterRefresh(Future<void> refresh) async {
    await refresh;
    return _connect();
  }

  void _beginTokenRenewal() {
    _authError = const LuxoError('Unauthorized', 401, 'access token expired');
    if (onTokenExpired == null || _closed) return;
    final abort = Completer<void>();
    _refreshAbort = abort;
    _refreshing = _renewAccessToken(abort, _credentialRevision);
  }

  Future<void> _renewAccessToken(Completer<void> abort, int revision) async {
    // Detach old work before invoking a user callback that can re-enter transport.
    await Future<void>.value();
    try {
      if (_closed || abort.isCompleted) return;
      final token = await withDeadline(
        Future.any<String?>([
          Future<String?>.sync(onTokenExpired!),
          _cancelledRenewal(abort),
        ]),
        timeout,
      );
      if (_closed || abort.isCompleted || revision != _credentialRevision)
        return;
      if (token == null || token.isEmpty) {
        throw const LuxoError('Unauthorized', 401, 'token renewal failed');
      }
      _token = token;
      _credentialRevision++;
      _authError = null;
    } catch (_) {
      if (!_closed && revision == _credentialRevision) {
        _authError = const LuxoError(
          'Unauthorized',
          401,
          'token renewal failed',
        );
      }
    } finally {
      if (identical(_refreshAbort, abort)) {
        _refreshAbort = null;
        _refreshing = null;
        _scheduleReconnect();
      }
    }
  }

  Future<String?> _cancelledRenewal(Completer<void> abort) async {
    await abort.future;
    return null;
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
      final ws = await connectSocket(url, timeout: timeout);
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
          e is TimeoutException
              ? LuxoError(
                  'TimeoutError',
                  0,
                  'connection timed out after $timeout',
                )
              : LuxoError('NetworkError', 0, 'WebSocket failed: $e'),
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
          _handleBinaryResponse(Uint8List.fromList(data), ws);
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
    if (failure == null && ws.closeCode == 4001) {
      _beginTokenRenewal();
      failure = _authError;
    }
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
    for (final pending in _pending.values)
      pending.completer.completeError(error);
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
    if (_closed ||
        _authError != null ||
        _refreshing != null ||
        !_autoReconnect ||
        _ws != null ||
        _reconnectTimer != null)
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
    final mode = _mode;
    final meta = _schema[api];
    final requests = _jsonRequests;
    await _connect();
    final ws = _requireSocket();
    final id = _requestIDs.next();
    final pending = _PendingCall(mode, meta?.response);
    final completer = pending.completer;
    _pending[id] = pending;

    try {
      if (mode == TransportMode.binary) {
        if (meta == null) {
          throw LuxoError('ConfigError', 0, 'no schema for "$api"');
        }

        final enc = LuxoEncoder();
        enc.writeVarint(BinaryFrameType.callRequest);
        _writeSequence(enc, id);
        _writeRequest(enc, meta, params, request);
        if (_pending.containsKey(id)) ws.add(enc.bytes());
      } else {
        final body = requests.encode(
          api,
          _requestJSON(params, request),
          sequence: id,
        );
        if (_pending.containsKey(id)) ws.add(body);
      }
    } catch (error, stackTrace) {
      _pending.remove(id);
      if (!completer.isCompleted) completer.completeError(error, stackTrace);
    }

    try {
      final result = await withDeadline(completer.future, timeout);
      return decoder == null
          ? (result is Uint8List || pending.response != null
                    ? result
                    : normalizeLuxoJSON(result))
                as T
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
      json = _jsonResponses.decode(data);
    } on JSONModelDepthError catch (error) {
      _failJSONModelDepth(error.envelope);
      return;
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
    final pending = _pending[id];
    if (pending == null || pending.mode != TransportMode.json) return;
    _pending.remove(id);
    final c = pending.completer;
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

  JSONResponseSchema? _responseSchemaFor(Map<String, Object?> json) {
    if (json.containsKey(r'$end')) {
      if (json.containsKey(r'$sub') ||
          json.containsKey(r'$stream') ||
          json.containsKey(r'$id'))
        return null;
      final id = uint64FromJSON(json[r'$end']);
      return _subscriptions[id]?.mode == TransportMode.json
          ? const JSONResponseSchema()
          : null;
    }
    if (json.containsKey(r'$sub')) {
      final id = uint64FromJSON(json[r'$sub']);
      return _subscriptions[id]?.mode == TransportMode.json
          ? const JSONResponseSchema()
          : null;
    }
    if (json.containsKey(r'$stream')) {
      final id = uint64FromJSON(json[r'$stream']);
      final subscription = _subscriptions[id];
      if (subscription == null ||
          subscription.mode != TransportMode.json ||
          subscription.acknowledgement != null)
        return null;
      return subscription.response;
    }
    final id = uint64FromJSON(json[r'$id']);
    final pending = _pending[id];
    if (pending == null || pending.mode != TransportMode.json) return null;
    return pending.response ?? const JSONResponseSchema();
  }

  void _failJSONModelDepth(Map<String, Object?> json) {
    const error = LuxoError(
      'DecodeError',
      0,
      'Model nesting exceeds 64 levels',
    );
    if (json.containsKey(r'$stream')) {
      final id = uint64FromJSON(json[r'$stream']);
      final subscription = _subscriptions[id];
      if (id != null && subscription != null)
        _failSubscription(id, subscription, error);
    } else {
      final id = uint64FromJSON(json[r'$id']);
      _pending.remove(id)?.completer.completeError(error);
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

  void _handleBinaryResponse(Uint8List data, LuxoSocket ws) {
    if (data.length < 2) {
      _invalidBinaryMessage(ws);
      return;
    }
    final frameType = data[0];
    if (frameType != BinaryFrameType.callSuccess &&
        frameType != BinaryFrameType.callError &&
        frameType != BinaryFrameType.stream &&
        frameType != BinaryFrameType.subscribeSuccess &&
        frameType != BinaryFrameType.subscribeError &&
        frameType != BinaryFrameType.streamComplete &&
        frameType != BinaryFrameType.streamError) {
      _invalidBinaryMessage(ws);
      return;
    }
    var low = 0;
    var high = 0;
    for (var i = 0; i < 10 && i + 1 < data.length; i++) {
      final byte = data[i + 1];
      if (i == 9 && byte > 1) {
        _invalidBinaryMessage(ws);
        return;
      }
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
    _invalidBinaryMessage(ws);
  }

  void _invalidBinaryMessage(LuxoSocket ws) {
    _disconnected(
      ws,
      LuxoError('ParseError', 0, 'invalid WebSocket Binary message'),
    );
  }

  void _dispatchBinaryResponse(
    Uint8List data,
    int frameType,
    Object id,
    int off,
  ) {
    if (frameType == BinaryFrameType.subscribeSuccess ||
        frameType == BinaryFrameType.subscribeError) {
      _acknowledgeSubscription(
        id,
        TransportMode.binary,
        frameType == BinaryFrameType.subscribeError
            ? decodeBinaryError(Uint8List.sublistView(data, off), 0)
            : off == data.length
            ? null
            : LuxoError(
                'ParseError',
                0,
                'trailing subscription acknowledgement bytes',
              ),
      );
      return;
    }
    if (frameType == BinaryFrameType.stream) {
      if (off == data.length) {
        final subscription = _subscriptions[id];
        if (subscription != null &&
            subscription.mode == TransportMode.binary &&
            subscription.acknowledgement == null) {
          _failSubscription(id, subscription, _missingBinaryResponse());
        }
        return;
      }
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
    final pending = _pending[id];
    if (pending == null || pending.mode != TransportMode.binary) return;
    _pending.remove(id);
    final c = pending.completer;
    if (frameType == BinaryFrameType.callError) {
      c.completeError(decodeBinaryError(Uint8List.sublistView(data, off), 0));
    } else if (off == data.length) {
      c.completeError(_missingBinaryResponse());
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
    final mode = _mode;
    final meta = _schema[api];
    final requests = _jsonRequests;
    await _connect();
    final ws = _requireSocket();
    final id = _requestIDs.next();
    final acknowledgement = Completer<void Function()>();
    final subscription = _Subscription<T>(
      mode,
      decoder,
      onData,
      acknowledgement,
      response: meta?.response,
      onError: onError,
      onDone: onDone,
    );
    _subscriptions[id] = subscription;
    try {
      _sendSubscription(
        ws,
        id,
        api,
        params,
        subscription.mode,
        request,
        meta,
        requests,
      );
    } catch (error, stackTrace) {
      _subscriptions.remove(id);
      if (!acknowledgement.isCompleted) {
        acknowledgement.completeError(error, stackTrace);
      }
    }
    try {
      return await withDeadline(acknowledgement.future, timeout);
    } on TimeoutException {
      try {
        _unsubscribe(id);
      } catch (_) {
        // Remote cancellation is advisory; preserve the local timeout result.
      }
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
    var message = 'invalid subscription payload';
    try {
      message = cause is LuxoError ? cause.message : cause.toString();
    } catch (_) {
      /* Diagnostic formatting must not prevent local retirement. */
    }
    final error = LuxoError('DecodeError', 0, message);
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
    APISchemaEntry? meta,
    JSONRequestEncoder requests,
  ) {
    if (mode == TransportMode.json) {
      final body = requests.encode(
        api,
        _requestJSON(params, request),
        sequence: id,
        subscription: true,
      );
      if (_subscriptions.containsKey(id)) ws.add(body);
      return;
    }
    if (meta == null) throw LuxoError('ConfigError', 0, 'no schema for "$api"');
    final enc = LuxoEncoder();
    enc.writeVarint(BinaryFrameType.subscribe);
    _writeSequence(enc, id);
    _writeRequest(enc, meta, params, request);
    if (_subscriptions.containsKey(id)) ws.add(enc.bytes());
  }

  @override
  void close() {
    _closed = true;
    _credentialRevision++;
    final abort = _refreshAbort;
    if (abort != null && !abort.isCompleted) abort.complete();
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
