export type QueryKey = readonly unknown[]

export interface QueryFunctionContext {
  signal: AbortSignal
}

export type QueryFunction<T> = (context: QueryFunctionContext) => Promise<T>

export interface QueryClientDefaults {
  staleTime?: number
  gcTime?: number
}

export interface FetchQueryOptions<T> {
  queryKey: QueryKey
  queryFn: QueryFunction<T>
  staleTime?: number
  gcTime?: number
  force?: boolean
}

export interface QueryFilters {
  queryKey?: QueryKey
  exact?: boolean
}

export interface QueryEvent {
  type: 'updated' | 'error' | 'invalidated' | 'cancelled' | 'removed'
}

export interface QueryState<T> {
  data: T | undefined
  error: Error | undefined
  hasData: boolean
  isFetching: boolean
  isInvalidated: boolean
}

export type QueryUpdater<T> = T | ((current: T | undefined) => T)
export type QueryListener = (event: QueryEvent) => void

interface QueryIdentity {
  hash: string
  key: QueryKey
  segments: string[]
}

interface QueryEntry {
  identity: QueryIdentity
  data: unknown
  error: Error | undefined
  hasData: boolean
  invalidated: boolean
  updatedAt: number
  promise: Promise<unknown> | undefined
  controller: AbortController | undefined
  revision: number
  gcTime: number
  gcTimer: ReturnType<typeof setTimeout> | undefined
  listeners: Set<QueryListener>
}

const DEFAULT_STALE_TIME = 0
const DEFAULT_GC_TIME = 0

function encodeNumber(value: number): string {
  if (Number.isNaN(value)) return 'number:NaN'
  if (value === Infinity) return 'number:Infinity'
  if (value === -Infinity) return 'number:-Infinity'
  if (Object.is(value, -0)) return 'number:-0'
  return `number:${value}`
}

function encodeObject(value: object, seen: Set<object>): string {
  if (seen.has(value)) throw new TypeError('query key must not contain circular values')
  seen.add(value)
  let encoded: string
  if (Array.isArray(value)) {
    encoded = `array:[${value.map(item => encodeQueryValue(item, seen)).join(',')}]`
  } else if (value instanceof Date) {
    encoded = `date:${value.toISOString()}`
  } else {
    const prototype = Object.getPrototypeOf(value)
    if (prototype !== Object.prototype && prototype !== null) {
      throw new TypeError('query key objects must be plain objects')
    }
    const record = value as Record<string, unknown>
    const fields = Object.keys(record).sort().map(key => {
      return `${encodeQueryValue(key, seen)}=${encodeQueryValue(record[key], seen)}`
    })
    encoded = `object:{${fields.join(',')}}`
  }
  seen.delete(value)
  return encoded
}

function encodeQueryValue(value: unknown, seen: Set<object>): string {
  if (value === null) return 'null'
  switch (typeof value) {
    case 'undefined': return 'undefined'
    case 'boolean': return value ? 'boolean:1' : 'boolean:0'
    case 'number': return encodeNumber(value)
    case 'bigint': return `bigint:${value}`
    case 'string': return `string:${value.length}:${value}`
    case 'object': return encodeObject(value, seen)
    default: throw new TypeError(`unsupported query key value: ${typeof value}`)
  }
}

export function hashQueryKey(queryKey: QueryKey): string {
  return encodeObject(queryKey, new Set())
}

function createIdentity(queryKey: QueryKey): QueryIdentity {
  const segments = queryKey.map(value => encodeQueryValue(value, new Set()))
  return { hash: `query:[${segments.join(',')}]`, key: [...queryKey], segments }
}

function normalizeDuration(value: number | undefined, fallback: number, name: string): number {
  const duration = value ?? fallback
  if (duration < 0 || Number.isNaN(duration)) throw new RangeError(`${name} must be zero or greater`)
  return duration
}

function normalizeError(error: unknown): Error {
  return error instanceof Error ? error : new Error(String(error))
}

