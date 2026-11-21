import 'dart:convert';
import 'dart:io';

import 'package:luxo_client/luxo_client.dart';
import 'package:luxo_client/http2.dart';

import 'generated/client.dart';
import 'generated/types.dart';

void narrow(List<Product> items, [int count = 128]) {
  if (items.length != count ||
      items.map((v) => v.name.value).toSet().length != count) {
    throw StateError('row count or duplicate names');
  }
  for (final item in items) {
    if (!item.name.value.startsWith('item-') ||
        item.id.isSelected ||
        item.price.isSelected ||
        item.description.isSelected) {
      throw StateError('unexpected selected fields');
    }
  }
}

Future<void> main(List<String> args) async {
  final initialization = Stopwatch()..start();
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
  try {
    if (args.length == 5 && args[2] == 'probe') {
      initialization.stop();
      await verifyFirstUse(
        client,
        args[3],
        args[4],
        initialization.elapsedMicroseconds,
      );
      return;
    }
    final plan = LuxoClient.$prepareSelection('name');
    if (args.length == 3 && args[2] == 'inferred') {
      await verifyInferred(client);
      return;
    }
    narrow(await client.products($selectionPlan: plan));
    narrow(await client.selectedProducts($selectionPlan: plan));
    final full = await client.selectedProducts(
      select: 'id,name,price,description',
    );
    if (full.length != 128) throw StateError('full count');
    for (final item in full) {
      final price = item.price.value.toInt();
      final expected = LuxoInt64.fromBigInt(
        BigInt.parse('9007199254740992') + BigInt.from(price),
      );
      if (item.id.value != expected ||
          item.name.value != 'item-$price' ||
          item.description.value != 'x' * 4096)
        throw StateError('full values');
    }
    final first = await client.listProducts(
      pageSize: LuxoInt64.fromInt(64),
      $selectionPlan: plan,
    );
    final second = await client.listProducts(
      page: LuxoInt64.fromInt(2),
      pageSize: LuxoInt64.fromInt(64),
      $selectionPlan: plan,
    );
    narrow(first.items, 64);
    narrow(second.items, 64);
    narrow([...first.items, ...second.items]);
    if (first.total != LuxoInt64.fromInt(128) ||
        second.total != first.total ||
        first.page != LuxoInt64.fromInt(1) ||
        second.page != LuxoInt64.fromInt(2) ||
        first.pageSize != LuxoInt64.fromInt(64) ||
        second.pageSize != first.pageSize) {
      throw StateError('pagination');
    }
    print(
      'Dart AOT: five generated H2 calls, 128 rows, exact Int64 and SQL projection passed',
    );
  } finally {
    client.close();
  }
}

Future<void> verifyFirstUse(
  LuxoClient client,
  String mode,
  String api,
  int initializationMicros,
) async {
  client.setMode(switch (mode) {
    'json' => TransportMode.json,
    'binary' => TransportMode.binary,
    _ => throw StateError('unknown probe mode'),
  });
  final invoke = switch (api) {
    'ownProducts' => () => inferredOwnFields(client),
    'ownPrivateProducts' => () => inferredOwnPrivateFields(client),
    'combinedProducts' => () => inferredCombinedFields(client),
    'forwardedProducts' => () => inferredForwardedFields(client),
    'scalarCalledProducts' => () => client.scalarCalledProducts(),
    'aliasedOwnProducts' => () => inferredAliasedOwnFields(client),
    _ => throw StateError('unknown probe API'),
  };
  final first = Stopwatch()..start();
  final names = await invoke();
  first.stop();
  verifyNames(names);
  for (var index = 0; index < 10; index++) {
    verifyNames(await invoke());
  }
  final steady = <int>[];
  for (var index = 0; index < 30; index++) {
    final watch = Stopwatch()..start();
    final values = await invoke();
    watch.stop();
    steady.add(watch.elapsedMicroseconds * 1000);
    verifyNames(values);
  }
  print(
    jsonEncode({
      'api': api,
      'mode': mode,
      'initializationNs': initializationMicros * 1000,
      'firstNs': first.elapsedMicroseconds * 1000,
      'steadyNs': steady,
      'verifiedCalls': 41,
    }),
  );
}

