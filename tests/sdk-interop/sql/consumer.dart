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
  try {
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
