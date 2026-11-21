import 'dart:async';

/// Waits without expiring before the monotonic budget, even for coarse timers.
/// A supplied running clock keeps multiple phases on the same deadline.
Future<T> withDeadline<T>(
  Future<T> pending,
  Duration timeout, {
  Stopwatch? clock,
  FutureOr<T> Function()? onTimeout,
}) {
  final result = Completer<T>();
  _Deadline(
    pending,
    timeout,
    result,
    clock ?? (Stopwatch()..start()),
    onTimeout,
  );
  return result.future;
}

class _Deadline<T> {
  static const _quantumMicros = Duration.microsecondsPerDay;
  final Duration _timeout;
  Completer<T>? _result;
  Stopwatch? _clock;
  FutureOr<T> Function()? _onTimeout;
  Timer? _timer;
  bool _checking = false;

  _Deadline(
    Future<T> pending,
    this._timeout,
    this._result,
    this._clock,
    this._onTimeout,
  ) {
    _schedule();
    unawaited(pending.then<void>(_complete, onError: _fail));
  }

  void _schedule() {
    final elapsed = _clock!.elapsedMicroseconds;
    final budget = _timeout.inMicroseconds;
    final remaining = budget <= elapsed ? 0 : budget - elapsed;
    // Bound conversion before rounding so even Duration's maximum cannot overflow.
    final micros = remaining > _quantumMicros ? _quantumMicros : remaining;
    final millis =
        micros ~/ Duration.microsecondsPerMillisecond +
        (micros % Duration.microsecondsPerMillisecond == 0 ? 0 : 1);
    _timer = Timer(Duration(milliseconds: millis), _check);
  }

  void _check() {
    final clock = _clock;
    if (clock == null || _checking) return;
    _checking = true;
    try {
      if (_timeout.inMicroseconds > clock.elapsedMicroseconds) {
        final timer = _timer;
        _timer = null;
        timer?.cancel();
        if (_clock != null) _schedule();
        return;
      }
      final callback = _onTimeout;
      final result = _finish()!;
      try {
        if (callback == null)
          throw TimeoutException('Future not completed', _timeout);
        result.complete(callback());
      } catch (error, stack) {
        result.completeError(error, stack);
      }
    } finally {
      _checking = false;
    }
  }

  Completer<T>? _finish() {
    final result = _result;
    if (result == null) return null;
    _result = null;
    _clock = null;
    _onTimeout = null;
    final timer = _timer;
    _timer = null;
    timer?.cancel();
    return result;
  }

  void _complete(T value) => _finish()?.complete(value);
  void _fail(Object error, StackTrace stack) =>
      _finish()?.completeError(error, stack);
}
