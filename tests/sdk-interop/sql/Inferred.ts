import { LuxoClient } from './generated/client'

export function createClient(endpoint: string) { return LuxoClient.create(endpoint) }

function verifyNames(values: readonly string[]) {
  const actual = [...values].sort()
  const expected = Array.from({ length: 128 }, (_, index) => `item-${index + 1}`).sort()
  if (actual.length !== expected.length || actual.some((value, index) => value !== expected[index])) {
    throw new Error('inferred result names differ')
  }
}

async function filtered(client: LuxoClient) {
  const rows = await client.selectedProducts()
  return rows.filter((...args) => args[0].price > 0n).map(row => row.name)
}

async function first(client: LuxoClient) {
  const rows = await client.selectedProducts()
  return rows.find(row => row.price === 1n)?.name
}

async function aliased(client: LuxoClient) {
  const rows = await client.selectedProducts()
  const active = rows.filter(row => row.price > 0n)
  return active[0]!.name
}

async function dictionary(client: LuxoClient) {
  const rows = await client.selectedProducts()
  const packed = { local: 0, first: rows['0']! }
  const { local, ...rest } = packed
  return rest['first'].name
}

async function record(client: LuxoClient) {
  const rows = await client.selectedProducts()
  const packed = [0, rows[128]] as const
  const [, ...rest] = packed
  const [first = rows[0]!] = rest
  return first.name
}

export async function verifyInferred(client: LuxoClient) {
  for (const mode of ['json', 'binary'] as const) {
    client.setMode(mode)
    verifyNames(await filtered(client))
    if (await first(client) !== 'item-1' || await aliased(client) !== 'item-1' ||
        await dictionary(client) !== 'item-1' || await record(client) !== 'item-1') {
      throw new Error('inferred first result differs')
    }
    // Keep static call sites distinct so the build must infer each API independently.
    const priced = await client.pricedProducts()
    verifyNames(priced.map(row => row.name))
    const privateRows = await client.privateProducts()
    verifyNames(privateRows.map(row => row.name))
    const called = await client.calledProducts()
    verifyNames(called.map(row => row.name))
    const calledPrivate = await client.calledPrivateProducts()
    verifyNames(calledPrivate.map(row => row.name))
    const nested = await client.nestedProducts()
    verifyNames(nested.map(row => row.name))
    const own = await client.ownProducts()
    verifyNames(own.map(row => row.name))
    const ownPrivate = await client.ownPrivateProducts()
    const combined = await client.combinedProducts()
    const { rows: selected = combined } = { rows: ownPrivate }
    const { rows: alternate = ownPrivate } = { rows: combined }
    verifyNames(selected.map(row => row.name))
    verifyNames(alternate.map(row => row.name))
    const forwarded = await client.forwardedProducts()
    verifyNames(forwarded.map(row => row.name))
    verifyNames(await client.scalarCalledProducts())
    const aliasedOwn = await client.aliasedOwnProducts()
    verifyNames(aliasedOwn.map(row => row.name))
  }
}
