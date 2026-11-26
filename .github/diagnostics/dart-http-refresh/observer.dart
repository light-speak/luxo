import 'dart:isolate';

import 'package:vm_service/vm_service_io.dart';

Future<void> main(List<String> arguments, SendPort parent) async {
  final service = await vmServiceConnectUri(arguments[0]);
  final replies = ReceivePort();
  parent.send(replies.sendPort);
  try {
    await for (final bool reset in replies) {
      final profile = await service.getAllocationProfile(
        arguments[1],
        reset: reset,
        gc: reset,
      );
      var bytes = 0;
      var objects = 0;
      for (final stats in profile.members!) {
        bytes += stats.accumulatedSize!;
        objects += stats.instancesAccumulated!;
      }
      parent.send([bytes, objects]);
    }
  } finally {
    replies.close();
    await service.dispose();
  }
}
