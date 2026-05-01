/// Luxo client codegen CLI.
///
/// Usage:
///   dart run luxo_client:generate --endpoint http://localhost:4000/luvia --key YOUR_KEY [--out lib/src/luxo/]
import 'package:luxo_client/src/codegen.dart';

void main(List<String> args) async {
  String? endpoint;
  String? key;
  String outDir = 'lib/src/luxo';

  for (int i = 0; i < args.length; i++) {
    switch (args[i]) {
      case '--endpoint':
        endpoint = args[++i];
      case '--key':
        key = args[++i];
      case '--out':
        outDir = args[++i];
    }
  }

  if (endpoint == null || key == null) {
    print('Usage: dart run luxo_client:generate --endpoint <url> --key <key> [--out <dir>]');
    return;
  }

  await generate(endpoint: endpoint, key: key, outDir: outDir);
}
