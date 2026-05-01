/** Luxo API error with structured error info */
export class LuxoError extends Error {
  constructor(
    /** Error name (e.g., "NotFound", "Unauthorized") */
    public readonly error: string,
    /** HTTP-like status code */
    public readonly code: number,
    /** Human-readable message */
    message: string,
    /** Trace ID for debugging */
    public readonly traceId?: string,
  ) {
    super(message)
    this.name = 'LuxoError'
  }
}
