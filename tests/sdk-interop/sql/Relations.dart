import 'dart:io';

import 'package:luxo_client/http2.dart';
import 'package:luxo_client/luxo_client.dart';

import 'generated/client.dart';

const articleViews = [
  'article-1:alice',
  'article-2:alice',
  'article-3:bob',
  'article-4:-',
  'article-5:zero',
  'article-6:-',
];

void same(List<String> actual, List<String> expected) {
  if (actual.length != expected.length) {
    throw StateError('relationship row count differs: $actual');
  }
  for (var index = 0; index < actual.length; index++) {
    if (actual[index] != expected[index]) {
      throw StateError('relationship values differ: $actual');
    }
  }
}

void verifyPageMetadata(
  LuxoInt64 total,
  LuxoInt64 page,
  LuxoInt64 pageSize,
  int expectedPage,
) {
  if (total != LuxoInt64.fromInt(6) ||
      page != LuxoInt64.fromInt(expectedPage) ||
      pageSize != LuxoInt64.fromInt(2)) {
    throw StateError('page metadata differs');
  }
}

Future<void> verifyRowsAndPeople(LuxoClient client) async {
  final rows = await client.articleRows();
  same(
    rows
        .map(
          (row) => '${row.title.value}:${row.author.value?.name.value ?? '-'}',
        )
        .toList(),
    articleViews,
  );
  final people = await client.peopleRows();
  same(
    people.map((person) {
      final titles =
          person.articles.value.map((row) => row.title.value).toList()..sort();
      return '${person.name.value}:${titles.join('|')}';
    }).toList(),
    ['zero:article-5', 'alice:article-1|article-2', 'bob:article-3', 'empty:'],
  );
}

Future<void> verifyPages(LuxoClient client) async {
  for (var number = 1; number <= 4; number++) {
    final page = await client.articlePage(
      page: LuxoInt64.fromInt(number),
      pageSize: LuxoInt64.fromInt(2),
    );
    verifyPageMetadata(page.total, page.page, page.pageSize, number);
    same(
      page.items
          .map(
            (row) =>
                '${row.title.value}:${row.author.value?.name.value ?? '-'}',
          )
          .toList(),
      articleViews.skip((number - 1) * 2).take(2).toList(),
    );
  }
  final boxed = await client.boxedArticlePage(
    page: LuxoInt64.fromInt(2),
    pageSize: LuxoInt64.fromInt(2),
  );
  verifyPageMetadata(boxed.total, boxed.page, boxed.pageSize, 2);
  same(
    boxed.items
        .map(
          (row) => '${row.title.value}:${row.author.value?.name.value ?? '-'}',
        )
        .toList(),
    articleViews.skip(2).take(2).toList(),
  );
}

Future<void> verifyExplicitSelections(LuxoClient client) async {
  final explicit = await client.articlePage(
    pageSize: LuxoInt64.fromInt(2),
    select: 'title',
  );
  verifyPageMetadata(explicit.total, explicit.page, explicit.pageSize, 1);
  same(explicit.items.map((row) => row.title.value).toList(), [
    'article-1',
    'article-2',
  ]);
  final names = await client.peopleRows(select: 'name');
  same(names.map((person) => person.name.value).toList(), [
    'zero',
    'alice',
    'bob',
    'empty',
  ]);
}

Future<void> main(List<String> args) async {
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
    for (final mode in [TransportMode.json, TransportMode.binary]) {
      client.setMode(mode);
      await verifyRowsAndPeople(client);
      await verifyPages(client);
      await verifyExplicitSelections(client);
    }
    print(
      'Dart AOT: eighteen inferred/explicit relationship and pagination calls passed',
    );
  } finally {
    client.close();
  }
}
