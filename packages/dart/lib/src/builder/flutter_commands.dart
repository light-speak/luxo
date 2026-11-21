import 'dart:convert';

/// Serializes source preparation before Flutter's machine-protocol restart.
Future<void> forwardFlutterCommands(
  Stream<String> commands, {
  required Future<void> Function() refresh,
  required void Function(String) send,
  required void Function(String) respond,
}) async {
  await for (final line in commands) {
    final Object? decoded;
    try {
      decoded = jsonDecode(line);
    } on FormatException {
      send(line);
      continue;
    }
    if (decoded is! List || decoded.length != 1 || decoded.single is! Map) {
      send(line);
      continue;
    }
    final command = decoded.single as Map;
    if (command['method'] == 'app.restart') {
      try {
        await refresh();
      } catch (error) {
        respond(
          jsonEncode([
            {
              'id': command['id'],
              'error': 'Luxo selection build failed: $error',
            },
          ]),
        );
        continue;
      }
    }
    send(line);
  }
}
