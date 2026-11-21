import 'dart:convert';
import 'dart:io';

import 'package:luxo_client/luxo_client.dart';
import 'package:luxo_client/http2.dart';

import 'generated/client.dart';
import 'generated/types.dart';

final expectedNames = List.generate(128, (i) => 'item-${i + 1}')..sort();

void verify(List<Product> items) {
  if (items.length != 128) throw StateError('row count');
  final names = items.map((item) => item.name.value).toList()..sort();
  for (var i = 0; i < names.length; i++) {
    if (names[i] != expectedNames[i]) throw StateError('incorrect name');
  }
  for (final item in items) {
    if (item.id.isSelected ||
        item.price.isSelected ||
        item.description.isSelected) {
      throw StateError('unexpected selected fields');
    }
  }
}

Map<String, double> summarize(List<double> samples) {
  final sorted = [...samples]..sort();
  double at(double fraction) => sorted[(sorted.length * fraction).ceil() - 1];
  return {
    'p50Us': at(0.5),
    'p95Us': at(0.95),
    'p99Us': at(0.99),
    'meanUs': samples.reduce((a, b) => a + b) / samples.length,
  };
}

Future<void> main(List<String> args) async {
  final endpoint = Uri.parse(args[0]);
  final trust = SecurityContext(withTrustedRoots: false)
    ..setTrustedCertificates(args[1]);
  final client = LuxoClient(
    HttpTransport(
      endpoint.toString(),
      client: LuxoHttp2Client(endpoint, securityContext: trust),
      options: const TransportOptions(mode: TransportMode.binary),
    ),
  );
  final plan = LuxoClient.$prepareSelection('name');
  final paths = <String, Future<List<Product>> Function()>{
    'fullSQL': () => client.products($selectionPlan: plan),
    'selectedSQL': () => client.selectedProducts($selectionPlan: plan),
  };
  final batches = <Map<String, Object>>[];
  final clock = Stopwatch()..start();
  try {
    for (var i = 0; i < 20; i++) {
      for (final call in paths.values) {
        verify(await call());
      }
    }
    for (var batch = 0; batch < 3; batch++) {
      final samples = {'fullSQL': <double>[], 'selectedSQL': <double>[]};
      for (var i = 0; i < 50; i++) {
        final order = (batch + i).isOdd
            ? ['selectedSQL', 'fullSQL']
            : ['fullSQL', 'selectedSQL'];
        for (final name in order) {
          final start = clock.elapsedTicks;
          final items = await paths[name]!();
          final ticks = clock.elapsedTicks - start;
          samples[name]!.add(ticks * 1000000 / clock.frequency);
          verify(items);
        }
      }
      batches.add({
        'samples': samples,
        'summary': samples.map(
          (name, values) => MapEntry(name, summarize(values)),
        ),
      });
    }
    final evidence = {
      'runtime': Platform.version,
      'concurrency': 1,
      'rows': 128,
      'descriptionBytesPerRow': 4096,
      'selectedFields': ['name'],
      'batches': batches,
      'scope':
          'AOT SDK/TLS H2/PG calls; includes decoding, excludes assertions. '
          'No CPU or allocation claim.',
    };
    final root = Platform.environment['LUXO_SQL_SCRATCH']!;
    File('$root/projection-dart.json').writeAsStringSync(jsonEncode(evidence));
    print(
      jsonEncode({
        'summaries': batches.map((batch) => batch['summary']).toList(),
      }),
    );
  } finally {
    client.close();
  }
}