Future<List<String>> inferredFilter(LuxoClient client) async {
  final rows = await client.selectedProducts();
  return rows
      .where((row) => row.price.value.toInt() > 0)
      .map((row) => row.name.value)
      .toList();
}

Future<String> inferredFirst(LuxoClient client) async {
  final rows = await client.selectedProducts();
  return rows.firstWhere((row) => row.price.value.toInt() == 1).name.value;
}

Future<String> inferredAlias(LuxoClient client) async {
  final rows = await client.selectedProducts();
  final active = rows.where((row) => row.price.value.toInt() > 0).toList();
  return active[0].name.value;
}

Future<String> inferredMap(LuxoClient client) async {
  final rows = await client.selectedProducts();
  final packed = {'first': rows.first};
  return packed['first']!.name.value;
}

Future<String> inferredRecord(LuxoClient client) async {
  final rows = await client.selectedProducts();
  final packed = (first: rows.first);
  return packed.first.name.value;
}

Future<List<String>> inferredBusinessFields(LuxoClient client) async {
  final rows = await client.pricedProducts();
  return rows.map((row) => row.name.value).toList();
}

Future<List<String>> inferredPrivateFields(LuxoClient client) async {
  final rows = await client.privateProducts();
  return rows.map((row) => row.name.value).toList();
}

Future<List<String>> inferredCalledFields(LuxoClient client) async {
  final rows = await client.calledProducts();
  return rows.map((row) => row.name.value).toList();
}

Future<List<String>> inferredCalledPrivateFields(LuxoClient client) async {
  final rows = await client.calledPrivateProducts();
  return rows.map((row) => row.name.value).toList();
}

Future<List<String>> inferredNestedFields(LuxoClient client) async {
  final rows = await client.nestedProducts();
  return rows.map((row) => row.name.value).toList();
}

Future<List<String>> inferredOwnFields(LuxoClient client) async {
  final rows = await client.ownProducts();
  return rows.map((row) => row.name.value).toList();
}

Future<List<String>> inferredOwnPrivateFields(LuxoClient client) async {
  final rows = await client.ownPrivateProducts();
  return rows.map((row) => row.name.value).toList();
}

Future<List<String>> inferredCombinedFields(LuxoClient client) async {
  final rows = await client.combinedProducts();
  return rows.map((row) => row.name.value).toList();
}

Future<List<String>> inferredForwardedFields(LuxoClient client) async {
  final rows = await client.forwardedProducts();
  return rows.map((row) => row.name.value).toList();
}

Future<List<String>> inferredAliasedOwnFields(LuxoClient client) async {
  final rows = await client.aliasedOwnProducts();
  return rows.map((row) => row.name.value).toList();
}

void verifyNames(List<String> names) {
  final actual = [...names]..sort();
  final expected = List.generate(128, (i) => 'item-${i + 1}')..sort();
  if (actual.length != expected.length) throw StateError('inferred row count');
  for (var i = 0; i < actual.length; i++) {
    if (actual[i] != expected[i]) throw StateError('inferred name');
  }
}

Future<void> verifyInferred(LuxoClient client) async {
  for (final mode in [TransportMode.json, TransportMode.binary]) {
    client.setMode(mode);
    verifyNames(await inferredFilter(client));
    if (await inferredFirst(client) != 'item-1' ||
        await inferredAlias(client) != 'item-1' ||
        await inferredMap(client) != 'item-1' ||
        await inferredRecord(client) != 'item-1') {
      throw StateError('inferred selected field');
    }
    verifyNames(await inferredBusinessFields(client));
    verifyNames(await inferredPrivateFields(client));
    verifyNames(await inferredCalledFields(client));
    verifyNames(await inferredCalledPrivateFields(client));
    verifyNames(await inferredNestedFields(client));
    verifyNames(await inferredOwnFields(client));
    verifyNames(await inferredOwnPrivateFields(client));
    verifyNames(await inferredCombinedFields(client));
    verifyNames(await inferredForwardedFields(client));
    verifyNames(await client.scalarCalledProducts());
    verifyNames(await inferredAliasedOwnFields(client));
  }
  print('Dart inferred AOT: thirty-two JSON/Binary H2 calls passed');
}
