import { describe, expect, test } from 'vitest'
import { createRequire } from 'node:module'
import { join } from 'node:path'

// Use the same native modules as the production build, without Vite export getters.
const compiled = process.env.LUXO_BENCH_MODULES
if (!compiled) throw new Error('Compile CommonJS modules under .tmp and set LUXO_BENCH_MODULES')
const requireCompiled = createRequire(import.meta.url)
const { JSONParamValidator } = requireCompiled(join(compiled, 'json-params.js')) as typeof import('./json-params')
const { encodeLuxoJSON } = requireCompiled(join(compiled, 'json.js')) as typeof import('./json')

const validator = new JSONParamValidator()
validator.setSchema({ echo: { id: 1, params: [
  { name: 'value', fieldID: 1, type: 'Int' },
  { name: 'items', fieldID: 2, type: 'Model', typeName: 'Item', isList: true },
], types: { Item: {
  value: { fieldID: 1, type: 'Int' },
  duration: { fieldID: 2, type: 'Duration', nullable: true },
  label: { fieldID: 3, type: 'String' },
} } } })

for (const [name, params] of [
  ['scalar', { $api: 'echo', value: 9223372036854775807n }],
  ['nested list', { $api: 'echo', items: Array.from({ length: 16 }, (_, index) => ({
    value: BigInt(index), duration: -9223372036854775808n, label: 'task',
  })) }],
] as const) {
  describe(name, () => {
    test('JSON encoding validation comparison', async ({ bench }) => {
      await bench.compare(
        bench('JSON encoding without integer validation (baseline)', () => encodeLuxoJSON(params)),
        bench('compiled integer validation + JSON encoding', () => {
          validator.validate('echo', params)
          return encodeLuxoJSON(params)
        }),
        { time: 500 },
      )
    })
    test('compiled integer validation only', async ({ bench }) => {
      await bench('compiled integer validation only', () => validator.validate('echo', params)).run({ time: 500 })
    })
    test('Bytes-free prepared request', async ({ bench }) => {
      expect(validator.prepare('echo', params)).toBe(params)
      const result = await bench.compare(
        bench('validate and encode', () => batch(() => {
          validator.validate('echo', params)
          return encodeLuxoJSON(params).length
        })),
        bench('prepare and encode', () => batch(() => encodeLuxoJSON(validator.prepare('echo', params)).length)),
        { time: 500 },
      )
      console.log(JSON.stringify({ name, comparison: 'same integer plan; no Bytes in schema',
        beforeMedianNs: result.get('validate and encode').latency.p50 * 10000,
        afterMedianNs: result.get('prepare and encode').latency.p50 * 10000,
        beforeRME: result.get('validate and encode').latency.rme,
        afterRME: result.get('prepare and encode').latency.rme,
      }))
    })
  })
}

function batch(operation: () => number): number {
  let sink = 0
  for (let index = 0; index < 100; index++) sink += operation()
  return sink
}
