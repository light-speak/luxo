import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
const format = process.argv[2]
const { LuxoClient } = await import(`./${format}/out/client.js`)
const select = 'id,whole,ratio,amount,moment,elapsed,token'
const cases = JSON.parse(readFileSync(process.env.LUXO_SQL_PRECISION_CORPUS, 'utf8'))
const base = format === 'esm' ? 100 : 0
const client = LuxoClient.create(process.argv[3])
function verify(row, item, id) {
 assert.equal(row.id, id)
 assert.equal(row.whole, BigInt(item.whole))
 assert.ok(Object.is(row.ratio, Number(item.ratio)), `float bits changed at ${id}`)
 assert.equal(row.amount, item.amount)
 assert.equal(row.moment, item.storedMoment)
 assert.equal(row.elapsed, BigInt(item.elapsed))
 assert.equal(row.token, item.token.toLowerCase())
}
const nullableCases = [
 { number: null, items: null }, { number: 0n, items: [] },
 { number: 9223372036854775807n, items: [-9223372036854775808n, 9007199254740993n, 9223372036854775807n] },
]
function verifyNullable(row) {
 const expected = nullableCases[Number(row.id % 10n) - 1]
 assert.equal(row.number, expected.number)
 assert.deepEqual(row.items, expected.items)
}
async function verifyNullableStorage() {
 for (const [modeIndex, mode] of ['json', 'binary'].entries()) {
  client.setMode(mode)
  for (const [index, item] of nullableCases.entries()) {
   const id = BigInt(base + modeIndex * 10 + index + 1)
   verifyNullable(await client.storeNullableRecord({ id, ...item, $select: 'id,number,items' }))
   const row = await client.readNullableRecord({ id, $select: 'id,number,items' })
   assert.equal(row.id, id)
   verifyNullable(row)
  }
  const rows = await client.nullableRecords({ $select: 'id,number,items' })
  assert.equal(rows.length, (base === 100 ? 6 : 0) + (modeIndex + 1) * nullableCases.length)
  for (const row of rows) verifyNullable(row)
  assert.equal(await client.readNullableRecord({ id: 999999n, $select: 'id,number,items' }), null)
  console.log(`${format} ${mode}: nullable zero, null, empty/list persistence passed`)
 }
}
try {
 if (process.env.LUXO_SQL_NULLABLE === '1') await verifyNullableStorage()
 else {
 for (const [modeIndex, mode] of ['json', 'binary'].entries()) {
  client.setMode(mode)
  for (const [index, item] of cases.entries()) {
   const id = BigInt(base + modeIndex * 10 + index + 1)
   const row = await client.storePrecision({ id, whole: BigInt(item.whole), ratio: Number(item.ratio), amount: item.amount, moment: item.moment, elapsed: BigInt(item.elapsed), token: item.token, $select: select })
   verify(row, item, id)
   verify(await client.readPrecision({ id, $select: select }), item, id)
  }
  const rows = await client.precisionRecords({ $select: select })
  assert.equal(rows.length, (base === 100 ? 12 : 0) + (modeIndex + 1) * cases.length)
  for (const row of rows) verify(row, cases[Number(row.id % 10n) - 1], row.id)
  const item = cases[0]
  await assert.rejects(client.storePrecision({ id: 999999n, whole: 1n, ratio: 1, amount: '1', moment: item.moment, elapsed: 1n, token: item.token, $select: select }))
  assert.equal(await client.readPrecision({ id: 999999n, $select: select }), null)
  console.log(`${format} ${mode}: six exact persistent records, list and rejected Duration passed`)
 }
 }
} finally { client.close() }
