import 'dart:async';

import 'package:luxo_client/src/deadline.dart';
import 'package:test/test.dart';

void main() {
  test(
    'an already completed producer still settles after the call returns',
    () async {
      var returned = false;
      final pending = withDeadline(
        Future.value(42),
        const Duration(seconds: 1),
      );
      final checked = pending.then((value) {
        expect(returned, isTrue);
        return value;
      });
      returned = true;
      expect(await checked, 42);
    },
  );
  test('an early timer cannot expire a pending operation', () async {
    final harness = _Timers();
    final clock = _Clock();
    final source = Completer<int>();
    final pending = harness.run(
      () => withDeadline(
        source.future,
        const Duration(microseconds: 1500),
        clock: clock,
      ),
    );
    var completed = false;
    final observed = pending.then((value) {
      completed = true;
      return value;
    });
    clock.value = const Duration(microseconds: 1499);
    harness.last.fire();
    await Future<void>.value();
    expect(completed, isFalse);
    expect(harness.active, 1);
    source.complete(42);
    expect(await observed, 42);
    expect(harness.active, 0);
    harness.last.fire(stale: true);
    expect(harness.active, 0);
  });

  test('the deadline fires once and handles late producer errors', () async {
    final harness = _Timers();
    final clock = _Clock();
    final source = Completer<int>();
    var expired = 0;
    final pending = harness.run(
      () => withDeadline(
        source.future,
        const Duration(microseconds: 1500),
        clock: clock,
        onTimeout: () {
          expired++;
          return 7;
        },
      ),
    );
    clock.value = const Duration(microseconds: 1500);
    harness.last.fire();
    expect(await pending, 7);
    harness.last.fire(stale: true);
    source.completeError(StateError('late'));
    await Future<void>.value();
    expect(expired, 1);
    expect(harness.active, 0);
  });

  test('default expiration preserves TimeoutException duration', () async {
    final harness = _Timers();
    final clock = _Clock();
    final pending = harness.run(
      () => withDeadline(
        Completer<int>().future,
        const Duration(milliseconds: 3),
        clock: clock,
      ),
    );
    final checked = expectLater(
      pending,
      throwsA(
        isA<TimeoutException>().having(
          (error) => error.duration,
          'duration',
          const Duration(milliseconds: 3),
        ),
      ),
    );
    clock.value = const Duration(milliseconds: 3);
    harness.last.fire();
    await checked;
  });

  test(
    'producer errors and timeout callback errors preserve identity',
    () async {
      final failure = StateError('producer');
      final stack = StackTrace.current;
      final harness = _Timers();
      final source = Completer<int>();
      final pending = harness.run(
        () => withDeadline(source.future, const Duration(seconds: 1)),
      );
      source.completeError(failure, stack);
      try {
        await pending;
        fail('producer error disappeared');
      } catch (error, actual) {
        expect(error, same(failure));
        expect(actual, same(stack));
      }
      expect(harness.active, 0);

      final clock = _Clock();
      final expired = harness.run(
        () => withDeadline(
          Completer<int>().future,
          const Duration(seconds: 1),
          clock: clock,
          onTimeout: () => throw failure,
        ),
      );
      final checked = expectLater(expired, throwsA(same(failure)));
      clock.value = const Duration(seconds: 1);
      harness.last.fire();
      await checked;
    },
  );

  test(
    'an asynchronous timeout result retains its completion semantics',
    () async {
      final harness = _Timers();
      final clock = _Clock();
      final fallback = Completer<int>();
      final source = Completer<int>();
      final pending = harness.run(
        () => withDeadline(
          source.future,
          const Duration(seconds: 1),
          clock: clock,
          onTimeout: () => fallback.future,
        ),
      );
      clock.value = const Duration(seconds: 1);
      harness.last.fire();
      source.complete(1);
      fallback.complete(9);
      expect(await pending, 9);
      expect(harness.active, 0);
    },
  );

  for (final duration in [
    Duration.zero,
    const Duration(microseconds: -1),
    const Duration(microseconds: -0x8000000000000000),
  ]) {
    test('a completed producer wins the queued timer for $duration', () async {
      final harness = _Timers();
      final pending = harness.run(
        () => withDeadline(Future.value(42), duration),
      );
      expect(await pending, 42);
      expect(harness.active, 0);
    });
    test('a pending producer expires for $duration', () async {
      final harness = _Timers();
      final pending = harness.run(
        () => withDeadline(Completer<int>().future, duration),
      );
      final checked = expectLater(pending, throwsA(isA<TimeoutException>()));
      harness.last.fire();
      await checked;
    });
  }

  test('very long limits rearm with one live timer and no overflow', () async {
    final harness = _Timers();
    final clock = _Clock();
    const limit = Duration(microseconds: 0x7fffffffffffffff);
    final source = Completer<int>();
    final pending = harness.run(
      () => withDeadline(source.future, limit, clock: clock),
    );
    for (var day = 1; day <= 3; day++) {
      expect(harness.last.duration, const Duration(days: 1));
      clock.value = Duration(days: day);
      harness.last.fire();
      expect(harness.active, 1);
    }
    clock.value = limit - const Duration(microseconds: 1);
    harness.last.fire();
    expect(harness.last.duration, const Duration(milliseconds: 1));
    source.complete(42);
    expect(await pending, 42);
    expect(harness.active, 0);
  });

  test('a reentrant cancelled callback cannot rearm completed work', () async {
    final harness = _Timers()..fireOnCancel = true;
    final source = Completer<int>();
    final pending = harness.run(
      () => withDeadline(source.future, const Duration(seconds: 1)),
    );
    source.complete(42);
    expect(await pending, 42);
    expect(harness.active, 0);
  });

  test('early cancellation reentrancy keeps one timer', () async {
    final harness = _Timers()..fireOnCancel = true;
    final clock = _Clock();
    final source = Completer<int>();
    final pending = harness.run(
      () =>
          withDeadline(source.future, const Duration(seconds: 1), clock: clock),
    );
    harness.last.fire();
    expect(harness.active, 1);
    source.complete(42);
    expect(await pending, 42);
  });

  test(
    'a producer completing during early timer cancellation prevents rearming',
    () async {
      final harness = _Timers();
      final clock = _Clock();
      final source = Completer<int>.sync();
      harness.onCancel = () => source.complete(42);
      final pending = harness.run(
        () => withDeadline(
          source.future,
          const Duration(seconds: 1),
          clock: clock,
        ),
      );
      harness.last.fire();
      expect(await pending, 42);
      expect(harness.active, 0);
      expect(harness.timers, hasLength(1));
    },
  );
}

