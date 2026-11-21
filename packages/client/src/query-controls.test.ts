import { describe, expect, it } from 'vitest'
import { Encoder } from './codec'
import { encodeBinaryBody } from './transport'
import { writeFilters, writeSorters, writeDynamicFilters, writeDynamicSorters } from './query-controls'
import type { Filter, Sorter } from './types'

describe('typed query controls', () => {
  it('preserves every operator and scalar wire representation', () => {
    const operators = ['eq', 'ne', 'gt', 'gte', 'lt', 'lte', 'contains', 'startswith', 'endswith', 'match'] as const
    const filters: Filter[] = operators.map(op => ({ field: 'name', op, value: '中文' }))
    filters.push({ field: 'id', op: 'eq', value: 9007199254740993n },
      { field: 'active', op: 'eq', value: false }, { field: 'score', op: 'gt', value: 1.25 })
    const sorters: Sorter[] = [{ field: 'id', order: 'desc' }, { field: 'name', order: 'asc' }]
    const encoder = new Encoder()
    encoder.writeVarint(7)
    encoder.writeVarint(0)
    writeFilters(encoder, filters)
    writeSorters(encoder, sorters)
    encoder.writeEnd()
    expect(encoder.bytes()).toEqual(encodeBinaryBody({ id: 7 }, { $filters: filters, $sorters: sorters }))
  })

  it('reads each typed attribute once', () => {
    const reads = { field: 0, op: 0, value: 0 }
    const filter: Filter = {
      get field() { reads.field++; return 'id' },
      get op(): Filter['op'] { reads.op++; return 'eq' },
      get value() { reads.value++; return 42n },
    }
    writeFilters(new Encoder(), [filter])
    expect(reads).toEqual({ field: 1, op: 1, value: 1 })
  })

  it('rejects invalid dynamic entries before accepting inherited operator names', () => {
    for (const op of ['constructor', '__proto__', 'toString', '', 1]) {
      expect(() => writeDynamicFilters(new Encoder(), [{ field: 'id', op, value: 1 }])).toThrow('invalid $filters')
    }
    for (const value of [null, {}, [null], [undefined], new Array(1), [{ field: 'x', op: 'eq', value: NaN }]]) {
      expect(() => writeDynamicFilters(new Encoder(), value)).toThrow()
    }
    for (const value of [null, {}, [null], new Array(1), [{ field: '', order: 'asc' }], [{ field: 'x', order: 'ASC' }]]) {
      expect(() => writeDynamicSorters(new Encoder(), value)).toThrow()
    }
  })

  it('retains limits on both entry points', () => {
    const filters: Filter[] = Array.from({ length: 1001 }, () => ({ field: 'id', op: 'eq', value: 1n }))
    const sorters: Sorter[] = Array.from({ length: 101 }, () => ({ field: 'id', order: 'asc' }))
    expect(() => writeFilters(new Encoder(), filters)).toThrow('1000')
    expect(() => writeDynamicFilters(new Encoder(), filters)).toThrow('1000')
    expect(() => writeSorters(new Encoder(), sorters)).toThrow('100')
    expect(() => writeDynamicSorters(new Encoder(), sorters)).toThrow('100')
  })
})
