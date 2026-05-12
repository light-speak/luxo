import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';
import 'package:http/http.dart' as http;
import 'error.dart';
import 'codec.dart';

/// Transport mode: json for debugging, binary for production.
enum TransportMode { json, binary }

/// API schema metadata for binary encoding.
class APISchemaEntry {
  final int id;
  final List<ParamSchema> params;
  const APISchemaEntry(this.id, [this.params = const []]);
}

class ParamSchema {
  final int fieldID;
  final String name;
  final String type;
  const ParamSchema(this.fieldID, this.name, this.type);
}

/// Transport options.
class TransportOptions {
  final String? token;
  final TransportMode mode;
  final Map<String, String>? headers;
  const TransportOptions({this.token, this.mode = TransportMode.json, this.headers});
}

/// Transport interface — implemented by HTTP and WebSocket.
abstract class Transport {
  Future<dynamic> call(String api, {Map<String, dynamic>? params});
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

  HttpTransport(this.endpoint, {
    TransportOptions? options,
    http.Client? client,
    this.timeout = const Duration(seconds: 30),
    this.onTokenExpired,
  })  : _client = client ?? http.Client(),
        _mode = options?.mode ?? TransportMode.json,
        _schema = {} {
    if (options?.headers != null) _headers.addAll(options!.headers!);
    if (options?.token != null) _headers['Authorization'] = 'Bearer ${options!.token}';
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
  Future<dynamic> call(String api, {Map<String, dynamic>? params}) {
    return _mode == TransportMode.binary ? _binaryCall(api, params) : _jsonCall(api, params);
  }

  Future<dynamic> _jsonCall(String api, Map<String, dynamic>? params, {bool isRetry = false}) async {
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
      throw LuxoError((json['error'] ?? 'Unknown') as String, (json['code'] ?? 0) as int,
          (json['message'] ?? '') as String, json['traceId'] as String?);
    }
    return json['data'];
  }

  Future<dynamic> _binaryCall(String api, Map<String, dynamic>? params, {bool isRetry = false}) async {
    final meta = _schema[api];
    if (meta == null) throw LuxoError('ConfigError', 0, 'no schema for "$api" — use LuxoClient.create()');

    final enc = LuxoEncoder();
    enc.writeVarint(meta.id);
    enc.writeVarint(0);
    if (params != null) {
      for (final pm in meta.params) {
        final v = params[pm.name];
        if (v == null) continue;
        switch (pm.type) {
          case 'Int': enc.writeFieldInt(pm.fieldID, v as int);
          case 'Float': enc.writeFieldFloat(pm.fieldID, (v as num).toDouble());
          case 'String': enc.writeFieldString(pm.fieldID, v as String);
          case 'Boolean': enc.writeFieldBool(pm.fieldID, v as bool);
        }
      }
    }
    enc.writeEnd();

    final http.Response resp;
    try {
      resp = await _client.post(Uri.parse(endpoint),
          headers: {'Content-Type': 'application/x-luxo', 'X-Luxo-Mode': 'binary', ..._headers},
          body: enc.bytes())
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
      try {
        final json = jsonDecode(resp.body) as Map<String, dynamic>;
        throw LuxoError((json['error'] ?? 'Error') as String, (json['code'] ?? resp.statusCode) as int,
            (json['message'] ?? '') as String, json['traceId'] as String?);
      } catch (e) {
        if (e is LuxoError) rethrow;
        throw LuxoError('Error', resp.statusCode, 'HTTP ${resp.statusCode}');
      }
    }
    return Uint8List.fromList(resp.bodyBytes);
  }
}

// --- WebSocket Transport ---

/// WebSocket transport — persistent connection, multiplexed requests.
/// Supports automatic reconnection with exponential backoff.
class WsTransport implements Transport {
  final String endpoint;
  TransportMode _mode;
  Map<String, APISchemaEntry> _schema = {};
  String? _token;

  dynamic _ws; // WebSocket (dart:io or dart:html)
  int _seq = 0;
  final _pending = <int, Completer<dynamic>>{};
  Completer<void>? _connectCompleter;

