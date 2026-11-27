import assert from 'node:assert/strict'
const format = process.argv[2]
if (process.env.LUXO_SQL_INFERRED === '1') {
 const { createClient, verifyInferred } = await import(`./${format}/out/inferred.${format === 'cjs' ? 'cjs' : 'mjs'}`)
 const client = createClient(process.argv[3])
 try {
  await verifyInferred(client)
  const inferredCalls = process.env.LUXO_SQL_RELATIONS === '1' ? 18 : 32
  console.log(JSON.stringify({ format, inferredCalls, modes: ['json', 'binary'], passed: true }))
 } finally { client.close() }
} else {
const { LuxoClient } = await import(`./${format}/out/client.js`)
const { prepareSelection } = await import(`./${format}/node_modules/@luxojs/client/dist/index.${format === 'esm' ? 'mjs' : 'js'}`)
const metrics = []
const client = LuxoClient.create(process.argv[3], { mode: 'binary', observer: event => metrics.push(event) })
function narrow(items, count = 128) {
 assert.equal(items.length, count)
 assert.equal(new Set(items.map(item => item.name)).size, count)
 for (const item of items) {
  assert.match(item.name, /^item-\d+$/)
  assert.equal(item.id, undefined)
  assert.equal(item.price, undefined)
  assert.equal(item.description, undefined)
  const copied = { ...item }
  assert.equal(Object.hasOwn(item, 'name'), true)
  assert.equal(Object.hasOwn(copied, 'name'), true)
  assert.equal(copied.name, item.name)
  for (const field of ['id', 'price', 'description']) {
   assert.equal(Object.hasOwn(item, field), false, `unselected ${field} became an own property`)
   assert.equal(Object.hasOwn(copied, field), false, `spread copied unselected ${field}`)
  }
  const applied = { id: 0n, price: 0n, description: '', ...item }
  assert.equal(applied.id, 0n)
  assert.equal(applied.price, 0n)
  assert.equal(applied.description, '')
 }
}
try {
 const plan = prepareSelection('name')
 narrow(await client.products({ $select: plan }))
 narrow(await client.selectedProducts({ $select: plan }))
 const full = await client.selectedProducts({ $select: 'id,name,price,description' })
 assert.equal(full.length, 128)
 for (const item of full) {
  assert.equal(item.id, 9007199254740992n + item.price)
  assert.equal(item.name, `item-${item.price}`)
  assert.equal(item.description, 'x'.repeat(4096))
 }
 const page = await client.listProducts({ pageSize: 64n, $select: plan })
 narrow(page.items, 64)
 assert.equal(page.total, 128n)
 assert.equal(page.page, 1n)
 assert.equal(page.pageSize, 64n)
 const second = await client.listProducts({ page: 2n, pageSize: 64n, $select: plan })
 narrow(second.items, 64)
 assert.equal(second.page, 2n)
 assert.equal(second.total, 128n)
 narrow([...page.items, ...second.items])
 console.log(JSON.stringify({ format, metrics, passed: true }))
} finally { client.close() }
}
