import 'dart:convert';
import 'dart:io';

import 'package:luxo_client/luxo_client.dart';
import 'package:luxo_client/http2.dart';

import 'generated/client.dart';
import 'generated/types.dart';

const select = 'id,whole,ratio,amount,moment,elapsed,token';

void verify(PrecisionRecord row, Map<String, Object?> item, LuxoInt64 id) {
  final ratio = double.parse(item['ratio']! as String);
  final actual = row.ratio.value;
  if (row.id.value != id ||
      row.whole.value != LuxoInt64.parse(item['whole']! as String) ||
      actual != ratio ||
      (actual == 0 && actual.isNegative != ratio.isNegative) ||
      row.amount.value != item['amount'] ||
      row.moment.value != item['storedMoment'] ||
      row.elapsed.value != LuxoInt64.parse(item['elapsed']! as String) ||
      row.token.value != (item['token']! as String).toLowerCase()) {
    throw StateError('persistent precision changed for $id');
  }
}

Future<void> main(List<String> args) async {
  final values = jsonDecode(
    File(Platform.environment['LUXO_SQL_PRECISION_CORPUS']!).readAsStringSync(),
  ) as List<Object?>;
  final cases = values
      .map((value) => Map<String, Object?>.from(value! as Map))
      .toList();
  final endpoint = Uri.parse(args[0]);
  final trust = SecurityContext(withTrustedRoots: false)
    ..setTrustedCertificates(args[1]);
  final client = LuxoClient(
    HttpTransport(
      endpoint.toString(),
      client: LuxoHttp2Client(endpoint, securityContext: trust),
    ),
  );
  try {
    if (Platform.environment['LUXO_SQL_NULLABLE'] == '1') {
      await verifyNullableStorage(client);
      return;
    }
    for (var modeIndex = 0; modeIndex < 2; modeIndex++) {
      client.setMode(
        modeIndex == 0 ? TransportMode.json : TransportMode.binary,
      );
      for (var index = 0; index < cases.length; index++) {
        final item = cases[index];
        final id = LuxoInt64.fromInt(modeIndex * 10 + index + 1);
        final row = await client.storePrecision(
          id: id,
          whole: LuxoInt64.parse(item['whole']! as String),
          ratio: double.parse(item['ratio']! as String),
          amount: item['amount']! as String,
          moment: item['moment']! as String,
          elapsed: LuxoInt64.parse(item['elapsed']! as String),
          token: item['token']! as String,
          select: select,
        );
        verify(row, item, id);
        verify((await client.readPrecision(id: id, select: select))!, item, id);
      }
      final rows = await client.precisionRecords(select: select);
      if (rows.length != (modeIndex + 1) * cases.length)
        throw StateError('stored list count');
      for (final row in rows) {
        verify(row, cases[row.id.value.toInt() % 10 - 1], row.id.value);
      }
      var rejected = false;
      try {
        await client.storePrecision(
          id: LuxoInt64.fromInt(999999),
          whole: LuxoInt64.fromInt(1),
          ratio: 1,
          amount: '1',
          moment: cases.first['moment']! as String,
          elapsed: LuxoInt64.fromInt(1),
          token: cases.first['token']! as String,
          select: select,
        );
      } on LuxoError {
        rejected = true;
      }
      if (!rejected ||
          await client.readPrecision(
                id: LuxoInt64.fromInt(999999),
                select: select,
              ) !=
              null) {
        throw StateError('inexact Duration persisted');
      }
      print(
        'Dart $modeIndex: six exact persistent records, list and rejected Duration passed',
      );
    }
  } finally {
    client.close();
  }
}

final nullableCases = <(LuxoInt64?, List<LuxoInt64>?)>[
  (null, null),
  (LuxoInt64.zero, []),
  (
    LuxoInt64.maxValue,
    [
      LuxoInt64.minValue,
      LuxoInt64.parse('9007199254740993'),
      LuxoInt64.maxValue,
    ],
  ),
];
void verifyNullable(NullableRecord row) {
  final expected = nullableCases[row.id.value.toInt() % 10 - 1];
  if (row.number.value != expected.$1 ||
      encodeLuxoJSON(row.items.value) != encodeLuxoJSON(expected.$2)) {
    throw StateError('nullable persisted state changed');
  }
}

Future<void> verifyNullableStorage(LuxoClient client) async {
  const fields = 'id,number,items';
  for (var modeIndex = 0; modeIndex < 2; modeIndex++) {
    client.setMode(modeIndex == 0 ? TransportMode.json : TransportMode.binary);
    for (var index = 0; index < nullableCases.length; index++) {
      final id = LuxoInt64.fromInt(modeIndex * 10 + index + 1);
      final item = nullableCases[index];
      verifyNullable(
        await client.storeNullableRecord(
          id: id,
          number: item.$1,
          items: item.$2,
          select: fields,
        ),
      );
      final row = (await client.readNullableRecord(id: id, select: fields))!;
      if (row.id.value != id) throw StateError('wrong persisted ID');
      verifyNullable(row);
    }
    final rows = await client.nullableRecords(select: fields);
    if (rows.length != (modeIndex + 1) * nullableCases.length)
      throw StateError('nullable row count');
    for (final row in rows) {
      verifyNullable(row);
    }
    if (await client.readNullableRecord(
          id: LuxoInt64.fromInt(999999),
          select: fields,
        ) !=
        null)
      throw StateError('missing row became present');
    print(
      'Dart $modeIndex: nullable zero, null, empty/list persistence passed',
    );
  }
}
