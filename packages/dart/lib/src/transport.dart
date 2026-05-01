import 'dart:convert';
import 'package:http/http.dart' as http;
import 'error.dart';

/// Transport interface -- implement for different runtimes.
abstract class Transport {
  Future<T> call<T>(String api, {Map<String, dynamic>? params, T Function(dynamic)? decoder});
}

/// HTTP-based transport for Flutter / Dart server / CLI.
class HttpTransport implements Transport {
  final String endpoint;
  final Map<String, String> _headers = {'Content-Type': 'application/json'};
  final http.Client _client;

  HttpTransport(
    this.endpoint, {
    String? token,
    Map<String, String>? headers,
    http.Client? client,
  }) : _client = client ?? http.Client() {
    if (headers != null) _headers.addAll(headers);
    if (token != null) _headers['Authorization'] = 'Bearer $token';
  }

  @override
  Future<T> call<T>(String api, {Map<String, dynamic>? params, T Function(dynamic)? decoder}) async {
    final body = <String, dynamic>{r'$api': api};
    if (params != null) body.addAll(params);

    final resp = await _client.post(
      Uri.parse(endpoint),
      headers: _headers,
      body: jsonEncode(body),
    );

    final json = jsonDecode(resp.body) as Map<String, dynamic>;

    if (json.containsKey('error')) {
      throw LuxoError(
        json['error'] as String,
        json['code'] as int,
        json['message'] as String,
        json['traceId'] as String?,
      );
    }

    final data = json['data'];
    if (decoder != null) return decoder(data);
    return data as T;
  }

  /// Update authorization token.
  void setToken(String token) {
    _headers['Authorization'] = 'Bearer $token';
  }

  /// Close the underlying HTTP client.
  void close() => _client.close();
}