export class QueryClient {
  private readonly entries = new Map<string, QueryEntry>()
  private readonly staleTime: number
  private readonly gcTime: number

  constructor(defaults: QueryClientDefaults = {}) {
    this.staleTime = normalizeDuration(defaults.staleTime, DEFAULT_STALE_TIME, 'staleTime')
    this.gcTime = normalizeDuration(defaults.gcTime, DEFAULT_GC_TIME, 'gcTime')
  }

  fetchQuery<T>(options: FetchQueryOptions<T>): Promise<T> {
    const staleTime = normalizeDuration(options.staleTime, this.staleTime, 'staleTime')
    const gcTime = normalizeDuration(options.gcTime, this.gcTime, 'gcTime')
    const entry = this.getOrCreate(options.queryKey, gcTime)
    entry.gcTime = gcTime
    this.clearGcTimer(entry)
    if (entry.promise) return entry.promise as Promise<T>
    if (!options.force && this.isFresh(entry, staleTime)) {
      this.scheduleGc(entry)
      return Promise.resolve(entry.data as T)
    }
    return this.startQuery(entry, options.queryFn)
  }

  async prefetchQuery<T>(options: FetchQueryOptions<T>): Promise<void> {
    try {
      await this.fetchQuery(options)
    } catch {
      // Prefetch errors are available through getQueryState and never escape.
    }
  }

  getQueryData<T>(queryKey: QueryKey): T | undefined {
    const entry = this.entries.get(createIdentity(queryKey).hash)
    return entry?.hasData ? entry.data as T : undefined
  }

  getQueryState<T>(queryKey: QueryKey): QueryState<T> {
    const entry = this.entries.get(createIdentity(queryKey).hash)
    return {
      data: entry?.hasData ? entry.data as T : undefined,
      error: entry?.error,
      hasData: entry?.hasData ?? false,
      isFetching: entry?.promise !== undefined,
      isInvalidated: entry?.invalidated ?? false,
    }
  }

  setQueryData<T>(queryKey: QueryKey, updater: QueryUpdater<T>): T {
    const entry = this.getOrCreate(queryKey, this.gcTime)
    const current = entry.hasData ? entry.data as T : undefined
    const data = typeof updater === 'function'
      ? (updater as (value: T | undefined) => T)(current)
      : updater
    entry.data = data
    entry.error = undefined
    entry.hasData = true
    entry.invalidated = false
    entry.updatedAt = Date.now()
    this.notify(entry, { type: 'updated' })
    this.scheduleGc(entry)
    return data
  }

  subscribe(queryKey: QueryKey, listener: QueryListener): () => void {
    const entry = this.getOrCreate(queryKey, this.gcTime)
    this.clearGcTimer(entry)
    entry.listeners.add(listener)
    return () => {
      if (!entry.listeners.delete(listener)) return
      this.scheduleGc(entry)
    }
  }

  invalidateQueries(filters: QueryFilters = {}): void {
    for (const entry of this.matchingEntries(filters)) {
      entry.invalidated = true
      this.notify(entry, { type: 'invalidated' })
    }
  }

  cancelQueries(filters: QueryFilters = {}): void {
    for (const entry of this.matchingEntries(filters)) this.cancelEntry(entry)
  }

  removeQueries(filters: QueryFilters = {}): void {
    for (const entry of this.matchingEntries(filters)) {
      this.cancelEntry(entry)
      entry.data = undefined
      entry.error = undefined
      entry.hasData = false
      entry.invalidated = true
      entry.updatedAt = 0
      this.notify(entry, { type: 'removed' })
      this.clearGcTimer(entry)
      if (entry.listeners.size === 0) this.entries.delete(entry.identity.hash)
    }
  }

  clear(): void {
    this.removeQueries()
  }

