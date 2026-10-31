import { Encoder } from './codec'
import { LuxoError } from './error'
import { measure } from './measure.bench'
import { writeFilters, writeSorters } from './query-controls'
import type { Filter, Sorter } from './types'

// Frozen pre-split implementation, used only as the local benchmark baseline.
const operators: Record<string, number> = {
  eq: 1, ne: 2, gt: 3, gte: 4, lt: 5, lte: 6, contains: 7, startswith: 8, endswith: 9, match: 10,
}
function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}
function isFilterValue(value: unknown): value is string | number | bigint | boolean {
  return typeof value === 'string' || typeof value === 'boolean' || typeof value === 'bigint' ||
    (typeof value === 'number' && Number.isFinite(value))
}
function oldFilters(enc: Encoder, value: unknown): void {
  if (!Array.isArray(value) || value.length > 1000) {
    throw new LuxoError('ConfigError', 0, '$filters must be an array with at most 1000 entries')
  }
  enc.writeVarint(0x7ffffffe)
  enc.writeVarint(value.length)
  for (const [index, item] of value.entries()) {
    if (!isRecord(item) || typeof item.field !== 'string' || item.field === '' ||
        typeof item.op !== 'string' || !operators[item.op] || !isFilterValue(item.value)) {
      throw new LuxoError('ConfigError', 0, `invalid $filters entry at index ${index}`)
    }
    enc.writeString(item.field)
    enc.writeVarint(operators[item.op])
    enc.writeString(String(item.value))
  }
}
function oldSorters(enc: Encoder, value: unknown): void {
  if (!Array.isArray(value) || value.length > 100) {
    throw new LuxoError('ConfigError', 0, '$sorters must be an array with at most 100 entries')
  }
  enc.writeVarint(0x7fffffff)
  enc.writeVarint(value.length)
  for (const [index, item] of value.entries()) {
    if (!isRecord(item) || typeof item.field !== 'string' || item.field === '' ||
        (item.order !== 'asc' && item.order !== 'desc')) {
      throw new LuxoError('ConfigError', 0, `invalid $sorters entry at index ${index}`)
    }
    enc.writeString(item.field)
    enc.writeBool(item.order === 'desc')
  }
}

for (const size of [0, 1, 16, 100]) {
  const filters: Filter[] = Array.from({ length: size }, (_, i) => ({
    field: `field${i}`, op: i % 2 ? 'contains' : 'eq', value: i % 2 ? '中文' : 9007199254740993n,
  }))
  const sorters: Sorter[] = Array.from({ length: size }, (_, i) => ({ field: `field${i}`, order: i % 2 ? 'asc' : 'desc' }))
  const before = new Encoder(), after = new Encoder()
  measure(`filters ${size}`, () => {
    before.reset(); oldFilters(before, filters); return before.bytes()
  }, () => {
    after.reset(); writeFilters(after, filters); return after.bytes()
  })
  measure(`sorters ${size}`, () => {
    before.reset(); oldSorters(before, sorters); return before.bytes()
  }, () => {
    after.reset(); writeSorters(after, sorters); return after.bytes()
  })
}
