/// Luxo client SDK for Dart / Flutter.
///
/// Provides type-safe API calls with pluggable transport.
/// Supports JSON (debugging) and binary (production) modes.
library luxo_client;

export 'src/transport.dart';
export 'src/error.dart';
export 'src/types.dart';
export 'src/codec.dart';
export 'src/codegen.dart' show generate;
