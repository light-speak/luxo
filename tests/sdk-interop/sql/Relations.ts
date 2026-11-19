import { LuxoClient } from './generated/client'

export function createClient(endpoint: string) { return LuxoClient.create(endpoint) }

function same(actual: readonly (string | undefined)[], expected: readonly string[]) {
  if (actual.join('\n') !== expected.join('\n')) {
    throw new Error(`relationship values differ: ${JSON.stringify(actual)}`)
  }
}

const articleViews = [
  'article-1:alice', 'article-2:alice', 'article-3:bob',
  'article-4:-', 'article-5:zero', 'article-6:-',
]

export async function verifyInferred(client: LuxoClient) {
  for (const mode of ['json', 'binary'] as const) {
    client.setMode(mode)
    const rows = await client.articleRows()
    same(rows.map(row => `${row.title}:${row.author?.name ?? '-'}`), articleViews)
    const people = await client.peopleRows()
    same(people.map(person => `${person.name}:${person.articles?.map(row => row.title).sort().join('|') ?? '<unselected>'}`), [
      'zero:article-5', 'alice:article-1|article-2', 'bob:article-3', 'empty:',
    ])
    for (let number = 1; number <= 4; number++) {
      const page = await client.articlePage({ page: BigInt(number), pageSize: 2n })
      if (page.total !== 6n || page.page !== BigInt(number) || page.pageSize !== 2n) {
        throw new Error('automatic page metadata differs')
      }
      same(page.items.map(row => `${row.title}:${row.author?.name ?? '-'}`), articleViews.slice((number - 1) * 2, number * 2))
    }
    const boxed = await client.boxedArticlePage({ page: 2n, pageSize: 2n })
    if (boxed.total !== 6n || boxed.page !== 2n || boxed.pageSize !== 2n) {
      throw new Error('boxed page metadata differs')
    }
    same(boxed.items.map(row => `${row.title}:${row.author?.name ?? '-'}`), articleViews.slice(2, 4))
    const explicit = await client.articlePage({ pageSize: 2n, $select: 'title' })
    if (explicit.total !== 6n || explicit.page !== 1n || explicit.pageSize !== 2n) {
      throw new Error('explicit page metadata differs')
    }
    same(explicit.items.map(row => row.title), ['article-1', 'article-2'])
    const names = await client.peopleRows({ $select: 'name' })
    same(names.map(person => person.name), ['zero', 'alice', 'bob', 'empty'])
  }
}
