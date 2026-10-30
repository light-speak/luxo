import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:http/http.dart' as http;
import 'package:http2/transport.dart' as h2;

import 'buffered_http_response.dart';

part 'http2_exchange.dart';
part 'http2_socket.dart';

/// Native unary HTTP client with one TLS connection and no implicit replay.
/// Capacity is held through the complete response body, not just its headers.
/// Excess concurrent requests fail immediately; no hidden waiting queue exists.
/// Responses are buffered up to [maxBodyBytes]. Use [http.AbortableRequest]
/// (as Luxo's HttpTransport does) to cancel a request or apply its deadline.
class LuxoHttp2Client extends http.BaseClient {
  final Uri endpoint;
  final int maxConcurrentRequests;
  final int maxBodyBytes;
  final Duration connectTimeout;
  final SecurityContext? securityContext;
  final _pending = <_H2Exchange>{};
  h2.ClientTransportConnection? _connection;
  Future<h2.ClientTransportConnection>? _connecting;
  ConnectionTask<RawSocket>? _dialTask;
  Completer<void>? _dialCancelled;
  RawSocket? _socket;
  int _generation = 0;
  int _streams = 0;
  bool _closed = false;

  LuxoHttp2Client(
    this.endpoint, {
    this.maxConcurrentRequests = 16,
    this.maxBodyBytes = 2 * 1024 * 1024,
    this.connectTimeout = const Duration(seconds: 10),
    this.securityContext,
  }) {
    if (endpoint.scheme != 'https' ||
        endpoint.host.isEmpty ||
        endpoint.userInfo.isNotEmpty ||
        maxConcurrentRequests <= 0 ||
        maxBodyBytes <= 0 ||
        connectTimeout <= Duration.zero) {
      throw ArgumentError(
        'HTTPS, no URL credentials and positive limits required',
      );
    }
  }

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) {
    if (request is! http.Request) {
      return Future.error(
        http.ClientException(
          'Luxo HTTP/2 requires a buffered request',
          request.url,
        ),
      );
    }
    if (_closed ||
        request.url.userInfo.isNotEmpty ||
        request.url.origin != endpoint.origin) {
      return Future.error(
        http.ClientException(
          'HTTP/2 client closed or origin mismatch',
          request.url,
        ),
      );
    }
    if (_pending.length >= maxConcurrentRequests) {
      return Future.error(
        http.ClientException('HTTP/2 request capacity exhausted', request.url),
      );
    }
    final exchange = _H2Exchange(this, request);
    _pending.add(exchange);
    exchange.start();
    return exchange.result.future;
  }

  Future<h2.ClientTransportConnection> _getConnection() async {
    final existing = _connection;
    if (existing != null && existing.isOpen) return existing;
    if (_streams != 0)
      throw http.ClientException('HTTP/2 connection is draining');
    if (_closed) throw http.ClientException('HTTP/2 client closed');
    final inProgress = _connecting;
    if (inProgress != null) return inProgress;
    _connection = null;
    final generation = ++_generation;
    final cancelled = Completer<void>();
    _dialCancelled = cancelled;
    final future =
        Future.any([
          _dial(generation),
          cancelled.future.then<h2.ClientTransportConnection>(
            (_) => throw http.ClientException('HTTP/2 connection cancelled'),
          ),
        ]).timeout(
          connectTimeout,
          onTimeout: () {
            _cancelDial();
            throw TimeoutException(
              'HTTP/2 connection deadline exceeded',
              connectTimeout,
            );
          },
        );
    _connecting = future;
    try {
      return await future;
    } finally {
      if (identical(_connecting, future)) _connecting = null;
    }
  }

  Future<h2.ClientTransportConnection> _dial(int generation) async {
    unawaited(_socket?.close());
    final task = await RawSocket.startConnect(endpoint.host, endpoint.port);
    if (_closed || generation != _generation) {
      unawaited(
        task.socket.then<void>((socket) {
          unawaited(socket.close());
        }, onError: (Object _) {}),
      );
      task.cancel();
      throw http.ClientException('HTTP/2 connection cancelled');
    }
    _dialTask = task;
    try {
      final plain = await task.socket;
      if (_closed || generation != _generation) {
        unawaited(plain.close());
        throw http.ClientException('HTTP/2 connection cancelled');
      }
      _socket = plain;
      configureHttp2Socket(plain);
      final secure = await RawSecureSocket.secure(
        plain,
        host: endpoint.host,
        context: securityContext,
        supportedProtocols: ['h2'],
      );
      if (_closed ||
          generation != _generation ||
          secure.selectedProtocol != 'h2') {
        unawaited(secure.close());
        throw http.ClientException(
          'HTTP/2 cancelled or ALPN did not negotiate h2',
        );
      }
      final channel = _H2Socket(secure);
      final connection = h2.ClientTransportConnection.viaStreams(
        channel.incoming,
        channel.outgoing,
      );
      await Future.any([
        connection.onInitialPeerSettingsReceived,
        channel.closed.future.then<void>(
          (_) =>
              throw http.ClientException('HTTP/2 peer closed before SETTINGS'),
        ),
      ]);
      if (_closed || generation != _generation) {
        unawaited(secure.close());
        throw http.ClientException('HTTP/2 connection cancelled');
      }
      return _connection = connection;
    } catch (_) {
      if (generation == _generation) unawaited(_socket?.close());
      rethrow;
    }
  }

  void _cancelDial() {
    _generation++;
    final cancelled = _dialCancelled;
    if (cancelled != null && !cancelled.isCompleted) cancelled.complete();
    _dialTask?.cancel();
    unawaited(_socket?.close());
  }

  void _release(_H2Exchange exchange) {
    _pending.remove(exchange);
    if (exchange.stream != null) _streams--;
    if (_pending.isEmpty && _connection == null && _connecting != null) {
      _cancelDial();
    }
  }

  @override
  void close() {
    if (_closed) return;
    _closed = true;
    for (final exchange in _pending.toList()) {
      exchange.fail(http.RequestAbortedException(exchange.request.url));
    }
    _cancelDial();
  }
}
