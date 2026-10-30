import 'package:build/build.dart';

/// Rejects obsolete API-wide hints instead of generating unused selections.
Builder selectHintsBuilder(BuilderOptions options) => throw UnsupportedError(
  'API-wide selection hints are not supported. Use dart run luxo_client:build '
  'or dart run luxo_client:flutter for per-call selection inference.',
);