  private getOrCreate(queryKey: QueryKey, gcTime: number): QueryEntry {
    const identity = createIdentity(queryKey)
    const existing = this.entries.get(identity.hash)
    if (existing) return existing
    const entry: QueryEntry = {
      identity,
      data: undefined,
      error: undefined,
      hasData: false,
      invalidated: false,
      updatedAt: 0,
      promise: undefined,
      controller: undefined,
      revision: 0,
      gcTime,
      gcTimer: undefined,
      listeners: new Set(),
    }
    this.entries.set(identity.hash, entry)
    return entry
  }

  private startQuery<T>(entry: QueryEntry, queryFn: QueryFunction<T>): Promise<T> {
    const controller = new AbortController()
    const revision = ++entry.revision
    entry.controller = controller
    entry.error = undefined
    let source: Promise<T>
    try {
      source = Promise.resolve(queryFn({ signal: controller.signal }))
    } catch (error) {
      source = Promise.reject(error)
    }
    let promise: Promise<T>
    promise = source.then(
      data => this.completeQuery(entry, revision, data, promise),
      error => {
        this.failQuery(entry, revision, error, promise)
        throw error
      },
    )
    entry.promise = promise
    return promise
  }

  private completeQuery<T>(entry: QueryEntry, revision: number, data: T, promise: Promise<T>): T {
    if (entry.revision !== revision || entry.promise !== promise) return data
    entry.data = data
    entry.error = undefined
    entry.hasData = true
    entry.invalidated = false
    entry.updatedAt = Date.now()
    entry.promise = undefined
    entry.controller = undefined
    this.notify(entry, { type: 'updated' })
    this.scheduleGc(entry)
    return data
  }

  private failQuery(entry: QueryEntry, revision: number, error: unknown, promise: Promise<unknown>): void {
    if (entry.revision !== revision || entry.promise !== promise) return
    entry.error = normalizeError(error)
    entry.promise = undefined
    entry.controller = undefined
    this.notify(entry, { type: 'error' })
    this.scheduleGc(entry)
  }

  private cancelEntry(entry: QueryEntry): void {
    if (!entry.promise) return
    entry.controller?.abort()
    entry.revision++
    entry.promise = undefined
    entry.controller = undefined
    this.notify(entry, { type: 'cancelled' })
    this.scheduleGc(entry)
  }

  private isFresh(entry: QueryEntry, staleTime: number): boolean {
    if (!entry.hasData || entry.invalidated || staleTime === 0) return false
    return staleTime === Infinity || Date.now() - entry.updatedAt < staleTime
  }

  private matchingEntries(filters: QueryFilters): QueryEntry[] {
    if (!filters.queryKey) return [...this.entries.values()]
    const identity = createIdentity(filters.queryKey)
    return [...this.entries.values()].filter(entry => {
      if (filters.exact) return entry.identity.hash === identity.hash
      if (identity.segments.length > entry.identity.segments.length) return false
      return identity.segments.every((segment, index) => entry.identity.segments[index] === segment)
    })
  }

  private notify(entry: QueryEntry, event: QueryEvent): void {
    for (const listener of [...entry.listeners]) {
      try { listener(event) } catch { /* Observers must not break query state. */ }
    }
  }

  private clearGcTimer(entry: QueryEntry): void {
    if (entry.gcTimer === undefined) return
    clearTimeout(entry.gcTimer)
    entry.gcTimer = undefined
  }

  private scheduleGc(entry: QueryEntry): void {
    this.clearGcTimer(entry)
    if (entry.listeners.size > 0 || entry.gcTime === Infinity) return
    if (entry.gcTime > 0) {
      entry.gcTimer = setTimeout(() => this.collectEntry(entry), entry.gcTime)
      return
    }
    queueMicrotask(() => this.collectEntry(entry))
  }

  private collectEntry(entry: QueryEntry): void {
    if (entry.listeners.size > 0 || this.entries.get(entry.identity.hash) !== entry) return
    this.cancelEntry(entry)
    if (entry.promise) return
    this.entries.delete(entry.identity.hash)
  }
}
