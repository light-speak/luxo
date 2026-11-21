import 'package:luxo_client/src/codegen.dart';

Future<void> main(List<String> args) =>
    generate(endpoint: args[0], key: 'benchmark', outDir: args[1]);
