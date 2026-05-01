/// Luxo API error with structured error info.
class LuxoError implements Exception {
  /// Error name (e.g., "NotFound", "Unauthorized").
  final String error;

  /// HTTP-like status code.
  final int code;

  /// Human-readable message.
  final String message;

  /// Trace ID for debugging.
  final String? traceId;

  const LuxoError(this.error, this.code, this.message, [this.traceId]);

  @override
  String toString() => 'LuxoError($error, $code, $message)';
}
