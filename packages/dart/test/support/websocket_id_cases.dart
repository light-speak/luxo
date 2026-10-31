import 'dart:async';
import 'dart:typed_data';

import 'package:luxo_client/luxo_client.dart';
import 'package:luxo_client/src/transport.dart' show wsTransportWithSequence;

/// Runs unchanged on VM, dart2js and dart2wasm against a real loopback socket.
Future<void> verifyWebSocketIDs(String endpoint) async {
  await _verifyJSONParameters(endpoint);
  for (final mode in TransportMode.values) {
    await _verifySubscriptionIDs(endpoint, mode);
    for (final start in [
      '9007199254740990',
      '9223372036854775806',
      '18446744073709551614',
    ]) {
      final transport = wsTransportWithSequence(
        endpoint,
        BigInt.parse(start),
        options: TransportOptions(mode: mode),
      );
      transport.setSchema({
        'echo': const APISchemaEntry(7, [ParamSchema(1, 'value', 'Int')]),
      });
      Future<LuxoInt64> call() => transport.call(
        'echo',
        params: {'value': LuxoInt64.maxValue},
        decoder: (data) => data is Uint8List
            ? LuxoDecoder(data).readIntChecked()
            : luxoInt64FromJSON(data),
      );
      try {
        if (await call() != LuxoInt64.maxValue)
          throw StateError('large ID lost response');
        if (start == '18446744073709551614') {
          for (var i = 0; i < 2; i++) {
            try {
              await call();
              throw StateError('request counter wrapped');
            } on LuxoError catch (error) {
              if (error.error != 'RequestIDExhausted') rethrow;
            }
          }
        } else {
          final responses = await Future.wait([call(), call()]);
          if (responses.any((value) => value != LuxoInt64.maxValue)) {
            throw StateError('adjacent large IDs lost precision');
          }
        }
      } finally {
        transport.close();
      }
    }
  }
}

Future<void> _verifySubscriptionIDs(String endpoint, TransportMode mode) async {
  for (final start in [
    '9007199254740990',
    '9223372036854775806',
    '18446744073709551613',
  ]) {
    final transport =
        wsTransportWithSequence(
          endpoint,
          BigInt.parse(start),
          options: TransportOptions(mode: mode),
        )..setSchema({
          'echo': const APISchemaEntry(7, [ParamSchema(1, 'value', 'Int')]),
        });
    try {
      final received = [Completer<LuxoInt64>(), Completer<LuxoInt64>()];
      final stops = await Future.wait([
        for (var i = 0; i < 2; i++)
          transport.subscribe('echo', {'value': LuxoInt64.maxValue}, (data) {
            received[i].complete(
              data is Uint8List
                  ? LuxoDecoder(data).readIntChecked()
                  : luxoInt64FromJSON(data),
            );
          }, decoder: (value) => value),
      ]);
      final values = await Future.wait(received.map((c) => c.future))
          .timeout(const Duration(seconds: 3));
      if (values.any((value) => value != LuxoInt64.maxValue)) {
        throw StateError('full-width subscription ID lost data');
      }
      for (final stop in stops) {
        stop();
        stop();
      }
      if (start == '18446744073709551613') {
        try {
          await transport.subscribe(
            'echo',
            {'value': LuxoInt64.maxValue},
            (_) {},
            decoder: (value) => value,
          );
          throw StateError('subscription counter wrapped');
        } on LuxoError catch (error) {
          if (error.error != 'RequestIDExhausted') rethrow;
        }
      }
    } finally {
      transport.close();
    }
  }
}

Future<void> _verifyJSONParameters(String endpoint) async {
  final transport = WsTransport(endpoint, autoReconnect: false);
  transport.setSchema({
    'echo': const APISchemaEntry(7, [ParamSchema(1, 'value', 'Int')]),
  });
  try {
    for (final invalid in <Object?>[
      1.0,
      1.5,
      true,
      '1',
      null,
      <Object?>[],
      BigInt.one,
    ]) {
      await _expectConfigError(
        transport.call<Object>('echo', params: {'value': invalid}),
      );
      await _expectConfigError(
        transport.subscribe(
          'echo',
          {'value': invalid},
          (_) {},
          decoder: (value) => value,
        ),
      );
    }
    final params = <String, Object?>{
      'value': LuxoInt64.maxValue,
      r'$api': double.nan,
      r'$id': double.infinity,
      r'$sub': double.negativeInfinity,
    };
    final response = await transport.call<LuxoInt64>(
      'echo',
      params: params,
      decoder: luxoInt64FromJSON,
    );
    if (response != LuxoInt64.maxValue)
      throw StateError('valid call failed after invalid parameters');
    final received = Completer<LuxoInt64>();
    final unsubscribe = await transport.subscribe(
      'echo',
      params,
      (data) => received.complete(luxoInt64FromJSON(data)),
      decoder: (value) => value,
    );
    if (await received.future.timeout(const Duration(seconds: 3)) !=
        LuxoInt64.maxValue) {
      throw StateError('valid subscription failed after invalid parameters');
    }
    unsubscribe();
  } finally {
    transport.close();
  }
}

Future<void> _expectConfigError(Future<Object?> request) async {
  try {
    await request;
    throw StateError('invalid integer was sent');
  } on LuxoError catch (error) {
    if (error.error != 'ConfigError') rethrow;
  }
}
