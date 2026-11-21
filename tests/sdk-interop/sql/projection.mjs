import assert from 'node:assert/strict'
import { writeFileSync } from 'node:fs'
import { join } from 'node:path'

const format = process.argv[2]
const { LuxoClient } = await import(`./${format}/out/client.js`)
const { prepareSelection } = await import(`./${format}/node_modules/@luxojs/client/dist/index.${format === 'esm' ? 'mjs' : 'js'}`)
const client = LuxoClient.create(process.argv[3], { mode: 'binary' })
const plan = prepareSelection('name')
const expected = Array.from({ length: 128 }, (_, i) => `item-${i + 1}`).sort()
const paths = {
  fullSQL: () => client.products({ $select: plan }),
  selectedSQL: () => client.selectedProducts({ $select: plan }),
}
function verify(items) {
  assert.equal(items.length, 128)
  assert.deepEqual(items.map(item => item.name).sort(), expected)
  for (const item of items) {
    assert.equal(item.id, undefined)
    assert.equal(item.price, undefined)
    assert.equal(item.description, undefined)
  }
}
function summarize(samples) {
  const sorted = [...samples].sort((a, b) => a - b)
  const at = fraction => sorted[Math.ceil(sorted.length * fraction) - 1]
  return { p50Us: at(0.5), p95Us: at(0.95), p99Us: at(0.99),
    meanUs: samples.reduce((a, b) => a + b, 0) / samples.length }
}
const batches = []
try {
  for (let i = 0; i < 20; i++) {
    for (const call of Object.values(paths)) verify(await call())
  }
  for (let batch = 0; batch < 3; batch++) {
    const samples = { fullSQL: [], selectedSQL: [] }
    for (let i = 0; i < 50; i++) {
      const order = (batch + i) % 2 ? ['selectedSQL', 'fullSQL'] : ['fullSQL', 'selectedSQL']
      for (const name of order) {
        const start = process.hrtime.bigint()
        const items = await paths[name]()
        samples[name].push(Number(process.hrtime.bigint() - start) / 1000)
        verify(items)
      }
    }
    batches.push({ samples, summary: Object.fromEntries(
      Object.entries(samples).map(([name, values]) => [name, summarize(values)])) })
  }
  const evidence = { format, node: process.version, concurrency: 1, rows: 128,
    descriptionBytesPerRow: 4096, selectedFields: ['name'], batches,
    scope: 'Local paired SDK/TLS H2/PG calls; includes decoding, excludes assertions. No CPU or allocation claim.' }
  writeFileSync(join(process.env.LUXO_SQL_SCRATCH, `projection-${format}.json`), JSON.stringify(evidence, null, 2))
  console.log(JSON.stringify({ format, summaries: batches.map(batch => batch.summary), passed: true }))
} finally { client.close() }
