import type { Encoder } from './codec'
import { LuxoError } from './error'
import type { Filter, Sorter } from './types'

const filterOperatorIDs: Readonly<Record<string, number>> = Object.assign(Object.create(null) as Record<string, number>, {
  eq: 1, ne: 2, gt: 3, gte: 4, lt: 5, lte: 6,
  contains: 7, startswith: 8, endswith: 9, match: 10,
})

function writeHeader(enc: Encoder, value: unknown, filters: boolean): void {
  const limit = filters ? 1000 : 100
  if (!Array.isArray(value) || value.length > limit) {
    throw new LuxoError('ConfigError', 0, `${filters ? '$filters' : '$sorters'} must be an array with at most ${limit} entries`)
  }
  enc.writeVarint(filters ? 0x7ffffffe : 0x7fffffff)
  enc.writeVarint(value.length)
}

function invalidEntry(kind: string, index: number): never {
  throw new LuxoError('ConfigError', 0, `invalid ${kind} entry at index ${index}`)
}

function writeFilter(enc: Encoder, field: unknown, op: unknown, value: unknown, index: number): void {
  const operator = typeof op === 'string' ? filterOperatorIDs[op] : undefined
  if (typeof field !== 'string' || field === '' || operator === undefined ||
      !(typeof value === 'string' || typeof value === 'boolean' || typeof value === 'bigint' ||
        (typeof value === 'number' && Number.isFinite(value)))) {
    invalidEntry('$filters', index)
  }
  enc.writeString(field)
  enc.writeVarint(operator)
  enc.writeString(String(value))
}

function writeSorter(enc: Encoder, field: unknown, order: unknown, index: number): void {
  if (typeof field !== 'string' || field === '' || (order !== 'asc' && order !== 'desc')) {
    invalidEntry('$sorters', index)
  }
  enc.writeString(field)
  enc.writeBool(order === 'desc')
}

/** Generated requests read typed properties directly, without generic record conversion. */
export function writeFilters(enc: Encoder, filters: readonly Filter[]): void {
  writeHeader(enc, filters, true)
  for (let index = 0; index < filters.length; index++) {
    const item = filters[index]
    if (item === null || typeof item !== 'object') invalidEntry('$filters', index)
    writeFilter(enc, item.field, item.op, item.value, index)
  }
}

export function writeSorters(enc: Encoder, sorters: readonly Sorter[]): void {
  writeHeader(enc, sorters, false)
  for (let index = 0; index < sorters.length; index++) {
    const item = sorters[index]
    if (item === null || typeof item !== 'object') invalidEntry('$sorters', index)
    writeSorter(enc, item.field, item.order, index)
  }
}

/** Studio and handwritten dynamic calls retain an explicitly checked boundary. */
export function writeDynamicFilters(enc: Encoder, filters: unknown): void {
  writeHeader(enc, filters, true)
  const entries = filters as readonly unknown[]
  for (let index = 0; index < entries.length; index++) {
    const item = record(entries[index], '$filters', index)
    writeFilter(enc, item.field, item.op, item.value, index)
  }
}

export function writeDynamicSorters(enc: Encoder, sorters: unknown): void {
  writeHeader(enc, sorters, false)
  const entries = sorters as readonly unknown[]
  for (let index = 0; index < entries.length; index++) {
    const item = record(entries[index], '$sorters', index)
    writeSorter(enc, item.field, item.order, index)
  }
}

function record(value: unknown, kind: string, index: number): Record<string, unknown> {
  if (typeof value !== 'object' || value === null) invalidEntry(kind, index)
  return value as Record<string, unknown>
}
