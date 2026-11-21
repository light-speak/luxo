import 'dart:async';
import 'dart:typed_data';

import 'package:luxo_client/luxo_client.dart';
import 'package:luxo_client/src/transport.dart' show wsTransportWithSequence;

class EmptyBinaryRequest extends GeneratedRequest {
  @override
  final int apiID;
  EmptyBinaryRequest(this.apiID);
  @override
  void writeParameters(LuxoEncoder encoder) {}
  @override
  Map<String, dynamic> toJSONParameters() =>
      throw StateError('Binary request used JSON parameters');
}

List<Uint8List> binaryPresenceReplies(Object? message) {
  final decoder = LuxoDecoder(Uint8List.fromList(message as List<int>));
  final tag = decoder.readBoundedUnsigned(127, 'fixture tag');
  final id = decoder.readBoundedUnsigned(127, 'fixture id');
  Uint8List frame(int tag, [bool present = false]) =>
      Uint8List.fromList([tag, id, if (present) 0]);
  if (tag == BinaryFrameType.unsubscribe) return [];
  if (tag == BinaryFrameType.callRequest) return [frame(2, id != 3)];
  return [
    frame(0x13),
    frame(0x12, true),
    if (id == 1) ...[frame(0x12), frame(0x12), frame(0x15)],
  ];
}

Future<void> verifyWebSocketBinaryPresence(String endpoint) async {
  for (final generated in [false, true]) {
    final transport = WsTransport(
      endpoint,
      options: const TransportOptions(mode: TransportMode.binary),
      autoReconnect: false,
      timeout: const Duration(seconds: 2),
    );
    transport.setSchema({'echo': const APISchemaEntry(7)});
    final request = generated ? EmptyBinaryRequest(7) : null;
    final failed = Completer<LuxoError>(), peer = Completer<Object?>();
    final values = <Object?>[];
    var errors = 0, completed = 0, decoded = 0;
    Object? decode(Object? value) {
      decoded++;
      return value is Uint8List && value.length == 1 && value[0] == 0
          ? null
          : value;
    }

    try {
      final stop = await transport.subscribe<Object?>(
        'echo',
        {},
        values.add,
        request: request,
        decoder: decode,
        onDone: () {
          completed++;
        },
        onError: (error) {
          errors++;
          if (!failed.isCompleted) failed.complete(error);
        },
      );
      final stopPeer = await transport.subscribe<Object?>(
        'echo',
        {},
        peer.complete,
        request: request,
        decoder: decode,
      );
      final error = await failed.future.timeout(const Duration(seconds: 3));
      if (error.error != 'ParseError' ||
          await peer.future != null ||
          values.length != 1 ||
          values.single != null ||
          errors != 1 ||
          completed != 0 ||
          decoded != 2) {
        throw StateError(
          'missing Binary stream reached decoder or peer: $error/$values/$errors/$completed/$decoded',
        );
      }
      try {
        await transport.call('echo', request: request);
        throw StateError('missing Binary call body accepted');
      } on LuxoError catch (error) {
        if (error.error != 'ParseError') rethrow;
      }
      final value = await transport.call('echo', request: request);
      if (value is! Uint8List || value.length != 1 || value[0] != 0) {
        throw StateError('Binary null marker was lost: $value');
      }
      stop();
      stopPeer();
    } finally {
      transport.close();
    }
  }
}

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
