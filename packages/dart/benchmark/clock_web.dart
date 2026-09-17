import 'dart:js_interop';

@JS('performance.now')
external double _now();

double nowNanoseconds() => _now() * 1e6;