class _Clock extends Stopwatch {
  Duration value = Duration.zero;
  @override
  Duration get elapsed => value;
  @override
  int get elapsedMicroseconds => value.inMicroseconds;
}

class _Timers {
  final timers = <_Timer>[];
  bool fireOnCancel = false;
  void Function()? onCancel;
  _Timer get last => timers.last;
  int get active => timers.where((timer) => timer.isActive).length;
  T run<T>(T Function() body) => runZoned(
    body,
    zoneSpecification: ZoneSpecification(
      createTimer: (self, parent, zone, duration, callback) {
        final timer = _Timer(duration, callback, fireOnCancel, zone, onCancel);
        timers.add(timer);
        return timer;
      },
    ),
  );
}

class _Timer implements Timer {
  final Duration duration;
  final void Function() callback;
  final bool fireOnCancel;
  final Zone zone;
  final void Function()? onCancel;
  bool _active = true;
  int _tick = 0;
  _Timer(
    this.duration,
    this.callback,
    this.fireOnCancel,
    this.zone,
    this.onCancel,
  );
  @override
  bool get isActive => _active;
  @override
  int get tick => _tick;
  @override
  void cancel() {
    _active = false;
    if (fireOnCancel) zone.run(callback);
    onCancel?.call();
  }

  void fire({bool stale = false}) {
    if (!_active && !stale) throw StateError('inactive timer fired');
    _active = false;
    _tick++;
    zone.run(callback);
  }
}
