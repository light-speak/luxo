import 'package:test/test.dart';

import 'json_kinds_benchmark.dart' as benchmark;

void main() {
  test(
    'JSON list kind checks',
    benchmark.main,
    timeout: const Timeout(Duration(minutes: 2)),
  );
  test(
    'JSON list construction candidates',
    benchmark.compareCandidates,
    timeout: const Timeout(Duration(minutes: 2)),
  );
}
