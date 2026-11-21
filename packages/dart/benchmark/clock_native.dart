final _watch = Stopwatch()..start();

double nowNanoseconds() => _watch.elapsedTicks * 1e9 / _watch.frequency;