  // Reconnection state
  bool _autoReconnect;
  bool _closed = false;
  int _reconnectAttempts = 0;
  static const int _maxBackoffSeconds = 30;
  Timer? _reconnectTimer;

  WsTransport(this.endpoint, {TransportOptions? options, bool autoReconnect = true})
      : _mode = options?.mode ?? TransportMode.json,
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
    final url = _token != null ? '$endpoint?token=$_token' : endpoint;

    try {
      // Use dart:io WebSocket
      final ws = await _platformConnect(url);
      _ws = ws;
      _reconnectAttempts = 0; // Reset backoff on successful connect
      _listenWs(ws);
      _connectCompleter!.complete();
    } catch (e) {
      _connectCompleter!.completeError(LuxoError('NetworkError', 0, 'WebSocket failed: $e'));
    }
    _connectCompleter = null;
  }

  // Platform connect — dart:io WebSocket.connect
  Future<dynamic> _platformConnect(String url) async {
    // Import dynamically to support both io and html
    final dynamic ws = await Function.apply(
      // ignore: avoid_dynamic_calls
      Uri.parse('dart:io').resolve('WebSocket').toString as dynamic,
      [url],
    );
    return ws;
  }

  void _listenWs(dynamic ws) {
    // Listen for messages and dispatch to pending requests
    (ws as Stream).listen((data) {
      if (_mode == TransportMode.binary && data is List<int>) {
        _handleBinaryResponse(Uint8List.fromList(data));
      } else if (data is String) {
        _handleJsonResponse(data);
      }
    }, onDone: () {
      _ws = null;
      for (final c in _pending.values) {
        c.completeError(LuxoError('NetworkError', 0, 'WebSocket closed'));
      }
      _pending.clear();
      _scheduleReconnect();
    });
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
  Future<dynamic> call(String api, {Map<String, dynamic>? params}) async {
    await _connect();
    final id = ++_seq;
    final completer = Completer<dynamic>();
    _pending[id] = completer;

    if (_mode == TransportMode.binary) {
      final meta = _schema[api];
      if (meta == null) throw LuxoError('ConfigError', 0, 'no schema for "$api"');

      final enc = LuxoEncoder();
      enc.writeVarint(id);
      enc.writeVarint(meta.id);
      enc.writeVarint(0);
      if (params != null) {
        for (final pm in meta.params) {
          final v = params[pm.name];
          if (v == null) continue;
          switch (pm.type) {
            case 'Int': enc.writeFieldInt(pm.fieldID, v as int);
            case 'Float': enc.writeFieldFloat(pm.fieldID, (v as num).toDouble());
            case 'String': enc.writeFieldString(pm.fieldID, v as String);
            case 'Boolean': enc.writeFieldBool(pm.fieldID, v as bool);
          }
        }
      }
      enc.writeEnd();
      (_ws as dynamic).add(enc.bytes());
    } else {
      final body = jsonEncode({r'$id': id, r'$api': api, ...?params});
      (_ws as dynamic).add(body);
    }

    return completer.future;
  }

  void _handleJsonResponse(String data) {
    try {
      final json = jsonDecode(data) as Map<String, dynamic>;
      final id = json[r'$id'] as int;
      final c = _pending.remove(id);
      if (c == null) return;
      if (json.containsKey('error')) {
        c.completeError(LuxoError(json['error'] as String, (json['code'] ?? 0) as int,
            (json['message'] ?? '') as String, json['traceId'] as String?));
      } else {
        c.complete(json['data']);
      }
    } catch (_) {}
  }

  void _handleBinaryResponse(Uint8List data) {
    final dec = LuxoDecoder(data);
    // Read seq varint
    int id = 0;
    int shift = 0;
    int off = 0;
    while (off < data.length) {
      final b = data[off++];
      id += (b & 0x7F) * (1 << shift);
      if (b < 0x80) break;
      shift += 7;
    }
    final c = _pending.remove(id);
    if (c == null) return;
    c.complete(data.sublist(off));
  }

  @override
  void close() {
    _closed = true;
    _autoReconnect = false;
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    (_ws as dynamic)?.close();
    _ws = null;
  }
}
