import { describe, test } from 'vitest'
import { analyzeAndTransform } from './analyzer'
import { correctedCases, equivalentCases, errorCases, schema } from '../benchmarks/analyzer-cases'

describe('selection analysis: equivalent outputs', () => {
  for (const [name, source] of Object.entries(equivalentCases)) {
    test(name, async ({ bench }) => {
      await bench(name, () => { analyzeAndTransform(source, 'benchmark.ts', schema) }).run()
    })
  }
})

describe('selection analysis: corrected behavior, no old/new speed ratio', () => {
  for (const [name, source] of Object.entries(correctedCases)) {
    test(name, async ({ bench }) => {
      await bench(name, () => { analyzeAndTransform(source, 'benchmark.ts', schema) }).run()
    })
  }
})

describe('selection analysis: syntax diagnostics', () => {
  for (const [name, source] of Object.entries(errorCases)) {
    test(name, async ({ bench }) => {
      await bench(name, () => {
        try { analyzeAndTransform(source, 'benchmark.ts', schema) }
        catch (error) {
          if (error instanceof SyntaxError) return error.name
          throw error
        }
        throw new Error('Expected a syntax diagnostic')
      }).run()
    })
  }
})
