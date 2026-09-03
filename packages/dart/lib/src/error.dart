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

  /// Structured application error data.
  final dynamic data;

  /// Development-only underlying cause.
  final String? cause;

  const LuxoError(
    this.error,
    this.code,
    this.message, [
    this.traceId,
    this.data,
    this.cause,
  ]);

  @override
  String toString() => 'LuxoError($error, $code, $message)';
}
