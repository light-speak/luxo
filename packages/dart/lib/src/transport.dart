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

/// Transport interface -- implement for different runtimes.
abstract class Transport {
  Future<T> call<T>(String api, {Map<String, dynamic>? params, T Function(dynamic)? decoder});
}

/// HTTP-based transport for Flutter / Dart server / CLI.
/// Supports both JSON (debugging) and binary (production) modes.
class HttpTransport implements Transport {
  final String endpoint;
  final Map<String, String> _headers = {'Content-Type': 'application/json'};
  final http.Client _client;
  TransportMode _mode;
  final Map<String, APISchemaEntry> _schema;

  HttpTransport(
    this.endpoint, {
    String? token,
    Map<String, String>? headers,
    http.Client? client,
    TransportMode mode = TransportMode.json,
    Map<String, APISchemaEntry>? schema,
  })  : _client = client ?? http.Client(),
        _mode = mode,
        _schema = schema ?? {} {
    if (headers != null) _headers.addAll(headers);
    if (token != null) _headers['Authorization'] = 'Bearer $token';
  }

  @override
  Future<T> call<T>(String api, {Map<String, dynamic>? params, T Function(dynamic)? decoder}) async {
    if (_mode == TransportMode.binary) {
      return _callBinary<T>(api, params: params, decoder: decoder);
    }
    return _callJSON<T>(api, params: params, decoder: decoder);
  }

  Future<T> _callJSON<T>(String api, {Map<String, dynamic>? params, T Function(dynamic)? decoder}) async {
    final body = <String, dynamic>{r'$api': api};
    if (params != null) body.addAll(params);

    final http.Response resp;
    try {
      resp = await _client.post(Uri.parse(endpoint), headers: _headers, body: jsonEncode(body));
    } catch (e) {
      throw LuxoError('NetworkError', 0, e.toString());
    }

    final Map<String, dynamic> json;
    try {
      json = jsonDecode(resp.body) as Map<String, dynamic>;
    } catch (e) {
      throw LuxoError('ParseError', resp.statusCode, 'invalid JSON response: $e');
    }

    if (json.containsKey('error')) {
      throw LuxoError(
        (json['error'] ?? 'Unknown') as String,
        (json['code'] ?? 0) as int,
        (json['message'] ?? '') as String,
        json['traceId'] as String?,
      );
    }

    final data = json['data'];
    if (decoder != null) return decoder(data);
    return data as T;
  }

  Future<T> _callBinary<T>(String api, {Map<String, dynamic>? params, T Function(dynamic)? decoder}) async {
    final meta = _schema[api];
    if (meta == null) {
      throw LuxoError('ConfigError', 0, 'no schema for API "$api" — binary mode requires schema');
    }

    // Encode binary request
    final enc = LuxoEncoder();
    enc.writeVarint(meta.id);
    enc.writeVarint(0); // field mask = 0 (SELECT *)

    if (params != null) {
      for (final pm in meta.params) {
        final v = params[pm.name];
        if (v == null) continue;
        switch (pm.type) {
          case 'Int':
            enc.writeFieldInt(pm.fieldID, v as int);
          case 'Float':
            enc.writeFieldFloat(pm.fieldID, (v as num).toDouble());
          case 'String':
            enc.writeFieldString(pm.fieldID, v as String);
          case 'Boolean':
            enc.writeFieldBool(pm.fieldID, v as bool);
        }
      }
    }
    enc.writeEnd();

    final http.Response resp;
    try {
      resp = await _client.post(
        Uri.parse(endpoint),
        headers: {
          'Content-Type': 'application/x-luxo',
          'X-Luxo-Mode': 'binary',
          ..._headers,
        },
        body: enc.bytes(),
      );
    } catch (e) {
      throw LuxoError('NetworkError', 0, e.toString());
    }

    if (resp.statusCode != 200) {
      try {
        final json = jsonDecode(resp.body) as Map<String, dynamic>;
        throw LuxoError(
          (json['error'] ?? 'Error') as String,
          (json['code'] ?? resp.statusCode) as int,
          (json['message'] ?? '') as String,
          json['traceId'] as String?,
        );
      } catch (e) {
        if (e is LuxoError) rethrow;
        throw LuxoError('Error', resp.statusCode, 'HTTP ${resp.statusCode}');
      }
    }

    // Return raw bytes — generated client decodes with ReadLuxo
    final data = Uint8List.fromList(resp.bodyBytes);
    if (decoder != null) return decoder(data);
    return data as T;
  }

  /// Update authorization token.
  void setToken(String token) {
    _headers['Authorization'] = 'Bearer $token';
  }

  /// Switch transport mode at runtime.
  void setMode(TransportMode mode) {
    _mode = mode;
  }

  /// Close the underlying HTTP client.
  void close() => _client.close();
}
